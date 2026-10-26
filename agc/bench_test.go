package agc

import (
	"fmt"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

func BenchmarkAGCProcess(b *testing.B) {
	for _, c := range []struct{ rate, chunk int }{
		{8000, 80}, {16000, 160}, {32000, 320}, {48000, 480}, {16000, 320}, {16000, 37},
	} {
		b.Run(fmt.Sprintf("%dk_%d", c.rate/1000, c.chunk), func(b *testing.B) {
			a, err := New(DefaultConfig(c.rate))
			if err != nil {
				b.Fatal(err)
			}
			// Speech at full scale keeps the limiter and the level estimate busy.
			x := testutil.SpeechLike(c.rate, float64(c.rate), 32767, 51)
			y := make([]int16, len(x))
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				if i+c.chunk > len(x) {
					i = 0
				}
				if err := a.Process(x[i:i+c.chunk], y[i:i+c.chunk], true); err != nil {
					b.Fatal(err)
				}
				i += c.chunk
			}
			testutil.ReportFrameMetrics(b, c.chunk, float64(c.rate))
		})
	}
}

func BenchmarkAGCNew(b *testing.B) {
	cfg := DefaultConfig(48000)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := New(cfg); err != nil {
			b.Fatal(err)
		}
	}
}
