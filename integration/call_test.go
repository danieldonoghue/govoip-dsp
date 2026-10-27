package integration

import (
	"errors"
	"fmt"

	"github.com/danieldonoghue/govoip-dsp/agc"
	"github.com/danieldonoghue/govoip-dsp/buffer"
	"github.com/danieldonoghue/govoip-dsp/echo"
	"github.com/danieldonoghue/govoip-dsp/preprocess"
	"github.com/danieldonoghue/govoip-dsp/resample"
)

// This file is the recipe of docs/usage.md; TestUsageCodeIsTested checks
// that every Go block there appears here or in usage_test.go verbatim.

// Sentinel errors that the buffer sizes of newCall rule out.
var (
	errShortRead = errors.New("call: resampler left input unread")
	errQueueFull = errors.New("call: resampled queue full")
)

// call is the send path of one call: every processor and buffer it needs,
// made once, used from one goroutine.
type call struct {
	netRate, rate   int // network and processing rates, Hz
	netFrame, frame int // samples per frame at each rate

	ec  *echo.Canceller
	pre *preprocess.Preprocessor
	gc  *agc.AGC

	// Set only when the network rate is not 8 or 16 kHz.
	micRS, farRS   *resample.Resampler
	micQ, farQ     *buffer.Ring
	rsOut          []int16
	micBuf, farBuf []int16

	clean    []int16 // echo output, then preprocess output, in place
	residual []int32
}

// newCall makes the processors of one call whose network audio runs at
// netRate Hz in frames of frameMs milliseconds.
func newCall(netRate, frameMs int) (*call, error) {
	// Process at the network rate when the echo canceller supports it, so
	// nothing is resampled; otherwise resample to 16 kHz.
	rate := netRate
	if rate != 8000 && rate != 16000 {
		rate = 16000 // the echo canceller supports 8 and 16 kHz only
	}
	// A frame is the same duration at both rates, so one network frame
	// becomes one processing frame (20 ms: 160 samples at 8 kHz).
	c := &call{
		netRate: netRate, rate: rate,
		netFrame: netRate * frameMs / 1000, frame: rate * frameMs / 1000,
	}
	// The preprocessor's FFT needs a supported frame size; check it before
	// building anything, so a bad frame duration fails here, not mid-call.
	if !preprocess.SupportedFrameSize(c.frame) {
		return nil, fmt.Errorf("call: no preprocessor for %d-sample frames", c.frame)
	}

	var err error
	c.ec, err = echo.New(echo.Config{
		FrameSize:    c.frame,
		FilterLength: rate / 5, // models echo delays up to 200 ms
		SampleRate:   rate,
	})
	if err != nil {
		return nil, err
	}
	c.pre, err = preprocess.New(preprocess.Config{SampleRate: rate, FrameSize: c.frame})
	if err != nil {
		return nil, err
	}
	// The VAD decision is what tells the AGC which frames are speech.
	c.pre.SetVAD(true) // denoise is on by default
	c.gc, err = agc.New(agc.DefaultConfig(rate))
	if err != nil {
		return nil, err
	}
	// Scratch buffers, made once so the per-frame path never allocates; the
	// residual spectrum has one value more than the frame (ResidualLen).
	c.clean = make([]int16, c.frame)
	c.residual = make([]int32, c.ec.ResidualLen())

	// Resampling only where the network rate is not 8 or 16 kHz: one
	// resampler per stream, because each keeps its own filter history.
	if rate != netRate {
		rs := resample.Config{Channels: 1, InRate: netRate, OutRate: rate, Quality: resample.QualityVoIP}
		if c.micRS, err = resample.New(rs); err != nil {
			return nil, err
		}
		if c.farRS, err = resample.New(rs); err != nil {
			return nil, err
		}
		// OutLen gives the output space one call needs to consume a whole
		// network frame, so no input is ever left behind.
		c.rsOut = make([]int16, c.micRS.OutLen(c.netFrame))
		// Less than one frame waits between calls, plus one call's output.
		q := buffer.Config{Capacity: c.frame + len(c.rsOut), Overflow: buffer.Reject}
		if c.micQ, err = buffer.New(q); err != nil {
			return nil, err
		}
		if c.farQ, err = buffer.New(q); err != nil {
			return nil, err
		}
		c.micBuf = make([]int16, c.frame)
		c.farBuf = make([]int16, c.frame)
	}
	return c, nil
}

// outLen is the length of the out slice that process needs.
func (c *call) outLen() int { return 2 * c.frame }

// process cleans one network frame of mic against the far frame sent with
// it, and returns the samples written to out: whole processing frames.
func (c *call) process(mic, far, out []int16) (int, error) {
	// Without resampling a network frame is a processing frame.
	if c.micRS == nil {
		return c.frame, c.step(mic, far, out[:c.frame])
	}
	// Resample both streams the same way, so that they stay paired sample
	// for sample, and queue the results until a whole frame is there.
	if err := c.resampleInto(c.micRS, c.micQ, mic); err != nil {
		return 0, err
	}
	if err := c.resampleInto(c.farRS, c.farQ, far); err != nil {
		return 0, err
	}
	// The echo canceller and the preprocessor take whole frames only. Both
	// queues hold the same number of samples, so checking one is enough.
	n := 0
	for c.micQ.Len() >= c.frame && n+c.frame <= len(out) {
		c.micQ.Read(c.micBuf)
		c.farQ.Read(c.farBuf)
		if err := c.step(c.micBuf, c.farBuf, out[n:n+c.frame]); err != nil {
			return n, err
		}
		n += c.frame
	}
	return n, nil
}

func (c *call) resampleInto(r *resample.Resampler, q *buffer.Ring, x []int16) error {
	// A resampler may leave input unread when its output is full; with
	// rsOut sized by OutLen that cannot happen, so it is reported as an error.
	read, written, err := r.Process(x, c.rsOut)
	if err != nil {
		return err
	}
	if read != len(x) {
		return errShortRead
	}
	// The queue is sized so that it always has room; a short write would
	// drop samples and unpair the two streams, so it is reported too.
	if q.Write(c.rsOut[:written]) != written {
		return errQueueFull
	}
	return nil
}

// step runs one processing frame through the send path.
func (c *call) step(mic, far, out []int16) error {
	// Remove the echo of far, the frame sent at the same time, from mic.
	if err := c.ec.Cancel(mic, far, c.clean); err != nil {
		return err
	}
	// The residual is the echo that Cancel could not remove from this very
	// frame, so it must be read after Cancel and before the next one.
	if err := c.ec.Residual(c.residual); err != nil {
		return err
	}
	// The preprocessor suppresses noise and that residual echo in place,
	// and decides whether the frame is speech.
	speech, err := c.pre.RunEcho(c.clean, c.residual)
	if err != nil {
		return err
	}
	// The AGC raises the gain only on speech frames, so the VAD decision
	// keeps it from turning up pauses.
	return c.gc.Process(c.clean, out, speech)
}
