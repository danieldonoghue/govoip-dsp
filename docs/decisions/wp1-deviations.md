# ADR wp1-deviations: Resampler deviations where C is undefined or stalls

- **Status:** accepted.
- **Date:** 2026-10-26

## Context

In a few places the C resampler is undefined or stops making progress.

## Decision

These deviations are accepted, documented and tested:

- `SetRateFrac` checks for overflow before it changes any state, where C
  changes part of it and can then read outside the table.
- Offered lengths are clamped to 2^31-1 frames, where C stops making
  progress.
- After a rejected rate change, the input position saturates at 2^31-1.
  With a ratio of 2^31 or more the `-fwrapv` reference wraps instead; the
  output is zeros either way, but the number of zeros can differ (8 in C
  and 1 in Go in one measured case).

## Consequences

- `resample/doc.go` and `docs/algorithms.md` document them;
  `docs/acceptance/resample.md` records them (deviations 1 and 2, change 4).
