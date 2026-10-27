# ADR wp4-noise-protection: AGC noise protection

- **Status:** accepted.
- **Date:** 2026-10-26

## Context

The `preprocess` VAD reports speech on stationary noise, so stationary noise
above the AGC's level gate is driven to the target (criterion N5: up to
+23.86 dB of gain). A registered noise-floor safeguard failed criteria C2 to
C6, did not help, and was reverted. A level-based rule cannot tell a steady
sine or white noise, which criteria C4 and C5 require to be amplified, from
background noise.

## Decision

- The level gate is the only noise protection.
- The AGC belongs after `preprocess` denoise, and callers set `GateLevel`
  above the residual noise.
- Integration testing (WP7) measures the whole chain.

## Consequences

- `agc/doc.go` and `docs/algorithms.md` state the limitation.

## Alternatives considered

- **A default gate higher than -50 dBFS.**
- **A spectral rule**, for example flatness, which would also need the C4
  and C5 white-noise signals reconsidered.
- **A speech probability or noise estimate from `preprocess`**, which
  changes the `agc` API.
