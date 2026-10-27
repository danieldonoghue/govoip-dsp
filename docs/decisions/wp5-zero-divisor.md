# ADR wp5-zero-divisor: Zero divisor in the noise suppressor

- **Status:** accepted.
- **Date:** 2026-10-26

## Context

`RunEcho` on a frame after silence, with a residual whose band sum wraps to
-1 (bins near 2^31-1), makes `DIV32_16_Q15` at `preprocess.c:140`
(`compute_gain_floor`) divide by zero. C is undefined there. The canonical
arm64 build gives quotient 0 (checked in `testdata/speexdsp/preprocess_zero`);
amd64 C would trap. Three more sites have no proof of a nonzero divisor, but
were never seen to reach zero.

## Decision

- At all four sites the port returns 0, as the canonical arm64 reference
  does, through `fixed.Div32x16OrZero` and `fixed.PDiv32x16OrZero`. It never
  panics.
- `RunEcho` keeps its API.

## Consequences

- `docs/algorithms.md`, "Divisions", lists the sites.

## Alternatives considered

- **Return an error from `RunEcho`** for residuals no echo canceller can
  produce. Changes the API, and the bound was not derived.
