# ADR local-macro-ports: Macros that one upstream file defines

- **Status:** accepted.
- **Date:** 2026-10-28

## Context

`docs/contracts.md` section 1 said "never re-derive a macro locally", with
`internal/fixed` as the single home of fixed-point operations. Some ports
keep a helper for a macro that exactly one upstream file defines:

- `top16` for `TOP16` in `mdf.c` (`echo/mdf.go`).
- `div32x16Q8` and `div32x16Q15` for `DIV32_16_Q8` and `DIV32_16_Q15` in
  `preprocess.c` (`preprocess/run.go`). `preprocess.c` defines them for
  itself, as static inline functions; no shared header defines them.
- `sMul`, `cMul`, `cMul4` and `cFixDiv` for `S_MUL`, `C_MUL`, `C_MUL4` and
  `C_FIXDIV` in `_kiss_fft_guts.h` (`internal/fft/kiss.go`).

## Decision

The no-local-macro rule covers the macros of the shared headers:
`fixed_generic.h`, `arch.h`, `math_approx.h` and `pseudofloat.h`. A port
calls `internal/fixed` for those. A macro that one upstream file defines
stays with the package that ports that file.

## Consequences

- `docs/contracts.md` section 1 states the rule and names the three
  upstream files above as examples.
- A local helper still cites the upstream file and line of its macro, and
  a port never keeps a local copy of a shared-header macro.

## Alternatives considered

- **Move the local helpers into `internal/fixed`.** `internal/fixed`
  would then hold code that none of the four shared headers defines, and
  the package that ports the defining file would no longer hold what that
  file defines.
