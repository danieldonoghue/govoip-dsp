package buffer

import (
	"errors"
	"slices"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

func newRing(t testing.TB, capacity int, policy Overflow) *Ring {
	t.Helper()
	b, err := New(Config{Capacity: capacity, Overflow: policy})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func ramp(start, n int) []int16 {
	p := make([]int16, n)
	for i := range p {
		p[i] = int16(start + i)
	}
	return p
}

func readAll(b fifo) []int16 {
	p := make([]int16, b.Len())
	return p[:b.Read(p)]
}

func TestConfigErrors(t *testing.T) {
	bad := []Config{
		{Capacity: 0, Overflow: Reject},
		{Capacity: -1, Overflow: Reject},
		{Capacity: MaxCapacity + 1, Overflow: Overwrite},
		{Capacity: 10},
		{Capacity: 10, Overflow: Overwrite + 1},
		{Capacity: 10, Overflow: -1},
	}
	if err := checkCapacity(MaxCapacity); err != nil {
		t.Errorf("MaxCapacity rejected: %v", err)
	}
	for _, cfg := range bad {
		if b, err := New(cfg); !errors.Is(err, ErrConfig) || b != nil {
			t.Errorf("New(%+v) = %v, %v; want nil, ErrConfig", cfg, b, err)
		}
	}
	for _, c := range []int{0, -5, MaxCapacity + 1} {
		if s, err := NewSPSC(SPSCConfig{Capacity: c}); !errors.Is(err, ErrConfig) || s != nil {
			t.Errorf("NewSPSC(%d) = %v, %v; want nil, ErrConfig", c, s, err)
		}
	}
	if _, err := New(Config{Capacity: 1, Overflow: Reject}); err != nil {
		t.Error(err)
	}
}

// TestFullAndEmpty fills every small capacity to the last slot and drains it.
func TestFullAndEmpty(t *testing.T) {
	for capacity := 1; capacity <= 64; capacity++ {
		for _, b := range fifos(t, capacity) {
			for lap := range 3 { // start at different offsets
				b.Write(ramp(0, lap))
				b.Consume(lap)
				if b.Len() != 0 || b.Free() != capacity {
					t.Fatalf("cap %d: empty buffer has Len %d Free %d", capacity, b.Len(), b.Free())
				}
				if n := b.Write(ramp(1, capacity)); n != capacity || b.Len() != capacity || b.Free() != 0 {
					t.Fatalf("cap %d: Write = %d, Len %d Free %d; want full", capacity, n, b.Len(), b.Free())
				}
				if got := readAll(b); !slices.Equal(got, ramp(1, capacity)) {
					t.Fatalf("cap %d: read %v", capacity, got)
				}
			}
		}
	}
}

// fifos returns a Reject Ring, an Overwrite Ring and an SPSC of one capacity.
func fifos(t testing.TB, capacity int) []fifo {
	s, err := NewSPSC(SPSCConfig{Capacity: capacity})
	if err != nil {
		t.Fatal(err)
	}
	return []fifo{newRing(t, capacity, Reject), newRing(t, capacity, Overwrite), s}
}

func TestRejectKeepsStoredSamples(t *testing.T) {
	for _, b := range []fifo{newRing(t, 5, Reject), fifos(t, 5)[2]} {
		if n := b.Write(ramp(0, 3)); n != 3 {
			t.Fatalf("Write = %d", n)
		}
		if n := b.Write(ramp(10, 4)); n != 2 {
			t.Fatalf("Write past capacity = %d, want 2", n)
		}
		if n := b.Write(ramp(20, 1)); n != 0 {
			t.Fatalf("Write to a full buffer = %d, want 0", n)
		}
		if got, want := readAll(b), []int16{0, 1, 2, 10, 11}; !slices.Equal(got, want) {
			t.Fatalf("read %v, want %v", got, want)
		}
	}
}

func TestOverwriteDiscardsOldest(t *testing.T) {
	b := newRing(t, 5, Overwrite)
	b.Write(ramp(0, 3))
	if n := b.Write(ramp(10, 4)); n != 4 {
		t.Fatalf("Write = %d, want 4", n)
	}
	if got, want := readAll(b), []int16{2, 10, 11, 12, 13}; !slices.Equal(got, want) {
		t.Fatalf("read %v, want %v", got, want)
	}
	b.Write(ramp(0, 2))
	if n := b.Write(ramp(100, 12)); n != 5 {
		t.Fatalf("Write of 12 into capacity 5 = %d, want 5", n)
	}
	if got, want := readAll(b), ramp(107, 5); !slices.Equal(got, want) {
		t.Fatalf("read %v, want %v", got, want)
	}
}

func TestShortRead(t *testing.T) {
	for _, b := range fifos(t, 8) {
		b.Write(ramp(1, 3))
		p := []int16{-1, -1, -1, -1, -1}
		if n := b.Read(p); n != 3 || !slices.Equal(p, []int16{1, 2, 3, -1, -1}) {
			t.Fatalf("Read = %d, %v; want 3 samples and the rest untouched", n, p)
		}
	}
}

func TestClipping(t *testing.T) {
	for _, b := range fifos(t, 6) {
		b.Write(ramp(0, 4))
		if n := b.Consume(10); n != 4 || b.Len() != 0 {
			t.Fatalf("Consume(10) = %d, Len %d", n, b.Len())
		}
		b.WriteSlices()
		if n := b.Commit(10); n != 6 || b.Len() != 6 {
			t.Fatalf("Commit(10) = %d, Len %d", n, b.Len())
		}
		if b.Commit(1) != 0 || b.Consume(-1) != 0 || b.Commit(-1) != 0 || b.Len() != 6 {
			t.Fatal("clipped Commit or Consume changed a full buffer")
		}
	}
}

// TestCommitClipsToView checks that Commit publishes only the write view, even
// after reads have freed more space, and that a Write expires the view.
func TestCommitClipsToView(t *testing.T) {
	for _, b := range []fifo{newRing(t, 7, Reject), newRing(t, 7, Overwrite), fifos(t, 7)[2]} {
		if b.Commit(3) != 0 {
			t.Fatal("Commit without a write view published samples")
		}
		b.Write(ramp(0, 5))
		w1, w2 := b.WriteSlices()
		if len(w1)+len(w2) != 2 {
			t.Fatalf("test setup: write view %d+%d", len(w1), len(w2))
		}
		w1[0], w1[1] = 5, 6
		readAll(b) // frees 5 more slots
		if n := b.Commit(1); n != 1 {
			t.Fatalf("first Commit(1) = %d", n)
		}
		if n := b.Commit(7); n != 1 {
			t.Fatalf("Commit(7) after a 2-sample view and Commit(1) = %d, want 1", n)
		}
		if got := readAll(b); !slices.Equal(got, []int16{5, 6}) {
			t.Fatalf("read %v, want [5 6]", got)
		}
		b.WriteSlices()
		b.Write(ramp(9, 1))
		if b.Commit(1) != 0 {
			t.Fatal("Commit after Write published samples")
		}
		b.WriteSlices()
		b.Reset()
		if b.Commit(1) != 0 || b.Len() != 0 {
			t.Fatal("Commit after Reset published samples")
		}
	}
}

// TestRingReadViewSurvivesRejectWrites holds a wrapped read view while writes
// and commits fill the free space, then checks the view and the read order.
func TestRingReadViewSurvivesRejectWrites(t *testing.T) {
	b := newRing(t, 7, Reject)
	b.Write(ramp(0, 5))
	b.Consume(4)
	b.Write(ramp(5, 4)) // unread 4..8 at slots 4, 5, 6, 0, 1
	first, second := b.ReadSlices()
	if len(second) == 0 {
		t.Fatal("test setup: read view does not wrap")
	}
	snapshot := slices.Concat(first, second)
	b.Write(ramp(9, 1))
	w1, w2 := b.WriteSlices()
	if len(w1) != 1 || len(w2) != 0 {
		t.Fatalf("test setup: write view %d+%d", len(w1), len(w2))
	}
	w1[0] = 10
	b.Commit(1)
	if n := b.Write(ramp(50, 3)); n != 0 {
		t.Fatalf("Write to full = %d", n)
	}
	if got := slices.Concat(first, second); !slices.Equal(got, snapshot) || !slices.Equal(got, ramp(4, 5)) {
		t.Fatalf("read view changed to %v, was %v", got, snapshot)
	}
	if got := readAll(b); !slices.Equal(got, ramp(4, 7)) {
		t.Fatalf("read %v, want %v", got, ramp(4, 7))
	}
}

// TestRingWriteViewSurvivesReads fills a write view while reads and consumes
// run, then commits it.
func TestRingWriteViewSurvivesReads(t *testing.T) {
	b := newRing(t, 7, Overwrite)
	b.Write(ramp(0, 6))
	b.Consume(2) // unread 2..5 at slots 2..5; free slots 6, 0, 1
	w1, w2 := b.WriteSlices()
	if len(w1)+len(w2) != 3 || len(w2) == 0 {
		t.Fatalf("test setup: write view %d+%d", len(w1), len(w2))
	}
	w1[0] = 6
	p := make([]int16, 2)
	b.Read(p)
	w2[0] = 7
	b.Consume(1)
	w2[1] = 8
	if n := b.Commit(3); n != 3 {
		t.Fatalf("Commit = %d", n)
	}
	if got := readAll(b); !slices.Equal(got, ramp(5, 4)) {
		t.Fatalf("read %v, want %v", got, ramp(5, 4))
	}
}

// TestAppendOnView checks that append on a full-length view reallocates
// instead of writing into the ring.
func TestAppendOnView(t *testing.T) {
	for _, b := range fifos(t, 8) {
		b.Write(ramp(1, 3))
		first, _ := b.ReadSlices()
		_ = append(first, 99)
		w, _ := b.WriteSlices()
		_ = append(w[:0], ramp(50, 6)...)
		if got := readAll(b); !slices.Equal(got, ramp(1, 3)) {
			t.Fatalf("read %v after append on views", got)
		}
		if b.Commit(5) != 5 {
			t.Fatal("Commit")
		}
		if got := readAll(b); slices.Contains(got, 99) {
			t.Fatalf("append on the read view wrote into the ring: %v", got)
		}
	}
}

// TestResetRing checks that a used, wrapped buffer behaves as a fresh one after
// Reset, including where its views split.
func TestResetRing(t *testing.T) {
	for capacity := 1; capacity <= 9; capacity++ {
		used, fresh := fifos(t, capacity), fifos(t, capacity)
		for i := range used {
			used[i].Write(ramp(0, capacity))
			used[i].Consume(capacity / 2)
			used[i].Write(ramp(100, capacity))
			used[i].Reset()
			trace := func(b fifo) []int {
				var out []int
				rng := testutil.SplitMix64{State: 7}
				p := make([]int16, 2*capacity)
				for range 200 {
					n := rng.Intn(2*capacity + 1)
					if rng.Intn(2) == 0 {
						out = append(out, b.Write(ramp(n, n)))
					} else {
						k := b.Read(p[:n])
						for _, v := range p[:k] {
							out = append(out, int(v))
						}
					}
					f1, f2 := b.ReadSlices()
					w1, w2 := b.WriteSlices()
					out = append(out, len(f1), len(f2), len(w1), len(w2), b.Len())
				}
				return out
			}
			if got, want := trace(used[i]), trace(fresh[i]); !slices.Equal(got, want) {
				t.Fatalf("cap %d, buffer %d: after Reset the trace differs from a fresh buffer", capacity, i)
			}
		}
	}
}

// TestChunkInvarianceRing writes and reads one stream in different chunk
// partitions, retrying what a write rejects, and expects the stream back.
func TestChunkInvarianceRing(t *testing.T) {
	in := testutil.WhiteNoise(20000, 16384, 3)
	for _, capacity := range []int{1, 7, 160, 1000} {
		for _, sizes := range [][2]int{{1, 1}, {80, 160}, {331, 97}, {4096, 4096}, {3, 1001}} {
			for _, b := range fifos(t, capacity) {
				if got := pump(b, in, sizes[0], sizes[1]); !slices.Equal(got, in) {
					t.Fatalf("cap %d, chunks %v: output differs from input", capacity, sizes)
				}
			}
		}
	}
}

// FuzzChunksRing is TestChunkInvarianceRing with fuzzed capacity and sizes.
func FuzzChunksRing(f *testing.F) {
	f.Add(uint16(7), uint16(3), uint16(5), false)
	f.Add(uint16(1), uint16(1000), uint16(1), true)
	in := testutil.WhiteNoise(5000, 16384, 4)
	f.Fuzz(func(t *testing.T, capacity, w, r uint16, useViews bool) {
		c := 1 + int(capacity)%2048
		for _, b := range fifos(t, c) {
			var got []int16
			if useViews {
				got = pumpViews(b, in, 1+int(w), 1+int(r))
			} else {
				got = pump(b, in, 1+int(w), 1+int(r))
			}
			if !slices.Equal(got, in) {
				t.Fatalf("cap %d, chunks %d/%d, views %v: output differs", c, w, r, useViews)
			}
		}
	})
}

// pump alternates a write of up to w samples (never overflowing) and a read of
// up to r samples until the whole input has come out, or a broken buffer stalls.
func pump(b fifo, in []int16, w, r int) []int16 {
	out := make([]int16, 0, len(in))
	p := make([]int16, r)
	for sent, i := 0, 0; len(out) < len(in) && i <= 2*len(in); i++ {
		chunk := in[sent:min(len(in), sent+w)]
		sent += b.Write(chunk[:min(len(chunk), b.Free())])
		out = append(out, p[:b.Read(p)]...)
	}
	return out
}

// pumpViews is pump using WriteSlices/Commit and ReadSlices/Consume.
func pumpViews(b fifo, in []int16, w, r int) []int16 {
	out := make([]int16, 0, len(in))
	for sent, i := 0, 0; len(out) < len(in) && i <= 2*len(in); i++ {
		w1, w2 := b.WriteSlices()
		chunk := in[sent:min(len(in), sent+w)]
		k := copy(w1, chunk)
		k += copy(w2, chunk[k:])
		sent += b.Commit(k)
		r1, r2 := b.ReadSlices()
		k = min(r, len(r1)+len(r2))
		out = append(out, slices.Concat(r1, r2)[:k]...)
		b.Consume(k)
	}
	return out
}

// TestNoAllocsRing covers every method on wrap, overflow and clipping paths.
func TestNoAllocsRing(t *testing.T) {
	for _, policy := range []Overflow{Reject, Overwrite} {
		b := newRing(t, 101, policy)
		in := ramp(0, 250)
		out := make([]int16, 160)
		testutil.AssertNoAllocs(t, 100, func() {
			b.Write(in[:80])
			b.Write(in) // overflow
			b.Read(out)
			w1, w2 := b.WriteSlices()
			copy(w1, in)
			copy(w2, in)
			b.Commit(500)
			r1, r2 := b.ReadSlices()
			_, _ = r1, r2
			b.Consume(37)
			b.Commit(-1)
			b.Consume(-1)
			b.Write(nil)
			b.Read(nil)
			_ = b.Len() + b.Free() + b.Cap()
			b.Read(out[:3])
			if b.Len() > 90 {
				b.Reset()
			}
		})
	}
}
