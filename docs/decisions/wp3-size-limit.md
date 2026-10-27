# ADR wp3-size-limit: FFT size limit

- **Status:** accepted.
- **Date:** 2026-10-26

## Context

The reference FFT has no upper size limit; it refuses odd sizes and aborts
on a radix above 17. The largest MVP size is 1920 points.

## Decision

- `internal/fft` accepts sizes up to 4096 (frame sizes up to 2048) and
  rejects larger ones.

## Consequences

- A larger limit is a new decision.
