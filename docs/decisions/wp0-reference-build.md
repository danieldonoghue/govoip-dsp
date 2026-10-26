# ADR wp0-reference-build: reference build configuration and toolchain

- **Status:** accepted by WP0; the owner reviews it at the WP0 checkpoint.
- **Date:** 2026-10-26

## Context

Requirements §4.1 fixes the reference: SpeexDSP 1.2.1, commit `1b28a0f6`,
FIXED_POINT, USE_KISS_FFT, no SIMD, no `RESAMPLE_FULL_SINC_TABLE`, no
`FIXED_DEBUG`, flags `-O2 -fwrapv -ffp-contract=off`, a sanitizer build, a
pinned Linux container as the canonical environment and the macOS host as a
cross-check. It leaves open the container's image, compiler and CPU
architecture.

## Decision

1. `reference/config.h` defines `FIXED_POINT`, `USE_KISS_FFT`, `VAR_ARRAYS`
   and an empty `EXPORT`, mirroring `configure --enable-fixed-point` with
   clang, and fails the build if any forbidden macro is defined.
   `reference/include/speexdsp_config_types.h` replaces the generated type
   header with `<stdint.h>` types.
2. The canonical container is `debian:trixie-slim` pinned by its linux/arm64
   manifest digest
   (`sha256:a66b19aa552f595b4ab276c6b4f9b24fa4546632c5ecd88cccdced4a6866323a`),
   with `clang-19=1:19.1.7-3+b1` and `libclang-rt-19-dev` at the same
   version. It runs natively on the owner's Apple Silicon Mac and on GitHub's
   `ubuntu-24.04-arm` runner, which the fixture workflow uses.
3. The sanitizer build is `-O2 -ffp-contract=off -fsanitize=undefined`,
   without `-fwrapv`: with `-fwrapv`, clang 19 reports no signed overflow and
   no shift of a negative value (measured), which would defeat the purpose.
   The harness fails if the sanitizer build's outputs differ from the plain
   build's.
4. The host mode uses the Xcode command line tools clang and is a
   cross-check; any difference is a finding for `docs/testing.md`.

## Consequences

- Integer-only components should produce identical fixtures in both modes;
  `fixed` does (`docs/testing.md`).
- Components whose initialisation calls libm (resampler sinc table,
  filterbank, preprocess) get glibc's aarch64 results as the reference.
  WP1 and WP5 compare them with the host's Apple libm and record
  differences.
- An amd64 container would add a third libm (glibc x86_64, which can pick
  FMA variants of some functions at run time on CPUs that have FMA). This is
  a belief from glibc's design, not something WP0 measured, and is not part
  of the reference.

## Alternatives considered

- **linux/amd64 container.** Matches the second reference environment's
  architecture, but runs only under emulation on the Mac, and emulation may
  hide CPU features that select libm variants. The owner chose linux/arm64
  (`docs/status.md`, "Owner decisions").
- **GCC instead of clang.** Equally valid C, but the host compiler is clang,
  so a clang container keeps one compiler family and isolates OS and libm as
  the variables.
- **autotools build.** Rejected by requirements §4.1 in favour of an
  explicit `config.h`.
