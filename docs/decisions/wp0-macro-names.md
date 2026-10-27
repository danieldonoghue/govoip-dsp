# ADR wp0-macro-names: Go names for the fixed-point macros

- **Status:** accepted.
- **Date:** 2026-10-26

## Context

`internal/fixed` ports the macros of `fixed_generic.h`, `arch.h`,
`math_approx.h` and `pseudofloat.h`. Exact upstream names (`MULT16_16_Q15`)
grep directly against the C, but are not idiomatic Go.

## Decision

- `internal/fixed` uses mixed-caps Go names: `MULT16_16_Q15` is
  `Mult16x16Q15`.
- The package doc has a table that maps every ported macro to its Go
  function.

## Consequences

- Ports find a macro's Go name through the mapping table.

## Alternatives considered

- **Upstream names, with underscores.** Direct to grep against the C, but
  not idiomatic Go.
