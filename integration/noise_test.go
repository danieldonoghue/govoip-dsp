package integration

import (
	"fmt"
	"math"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/agc"
	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
	"github.com/danieldonoghue/govoip-dsp/preprocess"
)

// TestNoiseThroughSendChain reports stationary noise through preprocess and
// agc, with the VAD decision as speech (criteria N1 and N2).
func TestNoiseThroughSendChain(t *testing.T) {
	const seconds = 30
	gens := []struct {
		name string
		gen  func(n int, amp int16, seed uint64) []int16
	}{{"white", testutil.WhiteNoise}, {"pink", testutil.PinkNoise}}
	for _, rate := range []int{8000, 16000} {
		for _, g := range gens {
			for _, amp := range []int16{2000, 600, 200, 60} {
				t.Run(fmt.Sprintf("%dk_%s_%d", rate/1000, g.name, amp), func(t *testing.T) {
					frame := rate / 50
					pre, err := preprocess.New(preprocess.Config{SampleRate: rate, FrameSize: frame})
					if err != nil {
						t.Fatal(err)
					}
					pre.SetVAD(true)
					cfg := agc.DefaultConfig(rate)
					gc, err := agc.New(cfg)
					if err != nil {
						t.Fatal(err)
					}
					in := g.gen(seconds*rate, amp, testutil.Seed(t.Name()))
					den := make([]int16, len(in))
					out := make([]int16, len(in))
					copy(den, in)
					speechFrames, frames := 0, 0
					maxGain := math.Inf(-1)
					for i := 0; i+frame <= len(in); i += frame {
						speech, err := pre.Run(den[i : i+frame])
						if err != nil {
							t.Fatal(err)
						}
						if err := gc.Process(den[i:i+frame], out[i:i+frame], speech); err != nil {
							t.Fatal(err)
						}
						if i >= (seconds-10)*rate {
							frames++
							if speech {
								speechFrames++
							}
						}
						// Gain over each whole second from 1 s on, the last included.
						if end := i + frame; end%rate == 0 && end >= 2*rate {
							maxGain = max(maxGain, dB(energy(out[end-rate:end]), energy(den[end-rate:end])))
						}
					}
					last := func(x []int16) []int16 { return x[(seconds-10)*rate:] }
					inL := testutil.DBFS(testutil.RMS(last(in)))
					denL := testutil.DBFS(testutil.RMS(last(den)))
					outL := testutil.DBFS(testutil.RMS(last(out)))
					peak := 0
					for _, v := range out {
						peak = max(peak, int(math.Abs(float64(v))))
					}
					t.Logf("input %.1f dBFS, after preprocess %.1f dBFS (%.1f dB), VAD speech %d of %d frames, AGC out %.1f dBFS, AGC gain %+.1f dB (max per second %+.1f dB), gate %d dBFS",
						inL, denL, denL-inL, speechFrames, frames, outL, outL-denL, maxGain, cfg.GateLevel)
					if maxGain > float64(cfg.MaxGain)+0.5 {
						t.Errorf("N2: AGC gain %.1f dB above MaxGain %d dB", maxGain, cfg.MaxGain)
					}
					if peak > ceiling {
						t.Errorf("N2: peak %d above the ceiling %d", peak, ceiling)
					}
				})
			}
		}
	}
}
