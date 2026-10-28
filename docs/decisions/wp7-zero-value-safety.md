# ADR wp7-zero-value-safety: Zero values never panic

- **Status:** accepted; implemented in WP7b.
- **Date:** 2026-10-27

## Context

The zero values of the stateful types, a `T{}` not made by `New`, behaved
differently. `preprocess` and `internal/fft` returned `ErrConfig` from
their frame methods. A zero `echo.Canceller` panicked on every method
(`SetSampleRate` only for a valid rate). A zero `resample.Resampler`
panicked on `Process`, `ProcessFloat`, `OutLen`, `OutputLatency` and its
rate and quality setters, and its per-channel methods returned
`ErrChannel`. A zero `agc.AGC` panicked on `Process`. Requirements §16
allows no panics for any input, and `docs/contracts.md` section 2 says
nothing panics on caller input.

## Decision

- No exported method of a stateful type panics on its zero value, for
  any arguments.
- Methods with an `error` result return `ErrConfig` and write nothing.
- The other methods return zero values and change nothing, except setters
  that only store a setting, such as those of `preprocess`.
- A nil `*T` receiver is not covered.

## Consequences

- `docs/contracts.md` section 2 states the rule, each package doc states
  its zero-value behaviour, and each package tests every exported method
  with `testutil.CheckZeroValue`.
- `preprocess` `PSD` and `NoisePSD` return `ErrConfig` on a zero value
  instead of nil.
- Instances made by `New` behave as before. Measured on a loaded machine,
  the geomean change of the `resample`, `agc` and `echo` benchmarks is
  within 1.2% (`docs/performance.md`).

## Alternatives considered

- **Frame methods only**, the first wording of this decision. It would
  leave getters, setters and `Reset` able to panic.
