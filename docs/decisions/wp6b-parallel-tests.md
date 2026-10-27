# ADR wp6b-parallel-tests: Parallel reference subtests

- **Status:** accepted.
- **Date:** 2026-10-27

## Context

The `echo` and `preprocess` reference tests ran their cases one at a time.
On CI the normal `echo` test took up to 516 s and the `preprocess` race run
up to 436 s, against Go's default of 10 minutes per package. Requirements
§5.6 asks for echo runs of at least 10 minutes of audio.

## Decision

- Independent reference cases run as parallel subtests, with the same
  cases and comparisons in the normal, `-short` and `-race` runs.
- `TestScenarioMeasurements` gains one subtest level, `measure/<case>`.
- No mechanism orders the long cases: most of the wall time that remains
  is in tests that run one at a time (`docs/testing.md`, "Race detector
  and short mode").
- The 10-minute echo runs stay as they are.
- Further cuts that change test names or test content (shared generated
  speech, splitting the remaining serial tests, a wider `-race` subset)
  are separate decisions, taken after a CPU profile.

## Consequences

- On the M1 Pro, `go test ./echo` takes about 50 s instead of 213 s, and
  `go test -race ./preprocess` about 44 s instead of 93 s
  (`docs/testing.md`, "Race detector and short mode").
- The normal `echo` run peaks at 303 MB resident instead of 243 MB
  (`docs/testing.md`, "Race detector and short mode").
