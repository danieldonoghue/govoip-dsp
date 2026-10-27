# SpeexDSP reference harness

This directory builds small C driver programs against SpeexDSP 1.2.1 and runs
them to produce the committed fixtures in `testdata/speexdsp/<component>/`.
It holds C sources and scripts only. Ordinary `go test` never needs it.

## Files

| Path | Purpose |
|---|---|
| `fetch.sh` | Fetches SpeexDSP at commit `1b28a0f61bc31162979e1f26f3981fc3637095c8` into `speexdsp/` (git-ignored) and verifies the commit |
| `config.h` | The reference configuration: `FIXED_POINT`, `USE_KISS_FFT`; no SIMD, `RESAMPLE_FULL_SINC_TABLE` or `FIXED_DEBUG` |
| `include/speexdsp_config_types.h` | Replaces the header that `configure` generates (Linux only) |
| `Dockerfile` | The canonical toolchain: Debian 13 arm64, clang 19, both pinned |
| `fixtures.sh` | Entry point for `make fixtures` and `make fixtures-host` |
| `harness.sh` | Builds and runs the drivers with the toolchain it runs on |
| `common/harness.[ch]` | SplitMix64, SHA-256, hashed streams, manifest writer |
| `drivers/<component>.c` | One driver per component |

`build/` (git-ignored) holds binaries, Go-written inputs, sanitizer output and
`--check` output.

## Running

```sh
make fixtures        # regenerate every fixture set (container if Docker works)
make fixtures-host   # rebuild with the host clang and compare with the committed hashes
reference/fixtures.sh fixed          # one component
HARNESS_MODE=host reference/fixtures.sh fixed
```

Each run builds every driver twice: with `-O2 -fwrapv -ffp-contract=off`, which
writes the fixtures, and with `-O2 -ffp-contract=off -fsanitize=undefined`,
which writes to `build/<mode>/ubsan/`. The sanitizer build leaves out `-fwrapv`,
because with it clang reports no signed overflow and no shift of a negative
value. The script fails if the two builds give different outputs. The sanitizer
log is `build/<mode>/ubsan/<component>.log`.

## Adding a driver

1. Create `drivers/<component>.c`. Its header comment holds two directives:

   ```c
    * harness-sources: fftwrap.c kiss_fft.c kiss_fftr.c
    * harness-inputs: ./internal/fft
   ```

   `harness-sources` lists the upstream `libspeexdsp/*.c` files to compile in.
   Leave out a file the driver `#include`s to read its static state.
   `harness-inputs`, if present, names the Go package whose `TestHarnessInputs`
   test writes input files before the driver runs (see
   `internal/fixed/reference_test.go`); leave it empty when the driver needs no
   Go-written input.
2. Include `config.h` first, then the upstream headers, then `harness.h`.
3. `main(argc, argv)` gets the output directory and the input directory. Call
   `hz_manifest_open(out, "<component>", "reference/drivers/<component>.c")`,
   then for each case `hz_case_begin`, one `hz_stream_begin` per input or output
   stream, `hz_put_*` per element, and `hz_case_end(params_json)`. Close with
   `hz_manifest_close()`.
4. Integer inputs can be generated on both sides from SplitMix64 (`hz_next`
   here, `testutil.SplitMix64` in Go). Anything that needs floating-point maths
   to generate is written by the Go test, so that only one libm is involved.
5. Run `reference/fixtures.sh <component>` and commit
   `testdata/speexdsp/<component>/`. Keep within the size limits of
   requirements §4.2: commit short prefixes and let the chunk hashes cover the
   rest.

The manifest format and the Go loader are described in `docs/contracts.md`
("Fixtures").

## Upstream patches

None are applied; the sources are used exactly as tagged. The upstream tree
also ships `regression-fixes/1-resampler_unsigned_fix.patch`, which targets the
older `libspeex/` layout; its change is already present in
`libspeexdsp/resample.c:673-676` at this commit.
