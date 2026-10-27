# ADR wp7-zero-value-safety: Zero values return ErrConfig

- **Status:** accepted; not yet implemented.
- **Date:** 2026-10-27

## Context

The zero values of the stateful types behave differently. `preprocess` and
`internal/fft` return `ErrConfig`. A zero `echo.Canceller` panics on its
frame methods (`Cancel`, `Capture`, `Playback`, `Residual`), on `Reset`
and on its getters. A zero `resample.Resampler` panics on `Process`,
`ProcessFloat` and `OutLen`. Neither is documented. A zero `agc.AGC`
panics on `Process`; its package documentation gives the no-panic
guarantee only for an AGC returned by `New`.

## Decision

Frame methods of every stateful type return `ErrConfig` on a zero value, as
`preprocess` and `internal/fft` do. This becomes a contract in
`docs/contracts.md` together with the fixes.

## Consequences

- `echo` and `resample` need fixes, and `agc` a documentation and code
  change, in work package WP7b (`docs/architecture.md`, "Work packages"),
  before the developer documentation of WP8.
- Until then the types behave as described above.
