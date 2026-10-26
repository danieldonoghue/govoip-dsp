# ADR owner-codecs-separate-repo: codecs live in a separate repository

- **Status:** accepted by the project owner; the work is planned, not
  started.
- **Date:** 2026-10-26

## Context

A VoIP pipeline needs both signal processing and codecs. This repository is
a pure-Go DSP library whose compatibility reference is SpeexDSP 1.2.1;
codecs are a different job (encoding and decoding, not processing), with
different references and test vectors. Requirements §2 lists codecs as out of
scope.

## Decision

1. Codecs are not added to this repository.
2. They are planned for a separate repository, with one Go package per
   codec. That repository follows the same rules as this one where they
   apply: pure Go with no cgo, bit-exact or conformance tests against
   official reference software and test vectors, and zero steady-state
   allocations.
3. Which codecs are built is sized to the consumer that exists when the work
   starts. The first consumer is an Asterisk integration. Asterisk already
   transcodes, and AudioSocket delivers slin, so the Go side may need only
   slin or G.711 at first.
4. A shared codec interface is a general mechanism. Under the sizing rule
   (requirements §2) it goes to the owner before it is built.

## Candidates

| Format | Work | Notes |
|---|---|---|
| slin | None | Raw signed 16-bit PCM, the native `int16` format of this library. At most byte order and rate, and rate is `resample`'s job. |
| G.711 (PCMA, PCMU) | Very small | Table-based companding. Test against ITU-T G.191 reference software. First candidate. |
| G.722 | Moderate | 16 kHz sub-band ADPCM. ITU-T reference code and test vectors; patents expired. Second candidate. |
| G.726 | Small to moderate | Narrowband ADPCM; less common now. |
| GSM 06.10 | Moderate | Still seen in some Asterisk setups. |
| G.729 | Large | Patents expired in 2017; ITU-T reference available. |
| Opus | Very large | A full pure-Go encoder and decoder is a project of its own, and wrapping libopus would need cgo. A separate decision. |
| AMR, AMR-WB | Large | Patent licensing makes them a poor fit. |

## Consequences

This repository stays a DSP library with one compatibility reference. The
codec repository can depend on nothing from here, or at most on public DSP
packages such as `resample`.
