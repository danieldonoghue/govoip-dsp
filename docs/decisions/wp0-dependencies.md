# ADR wp0-dependencies: Dependency policy

- **Status:** accepted (requirements §11); recorded by WP0.
- **Date:** 2026-10-26

## Context

The library must be pure Go at run time, build with `CGO_ENABLED=0`, and
prefer the standard library. `golang.org/x/sys` is pre-approved for CPU
feature detection.

## Decision

1. The module has no dependencies today. `golang.org/x/sys` may be added
   when `internal/kernels` (WP9) needs `cpu`.
2. Any other module dependency needs its own ADR covering licence, purity
   under `CGO_ENABLED=0`, maintenance, platforms, performance, and why a small
   own implementation is worse.
3. Tools are not module dependencies. CI runs them at pinned versions with
   `go run <module>@<version>` (staticcheck v0.8.2; actionlint v1.7.12 was
   used locally), so they never enter `go.mod`.
4. `make deps-guard` and CI fail if a non-standard package (tests included)
   has cgo files, or if `go list -deps -test` shows a module other than this
   one and `golang.org/x/sys`.

## Consequences

- Test support (signals, metrics, FFT for spectral flatness, SHA-256
  checks) is written against the standard library in `internal/testutil`.
- FLAC decoding for speech tests is done once by an external tool in
  `make speech`, not by a Go dependency.
