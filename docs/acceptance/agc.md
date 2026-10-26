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
- **Frame gain** `G_f` is `20·log10(RMS(output frame f)/RMS(input frame
  f))`, computed only for frames whose input RMS is at least 10 dB above the
  gate, so integer rounding does not matter.
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

## Changes

None yet.
