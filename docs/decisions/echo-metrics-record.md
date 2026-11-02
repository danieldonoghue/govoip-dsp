# ADR echo-metrics-record: Where the echo scenario figures are recorded

- **Status:** accepted; supersedes in part the scenario-measurements
  statement of `docs/acceptance/echo.md` (section "Scenario measurements").
- **Date:** 2026-11-02

## Context

The scenario-measurements statement of `docs/acceptance/echo.md` (section
"Scenario measurements") says: "Every scenario's figures
are reported in `docs/algorithms.md` for every configuration, and the Go
test pins them". The figures of every configuration are now in
`echo/testdata/metrics.golden`, one line per case. The file has 384 lines:
16 scenarios in 24 configurations (4 rates, 2 frame sizes and 3 filter
lengths). The 10 long runs (`long60` at 4 rates and 2 frame sizes, and
`long10m` at 2 rates) are not measured, because `TestScenarioMeasurements`
skips them. `docs/algorithms.md` gives ranges per rate for the figures it
lists, not a line per configuration.

## Decision

- The per-configuration figures are pinned in `echo/testdata/metrics.golden`,
  for every configuration except the long runs.
- `docs/algorithms.md` gives the ranges per rate. It does not list every
  configuration.

## Consequences

- The Go test still pins the figures, so a change in behaviour is visible.
  The statement keeps its other parts, including the sanity floors.
- A change of the figures is an update of `metrics.golden`
  (`-update-metrics`) and, where a range moves, of `docs/algorithms.md`.

## Alternatives considered

- **List every configuration in `docs/algorithms.md`.** 384 lines of figures
  in a reference page, which the golden file already holds and the test
  checks.
