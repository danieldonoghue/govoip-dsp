// Derived from SpeexDSP 1.2.1 (https://github.com/xiph/speexdsp, commit
// 1b28a0f61bc31162979e1f26f3981fc3637095c8): libspeexdsp/mdf.c and
// include/speex/speex_echo.h.
//
// Copyright (C) 2003-2008 Jean-Marc Valin (mdf.c)
// Copyright (C) Jean-Marc Valin (speex_echo.h)
//
// Use of this source is governed by the BSD-3-Clause licence in
// LICENSES/SpeexDSP-COPYING. Changes: the speex_echo_* functions and ctl
// requests became a Config, a constructor and methods returning errors.

package echo

import (
	"errors"
	"fmt"

	"github.com/danieldonoghue/govoip-dsp/internal/fft"
)

// Limits checked by New and SetSampleRate.
const (
	// MaxFrameSize is the largest frame size: internal/fft supports
	// transforms of at most 4096 samples, and the window is two frames.
	MaxFrameSize = fft.MaxSize / 2
	// MaxFilterLength bounds the filter length in samples, and so the
	// memory a Canceller uses (about 14 bytes per sample of filter).
	MaxFilterLength = 64 * 1024
	// MinSampleRate and MaxSampleRate bound the sampling rate in Hz.
	MinSampleRate = 8000
	MaxSampleRate = 48000
)

// maxBlocks keeps the block count M within spx_word16_t, which mdf.c
// divides by (mdf.c:482, mdf.c:709).
const maxBlocks = 32767

// Sentinel errors. Methods called per frame return only these.
var (
	ErrConfig     = errors.New("echo: invalid configuration")
	ErrBufferSize = errors.New("echo: buffer length does not match the frame size")
)

// Config configures a Canceller. Every field must be set.
type Config struct {
	// FrameSize is the number of samples per frame, 1 to MaxFrameSize,
	// such that internal/fft supports 2*FrameSize (for example 80, 160,
	// 320 or 480: 10 ms at 8, 16, 32 or 48 kHz).
	FrameSize int
	// FilterLength is the echo tail the filter models, in samples, 1 to
	// MaxFilterLength. It is rounded up to a whole number of frames.
	FilterLength int
	// SampleRate is the sampling rate in Hz, MinSampleRate to
	// MaxSampleRate. It sets the adaptation constants and the DC notch.
	// Only 8000 and 16000 are supported: higher rates are bit-exact with
	// the reference but cancel poorly (see the package doc).
	SampleRate int
}

// Canceller is a mono acoustic echo canceller: speex_echo_state_init with
// one microphone and one loudspeaker. See the package doc.
type Canceller struct {
	st *state
}

// New is speex_echo_state_init(FrameSize, FilterLength) followed by
// SPEEX_ECHO_SET_SAMPLING_RATE. It returns an error wrapping ErrConfig for
// an invalid configuration. It accepts rates above 16 kHz, where the
// reference truncates the rate to 16 bits and overflows its energy sums;
// resample to 8 or 16 kHz instead (see the package doc).
func New(cfg Config) (*Canceller, error) {
	if cfg.FrameSize < 1 || cfg.FrameSize > MaxFrameSize || !fft.Supported(2*cfg.FrameSize) {
		return nil, fmt.Errorf("%w: frame size %d (want 1 to %d, with 2*frame size supported by the FFT)",
			ErrConfig, cfg.FrameSize, MaxFrameSize)
	}
	if cfg.FilterLength < 1 || cfg.FilterLength > MaxFilterLength {
		return nil, fmt.Errorf("%w: filter length %d (want 1 to %d samples)", ErrConfig, cfg.FilterLength, MaxFilterLength)
	}
	if m := (cfg.FilterLength + cfg.FrameSize - 1) / cfg.FrameSize; m > maxBlocks {
		return nil, fmt.Errorf("%w: filter length %d is %d frames (want at most %d)", ErrConfig, cfg.FilterLength, m, maxBlocks)
	}
	if !validRate(cfg.SampleRate) {
		return nil, fmt.Errorf("%w: sample rate %d (want %d to %d Hz)", ErrConfig, cfg.SampleRate, MinSampleRate, MaxSampleRate)
	}
	st, err := newState(cfg.FrameSize, cfg.FilterLength, cfg.SampleRate)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConfig, err)
	}
	return &Canceller{st: st}, nil
}

func validRate(rate int) bool { return rate >= MinSampleRate && rate <= MaxSampleRate }

// FrameSize returns the frame size in samples (SPEEX_ECHO_GET_FRAME_SIZE).
func (c *Canceller) FrameSize() int { return c.st.frameSize }

// SampleRate returns the sampling rate (SPEEX_ECHO_GET_SAMPLING_RATE).
func (c *Canceller) SampleRate() int { return c.st.samplingRate }

// FilterLength returns the modelled echo tail in samples: the configured
// length rounded up to whole frames (SPEEX_ECHO_GET_IMPULSE_RESPONSE_SIZE).
func (c *Canceller) FilterLength() int { return c.st.m * c.st.frameSize }

// SetSampleRate is SPEEX_ECHO_SET_SAMPLING_RATE. As in the reference it
// may be called at any time, keeps the filter state, and is kept by Reset.
// It returns ErrConfig, changing nothing, for a rate outside the limits.
func (c *Canceller) SetSampleRate(rate int) error {
	if !validRate(rate) {
		return ErrConfig
	}
	c.st.setRate(rate)
	return nil
}

// Cancel is speex_echo_cancellation: it removes the echo of far from mic
// and writes the result to out. mic is the microphone frame and far the
// loudspeaker frame that the echo in mic comes from; see the package doc
// for alignment. Each slice must hold exactly FrameSize samples, and out
// must not overlap mic. Otherwise it returns ErrBufferSize and does
// nothing.
func (c *Canceller) Cancel(mic, far, out []int16) error {
	fs := c.st.frameSize
	if len(mic) != fs || len(far) != fs || len(out) != fs {
		return ErrBufferSize
	}
	c.st.cancel(mic, far, out)
	return nil
}

// Capture is speex_echo_capture: Cancel with the far end taken from the
// frames given to Playback, through a queue (see the package doc). With no
// buffered playback frame it copies mic to out. Slice rules are those of
// Cancel.
func (c *Canceller) Capture(mic, out []int16) error {
	fs := c.st.frameSize
	if len(mic) != fs || len(out) != fs {
		return ErrBufferSize
	}
	c.st.capture(mic, out)
	return nil
}

// Playback is speex_echo_playback: it queues a loudspeaker frame for a
// later Capture. It returns ErrBufferSize, doing nothing, unless far holds
// exactly FrameSize samples. far is copied, not retained.
func (c *Canceller) Playback(far []int16) error {
	if len(far) != c.st.frameSize {
		return ErrBufferSize
	}
	c.st.playback(far)
	return nil
}

// Reset is speex_echo_state_reset. The package doc lists the state the
// reference keeps.
func (c *Canceller) Reset() { c.st.reset() }

// ResidualLen returns FrameSize()+1, the number of values Residual writes.
func (c *Canceller) ResidualLen() int { return c.st.frameSize + 1 }

// Residual writes the residual-echo power spectrum of the most recent
// Cancel or Capture call into dst[:ResidualLen()], exactly as
// speex_echo_get_residual computes it. It returns ErrBufferSize if
// len(dst) < ResidualLen(). It does not change any later output of c.
func (c *Canceller) Residual(dst []int32) error {
	if len(dst) < c.st.frameSize+1 {
		return ErrBufferSize
	}
	c.st.residual(dst)
	return nil
}

// ImpulseResponseLen returns FilterLength(), the number of values
// ImpulseResponse writes.
func (c *Canceller) ImpulseResponseLen() int { return c.st.m * c.st.frameSize }

// ImpulseResponse writes the current filter's impulse response into
// dst[:ImpulseResponseLen()], as SPEEX_ECHO_GET_IMPULSE_RESPONSE does. It
// returns ErrBufferSize if dst is shorter. It does not change any later
// output of c.
func (c *Canceller) ImpulseResponse(dst []int32) error {
	if len(dst) < c.st.m*c.st.frameSize {
		return ErrBufferSize
	}
	c.st.impulseResponse(dst)
	return nil
}
