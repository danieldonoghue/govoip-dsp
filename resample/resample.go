// Derived from SpeexDSP 1.2.1 (https://github.com/xiph/speexdsp, commit
// 1b28a0f61bc31162979e1f26f3981fc3637095c8): libspeexdsp/resample.c.
//
// Copyright (C) 2007-2008 Jean-Marc Valin
// Copyright (C) 2008      Thorvald Natvig
//
// Use of this source is governed by the BSD-3-Clause licence in
// LICENSES/SpeexDSP-COPYING. Changes: ported to Go, with the API described
// in doc.go.

package resample

import (
	"errors"
	"fmt"
	"math"
)

// Quality levels, as in speex_resampler.h.
const (
	QualityMin     = 0
	QualityMax     = 10
	QualityDefault = 4
	QualityVoIP    = 3
	QualityDesktop = 5
)

// Sentinel errors. The process methods return only these, so they do not
// allocate.
var (
	// ErrConfig reports an invalid argument (RESAMPLER_ERR_INVALID_ARG).
	ErrConfig = errors.New("resample: invalid argument")
	// ErrAllocFailed reports that the filter for the requested
	// configuration exceeds the size limits (RESAMPLER_ERR_ALLOC_FAILED).
	// Above the Go-only cap, or for a filter length that wraps to 0 (see
	// the package doc), nothing changes. Where the reference's ratio or
	// table limits reject a rate or quality change, the resampler outputs
	// zeros, and every process call returns it, until a later change
	// succeeds.
	ErrAllocFailed = errors.New("resample: filter exceeds the size limits")
	// ErrOverflow reports that a ratio change would overflow the fractional
	// position (RESAMPLER_ERR_OVERFLOW). The resampler is left unchanged.
	ErrOverflow = errors.New("resample: ratio change overflows the fractional position")
	// ErrChannel reports a channel index outside [0, Channels()).
	ErrChannel = errors.New("resample: channel index out of range")
	// ErrStride reports a stride below 1.
	ErrStride = errors.New("resample: stride below 1")
	// ErrBufferSize reports an interleaved buffer whose length is not a
	// multiple of the channel count.
	ErrBufferSize = errors.New("resample: interleaved length not a multiple of the channel count")
)

// Config configures a Resampler.
type Config struct {
	// Channels is the number of channels, at least 1.
	Channels int
	// InRate and OutRate are the sampling rates in Hz. Unless RatioNum and
	// RatioDen are set, they also give the ratio and must be at least 1.
	InRate, OutRate int
	// RatioNum and RatioDen, when both are non-zero, set the resampling
	// ratio exactly (speex_resampler_init_frac); InRate and OutRate are
	// then only reported back by Rate. Leave both zero to use
	// InRate/OutRate; setting only one is an error.
	RatioNum, RatioDen int
	// Quality is 0 (fastest) to 10 (best). The zero value is quality 0.
	Quality int
}

type kernel uint8

const (
	kernelDirect kernel = iota
	kernelInterpolate
	kernelZero
)

// fixedStackAlloc is FIXED_STACK_ALLOC with VAR_ARRAYS (resample.c:109): the
// largest output chunk of the float32 path.
const fixedStackAlloc = 8192

// maxFrames is the largest length a process call offers to the reference
// code; see "Deviations" in the package doc.
const maxFrames = math.MaxInt32

// A Resampler converts a stream between sampling rates. See the package doc.
type Resampler struct {
	inRate, outRate  uint32
	numRate, denRate uint32
	quality          int
	nbChannels       uint32
	filtLen          uint32
	memAllocSize     uint32
	bufferSize       uint32
	intAdvance       int32
	fracAdvance      int32
	cutoff           float32
	oversample       uint32
	initialised      bool
	started          bool
	kernel           kernel

	lastSample   []int32
	sampFracNum  []uint32
	magicSamples []uint32

	mem       []int16
	sincTable []int16

	ystack [fixedStackAlloc]int16
}

func checkU32(v int) bool { return v >= 0 && uint64(v) <= math.MaxUint32 }

// New validates cfg and builds the filter (speex_resampler_init_frac). It
// returns an error wrapping ErrConfig for an invalid configuration, and
// ErrAllocFailed above the resource cap or where the reference's size
// limits reject the filter.
func New(cfg Config) (*Resampler, error) {
	num, den := cfg.RatioNum, cfg.RatioDen
	if num == 0 && den == 0 {
		num, den = cfg.InRate, cfg.OutRate
	}
	switch {
	case cfg.Channels < 1 || !checkU32(cfg.Channels):
		return nil, fmt.Errorf("%w: %d channels", ErrConfig, cfg.Channels)
	case cfg.Quality < QualityMin || cfg.Quality > QualityMax:
		return nil, fmt.Errorf("%w: quality %d", ErrConfig, cfg.Quality)
	case !checkU32(cfg.InRate) || !checkU32(cfg.OutRate):
		return nil, fmt.Errorf("%w: rates %d and %d Hz", ErrConfig, cfg.InRate, cfg.OutRate)
	case num < 1 || den < 1 || !checkU32(num) || !checkU32(den):
		return nil, fmt.Errorf("%w: ratio %d:%d", ErrConfig, num, den)
	}
	if cfg.Channels > maxChannels {
		return nil, fmt.Errorf("%w: %d channels, more than %d", ErrAllocFailed, cfg.Channels, maxChannels)
	}
	r := &Resampler{
		quality:    cfg.Quality,
		nbChannels: uint32(cfg.Channels),
		cutoff:     1,
		bufferSize: 160,
	}
	if f := gcd(uint32(num), uint32(den)); r.checkCap(cfg.Quality, uint32(num)/f, uint32(den)/f) != nil {
		return nil, fmt.Errorf("%w: ratio %d:%d at quality %d needs more than %d filter entries",
			ErrAllocFailed, num, den, cfg.Quality, maxEntries)
	}
	r.setRateFrac(uint32(num), uint32(den), uint32(cfg.InRate), uint32(cfg.OutRate))
	// update_filter runs before the per-channel arrays exist, which it does
	// not touch before the first process call, so that a channel count the
	// size limits reject fails before allocating for it.
	if err := r.updateFilter(); err != nil {
		return nil, fmt.Errorf("%w: %d channels, ratio %d:%d, quality %d", err, cfg.Channels, num, den, cfg.Quality)
	}
	r.lastSample = make([]int32, cfg.Channels)
	r.sampFracNum = make([]uint32, cfg.Channels)
	r.magicSamples = make([]uint32, cfg.Channels)
	r.initialised = true
	return r, nil
}

func gcd(a, b uint32) uint32 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// setRateFrac stores the ratio, reduced, and rescales the fractional
// positions; the caller has checked that the rescaling cannot overflow.
func (r *Resampler) setRateFrac(num, den, inRate, outRate uint32) {
	oldDen := r.denRate
	r.inRate, r.outRate = inRate, outRate
	fact := gcd(num, den)
	r.numRate, r.denRate = num/fact, den/fact
	if oldDen > 0 {
		for i := range r.sampFracNum {
			v, _ := multiplyFrac(r.sampFracNum[i], r.denRate, oldDen)
			if v >= r.denRate {
				v = r.denRate - 1
			}
			r.sampFracNum[i] = v
		}
	}
}

// SetRate sets the rates in Hz, which also give the ratio
// (speex_resampler_set_rate). See SetRateFrac for the semantics.
func (r *Resampler) SetRate(inRate, outRate int) error {
	return r.SetRateFrac(inRate, outRate, inRate, outRate)
}

// SetRateFrac sets the ratio num/den and the reported rates
// (speex_resampler_set_rate_frac). The stream position is kept, and the
// filter is rebuilt for the new ratio, keeping the history; this may
// allocate. Unlike C, an ErrOverflow leaves the resampler unchanged. For
// ErrAllocFailed, see ErrAllocFailed.
func (r *Resampler) SetRateFrac(num, den, inRate, outRate int) error {
	if num < 1 || den < 1 || !checkU32(num) || !checkU32(den) || !checkU32(inRate) || !checkU32(outRate) {
		return ErrConfig
	}
	n, d, in, out := uint32(num), uint32(den), uint32(inRate), uint32(outRate)
	// The comparison uses the unreduced ratio, as upstream does.
	if r.inRate == in && r.outRate == out && r.numRate == n && r.denRate == d {
		return nil
	}
	f := gcd(n, d)
	newDen := d / f
	for _, s := range r.sampFracNum {
		if _, ok := multiplyFrac(s, newDen, r.denRate); !ok {
			return ErrOverflow
		}
	}
	if err := r.checkCap(r.quality, n/f, newDen); err != nil {
		return err
	}
	r.setRateFrac(n, d, in, out)
	return r.updateFilter()
}

// Rate returns the rates in Hz last set (speex_resampler_get_rate).
func (r *Resampler) Rate() (inRate, outRate int) { return int(r.inRate), int(r.outRate) }

// Ratio returns the reduced ratio num/den (speex_resampler_get_ratio).
func (r *Resampler) Ratio() (num, den int) { return int(r.numRate), int(r.denRate) }

// SetQuality sets the quality, 0 to 10 (speex_resampler_set_quality). The
// filter is rebuilt, keeping the history; this may allocate.
func (r *Resampler) SetQuality(quality int) error {
	if quality < QualityMin || quality > QualityMax {
		return ErrConfig
	}
	if r.quality == quality {
		return nil
	}
	if err := r.checkCap(quality, r.numRate, r.denRate); err != nil {
		return err
	}
	r.quality = quality
	return r.updateFilter()
}

// Quality returns the quality level (speex_resampler_get_quality).
func (r *Resampler) Quality() int { return r.quality }

// Channels returns the channel count.
func (r *Resampler) Channels() int { return int(r.nbChannels) }

// InputLatency returns the latency in input samples
// (speex_resampler_get_input_latency).
func (r *Resampler) InputLatency() int { return int(r.filtLen / 2) }

// OutputLatency returns the latency in output samples, rounded
// (speex_resampler_get_output_latency).
func (r *Resampler) OutputLatency() int {
	return int(int32(((r.filtLen/2)*r.denRate + (r.numRate >> 1)) / r.numRate))
}

// SkipZeros makes the first InputLatency input samples of each channel
// produce no output, so that output starts aligned with the input
// (speex_resampler_skip_zeros). Call it before the first process call.
func (r *Resampler) SkipZeros() {
	for i := range r.lastSample {
		r.lastSample[i] = int32(r.filtLen / 2)
	}
}

// Reset clears the stream state as speex_resampler_reset_mem does. It keeps
// the rates, ratio and quality. Like upstream, it clears only the first
// Channels()*(filt_len-1) samples of the filter memory, which is the whole
// history of a mono stream but not of the other channels of a multichannel
// stream. Reset does not allocate.
func (r *Resampler) Reset() {
	for i := range r.lastSample {
		r.lastSample[i] = 0
		r.magicSamples[i] = 0
		r.sampFracNum[i] = 0
	}
	clear(r.mem[:int(r.nbChannels)*int(r.filtLen-1)])
}
