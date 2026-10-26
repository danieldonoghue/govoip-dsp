// Package echo is a mono acoustic echo canceller: a port of the MDF
// (multidelay block frequency-domain) adaptive filter of SpeexDSP,
// mdf.c, with one microphone and one loudspeaker.
//
// Parity: bit-exact with SpeexDSP 1.2.1 speex_echo_cancellation,
// speex_echo_capture, speex_echo_playback, speex_echo_state_reset,
// speex_echo_get_residual and the speex_echo_ctl requests (FIXED_POINT)
// for every configuration New accepts, as checked against the reference
// harness at 8, 16, 32 and 48 kHz with 10 and 20 ms frames and 64, 128 and
// 256 ms filters (testdata/speexdsp/echo, docs/acceptance/echo.md).
//
// # Configuration
//
// A Canceller works on frames of Config.FrameSize samples, at
// Config.SampleRate (8000 to 48000 Hz), with an adaptive filter of
// Config.FilterLength samples rounded up to whole frames. 10 or 20 ms
// frames are typical; the frame size must make 2*FrameSize a supported FFT
// size (every even size up to 4096 whose half has no prime factor above
// 17, so 80, 160, 320, 480, 640 and 960 all work). Frame size and filter
// length need a new Canceller; the sampling rate can change on the fly
// with SetSampleRate, as SPEEX_ECHO_SET_SAMPLING_RATE does.
//
// The reference truncates the sampling rate to 16 bits where it derives
// its adaptation constants (DIV32_16 in mdf.c:1233-1236). Rates above 32767
// Hz (44.1 and 48 kHz) therefore give negative constants, and the
// reference adapts differently there; this port does the same.
//
// # Stream alignment
//
// Cancel(mic, far, out) treats far as the loudspeaker signal whose echo is
// in mic. The filter models echo paths whose delay from far to mic lies
// between 0 and about FilterLength() samples:
//
//   - Echo that reaches mic before the far frame that causes it is passed
//     to Cancel (an acausal path) cannot be cancelled.
//   - The part of an echo path longer than FilterLength() is not modelled
//     and stays in the output. This includes a bulk delay larger than the
//     filter: then nothing is cancelled.
//   - A drifting delay or clock is not tracked. The package does no delay
//     estimation; the caller aligns the streams.
//
// The buffered mode follows speex_echo_capture and speex_echo_playback.
// Call Playback with each frame as it is handed to the sound card, and
// Capture with each microphone frame. Capture cancels against the frame
// given to Playback two frames earlier (PLAYBACK_DELAY in mdf.c), so the
// modelled echo delay is from 2 frames to 2 frames plus about
// FilterLength() after the Playback call. In detail:
//
//   - Playback calls before the first Capture are discarded.
//   - Up to three frames are queued; a Playback with three queued frames
//     is discarded.
//   - A Playback into an empty queue is queued twice.
//   - A Capture with no queued frame copies mic to out unprocessed and
//     empties the queue.
//
// The reference prints a warning in each of the last three cases; this
// package does not report them.
//
// # Frames, buffers and aliasing
//
// Every frame method takes exactly FrameSize samples and returns
// ErrBufferSize, doing nothing, for any other length. out must not overlap
// mic or far; overlap is not detected. No method retains a caller slice.
// The output is the same frame as the input: there is no added latency.
//
// # Residual echo
//
// Residual writes the residual-echo power spectrum (FrameSize()+1 values)
// of the most recent Cancel or Capture, the input that
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
// the leak estimate and the older half of the echo estimate that Residual
// reads. So after Reset, Cancel gives the same output as a new Canceller
// only if the filter had not adapted before; Residual reads stale values
// until the next Cancel. The canceller also resets itself, as the
// reference does, when its energy checks fail (for example after an
// arithmetic overflow on full-scale input).
//
// # Concurrency and allocation
//
// A Canceller is not safe for concurrent use. Use one per stream. New
// allocates; no other method allocates. Each frame costs a fixed amount of
// work for the configuration: nine FFTs of 2*FrameSize points (seven with
// a one-frame filter) plus work proportional to 2*FilterLength().
package echo
