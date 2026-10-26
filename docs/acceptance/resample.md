# Acceptance criteria: `resample`

Pre-registered before the implementation, as requirements §9 demands. Changes
after the first implementation commit need a written justification in the
"Changes" section at the end of this file.

## Parity class

**Bit-exact** with SpeexDSP 1.2.1 `resample.c` (commit `1b28a0f6`),
FIXED_POINT build, no SIMD, no `RESAMPLE_FULL_SINC_TABLE`, compiled by the
reference harness with `-O2 -fwrapv -ffp-contract=off` in the canonical
container (linux/arm64, clang 19).

For every case below, the Go port must produce exactly what the C build
produces:

1. the same constructor result (success, or the error that matches the C
   error code);
2. the same filter state after construction: `filt_len`, `oversample`,
   `int_advance`, `frac_advance`, the bits of `cutoff`, the choice of the
   direct or the interpolated kernel, and every entry of the used part of
   the sinc table (`filt_len*den_rate` entries for the direct kernel,
   `filt_len*oversample+8` for the interpolated one);
3. the same accounting for every process call: input and output lengths
   offered, input consumed, output produced and the return code;
4. the same output samples, in order;
5. after every rate, quality, reset or skip-zeros event, the same return
   code, input latency, output latency and reduced ratio.

The driver records 2 to 5 as streams (SHA-256 per stream, committed prefixes
for a few cases) and the scalar state as case parameters. Mismatches are
reported with the first differing element.

## Inputs and call sequences

- **Signal.** One deterministic `int16` stream from SplitMix64 (seed
  `Seed("signal")`), generated identically by the driver and the Go test. It
  is a sequence of segments of random length (64 to 575 samples): full-scale
  white noise, full-scale square waves (which overshoot and saturate the
  output), low-level noise (±16), silence, impulse trains and sawtooth
  ramps. Multichannel cases take consecutive samples as interleaved frames.
  A `signal` case records the first 32768 samples, and the Go test checks
  its generator against that hash.
- **Float signal.** For `float32` cases: values `v/1.5f` for integer `v` in
  [-50000, 50000] from SplitMix64 (seed `Seed("signal_f32")`), with every
  97th sample replaced by a special value: ±Inf, -0, ±0.5, ±0.99999994,
  32766, 32766.5, 32767, -32767, -32767.5, -32768, ±1e30. NaN is not sent to
  C (undefined there); the Go-only NaN policy is tested separately.
- **Call sequence.** Each case draws its calls from SplitMix64 seeded with
  `Seed(<case name>)`: offered input of 0, 1 to 4, 1 to 480 or 161 to 1160
  frames (the last exceeds the 160-sample internal buffer); offered output
  of 0, 1 to 8 (starved), or enough for the input plus up to 64. Unconsumed
  input is offered again. A case ends when all input is consumed.

## Configurations covered

Rates R = {8000, 16000, 24000, 32000, 44100, 48000, 96000} Hz.

1. **Rate pairs, mono, `int16`:** every quality 0 to 10 × every ordered pair
   in R × R (539 cases), 120 ms of input each. Each case includes the table.
2. **Fractional ratios, mono, `int16`:** every quality 0 to 10 × the ratios
   (num:den) 48000:47999, 47999:48000, 44101:44100, 1000003:999983, 7:5, 5:7,
   1:100, 100:1, 2147483646:2147483647 and 4294967294:4294967295 (110
   cases), 4800 input frames each, with the table. The last two have
   `den_rate` at or above 2^27 and 2^31, where the C `uint32` products wrap
   and `PDIV32` divides by a negative `int32`.
3. **Constructor failures:** 600000000:1 at qualities 0 and 10 (overflow in
   `multiply_frac`), 2147483647:2147483646 and 4294967295:4294967294 at
   quality 5 (the same), 6300001:3 at quality 10 (table size limit), and
   7000000 channels at 1:1 quality 0 (memory size limit).
4. **Stereo interleaved, `int16`:** every quality × the pairs 8k→16k,
   16k→8k, 16k→48k, 48k→16k, 44.1k→48k, 48k→44.1k, 48k→8k and 96k→44.1k
   (88 cases).
5. **`float32` interleaved:** qualities 0, 3, 5, 8, 10 × 8k→96k (output
   calls above the 8192-sample stack buffer), 48k→16k, 44.1k→48k and
   16k→48k, mono; plus 44.1k→48k stereo at the same qualities.
6. **Per-channel calls with strides:** `speex_resampler_process_int` and
   `_float` on interleaved buffers, each channel with its own call
   sequence, output written with stride 3: `int16` at qualities 3 and 10 ×
   48k→16k and 44.1k→48k × 2 and 3 channels; `float32` at quality 5,
   44.1k→48k, 2 channels, and at quality 10, 48k→16k, 3 channels.
7. **Events:**
   - `skip_zeros` before the first call: qualities 0, 5, 10 × 48k→16k,
     16k→48k and 44.1k→48k.
   - Quality changes mid-stream (up, down, several in a row without
     processing between them), mono and stereo.
   - Rate changes mid-stream that lengthen and shorten the filter, a
     sequence of small fractional ratio changes (drift-like), and repeated
     identical settings (the early return), mono and stereo.
   - `reset_mem` mid-stream: mono, stereo and 3 channels.
   - A rate change the reference rejects (600000000:1), which leaves the
     resampler producing zeros, followed by a valid rate that recovers.
   - A fractional rate change that overflows in `set_rate_frac`
     (999983:1000003 after 1000003:999983); the case ends at that event
     (see "Deviations").

## Go-only checks

These have no C counterpart, or C is undefined there:

- NaN `float32` input becomes 0 before saturation (contracts section 3);
  ±Inf, ±0 and the saturation boundaries are checked against C above.
- Chunk-partition invariance: for configurations from 1, 4, 5 and 7, any
  partition of the same input into calls, with any output capacities,
  gives the same concatenated output. Tested on fixed partitions and by
  `FuzzChunksResampler`, which also checks that nothing panics.
- `FuzzConfigResampler`: arbitrary configurations and sequences of rate,
  quality, reset and process calls never panic, and every invalid
  configuration returns an error.
- Zero allocations per process call in steady state, for every process
  method (`TestNoAllocs...`).
- `Reset` on a mono instance gives the output of a fresh instance fed the
  same input. For multichannel instances `Reset` reproduces the reference
  (only the first `channels*(filt_len-1)` memory samples are cleared), and
  the fixtures of 7 cover it.
- Every invalid argument returns an error: channel count, quality, zero
  ratio, stride below 1, channel index, interleaved lengths that are not a
  multiple of the channel count.

## Deviations from C, decided before implementation

1. **Atomic `set_rate_frac`.** On overflow, C has already changed the
   rates, the ratio and some channels' fractional positions, but not the
   filter; later processing can index outside the sinc table (undefined).
   Go checks every channel first and, on overflow, returns the error with
   no state changed. The fixtures stop at that event.
2. **Length clamp.** C takes `spx_uint32_t` lengths and, for an output
   length of 2^31 or more, its `(spx_int32_t)` casts stop all progress. Go
   clamps each offered length to 2^31-1 frames.
3. **No `NULL` input.** C feeds zeros for a `NULL` input pointer. Go has no
   such form; passing zeros gives identical output.

## Behavioural measurements (reported, no thresholds)

Bit-exactness defines correctness, so these numbers are reported in
`docs/algorithms.md` without pass/fail thresholds. They are measured from
the Go implementation with `float64` analysis code, for every quality 0 to
10:

1. **Latency:** `InputLatency` and `OutputLatency` for 48k→16k, 16k→48k,
   44.1k→48k and 8k→16k, and the measured delay: the output position of
   the peak response to an impulse, in output samples.
2. **Passband:** gain of steady tones (amplitude 8192) at 64 frequencies
   spaced evenly up to 0.95 f_N, where f_N is the lower Nyquist frequency.
   Reported: ripple (maximum minus minimum gain) up to 0.5 f_N, the -3 dB
   edge as a fraction of f_N, and the gain at 0.8 f_N and 0.9 f_N. Tone
   levels are least-squares fits at the known frequency over a steady-state
   window.
3. **Stopband (downsampling 48k→16k and 48k→8k):** tones between f_N and
   the input Nyquist frequency. Reported: the highest output level relative
   to the input tone, over tones above 1.1 f_N, 1.25 f_N and 1.5 f_N.
4. **Imaging (upsampling 16k→48k and 8k→16k):** tones up to 0.9 f_N. The
   output minus its fitted tone is the image and noise residual. Reported:
   its highest level relative to the tone.

## Benchmarks

`BenchmarkResamplerProcess/<config>` for 10 ms and 20 ms frames at 8k→16k,
16k→8k, 16k→48k, 48k→16k, 44.1k→48k and 48k→8k, qualities 3, 5, 8 and 10,
mono and stereo, plus odd chunk sizes (7 and 331 frames). Each reports
`ns/op`, `allocs/op`, `B/op`, `samples/s` and `rt-fraction`.

Targets:

- 0 allocations per operation in every benchmark (also asserted by tests).
- `rt-fraction` at most 0.1 for every 10 ms mono configuration on the Mac
  reference environment. This is a target, reported as measured.

## Changes

1. **More input for the event cases, and four more cases** (after the first
   implementation commit). Justification: a check that every listed event
   is applied showed that the event cases ended, for lack of input, before
   their later events, and mutation checks showed three reachable paths
   that no case reached. Nothing is removed or loosened:
   - Every event case gets 48000 input frames (the signal grows to 262144
     samples), and the Go test fails unless every listed event is applied
     (except after an overflow, which ends the case).
   - Two quality and rate changes in a row that both shorten the filter
     while magic samples are pending.
   - 270000001:270000000 at quality 0, where `16*den_rate` wraps in
     `uint32` and halves the oversampling; at quality 1 the same ratio is a
     constructor failure.
   - A mixed mode: a few per-channel calls on channel 0, then interleaved
     calls, so that the channels' states differ and the interleaved call
     reports the last channel's counts (2 channels at quality 5, 44.1k→48k;
     3 channels at quality 10, 48k→16k).
2. **Two `float32` event cases** (after change 1). Justification: a
   coverage profile of the reference replay showed that the `float32` path
   never processed magic samples or ran the zero kernel, and a mutation
   there was not caught. Added: 1:100 at quality 10 with quality drops to
   0 (magic samples worth more than the 8192-sample output chunk), and
   44.1k→48k at quality 10 with the rejected rate change and recovery of 7.
3. **One fractional ratio replaced, and broader Go-only checks** (recorded
   after review; the replacement was made in the first fixture commit,
   before the first implementation commit, without an entry here).
   Justification:
   - 4294967294:4294967295 was registered in the matrix of 2. With it,
     `den_rate + frac_advance` exceeds 2^32, the fractional position wraps
     and the input position stops advancing, so a case never consumes its
     input. It was replaced in the matrix by 1000000007:3000000019, which
     keeps `den_rate` above 2^31 (the negative `int32` divisor of
     `PDIV32`). The original ratio stays covered at qualities 0 and 10 as
     `wrap_q0_frac_4294967294_4294967295` and
     `wrap_q10_frac_4294967294_4294967295`, which run a fixed 50 calls;
     `TestReferenceCoverage` checks that they exist.
   - Review found a panic and a hang outside the fuzzed domain (ratios of
     2^31 and more after a rejected change; strides near `math.MaxInt`).
     After their fix, `FuzzConfigResampler` also draws ratios above 6e8,
     which the size limits always reject, and strides near 1, near
     `math.MaxInt` and spread between. Chunk-partition invariance is tested for all four
     process methods, not only `Process`.
4. **Deviation 4, decided after review:** after a rejected rate change, C's
   input position can overflow `int` (ratios of 2^31 and more, or a
   later filter growth). Go computes that position in `int64` and
   saturates it at 2^31-1, which stops the panics that the wrapped
   position caused in Go. Results are unchanged wherever C does not
   overflow. Where it does, the `-fwrapv` reference build wraps and, in at
   least one measured case (8000:8000 quality 0, then `set_rate(4000000000,
   1)`, then 100 input and 100 output frames), produces 8 zeros where Go
   produces 1; the output is zeros either way.
5. **Go-only resource cap** (owner decision, 2026-10-26, after review;
   pre-registered here before its implementation). Justification: the
   reference limits allow filters of about 10^9 table entries and 2 GB
   allocations, and in Go a failed allocation is fatal, not an error.
   - **Derivation.** Over the supported configurations (every pairing of
     8, 16, 24, 32, 44.1, 48 and 96 kHz, every quality, up to 32
     channels), the largest need is 96k→8k at quality 10 with 32
     channels: a 3072-entry sinc table plus 32 × 3231 history entries,
     106464 `int16` entries (208 KB), measured from the Go port.
   - **Cap.** A configuration whose sinc table plus all channels' history
     needs more than 16 × 1024 × 1024 entries (32 MiB) is rejected. That
     is about 158 times the largest supported need, so drift ratios,
     odd rates and downsampling by up to about 30000 (mono, quality 10)
     still work, while the construction cost stays below about 1.7 · 10^7
     sine evaluations. The channel count is bounded at 256, 8 times the
     largest supported count.
   - **Behaviour.** `New`, `SetRate`, `SetRateFrac` and `SetQuality`
     return `ErrAllocFailed` and change no state when the new
     configuration exceeds the cap or the channel bound. Configurations
     that C itself rejects keep C's failure path (the zero kernel), so
     the fixture cases with rejected changes are unchanged.
   - **Tests.** A configuration just under the cap succeeds; one just over
     returns `ErrAllocFailed` with the state unchanged (rates, ratio,
     quality, and later output equal to an untouched copy); 4000000:1 at
     quality 10 and 6000001:3 at quality 10 are rejected in under 10 ms
     each; more than 256 channels returns `ErrAllocFailed`. Every
     committed fixture case still passes; `initfail_q0_48000_48000_7000000ch`
     now fails on the channel bound, with the same error code as C.
6. **Corrections to change 5, after the review of the cap.** Justification:
   the review found a panic and three inaccuracies.
   - At quality 0, ratios such as 1073741823:2 make `multiply_frac` return
     at least 2^32-7, and rounding up to a multiple of 8 wraps `filt_len`
     to 0 (C is undefined there; Go panicked in `Process`). Such a filter
     is now rejected with `ErrAllocFailed` and no state change, like the
     cap. `TestFilterLengthWrap` failed before the fix.
   - "Configurations that C itself rejects keep C's failure path" holds
     for C's ratio and table limits only. C's memory limit
     (`INT_MAX/2/channels`) is always above the cap with at most 256
     channels, so there Go changes nothing where C outputs zeros.
   - The downsampling reach is about 21000 (non-integer ratios) to 32000
     (integer ratios), mono, quality 10, not "about 30000".
   - The cap bounds each configuration. Buffers only grow, so a sequence
     of accepted changes can hold more: measured 1.14 times the cap.
   - Added `TestCapBoundary`, which pins that a need of exactly the cap
     fits.
7. **Deterministic rejection check** (WP1b, owner-approved; pre-registered
   before the test change). Justification: CI run 40386500404 failed on
   windows-2025 with Go 1.26, where `TestResourceCap` reported a rejection
   of 256 channels, 100000:1, quality 10 after 64.57 ms against the 10 ms
   bound of change 5. Locally the same rejection takes 2.3 µs and
   allocates 18.6 KB in 4 allocations, so the wall-clock bound measures
   the shared runner, not the code. New criterion:
   - A rejected `New` over the cap, and a rejected `SetRate`,
     `SetRateFrac` or `SetQuality` over the cap, allocates less than
     1 MiB, measured with `runtime.MemStats.TotalAlloc` around the one
     call. A built filter at those sizes needs far more (above 32 MiB).
   - A loose wall-clock backstop remains: each rejection takes under 1 s.
     Without the cap these cases took about 14 s.
   - The 10 ms bound of change 5 is dropped.
8. **Correction to change 7, after review.** Its justification said that
   without the cap "these cases took about 14 s". Measured: 15 to 19 s
   for `New` at 4000000:1 and 6000001:3 (quality 10), 11 to 23 s for the
   setters, but about 0.37 s for 256 channels at 100000:1, which C's own
   memory limit rejects after building the table. That case is under the
   1 s backstop, so only the allocation bound catches it without the cap.
   Each setter case now gets its own resampler, so that no case can reuse
   buffers that an earlier case grew.
9. **Extension: `MaxOut` (WP1c, owner-approved; pre-registered before its
   implementation).** Justification: callers should not have to guess
   output buffer sizes. `(*Resampler).MaxOut(inFrames int) int` returns
   an upper bound on the output frames per channel that one process call
   can write when offered `inFrames` input frames per channel, in the
   current state. It is an extension; SpeexDSP has no equivalent. It must
   not allocate and must not change state; negative input gives 0.
   Criteria:
   - **Soundness (the tested property).** Before each call, for each
     channel the test offers `inFrames` frames and an output of exactly
     `MaxOut(inFrames)` frames per channel (times the channel count for
     the interleaved methods). After the call: (a) every channel wrote at
     most `MaxOut(inFrames)` frames, and (b) the call consumed all
     `inFrames` input frames. So a call never stops because the output
     filled up. This is checked for `Process`, `ProcessFloat`,
     `ProcessChannel` and `ProcessChannelFloat`, over every quality and
     rate pair of the matrix in 1, every fractional ratio of 2 except
     those of the next item, stereo and three channels, chunk sequences
     with sizes from 0 to 1160 frames, after rate and quality changes
     with magic samples pending, after `Reset` and `SkipZeros`, and in
     the zero-output state after a rejected change.
   - **Ratios where the fractional position can wrap** (`den_rate - 1 +
     (num_rate mod den_rate)` at least 2^32, for example
     4294967294:4294967295): the output per call is not bounded by the
     input there, so `MaxOut` returns 2^31-1, the largest length a call
     accepts. Tested on its own; such a buffer is not allocated.
   - **Tightness.** For a mono stream, and for interleaved channels whose
     states are equal, a call offered `MaxOut(inFrames)` output frames
     writes exactly `MaxOut(inFrames)` frames: the slack is 0. The one
     expected exception is the zero-output state after a rejected change
     with a ratio of 2^31 or more, where the saturated input position can
     only make the call write fewer frames; there the slack is measured
     and reported, not bounded. The worst slack over the matrix is
     reported.
   - **Fuzzing.** `FuzzMaxOut` draws configurations, events (quality and
     rate changes, including rejected ones, `Reset`, `SkipZeros`) and
     chunk sequences, and checks soundness on every call, and tightness
     for mono streams outside the zero-output state.
   - **Mutation checks:** an off-by-one in the bound in each direction,
     and ignoring pending magic samples, must each be caught.
   - **No allocation** (`TestNoAllocsMaxOut`), and no change to existing
     outputs: all 816 reference cases still pass.
10. **Correction to change 9 before the `MaxOut` implementation commit.**
    Justification: the first soundness run failed. With 16k→8k at quality
    0, position 1, and 161 frames offered, the call can write 80 frames,
    and an output of exactly 80 frames stops it after 160 frames: the
    kernel fills the output inside the first internal 160-sample chunk,
    and `process_int` does not start the next chunk once the output is
    full, although that chunk would write nothing. The same happens when
    pending magic samples fill the output. An output of the exact count
    therefore does not guarantee that all input is consumed.
    - `MaxOut(inFrames)` is the number of frames the call can write plus 1
      when `inFrames` > 0, so that the output never fills; for
      `inFrames` = 0 it is the number of frames the call can write.
    - **Tightness** becomes: for a mono stream, and for interleaved
      channels whose states are equal, outside the zero-output state,
      `MaxOut(inFrames)` minus the frames written is exactly 1 when
      `inFrames` > 0 and exactly 0 when `inFrames` = 0. The fuzz target
      checks the same. Soundness, the mutation checks and the other
      criteria of change 9 are unchanged.
11. **Correction to change 10, still before the implementation commit.**
    Justification: the event test failed with `ProcessFloat` offered no
    input while magic samples were pending: it wrote 0 frames where
    `MaxOut(0)` was 88. Upstream `speex_resampler_process_float` processes
    magic samples only inside its `while (ilen && olen)` loop, so with no
    input it writes nothing, while `speex_resampler_process_int` processes
    them first. The tightness criterion for `inFrames` = 0 therefore holds
    for the `int16` methods only: there the slack is exactly 0. For the
    `float32` methods with `inFrames` = 0, a call writes nothing, and the
    bound (still sound) is reported but not required to be tight.

