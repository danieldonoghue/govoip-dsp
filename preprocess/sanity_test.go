package preprocess

import (
	"fmt"
	"math"
	"slices"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

// Sanity signal tests (docs/acceptance/preprocess.md). Bit-exactness
// defines correctness, so these report in human terms and do not threshold.

// process runs x through a new Preprocessor and returns the output aligned
// with x (the reference delays by one frame) and the share of VAD frames.
func process(t *testing.T, rate, n int, x []int16, denoise bool) ([]int16, float64) {
	t.Helper()
	p := mustNew(t, rate, n)
	p.SetDenoise(denoise)
	p.SetVAD(true)
	out, vad := runAll(t, p, x[:len(x)/n*n], nil)
	speech := 0
	for _, v := range vad {
		if v {
			speech++
		}
	}
	return out[n:], float64(speech) / float64(len(vad))
}

func mixAtSNR(clean, noise []int16, snr float64) []int16 {
	g := testutil.RMS(clean) / testutil.RMS(noise) / math.Pow(10, snr/20)
	return testutil.Add(clean, testutil.Scale(noise, int(g*10000+0.5), 10000))
}

func TestSanityNoiseReduction(t *testing.T) {
	t.Parallel()
	for _, c := range mvpConfigs {
		rate, n := c[0], c[1]
		for _, nz := range []struct {
			name string
			x    []int16
		}{
			{"white", testutil.WhiteNoise(10*rate, 1000, 1)},
			{"pink", testutil.PinkNoise(10*rate, 3000, 2)},
		} {
			out, vad := process(t, rate, n, nz.x, true)
			skip := rate // the first second adapts
			in := testutil.DBFS(testutil.RMS(nz.x[skip:len(out)]))
			got := testutil.DBFS(testutil.RMS(out[skip:]))
			t.Logf("%5d Hz %4d: %-5s noise %6.1f dBFS -> %6.1f dBFS, reduction %5.1f dB, VAD speech %5.1f%%",
				rate, n, nz.name, in, got, in-got, 100*vad)
		}
	}
}

func reportSpeech(t *testing.T, label string, rate, n int, clean []int16) {
	noise := testutil.WhiteNoise(len(clean), 10000, 3)
	ref, refVAD := process(t, rate, n, clean, false)
	t.Logf("%s: clean, denoise off: SNR to input %.1f dB, VAD speech %.1f%%",
		label, testutil.SNR(clean[:len(ref)], ref), 100*refVAD)
	_, cleanVAD := process(t, rate, n, clean, true)
	t.Logf("%s: clean, denoise on: VAD speech %.1f%%", label, 100*cleanVAD)
	for _, snr := range []float64{0, 5, 10} {
		noisy := mixAtSNR(clean, noise, snr)
		out, vad := process(t, rate, n, noisy, true)
		before := testutil.SegmentalSNR(clean[:len(out)], noisy[:len(out)], n)
		after := testutil.SegmentalSNR(clean[:len(out)], out, n)
		t.Logf("%s: %4.0f dB SNR: segmental SNR %5.1f -> %5.1f dB (%+.1f dB), VAD speech %.1f%%",
			label, snr, before, after, after-before, 100*vad)
	}
}

func TestSanitySpeech(t *testing.T) {
	t.Parallel()
	const rate = 16000
	for _, n := range []int{160, 320} {
		reportSpeech(t, fmt.Sprintf("SpeechLike 16 kHz/%d", n), rate, n, testutil.SpeechLike(30*rate, rate, 12000, 4))
	}
	t.Run("LibriSpeech", func(t *testing.T) {
		var clean []int16
		for _, f := range testutil.SpeechFiles(t) {
			clean = append(clean, testutil.LoadSpeech(t, f)...)
			if len(clean) >= 60*rate {
				break
			}
		}
		for _, n := range []int{160, 320} {
			reportSpeech(t, fmt.Sprintf("LibriSpeech %d s, 16 kHz/%d", len(clean)/rate, n), rate, n, clean)
		}
	})
}

func TestSanityVAD(t *testing.T) {
	t.Parallel()
	const rate, n = 16000, 320
	for _, s := range []struct {
		name string
		x    []int16
	}{
		{"silence", testutil.Silence(20 * rate)},
		{"white noise -30 dBFS", testutil.WhiteNoise(20*rate, 1800, 5)},
		{"pink noise", testutil.PinkNoise(20*rate, 3000, 6)},
		{"pink noise, 120 s", testutil.PinkNoise(120*rate, 3000, 8)},
		{"SpeechLike", testutil.SpeechLike(20*rate, rate, 12000, 7)},
	} {
		_, vad := process(t, rate, n, s.x, true)
		p := mustNew(t, rate, n)
		last := s.x[len(s.x)-10*rate:]
		runAll(t, p, s.x[:len(s.x)-len(last)], nil)
		sum, frames := 0, 0
		for f := 0; f+n <= len(last); f += n {
			if _, err := p.Run(slices.Clone(last[f : f+n])); err != nil {
				t.Fatal(err)
			}
			sum += p.SpeechProb()
			frames++
		}
		t.Logf("16 kHz/%d %-20s VAD speech %5.1f%% of frames; mean SpeechProb over the last 10 s %d%%",
			n, s.name, 100*vad, sum/frames)
	}
}
