package echo

import (
	"fmt"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

// Scenario inputs (docs/acceptance/echo.md). Everything here is integer
// arithmetic or a platform-deterministic testutil generator, so the inputs
// are the same bits on every platform; the driver records them as streams.

// signals is one generated scenario. mic = sat(echo + near + noise); echo
// and near are kept for the measurements.
type signals struct {
	mic, far, near, echo []int16
}

// tap is one nonzero Q15 coefficient of a room response.
type tap struct {
	off int
	v   int32
}

func msToSamples(rate, ms int) int { return rate * ms / 1000 }

// speech is speech-like audio built from 2 s blocks of testutil.SpeechLike,
// so a prefix does not depend on the total length.
func speech(rate, n int, amp int16, seed string) []int16 {
	block := 2 * rate
	x := make([]int16, 0, n+block)
	for b := 0; len(x) < min(n, capLen(n)); b++ {
		x = append(x, testutil.SpeechLike(block, float64(rate), amp, testutil.Seed(fmt.Sprintf("%s/%d", seed, b)))...)
	}
	if len(x) < n {
		x = append(x, make([]int16, n-len(x))...)
	}
	return x[:n]
}

// genCap, when positive, limits the costly generators (speech and
// convolve) to their first genCap samples and leaves the rest zero. Both
// are prefix-safe, so the capped samples equal the uncapped ones
// (TestCappedInputsArePrefixes); partial reference runs use it.
var genCap int

func capLen(n int) int {
	if genCap > 0 {
		return min(n, genCap)
	}
	return n
}

// room returns a synthetic response: a bulk delay, a dense 2 ms early part
// and 48 sparse reflections decaying linearly to tailMs. gain is the Q15
// peak tap.
func room(rate int, seed string, delayMs, tailMs int, gain int32) []tap {
	r := testutil.SplitMix64{State: testutil.Seed(seed)}
	d := msToSamples(rate, delayMs)
	early := max(1, msToSamples(rate, 2))
	tail := max(d+early+1, msToSamples(rate, tailMs))
	var taps []tap
	for k := 0; k < early; k++ {
		v := gain * int32(early-k) / int32(early)
		v = v * int32(50+r.Intn(51)) / 100
		if r.Intn(2) == 1 {
			v = -v
		}
		taps = append(taps, tap{d + k, v})
	}
	for k := 0; k < 48; k++ {
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

// convolveAt returns the response output at sample i.
func convolveAt(x []int16, h []tap, i int) int64 {
	var acc int64
	for _, t := range h {
		if j := i - t.off; j >= 0 {
			acc += int64(t.v) * int64(x[j])
		}
	}
	return (acc + 16384) >> 15
}

func convolve(x []int16, h []tap) []int16 {
	y := make([]int16, len(x))
	for i := range y[:capLen(len(y))] {
		y[i] = sat16(convolveAt(x, h, i))
	}
	return y
}

// gate zeroes x outside the [from, to) sample ranges.
func gate(x []int16, ranges ...[2]int) []int16 {
	y := make([]int16, len(x))
	for _, r := range ranges {
		copy(y[r[0]:min(r[1], len(x))], x[r[0]:min(r[1], len(x))])
	}
	return y
}

// mix returns sat(a + b + c...) over the length of the first.
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

func delay(x []int16, d int) []int16 {
	y := make([]int16, len(x))
	if d < len(x) {
		copy(y[d:], x)
	}
	return y
}

const (
	farAmp  = 16000
	nearAmp = 12000
	noiseA  = 8
)

func pathA(rate int) []tap { return room(rate, "pathA", 4, 48, 16000) }
func pathB(rate int) []tap { return room(rate, "pathB", 8, 60, 14000) }

func sec(rate int, s float64) int { return int(float64(rate) * s) }

// scenario builds the named input set. filterMs matters only for
// misaligned_late and frame only for capture.
func scenario(name string, rate, filterMs, frame int) signals {
	n := func(s float64) int { return sec(rate, s) }
	noise := func(len int) []int16 { return testutil.WhiteNoise(len, noiseA, testutil.Seed(name+"/noise")) }
	echoOnly := func(far []int16, h []tap) signals {
		e := convolve(far, h)
		return signals{mic: mix(e, noise(len(far))), far: far, near: make([]int16, len(far)), echo: e}
	}
	switch name {
	case "farend":
		return echoOnly(speech(rate, n(16), farAmp, "far"), pathA(rate))
	case "nearend":
		near := speech(rate, n(12), nearAmp, "near")
		return signals{mic: mix(near, noise(len(near))), far: make([]int16, len(near)), near: near, echo: make([]int16, len(near))}
	case "doubletalk":
		far := speech(rate, n(20), farAmp, "far")
		e := convolve(far, pathA(rate))
		near := gate(speech(rate, n(20), nearAmp, "near"), [2]int{n(8), n(12)}, [2]int{n(15), n(18)})
		return signals{mic: mix(e, near, noise(len(far))), far: far, near: near, echo: e}
	case "silence":
		far := gate(speech(rate, n(16), farAmp, "far"), [2]int{0, n(4)}, [2]int{n(6), n(10)}, [2]int{n(12), n(16)})
		e := convolve(far, pathA(rate))
		s := signals{mic: mix(e, noise(len(far))), far: far, near: make([]int16, len(far)), echo: e}
		clear(s.mic[n(10):n(12)])
		return s
	case "lownoise":
		far := testutil.PinkNoise(n(16), 200, testutil.Seed("lownoise/far"))
		e := convolve(far, pathA(rate))
		mic := mix(e, testutil.WhiteNoise(len(far), 4, testutil.Seed("lownoise/noise")))
		return signals{mic: mic, far: far, near: make([]int16, len(far)), echo: e}
	case "ir_short":
		return echoOnly(speech(rate, n(12), farAmp, "far"), room(rate, "short", 0, 6, 16000))
	case "ir_long":
		return echoOnly(speech(rate, n(16), farAmp, "far"), room(rate, "long", 10, 220, 16000))
	case "path_sudden":
		far := speech(rate, n(20), farAmp, "far")
		a, b := pathA(rate), pathB(rate)
		e := make([]int16, len(far))
		for i := range e {
			h := a
			if i >= n(10) {
				h = b
			}
			e[i] = sat16(convolveAt(far, h, i))
		}
		return signals{mic: mix(e, noise(len(far))), far: far, near: make([]int16, len(far)), echo: e}
	case "path_gradual":
		far := speech(rate, n(20), farAmp, "far")
		a, b := pathA(rate), pathB(rate)
		e := make([]int16, len(far))
		f0, f1 := int64(n(6)), int64(n(14))
		for i := range e {
			k := min(max(int64(i)-f0, 0), f1-f0)
			ya, yb := convolveAt(far, a, i), convolveAt(far, b, i)
			e[i] = sat16((ya*(f1-f0-k) + yb*k) / (f1 - f0))
		}
		return signals{mic: mix(e, noise(len(far))), far: far, near: make([]int16, len(far)), echo: e}
	case "misaligned_late":
		far := speech(rate, n(16), farAmp, "far")
		h := pathA(rate)
		d := msToSamples(rate, filterMs+32)
		for i := range h {
			h[i].off += d
		}
		return echoOnly(far, h)
	case "misaligned_early":
		// The echo comes from far; the canceller is given far 30 ms late.
		far := speech(rate, n(16), farAmp, "far")
		s := echoOnly(far, pathA(rate))
		s.far = delay(far, msToSamples(rate, 30))
		return s
	case "diverge":
		return diverge(rate)
	case "edges":
		return edges(rate, frame)
	case "capture":
		// Echo of each played frame arrives two frames later, the delay the
		// buffered mode assumes; the far end is longer to cover overruns.
		far := speech(rate, n(19), farAmp, "far")
		e := convolve(delay(far, 2*frame), pathA(rate))[:n(16)]
		return signals{mic: mix(e, noise(len(e))), far: far, near: make([]int16, len(e)), echo: e}
	}
	panic("unknown scenario " + name)
}

func diverge(rate int) signals {
	n := func(s float64) int { return sec(rate, s) }
	total := n(20)
	far := speech(rate, total, farAmp, "far")
	near := make([]int16, total)
	r := testutil.SplitMix64{State: testutil.Seed("diverge")}
	// 5-7 s: full-scale square wave at 250 Hz.
	half := rate / 500
	for i := n(5); i < n(7); i++ {
		far[i] = 32767
		if (i/half)%2 == 1 {
			far[i] = -32767
		}
	}
	// 13-15 s: full-range noise, including -32768.
	for i := n(13); i < n(15); i++ {
		far[i] = int16(r.Next() >> 48)
	}
	e := convolve(far, pathA(rate))
	noise := testutil.WhiteNoise(total, noiseA, testutil.Seed("diverge/noise"))
	mic := make([]int16, total)
	for i := range mic {
		v := int64(e[i]) + int64(noise[i])
		switch {
		case i >= n(5) && i < n(7):
			v = 4 * int64(e[i]) // clipping microphone
		case i >= n(7) && i < n(9):
			v += 8000 // DC offset
		case i >= n(9) && i < n(13):
			v = int64(noise[i]) // echo path cut
		case i >= n(13) && i < n(15):
			v = int64(int16(r.Next() >> 48))
		}
		mic[i] = sat16(v)
	}
	return signals{mic: mic, far: far, near: near, echo: e}
}

// longRun builds blocks 30 s blocks: far-end speech, a path per block,
// near-end speech in the second half of some blocks and a 3 s silence. Each
// block depends only on its seed and earlier blocks, so a prefix of blocks
// is a prefix of the whole.
func longRun(rate, blocks int) signals {
	bl := 30 * rate
	far := make([]int16, 0, blocks*bl)
	near := make([]int16, 0, blocks*bl)
	paths := make([][]tap, blocks)
	for b := 0; b < blocks; b++ {
		r := testutil.SplitMix64{State: testutil.Seed(fmt.Sprintf("long/%d", b))}
		f := speech(rate, bl, farAmp, fmt.Sprintf("long/far/%d", b))
		s0 := r.Intn(26 * rate)
		clear(f[s0 : s0+3*rate])
		far = append(far, f...)
		nb := make([]int16, bl)
		if r.Intn(2) == 1 {
			copy(nb[bl/2:], speech(rate, bl/2, nearAmp, fmt.Sprintf("long/near/%d", b)))
		}
		near = append(near, nb...)
		paths[b] = room(rate, fmt.Sprintf("long/path/%d", b), 2+r.Intn(10), 30+r.Intn(60), int32(8000+r.Intn(8000)))
	}
	e := make([]int16, len(far))
	for i := range e {
		e[i] = sat16(convolveAt(far, paths[i/bl], i))
	}
	noise := testutil.WhiteNoise(len(far), noiseA, testutil.Seed("long/noise"))
	return signals{mic: mix(e, near, noise), far: far, near: near, echo: e}
}

// Case table.

var (
	rates     = []int{8000, 16000, 32000, 48000}
	frameMss  = []int{10, 20}
	filterMss = []int{64, 128, 256}
	scenarios = []string{"farend", "nearend", "doubletalk", "silence", "lownoise", "ir_short", "ir_long",
		"path_sudden", "path_gradual", "misaligned_late", "misaligned_early", "diverge", "reset", "capture", "rate_switch",
		"edges"}
)

// Op codes of the driver's op stream.
const (
	opCancel = iota
	opPlayback
	opCapture
	opReset
	opSetRate
)

type refCase struct {
	name                 string
	scenario             string
	rate, frame, filter  int
	frameMs, filterMs    int
	base                 string
	outPrefix, resPrefix int
	longBlocks           int // 0, or the 30 s block count of a long run
}

func khz(rate int) string { return fmt.Sprintf("%dk", rate/1000) }

func refCases() []refCase {
	var cs []refCase
	add := func(scen string, rate, fms, lms int, base string, blocks int) {
		fs := msToSamples(rate, fms)
		cs = append(cs, refCase{
			name:     fmt.Sprintf("%s_%s_%dms_%dms", scen, khz(rate), fms, lms),
			scenario: scen, rate: rate, frame: fs, filter: msToSamples(rate, lms),
			frameMs: fms, filterMs: lms, base: base,
			outPrefix: 3200, resPrefix: 2 * (fs + 1), longBlocks: blocks,
		})
	}
	for _, rate := range rates {
		for _, fms := range frameMss {
			for _, lms := range filterMss {
				for _, s := range scenarios {
					base := s + "_" + khz(rate)
					switch s {
					case "reset", "rate_switch":
						base = "farend_" + khz(rate)
					case "misaligned_late":
						base = fmt.Sprintf("%s_%dms", base, lms)
					case "capture", "edges":
						base = fmt.Sprintf("%s_%dms", base, fms)
					}
					add(s, rate, fms, lms, base, 0)
				}
			}
		}
	}
	for _, rate := range rates {
		for _, fms := range frameMss {
			add("long60", rate, fms, 256, "long60_"+khz(rate), 2)
		}
	}
	for _, rate := range rates[:2] {
		add("long10m", rate, 10, 128, "long10m_"+khz(rate), 20)
	}
	return cs
}

// inputsUncached generates the scenario signals of c. A long run is
// generated in full: its prefix property is used only by callers that cut
// the result.
func inputsUncached(c refCase) signals {
	switch {
	case c.longBlocks > 0:
		return longRun(c.rate, c.longBlocks)
	case c.scenario == "reset", c.scenario == "rate_switch":
		return scenario("farend", c.rate, c.filterMs, c.frame)
	}
	return scenario(c.scenario, c.rate, c.filterMs, c.frame)
}

// ops returns the driver's op stream for c: (op, arg) pairs.
func ops(c refCase, s signals) []int32 {
	fs := c.frame
	frames := len(s.mic) / fs
	var o []int32
	put := func(op, arg int32) { o = append(o, op, arg) }
	switch c.scenario {
	case "capture":
		r := testutil.SplitMix64{State: testutil.Seed(c.name + "/ops")}
		farFrames := len(s.far) / fs
		played := 0
		play := func() {
			if played < farFrames {
				put(opPlayback, 0)
				played++
			}
		}
		play() // both are discarded: no capture has started yet
		play()
		for k := 0; k < frames; k++ {
			switch v := r.Intn(100); {
			case v < 5: // no playback before this capture
			case v < 10: // two playbacks
				play()
				play()
			default:
				play()
			}
			put(opCapture, 0)
		}
	default:
		special := -1
		if c.scenario == "reset" || c.scenario == "rate_switch" {
			special = sec(c.rate, 8) / fs
		}
		switches := map[int]int{}
		if c.scenario == "edges" {
			for _, sw := range edgeRates(c.rate) {
				switches[sec(c.rate, sw.at)/fs] = sw.rate
			}
		}
		for k := 0; k < frames; k++ {
			if r, ok := switches[k]; ok {
				put(opSetRate, int32(r))
			}
			if k == special {
				if c.scenario == "reset" {
					put(opReset, 0)
				} else {
					put(opSetRate, int32(nextRate(c.rate)))
				}
			}
			put(opCancel, 0)
		}
	}
	return o
}

func nextRate(rate int) int {
	for i, r := range rates {
		if r == rate {
			return rates[(i+1)%len(rates)]
		}
	}
	return rates[0]
}

// edgeRates are the SetSampleRate calls of the edges scenario: both sides
// of the notch-radius steps and of the 16-bit truncation of the rate.
func edgeRates(rate int) []struct {
	at   float64
	rate int
} {
	return []struct {
		at   float64
		rate int
	}{{10, 12000}, {10.5, 11999}, {11, 24000}, {11.5, 23999}, {12, 32768}, {12.5, 32767}, {13, rate}}
}

// front simulates the DC notch and pre-emphasis of cancel
// (mdf.c:719-743), which depend only on the microphone samples.
type front struct {
	mem  [2]int32
	memD int16
}

// step feeds one sample and returns the pre-emphasis output before the
// clamp.
func (f *front) step(v int16, radius int16) int32 {
	var out [1]int16
	filterDCNotch16([]int16{v}, radius, out[:], &f.mem)
	tmp := int32(out[0]) - (16384+int32(q09)*int32(f.memD))>>15
	f.memD = out[0]
	return tmp
}

func notchRadius(rate int) int16 {
	switch {
	case rate < 12000:
		return q09
	case rate < 24000:
		return q0982
	}
	return q0992
}

// edges builds boundary inputs (docs/acceptance/echo.md, "Changes"): a far
// frame whose energy sits exactly on the adaptation threshold of
// mdf.c:1153, microphone samples at the saturation test of mdf.c:973, and
// microphone samples whose pre-emphasis output is exactly ±32767
// (mdf.c:728-739). Rate changes follow in the op stream (edgeRates).
func edges(rate, frame int) signals {
	n := func(s float64) int { return sec(rate, s) }
	total := n(16)
	far := make([]int16, total)
	copy(far[n(0.5):], speech(rate, total-n(0.5), farAmp, "far"))
	p15 := func(v int16) int16 { return int16((16384 + int32(q09)*int32(v)) >> 15) }

	// Frame 3: Sxx is twice the inner product of the pre-emphasised frame
	// (mdf.c:786 and :1044); place it between the thresholds for 999 and
	// 1000 so the comparison at mdf.c:1153 is decided by one unit.
	nn := 2 * frame
	thr := (nn * 1000) >> 6
	target := thr / 2
	if 2*target <= (nn*999)>>6 {
		panic("edges: no room between the thresholds")
	}
	x0, x1 := -1, -1
	for a := 0; a*a <= 64*target+63 && x0 < 0; a++ {
		for b := 0; b <= a; b++ {
			if v := a*a + b*b; v >= 64*target && v <= 64*target+63 {
				x0, x1 = a, b
				break
			}
		}
	}
	if x0 < 0 {
		panic("edges: no pair for the threshold")
	}
	k := 3*frame + 10 // an even offset in the frame, so x0 and x1 form one pair
	far[k] = int16(x0)
	far[k+1] = int16(x1) + p15(far[k])
	for j := k + 2; j < n(0.5); j++ {
		far[j] = p15(far[j-1]) // pre-emphasis output 0
	}

	e := convolve(far, pathA(rate))
	mic := mix(e, testutil.WhiteNoise(total, noiseA, testutil.Seed("edges/noise")))
	clip := func(lo, hi int) {
		for i := lo; i < hi; i++ {
			mic[i] = max(-31000, min(31000, mic[i]))
		}
	}
	at := func(s float64) int { return (n(s)/frame)*frame + frame/2 }
	// Saturation test: exactly ±32000 and the values just inside.
	for _, ev := range []struct {
		t float64
		v int16
	}{{8, 32000}, {8.25, -32000}, {8.5, 31999}, {8.55, -31999}} {
		i := at(ev.t)
		clip(i-frame, i+frame)
		mic[i] = ev.v
	}
	// Pre-emphasis output exactly 32767 and -32767.
	radius := notchRadius(rate)
	for _, ev := range []struct {
		t    float64
		want int32
	}{{8.75, 32767}, {9, -32767}} {
		i := at(ev.t)
		clip(i-frame, i+frame)
		var base front
		for _, v := range mic[:i-1] {
			base.step(v, radius)
		}
		sign := int16(1)
		if ev.want < 0 {
			sign = -1
		}
		found := false
		for prev := int16(20000); prev <= 31000 && !found; prev += 1000 {
			p := base
			p.step(-sign*prev, radius)
			for v := int16(0); v <= 31999; v++ {
				q := p
				if q.step(sign*v, radius) == ev.want {
					mic[i-1], mic[i] = -sign*prev, sign*v
					found = true
					break
				}
			}
		}
		if !found {
			panic(fmt.Sprintf("edges: no sample gives pre-emphasis output %d", ev.want))
		}
	}
	return signals{mic: mic, far: far, near: make([]int16, total), echo: e}
}

// TestEdgesHitBoundaries checks that the edges inputs reach the boundaries
// they are built for.
func TestEdgesHitBoundaries(t *testing.T) {
	for _, rate := range rates {
		for _, fms := range frameMss {
			fs := msToSamples(rate, fms)
			s := edges(rate, fs)
			// The threshold frame: Sxx = 2 * inner product of its pre-emphasis.
			x := make([]int16, fs)
			var memX int16
			for f := 0; f <= 3; f++ {
				for i := 0; i < fs; i++ {
					v := s.far[f*fs+i]
					x[i] = int16(int32(v) - (16384+int32(q09)*int32(memX))>>15)
					memX = v
				}
			}
			n := 2 * fs
			sxx := 2 * mdfInnerProd(x, x, fs)
			if !(sxx > int32((n*999)>>6) && sxx <= int32((n*1000)>>6)) {
				t.Errorf("%d Hz, %d: Sxx %d not in (%d, %d]", rate, fs, sxx, (n*999)>>6, (n*1000)>>6)
			}
			hits := map[int32]bool{}
			var f front
			for i, v := range s.mic[:sec(rate, 9.5)] {
				if v == 32000 || v == -32000 {
					hits[int32(v)] = true
				}
				if tmp := f.step(v, notchRadius(rate)); (tmp == 32767 || tmp == -32767) && i > sec(rate, 8.5) {
					hits[tmp*10] = true
				}
			}
			if len(hits) != 4 {
				t.Errorf("%d Hz, %d: boundaries reached %v, want ±32000 and pre-emphasis ±32767", rate, fs, hits)
			}
		}
	}
}
