package buffer

import (
	"errors"
	"fmt"
)

// MaxCapacity is the largest capacity in samples. It keeps the SPSC index
// arithmetic, which reaches nearly 4·Capacity, inside a 32-bit int.
const MaxCapacity = 512 * 1024 * 1024

// ErrConfig reports an invalid Config or SPSCConfig. New and NewSPSC wrap
// it; no other function returns an error.
var ErrConfig = errors.New("buffer: invalid configuration")

// Overflow selects what Ring.Write does with samples that do not fit. The zero
// value is invalid, so every Config chooses a policy.
type Overflow int

const (
	// Reject stores only the samples that fit.
	Reject Overflow = iota + 1
	// Overwrite discards the oldest unread samples to make room.
	Overwrite
)

// Config configures a Ring. Both fields are required and fixed for the
// Ring's life.
type Config struct {
	Capacity int      // samples, 1 to MaxCapacity; the storage is 2·Capacity bytes
	Overflow Overflow // Reject or Overwrite; the zero value is invalid
}

// Ring is a fixed-capacity FIFO of int16 samples for a single owner. It is not
// safe for concurrent use; use one per stream, and SPSC between goroutines.
type Ring struct {
	buf       []int16
	r         int // index of the oldest unread sample
	n         int // number of unread samples, 0 to len(buf)
	view      int // samples of the write view not yet committed
	overwrite bool
}

// New validates cfg and allocates the storage, 2·Capacity bytes. It returns
// an error wrapping ErrConfig for an invalid cfg.
func New(cfg Config) (*Ring, error) {
	if err := checkCapacity(cfg.Capacity); err != nil {
		return nil, err
	}
	if cfg.Overflow != Reject && cfg.Overflow != Overwrite {
		return nil, fmt.Errorf("%w: overflow policy %d, want Reject or Overwrite", ErrConfig, cfg.Overflow)
	}
	return &Ring{buf: make([]int16, cfg.Capacity), overwrite: cfg.Overflow == Overwrite}, nil
}

func checkCapacity(c int) error {
	if c < 1 || c > MaxCapacity {
		return fmt.Errorf("%w: capacity %d, want 1 to %d", ErrConfig, c, MaxCapacity)
	}
	return nil
}

// Cap returns the capacity in samples.
func (b *Ring) Cap() int { return len(b.buf) }

// Len returns the number of unread samples.
func (b *Ring) Len() int { return b.n }

// Free returns the number of samples that fit without overflow.
func (b *Ring) Free() int { return len(b.buf) - b.n }

// Write stores samples from p and returns how many: p[:min(len(p), Free())]
// under Reject, the last min(len(p), Cap()) samples of p under Overwrite.
func (b *Ring) Write(p []int16) int {
	b.view = 0
	if b.overwrite {
		if len(p) > len(b.buf) {
			p = p[len(p)-len(b.buf):]
		}
		if d := len(p) - b.Free(); d > 0 {
			b.Consume(d)
		}
	} else if len(p) > b.Free() {
		p = p[:b.Free()]
	}
	w := b.wpos()
	k := copy(b.buf[w:], p)
	copy(b.buf, p[k:])
	b.n += len(p)
	return len(p)
}

// Read moves up to len(p) of the oldest unread samples into p and returns how
// many it moved.
func (b *Ring) Read(p []int16) int {
	first, second := b.ReadSlices()
	k := copy(p, first)
	k += copy(p[k:], second)
	return b.Consume(k)
}

// ReadSlices returns the unread samples, oldest first, as two segments; second
// is empty unless they wrap.
func (b *Ring) ReadSlices() (first, second []int16) {
	return segments(b.buf, b.r, b.n)
}

// Consume discards up to n of the oldest unread samples and returns how many,
// n clipped to [0, Len()].
func (b *Ring) Consume(n int) int {
	n = clip(n, b.n)
	b.r += n
	if b.r >= len(b.buf) {
		b.r -= len(b.buf)
	}
	b.n -= n
	return n
}

// WriteSlices returns the free space, in write order, as two segments with a
// total length of Free(). Fill a prefix of it, then call Commit.
func (b *Ring) WriteSlices() (first, second []int16) {
	b.view = b.Free()
	return segments(b.buf, b.wpos(), b.view)
}

// Commit makes the next n samples of the write view readable and returns how
// many, n clipped to what remains of the view (0 without one).
func (b *Ring) Commit(n int) int {
	n = clip(n, b.view)
	b.view -= n
	b.n += n
	return n
}

// Reset discards every unread sample and expires every view. It keeps the
// storage and does not allocate.
func (b *Ring) Reset() {
	b.r, b.n, b.view = 0, 0, 0
}

func (b *Ring) wpos() int {
	w := b.r + b.n
	if w >= len(b.buf) {
		w -= len(b.buf)
	}
	return w
}

// segments returns n samples of buf from index start, wrapping at the end. Each
// segment's capacity equals its length, so append cannot write into buf.
func segments(buf []int16, start, n int) (first, second []int16) {
	if n == 0 {
		return nil, nil
	}
	end := start + n
	if end <= len(buf) {
		return buf[start:end:end], nil
	}
	end -= len(buf)
	return buf[start:len(buf):len(buf)], buf[0:end:end]
}

func clip(n, limit int) int {
	return max(0, min(n, limit))
}
