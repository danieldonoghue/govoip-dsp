// Package echo is a mono acoustic echo canceller: a port of the MDF
// (multidelay block frequency-domain) adaptive filter of SpeexDSP,
// mdf.c, with one microphone and one loudspeaker.
//
// Parity: bit-exact with SpeexDSP 1.2.1 speex_echo_cancellation,
// speex_echo_capture, speex_echo_playback, speex_echo_state_reset,
// speex_echo_get_residual and the speex_echo_ctl requests (FIXED_POINT),
// as checked against the reference harness at 8, 16, 32 and 48 kHz with 10
// and 20 ms frames and 64, 128 and 256 ms filters (the repository's
// testdata/speexdsp/echo, docs/acceptance/echo.md). One deviation, which
// no fixture reaches: where a FLOAT_MUL32 shift count leaves [-31, 31],
// the two reference compilers disagree, and the port saturates
// (docs/algorithms.md).
//
// Terms such as near end, far end, echo delay, echo tail, filter length,
// residual echo and ERLE are defined in the [glossary]. A frame here is a
// processing frame: FrameSize samples, passed whole to every frame
// method. The developer guide shows the [per-frame call order] of the
// send path, and docs/usage.md has two tested wiring recipes for a call.
// The SpeexDSP
// documentation of the same functions is the Speex manual's
// [echo cancellation section] and its [troubleshooting] advice, and the
// header [speex_echo.h].
//
// # Use
//
// Make one Canceller per call with New. For each frame, pass the
// microphone frame and the loudspeaker frame its echo comes from to
// Cancel, then, before the next frame, read Residual for the
// preprocessor:
//
//	c, err := echo.New(echo.Config{FrameSize: 160, FilterLength: 1600, SampleRate: 8000})
//	if err != nil {
//		return err // wraps ErrConfig: invalid frame size, filter or rate
//	}
//	out := make([]int16, c.FrameSize())        // made once
//	residual := make([]int32, c.ResidualLen()) // made once
//	for i := range mics { // mics[i] and fars[i]: 160 samples each
//		if err := c.Cancel(mics[i], fars[i], out); err != nil {
//			return err // ErrBufferSize: a slice is not 160 samples
//		}
//		if err := c.Residual(residual); err != nil {
//			return err
//		}
//		// then preprocess.Preprocessor.RunEcho(out, residual)
//	}
//
// When playback and capture arrive on separate callbacks, use Playback
// and Capture instead of Cancel ("Stream alignment" below). Call Reset to
// start a new call with the same configuration.
//
// Choose the filter length to cover the largest echo delay expected plus
// the echo tail, and no more. The upstream header suggests 100 to 500 ms;
// the Speex manual suggests about a third of the room's reverberation
// time, for example 100 ms in a small room, and warns that a filter that
// is too long is a common reason no echo is cancelled, because it adapts
// more slowly. The filter needs time to converge. In the synthetic
// scenarios of docs/algorithms.md ("Measured behaviour") the echo stage
// reached 10 dB of ERLE within 0.5 s of New at 8 and 16 kHz, and within
// 1 s of a sudden path change; in the call recipes of docs/usage.md a
// step of the echo delay from 100 to 140 ms took 2.5 to 3.5 s.
//
// # Configuration
//
// A Canceller works on frames of Config.FrameSize samples, at
// Config.SampleRate, with an adaptive filter of
// Config.FilterLength samples rounded up to whole frames. 10 or 20 ms
// frames are typical; the frame size must make 2*FrameSize a supported FFT
// size (every even size up to 4096 whose half has no prime factor above
// 17, so 80, 160, 320, 480, 640 and 960 all work). Frame size and filter
// length need a new Canceller; the sampling rate can change on the fly
// with SetSampleRate, as SPEEX_ECHO_SET_SAMPLING_RATE does.
//
// Supported rates: 8 and 16 kHz only. Resample higher rates down first.
// New accepts rates up to 48 kHz and stays bit-exact there, but the
// reference cancels poorly above 16 kHz. Two reference defects contribute:
//
//   - DIV32_16 truncates the rate to 16 bits where the adaptation
//     constants are derived (mdf.c:1233-1236), so above 32767 Hz (44.1
//     and 48 kHz) spec_average, beta0 and beta_max are negative.
//   - The 32-bit energy sums of mdf_inner_prod overflow on ordinary speech
//     with frames of 480 samples or more (10 ms at 48 kHz, 20 ms at 32 and
//     48 kHz), so the reference resets itself every few seconds.
//
// At 32 kHz with 10 ms frames neither applies and the measured cancellation
// is still weak; that cause has not been investigated. docs/algorithms.md
// gives the measured effect.
//
// # Stream alignment
//
// Cancel(mic, far, out) treats far as the loudspeaker signal whose echo is
// in mic. The filter models echo paths whose delay from far to mic lies
// between 0 and about FilterLength() samples:
//
//   - If the echo of a far frame is already in mic before that far frame
//     is passed to Cancel (an acausal path), it cannot be cancelled.
//   - The part of an echo path longer than FilterLength() is not modelled
//     and stays in the output. This includes a bulk delay larger than the
//     filter: then nothing is cancelled.
//   - A drifting delay or clock is not tracked. The package does no delay
//     estimation; the caller aligns the streams.
//
// The buffered mode follows speex_echo_capture and speex_echo_playback.
// Call Playback with each frame as it is handed to the sound card, and
// Capture with each microphone frame. The queue starts with two frames of
// silence (PLAYBACK_DELAY in mdf.c) and each Capture cancels against the
// oldest queued frame. With the calls alternating, that is the frame of the
// second-most-recent Playback call: about one frame period before the
// Capture if Playback comes first in each period, about two if it comes
// after. The filter models echo delays from there to about FilterLength()
// later; echo that arrives sooner is not cancelled. In detail:
//
//   - Playback calls before the first Capture after New or Reset (including
//     the canceller's own internal reset) are discarded.
//   - Up to three frames are queued; a Playback with three queued frames
//     is discarded.
//   - A Playback into an empty queue is queued twice.
//   - A Capture with no queued frame copies mic to out unprocessed.
//
// The reference prints a warning in each of these cases; this package does
// not report them.
//
// # Frames, buffers and aliasing
//
// Every frame method takes exactly FrameSize samples and returns
// ErrBufferSize, doing nothing, for any other length. out must not overlap
// mic or far; overlap is not detected. No method retains a caller slice.
// The output is the same frame as the input: there is no added latency. On
// a zero Canceller (not made by New) the methods with an error result
// return ErrConfig, the others return 0, and Reset does nothing.
//
// # Residual echo
//
// Residual writes the residual-echo power spectrum (FrameSize()+1 values)
// of the most recent frame that Cancel or Capture cancelled (a Capture
// with nothing queued cancels nothing), the input that
// preprocess.Preprocessor.RunEcho takes (docs/contracts.md section 12).
// Calling it does not change any later output, and two calls in a row give
// the same values.
//
// # Reset
//
// Reset is speex_echo_state_reset: it clears the filters, the far-end
// history, the power estimates, the adaptation state and the playback
// queue. Like the reference it keeps the sampling rate, the per-block
// adaptation weights learned once the filter has adapted (prop in mdf.c),
// the leak estimate and the newer half of the echo estimate that Residual
// reads. So after Reset, Cancel gives the same output as a new Canceller
// only if the filter had not adapted before, and Residual keeps reporting
// the echo from before the Reset until the filter adapts again. The
// canceller also resets itself, as the reference does, when its energy
// checks fail (for example after an arithmetic overflow on full-scale
// input).
//
// # Errors
//
// New returns an error wrapping ErrConfig for an invalid Config, naming
// the field; test it with errors.Is and fix the Config. SetSampleRate
// returns ErrConfig, unwrapped, for a rate out of range, and changes
// nothing. The frame methods return ErrBufferSize, unwrapped, for a slice
// of the wrong length and do nothing then; with slices sized from
// FrameSize, ResidualLen and ImpulseResponseLen it cannot occur, so treat
// it as a programming error. On a zero Canceller the methods with an error
// result return ErrConfig.
//
// # Limitations
//
//   - Mono: one microphone and one loudspeaker. The reference's
//     multichannel speex_echo_state_init_mc is not ported.
//   - 8 and 16 kHz only in practice (see "Configuration").
//   - No delay estimation and no drift correction: the caller aligns the
//     streams (see "Stream alignment").
//   - Non-linear distortion, such as a clipping loudspeaker or microphone,
//     cannot be modelled by the linear filter and is not cancelled.
//
// # Concurrency and allocation
//
// A Canceller is not safe for concurrent use. Use one per stream. New
// allocates; no other method allocates. Each frame costs a fixed amount of
// work for the configuration: nine FFTs of 2*FrameSize points (seven with
// a one-frame filter) plus work proportional to 2*FilterLength().
//
// [glossary]: https://github.com/danieldonoghue/govoip-dsp/blob/master/docs/glossary.md
// [per-frame call order]: https://github.com/danieldonoghue/govoip-dsp/blob/master/docs/guide.md#the-send-path-frame-by-frame
// [echo cancellation section]: https://www.speex.org/docs/manual/speex-manual/node7.html#SECTION00740000000000000000
// [troubleshooting]: https://www.speex.org/docs/manual/speex-manual/node7.html#SECTION00741000000000000000
// [speex_echo.h]: https://gitlab.xiph.org/xiph/speexdsp/-/blob/1b28a0f61bc31162979e1f26f3981fc3637095c8/include/speex/speex_echo.h
package echo
