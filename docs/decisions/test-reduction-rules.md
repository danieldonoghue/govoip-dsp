# ADR test-reduction-rules: When a test may skip or reduce under -short or -race

- **Status:** accepted.
- **Date:** 2026-10-28

## Context

`docs/contracts.md` section 9 allowed a test to reduce its work under
`-short` or `-race` only by comparing a leading part of a stream, with one
exception for packages without goroutines and one added by ADR
`wp7-race-skips`, which said that every test that starts goroutines runs in
full under `-race`. The tests do more than that:

- `TestInlining`, `TestMaskedInlining` and `TestInliningDivOrZero`
  (`internal/fixed`) and `TestScenarioIsPrefixSafe` (`integration`) skip
  whole under `-short`.
- The `echo` reference tests and `TestScenarioMeasurements` check one
  configuration per scenario under `-short`, and a smaller set under
  `-race`.
- `TestEdgesHitBoundaries` (`echo`) skips the 32 kHz and 48 kHz rows under
  `-race`.
- `TestStressSPSC` (`buffer`) moves 200,000 samples per capacity under
  `-short` and `-race`, and 1,000,000 in the normal run.

In each case the normal `go test` runs the full work.

## Decision

- A test may skip under `-short` or `-race`, or reduce its work to a named
  subset, only if the normal `go test` runs the full work.
- A goroutine test may run a shorter stream under `-race`, but the same
  concurrent paths.
- Every skip or reduction is named in the test and listed in
  `docs/testing.md`.

## Consequences

- `docs/contracts.md` section 9 states the rule. It replaces the leading
  part rule, the exception for packages without goroutines and the
  extension of ADR `wp7-race-skips`, which stays as the record of why the
  single-goroutine skips exist.
- A test that reduces its work without being named in the test and in
  `docs/testing.md` breaks the rule.

## Alternatives considered

- **Keep the leading-part rule and change the tests to meet it.** The
  whole-test skips and the one-configuration subsets of `echo` would have
  to go, and `echo` would compare a leading part of every configuration
  under `-short` and `-race`.
- **Allow no reduction under `-race`.** The `integration` run under
  `-race` took 620.6 s on the M1 Pro before the skips of ADR
  `wp7-race-skips`, against a 20-minute CI timeout.
