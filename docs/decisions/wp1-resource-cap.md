# ADR wp1-resource-cap: Resource cap in the resampler

- **Status:** accepted.
- **Date:** 2026-10-26

## Context

Without a cap, the resampler would accept every filter the reference
accepts. Some are very large: 4000000:1 at quality 10 builds a sinc table of
about 10^9 entries (2 GB). C would `malloc` the same, but in Go a failed
allocation ends the process; it is not an error. Over the supported
configurations the largest need is 106464 entries (96 kHz to 8 kHz, quality
10, 32 channels).

## Decision

- `resample` rejects a configuration that needs more than 16 × 1024 × 1024
  sinc-table and history entries, or more than 256 channels, with
  `ErrAllocFailed`. A rejected call changes no state.
- This is a Go-only deviation. Below the cap, behaviour is unchanged.

## Consequences

- `docs/acceptance/resample.md` (change 5) gives the derivation and the
  tests.

## Alternatives considered

- **Keep the reference limits.** A large enough request would end the
  process.
