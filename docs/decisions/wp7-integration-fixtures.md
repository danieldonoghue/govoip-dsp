# ADR wp7-integration-fixtures: Fixtures for the whole chain

- **Status:** accepted.
- **Date:** 2026-10-27

## Context

The bit-exact test of the echo and preprocess pairing needs a C driver and
fixtures. These live outside `integration/`, in the shared harness paths.

## Decision

- The driver is `reference/drivers/integration.c`, and the fixtures are in
  `testdata/speexdsp/integration/`, following the per-component layout of
  `docs/contracts.md` section 10.
- `docs/contracts.md` section 12 states that `integration` tests the
  pairing bit for bit against the C chain (`TestReferencePairing`).
