# ADR wp3-unported-functions: FFT functions left unported

- **Status:** accepted.
- **Date:** 2026-10-26

## Context

`fftwrap.c` and KISS FFT also provide `spx_fft_float`, `spx_ifft_float`,
`kiss_fftr` and `kiss_fftri`.

## Decision

- These four functions are not ported, because no MVP source calls them in
  the fixed-point build.

## Consequences

- `internal/fft` holds only what the ports use. A later consumer that needs
  one of them ports it then (requirements §2, "Sizing rule").
