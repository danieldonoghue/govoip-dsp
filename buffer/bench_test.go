package buffer

import (
	"runtime"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

// benchCases are the chunk sizes, the rate they are reported at, and the
// capacity. The wrap cases use a capacity that most cycles wrap around.
var benchCases = []struct {
	name     string
	chunk    int
	rate     float64
	capacity int
}{
	{"80", 80, 8000, 4 * 80},
	{"160", 160, 16000, 4 * 160},
	{"480", 480, 48000, 4 * 480},
	{"4096", 4096, 48000, 4 * 4096},
	{"odd97", 97, 48000, 1000},
	{"odd331", 331, 48000, 1000},
	{"wrap160", 160, 16000, 160 + 1},
	{"wrap480", 480, 48000, 480 + 7},
}

func BenchmarkRingWriteRead(b *testing.B) {
	for _, c := range benchCases {
		for _, policy := range []Overflow{Reject, Overwrite} {
			name := c.name + "/" + [...]string{Reject: "reject", Overwrite: "overwrite"}[policy]
			b.Run(name, func(b *testing.B) {
				r := newRing(b, c.capacity, policy)
				in := testutil.WhiteNoise(c.chunk, 16384, 1)
				out := make([]int16, c.chunk)
				b.ReportAllocs()
				for b.Loop() {
					r.Write(in)
					if r.Read(out) != c.chunk {
						b.Fatal("short read")
					}
				}
				testutil.ReportFrameMetrics(b, c.chunk, c.rate)
			})
		}
	}
}

// BenchmarkRingWriteSlices is a write/read cycle through the two views.
func BenchmarkRingWriteSlices(b *testing.B) {
	for _, c := range benchCases {
		b.Run(c.name, func(b *testing.B) {
			r := newRing(b, c.capacity, Reject)
			in := testutil.WhiteNoise(c.chunk, 16384, 1)
			out := make([]int16, c.chunk)
			b.ReportAllocs()
			for b.Loop() {
				w1, w2 := r.WriteSlices()
				k := copy(w1, in)
				copy(w2, in[k:])
				r.Commit(c.chunk)
				r1, r2 := r.ReadSlices()
				k = copy(out, r1)
				copy(out[k:], r2)
				if r.Consume(c.chunk) != c.chunk {
					b.Fatal("short read")
				}
			}
			testutil.ReportFrameMetrics(b, c.chunk, c.rate)
		})
	}
}

func BenchmarkSPSCWriteRead(b *testing.B) {
	for _, c := range benchCases {
		b.Run(c.name, func(b *testing.B) {
			s, err := NewSPSC(SPSCConfig{Capacity: c.capacity})
			if err != nil {
				b.Fatal(err)
			}
			in := testutil.WhiteNoise(c.chunk, 16384, 1)
			out := make([]int16, c.chunk)
			b.ReportAllocs()
			for b.Loop() {
				s.Write(in)
				if s.Read(out) != c.chunk {
					b.Fatal("short read")
				}
			}
			testutil.ReportFrameMetrics(b, c.chunk, c.rate)
		})
	}
}

// BenchmarkSPSCRead/handoff_480 reads 480-sample frames that a producer
// goroutine writes; each side yields when it cannot make progress.
func BenchmarkSPSCRead(b *testing.B) {
	b.Run("handoff_480", benchHandoff)
}

func benchHandoff(b *testing.B) {
	const chunk = 480
	s, err := NewSPSC(SPSCConfig{Capacity: 4 * chunk})
	if err != nil {
		b.Fatal(err)
	}
	in := testutil.WhiteNoise(chunk, 16384, 1)
	out := make([]int16, chunk)
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-done:
				return
			default:
			}
			if s.Free() >= chunk {
				s.Write(in)
			} else {
				runtime.Gosched()
			}
		}
	}()
	b.ReportAllocs()
	for b.Loop() {
		for s.Len() < chunk {
			runtime.Gosched()
		}
		s.Read(out)
	}
	testutil.ReportFrameMetrics(b, chunk, 48000)
	close(done)
	<-stopped
}
