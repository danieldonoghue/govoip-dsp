# Acceptance criteria: test gaps

Pre-registered before any test change of this work package. It covers test
code only: no library code changes. Each criterion makes a test check what
an earlier criterion or document already promises. Changes after the first
test commit need a written justification in a "Changes" section at the end
of this file.

## Parity class

None. The criteria are checks of the tests against the criteria and
documents they serve.

## Rule for every criterion

Each new or extended check is run on a scratch copy with a deliberate
fault, which is never committed. The check must fail on the fault and
pass on the real code. The result is recorded as "caught" or "not caught"
in `docs/testing.md`. If a check cannot pass without a library change, the
check is not weakened and the library is not changed: the gap is reported
with evidence and options.

## Criteria

TG1. **`FloatSub` and `FloatGT` (`docs/acceptance/fixed.md`, criteria 5 and
     the benchmark targets).** `internal/fixed` gets a benchmark for
     `FloatSub` and one for `FloatGT`. The inlining test lists them if
     `go build -gcflags=-m` reports `can inline` for them. If it does not,
     they stay off the list, and the gap is reported with the inliner cost
     from `-gcflags=-m=2`, because criterion 5 as narrowed already names
     them as not inlining. Fault: a benchmark has no pass condition, so the
     check is that a body mutated to a constant changes the ns/op, which
     shows the benchmark does not measure an empty loop.

TG2. **Inlining list (`docs/performance.md`, "All macro ports inline").**
     `TestInlining` checks `ILog2`, `VShr32Masked`, `FloatExtract16Masked`
     and `FloatExtract32Masked`. Pass: each one reports `can inline`.
     Fault: mark `ILog2` `//go:noinline` in a scratch copy, so that it no
     longer inlines (a loop does not stop Go from inlining it).
     `TestInlining` must fail with `ILog2 does not inline`. The same for
     one masked function.

TG3. **Ceiling on every signal (`docs/acceptance/agc.md` T1, "every other
     criterion's signals").** `checkCeiling` runs on the signals of R3
     (`TestAcceptanceCautiousStart`), G1 to G4 (the loop in
     `TestAcceptanceLibriSpeech`) and G5 and G6 (`TestAcceptanceCutThreshold`,
     including the +14 dB case). Pass: zero output samples above the
     ceiling, at 32767 or -32768, or wrapped, on each. Fault: raise the
     limiter's ceiling in a scratch copy to 32767. Each of the three tests
     must report a `checkCeiling` failure. A test that does not is
     recorded as "not caught", with the reason.

TG4. **NaN through `ProcessChannelFloat` (`docs/contracts.md` section 3).**
     A test feeds `ProcessChannelFloat` a strided multi-channel input with
     NaN in some samples of one channel, and the same input with those
     samples 0, through two resamplers of the same configuration. Pass: the
     outputs of every channel are identical, the output values are
     integer-valued `int16` values, and the NaN does not change the other
     channels. Fault: in a scratch copy, convert NaN as 32767 in the
     `ProcessChannelFloat` path. The test must fail.

TG5. **AGC gain bound in 10 ms windows (`docs/acceptance/integration.md`
     N2).** `TestNoiseThroughSendChain` also compares, for every 10 ms
     window of the run (the AGC frame) from the first full window after the
     AGC delay, the output energy with the energy of the AGC input that
     produced it, aligned by the AGC latency. Pass: the ratio never exceeds
     `MaxGain` + 0.5 dB, on every one of the 16 signals. The measured
     maximum is logged. If a window exceeds the bound, the figures are
     reported and the bound is not loosened. Fault: in a scratch copy, let
     the applied gain exceed `MaxGain` by 2 dB for one 10 ms block. The
     one-second check must not see it (this shows the gap), and the new
     check must fail.

TG6. **Chunks above the capacity (`docs/acceptance/buffer.md` S2).**
     `TestStressSPSC` makes the producer and the consumer each draw chunks
     larger than the capacity at every capacity of 1, 2, 3, 7, 64 and 1000,
     and fails if either side drew none. The run time is measured before
     and after. Fault: restore the cap of 300 on the chunk size. The test
     must fail at capacity 1000.

TG7. **`-budget` flag text.** The flag description in
     `integration/budget_test.go` says that the run is 10 minutes of audio
     per configuration, not 10 minutes of wall time. Checked by reading it
     against `TestRealTimeBudget`, which feeds 10 minutes of audio.
