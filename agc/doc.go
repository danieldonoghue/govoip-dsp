// Package agc is an automatic gain control (AGC) for 16-bit-scale int16
// speech, in integer fixed-point arithmetic. It raises quiet talkers and
// lowers loud ones towards one output level, and a limiter keeps every
// output sample under a ceiling.
//
// Parity: own design; contains no SpeexDSP code. SpeexDSP 1.2.1 has an AGC
// only in its floating-point build (speex_compute_agc in [preprocess.c]);
// no code is taken from it. Acceptance is behavioural, against
// docs/acceptance/agc.md.
//
// Terms such as dBFS, RMS, gate level and VAD are defined in the
// [glossary]. The [developer guide] shows where the AGC sits in a call.
//
// # Use
//
// Make one AGC per stream with New, pass every chunk of the stream to
// Process with the VAD decision for it, and call Reset to start a new
// stream:
//
//	a, err := agc.New(agc.DefaultConfig(8000))
//	if err != nil {
//		return err // wraps ErrConfig: the Config is invalid
//	}
//	out := make([]int16, 160) // made once, the length of one chunk
//	for _, chunk := range chunks { // each 160 samples, 20 ms at 8 kHz
//		speech := true // the VAD decision for chunk, if there is a VAD
//		if err := a.Process(chunk, out, speech); err != nil {
//			return err // ErrBufferSize: out shorter than chunk
//		}
//	}
//
// In a send chain the AGC runs last, after package preprocess, and speech
// is the VAD decision that preprocess.Preprocessor.Run or RunEcho returned
// for the same frame (with preprocess.Preprocessor.SetVAD(true)). Without
// a VAD, pass true: every frame above GateLevel then counts as speech.
//
// # Configuration
//
// Every Config field must be set; DefaultConfig gives the recommended
// values, and the zero Config is invalid. New checks each field and
// returns an error wrapping ErrConfig that names the first field out of
// range. Levels are whole dBFS, where 0 dBFS is amplitude 32768; gains are
// whole dB; rates are whole dB/s. The defaults are those of DefaultConfig.
//
//   - SampleRate: 8000 to 48000 Hz, no default. Sets the block length,
//     SampleRate/1000 samples, and so every time constant.
//   - TargetLevel: -40 to -3 dBFS, default -24. The output RMS a steady
//     signal above the gate converges to.
//   - LimitLevel: -20 to 0 dBFS and above TargetLevel, default -1. Every
//     output sample's magnitude is at most
//     min(32767, round(32768·10^(LimitLevel/20))).
//   - GateLevel: -80 dBFS up to but not including TargetLevel, default
//     -50. A frame whose RMS is below it never raises the gain or changes
//     the level estimate. It is the only protection against raising noise
//     (see "Noise protection").
//   - MaxGain: 0 to 40 dB, default 24. The highest AGC gain.
//   - MinGain: -40 to 0 dB, default -12. The lowest AGC gain; the limiter
//     may still reduce the applied gain below it.
//   - RiseRate: 1 to 100 dB/s, default 12. How fast the gain may rise; it
//     rises on active frames only.
//   - FallRate: 1 to 400 dB/s, default 40. How fast the gain may fall, on
//     any frame.
//   - LevelTime: 100 ms to 10 s, default 3 s. The averaging time of the
//     level estimate; a longer time follows level changes more slowly.
//
// Every setting is fixed at New. To change one, make a new AGC.
//
// # Algorithm
//
// The input is split into blocks of SampleRate/1000 samples (1 ms at rates
// that are multiples of 1000) and AGC frames of ten blocks, counted from
// the start of the stream.
//
//  1. For each frame, the RMS r is measured. A frame is active when its RMS
//     is at or above GateLevel and every one of its samples was passed with
//     speech true.
//  2. The level estimate L is a power mean of order 4 of active-frame RMS:
//     the fourth root of an exponential mean of min(r, 2L)^4, with time
//     constant LevelTime, or the plain mean until LevelTime of speech has
//     been seen. Loud frames dominate it, so it follows the peaks of speech
//     rather than the dips between syllables. Inactive frames leave it
//     unchanged.
//  3. A run of consecutive active frames more than 13 dB above L resets L
//     to their level and cuts the gain at once. The run must be one frame
//     longer than a 10 ms click can touch, so 3 frames at rates that are
//     multiples of 1000; an inactive frame ends it. Active frames more than
//     6 dB below L for LevelTime/3 in a row reset L to their level.
//  4. The target gain is TargetLevel/L, clamped to [MinGain, MaxGain]. The
//     AGC gain starts at 0 dB and moves towards it by at most RiseRate dB/s
//     up, on active frames only, and FallRate dB/s down, on any frame. So
//     the gain after t seconds of speech is at most RiseRate·t dB.
//  5. A lookahead limiter keeps every output sample at or below LimitLevel:
//     each block's gain is at most LimitLevel over the block's peak, and it
//     recovers towards the AGC gain with a 20 ms time constant. The applied
//     gain ramps linearly from block to block.
//
// TargetLevel is therefore the output RMS that a stationary signal above
// the gate converges to, as long as its peaks at that gain stay under
// LimitLevel; otherwise the limiter holds it lower. For speech, the AGC
// holds the order-4 power mean of active frame RMS at the target, so the
// active-speech RMS of the output is somewhat below it. The ideas of items
// 2 to 4 come from the floating-point AGC of SpeexDSP 1.2.1; the design and
// code are our own. docs/algorithms.md gives the design and its measured
// behaviour.
//
// # Noise protection
//
// Noise protection is the level gate and the speech flag, and nothing
// else. Frames below GateLevel never raise the gain or change the level
// estimate. Noise above GateLevel passed with speech true is treated as
// speech and driven to TargetLevel, with a gain of up to MaxGain. The VAD of
// the SpeexDSP 1.2.1 fixed-point preprocessor reported speech on almost
// every frame of stationary white and pink noise measured at 16 kHz above
// about -65 dBFS (docs/testing.md, "Noise through the send chain"), so
// passing its decision does not prevent this; only a gate above the noise
// does. In pauses the gain cannot rise, but it may finish falling.
//
// Use the AGC after preprocess with denoising on, which lowers stationary
// noise before the AGC sees it, and set GateLevel above the residual noise.
// At the default suppression (-15 dB), preprocess was measured to reduce
// white noise at -35 dBFS by 13 to 14 dB and pink noise at -37 dBFS by only
// 4 to 6 dB. Through the whole chain with the default gate, white noise from
// about -40 dBFS and pink noise from about -52 dBFS at the input are raised
// by 16 to 23 dB (docs/testing.md, "Whole-chain measurements").
//
// # Limitations
//
// The AGC is mono. Its only noise protection is the gate and the speech
// flag (see "Noise protection"). Levels and gains are whole dB, rates
// whole dB/s. It adds Latency() samples (2 ms) for its limiter.
//
// # Rates, chunking and latency
//
// SampleRate is 8000 to 48000 Hz; there is no frame size. Process accepts
// any number of samples, including zero, and its output does not depend on
// how the stream is split into calls, as long as the speech flag is the
// same for every sample. The output is the input delayed by Latency
// samples (2 ms at rates that are multiples of 1000); the first Latency
// output samples of a stream are zero.
//
// # Buffers, aliasing and retention
//
// Process writes exactly len(in) samples to out[:len(in)] and returns
// ErrBufferSize, writing nothing, if out is shorter. in and out may be the
// same slice; any other overlap is unsupported and not detected. No
// caller slice is retained.
//
// # Errors
//
// New returns an error wrapping ErrConfig for an invalid Config; test it
// with errors.Is. It means the Config must be fixed; retrying cannot
// help. Process returns ErrBufferSize, unwrapped, when out is shorter than
// in, and ErrConfig on a zero AGC; it changes nothing then. With out at
// least as long as in, Process on an AGC from New never fails.
//
// # Zero value
//
// A zero AGC (not made by New) is not usable: Process returns ErrConfig,
// Latency returns 0, and Reset does nothing. No method panics.
//
// # Reset
//
// Reset returns the AGC to the state New produced, including clearing the
// delay line, so the output after Reset equals that of a new AGC. It keeps
// the Config and does not allocate.
//
// # Arithmetic
//
// Process uses only integer arithmetic. New derives the integer constants
// with float64 maths (math.Pow, math.Expm1), rounded to the nearest
// integer.
//
// # Concurrency and allocation
//
// An AGC is not safe for concurrent use. Use one per stream. New allocates;
// no other method allocates. Each Process call does work proportional to
// len(in) and never blocks. No method panics, on an AGC returned by New or
// on a zero AGC.
//
// [preprocess.c]: https://gitlab.xiph.org/xiph/speexdsp/-/blob/1b28a0f61bc31162979e1f26f3981fc3637095c8/libspeexdsp/preprocess.c#L563-610
// [glossary]: https://github.com/danieldonoghue/govoip-dsp/blob/master/docs/glossary.md
// [developer guide]: https://github.com/danieldonoghue/govoip-dsp/blob/master/docs/guide.md#the-pipeline
package agc
