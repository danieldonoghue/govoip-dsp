# Acceptance criteria: `integration`

Pre-registered before the implementation, as requirements §9 demands. Changes
after the first implementation commit need a written justification in the
"Changes" section at the end of this file.

`integration` is a test-only package. It runs the MVP packages together as a
VoIP call uses them, checks the echo and preprocess pairing against the C
reference, measures the real-time budget (requirements §7), and holds the
tested recipe of `docs/usage.md`. It adds no library code.

## The chain under test

The send path of one call, per frame of `F` samples at the processing rate
`R` (8000 or 16000 Hz):

1. `echo.Canceller.Cancel(mic, far, e)`, or in buffered mode
   `Playback(far)` then `Capture(mic, e)`.
2. `echo.Canceller.Residual(res)`.
3. `vad := preprocess.Preprocessor.RunEcho(e, res)`, in place, with
   denoise on (the default) and VAD on.
4. `agc.AGC.Process(e, out, vad)`, with `agc.DefaultConfig(R)`.

Where the network or device runs at 48 kHz, `resample.Resampler` converts
the microphone and far-end streams down to 16 kHz before step 1 (quality
`resample.QualityVoIP`, 3), and the receive path converts 16 kHz back to
48 kHz. The echo filter length is 200 ms (`R/5` samples) everywhere,
which covers the requirements §7 expensive configuration.

## Parity class

- **Bit-exact** for steps 1 to 3 and the resampling: against the C chain
  of SpeexDSP 1.2.1 (FIXED_POINT, commit `1b28a0f6`), where the
  preprocessor holds the echo state through
  `SPEEX_PREPROCESS_SET_ECHO_STATE` and calls `speex_echo_get_residual`
  itself. This is the first test of the real pairing of contracts section
  12; until now `preprocess` was tested with residual spectra recorded
  from C.
- **Behavioural** for the whole chain including `agc`, which has no
  reference.

## Signals

All synthetic inputs are generated in Go from fixed seeds with
`internal/testutil` generators, which use integer arithmetic or IEEE basic
operations with explicit rounding, and integer arithmetic here, so they
are identical on every platform. The **call scenario** at rate `R`:

| Time (s) | Far end | Near end |
|---|---|---|
| 0 to 2 | silent | silent |
| 2 to 14 | speech | silent |
| 14 to 24 | silent | speech |
| 24 to 34 | speech | speech (double talk) |
| 34 to 40 | speech | silent |

- Far-end speech: `testutil.SpeechLike` blocks of 2 s, peak 16000.
- Echo: the far end convolved in integer arithmetic with a synthetic room
  response (32 ms bulk delay, a dense 2 ms early part and sparse
  reflections to 80 ms, peak tap 0.5 in Q15), so the echo lies inside the
  200 ms filter, also in buffered mode, where the reference lags by up to
  two frames.
- Near-end speech: `SpeechLike` with other seeds, peak 3000.
- Microphone noise: `testutil.WhiteNoise` of amplitude 30 throughout.
- At 48 kHz the scenario is generated at 48 kHz and resampled by the chain.

Longer runs repeat the 40 s layout with new seeds per repetition.

**Speech variant.** When the LibriSpeech cache is present
(`testutil.SpeechFiles`), the same layout uses concatenated utterances of
one speaker as far end and of another as near end, at 16 kHz and, resampled
in setup with `resample`, at 8 kHz. It skips when the files are absent and
says so.

## Bit-exact pairing (P)

A new driver, `reference/drivers/integration.c`, runs the C chain on inputs
that `integration`'s `TestHarnessInputs` writes. Cases (filter 200 ms,
denoise and VAD on, other preprocess settings at their defaults):

| Cases | Rate | Frames | Mode | Length |
|---|---|---|---|---|
| 4 | 8 and 16 kHz | 10 and 20 ms | `Cancel` | 40 s |
| 4 | 8 and 16 kHz | 10 and 20 ms | `Playback` then `Capture`, strictly alternating | 40 s |
| 2 | 48 kHz resampled to 16 kHz | 10 and 20 ms | `Cancel` | 40 s |
| 1 | 8 kHz | 20 ms | `Playback` then `Capture` | 10 minutes |
| 1 | 16 kHz | 20 ms | `Cancel` | 10 minutes |

In the resampled cases both C and Go call the resampler once per 10 or 20 ms
network frame and run the chain whenever a whole processing frame is
buffered.

Streams per case: the inputs (`mic`, `far`), the echo canceller output, the
residual spectrum after each frame, the preprocess output, the VAD decision
and `SPEEX_PREPROCESS_GET_PROB`.

1. **P1.** Every case matches: committed prefixes element by element, and
   the SHA-256 of every chunk of every stream, including the inputs.
2. **P2.** The fixtures come from the pinned container (ADR
   `wp0-reference-build`); the host clang build gives identical hashes, or
   the difference is documented in `docs/testing.md`.
3. **P3.** The `-fsanitize=undefined` build gives identical outputs. Its
   reports are listed in `docs/testing.md`.
4. **P4.** Committed files stay within requirements §4.2: no file above
   1 MB, and the `integration` fixtures below 1 MB in total.
5. **P5.** Under `-short` and `-race` the test compares a leading part of
   every case (the committed prefixes), and says so; the normal `go test`
   compares everything.

## Pipeline behaviour (B)

Measured on the call scenario at 8 and 16 kHz with 10 and 20 ms frames in
`Cancel` mode, and on the 48 kHz resampled chain with 20 ms frames.
Levels are RMS in dBFS over the named window. "Active frames" are 20 ms
frames whose output RMS is at least -50 dBFS.

1. **B1. Echo stage.** `10*log10(sum(mic^2)/sum(e^2))` over 8 to 14 s (far
   end only) is at least 20 dB. The echo tests measured 30 to 38 dB for
   comparable paths at 8 and 16 kHz.
2. **B2. Echo through the chain.** The same ratio with the chain output in
   place of `e`, over 8 to 14 s and over 36 to 40 s (after double talk),
   is at least 20 dB. This tests whether the AGC raises residual echo that
   the VAD passes as speech.
3. **B3. Near-end level.** The output RMS of the active frames over 18 to
   24 s (near end only) is within -24 ± 6 dBFS (the AGC target is -24
   dBFS).
4. **B4. Near end in double talk.** The output RMS of the active frames
   over 28 to 34 s is at most 10 dB below the B3 figure.
5. **B5. Ceiling.** No output sample exceeds the AGC ceiling for
   `LimitLevel` -1 dBFS (29205).
6. **B6. Speech variant.** B1 to B5 are reported for the LibriSpeech
   variant at 8 and 16 kHz, 20 ms frames, with the same thresholds. It
   skips without the cache.
7. **B7. Receive path.** Decoded 20 ms frames at 8 and 16 kHz resampled to
   48 kHz, and 48 kHz frames resampled to 16 kHz, one call per frame with
   an output buffer of `OutLen(frame)` samples: every call consumes its
   whole frame, and over 60 s the concatenated output equals one call over
   the whole stream.

Every figure is printed by the test (`-v`) and reported in
`docs/testing.md`. A threshold that fails is reported as a finding, with
its cause where it is found; it is not changed in the commit that makes
the test pass.

## Noise through the send chain (N)

Stationary noise only, 30 s, through `RunEcho` with no echo (`Run`),
denoise and VAD on, then `agc.Process` with `agc.DefaultConfig` (gate -50
dBFS) and the VAD decision as `speech`. Signals: `testutil.WhiteNoise` and
`testutil.PinkNoise` with peak amplitudes 2000, 600, 200 and 60, at 8 and
16 kHz with 20 ms frames. No recorded line noise is available to this
work package, so none is used.

Reported over the last 10 s, for each signal: the input RMS, the
preprocess output RMS, the fraction of frames the VAD marks as speech, the
AGC output RMS, and the AGC gain reached (output RMS over preprocess
output RMS, in dB).

1. **N1.** The figures are reported in `docs/testing.md`, with the answer
   to the question of ADR `wp4-noise-protection`: whether denoise keeps
   the residual noise below the -50 dBFS gate, and at which input levels
   it does not.
2. **N2.** The AGC gain never exceeds `MaxGain` (24 dB) by more than
   0.5 dB, and no output sample exceeds the ceiling.

## Real-time budget (T)

Requirements §7. Per-frame wall time of the whole chain, measured with the
monotonic clock around each frame, over 10 minutes of audio per
configuration (the 40 s call scenario repeated), after 2 s of warm-up that
is not counted:

| Configuration | Per frame |
|---|---|
| 8 kHz, 10 ms and 20 ms | steps 1 to 4 |
| 16 kHz, 10 ms and 20 ms | steps 1 to 4 |
| 48 kHz, 10 ms and 20 ms | microphone and far end 48 to 16 kHz, steps 1 to 4 at 16 kHz, receive path 16 to 48 kHz |
| 48 kHz, 10 ms, resampler quality 10 | as above, quality 10 |

1. **T1.** For each configuration report p50, p99 and the maximum, plus
   the number of garbage collections and heap allocations during the
   timed loop, with the hardware, OS, Go version and command, in
   `docs/performance.md`.
2. **T2.** Target on the Mac: p99 at most 10% of the frame duration, and
   the maximum below the frame duration, in every configuration.
3. **T3.** Zero heap allocations and zero garbage collections during the
   timed loop.
4. **T4.** The Linux amd64 VM is measured if it can be reached; otherwise
   it is reported as **not measured**.

The run takes minutes, so it runs only with a flag (`-budget`); the normal
`go test` runs the same code on 10 s of audio and checks T3 only. A
benchmark per configuration (`BenchmarkChain/<config>`) reports `ns/op`,
`allocs/op` and `rt-fraction` for `make bench`.

## Wiring guide (U)

`docs/usage.md` is a recipe for one call at 20 ms frames, for an Asterisk
integration through AudioSocket or external media.

1. **U1.** It covers: one set of processors per call, used from one
   goroutine; the per-frame order of `echo`, `preprocess` and `agc`;
   passing the VAD decision; a `buffer.SPSC` between the network goroutine
   and the processing goroutine, and dropping a backlog; sizing every
   buffer from the rate and frame duration with `resample.OutLen` and
   `preprocess.SupportedFrameSize`.
2. **U2.** Every Go code block in `docs/usage.md` appears verbatim in a
   test file of `integration`, and a test fails if one does not.
3. **U3.** A test runs the recipe with a network goroutine feeding the
   SPSC at 8, 16 and 48 kHz under `-race`, and checks that its output
   equals the same processors run frame by frame in one goroutine.
4. **U4.** The per-frame step of the recipe allocates nothing
   (`testutil.AssertNoAllocs` after warm-up).
5. **U5.** At least one runnable example with checked output.

## Validation run (V)

Recorded with commands and real output:

1. `make check` and `make race` pass, rebased on `master`.
2. Every fuzz target in the repository runs for 60 s each with no failure.
3. The allocation tests (`TestNoAllocs*`) pass, listed by name.
4. `make bench` runs; `make cross` builds every build-only target.

## Changes

### T3 counts the chain's allocations (during the implementation)

T3 was measured with `runtime.MemStats`, whose `Mallocs` counts the
allocations of every goroutine. The first 10 s run counted one allocation
in the 8 kHz, 10 ms loop. A memory profile at rate 1 traced it to the
runtime's background scavenger (`runtime.bgscavenge`, growing its timer
heap), not to the chain, while `testutil.AssertNoAllocs` on the same step
reported zero. T3 is now: zero allocations per frame by the chain
(`TestNoAllocsChain`, `testutil.AssertNoAllocs` after warm-up), and zero
garbage collections during the timed loop. The process-wide allocation
count is still reported with T1. The chain property is unchanged; only
allocations by other goroutines are no longer counted against it.

### Speech variant uses two chapters of one speaker (during the implementation)

The LibriSpeech cache that `make speech` fills holds 20 utterances of one
speaker (1272). The speech variant uses the utterances of one chapter as
far end and of another chapter as near end, and scales the near end by
1/4 so that its level is close to the synthetic near end (-36 dBFS on
active frames, against -33 dBFS). Its thresholds are unchanged.

### Budget input repeats one 40 s repetition (during the implementation)

The 10-minute budget loops one 40 s repetition of the call scenario
rather than repetitions with new seeds. A later run with new seeds for
every repetition, as registered, gave p50 within 2% of both looped runs
(`docs/performance.md`), and `-budget` now uses new seeds. The bit-exact
10-minute cases use new seeds as registered.

### B1 and B3 fail at 48 kHz, reported as a finding (after measurement)

On the 48 kHz chain with 20 ms frames, B1 measured 15.9 dB (floor 20) and
B3 -33.1 dBFS (window -30 to -18). The test keeps both thresholds. It
lists these two criteria for that configuration as reported findings, and
fails if either of them starts to pass, or if any other criterion fails,
so a change in either direction shows. The measured cause is in
`docs/testing.md`. Whether to change the scenario, the chain or the
criteria needs a decision.

### Input arithmetic worded exactly (after review)

"Signals" said the inputs use integer arithmetic only. `testutil.SpeechLike`
uses float64 with IEEE basic operations and explicit rounding, as
`docs/testing.md` ("Platform determinism of generated inputs") states. The
wording now says so; the inputs and their hashes are unchanged.

### Second recipe in buffered mode, criteria U6 to U10 added (after review)

A decision after review keeps the first recipe, which passes each received
frame with the frame sent at the same time to `Cancel`, for sources whose
sending and receiving are in step, and adds a second recipe for sources
where they are not (for example an Asterisk application that sends audio
only when it has some). The second recipe uses the echo canceller's
`Playback` and `Capture` and receives the sent and the received frames
through two `buffer.SPSC`s. These criteria are registered before its
code. The call scenario is as in "Signals", at 8 and 16 kHz with 20 ms
frames.

1. **U6. Uneven timing.** Each frame of the received and of the sent
   stream becomes available at its nominal time (k × 20 ms) plus an
   independent seeded delay of 0 to 30 ms (never before the frame
   before it), and the processing goroutine drains both buffers every
   20 ms. The echo stage over 8 to 14 s, as B1 measures it, is at least
   15 dB. 15 dB is below the 20 dB of B1 because the buffered mode moves
   the far end by one or two frames when its queue under- or overflows.
2. **U7. Idle sender.** The far end is white noise of peak 8000 from 2 to
   14 s, with the echo path of "Signals", microphone noise and no near
   end; nothing follows it, so the echo tail after 14 s is never silent.
   When the sender keeps sending silence and queuing it with
   `Playback`, the output energy over 14.0 to 14.3 s is at least 10 dB
   below that of a sender that queues nothing after 14 s (whose captures
   find an empty queue and pass the microphone through).
3. **U8. Two goroutines.** Received and sent frames arrive from two
   producer goroutines at irregular times; the processing goroutine
   records, for each received frame, how many sent frames it had queued
   before it. A single-goroutine replay of that order gives the same
   output, and the run passes under `-race`.
4. **U9. Allocation.** A processing tick (both buffers written and
   drained) allocates nothing (`testutil.AssertNoAllocs` after warm-up).
5. **U10. Documented.** `docs/usage.md` gives each recipe its own use
   cases and caveats, and `TestUsageCodeIsTested` covers its Go blocks.
   A drift beyond the canceller's queue (a sender pause of 100 ms, then a
   burst) is measured and reported, without a threshold.

### U6 fails, reported as a finding (after measurement)

U6 measured 0.6 dB at 8 kHz and -0.6 dB at 16 kHz (threshold 15 dB). The
threshold stays. `TestBufferedUnevenTiming` pins the failure and fails if
U6 starts to pass. `TestBufferedLagAndDelay` was added after this result,
reported only and without thresholds, to show the cause; it is in
`docs/testing.md`. What to change needs a decision.

### Recipe B queues one sent frame per received frame (after review)

A decision after the U6 result changes recipe B's `drain`: for each
received frame it queues exactly one sent frame with `Playback` (silence
when no sent frame has arrived), then calls `Capture`; later sent frames
wait for later received frames. U6 to U10 apply unchanged, with their
registered thresholds. U6, U7 and the U10 pause report now measure the
echo stage through the real `drain`, instead of through a copy of its
alternation. U8 replays the one-for-one order, including which received
frames got silence. A mutation that breaks the one-for-one rule must be
caught.

### U6 still fails with the one-for-one drain (after measurement)

With the one-for-one `drain`, U6 measured 0.7 dB at 8 kHz and 0.3 dB at
16 kHz (threshold 15 dB). The threshold stays, and the test pins the
failure again. `TestBufferedLagAndDelay` (reported only) shows that
jitter no longer matters at an echo delay of 100 ms (24.7 to 26.2 dB at
8 kHz, 19.1 to 20.3 dB at 16 kHz, for jitter from 0 to 30 ms), and that
the scenario's echo delay of 32 ms is shorter than the far end's lag in
the buffered mode. What to change needs a decision.

### Less work under `-race` (after review)

A decision after review adds a rule to `docs/contracts.md` section 9:
under `-race` a package may skip its single-goroutine tests and cases, if
every test that starts goroutines runs in full there. Under `-race`
`integration` then runs `TestRecipeThroughSPSC`, `TestRecipeDropsUnderRace`
and `TestBufferedTwoGoroutines` in full, `TestReferencePairing` as in
`-short` (the committed prefixes of every case and three cases in full),
and the short checks (`TestDrainDropsBacklog`, `TestQueueFullIsReported`,
`TestUsageCodeIsTested`, `TestReferenceCaseList`, the example). It skips,
with `t.Skip` naming the rule, the measurement tests (B, N, U6, U7, U10,
`TestFinding48k`, `TestBufferedLagAndDelay`, `TestReceivePath`) and the
allocation tests, case by case where they have cases. The normal
`go test` is unchanged. P5 still holds under `-race` in the cases it
names; no criterion threshold changes.

### U6 kept, U11 added for longer echo delays (after review)

A decision after the U6 result keeps U6 exactly as registered, as a
pinned finding: it marks the shortest echo delay recipe B can cancel.
U11 is registered here, before it is measured.

**U11. Longer echo delays.** The call scenario at 8 and 16 kHz with 20 ms
frames, its echo path moved to a bulk delay of 100 ms and of 200 ms (the
early part and the reflections follow it as in "Signals", so the path
ends 48 ms after the bulk delay), each frame of both streams up to 30 ms
late as in U6, through the real `drain`. The echo filter is 256 ms, the
longest the `echo` fixtures test, which covers the 200 ms delay plus its
48 ms tail.

1. The echo stage over 8 to 14 s, as B1 measures it, is at least 15 dB
   for both delays at both rates. The threshold is the one U6 registered
   before any figure with a longer delay existed: the B1 floor of 20 dB
   less 5 dB for the buffered mode's frame of lag; it stays above the
   10 dB floor of the `echo` criteria (`docs/acceptance/echo.md`, "Scenario
   measurements").
2. **Delay step**, reported without a threshold: the bulk delay steps
   from 100 ms to 140 ms at 8 s. Reported: the time from the step to the
   start of the first 0.5 s window, before 14 s, whose echo stage is at
   least 10 dB, as the `echo` criteria define convergence; "not reached"
   is a valid result.

### Wording of the U6 explanation, and U11's provenance (after review)

The entry "U6 kept, U11 added" says that U6 marks the shortest echo delay
recipe B can cancel. The measurements do not show that: with a one-frame
lag a 32 ms echo is still causal, and the loss there is not traced. Read
it as: U6 is a delay recipe B does not cancel. A reported sweep shows
that with up to 30 ms of jitter, delays of 60 ms and more were cancelled
and 50 ms and less were not (`docs/testing.md`).

The same entry says that U11 is registered before it is measured. Its
100 ms case had already been measured once, reported only, in
`TestBufferedLagAndDelay` (26.2 dB at 8 kHz and 20.3 dB at 16 kHz, with
a 200 ms filter), before U11 was written. U11's threshold is U6's, set
before any figure with a longer delay existed; its 200 ms case, the
256 ms filter and the delay step had not been measured.

### Recipe B sends what it plays (after review)

A decision after review changes recipe B so that the frames sent to the
network and the frames passed to `Playback` are the same by
construction. The application writes the audio it wants to send, in any
amount, into a pending `buffer.SPSC`. For each received frame the
processing goroutine takes at most one pending frame, or silence when
none is waiting, writes that frame to a second SPSC that the network
writer goroutine reads, and passes the same frame to `Playback`, then
calls `Capture`. When the writer's SPSC has no room for a frame, nothing
is sent for that received frame, `Playback` gets silence, and the pending
frame waits.

The tests then simulate the call: sent frame n plays in the slot of
received frame n on the call's clock, and its echo reaches the
microphone after the echo path's bulk delay. The microphone signal is
built from the frames actually sent, at the time each received frame
becomes available; echo of a frame not yet sent by then is left out and
counted, and the count is reported. U6 to U11 keep their thresholds and
are measured through the new code: the application's frames take the
place of the sent stream, with the same seeded delays.

**U12. Burst sender.** At 8 and 16 kHz with 20 ms frames, the
application writes all of the call scenario's far end from 2 to 14 s
into the pending SPSC at once at 2 s; the received frames have the U6
jitter; the echo path's bulk delay is 100 ms and the filter 256 ms.

1. The frames the writer sends equal, in order, the frames passed to
   `Playback`: a single-goroutine replay that passes the writer's frames
   to `Playback` gives the same canceller output, bit for bit.
2. The echo stage over 8 to 14 s, as B1 measures it, is at least 15 dB,
   U11's threshold.

Mutation checks: handing the writer a different frame from the one
passed to `Playback`, and taking more than one pending frame for one
received frame, must each be caught. A test with the application, the
network reader, the writer and the processing goroutine running
concurrently replays the observed order and passes under `-race`.

### Recipe B measured two ways (after review, before measuring)

With the processing goroutine choosing the sent frame and processing the
received frame in one step, sending and receiving are in step there, and
the Speex manual prefers `speex_echo_cancellation` for that case. So the
recipe is measured two ways against the same criteria, U6 to U12 with
their thresholds, through the same simulated call:

1. **(i)** `Playback` with the frame handed to the writer, then `Capture`
   with the received frame.
2. **(ii)** `Cancel(mic, sent, out)` with the frame just handed to the
   writer as `far`.

Both are reported side by side, with the effect of (i)'s one-frame lag.
If (ii) meets every criterion that (i) meets and is better on the echo
stage in U6, U11 and U12, `docs/usage.md` makes (ii) its main recipe A
and keeps `Playback`/`Capture` as recipe B for capture and playback
delivered on separate callbacks; otherwise (i) stays recipe B, with the
reasons stated.

### Outcome of the two-way measurement, and U6 again (after measurement)

Measured through the simulated call, both variants meet U7 to U12 and
fail U6: (i) 8.9 and 9.5 dB, (ii) 11.1 and 10.7 dB at 8 and 16 kHz. U6
keeps its threshold and is pinned again as a reported finding, in both
variants. In U6, 470 of the 2000 received frames would have needed echo
of frames not yet sent, so with up to 30 ms of jitter a 32 ms echo is not
a physical call in this design.

By the rule registered before measuring, (ii) is not clearly better: it
is better in U6 and in U11 at 16 kHz, but worse in U11 at 8 kHz (by 0.1
and 0.2 dB) and in U12 (by 0.3 and 1.2 dB). So (i), `Playback` then
`Capture`, stays recipe B, and `docs/usage.md` keeps its two recipes as
they are. (ii) avoids (i)'s one-frame lag, which matters only for echoes
shorter than realistic calls give; the tests keep measuring it
(`drainWith`) for comparison. Figures in `docs/testing.md`.

### Wording of the two-way outcome (after review)

Two statements of the entries above need a correction, without changing
any criterion. First, the sweep that "Wording of the U6 explanation"
cites (60 ms and more cancelled, 50 ms and less not) was of the earlier
one-for-one `drain`; with the current `drain`, 40 and 50 ms were
cancelled, with 16 received frames each that needed echo of frames not
yet sent. Second, the outcome entry says that (i)'s one-frame lag matters
only for echoes shorter than realistic calls give. (i) also fails the
physical 32 ms case without jitter (7.6 and 11.9 dB) that (ii) passes
(23.9 and 20.3 dB), and at 16 kHz with a 100 ms echo and up to 30 ms of
jitter the sweep gave (i) 15.1 dB against (ii) 21.0 dB with a 200 ms
filter. The count of frames that needed echo of unsent frames is a lower
bound, because the simulation models no network delay.

### Recipe A is the paced recipe with `Cancel` (decision after review)

A decision after review makes the `Cancel` variant of the paced recipe
recipe A, the main recipe: the application paces its sending off the
received frames (at most one pending frame, or silence, per received
frame, handed to the writer) and the processing goroutine calls
`Cancel(mic, sent, out)` with the frame just sent. The reasons are the
measured ones: it passes the 32 ms case without jitter (23.9 and 20.3 dB
at 8 and 16 kHz, against 7.6 and 11.9 dB with `Playback` and `Capture`);
it gave 21.0 against 15.1 dB at 16 kHz with a 100 ms echo, jitter and a
200 ms filter; it was worse only by 0.1 to 1.2 dB, in U11 at 8 kHz and
in U12; and it follows the Speex manual's advice for audio that is in
step. This decision supersedes the choosing rule of "Recipe B measured
two ways".

The earlier in-step recipe (a source that delivers each received frame
with the frame sent at the same time) becomes a note within recipe A,
with its tests. Recipe B becomes the `Playback`/`Capture` recipe for
capture and playback delivered on separate callbacks with their own
timing, using the one-for-one `drain` of "Recipe B queues one sent frame
per received frame", and keeps its measurements, stated for that case.

No threshold changes and every figure stays reported. U6 to U12 apply to
recipe A as registered: U6 fails (11.1 and 10.7 dB) and stays pinned; U7
to U12 pass. U6, U7 to U11 also stay with recipe B, as measured for its
one-for-one `drain`; U12 does not apply to it, since it does not send.
