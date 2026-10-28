// Package preprocess ports the SpeexDSP 1.2.1 preprocessor (preprocess.c and
// filterbank.c, FIXED_POINT build): noise suppression, voice activity
// detection (VAD) and residual-echo suppression on mono int16 frames.
//
// Parity: bit-exact with SpeexDSP 1.2.1 speex_preprocess_run,
// speex_preprocess_estimate_update and the ported speex_preprocess_ctl
// requests (FIXED_POINT) for the configurations of
// docs/acceptance/preprocess.md, as checked against the reference harness
// (the repository's testdata/speexdsp/preprocess).
//
// Terms such as frame, frame size, VAD, speech probability, power spectrum
// and residual echo are defined in the [glossary]; a frame here is a
// processing frame of FrameSize samples. The developer guide
// shows the [per-frame call order] of the send path. The SpeexDSP
// documentation of the same functions is the Speex manual's
// [preprocessor section] and [preprocessor options], and the header
// [speex_preprocess.h].
//
// # Use
//
// Make one Preprocessor per stream with New, choose the settings, then
// pass each frame to Run, or to RunEcho after an echo canceller:
//
//	p, err := preprocess.New(preprocess.Config{SampleRate: 8000, FrameSize: 160})
//	if err != nil {
//		return err // wraps ErrConfig: unsupported rate or frame size
//	}
//	p.SetVAD(true) // denoise is on by default
//	for _, frame := range frames { // each exactly 160 samples
//		speech, err := p.Run(frame) // frame is rewritten in place
//		if err != nil {
//			return err // ErrBufferSize: the frame is not 160 samples
//		}
//		if err := a.Process(frame, frame, speech); err != nil { // a: an agc.AGC
//			return err
//		}
//	}
//
// Reset starts a new stream with the same settings. A different rate or
// frame size needs a new Preprocessor.
//
// # Rates and frame sizes
//
// Config.SampleRate is MinSampleRate (4) to MaxSampleRate (65535) Hz: the
// range in which the reference's filterbank init is defined (below 4 Hz it
// divides by zero, above 65535 Hz it overflows). Config.FrameSize is 1 to
// MaxFrameSize (2048) samples with no prime factor above 17, as the FFT of
// 2*FrameSize requires; SupportedFrameSize checks a size before New. Typical
// use is 10 ms or 20 ms frames at 8, 16, 32 or 48 kHz. The upstream header
// recommends frames of 10 to 20 ms, and requires the same frame size as
// the echo canceller for residual-echo suppression. Each frame is analysed in a
// window of 2*FrameSize samples, the frame and the one before it, so the
// output is delayed by FrameSize samples (one frame).
//
// # Settings
//
// Settings are methods and may change between frames, as the reference
// ctl requests may. Each setter has a getter of the same name without
// "Set". After New:
//
//   - SetDenoise: noise suppression, on. With it off, frames still pass
//     through the analysis and synthesis, so the VAD works alone.
//   - SetVAD: the VAD decision, off. While it is off, Run and RunEcho
//     always report speech.
//   - SetProbStart: 35 percent. A frame starts speech when its speech
//     probability is above it.
//   - SetProbContinue: 20 percent. After a speech frame, speech goes on
//     while the probability is above it.
//   - SetNoiseSuppress: -15 dB, the most noise attenuation. Stored as
//     -|dB|; more attenuation removes more noise and risks more speech.
//   - SetEchoSuppress: -40 dB, the most residual-echo attenuation without
//     near-end speech. Stored as -|dB|. It acts on the residual echo that
//     RunEcho passes in; Run has none.
//   - SetEchoSuppressActive: -15 dB, the same during near-end speech.
//     Stored as -|dB|.
//
// The rate and frame size need a new instance. The fixed-point reference
// has no AGC (use package agc) and no working dereverb, so neither is
// offered. SpeechProb is the heuristic score behind the VAD, not a
// calibrated probability; PSD and NoisePSD report the last frame's power
// spectrum and the noise estimate.
//
// # Residual echo
//
// RunEcho takes the residual-echo power spectrum that echo.Canceller.Residual
// writes for the same frame (docs/contracts.md section 12); it replaces the
// reference's SET_ECHO_STATE pointer. Run is the reference without an echo
// state. Switching between them from frame to frame matches setting and
// clearing the echo state in C. Both instances must use the same frame
// size and rate; only the length of the residual is checked.
//
// # Buffers, aliasing and state
//
// Run and RunEcho process exactly FrameSize samples in place and return
// ErrBufferSize, changing nothing, for any other length or a residual
// shorter than FrameSize; RunEcho ignores residual values past FrameSize.
// EstimateUpdate only reads its frame, with the same length rule. PSD and
// NoisePSD write PSDLen() values and return ErrBufferSize, writing nothing,
// for a shorter slice. No method retains a caller slice. Reset clears the
// adaptive state and keeps the settings; the reference has no reset, and
// Reset equals a new instance with the same settings.
//
// # Errors
//
// New returns an error wrapping ErrConfig for an unsupported rate or frame
// size; test it with errors.Is. Fix the Config, or check the frame size
// with SupportedFrameSize first. The frame methods return ErrBufferSize,
// unwrapped, for a wrong length; with buffers sized from FrameSize it
// cannot occur, so treat it as a programming error. On a zero Preprocessor
// they return ErrConfig.
//
// # Zero value
//
// A zero Preprocessor (not made by New) is not usable. Its frame methods,
// PSD and NoisePSD return ErrConfig. The setters store their setting and
// the getters of those settings return what was stored (so Denoise is
// false, not the default true, until SetDenoise). FrameSize, SampleRate,
// PSDLen and SpeechProb return 0, and Reset does nothing. No method
// panics.
//
// # Arithmetic
//
// Arithmetic wraps on overflow, as the reference does when built with
// -fwrapv, and loud inputs do overflow inside the reference. Where a
// divisor can reach zero (docs/algorithms.md lists the sites), the quotient
// is 0, which is what the canonical arm64 reference computes; C itself is
// undefined there. No method panics.
//
// # Limitations
//
// The VAD of the reference reports speech on stationary noise and on
// residual echo, so it cannot keep the AGC from raising noise; a gate above
// the noise does (package agc). How much noise suppression removes
// depends on the noise: measured at the default -15 dB, white noise at
// -35 dBFS fell by 13 to 14 dB and pink noise at -37 dBFS by only 4 to
// 6 dB. Mono only.
//
// # Concurrency and allocation
//
// A Preprocessor is not safe for concurrent use. Use one per stream. New
// allocates; no other method allocates. Each frame costs two FFTs of
// 2*FrameSize samples (one forward, one inverse) plus work proportional
// to FrameSize.
//
// [glossary]: https://github.com/danieldonoghue/govoip-dsp/blob/master/docs/glossary.md
// [per-frame call order]: https://github.com/danieldonoghue/govoip-dsp/blob/master/docs/guide.md#the-send-path-frame-by-frame
// [preprocessor section]: https://www.speex.org/docs/manual/speex-manual/node7.html#SECTION00730000000000000000
// [preprocessor options]: https://www.speex.org/docs/manual/speex-manual/node7.html#SECTION00790000000000000000
// [speex_preprocess.h]: https://gitlab.xiph.org/xiph/speexdsp/-/blob/1b28a0f61bc31162979e1f26f3981fc3637095c8/include/speex/speex_preprocess.h
package preprocess
