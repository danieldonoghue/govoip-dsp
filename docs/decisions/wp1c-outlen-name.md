# ADR wp1c-outlen-name: Name of the resampler's output-length helper

- **Status:** accepted.
- **Date:** 2026-10-26

## Context

The resampler has a helper that returns the output length to offer one
process call. It was first named `MaxOut`, but for `inFrames` > 0 a call
writes at most one frame less than the value, so it is not a maximum.

## Decision

- The helper is `OutLen`: the length to offer one call.

## Consequences

- `docs/acceptance/resample.md` (change 14) records the rename; changes 9
  to 13 keep the old name as history.
