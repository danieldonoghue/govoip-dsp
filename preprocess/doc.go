// Package preprocess ports the SpeexDSP 1.2.1 preprocessor (preprocess.c and
// filterbank.c, FIXED_POINT build): noise suppression, voice activity
// detection (VAD) and residual-echo suppression on mono int16 frames.
//
// Parity: bit-exact with SpeexDSP 1.2.1 speex_preprocess_run,
// speex_preprocess_estimate_update and the ported speex_preprocess_ctl
// requests (FIXED_POINT) for every supported configuration, as checked
// against the reference harness (testdata/speexdsp/preprocess).
//
// # Rates and frame sizes
//
// Config.SampleRate is MinSampleRate (4) to MaxSampleRate (65535) Hz: the
// range in which the reference's filterbank init is defined (below 4 Hz it
// divides by zero, above 65535 Hz it overflows). Config.FrameSize is 1 to
// MaxFrameSize (2048) samples with no prime factor above 17, as the FFT of
// 2*FrameSize requires; SupportedFrameSize checks a size before New. Typical
// use is 10 ms or 20 ms frames at 8, 16, 32 or 48 kHz. Processing uses a
// 2*FrameSize window with 50% overlap, so the output is delayed by FrameSize
// samples.
//
// # Settings
//
// Settings are methods and may change between frames, as the reference
// ctl requests may: SetDenoise (on by default), SetVAD (off by default),
// SetProbStart and SetProbContinue (35% and 20%), SetNoiseSuppress (-15
// dB), SetEchoSuppress (-40 dB) and SetEchoSuppressActive (-15 dB). The rate
// and frame size need a new instance. The fixed-point reference has no AGC
// (use package agc) and no working dereverb, so neither is offered.
// SpeechProb is the heuristic score behind the VAD, not a calibrated
// probability.
//
// # Residual echo
//
// RunEcho takes the residual-echo power spectrum that echo.Canceller.Residual
// writes for the same frame (docs/contracts.md section 12); it replaces the
// reference's SET_ECHO_STATE pointer. Run is the reference without an echo
// state. Switching between them from frame to frame matches setting and
// clearing the echo state in C.
//
// # Buffers, aliasing and state
//
// Run and RunEcho process exactly FrameSize samples in place and return
// ErrBufferSize, changing nothing, for any other length or a residual
// shorter than FrameSize; RunEcho ignores residual values past FrameSize.
// EstimateUpdate only reads its frame, with the same length rule. No method
// retains a caller slice. Reset clears the adaptive state and keeps the
// settings; the reference has no reset, and Reset equals a new instance with
// the same settings. On a zero Preprocessor (not made by New) the frame
// methods, PSD and NoisePSD return ErrConfig, the setters store their
// setting, the other methods return zero values, and Reset does nothing.
//
// # Arithmetic
//
// Arithmetic wraps on overflow, as the reference does when built with
// -fwrapv, and loud inputs do overflow inside the reference. Where a
// divisor can reach zero (docs/algorithms.md lists the sites), the quotient
// is 0, which is what the canonical arm64 reference computes; C itself is
// undefined there. No method panics.
//
// # Concurrency and allocation
//
// A Preprocessor is not safe for concurrent use. Use one per stream. New
// allocates; no other method allocates. Work per frame is proportional to
// FrameSize times the FFT cost of 2*FrameSize samples.
package preprocess
