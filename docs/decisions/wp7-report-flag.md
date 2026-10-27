# ADR wp7-report-flag: Reported-only measurements behind -report

- **Status:** accepted.
- **Date:** 2026-10-27

## Context

Some `integration` tests only report figures, for example sweeps over echo
delay and jitter, and check no threshold. They added run time to every
`go test` and CI run.

## Decision

- Reported-only measurements run only with the test flag `-report`
  (`make report`).
- Every threshold and every pinned finding stays in the normal run.
- The comparison of recipe A with the `Playback` and `Capture` path is no
  longer run. Its figures stay recorded in `docs/testing.md` and the
  acceptance "Changes", as measured at commit `09a990a`.

## Consequences

- `go test ./integration` checks every criterion with a threshold.
- The reported figures are reproduced with `make report`, not on CI.
