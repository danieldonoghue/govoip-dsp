# ADR wp1-api: Resampler reset and integer types

- **Status:** accepted.
- **Date:** 2026-10-26

## Context

Upstream `speex_resampler_reset_mem` clears only the first
`channels*(filt_len-1)` memory samples, so with two or more channels some
old history survives a reset. Separately, rates and ratios in the API are
`int`; where `int` is 32 bits wide, ratios above 2^31-1, which the reference
accepts as `spx_uint32_t`, cannot be expressed.

## Decision

- Multichannel `Reset` keeps the upstream behaviour (bit-exact, tested).
  There is no full-reset extension.
- Rates and ratios stay `int`.

## Consequences

- The 32-bit limit on ratios is documented.

## Alternatives considered

- **A full reset as a marked extension.** Not chosen; the port keeps the
  reference behaviour.
- **`uint32` rates and ratios.** Matches C, but differs from the `int` in
  the `docs/contracts.md` example.
