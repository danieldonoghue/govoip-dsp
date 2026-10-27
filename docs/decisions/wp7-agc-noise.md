# ADR wp7-agc-noise: AGC noise after denoise is documented only

- **Status:** accepted.
- **Date:** 2026-10-27

## Context

ADR [`wp4-noise-protection`](wp4-noise-protection.md) left the level gate as
the AGC's only noise protection and asked for the whole chain to be
measured. In `integration`, with the default gate of -50 dBFS, the AGC
raises white noise from about -40 dBFS and pink noise from about -52 dBFS
at the input by 16 to 23 dB after `preprocess` (`docs/testing.md`,
"Whole-chain measurements").

## Decision

The measurement is documented. The default gate and the `agc` API do not
change.

## Consequences

- `docs/usage.md` tells callers to set `GateLevel` above the residual
  noise of the line.
- A higher default gate, or the preprocess noise estimate fed to `agc`,
  stays in `docs/architecture.md`, "Future work".
