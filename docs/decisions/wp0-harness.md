# ADR wp0-harness: reference harness and fixture format

- **Status:** accepted by WP0; the owner reviews it at the WP0 checkpoint.
- **Date:** 2026-10-26

## Context

Requirements §4.2 asks for a C harness outside the Go tree, fixtures as
little-endian raw `int16` plus a JSON manifest with provenance and SHA-256
hashes, small committed files (≲ 1 MB each, ≲ 20 MB in total), long runs
compared by hash, and `go test` that never needs a compiler. `internal/fixed`
alone has 69 cases of up to 10^7 inputs, about 7 GB of raw streams.

## Decision

1. One C driver per component in `reference/drivers/`, linked with only the
   upstream sources it needs, plus a small shared library
   (`reference/common/harness.c`: SplitMix64, SHA-256, hashed streams,
   manifest writer). The driver writes the manifest itself.
2. A stream is a sequence of `int16`, `int32`, `float32` or `float64`
   little-endian elements. The manifest records the SHA-256 of every
   2^20-element chunk; an optional committed file holds a prefix. Short
   streams are committed whole.
3. Go reads manifests with `internal/testutil`, checks committed prefixes
   element by element and the rest chunk by chunk, so a failure names the
   first differing element or chunk. A full local harness output
   (`HARNESS_FULL_OUT`, `GOVOIP_REFERENCE_FULL`) gives the exact element
   anywhere.
4. Integer inputs come from SplitMix64 on both sides; their streams are
   hashed too, so a generator mismatch shows as an input mismatch. Inputs
   that need floating-point maths are written by Go (`TestHarnessInputs`)
   before the driver runs, so only one maths library is ever involved.
5. Every run also builds and runs the sanitizer variant and compares
   outputs.

## Consequences

- `testdata/speexdsp/fixed` is 2.3 MB and covers 1.79 × 10^9 stream elements,
  6.4 × 10^8 of them outputs.
- Tests regenerate up to 10^7 inputs per case, about 4 s for
  `internal/fixed` on an M1 Pro; `-short` and `-race` compare only the first
  chunk.
- Generators used for bit-exact inputs must be platform-independent
  (`docs/contracts.md` section 9).
- The SHA-256 in C is a fresh implementation; it self-tests against FIPS
  180-2 vectors at start-up, and the Go side verifies every committed file
  with `crypto/sha256`.

## Alternatives considered

- **Committing full outputs.** Far beyond the size cap.
- **A Go program writing the manifest from raw C output.** Needs gigabytes
  of intermediate files per run, and splits the format between two
  programs.
- **A weaker checksum (FNV, CRC).** Cheaper in C, but requirements §4.2 and
  the briefs name SHA-256.
- **cgo calls into SpeexDSP from tests.** Violates the no-C-in-`go test`
  rule.
