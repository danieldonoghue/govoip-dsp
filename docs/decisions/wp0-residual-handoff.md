# ADR wp0-residual-handoff: Residual echo hand-off as a plain slice

- **Status:** accepted.
- **Date:** 2026-10-26

## Context

`preprocess` needs the residual echo power spectrum that `echo` estimates.
In SpeexDSP, `speex_preprocess_state` holds a pointer to the echo state
(`SPEEX_PREPROCESS_SET_ECHO_STATE`) and calls `speex_echo_get_residual`
itself.

## Decision

- `echo.Canceller.Residual` writes the residual power spectrum into a
  caller-provided `[]int32`, and `preprocess.Preprocessor.RunEcho` reads one.
  The caller passes the slice from one to the other.
- Neither package imports the other. `docs/contracts.md` section 12 defines
  the hand-off.

## Consequences

- `preprocess` can be tested with residuals from fixtures, without `echo`.
- Calling `Residual` changes no later output, which the echo acceptance
  criteria test.

## Alternatives considered

- **`preprocess` holds a `*echo.Canceller`.** Mirrors the C pointer, but
  couples the packages and stops `preprocess` testing without `echo`.
- **An interface with a `Residual` method.** Adds nothing the slice does not
  give.
