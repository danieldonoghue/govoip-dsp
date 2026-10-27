# ADR wp7-race-skips: Single-goroutine tests skipped under -race

- **Status:** accepted.
- **Date:** 2026-10-27

## Context

`integration` starts goroutines, so the existing `-race` exception for
packages without goroutines (`docs/contracts.md` section 9) did not apply
to it. Its `-race` run took 620.6 s on the M1 Pro, against a 20-minute
CI timeout.

## Decision

A package that starts goroutines may skip under `-race` the tests or cases
that run on one goroutine only. Every test that starts goroutines runs in
full under `-race`, and the skipped work runs in full in the normal
`go test`. Each skip calls `t.Skip` with a reason that names the rule
(`docs/contracts.md` section 9).

## Consequences

- On the M1 Pro, `go test -race ./integration` took 88.2 s instead of
  620.6 s when the skips were added, and 103.0 s with the later tests
  (`docs/testing.md`, "Race detector and short mode").
- `go test -v -race` lists every skipped test.
