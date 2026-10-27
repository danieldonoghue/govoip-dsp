# ADR wp5-reset: What the preprocessor's Reset keeps

- **Status:** accepted.
- **Date:** 2026-10-26

## Context

The reference preprocessor has no reset function.

## Decision

- `preprocess.Preprocessor.Reset` clears the signal state and keeps the
  settings made with the setters.

## Consequences

- A caller does not reapply its settings after a reset.
