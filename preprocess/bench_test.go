package preprocess

import (
	"fmt"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

// benchFrames is enough speech in noise for the estimates to adapt before
// timing starts; the loop then cycles through it.
const benchFrames = 200

func benchRun(b *testing.B, echo bool) {
	for _, c := range mvpConfigs {
		rate, n := c[0], c[1]
		b.Run(fmt.Sprintf("%dk_%d", rate/1000, n), func(b *testing.B) {
			p := mustNew(b, rate, n)
			p.SetVAD(true)
			x := signal("speech_noise", rate, benchFrames*n)
			res := make([]int32, n)
			for i := range res {
				res[i] = int32(x[i]) * int32(x[i]) >> 6
			}
			frame := make([]int16, n)
			for f := range benchFrames {
				copy(frame, x[f*n:(f+1)*n])
				_, _ = p.Run(frame)
			}
			b.ReportAllocs()
			f := 0
			for b.Loop() {
				copy(frame, x[f*n:(f+1)*n])
				f = (f + 1) % benchFrames
				if echo {
					_, _ = p.RunEcho(frame, res)
				} else {
					_, _ = p.Run(frame)
				}
			}
			testutil.ReportFrameMetrics(b, n, float64(rate))
		})
	}
}

func BenchmarkPreprocessorRun(b *testing.B)     { benchRun(b, false) }
func BenchmarkPreprocessorRunEcho(b *testing.B) { benchRun(b, true) }

func BenchmarkPreprocessorNew(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := New(Config{SampleRate: 48000, FrameSize: 960}); err != nil {
			b.Fatal(err)
		}
	}
}
