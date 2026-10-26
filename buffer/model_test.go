package buffer

import (
	"slices"
	"testing"
)

// fifo is the method set Ring and SPSC share, so one model check covers both.
type fifo interface {
	Cap() int
	Len() int
	Free() int
	Write(p []int16) int
	Read(p []int16) int
	ReadSlices() (first, second []int16)
	WriteSlices() (first, second []int16)
	Commit(n int) int
	Consume(n int) int
	Reset()
}

// model is a slice used as a queue, with no wrap-around: correct by inspection.
type model struct {
	data      []int16
	capacity  int
	overwrite bool
}

func (m *model) write(p []int16) int {
	if !m.overwrite {
		n := min(len(p), m.capacity-len(m.data))
		m.data = append(m.data, p[:n]...)
		return n
	}
	m.data = append(m.data, p...)
	if len(m.data) > m.capacity {
		m.data = m.data[len(m.data)-m.capacity:]
	}
	return min(len(p), m.capacity)
}

func (m *model) read(n int) []int16 {
	n = max(0, min(n, len(m.data)))
	out := slices.Clone(m.data[:n])
	m.data = m.data[n:]
	return out
}

// checker drives a fifo and the model with the same operations and compares
// them after every step.
type checker struct {
	t   *testing.T
	b   fifo
	m   model
	seq int16 // next sample value written, so order errors show
	tmp []int16
	// pending holds the filled, uncommitted rest of a live write view, or nil.
	pending []int16
}

func newChecker(t *testing.T, b fifo, overwrite bool) *checker {
	return &checker{t: t, b: b, m: model{capacity: b.Cap(), overwrite: overwrite}, tmp: make([]int16, 1024)}
}

func (c *checker) next(n int) []int16 {
	if n > len(c.tmp) {
		c.tmp = make([]int16, n)
	}
	p := c.tmp[:n]
	for i := range p {
		p[i] = c.seq
		c.seq++
	}
	return p
}

// op applies operation code with size argument n, then checks the state.
func (c *checker) op(code byte, n int) {
	t := c.t
	t.Helper()
	switch code % 12 {
	case 0: // Write
		c.pending = nil
		p := c.next(n)
		if got, want := c.b.Write(p), c.m.write(p); got != want {
			t.Fatalf("Write(%d) = %d, want %d", len(p), got, want)
		}
	case 1: // Read
		p := make([]int16, n)
		got := c.b.Read(p)
		want := c.m.read(n)
		if got != len(want) || !slices.Equal(p[:got], want) {
			t.Fatalf("Read(%d) = %d %v, want %v", n, got, p[:got], want)
		}
		if !allZero(p[got:]) {
			t.Fatalf("Read(%d) wrote beyond the %d samples it returned", n, got)
		}
	case 2: // WriteSlices, fill, Commit
		c.pending = nil
		first, second := c.b.WriteSlices()
		k := min(n, len(first)+len(second))
		p := c.next(k)
		copy(second, p[copy(first, p):])
		if got, want := c.b.Commit(n), c.m.write(p); got != want {
			t.Fatalf("Commit(%d) = %d, want %d", n, got, want)
		}
	case 3: // ReadSlices, Consume
		first, second := c.b.ReadSlices()
		view := slices.Concat(first, second)
		got := c.b.Consume(n)
		want := c.m.read(n)
		if got != len(want) || !slices.Equal(view[:got], want) {
			t.Fatalf("Consume(%d) = %d of %v, want %v", n, got, view, want)
		}
	case 4: // Commit and Consume with negative or zero counts
		if got := c.b.Commit(-n); got != 0 {
			t.Fatalf("Commit(%d) = %d, want 0", -n, got)
		}
		if got := c.b.Consume(-n); got != 0 {
			t.Fatalf("Consume(%d) = %d, want 0", -n, got)
		}
	case 5: // empty and nil slices
		c.pending = nil
		if c.b.Write(nil) != 0 || c.b.Write([]int16{}) != 0 || c.b.Read(nil) != 0 || c.b.Read([]int16{}) != 0 {
			t.Fatal("zero-length Write or Read returned nonzero")
		}
	case 6: // Reset, rarely
		if n%16 == 0 {
			c.pending = nil
			c.b.Reset()
			c.m.data = c.m.data[:0]
		}
	case 7: // Write longer than the capacity
		c.pending = nil
		p := c.next(c.b.Cap() + 1 + n)
		if got, want := c.b.Write(p), c.m.write(p); got != want {
			t.Fatalf("Write(%d) = %d, want %d", len(p), got, want)
		}
	case 8: // WriteSlices and fill it all, committed by later operations
		first, second := c.b.WriteSlices()
		p := c.next(len(first) + len(second))
		copy(second, p[copy(first, p):])
		c.pending = slices.Clone(p)
	case 9: // Commit part of a view opened earlier, possibly after reads
		want := max(0, min(n, len(c.pending)))
		if got := c.b.Commit(n); got != want {
			t.Fatalf("Commit(%d) of a %d-sample pending view = %d", n, len(c.pending), got)
		}
		c.m.write(c.pending[:want])
		c.pending = c.pending[want:]
	case 10: // drop the oldest samples without reading them
		if got, want := c.b.Consume(n), len(c.m.read(n)); got != want {
			t.Fatalf("Consume(%d) without a view = %d, want %d", n, got, want)
		}
	case 11: // keep only the newest k samples, k from -1 to Len()+1
		old := c.b.Len()
		k := n%(old+3) - 1
		if got, want := c.b.Consume(old-k), len(c.m.read(old-k)); got != want {
			t.Fatalf("Consume(Len() - %d) = %d, want %d", k, got, want)
		}
		if c.b.Len() != max(0, min(k, old)) {
			t.Fatalf("keeping the newest %d of %d left Len %d", k, old, c.b.Len())
		}
	}
	c.check()
}

func (c *checker) check() {
	t := c.t
	t.Helper()
	b := c.b
	if b.Len() != len(c.m.data) || b.Free() != b.Cap()-len(c.m.data) {
		t.Fatalf("Len %d Free %d, want %d and %d", b.Len(), b.Free(), len(c.m.data), b.Cap()-len(c.m.data))
	}
	first, second := b.ReadSlices()
	if got := slices.Concat(first, second); !slices.Equal(got, c.m.data) {
		t.Fatalf("read view %v, want %v", got, c.m.data)
	}
	checkSegments(t, "read", first, second, b.Len())
	if c.pending == nil { // do not replace a live view the operations use
		first, second = b.WriteSlices()
		checkSegments(t, "write", first, second, b.Free())
		b.Write(nil) // expire it, so a later Commit has no view
	}
}

// checkSegments checks a view's total length, that each segment's capacity is
// its length, and that the second segment is used only after the first.
func checkSegments(t *testing.T, name string, first, second []int16, total int) {
	t.Helper()
	if len(first)+len(second) != total {
		t.Fatalf("%s view has %d+%d samples, want %d", name, len(first), len(second), total)
	}
	if cap(first) != len(first) || cap(second) != len(second) {
		t.Fatalf("%s view capacities %d, %d exceed lengths %d, %d", name, cap(first), cap(second), len(first), len(second))
	}
	if len(first) == 0 && len(second) != 0 {
		t.Fatalf("%s view has an empty first segment and a nonempty second", name)
	}
}

func allZero(p []int16) bool {
	for _, v := range p {
		if v != 0 {
			return false
		}
	}
	return true
}

// runOps decodes data as a capacity followed by (operation, size) pairs.
func runOps(t *testing.T, data []byte, newFIFO func(capacity int) (fifo, bool)) {
	if len(data) < 1 {
		return
	}
	capacity := 1 + int(data[0])%300
	b, overwrite := newFIFO(capacity)
	c := newChecker(t, b, overwrite)
	c.check()
	for ops := data[1:]; len(ops) >= 2; ops = ops[2:] {
		c.op(ops[0], int(ops[1])*int(1+ops[0]>>3)%(2*capacity+2))
	}
}

func fuzzSeeds(f *testing.F) {
	f.Add([]byte{0, 0, 1, 1, 1})
	f.Add([]byte{6, 0, 5, 1, 3, 0, 7, 2, 2, 3, 1, 7, 9})
	f.Add([]byte{63, 0, 40, 0, 40, 1, 30, 0, 50, 3, 70, 2, 80, 1, 100, 7, 10, 4, 3, 6, 0})
	f.Add([]byte{255, 7, 255, 1, 100, 2, 255, 3, 255, 0, 200, 1, 255})
}

// FuzzRing checks Ring under both policies against the model.
func FuzzRing(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, policy := range []Overflow{Reject, Overwrite} {
			runOps(t, data, func(capacity int) (fifo, bool) {
				b, err := New(Config{Capacity: capacity, Overflow: policy})
				if err != nil {
					t.Fatal(err)
				}
				return b, policy == Overwrite
			})
		}
	})
}

// FuzzSPSC checks SPSC on one goroutine against the model with Reject.
func FuzzSPSC(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		runOps(t, data, func(capacity int) (fifo, bool) {
			s, err := NewSPSC(SPSCConfig{Capacity: capacity})
			if err != nil {
				t.Fatal(err)
			}
			return s, false
		})
	})
}

// TestModelOps runs a long seeded operation sequence for capacities that the
// fuzz seeds do not reach, so plain go test covers them.
func TestModelOps(t *testing.T) {
	for _, capacity := range []int{1, 2, 3, 7, 64, 160, 1000, 4096} {
		data := make([]byte, 1+2*20000)
		x := uint32(capacity)
		for i := range data {
			x = x*1664525 + 1013904223
			data[i] = byte(x >> 24)
		}
		for _, policy := range []Overflow{Reject, Overwrite} {
			b, err := New(Config{Capacity: capacity, Overflow: policy})
			if err != nil {
				t.Fatal(err)
			}
			c := newChecker(t, b, policy == Overwrite)
			s, err := NewSPSC(SPSCConfig{Capacity: capacity})
			if err != nil {
				t.Fatal(err)
			}
			cs := newChecker(t, s, false)
			for ops := data[1:]; len(ops) >= 2; ops = ops[2:] {
				n := int(ops[1]) * (1 + capacity/128)
				c.op(ops[0], n)
				if policy == Reject {
					cs.op(ops[0], n)
				}
			}
		}
	}
}
