// Derived from SpeexDSP 1.2.1 (https://github.com/xiph/speexdsp, commit
// 1b28a0f61bc31162979e1f26f3981fc3637095c8): libspeexdsp/fftwrap.c.
//
// Copyright (C) 2005-2006 Jean-Marc Valin
//
// Use of this source is governed by the BSD-3-Clause licence in
// LICENSES/SpeexDSP-COPYING. Changes: ported to Go, FIXED_POINT with
// USE_KISS_FFT only; length checks return errors.

package fft

import (
	"errors"
	"fmt"

	"github.com/danieldonoghue/govoip-dsp/internal/fixed"
)

// MaxSize is the largest supported transform size.
const MaxSize = 4096

// Sentinel errors.
var (
	ErrConfig     = errors.New("fft: invalid configuration")
	ErrBufferSize = errors.New("fft: buffer length differs from the transform size")
)

// Config configures an FFT.
type Config struct {
	// Size is the number of real samples per transform: even, 2 to MaxSize,
	// and Size/2 without a prime factor above 17 (see Supported).
	Size int
}

// FFT is the state of spx_fft_init: a forward and an inverse real
// transform of one size, with their scratch buffers.
type FFT struct {
	n        int
	forward  realState
	backward realState
}

// Supported reports whether New accepts size.
func Supported(size int) bool {
	if size < 2 || size > MaxSize || size%2 != 0 {
		return false
	}
	var f [2 * maxFactors]int
	kfFactor(size/2, f[:])
	for i := 0; ; i += 2 {
		if f[i] > maxRadix {
			return false
		}
		if f[i+1] <= 1 {
			return true
		}
	}
}

// New is spx_fft_init (fftwrap.c:304). A size that Supported rejects gives
// an error wrapping ErrConfig (the reference returns NULL or aborts).
func New(cfg Config) (*FFT, error) {
	if !Supported(cfg.Size) {
		return nil, fmt.Errorf("%w: size %d (want even, 2 to %d, size/2 without a prime factor above %d)",
			ErrConfig, cfg.Size, MaxSize, maxRadix)
	}
	f := &FFT{n: cfg.Size}
	f.forward.init(cfg.Size, false)
	f.backward.init(cfg.Size, true)
	return f, nil
}

// Size returns the transform size.
func (f *FFT) Size() int { return f.n }

// Forward is spx_fft (fftwrap.c:324), rewriting in as spx_fft does. The
// package doc gives the layout, scaling, length and aliasing rules.
func (f *FFT) Forward(in, out []int16) error {
	if f.n == 0 {
		return ErrConfig
	}
	if len(in) != f.n || len(out) != f.n {
		return ErrBufferSize
	}
	shift := maximizeRange(in, 32000)
	f.forward.forward(in, out)
	renormRange(in, shift)
	renormRange(out, shift)
	return nil
}

// Inverse is spx_ifft (fftwrap.c:348): the unscaled inverse, leaving in
// unchanged. The package doc gives the layout, length and aliasing rules.
func (f *FFT) Inverse(in, out []int16) error {
	if f.n == 0 {
		return ErrConfig
	}
	if len(in) != f.n || len(out) != f.n {
		return ErrBufferSize
	}
	f.backward.inverse(in, out)
	return nil
}

// maximizeRange is maximize_range (fftwrap.c:45) with in == out. As in C,
// max_val is a spx_word16_t, so -(-32768) wraps.
func maximizeRange(x []int16, bound int16) uint {
	var maxVal int16
	for _, v := range x {
		if int32(v) > int32(maxVal) {
			maxVal = v
		}
		if fixed.Neg16(v) > int32(maxVal) {
			maxVal = int16(fixed.Neg16(v))
		}
	}
	var shift uint
	for maxVal <= bound>>1 && maxVal != 0 {
		maxVal = int16(int32(maxVal) << 1)
		shift++
	}
	for i, v := range x {
		x[i] = int16(fixed.Shl16(v, shift))
	}
	return shift
}

// renormRange is renorm_range (fftwrap.c:69) with in == out.
func renormRange(x []int16, shift uint) {
	for i, v := range x {
		x[i] = fixed.PShr16(v, shift)
	}
}
