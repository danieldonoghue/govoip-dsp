package buffer

import (
	"fmt"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

// seqSample is the sample with sequence number i: a hash, so a lost,
// duplicated or stale sample almost never matches by accident.
func seqSample(i uint64) int16 {
	r := testutil.SplitMix64{State: i}
	return int16(r.Next())
}

// seqChecker checks a stream against seqSample, sample by sample.
type seqChecker struct {
	next uint64
}

// peek checks p against the samples that follow without consuming them.
func (c *seqChecker) peek(p []int16) error {
	for j, v := range p {
		if want := seqSample(c.next + uint64(j)); v != want {
			return fmt.Errorf("sample %d: got %d, want %d", c.next+uint64(j), v, want)
		}
	}
	return nil
}

func (c *seqChecker) take(p []int16) error {
	err := c.peek(p)
	c.next += uint64(len(p))
	return err
}

// skip records n samples dropped without reading: the next one expected is n
// further on.
func (c *seqChecker) skip(n int) {
	c.next += uint64(n)
}

// TestSeqCheckerDetects feeds the checker one lost, one duplicated and one
// stale sample, each in an otherwise correct stream.
func TestSeqCheckerDetects(t *testing.T) {
	good := make([]int16, 3000)
	for i := range good {
		good[i] = seqSample(uint64(i))
	}
	var c seqChecker
	if err := c.take(good); err != nil {
		t.Fatalf("correct stream rejected: %v", err)
	}
	streams := map[string][]int16{
		"lost":       slices.Delete(slices.Clone(good), 1500, 1501),
		"duplicated": slices.Insert(slices.Clone(good), 1500, good[1499]),
		"stale":      append(slices.Clone(good[:1500]), append([]int16{good[1500-1000]}, good[1501:]...)...),
	}
	for name, s := range streams {
		var c seqChecker
		err := c.take(s[:1000])
		if err == nil {
			err = c.take(s[1000:])
		}
		if err == nil {
			t.Errorf("%s sample not detected", name)
		} else {
			t.Logf("%s: %v", name, err)
		}
	}
	// A drop of 1000 samples recorded after sample 999: the gap in the stream
	// must be exactly 1000.
	gaps := map[string]int{"gap equal to the drop": 1000, "gap one longer": 1001, "gap one shorter": 999}
	for name, gap := range gaps {
		var c seqChecker
		err := c.take(good[:1000])
		c.skip(1000)
		if err == nil {
			err = c.take(good[1000+gap:])
		}
		if (err == nil) != (gap == 1000) {
			t.Errorf("%s: checker returned %v", name, err)
		}
	}
}

const stressTimeout = 2 * time.Minute

// stressSamples is the number of samples each stress run moves.
func stressSamples() int {
	if raceEnabled || testing.Short() {
		return 200 * 1000
	}
	return 1000 * 1000
}

// TestStressSPSC runs a real producer and consumer goroutine through each
// capacity, with random chunk sizes and both access styles on each side.
func TestStressSPSC(t *testing.T) {
	t.Logf("GOMAXPROCS %d, race %v, %d samples per capacity", runtime.GOMAXPROCS(0), raceEnabled, stressSamples())
	for _, capacity := range []int{1, 2, 3, 7, 64, 1000} {
		t.Run(fmt.Sprint(capacity), func(t *testing.T) {
			s, err := NewSPSC(SPSCConfig{Capacity: capacity})
			if err != nil {
				t.Fatal(err)
			}
			stressSPSC(t, s, stressSamples(), testutil.Seed(fmt.Sprint("stress", capacity)))
		})
	}
}

// stressSPSC moves total samples through s. Chunks go up to 2·Cap+3 samples,
// but at most 300, so large capacities still see thousands of operations.
func stressSPSC(t *testing.T, s *SPSC, total int, seed uint64) {
	maxChunk := min(2*s.Cap()+3, 300)
	var failed atomic.Bool
	fail := func(format string, args ...any) {
		t.Helper()
		failed.Store(true)
		t.Errorf(format, args...)
	}
	// A broken buffer can stall both sides; the deadline turns that into a failure.
	deadline := time.Now().Add(stressTimeout)
	stalled := func() bool {
		if time.Now().After(deadline) {
			fail("no progress before the %v deadline", stressTimeout)
		}
		return failed.Load()
	}
	var delivered, dropped int // consumer only; read after Wait
	var wg sync.WaitGroup
	wg.Go(func() { // producer
		rng := testutil.SplitMix64{State: seed}
		p := make([]int16, maxChunk)
		for sent := 0; sent < total && !stalled(); {
			size := min(rng.Intn(len(p)+1), total-sent)
			n := 0
			if rng.Intn(2) == 0 {
				for i := range p[:size] {
					p[i] = seqSample(uint64(sent + i))
				}
				n = s.Write(p[:size])
			} else {
				w1, w2 := s.WriteSlices()
				n = min(size, len(w1)+len(w2))
				for i := range n {
					if i < len(w1) {
						w1[i] = seqSample(uint64(sent + i))
					} else {
						w2[i-len(w1)] = seqSample(uint64(sent + i))
					}
				}
				runtime.Gosched() // hold the filled view while the consumer runs
				if c := s.Commit(n); c != n {
					fail("Commit(%d) = %d after filling the view", n, c)
				}
			}
			sent += n
			if n == 0 {
				runtime.Gosched()
			}
		}
	})
	wg.Go(func() { // consumer
		rng := testutil.SplitMix64{State: ^seed}
		p := make([]int16, maxChunk)
		var c seqChecker
		for int(c.next) < total && !stalled() {
			size := rng.Intn(len(p) + 1)
			n := 0
			switch mode := rng.Intn(5); {
			case mode == 4: // drop without reading, as after a stall
				want := size
				if rng.Intn(2) == 0 { // keep only the newest k samples
					want = s.Len() - rng.Intn(2*s.Cap()+1)
				}
				n = s.Consume(want)
				if n < 0 || n > max(want, 0) {
					fail("Consume(%d) = %d", want, n)
				}
				c.skip(n)
				dropped += n
			case mode < 2:
				n = s.Read(p[:size])
				if err := c.take(p[:n]); err != nil {
					fail("Read: %v", err)
				}
				delivered += n
			default:
				r1, r2 := s.ReadSlices()
				n = min(size, len(r1)+len(r2))
				view := slices.Concat(r1, r2)[:n]
				if err := c.peek(view); err != nil {
					fail("ReadSlices before yield: %v", err)
				}
				runtime.Gosched() // hold the view while the producer runs
				n1 := min(n, len(r1))
				if err := c.take(r1[:n1]); err != nil {
					fail("ReadSlices first segment after yield: %v", err)
				}
				if err := c.take(r2[:n-n1]); err != nil {
					fail("ReadSlices second segment after yield: %v", err)
				}
				if k := s.Consume(n); k != n {
					fail("Consume(%d) = %d", n, k)
				}
				delivered += n
			}
			if n == 0 {
				runtime.Gosched()
			}
		}
	})
	wg.Wait()
	if failed.Load() {
		return
	}
	if s.Len() != 0 {
		t.Errorf("Len %d after the consumer took every sample", s.Len())
	}
	if delivered+dropped != total {
		t.Errorf("%d delivered + %d dropped, want %d written", delivered, dropped, total)
	}
	t.Logf("%d delivered, %d dropped", delivered, dropped)
	if dropped == 0 || delivered == 0 {
		t.Errorf("%d delivered, %d dropped: the run did not exercise both", delivered, dropped)
	}
}

// TestResetSPSC resets a used, wrapped SPSC, checks it is laid out as a fresh
// one, and stresses it with two goroutines.
func TestResetSPSC(t *testing.T) {
	s, err := NewSPSC(SPSCConfig{Capacity: 7})
	if err != nil {
		t.Fatal(err)
	}
	s.Write(ramp(0, 5))
	s.Consume(3)
	s.Write(ramp(5, 4)) // wrapped
	s.Reset()
	if s.Len() != 0 || s.Free() != 7 {
		t.Fatalf("after Reset: Len %d Free %d", s.Len(), s.Free())
	}
	w1, w2 := s.WriteSlices()
	if len(w1) != 7 || len(w2) != 0 {
		t.Fatalf("after Reset the write view is %d+%d, want 7+0 as fresh", len(w1), len(w2))
	}
	stressSPSC(t, s, 50*1000, 1)
}

// TestNoAllocsSPSC covers every method on wrap, full, empty and clipping
// paths, on one goroutine.
func TestNoAllocsSPSC(t *testing.T) {
	s, err := NewSPSC(SPSCConfig{Capacity: 101})
	if err != nil {
		t.Fatal(err)
	}
	in := ramp(0, 250)
	out := make([]int16, 160)
	testutil.AssertNoAllocs(t, 100, func() {
		s.Write(in[:80])
		s.Write(in) // rejected excess
		s.Read(out)
		w1, w2 := s.WriteSlices()
		copy(w1, in)
		copy(w2, in)
		s.Commit(500)
		r1, r2 := s.ReadSlices()
		_, _ = r1, r2
		s.Consume(37)
		s.Commit(-1)
		s.Consume(-1)
		s.Write(nil)
		s.Read(nil)
		_ = s.Len() + s.Free() + s.Cap()
		s.Read(out[:3])
		s.Write(in[:50])
		s.Consume(10)          // drop with no read view
		s.Consume(s.Len() - 7) // keep the newest 7
		s.Consume(s.Len() - 500)
		if s.Len() > 90 {
			s.Reset()
		}
	})
}
