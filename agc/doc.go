// Package agc is an automatic gain control for 16-bit-scale int16 speech,
// in integer fixed-point arithmetic.
//
// Parity: own design; contains no SpeexDSP code. SpeexDSP 1.2.1 has an AGC
// only in its floating-point build (preprocess.c); no code is taken from it.
// Acceptance is behavioural, against docs/acceptance/agc.md.
//
// # Algorithm
//
// The input is split into blocks of SampleRate/1000 samples (1 ms at rates
// that are multiples of 1000) and frames of ten blocks, counted from the
// start of the stream.
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
//  3. Consecutive active frames more than 13 dB above L, one more than a
//     10 ms click can touch (3 at rates that are multiples of 1000), reset
//     L to their level and cut the gain at once; an inactive frame ends the
//     run. Active frames more than 6 dB below L for LevelTime/3 in a row
//     reset L to their level.
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
// active-speech RMS of the output is somewhat below it. Levels and gains
// are whole dB, rates whole dB/s. The ideas of items 2 to 4 come from the
// floating-point AGC of SpeexDSP 1.2.1; the design and code are our own.
//
// Noise protection is the level gate and the speech flag, and nothing
// else. Frames below GateLevel never raise the gain or change the level
// estimate. Noise above GateLevel passed with speech true is treated as
// speech and driven to TargetLevel, with a gain of up to MaxGain. The VAD of
// the SpeexDSP 1.2.1 fixed-point preprocessor reports speech on every frame
// of stationary white and pink noise, so passing its decision does not
// prevent this; only a gate above the noise does. In pauses the gain cannot
// rise, but it may finish falling.
//
// Use the AGC after preprocess with denoising on, which lowers stationary
// noise before the AGC sees it, and set GateLevel above the residual noise.
// At the default suppression (-15 dB), preprocess was measured to reduce
// white noise at -35 dBFS by 13 to 14 dB and pink noise at -37 dBFS by only
// 4 to 6 dB. Through the whole chain with the default gate, white noise from
// about -40 dBFS and pink noise from about -52 dBFS at the input are raised
// by 16 to 23 dB (docs/testing.md, "Whole-chain measurements").
//
// # Rates, chunking and latency
//
// SampleRate is 8000 to 48000 Hz. Process accepts any number of samples,
// including zero, and its output does not depend on how the stream is
// split into calls, as long as the speech flag is the same. The output is
// the input delayed by Latency samples (2 ms at rates that are multiples
// of 1000); the first Latency output samples of a stream are zero.
//
// # Buffers
//
// Process writes exactly len(in) samples to out and returns ErrBufferSize,
// writing nothing, if out is shorter. in and out may be the same slice;
// any other overlap is unsupported. No caller slice is retained. On a zero
// AGC (not made by New), Process returns ErrConfig, Latency returns 0, and
// Reset does nothing.
//
// # Reset and reconfiguration
//
// Reset returns the AGC to the state New produced, including clearing the
// delay line. Every setting is a Config field: to change one, make a new AGC.
//
// # Arithmetic
//
// Process uses only integer arithmetic. New derives the integer constants
// with float64 maths (math.Pow, math.Expm1), rounded to the nearest integer.
//
// # Concurrency and allocation
//
// An AGC is not safe for concurrent use. Use one per stream. New allocates;
// no other method allocates. Each Process call does work proportional to
// len(in) and never blocks. No method panics, on an AGC returned by New or
// on a zero AGC.
package agc
