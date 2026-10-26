package resample

import (
	"fmt"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

func rateName(r int) string {
	if r%1000 == 0 {
		return fmt.Sprintf("%dk", r/1000)
	}
	return fmt.Sprintf("%.1fk", float64(r)/1000)
}

// BenchmarkResamplerProcess processes one chunk per iteration. samples/s
// and rt-fraction count input frames at the input rate.
func BenchmarkResamplerProcess(b *testing.B) {
	pairs := [][2]int{{8000, 16000}, {16000, 8000}, {16000, 48000}, {48000, 16000}, {44100, 48000}, {48000, 8000}}
	for _, p := range pairs {
		for _, q := range []int{3, 5, 8, 10} {
			for _, ch := range []int{1, 2} {
				chName := map[int]string{1: "mono", 2: "stereo"}[ch]
				for _, frames := range []int{p[0] / 100, p[0] / 50, 7, 331} {
					name := fmt.Sprintf("q%d_%s_%s_%s_%d", q, rateName(p[0]), rateName(p[1]), chName, frames)
					b.Run(name, func(b *testing.B) {
						r, err := New(Config{Channels: ch, InRate: p[0], OutRate: p[1], Quality: q})
						if err != nil {
							b.Fatal(err)
						}
						in := testutil.SpeechLike(frames*ch, float64(p[0]), 20000, 1)
						out := make([]int16, (frames*p[1]/p[0]+8)*ch)
						for range 50 {
							r.Process(in, out)
						}
						b.ReportAllocs()
						for b.Loop() {
							if n, _, _ := r.Process(in, out); n != frames {
								b.Fatalf("consumed %d of %d frames", n, frames)
							}
						}
						testutil.ReportFrameMetrics(b, frames, float64(p[0]))
					})
				}
			}
		}
	}
}

// BenchmarkResamplerNew measures construction, which builds the sinc table.
func BenchmarkResamplerNew(b *testing.B) {
	for _, c := range []Config{
		{Channels: 1, InRate: 48000, OutRate: 16000, Quality: 3},
		{Channels: 1, InRate: 44100, OutRate: 48000, Quality: 10},
		{Channels: 2, InRate: 48000, OutRate: 8000, Quality: 10},
	} {
		b.Run(fmt.Sprintf("q%d_%s_%s_%dch", c.Quality, rateName(c.InRate), rateName(c.OutRate), c.Channels), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := New(c); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
