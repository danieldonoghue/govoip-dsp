# Acceptance criteria: `agc`

Pre-registered before the implementation, as requirements §9 demands.
Changes after the first implementation commit need a written justification in
the "Changes" section at the end of this file, and are never made in the
commit that makes a failing test pass.

## Parity class

**Behavioural.** SpeexDSP 1.2.1 has no fixed-point AGC (`preprocess.c:563`),
so there is no reference output. The package is an own design and contains no
SpeexDSP code. Every criterion below is a signal metric with a numeric
threshold.

## Design under test

The thresholds are derived from this design, which the implementation must
follow. Symbols: `R` sampling rate, `K = R/1000` samples per block (integer
division), `F = 10K` samples per frame (10 ms when `R` is a multiple of 1000),
`τa` attack time, `τr` release time.

1. **Level measure.** For each input frame (frames are aligned to the start
   of the stream, independent of how the caller chunks it), `r` is the RMS of
   its `F` samples. The level estimate `L` is an amplitude on the 16-bit
   scale.
2. **Activity.** A frame is *active* when every sample in it was passed with
   `speech = true` and `r` is at or above the gate level. Only active frames
   change `L`; inactive frames leave it unchanged.
3. **Attack and release.** On an active frame,
   `L += c · (min(r, 4L) − L)`, with `c = 1 − exp(−F/(R·τa))` when
   `r > L` and `c = 1 − exp(−F/(R·τr))` otherwise. The `min(r, 4L)` term
   (at most +12 dB of innovation per frame) keeps a click from knocking the
   gain down.
4. **Gain.** The AGC gain is `T/L`, clamped to `[MinGain, MaxGain]`, where
   `T` is the target level. `L` starts at `T`, so a new instance starts at
   0 dB.
5. **Limiter.** Per block `j`, with input peak `p_j`, the allowed gain is
   `C/p_j` (`C` the ceiling amplitude). The limiter gain `e_j` is the minimum
   of the allowed gain, the AGC gain, and the previous `e` released towards
   the AGC gain with a 20 ms time constant. The output gain ramps linearly
   across block `j` from `min(e_{j−1}, e_j)` to `min(e_j, e_{j+1})`, so every
   gain applied inside block `j` is at most `e_j`. This needs one block of
   lookahead, so the output is the input delayed by `D = 2K` samples
   (2 ms when `R` is a multiple of 1000).

**Definition of the target level.** `TargetLevel` (dBFS) is the value the
level estimate drives the output to: for a stationary input (constant frame
RMS) above the gate, the output's RMS converges to `TargetLevel`. For speech,
the AGC holds the attack/release-weighted mean of active-frame RMS at the
target, so the active-speech RMS of the output lies near, not exactly at,
the target (criterion S1).

dBFS is `20·log10(rms/32768)`, as `testutil.DBFS`.

## Configurations

| Name | Rate | TargetLevel | LimitLevel | GateLevel | MaxGain | MinGain | AttackTime | ReleaseTime |
|---|---|---|---|---|---|---|---|---|
| default | 8000, 16000, 44100, 48000 | −24 dBFS | −1 dBFS | −50 dBFS | 24 dB | −12 dB | 500 ms | 1500 ms |
| fast | 16000 | −24 dBFS | −1 dBFS | −50 dBFS | 24 dB | −12 dB | 100 ms | 300 ms |
| fullscale | 16000 | −24 dBFS | 0 dBFS | −50 dBFS | 24 dB | −12 dB | 500 ms | 1500 ms |

`default` is what `DefaultConfig` returns. Unless a criterion names another
configuration, it uses `default` at 16000 Hz.

## Measurement

- **Alignment.** Output sample `n + D` corresponds to input sample `n`.
  Output frame `f` is `out[fF+D : (f+1)F+D]`.
- **Frame level** is the dBFS of an output frame's RMS.
- **Frame gain** `G_f` is `20·log10(RMS(output frame f)/RMS(input frame f))`,
  computed only for frames whose input RMS is at least 10 dB above the gate, so
  integer rounding does not matter.
- **Applied gain** is the gain value the implementation multiplies each
  sample by. Criteria on it are checked by an in-package test that reads it
  per sample; criteria on levels use only the output samples.
- **Convergence time** after a level step at time `t0` (aligned to a frame
  boundary) is `t1 − t0`, where `t1` is the start of the first frame after
  which every frame level up to the end of the segment is within ±2 dB of
  the target.
- **Percentiles** `p5`, `p95` are nearest-rank percentiles over the frames
  considered.
- Test signals come from `internal/testutil` (`Sine`, `WhiteNoise`,
  `PinkNoise`, `SpeechLike`) with fixed seeds, scaled to the stated RMS in
  the test. "Sine" is 1 kHz.

## Criteria

### Level convergence (C)

| ID | Test | Threshold |
|---|---|---|
| C1 | Sine, 15 s at −44 dBFS then 15 s at −24 dBFS (+20 dB step); all four `default` rates and `fast` | convergence time ≤ 1.8·τa + 0.1 s: **1.0 s** (`default`), **0.28 s** (`fast`) |
| C2 | Sine, 15 s at −24 dBFS then 15 s at −44 dBFS (−20 dB step); same configurations | convergence time ≤ 3.8·τr + 0.1 s: **5.8 s** (`default`), **1.24 s** (`fast`) |
| C3 | Sine, steps −34 → −14 and −14 → −34 dBFS, `default` 16000 Hz | as C1 and C2 |
| C4 | White noise, steps −44 → −24 and −24 → −44 dBFS, `default` 16000 Hz | as C1 and C2 |
| C5 | Steady state: 15 s of sine and of white noise at −44, −34, −24 and −14 dBFS; mean frame level over the last 2 s | within **±0.25 dB** of target (sine), **±0.5 dB** (white noise) |
| C6 | Sine at −49 dBFS (above the gate, needs +25 dB); sine at −6 dBFS (needs −18 dB) | output level within ±0.25 dB of −25 dBFS and −18 dBFS: the gain stops at exactly MaxGain and MinGain; applied gain never above MaxGain |

**Reasoning.** For a step of the amplitude by a factor 10, the one-pole
estimate in item 3 gives, without the innovation clamp, a relative level
`1 − 0.9·e^(−t/τa)` after an upward step and `1 + 9·e^(−t/τr)` after a
downward one. Within ±2 dB means the gain error is at most a factor 1.259,
so the upward step needs `t = ln(0.9/0.206)·τa = 1.47·τa` and the downward
step `t = ln(9/0.259)·τr = 3.55·τr`. The clamp limits the first phase of the
upward step to a growth of `1 + 3c` per frame until `L ≥ r/4`; that takes
`ln(2.5)/ln(1 + 3c)` frames, about `0.31·τa`, after which the remaining gap
of 0.75 shrinks to 0.206 in `1.29·τa`, so 1.6·τa in total. The thresholds add
0.2·τa (upward) and 0.25·τr (downward) for coefficient rounding to Q15, and
0.1 s for frame granularity, the 2 ms delay and the limiter release. The
downward step is slow because an amplitude-domain one-pole approaches a lower
value with the large initial gap; this is the price of a release slow enough
not to pump (criterion P3).

C5: stationary signals have a constant (sine) or nearly constant (noise)
frame RMS, so `L` settles at it. The sine error budget is the `spx_sqrt`
approximation (measured before this file: at most 0.3 % relative error for
the inputs the level measure uses, 0.03 dB) plus Q15 gain rounding. White
noise frames of 160 samples have an RMS spread of about 5.6 %; with
`τa < τr` the estimate sits slightly above the mean frame RMS, a bias of a
fraction of that spread, so ±0.5 dB.

### Pumping (P) and speech level (S)

The first 10 s of each signal are excluded (settling from the initial 0 dB).

| ID | Test | Threshold |
|---|---|---|
| P1 | Sine at −34 dBFS, 40 s | `p95 − p5` of `G_f` ≤ **0.1 dB** |
| P2 | White noise at −34 dBFS, 40 s | `p95 − p5` of `G_f` ≤ **0.5 dB** |
| P3 | `SpeechLike`, 70 s, scaled so its active RMS (frames above the gate) is −34 dBFS | `p95 − p5` of `G_f` ≤ **3 dB** |
| P4 | LibriSpeech: the 20 utterances from `make speech`, each scaled to an active RMS of −34 dBFS, concatenated (skips when absent) | `p95 − p5` of `G_f` ≤ **3 dB** |
| S1 | P3 and P4 signals | active RMS of the output (frames whose input frame is above the gate) within **±3 dB** of target |

`max − min` of `G_f` is reported for P1 to P4 but has no threshold: a single
limiter event or an utterance boundary sets it.

**Reasoning.** P1, P2: once converged, a stationary input moves `L` only by
the frame RMS spread filtered by a one-pole of 50 to 150 frames: about
`5.6 % · sqrt(1/100)`, 0.05 dB standard deviation for white noise, and
nothing for a sine. A loud syllable 6 dB above `L` lasting 150 ms raises `L`
by `1 − e^(−0.15/0.5) = 26 %` of the gap, 2 dB of gain; the slower release
then brings it back. Most syllables are smaller, so the 5 % to 95 % spread
should stay under 3 dB. This is a judgement, not a derivation, and is
registered as such. S1: the estimator's mean of frame RMS is below the
active RMS (energy mean) of speech by Jensen's inequality, and the faster
attack biases `L` upward; the two partly cancel, and ±3 dB covers either
dominating.

### Transients, overshoot and clipping (T)

Transient suite, at 16000 Hz after 10 s of `SpeechLike` with active RMS
−44 dBFS (AGC gain near +20 dB), each followed by 2 s of the same speech:

1. 1 kHz sine at −24 dBFS for 2 s (+20 dB step);
2. full-scale white noise (peak 32767) for 500 ms;
3. full-scale 100 Hz square wave (±32767) for 500 ms;
4. single impulses of +32767 and of −32768;
5. a 10 ms full-scale white noise click;
6. a full-scale 50 Hz sine for 500 ms;
7. DC at −32768 for 100 ms.

| ID | Test | Threshold |
|---|---|---|
| T1 | Suite, all `default` rates; every other criterion's signals; fuzz inputs | **0** output samples with `|y| > C`, where `C = min(32767, round(32768·10^(LimitLevel/20)))` (29205 for −1 dBFS) |
| T2 | Suite, `default` | **0** output samples at 32767 or −32768 (clipped) |
| T2b | Suite, `fullscale` | **0** output samples at −32768; `|y| ≤ 32767` (no wrap) |
| T3 | C1 signal, `default` and `fast` | total time with frame level more than 6 dB above target ≤ 0.8·τa + 0.05 s: **0.45 s** (`default`), **0.13 s** (`fast`) |
| T4 | Sine at −44 dBFS (gain +20 dB), then a 10 ms full-scale noise click, aligned to a frame and straddling two frames | AGC gain drops by ≤ **1.25 dB**; applied gain back within **1.5 dB** of its pre-click value at most **100 ms** after the click ends |

**Reasoning.** T1 and T2 follow from item 5: every gain applied in block
`j` is at most `C/p_j`, so no product exceeds `C`, and `C < 32767` for the
default ceiling. They are counts, and the target is zero. T3: with the clamp,
`L` grows from 0.1 to 0.25 of the new level in about `0.31·τa`, then from
0.25 to 0.5 (6 dB) in `ln(0.75/0.5)·τa = 0.41·τa`; the limiter caps the
peaks but not the frame RMS of a sine, so it does not shorten this. 0.8·τa
plus 50 ms covers it. T4: a click frame is clamped to `4L`, so each frame it
touches raises `L` by a factor `1 + 3c = 1.059` (`c` = 0.0198 for 500 ms),
0.5 dB; two frames give 1.0 dB. The limiter, released over 20 ms, gets within
0.5 dB of the AGC gain after `ln(1/0.0615)·20 ms = 56 ms`; with the 1 dB AGC
drop that is within 1.5 dB of the pre-click gain, and 100 ms leaves margin
for the block lookahead.

### Noise (N)

| ID | Test | Threshold |
|---|---|---|
| N1 | White noise and pink noise at −55 dBFS (below the gate), 60 s, `speech = true`, fresh instance | applied gain never above **0 dB** (its initial value); output RMS ≤ input RMS + **0.1 dB** |
| N2 | White noise at −30 dBFS (above the gate), 60 s, `speech = false`, fresh instance | as N1 |
| N3 | `SpeechLike` at active RMS −44 dBFS for 15 s, then white noise at −60 dBFS for 60 s, `speech = true` | applied gain during the noise never above its value at the end of the speech (**0** Q15 units of increase) and never above MaxGain; output noise level ≤ **−36 dBFS** (−60 + MaxGain) |

**Reasoning.** Noise below the gate, or flagged as non-speech, never makes a
frame active, so `L` and the gain cannot move: the thresholds are exact
holds. With `speech = true` and noise above the gate, the AGC cannot tell
noise from speech and raises it towards the target; that case is the
caller's VAD's job and is documented, not tested as a pass.

### Silence (Q)

| ID | Test | Threshold |
|---|---|---|
| Q1 | 60 s of zeros, fresh instance | output exactly zero; applied gain exactly 0 dB throughout |
| Q2 | `SpeechLike` at active RMS −44 dBFS for 15 s, 60 s of zeros, the same speech for 10 s | during the zeros: output exactly zero, applied gain changes by **0** Q15 units; after: highest output frame level in the first 1 s ≤ highest in the last 5 s before the silence + **1 dB** |
| Q3 | As Q2, with ±1 LSB random dither instead of zeros | applied gain changes by 0 Q15 units during the dither; same burst bound |
| Q4 | As Q2, but the speech after the silence is 20 dB louder | T1 and T2 hold |

**Reasoning.** Silence and dither are below the gate, so the gain holds
exactly, and speech that resumes at the old level meets the old gain: no
burst. The 1 dB covers the speech's own frame-to-frame variation between the
two windows.

### Functional requirements

These are pass/fail tests, not thresholds:

- chunk-partition invariance: any partition of the input (with the speech
  flag constant within each chunk) gives bit-identical output (test and fuzz
  target);
- `Reset` gives output identical to a fresh instance;
- exact aliasing (`in` and `out` the same slice) gives the same output as
  separate slices;
- `len(out) < len(in)` returns `ErrBufferSize` and writes nothing;
- invalid configurations return `ErrConfig`; `New` never panics (fuzz);
- zero allocations per `Process` call in steady state;
- a 10-minute run at 48000 Hz keeps every state variable in its documented
  range.

## Benchmark targets

Mac reference environment (`docs/performance.md`):

- `Process` on a 10 ms frame at 48000 Hz (480 samples) at most **10 µs**
  (`rt-fraction` ≤ 0.001), and 0 allocs/op;
- the same for 80, 160 and 320 sample frames at 8, 16 and 32 kHz, a 20 ms
  frame at 16 kHz and 37-sample chunks, all reported;
- `New` measured and reported separately.

**Reasoning.** Per sample the work is one multiply for the output, one for the
energy, a compare for the peak and a ring-buffer step; per block one division;
per frame one square root and one 64-bit division. That is a few nanoseconds
per sample, about 2 µs per 480-sample frame; 10 µs leaves a factor of five.

## Design v2 (level-estimator redesign)

The owner chose to redesign the level estimator (2026-10-26) after design
v1 above failed P4. Design v1 stays on record as the fallback: its last code
state is the commit "agc: check the ceiling and exact gain limits in more
acceptance tests", and its measurements stay in "Results". Design v2 is
judged against the same criteria and the same threshold numbers, with
changes 2 and 3, and the additions in this section. It was written down here
before its implementation was committed. A prototype on a private copy of the
branch was used to choose LevelTime and the drop window; no threshold was
chosen or changed from it.

Items 1, 2 and 5 of "Design under test" stay. Items 3 and 4 become:

3. **Level estimate.** `E` is an exponential mean of `x⁴` over active
   frames, and `L = E^(1/4)`: a power mean of order 4 of frame RMS. Each
   active frame updates `E` towards `x⁴` with `x = min(r, 2L)` (+6 dB of
   innovation at most) and time constant `LevelTime`. Until one `LevelTime`
   of active frames has been seen, the weight is `max(c, 1/n)` for the
   `n`-th active frame, so `E` starts as the plain mean of the frames seen
   (no prior level). `E` and its roots use `fixed.Float`.
   - **Why order 4.** Speex's float AGC uses order 5. Order 4 needs only
     two squarings and two `FloatSqrt` calls, which `internal/fixed` has;
     order 5 needs a fifth root, which it does not. Both weight a frame
     10 dB above the rest by 100 or more (10² against 10^2.5), so the
     estimate follows the loud frames of speech rather than the dips.
   - **Sustained rise (immediate cut).** If 3 consecutive active frames each
     have energy above `10·L²` (+10 dB), `E` becomes the 4th power of the
     smallest of their `r`, and the AGC gain is set to the new target gain
     at once, bypassing the fall limit. Three frames (30 ms) is the
     shortest run a 10 ms click cannot produce, since a click touches at
     most two frames.
   - **Sustained drop.** If `LevelTime/3` consecutive active frames each have
     `r < L/2` (−6 dB), `E` becomes the 4th power of the largest of their
     `r`. Without this, the order-4 mean would need about 18·`LevelTime` to
     fall 20 dB.
4. **Gain.** The target gain is `TargetLevel/L`, clamped to
   `[MinGain, MaxGain]`. The AGC gain starts at 0 dB and moves towards the
   target gain by at most `RiseRate` dB/s up, and only on active frames, and
   by at most `FallRate` dB/s down, on any frame. The immediate cut is the
   only exception.
   - **Cautious start.** Starting at 0 dB and rising only on active frames
     already bounds the gain after `t` seconds of active frames by
     `RiseRate · t` dB, which is what Speex's growing maximum gain
     (`init_max`) provides. No separate cap is added, because it could never
     bind; criterion R3 checks the bound.

`Config` replaces `AttackTime` and `ReleaseTime` with `RiseRate` and
`FallRate` (whole dB/s) and `LevelTime`. The speech input stays a `bool`.

### Configurations for design v2

| Name | Change from the v1 table |
|---|---|
| default | `RiseRate` 12 dB/s, `FallRate` 40 dB/s, `LevelTime` 3 s, instead of the attack and release times |
| fast | `RiseRate` 60 dB/s, `FallRate` 200 dB/s, `LevelTime` 600 ms: five times the default speed, as v1's fast was five times v1's default |
| fullscale | as default, with `LimitLevel` 0 dBFS |

### Test-structure changes for design v2

These change how tests are set up, never a threshold value:

1. **C1, C2, T3 thresholds** are the registered numbers: 1.0 s, 5.8 s and
   0.45 s for `default`, and 0.28 s, 1.24 s and 0.13 s for `fast`. The tests
   no longer compute them from the v1 formulas in τa and τr, which have no
   meaning in v2.
2. **`fast`** is defined by the table above.
3. **"AGC gain"** (T4, N3, Q2 to Q4) is v2's gain before the limiter, as in
   v1.
4. Every other signal, length, settling window and measurement stays as
   registered.

### New criteria for design v2 (R)

All of these use the per-frame AGC gain that the in-package test reads,
`default` at 16000 Hz unless stated. The tolerance of 0.01 dB covers Q15
rounding of one step.

| ID | Test | Threshold |
|---|---|---|
| R1 | Every signal of C1 to C4, P3, P4, the T1 suite, N3 and Q2 to Q4 | the AGC gain rises by at most `RiseRate · F/R` + 0.01 dB per frame (0.13 dB at 16000 Hz), and never rises after an inactive frame |
| R2 | Same signals | apart from immediate cuts, the AGC gain falls by at most `FallRate · F/R` + 0.01 dB per frame (0.41 dB) |
| R3 | `SpeechLike` with active RMS −48 dBFS (needs +24 dB), fresh instance | after `n` active frames the AGC gain is at most `n · RiseRate · F/R` + 0.01 dB |
| R4 | C1 signal (+20 dB sine step), `default` and `fast` | exactly one immediate cut, within 50 ms of the step |
| R5 | T4 click signals (aligned and straddling) and the T1 suite's 10 ms click | no immediate cut |

**Reasoning.** R1 to R3 are the definitions of the mechanisms, so the only
allowance is rounding. R4: the cut needs 3 frames (30 ms), plus up to one
frame of alignment. R5 is the condition the 3-frame rule was chosen for.

### Regression guards for design v2 (G)

Registered on 2026-10-26 at the owner's request, **after** design v2 was
measured on these files. They are regression guards, not acceptance
evidence: they exist to catch a revert of two design choices that no
acceptance criterion separates, the +13 dB cut threshold and the order-4
power mean.

| ID | Test | Threshold |
|---|---|---|
| G1 to G4 | The P4 signal (the 20 LibriSpeech utterances), each utterance scaled to an active RMS of −38, −30, −24 and −20 dBFS, 10 s of settling, `default` at 16000 Hz | `p95 − p5` of `G_f` ≤ **3 dB** at every level |

**Reasoning.** Measured before registration: design v2 gives 2.05, 2.34,
2.42 and 2.55 dB. With the +10 dB cut threshold it gives 2.05, 2.34, 3.50
and 5.39 dB, so G3 and G4 fail. With order 2 it gives 1.60, 1.90, 2.14 and
6.78 dB, so G4 fails. The same 3 dB as P4 catches both, and leaves 0.45 dB
of margin at −20 dBFS.

### Regression guards for the cut threshold (G5, G6)

Registered on 2026-10-26 at the owner's request, **after** measuring design
v2. The owner accepted that steps of +10 to +13 dB do not cut. These guards
pin the +13 dB cut threshold from above (G5) and the accepted overshoot of
the steps just below it (G6). They are regression guards, not acceptance
evidence.

| ID | Test | Threshold |
|---|---|---|
| G5 | Sine, 15 s at −44 dBFS then 15 s at −30 dBFS (+14 dB step), `default` at 8000, 16000, 44100 and 48000 Hz | exactly one immediate cut, within 50 ms of the step (as R4); within 2 dB of the target at most **0.05 s** after the step |
| G6 | Sine, 15 s at −44 dBFS then +10, +11, +12 and +13 dB, same rates | time to within 2 dB of the target at most **1.76, 1.88, 1.95, 2.05 s**; time more than 6 dB above the target at most **0.43, 0.53, 0.62, 0.72 s** |

**Reasoning.** Measured before registration at the four rates: the +14 dB
step cuts at the third frame and is within 2 dB after 0.03 s. The +10 to
+13 dB steps take 1.71, 1.81 to 1.83, 1.90 and 1.995 to 2.00 s to come
within 2 dB, and spend 0.379 to 0.38, 0.479 to 0.48, 0.569 to 0.57 and
0.668 to 0.67 s more than 6 dB above the target. G6's thresholds are the
largest of these plus 0.05 s (5 frames). The arithmetic is deterministic,
the spread across rates is at most 0.02 s, and 5 frames also covers a
change of one unit in a rounded constant on another platform. A change of
the level estimate's dynamics, its clamp or the cut rule that lengthens
these times by more than 5 frames (2.5 % to 13 % of them) fails G6. G5
fails if the threshold rises above +14 dB (for example `cutRatio` 40,
+16 dB). Lowering the threshold makes G6 pass trivially, which G3 and G4
catch.

### Noise-floor safeguard (NF), trial

Registered on 2026-10-26 at the owner's request, before implementation.
The safeguard is kept only if every registered criterion (C, P, S, T, N,
Q, R, G) still passes and N4 passes; otherwise it is reverted and the
limit is documented as it stands.

**Rule.** A minimum-statistics noise-floor estimate of the input:

1. For every frame, active or not, the frame RMS `r` updates the minimum of
   the current sub-window of 10 frames (100 ms).
2. The last 15 sub-window minima (1.5 s) are kept. The noise floor `N` is
   the smallest of them and of the current sub-window's minimum. Until the
   first sub-window is complete, `N` is 0.
3. The AGC gain may rise on an active frame only if `r² > 10·N²`
   (`r` more than 10 dB above the floor). Falls, the cut, the level
   estimate and everything else are unchanged. `speech` stays a `bool`.

| ID | Test | Threshold |
|---|---|---|
| N4 | `testutil.PinkNoise` at a long-term RMS of −55 dBFS (the reported N1 case), 60 s, `speech = true`, fresh instance | applied gain never above **+3 dB** (design v2 without NF: +23.16 dB) |

**Reasoning and expected risk.** Noise is near its own floor, so its frames
rarely stand 10 dB above it; speech has pauses that set the floor low.
The risk is that a stationary signal is also at its own floor: the sine
and white-noise signals of C2, C5, C6, P1 and P2 need the gain to rise
with no pauses, so they may fail. That is measured, not assumed.

### Stationary noise with the speech flag set (N5)

Registered on 2026-10-26 at the owner's request, **after** measuring design
v2. WP5 found that the SpeexDSP 1.2.1 fixed-point VAD reports speech on
every frame of stationary noise, so `speech` from `preprocess` does not
protect the AGC against steady background noise.

| ID | Test | Threshold |
|---|---|---|
| N5 | White and pink noise at −60, −52, −45, −35 and −24 dBFS, 120 s each, `speech = true` throughout, fresh instance | if every 10 ms frame is below the gate: applied gain never above **0 dB**; otherwise: output RMS over the last 10 s at most **TargetLevel + 0.5 dB**, and applied gain never above MaxGain |

**Reasoning.** "Gain near 0 dB on stationary noise" cannot be met for
white noise together with the registered criteria: C4 and C5 require
white noise at −44 and −34 dBFS, passed with `speech = true`, to be raised
to the target (P2 only bounds the spread of the gain). No criterion
requires pink noise to be raised; for it, 0 dB is ruled out by the
level-based design, not by a criterion. A level-based AGC cannot tell
either noise from a wanted stationary signal. The bound above is what
design v2 meets: measured before registration, noise below
the gate keeps 0 dB, and noise above it is driven to −23.98 to
−28.22 dBFS (the target is −24 dBFS) with gains up to +23.86 dB.

## Changes

1. **N3, Q2, Q3: reference gain clarified** (before any test was written or
   run). "Its value at the end of the speech" and "applied gain changes by 0
   Q15 units" are measured as follows: the AGC gain (item 4) changes by 0
   Q15 units during the noise, silence or dither, and the applied gain stays
   at or below that AGC gain throughout. Reason: after the last gain change
   in the speech, the limiter gain still approaches the AGC gain with its
   20 ms release (item 5), so the applied gain can rise by a fraction of
   that last step after the speech ends. That rise is part of the speech's
   own gain change, not a response to the noise or silence, which is what
   these criteria test.
2. **C4: convergence measured on the gain** (after the first run, which
   failed C4 down: 6.08 s against 5.8 s). For white noise, C4 uses the
   *gain-corrected frame level*, the nominal input level of the segment plus
   `G_f`, instead of the output frame level. The thresholds are unchanged,
   and so are C1 to C3, where the two measures are equal because a sine's
   frame RMS is constant. Reason: the input white noise's own 10 ms frame
   levels span −45.18 to −42.94 dBFS around −44 dBFS (measured), so the
   output frame level leaves the ±2 dB band whenever a frame of the input
   does, long after the gain has converged. Measured on the AGC gain, the
   same run converges in 5.34 s, against 5.31 s for the sine. The original
   measure tested the test signal, not the AGC. The output frame level is
   still logged.
3. **N1: pink noise below the gate frame by frame** (after the first run,
   which failed N1 pink: applied gain up to +9.99 dB, output 6.38 dB above
   the input). N1's pink noise is scaled so that its loudest 10 ms frame is
   1 dB below the gate, instead of to a long-term RMS of −55 dBFS. Reason:
   N1 tests noise *below the gate*, and the gate is per frame (item 2).
   `testutil.PinkNoise` at a long-term RMS of −55 dBFS has 192 of 6000
   frames at or above −50 dBFS (loudest −46.3 dBFS, measured), because its
   slowest rows wander for seconds. Those frames are active by design. The
   original signal stays in the test as a reported case without a threshold,
   and the limitation it shows (noise whose frames cross the gate is
   amplified unless the caller passes `speech = false`) is documented in the
   package and in `docs/algorithms.md`.
4. **Correction to change 2** (after the independent review). The reason
   given in change 2 is inaccurate. No input frame leaves the ±2 dB band:
   the measured frames lie within ±1.2 dB of −44 dBFS. The cause is that,
   late in the downward step, the remaining gain error (close to 2 dB) adds
   to the frame-to-frame spread of the input. Change 2 therefore makes C4
   less strict than the output-level measure it replaces: 5.37 s measured
   on the gain, against 6.08 s on the output frame level. Change 2 also
   drops a rule without saying so. For C4, `G_f` is taken on every frame,
   because the −44 dBFS segment is only 6 dB above the gate, so the
   "10 dB above the gate" rule in "Measurement" would leave no frame to
   measure. Both changes 2 and 3 are put to the owner in `docs/status.md`.
5. **Owner decision on changes 2 and 3** (2026-10-26, relayed by the
   coordinator). The owner confirmed change 2 (C4 measured on the gain, as
   corrected by change 4) and change 3 (N1 pink noise scaled so that every
   frame is below the gate). The limitation that the original N1 pink
   signal shows stays documented in `agc/doc.go` and `docs/algorithms.md`.
6. **Design v2 corrections after its independent review** (2026-10-26).
   No threshold or test signal changes. The design text in "Design v2"
   changes as follows, for the reasons measured by the review and
   rechecked on a scratch copy:
   - **Loud run consecutive in time.** An inactive frame now ends the run.
     Before, clicks in pauses added up: three clicks a second apart cut
     the gain from +19.4 dB to −12 dB. Now: 19.23 → 18.71 dB.
   - **Run length from duration.** The run is one frame more than a 10 ms
     click can touch, `(F + c − 2)/F + 2` frames with `c` the samples in
     10 ms: 3 frames at rates that are multiples of 1000, 4 at 9999 and
     15999 Hz. Before, one click at 9999 Hz cut the gain from +20.0 to
     −2.8 dB. Now the lowest gain is 19.71 dB.
   - **Cut threshold +13 dB (energy ratio 20) instead of +10 dB.** With
     +10 dB, ordinary speech louder than the registered P4 level met the
     rule: P4 on the same 20 utterances at active levels −38, −34, −30,
     −27, −24 and −20 dBFS was 2.05, 2.19, 2.34, 3.41, 3.50 and 5.39 dB,
     with 0, 0, 0, 1, 1 and 3 cuts. With ratio 20 it is 2.05, 2.19, 2.34,
     2.38, 2.42 and 2.55 dB, with no cuts; ratios 32 and 50 gave the same.
     The +20 dB steps of C1, C3, C4 and T3 still cut at 30 ms. This choice
     used the P4 files at other levels, so it is not an out-of-sample
     result.
   - **Mean update without drift.** `E` is updated as `E − c·E + c·x`
     instead of `(1 − c)·E + c·x`, so `FloatMult` truncation no longer
     biases the level low. The worst steady-state level error on a sine
     (8000, 44100 and 48000 Hz, −79 to −3 dBFS) was −0.02 to −0.20 dB
     depending on `LevelTime`, and is now −0.024 dB for 100 ms, 3 s and
     10 s.
   - **Gain in Q22.** The gain is kept with 7 more fractional bits, so the
     rate limits hold at low gains: at MinGain −40 dB with 1 dB/s, the worst
     rise was 2.65 dB/s and is now 1.007 dB/s, the worst fall 1.020 dB/s
     (Q15 rounding of the per-frame factor).
   - **First frame clamped.** The first active frame is clamped to twice
     the initial level (the target), like every later frame, so a click as
     the first frame raises the level by at most 6 dB.
7. **Noise-floor safeguard trial: failed, reverted** (2026-10-26). The rule
   registered under "Noise-floor safeguard (NF), trial" was implemented exactly
   as written and measured against every criterion. It failed C2 (all rates and
   `fast`), C3 down, C4 down, C5 at −44 and −34 dBFS (sine and white noise), C6
   at −49 dBFS and both `TestConfigEdgeAccuracy` cases: a stationary signal is
   at its own floor, so the gain could not rise. N4 also failed, at +23.16 dB,
   unchanged: all 193 frames of the pink noise that reach the gate (by the AGC's
   fixed-point gate; change 3 counted 192 by a float dBFS measure) were more
   than 10 dB above its 1.5 s floor, because the frame RMS of this noise spreads
   by 10 to 16 dB within every 1.5 s window, so its loud frames always stand
   more than 10 dB above the raw minimum. As registered, the safeguard was
   reverted and is not part of design v2. The limit stands as documented in
   `agc/doc.go` and `docs/algorithms.md`; N4 is kept as a reported measurement.
8. **G6 reasoning corrected** (after its independent review, 2026-10-26;
   no threshold changes). The +10 dB step takes 1.71 s at 8, 16 and
   48 kHz but 1.716 s at 44.1 kHz, so the margin on the 1.76 s limit is
   0.044 s there, not 0.05 s; the other seven limits are the largest
   measured value plus 0.05 s. The claim that the margin covers a
   one-unit change of a rounded constant was measured by the review:
   `levCoef` − 1 lengthens the +10 dB time at 44.1 kHz to 1.736 s (and
   the time above target+6 dB by 0.01 s), `levCoef` + 1 shortens the
   times by 0.01 to 0.02 s, and `target` + 1 changes nothing; G6 passes in
   all three. The tightest case then keeps 0.024 s of headroom.

## Results

### Design v1

Measured on the Mac (`docs/performance.md`), 2026-10-26, with
`go test -count=1 -v ./agc` at the commit "agc: check the ceiling and
exact gain limits in more acceptance tests" (LibriSpeech present).

| ID | Threshold | Measured | Result |
|---|---|---|---|
| C1 | ≤ 1.0 s (`default`), ≤ 0.28 s (`fast`) | 0.808 to 0.810 s at all four rates; 0.170 s | pass |
| C2 | ≤ 5.8 s, ≤ 1.24 s | 5.340 to 5.368 s; 1.090 s | pass |
| C3 | as C1, C2 | 0.810 s, 5.340 s | pass |
| C4 | as C1, C2 (change 2) | 0.810 s, 5.370 s (output frame level: 0.880 s, 6.080 s) | pass as changed; down fails as registered |
| C5 | ±0.25 dB sine, ±0.5 dB noise | −0.010 to +0.007 dB; −0.127 to −0.118 dB | pass |
| C6 | ±0.25 dB of −25 and −18 dBFS | −25.007, −18.000 dBFS; gain exactly MaxGain, MinGain | pass |
| P1 | ≤ 0.1 dB | 0.008 dB | pass |
| P2 | ≤ 0.5 dB | 0.073 dB | pass |
| P3 | ≤ 3 dB | 2.212 dB | pass |
| P4 | ≤ 3 dB | 3.641 dB | **fail** |
| S1 | ±3 dB | −0.844 dB (SpeechLike), −0.765 dB (LibriSpeech) | pass |
| T1, T2 | 0 samples | 0 at all four rates; output peak 29205 = ceiling | pass |
| T2b | 0 at −32768, no wrap | 0; peak 32767 | pass |
| T3 | ≤ 0.45 s, ≤ 0.13 s | 0.369 to 0.370 s; 0.080 s | pass |
| T4 | ≤ 1.25 dB drop, ≤ 100 ms | 0.501, 1.003 dB; 42.1, 54.5 ms | pass |
| N1 | 0 dB, +0.1 dB | 0 dB and +0.000 dB (white; pink as changed). Pink at −55 dBFS as registered: +9.99 dB | pass as changed; pink fails as registered |
| N2 | 0 dB, +0.1 dB | 0 dB, +0.000 dB | pass |
| N3 | 0 change, ≤ −36 dBFS | 0 change, applied = AGC gain, −41.83 dBFS | pass |
| Q1 to Q4 | as stated | exact holds; −20.04 dBFS after vs −20.62 before | pass |
| Benchmark | ≤ 10 µs, 0 allocs | 1.26 µs, 0 allocs | pass |

### Design v2

Measured on the Mac, 2026-10-26, with `go test -count=1 -v ./agc` at the
commit "agc: apply the design v2 corrections" (LibriSpeech present).
Thresholds are the registered numbers, with changes 2 and 3; the design is
as corrected by change 6. The G, N4 and N5 rows come from the later test
commits "agc: check the P4 regression guards" and "agc: test N5,
stationary noise with the speech flag set"; `agc.go` is unchanged since
the corrections.

| ID | Threshold | v1 | v2 | v2 result |
|---|---|---|---|---|
| C1 | ≤ 1.0 s; ≤ 0.28 s (`fast`) | 0.81 s; 0.17 s | 0.03 s at all four rates; 0.03 s | pass |
| C2 | ≤ 5.8 s; ≤ 1.24 s | 5.34 to 5.37 s; 1.09 s | 2.45 s at all four rates; 0.50 s | pass |
| C3 | as C1, C2 | 0.81 s, 5.34 s | 0.03 s, 2.45 s | pass |
| C4 | as C1, C2 (change 2) | 0.81 s, 5.37 s | 0.03 s, 2.45 s (output frame level: 0.03 s, 2.45 s) | pass |
| C5 | ±0.25 dB sine, ±0.5 dB noise | worst 0.010, 0.127 dB | worst 0.016, 0.023 dB | pass |
| C6 | ±0.25 dB of −25 and −18 dBFS | −25.007, −18.000 | −25.007, −18.000 | pass |
| P1 | ≤ 0.1 dB | 0.008 dB | 0.000 dB | pass |
| P2 | ≤ 0.5 dB | 0.073 dB | 0.025 dB | pass |
| P3 | ≤ 3 dB | 2.212 dB | 0.989 dB | pass |
| P4 | ≤ 3 dB | 3.641 dB (fail) | 2.188 dB (reported at −38, −30, −24, −20 dBFS: 2.05, 2.34, 2.42, 2.55 dB) | pass |
| S1 | ±3 dB | −0.84, −0.77 dB | −1.41, −1.64 dB | pass |
| T1, T2 | 0 samples | 0 | 0; peak 29205 = ceiling | pass |
| T2b | no −32768, no wrap | 0 | 0; peak 32767 | pass |
| T3 | ≤ 0.45 s; ≤ 0.13 s | 0.37 s; 0.08 s | 0.03 s; 0.03 s | pass |
| T4 | ≤ 1.25 dB, ≤ 100 ms | 1.003 dB, 54.5 ms | 0.212 dB, 37.5 ms | pass |
| N1 | 0 dB, ≤ +0.1 dB | 0 dB, +0.000 | 0 dB, +0.000 (reported pink at −55 dBFS: +23.16 dB; v1 +9.99 dB) | pass |
| N2 | 0 dB, ≤ +0.1 dB | 0 dB, +0.000 | 0 dB, +0.000 | pass |
| N3 | 0 change, ≤ −36 dBFS | 0, −41.83 dBFS | 0, −40.87 dBFS | pass |
| Q1 to Q4 | exact holds, burst ≤ +1 dB | +0.58 dB | −0.19 dB | pass |
| R1, R2 | rate limits | n/a | no violation; each cut after a loud run and exactly to the target gain | pass |
| R3 | ≤ n·RiseRate·F/R | n/a | closest approach −0.0022 dB | pass |
| R4 | one cut within 50 ms | n/a | one cut, 30 ms after the step, all rates and `fast` | pass |
| R5 | no cut on clicks | n/a | none, including clicks in pauses and a click over 3 frames at 9999 Hz | pass |
| G1 to G4 | ≤ 3 dB each (regression guards, registered after measurement) | 4.14 dB at −30, 4.56 dB at −24 (from the review) | 2.048, 2.336, 2.420, 2.547 dB | pass |
| G5 | +14 dB step: one cut within 50 ms, within 2 dB ≤ 0.05 s | not run | one cut at the third frame, 0.03 s, at all four rates | pass |
| G6 | +10 to +13 dB: within 2 dB ≤ 1.76 to 2.05 s, above target+6 dB ≤ 0.43 to 0.72 s | not run | 1.71 to 2.00 s; 0.379 to 0.67 s | pass |
| N4 | ≤ +3 dB (noise-floor trial) | +9.99 dB | +23.16 dB without the safeguard, +23.16 dB with it | **fail**; safeguard reverted (change 7) |
| N5 | 0 dB below the gate; above it output ≤ target + 0.5 dB, gain ≤ MaxGain | not run | 0 dB below; −23.98 to −28.22 dBFS above, gain up to +23.86 dB | pass |
| Benchmark | ≤ 10 µs, 0 allocs | 1.27 µs | 1.29 µs, 0 allocs | pass |

