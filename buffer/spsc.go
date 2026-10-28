package buffer

import "sync/atomic"

// SPSCConfig configures an SPSC. Writes always reject the excess: overwriting
// would need the producer to move the consumer's index (requirements §16).
type SPSCConfig struct {
	Capacity int // samples, 1 to MaxCapacity; required, fixed for the SPSC's life
}

// cacheLine covers the cache lines of amd64 and arm64 (Apple uses 128 bytes).
// On a CPU with longer lines the indices may share one, which costs only speed.
const cacheLine = 128

// SPSC is a wait-free FIFO of int16 samples between one producer goroutine and
// one consumer goroutine. The package doc lists each side's methods.
type SPSC struct {
	buf  []int16
	_    [cacheLine]byte
	w    atomic.Uint32 // write index in [0, 2·Cap), stored by the producer only
	view int           // producer only: samples of the write view not yet committed
	_    [cacheLine]byte
	r    atomic.Uint32 // read index in [0, 2·Cap), stored by the consumer only
	_    [cacheLine - 4]byte
}

// NewSPSC validates cfg and allocates the storage, 2·Capacity bytes. It
// returns an error wrapping ErrConfig for an invalid cfg.
func NewSPSC(cfg SPSCConfig) (*SPSC, error) {
	if err := checkCapacity(cfg.Capacity); err != nil {
		return nil, err
	}
	return &SPSC{buf: make([]int16, cfg.Capacity)}, nil
}

// Cap returns the capacity in samples. Either side.
func (s *SPSC) Cap() int { return len(s.buf) }

// Len returns the number of samples the consumer can read. Consumer side; for
// the producer it is a snapshot.
func (s *SPSC) Len() int {
	r := int(s.r.Load())
	return s.count(int(s.w.Load()), r)
}

// Free returns the number of samples the producer can write. Producer side;
// for the consumer it is a snapshot.
func (s *SPSC) Free() int {
	w := int(s.w.Load())
	return len(s.buf) - s.count(w, int(s.r.Load()))
}

// Write stores p[:n] with n = min(len(p), Free()) and returns n. Producer
// side.
func (s *SPSC) Write(p []int16) int {
	s.view = 0
	if len(p) == 0 {
		return 0
	}
	w := int(s.w.Load())
	// Loading r orders the copy below after the consumer's reads of the slots.
	n := min(len(p), len(s.buf)-s.count(w, int(s.r.Load())))
	if n == 0 {
		return 0
	}
	k := copy(s.buf[s.slot(w):], p[:n])
	copy(s.buf, p[k:n])
	// Storing w publishes the samples just copied.
	s.w.Store(uint32(s.advance(w, n)))
	return n
}

// WriteSlices returns the free space, in write order, as two segments. Fill a
// prefix of it, then call Commit. Producer side.
func (s *SPSC) WriteSlices() (first, second []int16) {
	w := int(s.w.Load())
	s.view = len(s.buf) - s.count(w, int(s.r.Load()))
	return segments(s.buf, s.slot(w), s.view)
}

// Commit publishes the next n samples of the write view and returns how many,
// n clipped to what remains of the view (0 without one). Producer side.
func (s *SPSC) Commit(n int) int {
	n = min(n, s.view)
	if n <= 0 {
		return 0
	}
	s.view -= n
	w := int(s.w.Load())
	s.w.Store(uint32(s.advance(w, n)))
	return n
}

// Read moves up to len(p) of the oldest samples into p and returns how many.
// Consumer side.
func (s *SPSC) Read(p []int16) int {
	if len(p) == 0 {
		return 0
	}
	r := int(s.r.Load())
	// Loading w orders the copy below after the producer's writes of the slots.
	n := min(len(p), s.count(int(s.w.Load()), r))
	if n == 0 {
		return 0
	}
	first, second := segments(s.buf, s.slot(r), n)
	k := copy(p, first)
	copy(p[k:], second)
	// Storing r releases the slots just copied.
	s.r.Store(uint32(s.advance(r, n)))
	return n
}

// ReadSlices returns the readable samples, oldest first, as two segments.
// Consumer side.
func (s *SPSC) ReadSlices() (first, second []int16) {
	r := int(s.r.Load())
	return segments(s.buf, s.slot(r), s.count(int(s.w.Load()), r))
}

// Consume releases up to n of the oldest samples to the producer and returns
// how many, n clipped to [0, Len()]. Consumer side.
func (s *SPSC) Consume(n int) int {
	if n <= 0 {
		return 0
	}
	r := int(s.r.Load())
	n = min(n, s.count(int(s.w.Load()), r))
	if n == 0 {
		return 0
	}
	s.r.Store(uint32(s.advance(r, n)))
	return n
}

// Reset discards every sample and expires every view. Neither side may run
// during Reset, and the caller must synchronise both sides with it.
func (s *SPSC) Reset() {
	s.view = 0
	s.w.Store(0)
	s.r.Store(0)
}

// count returns the number of samples from index r up to index w, 0 to Cap.
// The clamp keeps a misused SPSC (two producers, say) from slicing out of range.
func (s *SPSC) count(w, r int) int {
	if w >= r {
		return min(w-r, len(s.buf))
	}
	return min(w+2*len(s.buf)-r, len(s.buf))
}

// slot maps an index in [0, 2·Cap) to its position in buf.
func (s *SPSC) slot(i int) int {
	if i >= len(s.buf) {
		return i - len(s.buf)
	}
	return i
}

// advance adds n, 0 to Cap, to an index modulo 2·Cap.
func (s *SPSC) advance(i, n int) int {
	i += n
	if i >= 2*len(s.buf) {
		i -= 2 * len(s.buf)
	}
	return i
}
