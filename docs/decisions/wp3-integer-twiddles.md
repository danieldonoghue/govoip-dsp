# ADR wp3-integer-twiddles: Integer twiddles in the fixed-point FFT

- **Status:** accepted.
- **Date:** 2026-10-26

## Context

The FFT work was first planned with floating-point twiddle factors. In the
fixed-point build, KISS FFT computes them with integers: `kiss_fft_alloc`
calls `kf_cexp2` (`kiss_fft.c:486`), which uses `spx_cos_norm`.

## Decision

- `internal/fft` computes the twiddles with integer arithmetic, as the
  reference does.
- The tables are checked against the C reference harness.

## Consequences

- No libm is involved in the FFT tables, so they do not depend on the
  platform's math library.
