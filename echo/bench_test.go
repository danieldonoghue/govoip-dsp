package echo

import (
	"fmt"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

// BenchmarkCancellerCancel runs adapted cancellers on the farend scenario
// (docs/acceptance/echo.md, "Benchmark targets").
func BenchmarkCancellerCancel(b *testing.B) {
	type bc struct{ rate, frameMs, filterMs int }
	var cases []bc
	for _, rate := range []int{8000, 16000} {
		for _, l := range filterMss {
			cases = append(cases, bc{rate, 10, l})
		}
	}
	cases = append(cases, bc{32000, 10, 256}, bc{48000, 10, 256}, bc{16000, 20, 256})
	for _, k := range cases {
		fs := msToSamples(k.rate, k.frameMs)
		name := fmt.Sprintf("%s_%dms_%dms_%d", khz(k.rate), k.frameMs, k.filterMs, fs)
		b.Run(name, func(b *testing.B) {
			s := gen{}.scenario("farend", k.rate, k.filterMs, fs)
			c, err := New(Config{FrameSize: fs, FilterLength: msToSamples(k.rate, k.filterMs), SampleRate: k.rate})
			if err != nil {
				b.Fatal(err)
			}
			out := make([]int16, fs)
			frames := len(s.mic) / fs
			// Warm up, into the adapted state where the reference reaches it.
			for k := 0; k < 4*frames && !(c.st.adapted && k >= frames/2); k++ {
				i := k % frames
				_ = c.Cancel(s.mic[i*fs:(i+1)*fs], s.far[i*fs:(i+1)*fs], out)
			}
			// The adapted path adds mdf_adjust_prop; report which one ran.
			adapted := 0.0
			if c.st.adapted {
				adapted = 1
			}
			i := frames / 2
			b.ReportAllocs()
			for b.Loop() {
				if i == frames {
					i = frames / 2
				}
				_ = c.Cancel(s.mic[i*fs:(i+1)*fs], s.far[i*fs:(i+1)*fs], out)
				i++
			}
			testutil.ReportFrameMetrics(b, fs, float64(k.rate))
			b.ReportMetric(adapted, "adapted")
		})
	}
}
