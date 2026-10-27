package integration

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
	"github.com/danieldonoghue/govoip-dsp/resample"
)

// Inputs use testutil generators and integer arithmetic only, so they are
// identical on every platform and the harness hashes can check them.

// scenarioSeconds is the length of one repetition of the call scenario.
const scenarioSeconds = 40

// segment is a time window in seconds within one repetition.
type segment struct{ from, to float64 }

// The call scenario layout (docs/acceptance/integration.md, "Signals").
var (
	farOn  = []segment{{2, 14}, {24, 40}}
	nearOn = []segment{{14, 34}}
)

// tap is one nonzero Q15 coefficient of a room response.
type tap struct {
	off int
	v   int32
}

// room returns a synthetic echo path: a bulk delay, a dense 2 ms early part
// and 48 sparse reflections decaying linearly to tailMs. gain is the Q15
// peak tap.
func room(rate int, seed string, delayMs, tailMs int, gain int32) []tap {
	r := testutil.SplitMix64{State: testutil.Seed(seed)}
	d := rate * delayMs / 1000
	early := max(1, rate*2/1000)
	tail := max(d+early+1, rate*tailMs/1000)
	var taps []tap
	for k := range early {
		v := gain * int32(early-k) / int32(early)
		v = v * int32(50+r.Intn(51)) / 100
		if r.Intn(2) == 1 {
			v = -v
		}
		taps = append(taps, tap{d + k, v})
	}
	for range 48 {
		off := d + early + r.Intn(tail-d-early)
		v := gain / 3 * int32(tail-off) / int32(tail-d)
		v = v * int32(r.Intn(101)) / 100
		if r.Intn(2) == 1 {
			v = -v
		}
		taps = append(taps, tap{off, v})
	}
	return taps
}

func sat16(v int64) int16 { return int16(max(-32768, min(32767, v))) }

// convolve returns x filtered by h, rounded from Q15 and saturated.
func convolve(x []int16, h []tap) []int16 {
	y := make([]int16, len(x))
	for i := range y {
		var acc int64
		for _, t := range h {
			if j := i - t.off; j >= 0 {
				acc += int64(t.v) * int64(x[j])
			}
		}
		y[i] = sat16((acc + 16384) >> 15)
	}
	return y
}

// mix returns the saturated sum of xs over the length of the first.
func mix(xs ...[]int16) []int16 {
	y := make([]int16, len(xs[0]))
	for i := range y {
		var s int64
		for _, x := range xs {
			if i < len(x) {
				s += int64(x[i])
			}
		}
		y[i] = sat16(s)
	}
	return y
}

// speech returns n samples built from 2 s blocks of testutil.SpeechLike,
// each with its own seed, so a prefix does not depend on n.
func speech(rate, n int, amp int16, seed string) []int16 {
	block := 2 * rate
	x := make([]int16, 0, n+block)
	for b := 0; len(x) < n; b++ {
		x = append(x, testutil.SpeechLike(block, float64(rate), amp, testutil.Seed(fmt.Sprintf("%s/%d", seed, b)))...)
	}
	return x[:n]
}

// gate zeroes x outside segs, repeated every scenarioSeconds.
func gate(x []int16, rate int, segs []segment) []int16 {
	y := make([]int16, len(x))
	rep := scenarioSeconds * rate
	for base := 0; base < len(x); base += rep {
		for _, s := range segs {
			from := min(len(x), base+int(s.from*float64(rate)))
			to := min(len(x), base+int(s.to*float64(rate)))
			copy(y[from:to], x[from:to])
		}
	}
	return y
}

// call holds the inputs of a call scenario: far is what the far end plays,
// near the talker at the microphone, echo the far end through the echo
// path, and mic what the microphone hears.
type callSignals struct {
	rate                 int
	far, near, echo, mic []int16
}

// echoPath is the path of every scenario: 32 ms bulk delay, reflections to
// 80 ms, peak tap 0.5.
func echoPath(rate int) []tap { return room(rate, "integration/path", 32, 80, 16384) }

// callScenario returns reps repetitions of the 40 s call scenario at rate.
func callScenario(rate, reps int) callSignals {
	n := reps * scenarioSeconds * rate
	far := gate(speech(rate, n, 16000, "integration/far"), rate, farOn)
	near := gate(speech(rate, n, 3000, "integration/near"), rate, nearOn)
	echo := convolve(far, echoPath(rate))
	noise := testutil.WhiteNoise(n, 30, testutil.Seed("integration/noise"))
	return callSignals{rate: rate, far: far, near: near, echo: echo, mic: mix(echo, near, noise)}
}

// speechScenario is the call scenario with LibriSpeech utterances of one
// chapter as far end and of another chapter as near end, at rate (8 or 16
// kHz). The cache may hold a single speaker. It skips when the speech files
// are absent.
func speechScenario(t testing.TB, rate int) callSignals {
	paths := testutil.SpeechFiles(t)
	n16 := scenarioSeconds * testutil.SpeechRate
	chapter := func(p string) string { return strings.Join(strings.SplitN(filepath.Base(p), "-", 3)[:2], "-") }
	var far, near []int16
	farCh, nearCh := chapter(paths[0]), ""
	for _, p := range paths {
		ch := chapter(p)
		switch {
		case ch == farCh && len(far) < n16:
			far = append(far, testutil.LoadSpeech(t, p)...)
		case ch != farCh && (nearCh == "" || ch == nearCh) && len(near) < n16:
			nearCh = ch
			near = append(near, testutil.LoadSpeech(t, p)...)
		}
	}
	if len(far) < n16 || len(near) < n16 {
		t.Skipf("need %d s of speech from two chapters; found %d and %d samples", scenarioSeconds, len(far), len(near))
	}
	far, near = far[:n16], near[:n16]
	if rate != testutil.SpeechRate {
		far, near = resampleAll(t, far, testutil.SpeechRate, rate), resampleAll(t, near, testutil.SpeechRate, rate)
	}
	far = gate(far, rate, farOn)
	near = gate(testutil.Scale(near, 1, 4), rate, nearOn)
	echo := convolve(far, echoPath(rate))
	noise := testutil.WhiteNoise(len(far), 30, testutil.Seed("integration/noise"))
	return callSignals{rate: rate, far: far, near: near, echo: echo, mic: mix(echo, near, noise)}
}

// resampleAll converts x in one call, skipping the filter delay.
func resampleAll(t testing.TB, x []int16, in, out int) []int16 {
	r, err := resample.New(resample.Config{Channels: 1, InRate: in, OutRate: out, Quality: resample.QualityDesktop})
	if err != nil {
		t.Fatal(err)
	}
	r.SkipZeros()
	y := make([]int16, r.OutLen(len(x)))
	_, n, err := r.Process(x, y)
	if err != nil {
		t.Fatal(err)
	}
	return y[:n]
}

// dB returns 10*log10(num/den).
func dB(num, den float64) float64 { return 10 * math.Log10(num/den) }

// energy returns the sum of squares of x.
func energy(x []int16) float64 {
	var s float64
	for _, v := range x {
		s += float64(v) * float64(v)
	}
	return s
}

// window returns x over [from, to) seconds.
func window(x []int16, rate int, from, to float64) []int16 {
	return x[int(from*float64(rate)):min(len(x), int(to*float64(rate)))]
}

// activeDBFS returns the RMS in dBFS of the 20 ms frames of x whose RMS is
// at least -50 dBFS, and the number of such frames.
func activeDBFS(x []int16, rate int) (float64, int) {
	n := rate / 50
	var s float64
	var count int
	for i := 0; i+n <= len(x); i += n {
		f := x[i : i+n]
		if testutil.DBFS(testutil.RMS(f)) >= -50 {
			s += energy(f)
			count++
		}
	}
	if count == 0 {
		return math.Inf(-1), 0
	}
	return testutil.DBFS(math.Sqrt(s / float64(count*n))), count
}
