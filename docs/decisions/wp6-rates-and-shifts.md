# ADR wp6-rates-and-shifts: Echo canceller rates and out-of-range shifts

- **Status:** accepted.
- **Date:** 2026-10-26; revised 2026-10-27

## Context

Two reference defects make the echo canceller weak at 32 and 48 kHz: at
44.1 and 48 kHz `mdf.c` truncates the rate to 16 bits in `DIV32_16`, and
with frames of 480 samples or more (10 ms at 48 kHz, 20 ms at 32 and
48 kHz) its 32-bit energy sums overflow on ordinary speech. Separately, C
leaves shift counts outside [0, 31] undefined, and the two reference
compilers disagree for `FLOAT_MUL32`: clang 19 vectorises it for the
interior bins (saturating) and keeps the edge bins scalar (modulo 32), and
Apple clang 21 keeps it all scalar.

## Decision

- The echo canceller is supported at 8 and 16 kHz only. The port accepts
  rates up to 48 kHz and stays bit-exact there.
- Where a shift count leaves the range C defines, the port follows the
  compiled reference: `FLOAT_EXTRACT16/32` and `FLOAT_DIV32_FLOAT` take the
  count modulo 32, as both reference compilers do.
- `FLOAT_MUL32` saturates; no fixture reaches such a count there.

## Consequences

- Callers resample higher rates down first.
- A correction for high rates would be an extension outside the
  compatibility path; none is built.

## Alternatives considered

- **A corrected constant set or wider sums as an extension.** Not needed at
  8 and 16 kHz.
- **Reject rates above 32767 Hz in `New`.** The port instead accepts every
  rate the reference accepts, and documents the supported ones.
- **Treat every out-of-range count as a documented deviation.** The port
  follows the compiled reference where both compilers agree.
