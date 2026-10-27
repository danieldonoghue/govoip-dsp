# ADR wp2-spsc-overflow: SPSC overflow policy

- **Status:** accepted.
- **Date:** 2026-10-26

## Context

`buffer.Ring` offers two overflow policies, `Reject` and `Overwrite`. For
the lock-free SPSC ring buffer, overwriting the oldest samples would need
the producer to move the consumer's index, which needs a compare-and-swap
loop or a lock (requirements §16). Dropping the oldest samples at write
time would also need per-sample atomic storage and would end zero-copy
reads.

## Decision

- The SPSC rejects samples that do not fit. It has no `Overwrite` policy.
- The consumer drops a backlog with `Consume(Len() - k)`, which keeps the
  newest k samples. This is wait-free and needs no new API (WP2b).
- `Ring` keeps both policies.

## Consequences

- After a stall, the consumer decides how much backlog to keep.

## Alternatives considered

- **Producer-side overwrite.** Needs compare-and-swap retries or a lock on
  the audio path.
- **A new drop method.** `Consume` already does the job.
