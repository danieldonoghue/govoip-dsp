# Testing

How govoip-dsp is tested, where the reference data comes from, and what the
reference harness has found. Each component owns its section; the general
sections belong to WP0.

## Strategy

1. **Bit-exact components** are compared with SpeexDSP 1.2.1 (FIXED_POINT)
   through fixtures that the C reference harness produces. A mismatch is a
   bug until it is root-caused (requirements §4.4).
2. **Behavioural components** (`buffer`, `agc`) are tested against
   pre-registered criteria in `docs/acceptance/`.
3. **Every component** also has unit, boundary, reset, chunking,
   allocation, and (for `float32` entry points) non-finite tests
   (requirements §8), plus sanity signal tests reported in human terms.
4. **Claims are tested so they can fail.** Mutation checks below record
   whether a deliberately broken version is caught.

`go test` never needs a C compiler or the network. Commands:

```sh
make check           # gofmt, vet, CGO_ENABLED=0 tests, purego tests, deps guard, staticcheck
make race            # go test -race ./...
go test -short ./... # shorter reference runs; see "Race detector and short mode"
make fixtures        # regenerate fixtures (Docker or host clang, network once)
make fixtures-host   # rebuild with the host clang and compare with the committed hashes
make speech          # download LibriSpeech dev-clean into .cache/speech/
```

CI runs `make speech` on one job, `test (ubuntu-24.04, Go 1.27.x)`, and
caches the converted files under a key made from the hash of
`internal/testutil/fetch-speech.sh`. The speech tests (for example the AGC's
P4 and G1 to G4) run there on every CI run that changes code and skip in the
other jobs. A cache miss downloads about 338 MB from OpenSLR, which happens
when the script changes or when GitHub has evicted the cache after 7 days
without use.

## Reference parity summary

Which configurations of each bit-exact component match the reference, and
any root-caused exceptions. `buffer` and `agc` are own designs, tested
against behavioural criteria (Strategy, item 2). The ADRs cited are in
[decisions/](decisions/README.md).

- **WP0 `internal/fixed`:** bit-exact with the container harness (clang 19,
  linux/arm64) on all 69 cases of `testdata/speexdsp/fixed`, and the Apple
  clang 21 host build gives identical hashes. Excluded inputs, where C is
  undefined, are listed in `docs/acceptance/fixed.md`. No exceptions.
- **WP3 `internal/fft`:** bit-exact with the container harness (clang 19,
  linux/arm64) on all 79 cases of `testdata/speexdsp/fft`: 12 signals at
  each MVP size (160, 320, 640, 960, 1280, 1920), the twiddle tables, and a
  sweep over all 427 supported sizes. The Apple clang 21 host build gives
  identical hashes. No exceptions.
- **WP1 `resample`:** bit-exact with the container harness (clang 19,
  linux/arm64) on all 816 cases of `testdata/speexdsp/resample`: every
  quality over every pairing of 8, 16, 24, 32, 44.1, 48 and 96 kHz and 10
  fractional ratios, with all 652 sinc tables equal entry for entry
  (Go `math.Sin` against glibc and macOS libm); stereo, `float32`,
  strided, mixed and event cases; constructor failures. The Apple clang 21
  host build gives identical hashes. No exceptions. Go-only deviations
  where C is undefined or stalls are listed in
  `docs/acceptance/resample.md`.
- **WP5 `preprocess`:** bit-exact with the container harness (clang 19,
  linux/arm64) on all 310 cases of `testdata/speexdsp/preprocess` (rates 8,
  16, 32 and 48 kHz at 10 and 20 ms; denoise and VAD on and off; suppress
  levels and VAD thresholds; edge signals; `EstimateUpdate`; PSD getters;
  runs of 60 s, 120 s and 210 s; echo residuals from the C `mdf.c`; nine
  other rate and frame-size pairs; window and filterbank tables and a rate
  sweep; settings) and the case of `testdata/speexdsp/preprocess_zero`.
  The Apple clang 21 host build gives identical hashes. No exceptions. The
  one reachable zero divisor matches the canonical arm64 build (ADR
  `wp5-zero-divisor`).
- **WP6 `echo`:** bit-exact with the container harness (clang 19,
  linux/arm64) on all 395 cases of `testdata/speexdsp/echo`: 16 scenarios
  at 8, 16, 32 and 48 kHz, 10 and 20 ms frames and 64, 128 and 256 ms
  filters, eight 60 s and two 10-minute runs, and the pseudo-float probe.
  The Apple clang 21 host build gives identical hashes. Supported at 8 and
  16 kHz only (ADR `wp6-rates-and-shifts`). One deviation that no fixture
  reaches: `FLOAT_MUL32` shift counts outside [-31, 31] saturate (same
  ADR).

## Reference harness

`reference/` builds one C driver per component against the pinned SpeexDSP
sources with the checked-in `config.h` (`FIXED_POINT`, `USE_KISS_FFT`; no
SIMD, `RESAMPLE_FULL_SINC_TABLE` or `FIXED_DEBUG`). See `reference/README.md`
for the workflow and `docs/contracts.md` section 10 for the fixture format.

| Build | Flags | Purpose |
|---|---|---|
| Plain | `-O2 -fwrapv -ffp-contract=off` | Writes the fixtures |
| Sanitizer | `-O2 -ffp-contract=off -fsanitize=undefined` | Reports undefined behaviour; its outputs must equal the plain build's |

The sanitizer build leaves out `-fwrapv`. Measured with clang 19.1.7 in the
container: with `-fwrapv`, `-fsanitize=undefined` reports neither signed
overflow nor a left shift of a negative value (both become defined), so a
sanitizer build with `-fwrapv` would hide exactly the places where the C
result depends on the compiler.

### Toolchains

| Mode | Toolchain | Used for |
|---|---|---|
| `container` (canonical) | Debian 13 `debian:trixie-slim@sha256:a66b19aa…6323a` (linux/arm64), `clang-19=1:19.1.7-3+b1` | Committed fixtures |
| `host` | macOS 27.0.1, Apple clang 21.0.0 (clang-2100.3.34.2), arm64 | Cross-check |

Each manifest's `build` block records the mode, image, compiler, flags, OS,
architecture and the SHA-256 of `config.h`.

## Fixture provenance and cross-checks

### `fixed` (WP0)

- Generated by `reference/drivers/fixed.c` in the container (`make fixtures`,
  2026-10-26). 69 cases, 2.3 MB committed.
- Host cross-check (`make fixtures-host`): the Apple clang build produces
  identical cases sections (every chunk hash and committed file hash) to the
  committed manifest.
- Sanitizer builds (container and host) produce identical outputs to the
  plain build, with the same 45 reports (listed below).
- Go side: `go test ./internal/fixed` passes on darwin/arm64 (native), and
  the linux/amd64 test binary passes the full reference comparison under
  Docker Desktop's amd64 emulation. Emulated is not native; CI's amd64
  runners are the native check.

### `fft` (WP3)

- Generated by `reference/drivers/fft.c` in the container
  (`reference/fixtures.sh fft`, 2026-10-26). 79 cases (72 signal cases and 6
  twiddle cases at the MVP sizes, and the sweep over all 427 supported
  sizes), 0.8 MB committed in 367 files.
- Host cross-check (`HARNESS_MODE=host reference/fixtures.sh --check fft`):
  the Apple clang build gives identical cases sections to the committed
  manifest.
- Sanitizer builds (container and host) give identical outputs to the plain
  build, with 12 reports each (listed below).
- Go side: `go test ./internal/fft` passes on darwin/arm64 (native), and the
  linux/arm64 and linux/amd64 test binaries pass in Docker
  (`debian:trixie-slim`; amd64 under Docker Desktop emulation, which is not
  native).

### `resample` (WP1)

- Generated by `reference/drivers/resample.c` in the container
  (`reference/fixtures.sh resample`, 2026-10-26): 816 cases plus the
  `signal` case, about 1.0 MB committed (manifest 0.87 MB, 22 prefix
  files from 8 cases).
- Host cross-check
  (`HARNESS_MODE=host reference/fixtures.sh --check resample`): the Apple clang
  build, with macOS libm, gives identical cases sections, including all 652 sinc
  tables.
- Sanitizer builds (container and host) give identical outputs to the
  plain build, with 2 reports (below).
- Go side: `TestReferenceCases` replays every case on darwin/arm64
  (native): the state after construction, every table entry, every call's
  accounting and every output sample. It runs in full under `-short` and
  `-race` too (about 2.5 s, and 41 s under `-race`).
  `TestReferenceCoverage` checks that the fixtures hold the pre-registered
  matrix, and the replay fails if a listed event is not applied.

### `preprocess` (WP5)

- Generated by `reference/drivers/preprocess.c` in the container
  (`reference/fixtures.sh preprocess`, 2026-10-26). 310 cases, 3.7 MB
  committed in 976 files (the size is the sum of file sizes; with 4 KB
  blocks it occupies about 7 MB on disk). The case table is
  `preprocess/cases_test.go`; `TestHarnessInputs` writes it with the inputs.
- `reference/drivers/preprocess_zero.c` writes
  `testdata/speexdsp/preprocess_zero` (1 case, 0.1 MB in 6 files): a
  reachable zero divisor, with a stand-in `speex_echo_get_residual` that
  returns a crafted residual. Its host cross-check also matches.
- The driver `#include`s the unmodified `preprocess.c`, to read
  `st->window`, `st->bank` and `st->speech_prob`, and compiles `mdf.c` for
  the echo cases.
- Host cross-check
  (`HARNESS_MODE=host sh reference/fixtures.sh --check preprocess`): the Apple
  clang build gives identical cases sections to the committed manifest.
- Sanitizer builds (container and host) give identical outputs to the plain
  build, with 51 reports each (listed below).
- Go side: `go test ./preprocess` passes on darwin/arm64 (native), and the
  linux/arm64 and linux/amd64 test binaries pass in Docker
  (`debian:trixie-slim`; amd64 under Docker Desktop emulation, which is not
  native). The LibriSpeech part of the sanity test skipped in Docker.
- The C echo canceller reset itself ("acting funny") in two of the eight
  echo groups during warm-up. This is upstream behaviour on these inputs;
  the recorded frames follow it.

### `echo` (WP6)

- Generated by `reference/drivers/echo.c` in the container
  (`reference/fixtures.sh echo`, 2026-10-26). 395 cases: the pseudo-float
  probe, 384 matrix cases (16 scenarios at 8, 16, 32 and 48 kHz, 10 and 20
  ms frames, 64, 128 and 256 ms filters), eight 60 s runs and two
  10-minute runs. 4.5 MB committed in 788 raw files plus the manifest; no
  file above 1 MB. Each case commits 3200 output samples and two frames of
  residual spectra; every stream (inputs, op stream, output, residual,
  impulse response) is covered by chunk hashes.
- The case list and the inputs come from the Go test
  (`harness-inputs: ./echo`, `TestHarnessInputs` in
  `echo/reference_test.go`): it writes
  `cases.txt`, one input pair per scenario and rate, and an op stream per
  case (0 cancel, 1 playback, 2 capture, 3 reset, 4 set rate). The driver
  interprets them, so the case table exists only in Go. The inputs take
  about 200 MB in `reference/build/in/echo/`, which is git-ignored.
- Host cross-check
  (`HARNESS_MODE=host sh reference/fixtures.sh --check echo`): the Apple
  clang 21 build gives identical cases sections to the committed
  manifest.
- Sanitizer builds (container and host) give identical outputs to the
  plain build, with 67 reports each.
- Go side: `go test ./echo` passes on darwin/arm64 against the container
  fixtures (181 to 255 s across runs).

**State trace.** The driver writes a per-call trace of every
`SpeexEchoState` variable (one FNV-1a hash per variable after every op)
and, for one op, a full dump. Build the fixtures once, then:

```sh
mkdir -p reference/build/trace/in reference/build/trace/out
grep '^<case> ' reference/build/in/echo/cases.txt > reference/build/trace/in/cases.txt
# link <base>.mic.raw, <base>.far.raw and <case>.ops.raw into trace/in
docker run --rm --platform linux/arm64 --user "$(id -u):$(id -g)" -v "$PWD:/src" -w /src \
    -e HARNESS_MODE=container -e HARNESS_IMAGE=trace \
    -e HARNESS_ECHO_TRACE=reference/build/trace -e HARNESS_ECHO_DUMP_OP=<k> \
    govoip-dsp-harness reference/build/container/bin/echo reference/build/trace/out reference/build/trace/in
GOVOIP_ECHO_TRACE=$PWD/reference/build/trace GOVOIP_ECHO_DUMP_OP=<k> \
    go test ./echo -run 'TestReferenceEcho/<case>$' -v
```

The test reports the first op after which a variable differs, for example
`first difference after op 14 in Eh (161 values)`, and with the dump the
first differing element at op `k`. Checked on 2026-10-26 with
`farend_16k_10ms_128ms`: all 47 variables equal at op 500 on the port, and
a mutation of the `Eh` update was reported at op 14 in `Eh`.

### `integration` (WP7)

- Generated by `reference/drivers/integration.c` in the container
  (`reference/fixtures.sh integration`, 2026-10-27). 12 cases: 8 and 16 kHz with
  10 and 20 ms frames in `Cancel` mode and in strictly alternating `Playback`
  and `Capture` mode, 48 kHz resampled to 16 kHz (quality 3) with 10 and 20 ms
  frames, and two 10-minute runs. 122 KB committed in 60 raw files plus the
  manifest (the sum of file sizes). Each case commits 4 frames of echo output,
  preprocess output and residual spectra and 200 VAD and probability values;
  chunk hashes cover every stream, including the inputs.
- The driver links `resample.c`, `mdf.c`, `preprocess.c` and
  `filterbank.c` unmodified and attaches the echo state with
  `SPEEX_PREPROCESS_SET_ECHO_STATE`, so the C preprocessor fetches the
  residual echo itself. It calls `speex_echo_get_residual` once more after
  each frame only to record the spectrum. The case table and the inputs
  come from `integration/reference_test.go`
  (`harness-inputs: ./integration`).
- Host cross-check
  (`HARNESS_MODE=host sh reference/fixtures.sh --check integration`): the
  Apple clang 21 build gives identical cases sections to the committed
  manifest.
- Sanitizer builds (container and host) give identical outputs to the
  plain build. Each log holds 38 report lines, at the sites listed below.
- Go side: `go test ./integration -run TestReferencePairing` passes on
  darwin/arm64 (22 s). This is the first test of the real pairing of
  `docs/contracts.md` section 12: `echo.Canceller.Residual` feeding
  `preprocess.Preprocessor.RunEcho`, against C where the preprocessor
  reads the canceller directly. No exceptions.

## Undefined behaviour found by the sanitizer

### `fixed` (WP0)

Every report below is inside a tested domain. In each case the plain build
(`-fwrapv`), the sanitizer build (no `-fwrapv`) and Go agree, because clang
generates two's-complement wrapping and shifting for these expressions on
arm64. Another compiler or optimisation level could differ.

| Upstream site | Report | Reached through |
|---|---|---|
| `math_approx.h:147` | left shift of negative value (`k<<1`, k = -6) | `spx_sqrt` for small inputs; every `FLOAT_SQRT` |
| `math_approx.h:149` | left shift of negative value (`VSHR32(rt, 7-k)`) | `spx_sqrt` |
| `math_approx.h:259` | left shift of negative value (`SHL16(integer,11)`) | `spx_exp2`, `spx_exp` for negative inputs |
| `pseudofloat.h:71`, `:78` | negation of -2^31 | `PSEUDOFLOAT(-2^31)` |
| `pseudofloat.h:124`, `:159`, `:200` | left shift of negative mantissa in normalisation | `FLOAT_ADD`, `FLOAT_SUB`, `FLOAT_MULT` |
| `pseudofloat.h:230`, `:238` | left shift of negative mantissa | `FLOAT_EXTRACT16`, `FLOAT_EXTRACT32` with e ≥ 0 |
| `pseudofloat.h:243` | signed overflow in `MULT16_32_Q15`; left shift of negative | `FLOAT_MUL32` |
| `pseudofloat.h:254`-`257` | negation of -2^31 in `ABS32`; left shift of negative | `FLOAT_MUL32U` |
| `pseudofloat.h:272`-`273`, `:300`-`301` | negation of -2^31; left shift of negative | `FLOAT_DIV32_FLOAT`, `FLOAT_DIV32` |
| `pseudofloat.h:331` | left shift of negative numerator | `FLOAT_DIVU` |
| `pseudofloat.h:341`, `:346` | left shift of negative mantissa | `FLOAT_SQRT` |
| `fixed_generic.h` macros, in the driver's call sites | signed overflow in `ADD32`, `SUB32`, `MAC16_16`, `MULT16_32_Q15`, `MULT16_32_P15`, `MAC16_32_Q15`, `PSHR32`, `PDIV32`, `PDIV32_16`; left shift of negative in `SHL16`, `SHL32`, `VSHR32`; negation of -2^31 in `ABS32`, `NEG32` | Edge inputs at the int32 limits |

The full log is regenerated by every `make fixtures` run in
`reference/build/<mode>/ubsan/fixed.log`.

### `fft` (WP3)

Every report is a left shift of a negative value; there is no signed
overflow report. The plain build, the sanitizer build and Go agree, because
clang shifts two's-complement values on arm64.

| Upstream site | Expression | Reached through |
|---|---|---|
| `kiss_fft.c:486` | `SHL32(phase,17)` with a negative phase | forward twiddles in `kiss_fft_alloc` |
| `kiss_fftr.c:67` | `SHL32(phase,16)` with a negative phase | forward super twiddles in `kiss_fftr_alloc` |
| `kiss_fft.c:57`-`60` | `SHL32(EXTEND32(Fout->r), 14)` and the other three | forward `kf_bfly2` |
| `kiss_fftr.c:240`-`241` | `SHL32(ADD32(...), 13)`, `SHL32(SUB32(...), 13)` | `kiss_fftr2` |
| `fftwrap.c:59` | `max_val <<= 1` with `max_val` = -32768 | `maximize_range` when the peak wraps |
| `fftwrap.c:64` | `SHL16(in[i], shift)` | `maximize_range` |

The two allocation sites appear twice in the log (12 lines, 10 sites).

### `resample` (WP1)

| Upstream site | Report | Reached through |
|---|---|---|
| `resample.c:456` | signed overflow in `PDIV32`: `-2^31 + -1` | `den_rate` at or above 2^31 (1000000007:3000000019), where `SHL32` of the remainder gives -2^31 and the `int16` half of `den_rate` is -1 |
| `resample.c:980` | variable-length array of size 0 | `speex_resampler_process_float` with an output length of 0 |

In the first, the plain build, the sanitizer build and Go all wrap. The
second reads nothing from the array.

### `preprocess` (WP5)

The log lists each site once. The plain build, the sanitizer build and Go
agree on every case.

| Upstream site | Report | Reached through |
|---|---|---|
| `preprocess.c:778` | `SHL32(ps,NOISE_SHIFT)` overflows | noise update on loud bins |
| `filterbank.c:144` | signed overflow in a band sum | `filterbank_compute_bank32` on large noise or residual estimates |
| `preprocess.c:139` | `SHL32(a,15)` overflows in `DIV32_16_Q15` | gain and gain-floor divisions |
| `preprocess.c:643` | `SHL16` of a negative sample | frame normalisation |
| `preprocess.c:847`, `:888` | `SHL32` of a negative `post` | `theta` |
| `preprocess.c:1147`, `:1153`, `:1159` | negation of -2^31 in `-ABS` | the suppress setters with `math.MinInt32` |
| `math_approx.h:147`, `:259` | left shift of negative | `spx_sqrt`, `spx_exp` (as in `fixed`) |
| `kiss_fft.c`, `kiss_fftr.c`, `fftwrap.c` | left shift of negative | as in `fft` |
| `mdf.c`, `pseudofloat.h` | signed overflow, shifts, negation | the C echo canceller of the echo cases (WP6's component) |

No report is a division by zero. The `preprocess_zero` driver adds one,
on purpose: `preprocess.c:140: division by zero` (24 reports in its log),
with outputs identical to the plain build.

### `echo` (WP6)

The probe case runs first and deliberately shifts out of range, so in the
committed run its sites take the deduplicated reports. An earlier host run
without the probe (same fixtures otherwise) gives the reports from
`mdf.c` itself: signed overflow and left shifts of negative values at
`mdf.c:200-206` (DC notch), `:220-222` (`mdf_inner_prod`), `:357`
(`mdf_adjust_prop`), `:818` (weight update), `:840`, `:848`, `:853`
(AUMDF), `:896-897`, `:911`, `:925`, `:933`, `:943` (foreground logic),
`:1044` (second `Sxx` sum), `:1101` (`RER`), `:468` (window), and in
`pseudofloat.h`, `math_approx.h`, `kiss_fft*.c` and `fftwrap.c` as listed
for `fixed` and `fft`. Go wraps and shifts the same way. The one report
that is not two's-complement arithmetic:

| Upstream site | Report | Reached through | Port |
|---|---|---|---|
| `pseudofloat.h:236` | shift exponent 32 too large; `1 << 31` | `FLOAT_EXTRACT32(bound)`, `mdf.c:1108`, when `Sey` is near 0 and `Syy` is large | `FloatExtract32Masked`: the compiled reference shifts modulo 32 (`docs/algorithms.md`, "Shifts outside [0, 31]") |

In the probe, `FLOAT_DIV32_FLOAT` with a divisor mantissa of -1 also
divides -2^31 by -1, which arm64 `SDIV` and Go both give as -2^31.

### `integration` (WP7)

Every report is at a site already listed above for `fixed`, `fft`,
`preprocess` or `echo`: left shifts of negative values in `mdf.c`,
`preprocess.c`, `pseudofloat.h`, `math_approx.h`, `kiss_fft.c`,
`kiss_fftr.c` and `fftwrap.c`, the signed overflow of the weight update at
`mdf.c:818`, and the `FLOAT_EXTRACT32` shift exponents 34 and 35 at
`pseudofloat.h:236` (the masked shift of the `echo` table). `resample.c`
reports nothing on these inputs. Outputs are identical to the plain build.

## Compiler-dependent constants

### `fixed` (WP0)

The driver evaluates every QCONST16 and QCONST32 literal pair that appears
in the upstream text. One of them, `QCONST16(4.f,13)` at `preprocess.c:260`,
is out of `int16` range (32768.5 before the cast), but it is in the
`#else` branch of `#ifdef FIXED_POINT`, so the reference build never
compiles it; the fixed-point branch uses `32767` at `preprocess.c:258`. In
the driver's static initialiser both harness compilers fold it to 32767. At
run time the same out-of-range cast is undefined and varies (measured in the
container: clang 19 gives 40000 at `-O2` and -25536 at `-O0` for
`(short)40000.5`). `QConst16` saturates, which matches the folded value; for
the reference build this is a Go-only choice, since no compiled literal is
out of range. The other 42 QCONST16 and all 6 QCONST32 pairs are in range
and match exactly.

## libm findings

None yet. `internal/fixed` uses no libm. Components with float
initialisation record theirs here.

### `resample` (WP1)

The sinc tables use Go's `math.Sin` in place of libm `sin`. All 652 tables
in the fixtures (every quality over every rate pair and the fractional
ratios) equal the glibc 2.41 (container) and macOS libm (host) tables entry
for entry, and the two C builds agree with each other. No libm-induced
difference was found, so no table is embedded. A difference would show as
a `table` stream mismatch.

### `preprocess` (WP5)

None. The fixed-point `preprocess.c` and `filterbank.c` have no floating
point and call no libm function: compiled with the reference flags (host
Apple clang), neither object references a libm symbol, and the only
instructions on floating-point registers are `fmov` moves and two
`fneg.4s` that build integer sign masks (`docs/algorithms.md`). Their
tables are still compared with the harness.

## Mutation checks

### `fixed` (WP0)

Run on a scratch copy with `go test -short ./internal/fixed` (first chunk of
each stream only), so these are a lower bound on what the full test catches.

| Mutation | Result |
|---|---|
| `Mult16x16P15` rounding constant 16384 → 16383 | caught (`cos_pi_2`) |
| `Sqrt` C3 4204 → 4215 (the commented-out upstream value) | caught (`sqrt`) |
| `PDiv32` rounding term without the `int16` cast | caught (`pdiv32`) |
| `Sub16` truncating to `int16` | caught (`cos_pi_2`) |
| `cosPi2` truncating before `MIN16` | caught (`cos_pi_2`) |
| pseudo-float alignment shift cap 15 → 14 | caught (`float_gt`) |
| `Abs16` wrapping in `int16` | caught (`abs16`) |
| `FloatSub` returning `-b` for a zero `a` | caught (`float_sub`) |
| `Word2IntFloat32` lower bound -32767 → -32768 | caught (`word2int_f32`) |
| `FloatDivU` loop `>=` → `>` | caught (`float_divu`) |
| `Exp` upper bound 21290 → 21291 | caught (`exp`) |
| `QConst16` wrapping instead of saturating | caught (`qconst16`) |
| `QConst16` rounding offset 0.5 → 0.25 | caught (`qconst16`) |
| `FloatExtract32` rounding term `1<<(s-1)` → `1<<s` | caught (`float_extract32`) |
| `Sqrt` final shift `7-k` → `6-k` | caught (`sqrt`) |
| `TestInlining` with `Sqrt` (which does not inline) added to the list | caught |
| `Word2Int` (integer) bounds -32767/32766 → -32768/32767 | not caught: equivalent for integers |
| `PShr32` rewritten without changing its value | not caught: equivalent, as intended |
| `FloatSqrt` exponent `e>>1` → `e/2` | not caught: equivalent, e is even there |
| `CosNorm` fold `x > 1<<16` → `x >= 1<<16` | not caught: equivalent, x = 2^16 folds to itself |
| `Atan` saturation `e >= 29` → `e >= 30` | not caught: equivalent, e = 29 also gives 25736 |

The `testutil` fixture tests check that the stream checker reports a
prefix mismatch with its index, a chunk mismatch with its range, short and
long streams, a wrong committed file hash and a wrong element type.

### `fft` (WP3)

Run on the working tree with the full `go test ./internal/fft`.

| Mutation | Result |
|---|---|
| `kf_bfly4` forward: sign of `s4.i` in `Fout[m].r` | caught |
| `kf_bfly4` forward: `PSHR16` → plain shift | caught |
| `kf_bfly4` inverse: sign of `s4.i` in `Fout[m].r` | caught |
| `kf_bfly2` forward: `SHR32(..., 1)` → `SHR32(..., 2)` | caught |
| `kf_bfly2` inverse: `Fout - t` → `t - Fout` | caught |
| `sround` without rounding | caught |
| `C_MUL4` shift 17 → 16 | caught |
| `C_FIXDIV`: `SAMP_MAX` 32767 → 32768 | caught |
| `kf_bfly3`: `HALF_OF` dropped | caught |
| `kf_bfly3`: `epi3.i` → `epi3.r` | caught |
| `kf_bfly3`: `C_FIXDIV` by 2 instead of 3 | caught |
| `kf_bfly5`: `ya`/`yb` swapped in `scratch[11]` | caught |
| `kf_bfly5`: sign in `scratch[12].i` | caught |
| `kf_bfly_generic`: no `C_FIXDIV` | caught |
| `kf_bfly_generic`: no twiddle-index wrap | caught |
| `kf_bfly_generic`: twiddle index off by one stride | caught |
| `kf_shuffle`: recursion stride `fstride*p` → `fstride*m` | caught |
| `kf_factor`: start with radix 2 instead of 4 | caught |
| twiddle phase `SHL32(phase,17)` → 16 | caught |
| `kf_cexp2`: `phase-32768` → `phase+32768` | caught |
| super twiddles without the `nfft>>1` offset | caught |
| `kiss_fftr2`: `PSHR32` → `SHR32` in `f2k.i` | caught |
| `kiss_fftr2`: `tdc` without `C_FIXDIV` | caught |
| `kiss_fftri2`: no negation of `tmpbuf[ncfft-k].i` | caught |
| `kiss_fftri2`: sign of `tmpbuf[0].i` | caught |
| `maximize_range`: no wrap of `-in[i]` | caught |
| `maximize_range`: bound 32000 → 32767 | caught (signals 10 and 11) |
| `maximize_range`: `<=` → `<` | caught (signals 10 and 11) |
| `maximize_range`: shift loop without the wrap to 0 | caught (signal 12) |
| no `renorm_range` of the input | caught |
| `renorm_range` without rounding | caught |
| `Supported`: radix limit 17 → 19 | caught |
| `kiss_fftr2`: the two store pairs swapped | not caught: equivalent, the pairs differ only at k = ncfft/2, where the super twiddle is (-32767, 0) and both pairs store the same values |

The three rows marked with signals 10 to 12 were not caught by the first
nine signals; the inputs were added for them (`docs/acceptance/fft.md`,
"Changes").

### `buffer` (WP2)

Run on a scratch copy of the package on the Mac (darwin/arm64, GOMAXPROCS 10),
against the final code. The four ordering mutations ran with
`go test -race -run 'TestStressSPSC|TestResetSPSC'` and again without `-race`;
the source mutations ran with `-run TestWaitFreeSource`; the rest ran the whole
package (with `-race` for the SPSC index mutations).

| Mutation | Result |
|---|---|
| SPSC producer stores `w` before copying the samples | caught (data race; without `-race`: sequence check, stale sample) |
| SPSC consumer stores `r` before copying the samples out | caught (data race; without `-race`: sequence check) |
| SPSC indices as plain `uint32` loads and stores | caught (data race; without `-race`: sequence check, on arm64) |
| SPSC producer free count one too large | caught (data race; without `-race`: sequence check) |
| SPSC indices modulo Cap instead of 2·Cap (full reads as empty) | caught (`TestModelOps`, `TestFullAndEmpty`) |
| SPSC `Consume` releases one slot more than it returns | caught (`TestModelOps`, `TestStressSPSC`) |
| SPSC `Reset` sets `w` to `r` instead of zeroing both | caught (`TestResetRing`, `TestResetSPSC`) |
| SPSC `Commit` clipped to `Free` instead of the write view | caught (`TestModelOps`, `TestCommitClipsToView`) |
| SPSC `Commit` does not shrink the remaining view | caught (`TestModelOps`, `TestClipping`) |
| a `for` retry loop added to SPSC `Write` | caught (`TestWaitFreeSource`) |
| a `goto` retry loop and `atomic.AddUint32` added to SPSC `Write` | caught (`TestWaitFreeSource`) |
| `CompareAndSwap` instead of `Store` in SPSC `Consume` | caught (`TestWaitFreeSource`) |
| SPSC `count` without the clamp to `Cap` | not caught: equivalent for correct use, where the count never exceeds `Cap`; the clamp only guards misuse |
| `Ring` under `Overwrite` discards one sample too few | caught (`TestModelOps`, `TestOverwriteDiscardsOldest`) |
| `Ring` under `Reject` keeps the last samples of the input | caught (`TestModelOps`, `TestRejectKeepsStoredSamples`) |
| `Ring` read position wraps at `>` instead of `>=` | caught (`TestModelOps`) |
| views returned without a capacity limit | caught (`TestModelOps`, `TestAppendOnView`) |
| `Ring` under `Overwrite` keeps the first `Cap` samples of a long write | caught (`TestModelOps`, `TestOverwriteDiscardsOldest`) |
| `Ring` `Reset` keeps the read position | caught (`TestResetRing`) |
| `Ring` `Commit` clipped to `Free` instead of the write view | caught (`TestModelOps`, `TestCommitClipsToView`) |
| `Ring` `Write` does not expire the write view | caught (`TestModelOps`, `TestCommitClipsToView`) |

The ordering mutations without `-race` were caught on arm64, whose memory
model is weak; on amd64 a plain-store mutation may only be caught by the
race detector. `TestSeqCheckerDetects` checks that the sequence checker
reports a lost, a duplicated and a stale sample. The independent review ran
six further mutations of its own (a re-loaded `w` in `Read`, index stores
between the two wrap segments on each side, a write view one sample too
large, `Reset` zeroing only `w`, `count` with `>`): all caught. Its
seventh, the `goto` and `atomic.AddUint32` row above, was not caught by the
first `TestWaitFreeSource`; the test was extended and now catches it.

#### `buffer` backlog drop (WP2b)

`SPSC.Consume` is also the consumer's way to drop a backlog without reading
it, so these mutations target that path. Run on a scratch copy on the Mac,
with the tests named, under `-race` unless noted.

| Mutation | Result |
|---|---|
| ordering: `Consume` clips to `Cap` instead of the count from the `w` it loaded | caught (`TestModelOps`, `TestClipping`; `TestStressSPSC` alone: data race, and the sequence check without `-race`) |
| `Consume` releases one sample more than it returns | caught (`TestModelOps`, `TestStressSPSC`) |
| `Consume` returns one less than it released | caught (`TestModelOps`, `TestClipping`, `TestStressSPSC`) |
| `Consume`, when dropping everything, releases up to a re-loaded `w` but returns the old count (only wrong under concurrency) | caught (`TestStressSPSC` only, as expected) |
| stress consumer does not skip the checker past a drop | caught (`TestStressSPSC`) |
| stress consumer skips one sample more than it dropped | caught (`TestStressSPSC`) |
| stress keep-newest branch disabled | caught (`TestStressSPSC`, which requires a keep-newest drop) |

The drop path shares its code with the view path, so the WP2 tests already
catch most of these mutations. The stress test adds detection of the
clip-to-`Cap` mutation through its drops, and its keep-newest requirement
catches the disabled branch. The WP2 mutation table above was rerun with the
WP2b code (`spsc.go` unchanged): same results.

### `resample` (WP1)

Run with `go test ./resample -run <test> -count=1` on a scratch change,
restored after each.

| Mutation | Result |
|---|---|
| `cubic_coef` without the final `+1` | caught (`q0_8000_44100`) |
| direct kernel saturating at 32766 | caught (`q0_8000_8000`) |
| `PDIV32` replaced by `DIV32` for the fraction | caught (`q0_8000_44100`) |
| interpolated table offset off by one | caught (`q0_8000_44100`) |
| `float32` input clamped instead of `WORD2INT` | caught (`f_q0_8000_96000`) |
| `Reset` clearing every channel's history | caught (`reset_q5_48000_16000_2ch`) |
| `SkipZeros` at `filt_len/2+1` | caught (`skip_q0_48000_16000`) |
| filter growth without the `last_sample` adjustment | caught (`qchange_48000_16000_1ch`) |
| filter growth without zeroing the magic samples | caught (`qburst_44100_48000_1ch`) |
| filter shrink dropping earlier magic samples | caught (`qburst_44100_48000_1ch`, after change 1 of the criteria) |
| magic samples not moved after a partial run | caught (`qchange_44100_48000_1ch`) |
| `16*den_rate` compared without the `uint32` wrap | caught (`q0_frac_270000001_270000000`, after change 1) |
| interleaved call reporting the first channel's counts | caught (`mixed_q5_44100_48000_2ch`, after change 1) |
| `float32` path running magic samples only once per call | caught: the replay of `f_qdrop_q10_frac_1_100` hangs (after change 2) |
| `SetRateFrac` without rescaling the fractional positions | caught (`rate_q5_48000_16000_1ch`) |
| output latency without its rounding term | caught (`q0_32000_44100`) |
| zero kernel writing 1 | caught (`fail_q10_44100_48000_1ch`) |
| memory size check removed | caught (`initfail_q0_48000_48000_7000000ch`) |
| NaN input mapped to 1 | caught (`TestFloatNonFinite`) |
| `SetRateFrac` without the overflow pre-check | caught (`TestSetRateFracOverflowLeavesStateUnchanged`) |
| an allocation in `Process` | caught (`TestNoAllocsProcess`) |
| `Reset` not clearing `last_sample` | caught (`TestResetMonoEqualsNew`) |
| strided kernel writing next to its element | caught (`TestStridesLeaveOtherElements`) |
| zero kernel position without saturation (the code before review) | caught (`TestHugeRatioAfterFailedChange`, panic; and `FuzzConfigResampler`) |
| `frames` as `(n+stride-1)/stride` (the code before review) | caught (`TestHugeStrides`, hang) |
| cap comparison `>` → `>=` | caught (`TestCapBoundary`) |
| filter length wrapping to 0 accepted (the code before review) | caught (`TestFilterLengthWrap`, panic) |
| resource cap removed (`maxEntries` set to `math.MaxInt64`) | caught (`TestResourceCap`: each of the six rejection cases allocates 34 MB to 4.1 GB against a bound of 1 MiB; two `New` cases then succeed, and the other four fail through C's limits only after allocating, so only the allocation and time bounds catch those; `New` at 4000000:1, quality 10, takes about 15 to 19 s) |
| `OutLen` without the +1 | caught (`TestOutLenMatrix`, slack 0) |
| `OutLen` with +2 | caught (`TestOutLenMatrix`, slack 2) |
| `OutLen` count with floor instead of ceil | caught (`TestOutLenMatrix`) |
| `OutLen` count one higher | caught (`TestOutLenMatrix`, slack 2) |
| `OutLen` ignoring pending magic samples | caught (`TestOutLenEvents`: input left unconsumed) |
| `OutLen` without the wrap check | caught (`TestOutLenEdges`) |
| `OutLen` wrap check `>` → `>=` | caught (`TestOutLenWrapBoundary`) |
| `OutLen` from the first channel only, or the last only | caught (`TestOutLenDivergedChannels`, after a first version that did not make the channels differ enough was not caught) |
| history not shifted when a call produces nothing | caught (`TestChunkInvarianceProcess`; also `FuzzChunksResampler` from its committed seed) |
| direct table `i/den` in `float64` then `float32` | not caught: equivalent on every tested table (double rounding rarely differs) |
| `compute_func` cube in `float64` then `float32` | not caught: equivalent, the product of two `float32` is exact in `float64` |
| `.5f*N` in `float64` | not caught: equivalent while `N` < 2^24 |
| 8192-sample `float32` chunk → 4096 | not caught: equivalent, the chunking does not change the output |
| `set_rate_frac` safety net removed | not caught: unreachable, `multiply_frac` already gives a value below `den` |
| early return comparing the reduced ratio | not caught: equivalent, the extra `update_filter` changes nothing visible |
| main loop not skipped while magic samples remain (`int16`) | not caught: equivalent, magic samples remain only when the output is full |
| a consumed sample re-offered when the output is full | not caught: equivalent, the re-offered sample overwrites the same value |

### `preprocess` (WP5)

Run by a script on the working tree with the full `go test ./preprocess`,
each mutation reverted before the next.

| Mutation | Result |
|---|---|
| `toBARK`: 3355 → 3356 | caught |
| `filterbank_new`: break on `mel > max_mel` → `>=` | caught |
| `filterbank_compute_bank32`: `MULT16_32_P15` → `Q15` (left) | caught |
| `filterbank_compute_psd16`: `PSHR32` → `SHR32` | caught |
| `conj_window`: `4 - x` → `3 - x` | caught |
| `frame_shift`: 14 → 13 | caught |
| power spectrum: `PSHR32` → `SHR32` | caught |
| `min_range` 300 → 301 | caught (by `8000_80_vlong` and `8000_1_extra` only) |
| `nb_adapt` cap 20000 → 19999 | not caught: equivalent, `beta` is 983 and `min_range` 300 for both |
| `Smin`/`Stmp` reset at `nb_adapt` 1 → 0 | caught |
| `echo_noise` decay `.6` → 19660 | caught |
| `echo_noise`: `MAX32` → sum | caught |
| `tot_noise` without `reverb_estimate` | not caught: equivalent, the term is always 0 |
| `DIV32_16_Q8`: `>=` → `>` | caught |
| `DIV32_16_Q8`/`Q15`: range step 2^19 → 2^20 | caught |
| `hypergeom_gain`: `ind > 19` → `> 18` | caught |
| `qcurve`: 512 → 511 | caught |
| `compute_gain_floor`: `>` → `>=` | caught |
| Bark gain constraint: `>` → `>=` | caught |
| gain floor: `<` → `<=` | not caught: equivalent, assigns an equal value |
| band `gain2`: 256 → 255 | caught |
| `zeta`: last loop from `N-1` → `N` | not caught: equivalent, the bin values of `zeta` are never read |
| denoise off: unity gain on bins only | not caught: equivalent, the band `gain2` is recomputed before use |
| VAD: continue without `was_speech` | caught |
| `estimate_update`: `.95` → `.8` | caught |
| `estimate_update`: `outbuf` window index | caught |
| `estimate_update`: reverb decay → 0 | not caught: equivalent, the estimate is always 0 |
| `SET_PROB_*`: clamp 100 → 101 | caught |
| VAD start `>` → `>=` | caught (after the 77% and 84% cases were added) |
| VAD continue `>` → `>=` | caught (after the 77% and 84% cases were added) |
| `Div32x16OrZero(a, 0)` → 32767 | caught, by `TestReferenceZeroDivisor` against C |
| `SET_ECHO_SUPPRESS_ACTIVE`: `-ABS` → negation | caught |
| `GET_NOISE_PSD`: `PSHR32` → `SHR32` | caught |

Only the two cases that pass 10000 frames catch the `min_range` row: the
210 s run added for it (`docs/acceptance/preprocess.md`, "Changes") and the
`N` = 1 case. A `QCONST16(.89f,15)` constant changed by one fails 218
subtests, in the full run and in `-short` mode.

The independent review tried 26 further mutations of its own: 19 caught, 6
not caught and equivalent, and 1 not caught as a test gap (the VAD start
`>=`), closed by the 77% and 84% cases.

`SupportedFrameSize` (WP5b), with the full `go test ./preprocess`:

| Mutation | Result |
|---|---|
| no lower bound | caught (`math.MinInt+80`, where `2*n` wraps to 160) |
| `n >= 1` → `n >= 2` | caught |
| `n <= MaxFrameSize` → `<` | caught |
| `fft.Supported(2*n)` → `2*n+2` | caught |
| `n >= 1` → `n >= 0` | not caught: equivalent, `fft.Supported(0)` is false |
| `n <= MaxFrameSize` → `<= MaxFrameSize+1` | not caught: equivalent, `fft.Supported(4098)` is false |
| no upper bound | not caught: equivalent, for `n` above 2048 `2*n` is above 4096 or wraps negative, and `fft.Supported` rejects both |

### `agc` (WP4)

#### Design v2

Run with `go test -count=1 ./agc` per mutation on a copy of the branch at
the commit "agc: test the level bias, low-gain rates and first-frame
clamp", with the LibriSpeech files present (the unmutated run passes, P4
included), restoring `agc.go` after each.

| Mutation | Result |
|---|---|
| Limiter removed (block peak ignored) | caught (T1/T2 suite, T4, C1, C3, C4, Q4, long run, boundary configs) |
| Limiter ceiling one LSB high | caught (same tests except Q4) |
| Ramp end not bounded by the previous block's limiter gain | caught (T1/T2 suite, T4, C4, Q4, long run, boundary configs) |
| Limiter never releases | caught (C2 to C6, P3, P4, edge configs) |
| Gate removed | caught (N1, N3, P3, P4, Q2 to Q4, R3, R5 pauses, T1/T2 suite) |
| Speech flag ignored | caught (N2, `TestSpeechFlag`) |
| Speech flag not reset at the frame end | caught (`TestSpeechFlag`) |
| Innovation clamp `min(r, 2L)` removed | caught (T4, C1, C3, C4) |
| Gain rises on inactive frames | caught (R1 on N3, P3, P4, Q2 to Q4, T1/T2 suite, R5 pauses; R3) |
| Rise limit removed | caught (R1, R3, C1, C2, T4, R5 and more) |
| Fall limit removed | caught (R2 on C3 down, `TestLowGainRates`) |
| Loud run one frame shorter | caught (T4 straddling, R5 pauses and 9999 Hz, R5 at 44.1 kHz) |
| Loud run fixed at 3 frames at every rate | caught (R5 at 9999 Hz) |
| Inactive frames do not end the loud run | caught (R5 pauses) |
| Cut threshold +10 dB instead of +13 dB | not caught: P4 at −34 dBFS does not trigger it; the effect shows at louder levels, which are reported without a threshold (3.41 to 5.39 dB at −27 to −20 dBFS) |
| Immediate cut removed | caught (R4, C1) |
| Sustained-drop reset removed | caught (C2, C3, C4, edge configs) |
| Warm-up removed | caught (`TestSpeechFlag` only; P3, P4 and S1 still pass) |
| First frame not clamped | caught (`TestFirstFrameClick`) |
| Mean update `(1 − c)·E + c·x` (truncation bias) | caught (`TestLevelBias`) |
| Gain kept in Q15 instead of Q22 | caught (`TestLowGainRates`) |
| Power mean of order 2 instead of 4 | not caught: passes every registered criterion; its weakness shows at −20 dBFS (P4 6.78 dB, reported only) |
| Gain not clamped to MaxGain | caught (C6) |
| Gain not clamped to MinGain | caught (C6, T1/T2 suite) |
| `Reset` keeps the delay line | caught (`TestResetProcess`) |
| Input read after the output is written (breaks aliasing) | caught (`TestAliasingProcess`) |
| Output saturation removed | not caught: equivalent, the limiter keeps every product within the ceiling |
| Rise step without the one-unit minimum | not caught: equivalent, in Q22 the step rounds to zero only below −80 dB of gain, which no configuration allows |

Checks of the regression guards (G1 to G6) and N5, each on a copy of the
branch. G1 to G4 skip without the LibriSpeech files, so the protection
against a lower cut threshold (G3, G4) runs only where `make speech` has
run:

| Mutation | Result |
|---|---|
| Cut threshold +10 dB instead of +13 dB | caught by G3 (3.50 dB) and G4 (5.39 dB) |
| Power mean of order 2 instead of 4 | caught by G4 (6.78 dB) |
| Gate removed | caught by N5 (3 cases) |
| Gain not clamped to MaxGain | not caught by N5 (no N5 signal needs more than MaxGain); caught by C6 |
| Cut threshold +13 → +16 dB (`cutRatio` 20 → 40) | caught by G5 at all four rates |
| Cut threshold +13 → +14.8 dB (`cutRatio` 30) | caught by G5 at all four rates |
| Innovation clamp 2L → 1.5L | caught by G6 (all steps, all rates) |
| Innovation clamp 2L → 3L | caught by G5 (the +14 dB step no longer cuts) and `TestFirstFrameClick` |
| Default `LevelTime` 3 s → 4 s | caught by G6 (all steps, all rates) and `TestConstants` |
| Cut threshold +13 → +12 dB (`cutRatio` 16) | caught by G4 only (3.60 dB), so only with the LibriSpeech files present |
| Loud run one frame longer | not caught: the cut comes 10 ms later, still within R4's 50 ms |
| Default `FallRate` 40 → 20 dB/s | not caught by G5 or G6 (these steps follow the level estimate, not the fall limit); caught by `TestConstants` |

The noise-floor safeguard trial (change 7 in `docs/acceptance/agc.md`)
was reverted after it failed registered criteria, so it has no mutation
checks.

#### Design v1 (fallback)

Run with `go test -count=1 ./agc` per mutation on a copy of the branch at
the commit "agc: check the ceiling and exact gain limits in more acceptance
tests", without the LibriSpeech files (so P4 skips and the unmutated run
passes), restoring `agc.go` after each.

| Mutation | Result |
|---|---|
| Limiter removed (block peak ignored) | caught (T1/T2 suite, T4, C1, Q4, long run, boundary configs) |
| Limiter ceiling one LSB high | caught (same tests) |
| Ramp end not bounded by the previous block's limiter gain | caught (T1/T2 suite, T4, Q4, long run) |
| Limiter never releases | caught (C2, C5, C6, P3) |
| One-pole steps floored instead of rounded towards the target | caught (`TestConfigEdgeAccuracy`) |
| Mean square without fractional bits | caught (`TestRMS`) |
| Gate removed | caught (N1, P3, Q1) |
| Speech flag ignored | caught (N2, `TestSpeechFlag`) |
| Speech flag not reset at the frame end | caught (`TestSpeechFlag`) |
| Innovation clamp `min(r, 4L)` removed | caught (T4) |
| Attack and release coefficients swapped | caught (C1, C3, C4, P3) |
| Gain starts at MaxGain instead of 0 dB | caught (N1, N2, Q1, `TestLatency`, `TestSpeechFlag`) |
| Level estimate starts at the gate (gain still starts at 0 dB) | caught (`TestSpeechFlag`) |
| Gain not clamped to MaxGain | caught (C6) |
| Gain not clamped to MinGain | caught (C6, T1/T2 suite) |
| RMS result one bit low (Q12 → Q11) | caught (C1 to C6) |
| `Reset` keeps the delay line | caught (`TestResetProcess`) |
| Input read after the output is written (breaks aliasing) | caught (`TestAliasingProcess`) |
| Output saturation removed | not caught: equivalent, the limiter keeps every product within the ceiling |
| Ramp step `(b−a)/K` → `(b−a)/(K−1)` | not caught: the ramp still stays between its endpoints, and no criterion depends on its exact slope |

### `echo` (WP6)

Run on the working tree, one mutation of `echo/mdf.go` at a time, against 13
reference cases covering every path (the tests that `-run TestReference`
selects, or the full `go test ./echo` where stated). 38 mutations: 33 caught
(one of them only by the allocation test, and semantically equivalent), 5 not
caught: three boundaries that no input reaches, one equivalent mutation, and one
whose difference a later clamp hides in every frame where it occurs.

| Mutation | Result |
|---|---|
| DC notch: `den2` constant 0.7 → 0.9 | caught |
| microphone pre-emphasis clamp `> 32767` → `> 32766` | caught (by `edges`, after it was added) |
| far-end saturation hold `M+1` → `M` | caught |
| no shift of the far-end history `X` | caught |
| forward FFT of a copy of `x` instead of `x` (contract section 13, rule 4) | caught by `TestNoAllocs` only when the copy allocates; with a non-allocating copy not caught: equivalent, since pre-emphasis keeps `x` within ±32767 and `Forward` restores such an input exactly |
| foreground `PSHR32` by `WEIGHT_SHIFT-1` | caught |
| conjugate product: `-X[i+1]` without the 16-bit wrap | not caught: differs only for a spectrum value of -32768, which no input reaches |
| `mdf_adjust_prop` 0.1 → 0.99 | caught |
| AUMDF block choice `j-1` → `j` | caught |
| AUMDF scale-up shift 3 → 2 | caught |
| `Dbf` offset 10 → 11 | caught |
| foreground update test `FLOAT_GT` → `FLOAT_LT` | caught |
| backtrack constant `VAR_BACKTRACK` → `VAR1_UPDATE` | caught |
| smooth transition: windows swapped | caught |
| output truncates instead of `WORD2INT` | caught |
| microphone saturation test ±32000 → ±32001 | caught (by `edges`) |
| `screwed_up += 50` → `+= 49` | caught |
| `screwed_up` test `Sff/4` → `Sff/2` | caught (full `go test ./echo`) |
| `See` floor 100 → 101 | caught |
| no second `Sxx` sum (`mdf.c:1044`) | caught |
| `MIN_LEAK` mantissa 20972 → 20971 | caught |
| leak estimate test `> 16383` → `>= 16383` | not caught: needs a leak estimate of exactly 16383, which no input reaches |
| `bound` extract unmasked instead of masked | not caught: the 3 frames where the two differ are hidden by the clamp to `See/2` (`docs/algorithms.md`); the masked choice rests on the disassembly |
| adapted test 0.03 → 0.15 | caught |
| adaptation mask clamp `e/2` → `e/4` | caught |
| `power_1` shift `WEIGHT_SHIFT+16` → `+15` | caught |
| temporary-rate threshold `N*1000` → `N*999` | caught (by `edges`) |
| `last_y = in - out` → `out - in` | caught |
| residual `leak2` test `<= 16383` → `< 16383` | not caught: same boundary as the leak estimate |
| capture: partial queue not emptied | not caught: equivalent, the queue position is always a whole number of frames |
| playback discard `>` → `>=` | caught |
| auto-fill `<=` → `<` | caught |
| `Reset` keeps `Eh` | caught |
| `Reset` clears all of `last_y` | caught |
| `Reset` clears `prop` | caught |
| notch radius step `< 12000` → `<= 12000` | caught (by `edges`) |
| impulse response shift 6 → 5 | caught |
| `Pyy` floor against 0 instead of 1 | caught |

The four rows marked `edges` were not caught before that scenario existed;
it was added for the five boundaries behind them, the notch step counting
twice (`docs/acceptance/echo.md`, "Changes").

### `integration` (WP7)

One mutation of the test's Go chain, or of the recipe, at a time.

| Mutation | Result |
|---|---|
| `Run` instead of `RunEcho` (no residual echo) | caught: `prob` prefix from frame 152, and the `out` and `vad` hashes |
| residual of the previous frame (`Residual` before `Cancel`) | caught: `residual`, `out` and `vad` hashes |
| one output sample flipped at frame 20000 of a 10-minute run | caught by the chunk hash in the full run; not caught under `-short`, which compares only the prefixes of the 10-minute runs |
| one output sample flipped at frame 10, under `-short` | caught by the three cases compared in full; before they were added, not caught |
| one VAD decision flipped at frame 150, under `-short` | caught |
| resampler quality 4 instead of 3 in the resampled cases | caught |
| recipe: one far frame skipped per received frame | caught by `TestRecipeThroughSPSC` |
| recipe: `onPacket` stores one sample fewer | caught by `TestRecipeThroughSPSC` (short output after its 1 minute deadline) and `TestDrainDropsBacklog` |
| recipe: backlog drop without skipping the dropped frames' far frames | caught by `TestDrainDropsBacklog` |
| recipe: backlog drop keeps the oldest frame | caught by `TestDrainDropsBacklog` |
| recipe: `deliver` allocates | caught by `TestNoAllocsRecipe` |
| recipe: AGC `speech` always true instead of the VAD decision | not caught by `TestRecipeThroughSPSC`: over its 10 s the VAD is false only on frames below the AGC gate, so the output is the same |
| `docs/usage.md`: one changed code line | caught by `TestUsageCodeIsTested` |
| recipe B: `drain` queues every waiting sent frame before a capture (breaks one for one) | caught by `TestBufferedTwoGoroutines` (received frame 3 got sent frame 2) |
| recipe B: `drain` queues nothing when no sent frame has arrived | caught by `TestBufferedIdleSender` (U7 -0.0 and 0.2 dB) |
| recipe B: `drain` queues the previous frame instead of silence | caught by `TestBufferedIdleSender` (U7 -8.2 and -9.1 dB) |
| recipe B: `deliver` allocates | caught by `TestNoAllocsBuffered` |
| lag test: reference two frames late instead of one | caught by `TestBufferedLagIsOneFrame` |
| U11: filter 128 ms instead of 256 ms | caught by `TestBufferedLongDelay` (200 ms delay: -0.2 and 2.1 dB) |
| recipe A: `resampleInto` ignores the queue result | caught by `TestQueueFullIsReported` |
| recipe A: backlog drop without skipping far frames, two goroutines | caught by `TestRecipeDropsUnderRace` (343 mismatches) |

## Whole-chain measurements (`integration`, WP7)

From `go test ./integration -run 'TestPipeline|TestNoise' -v`
(darwin/arm64, 2026-10-27). Definitions and thresholds are in
`docs/acceptance/integration.md`: B1 is the echo stage over 8 to 14 s,
B2 the echo through the whole chain over 8 to 14 s and 36 to 40 s, B3
the near-end output level (active 20 ms frames, 18 to 24 s), B4 the
double-talk level against B3, B5 the output peak against the ceiling
29205.

| Signal, rate, frame | B1 (dB) | B2 (dB) | B3 (dBFS) | B4 (dB) | B5 peak |
|---|---|---|---|---|---|
| synthetic, 8 kHz, 10 ms | 27.2 | 27.9, 31.0 | -26.1 | -1.5 | 13592 |
| synthetic, 8 kHz, 20 ms | 23.9 | 25.1, 31.8 | -26.7 | -0.9 | 13257 |
| synthetic, 16 kHz, 10 ms | 20.2 | 20.4, 22.5 | -25.4 | -1.1 | 29205 |
| synthetic, 16 kHz, 20 ms | 20.3 | 24.2, 22.5 | -26.7 | -0.1 | 29205 |
| synthetic, 48 kHz resampled to 16 kHz, 20 ms | **15.9** | 27.4, 25.9 | **-33.1** | +2.4 | 27918 |
| LibriSpeech, 8 kHz, 20 ms | 25.3 | 30.3, 33.4 | -27.9 | +1.0 | 9880 |
| LibriSpeech, 16 kHz, 20 ms | 26.0 | 33.0, 37.8 | -26.8 | +0.6 | 10589 |

Bold figures miss their thresholds (B1 at least 20 dB, B3 within -24 ± 6
dBFS). Every other figure meets its threshold. The 16 kHz B1 figures are
close to the floor, and the 16 kHz peaks are at the ceiling, which the
limiter holds.

**The 48 kHz finding.** `TestFinding48k` reports these figures (it asserts
nothing; `TestPipelineCall` pins the failures):

1. The echo stage cancels less on this input: 15.9 dB, against 20.2 to 20.3
   dB at 16 kHz with the scenario generated at 16 kHz.
2. A higher resampler quality makes it worse, not better: 14.8 dB at
   quality 5 and 9.4 dB at quality 10. With the echo built at 16 kHz from
   the resampled far end it gives 16.9 dB. So neither the low quality of 3
   nor the echo path generated at 48 kHz explains the gap. The content of
   the signals generated at 48 kHz and low-passed to 8 kHz matters, and so
   does the resampler's filter; the mechanism has not been traced.
3. The residual echo after `preprocess` is louder: -18.9 dBFS on active
   frames over 2 to 14 s, against -27.6 dBFS at 16 kHz. The VAD reports
   speech on 335 of those 600 frames (16 kHz: 593 of 600).
4. The AGC takes that residual echo as speech and lowers its gain: -9.2
   dB over 8 to 14 s (16 kHz: -0.7 dB). Over 18 to 24 s, after the near
   end starts at 14 s, the gain is +0.7 dB, against +7.2 dB at 16 kHz. So
   B3 fails as a consequence of B1.

The test pins both failures, so it shows when either changes. Whether to
change the scenario, the chain or the criteria needs a decision.

**Recipe B, sending paced by the received frames** (`docs/usage.md`;
criteria U6 to U10). `drain` queues one sent frame, or silence, with
`Playback` before each `Capture`. From
`go test ./integration -run 'TestBuffered|TestNoAllocsBuffered' -v`
(darwin/arm64, 2026-10-27), 20 ms frames, the call scenario (echo 32 ms
after the far frame):

| Criterion | 8 kHz | 16 kHz | Threshold |
|---|---|---|---|
| U6: echo stage, 0 to 30 ms jitter per frame | **0.7 dB** | **0.3 dB** | at least 15 dB |
| U6: echo through the chain, same run | 3.4 dB | 4.9 dB | reported |
| U7: echo tail, silence queued against nothing queued | 29.4 dB | 10.5 dB | at least 10 dB |
| U8: replay of the two-goroutine order | equal (32 of 500 frames with silence) | (8 kHz only) | equal |
| U9: allocations per tick | 0 | 0 | 0 |
| U10: 100 ms sender pause then a burst, echo stage over 8 to 14 s | 0.5 dB | 0.1 dB | reported |

U6 fails at both rates; the test pins it as a reported finding.
`TestBufferedLagAndDelay` (reported only) gives the echo stage over 8 to
14 s against the far end's lag, the echo delay and the jitter:

| Case | 8 kHz, echo at 32 ms | 8 kHz, echo at 100 ms | 16 kHz, echo at 32 ms | 16 kHz, echo at 100 ms |
|---|---|---|---|---|
| `Cancel`, far end on time | 23.9 | 25.0 | 20.3 | 21.3 |
| `Cancel`, far end 1 frame late | 7.6 | 24.7 | 11.9 | 19.1 |
| `Cancel`, far end 2 frames late | 0.3 | 25.3 | -0.3 | 19.3 |
| `drain`, no jitter | 7.6 | 24.7 | 11.9 | 19.1 |
| `drain`, jitter 0 to 2 ms | 0.3 | 25.3 | -0.3 | 19.3 |
| `drain`, jitter 0 to 5 ms | 0.3 | 25.3 | -0.3 | 19.3 |
| `drain`, jitter 0 to 10 ms | 0.3 | 25.3 | -0.3 | 19.3 |
| `drain`, jitter 0 to 30 ms | 0.7 | 26.2 | 0.3 | 20.3 |

1. Without jitter, `drain` equals `Cancel` with the far end one frame
   late: `Playback` before `Capture` delays the far end by one frame.
   `TestBufferedLagIsOneFrame` checks this bit for bit: received frame k
   is cancelled against sent frame k-1, and frames 0 and 1 against
   silence, because a `Playback` before the first `Capture` is discarded
   (`mdf.c:656`). The SpeexDSP header's two frames of delay is the lag
   when `Capture` comes before `Playback`.
2. With jitter, a sent frame that arrives after its received frame is
   replaced by silence, and every later sent frame moves one frame later.
   With jitter of 2 to 10 ms the figures equal those of `Cancel` with the
   far end two frames late exactly; with 30 ms they are close to them.
3. The canceller's queue no longer runs dry or overflows, so jitter itself
   costs nothing: with a 100 ms echo the echo stage is 24.7 to 26.2 dB at
   8 kHz and 19.1 to 20.3 dB at 16 kHz for every jitter measured.
4. The scenario's echo returns 32 ms after the far frame, sooner than a
   lag of two frames (40 ms), so part of it is ahead of the far end the
   canceller sees: hence U6 fails. U6 stays as a pinned finding: it marks
   the shortest echo delay the recipe can cancel.

**U11, longer echo delays** (`TestBufferedLongDelay`): the call scenario
with the echo's bulk delay at 100 and 200 ms, 0 to 30 ms of jitter, 256 ms
filter, through the real `drain`. Echo stage over 8 to 14 s, threshold
15 dB:

| Echo delay | 8 kHz | 16 kHz |
|---|---|---|
| 100 ms | 25.6 dB | 19.8 dB |
| 200 ms | 24.3 dB | 19.2 dB |

Both pass at both rates. A step of the bulk delay from 100 to 140 ms at
8 s, reported only: the echo stage was 21.7 dB (8 kHz) and 17.6 dB
(16 kHz) over 6 to 8 s, and the first 0.5 s window at 10 dB or more again
started 3.5 s (8 kHz) and 3.0 s (16 kHz) after the step.

**Noise through the send chain** (N1). 30 s of stationary noise,
`preprocess` (denoise and VAD on, defaults) then `agc` (`DefaultConfig`,
gate -50 dBFS) with the VAD decision as `speech`, figures over the last
10 s, 20 ms frames. Levels in dBFS; "VAD" is the share of the 500 frames
reported as speech.

| Rate, noise, peak | Input | After `preprocess` | VAD | AGC output | AGC gain |
|---|---|---|---|---|---|
| 8 kHz, white, 2000 | -29.0 | -42.5 | 100% | -23.8 | +18.6 dB |
| 8 kHz, white, 600 | -39.5 | -52.5 | 100% | -36.0 | +16.5 dB |
| 8 kHz, white, 200 | -49.0 | -55.0 | 100% | -55.0 | 0.0 dB |
| 8 kHz, white, 60 | -59.5 | -74.9 | 0% | -74.9 | 0.0 dB |
| 8 kHz, pink, 2000 | -41.7 | -46.4 | 100% | -28.8 | +17.6 dB |
| 8 kHz, pink, 600 | -51.9 | -56.2 | 100% | -33.3 | +23.0 dB |
| 8 kHz, pink, 200 | -62.2 | -67.6 | 96% | -67.6 | 0.0 dB |
| 8 kHz, pink, 60 | -72.9 | -80.7 | 44% | -80.7 | 0.0 dB |
| 16 kHz, white, 2000 | -29.1 | -42.6 | 100% | -23.8 | +18.8 dB |
| 16 kHz, white, 600 | -39.5 | -52.1 | 100% | -35.5 | +16.6 dB |
| 16 kHz, white, 200 | -49.0 | -59.7 | 100% | -59.7 | 0.0 dB |
| 16 kHz, white, 60 | -59.5 | -75.0 | 100% | -75.0 | 0.0 dB |
| 16 kHz, pink, 2000 | -41.4 | -45.5 | 100% | -28.0 | +17.5 dB |
| 16 kHz, pink, 600 | -51.7 | -55.8 | 100% | -35.3 | +20.5 dB |
| 16 kHz, pink, 200 | -61.4 | -65.4 | 96% | -65.4 | 0.0 dB |
| 16 kHz, pink, 60 | -71.8 | -77.7 | 50% | -77.7 | 0.0 dB |

The answer to ADR `wp4-noise-protection`: denoise does not keep the residual
noise below the -50 dBFS gate for white noise at -40 dBFS and above, or for pink
noise at -52 dBFS and above at the input. There the AGC raises the noise by 16.5
to 23.0 dB, up to its target for the loudest inputs. This happens even where the
average residual is below the gate (white -39.5 and pink -51.9 dBFS inputs, -52
to -56 dBFS after `preprocess`). The AGC gates each 10 ms frame, so frames of
the fluctuating residual above the gate can raise the gain; this explanation
follows from the AGC design and was not traced frame by frame. Below that the
gate holds, and the AGC gain stays at 0 dB. At 16 kHz the VAD reports speech on
almost every frame of noise above about -65 dBFS; at 8 kHz it reports none on
white noise at -59.5 dBFS. No recorded line noise was available, so none was
measured. N2 holds: the largest one-second gain is +23.6 dB (MaxGain 24) and no
sample exceeds the ceiling.

## Platform determinism of generated inputs

Tests regenerate long inputs from seeds, so generators must give the same
bits everywhere. `internal/testutil` uses integer arithmetic or IEEE basic
operations with explicit rounding, and its own sine (`dsin`) instead of
`math.Sin`. `TestGeneratorsAreDeterministic` pins SHA-256 hashes recorded on
darwin/arm64; the same test binary passes on linux/arm64 (container) and
linux/amd64 (emulated). CI repeats it natively on all four runners, where
it passes.

## Race detector and short mode

Under `-race` (detected by the `race` build tag) and `-short`, the
`internal/fixed` reference tests compare the committed prefix and the first
chunk of every stream instead of all 10^7 inputs. Full comparisons run in
the normal `go test`.

The `buffer` SPSC stress test moves 2·10^5 samples per capacity under
`-race` or `-short` instead of 10^6, with the same capacities, chunk sizes
and access styles.

`preprocess` compares the long runs (more than one chunk) up to their first
chunk in those modes, and their per-frame VAD and probability streams up to the
committed prefix; every other case runs in full. `TestReferenceRun` and the
three sanity tests are parallel tests, and the reference cases are parallel
subtests.

Under `-short`, the `echo` reference test runs the first 1 s of every
case and compares the committed prefixes of `out` and
`residual` element by element; it skips the chunk hashes, which need a
whole chunk of 2^20 elements. Its inputs are generated only over their
first 2 s, which `TestCappedInputsArePrefixes` shows equal the full inputs
there. These prefixes are weak: the `out` prefix (0.07 to 0.4 s) is mostly
from before adaptation, every committed `residual` prefix is zero (the
echo estimate is still empty), and ops after 1 s (the reset, rate changes
and `diverge` events) do not run. So under `-short` one configuration per
scenario is still checked in full: 8 kHz, 10 ms, 64 ms, and for `diverge`
16 kHz, 20 ms, 64 ms, which reaches the internal reset.

Under `-race` only those full checks run, in `TestReferenceEcho` and
`TestReferenceWithoutResidualCalls`; the other cases are skipped, along with the
32 and 48 kHz rows of `TestEdgesHitBoundaries`, and the scenario measurements
run only `farend` at 16 kHz, 10 ms, 128 ms. The package code starts no
goroutines and uses no `sync`, so the race detector adds run time there, not
coverage: the only shared state it checks is the tests' input cache. The
normal `go test` checks all 395 cases in full. Swapping `q07` and `q03` in
the adaptation mask (`mdf.go:668`) is caught under `-race` by both
reference tests. Under `-short` (no race) the scenario measurements run one
configuration (16 kHz, 10 ms, 128 ms).

The `echo` reference cases and scenario measurements run as parallel
subtests. Each input set is generated by the first case that needs it,
shared read-only with the other cases of the same test, and dropped after
the last one.

Wall time of `go test -count=1` of one package at a time, with the test
binaries built beforehand, median of three runs on the M1 Pro (10 cores,
macOS, Go 1.26.5, 2026-10-27). "Serial" is the test files of `master` at
`05b676c`, where every case ran one at a time.

| Package, mode | Serial | Parallel |
|---|---|---|
| `echo` | 213 s | 50 s |
| `echo`, `-short` | 37 s | 8 s |
| `echo`, `-race` | 105 s | 70 s |
| `preprocess` | 8 s | 5 s |
| `preprocess`, `-short` | 8 s | 5 s |
| `preprocess`, `-race` | 93 s | 44 s |

Most of what remains runs one test at a time. Under `-race`, `echo` spends
about 45 s in three tests outside the reference cases
(`TestDivisorInvariantsUnderExtremes`, `TestSpeechScenario` with the speech
files present, `TestCaptureEqualsDelayedCancel`), and `preprocess` waits on
`TestSanitySpeech` (about 38 s). In the normal `echo` run,
`TestCappedInputsArePrefixes` takes about 15 s, generating every input set.

Peak resident memory of the normal `echo` test binary (`/usr/bin/time -l`,
one run each, 2026-10-27): 243 MB serial, 303 MB parallel.

`integration` compares the committed prefixes of most
`TestReferencePairing` cases under `-short` and `-race`: 4 frames of the
echo output, preprocess output and residual spectra, which lie in the
silent first 2 s, and the first 200 VAD and probability values, which
reach into the far-end speech only with 20 ms frames. So three cases are
still compared in full there: `cancel_8k_20ms`, `capture_8k_20ms` and
`resampled_48k_16k_20ms`. The normal `go test` compares every stream of
every case in full. The 10-minute cases then generate one 40 s repetition
of their inputs, which `TestScenarioIsPrefixSafe` shows equals the start
of the full inputs.

Under `-race`, `integration` also skips its single-goroutine measurement
and allocation tests, case by case, by the rule that `docs/contracts.md`
section 9 adds for packages that start goroutines: `TestPipelineCall`,
`TestPipelineSpeech`, `TestReceivePath`, `TestNoiseThroughSendChain`,
`TestFinding48k`, `TestBufferedUnevenTiming`, `TestBufferedIdleSender`,
`TestBufferedLagAndDelay`, `TestRealTimeBudget` and the `TestNoAllocs*`
tests. Each skip names the rule, so `go test -v -race ./integration`
lists them. The tests that start goroutines (`TestRecipeThroughSPSC`,
`TestRecipeDropsUnderRace`, `TestBufferedTwoGoroutines`) run in full, and
`TestReferencePairing` runs as under `-short`. The normal `go test` runs
everything. Measured on the M1 Pro, one run each, 2026-10-27:
`go test -race -count=1 -timeout 20m ./integration` took 620.6 s before
the skips (load average 1.89 before, 1.63 after) and 88.2 s with them
(1.58 before, 1.81 after).

CI and the Makefile run the full and race test runs with `-timeout 20m`
instead of Go's default of 10 minutes per package. On CI runners the normal
`echo` test has taken up to 516 s (windows-2025) and the `preprocess` race
run up to 436 s (ubuntu-24.04), too close to the default.

## Speech

`make speech` downloads LibriSpeech dev-clean (CC BY 4.0) into
`.cache/speech/`, checks its MD5 (`42e2234ba48799c1f50f24a7926300a1`, as
published by OpenSLR), and converts the first 20 utterances to 16 kHz int16
raw files. Tests that use them call `testutil.SpeechFiles`, which skips with
a message when they are absent. Run on 2026-10-26: 20 files written.

`echo` (`TestSpeechScenario`, 2026-10-27, 16 kHz): the first 10 files as
far end and the last 10 as near end, 30 s, echo path A, far end only for
the first 15 s, then double talk. ERLE is pooled over the last 4 s before
the double talk; definitions as in `docs/acceptance/echo.md`.

| Frame, filter | Steady-state ERLE | Convergence | Near-to-residual, out / mic | Gain |
|---|---|---|---|---|
| 10 ms, 128 ms | 32.49 dB | 1.0 s | 16.36 / 2.87 dB | 13.49 dB |
| 20 ms, 256 ms | 32.57 dB | 1.0 s | 24.53 / 2.87 dB | 21.65 dB |

`integration` (`TestPipelineSpeech`, 2026-10-27): the cache holds one
speaker (1272), so the utterances of chapter 128104 are the far end and
those of the next chapter the near end, the near end scaled by 1/4, in the
40 s layout of the call scenario. Figures are in "Whole-chain
measurements" above. With the near end unscaled (reported only), B3 is
-26.1 dBFS at 8 kHz and -25.4 dBFS at 16 kHz, and B4 +0.7 and +0.1 dB,
against -27.9 and -26.8 dBFS, +1.0 and +0.6 dB with the 1/4 scaling.

## Fuzzing

Each component adds its fuzz targets here. `internal/fixed` has none: its
functions are pure, and the exhaustive and 10^7-input comparisons cover
their domains.

`internal/fft` has `FuzzTransform`: any supported size, any input and a wrong
length in either slice, for both directions. It checks that a wrong length gives
`ErrBufferSize`, that neither call fails otherwise, that `Inverse` leaves its
input unchanged, and that `Forward` restores an input without -32768. Runs on
2026-10-26
(`go test ./internal/fft -run '^$' -fuzz FuzzTransform -fuzztime <t>`): 60 s
before the length check was added, 2.5 million executions; 30 s with it; no
failure in either.

### `buffer` (WP2)

`FuzzRing` (both policies) and `FuzzSPSC` (one goroutine) decode an operation
sequence and compare the buffer with a slice-queue model after every operation;
`FuzzChunksRing` checks chunk-partition invariance. Seeds are in
`buffer/testdata/fuzz/`. Run on the Mac on 2026-10-26 against the final code,
with `go test -run '^$' -fuzz '^<target>$' -fuzztime <t> -parallel 4 ./buffer`:

| Target | Time | Executions | Failures |
|---|---|---|---|
| `FuzzRing` | 10 min | 27,972,227 | none |
| `FuzzSPSC` | 10 min | 9,290,639 | none |
| `FuzzChunksRing` | 3 min | 2,201,674 | none |

With the WP2b drop and keep-newest operations (operation codes now taken
modulo 12, so the committed seeds decode to different sequences than when
they were added), run again on 2026-10-26 with `-timeout 15m` added (the
default 10-minute test timeout would end a 10-minute run):

| Target | Time | Executions | Failures |
|---|---|---|---|
| `FuzzRing` | 10 min | 29,757,663 | none |
| `FuzzSPSC` | 10 min | 37,028,094 | none |

### `resample` (WP1)

- `FuzzChunksResampler`: a configuration from a fixed list, one of the
  four process methods, and a plan of chunk sizes and output capacities;
  checks that the output equals the one-call `Process` output and that
  nothing panics. Fuzzed for 5 minutes on the Mac (290374 executions), no
  failure. Two seeds come from harness bugs that fuzzing found (plans that
  never offer input and output together, and long runs of empty calls).
- `FuzzConfigResampler`: arbitrary channel counts, ratios (downsampling by at
  most 64, so filters stay small, or by more than 6e8, which the size limits
  reject before allocating), qualities, strides near 1, within 4096 of
  `math.MaxInt` and spread between, and sequences of process, rate, quality,
  reset and skip-zeros calls; checks that nothing panics and that invalid
  arguments are rejected. Fuzzed for 5 minutes on the Mac (5144408 executions),
  no failure. The independent review found a panic (ratios of 2^31 and more
  after a rejected change) and a hang (strides near `math.MaxInt`) outside the
  first version of this domain; against the code before their fix, the widened
  target finds the panic within 2 minutes, and the stride version finds the old
  stride panic within 1 s; both inputs are kept as seeds. A re-review measured
  372k executions in 45 s with no failure.
- `FuzzOutLen`: channel counts 1 to 3, ratios with denominators up to 200
  (downsampling by up to 8), qualities, events (quality and rate changes,
  a rejected change, `Reset`, `SkipZeros`) and calls of 0 to 1275 frames
  through all four process methods, each with an output of exactly
  `OutLen`; checks that all input is consumed, that nothing more than
  `OutLen` is written, and the slack of 1. Fuzzed for 3 minutes on the
  Mac (337474 executions), no failure.

### `preprocess` (WP5)

`FuzzRun`: one of three configurations (8 kHz with `N` = 1, 16 kHz/160, 48
kHz/480), any frame contents, any `int32` residual or a nonnegative one below
2^31, any suppress levels and thresholds, denoise and VAD on or off, and
`EstimateUpdate` mixed in. It checks that nothing fails or panics and that the
VAD reports speech while off. Run on 2026-10-26
(`go test ./preprocess -run '^$' -fuzz FuzzRun -fuzztime 120s`): 1.48 million
executions, no failure. No corpus is committed.

### `agc` (WP4)

`FuzzChunksProcess` checks that any partition of a byte-derived stream
(with speech flags) gives bit-identical output, that no sample exceeds the
ceiling or flips sign against its input, and that the first `Latency`
samples are zero. `FuzzConfig` checks that `New` returns `ErrConfig` or a
working AGC for any configuration. Runs on 2026-10-26 with
`go test ./agc -run '^$' -fuzz <target> -fuzztime 60s`, no failures:
design v1 `FuzzChunksProcess` 4,793,678 and `FuzzConfig` 1,415,919
executions; design v2 (after its review corrections) 587,550 and 4,437,804
executions. The seed corpora are the `f.Add` calls; the fuzzer found
nothing to commit to `agc/testdata/fuzz/`.

`echo` has `FuzzCanceller`: a random configuration (frame sizes 1 to 480,
filter lengths, rates including 32767, 32768 and 44100), then a random
sequence of `Cancel`, `Playback`, `Capture`, `Reset`, `SetSampleRate`,
`Residual`, `ImpulseResponse` and wrong-length calls, with samples from the
input or set to -32768 or alternating full scale. It checks that nothing
panics, that wrong lengths give `ErrBufferSize`, that a `Capture` with an
empty queue copies the microphone, and the divisor invariants (`power >= 0`,
`Xf >= 0`) after every call. Run on 2026-10-26 for 60 s, 2.77 million
executions, no failure:

```sh
go test ./echo -run '^$' -fuzz FuzzCanceller -fuzztime 60s
```

No corpus is committed; the seeds are `f.Add` only.
