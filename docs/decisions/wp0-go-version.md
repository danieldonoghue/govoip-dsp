# ADR wp0-go-version: Go language version

- **Status:** accepted by WP0.
- **Date:** 2026-10-26

## Context

Requirements §12: `go.mod` targets the oldest Go release the Go team still
supports when the module is initialised, and CI tests both supported
releases. On 2026-10-26 `https://go.dev/dl/?mode=json` lists Go 1.27.3 and
Go 1.26.10 as the supported releases.

## Decision

- `go.mod` says `go 1.26`, with no `toolchain` line.
- CI runs `1.26.x` and `1.27.x` on every test runner.

## Consequences

- The code may use everything up to Go 1.26, including `b.Loop()` and the
  `min` and `max` builtins, which the code uses.
- When Go 1.28 ships and 1.26 leaves support, raising the `go` line is a new
  decision.
