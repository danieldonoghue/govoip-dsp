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
//  2. An active frame moves the level estimate L towards min(r, 4L), with
//     time constant AttackTime when r > L and ReleaseTime otherwise. The
//     4L bound (+12 dB) keeps a click from knocking the gain down. Inactive
//     frames leave L, and so the gain, unchanged.
//  3. The AGC gain is TargetLevel/L, clamped to [MinGain, MaxGain]. L
//     starts at TargetLevel, so the gain starts at 0 dB.
//  4. A lookahead limiter keeps every output sample at or below LimitLevel:
//     each block's gain is at most LimitLevel over the block's peak, and it
//     recovers towards the AGC gain with a 20 ms time constant. The applied
//     gain ramps linearly from block to block.
//
// TargetLevel is therefore the output RMS that a stationary signal above
// the gate converges to, as long as its peaks at that gain stay under
// LimitLevel; otherwise the limiter holds it lower. For speech, the AGC
// holds the attack- and release-weighted mean of active frame RMS at the
// target. Levels and gains are whole dB.
//
// Noise protection is the gate and the speech flag. Noise above GateLevel
// passed with speech true is treated as speech and raised towards the
// target; pass the VAD decision (for example from preprocess) to prevent
// that. In pauses the gain holds, so background noise in a pause gets the
// same gain as the speech around it.
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
// any other overlap is unsupported. No caller slice is retained.
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
// no other method allocates. For an AGC returned by New, each Process call
// does work proportional to len(in), and never blocks or panics.
package agc
