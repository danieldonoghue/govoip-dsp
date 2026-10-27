# ADR wp4-step-response: AGC response to +10 to +13 dB steps

- **Status:** accepted.
- **Date:** 2026-10-26

## Context

Level steps of +10 to +13 dB do not trigger the AGC's fast cut. They stay
more than 6 dB above the target for 0.38 to 0.67 s, and within 2 dB after
1.71 to 2.00 s, against 0.03 s for steps of +14 dB and more. A lower cut
threshold brings back cuts on loud speech (guards G3 and G4).

## Decision

- This behaviour is accepted without an immediate cut.
- Regression guards G5 and G6 hold it.

## Consequences

- `docs/acceptance/agc.md` and `docs/algorithms.md` record the measurements.

## Alternatives considered

- **A lower cut threshold.** Cuts loud speech.
- **An acceptance criterion for these steps.** Not registered; G5 and G6
  are regression guards, not acceptance criteria (`docs/acceptance/agc.md`).
