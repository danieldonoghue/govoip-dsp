# ADR wp1-stopband: Resampler stopband

- **Status:** accepted.
- **Date:** 2026-10-26

## Context

The upstream comments describe a stopband of about 100 dB, which applies to
the float build. The measured fixed-point stopband is about -55 to -70 dB
at qualities 1 to 10 (`docs/algorithms.md`).

## Decision

- The measured figure is the reference's own, because the port is
  bit-exact. No action is needed.

## Consequences

- `docs/algorithms.md` states the measured stopband.
