# govoip-dsp requirements

This is the stable specification for `govoip-dsp`. All agents and contributors
work from it. Work-package briefs in [`prompts/`](../prompts/) say *what to build next*; this
document says *what "correct" means*. If a brief and this document conflict,
this document wins; raise the conflict in [`docs/status.md`](status.md) rather than
resolving it silently.

Changes to this file require the project owner's approval.

---

## 1. Objective

`govoip-dsp` (module `github.com/danieldonoghue/govoip-dsp`) is a
production-quality, pure-Go, real-time audio DSP library for VoIP, WebRTC-style
communications and streaming media pipelines.

Its compatibility reference is **SpeexDSP 1.2.1, FIXED_POINT build** (§4). The
goal is an idiomatic, maintainable, high-performance Go implementation whose
compatibility paths are **bit-exact** with that reference, plus clearly
separated, documented extensions where the reference is missing or deficient.

The project is not a CGO wrapper and does not reproduce the C API mechanically.

### Non-negotiable requirements

- Pure Go at runtime: no CGO, C compiler, native library or external executable
  is required to build, test or use the library.
- A portable Go implementation of every component is the correctness reference
  for all architectures. Optimised kernels must be bit-exact with it (§6).
- Zero heap allocations per steady-state frame after construction and warm-up
  for all streaming components, verified by tests.
- Compatibility behaviour and extensions are separated in code, configuration
  and documentation.
- No placeholder implementations, stubs, or advertised-but-unimplemented
  features.
- Honest reporting (§17).

---

## 2. Scope and priorities

### MVP (must ship in the first release)

| Component | Package | Parity class | Notes |
|---|---|---|---|
| Fixed-point arithmetic | `internal/fixed` | Bit-exact | Port of `arch.h`, `fixed_generic.h`, `math_approx.h`, `pseudofloat.h` semantics |
| Streaming resampler | `resample` | Bit-exact | Port of `resample.c`; multichannel supported |
| Ring buffer | `buffer` | None (own design) | `buffer.c` is design input only |
| FFT and DSP primitives | `internal/fft` (+ helpers as needed) | Bit-exact | KISS FFT fixed-point via `fftwrap.c` semantics |
| Noise suppression + VAD | `preprocess` | Bit-exact | Port of `preprocess.c` + `filterbank.c`, mono |
| Acoustic echo canceller | `echo` | Bit-exact | Port of `mdf.c`, mono (1 mic, 1 speaker) |
| Automatic gain control | `agc` | Behavioural only | Own fixed-point design (SpeexDSP 1.2.1 has no fixed-point AGC, see §4.4) |
| Developer documentation | every public package, root package `govoipdsp`, `docs/` | n/a | §13; GitHub pages with Mermaid diagrams, plus text-only go doc with examples |

### Stretch (in priority order, after MVP is validated)

1. Packet jitter buffer (`jitter`), reference `jitter.c`.
2. Clock-drift ratio controller on top of the resampler (extension).
3. Performance work (WP9, after the developer documentation): portable gains,
   then arm64 NEON and amd64 SSE2/AVX2 kernels for profiled hot spots.
4. Multichannel AEC (`speex_echo_state_init_mc` equivalent).
5. Dereverberation, as an extension only. SpeexDSP 1.2.1 has a dereverb enable
   flag, but its level and decay setters are commented out
   (`preprocess.c:1112-1125`), so the reverb term is always zero. Do not mirror
   those controls as if they worked.

A stretch item starts only after WP7 is merged and the project owner has
reviewed its final report.

### Sizing rule

For a ported component, the reference API sets the compatibility scope. For
everything else, build what a current consumer needs and record the rest as
future work. A choice between a concrete type and a general mechanism (an
interface, a registry, a hook) goes to the project owner before it is built.

### Out of scope

RTP parsing, codecs, codec-specific PLC, neural models, hard real-time
guarantees. Codecs are planned for a separate repository, one package per
codec (`docs/decisions/owner-codecs-separate-repo.md`).

---

## 3. Data representation and public API conventions

### Samples

- The native sample type is **`int16` on the 16-bit scale `[-32768, 32767]`**,
  matching `spx_int16_t` in the fixed-point reference.
- Where a component offers `float32` entry points (the resampler does, mirroring
  `speex_resampler_process_float`), the `float32` values are **on the same
  16-bit scale, not normalised to `[-1, 1]`**.
- Saturation follows the fixed-point reference exactly, at the same points:
  - `float32` input is converted with the fixed-point `WORD2INT` (`arch.h:104`,
    applied at `resample.c:1003`): `x < -32767 → -32768`, `x > 32766 → 32767`,
    otherwise `x`, then C's conversion to `spx_int16_t` (truncation toward
    zero).
  - `int16` output is saturated inside the arithmetic, where the reference does
    it (for example `SATURATE32PSHR` in the resampler's inner product).
  - `float32` output is an exact copy of the `int16` result (`resample.c:1020`),
    so it is always an integer in `[-32768, 32767]`.
- **Non-finite input policy (float32 entry points only):** C behaviour for NaN
  in `WORD2INT` is undefined. Go converts NaN to `0` before saturation; `±Inf`
  saturates through the normal comparisons. This is a documented deviation and
  must be tested.

### Channels

- Resampler: interleaved multichannel with the reference's stride semantics.
- Echo and preprocess: mono in the MVP.

### API rules

- Explicit `Config` structs and `New...` constructors returning `(*T, error)`.
  Invalid configurations return errors; no panics for caller mistakes.
- Streaming methods take caller-provided input and output slices; no per-call
  output allocation.
- Every stateful type documents: supported rates and frame sizes, chunking
  rules, buffer ownership, whether input and output may alias, whether caller
  memory is retained, output-exhaustion behaviour, `Reset` semantics,
  concurrency (one instance per stream, not safe for concurrent use unless
  stated), which settings require reinitialisation, latency, and allocation
  behaviour.
- SpeexDSP's `ctl`-style requests become typed methods or config fields. Return
  codes become Go errors.
- Keep exported APIs small; keep low-level operations in `internal/` unless
  there is a clear reason to export them.
- Cross-package contracts (for example the echo → preprocess residual-echo
  coupling) are defined in `docs/contracts.md`, which the foundation work
  package (WP0) creates and owns. Prefer concrete types; an interface needs
  owner approval (§2).

---

## 4. Reference implementation

### 4.1 Pinned reference

- Upstream: `https://github.com/xiph/speexdsp`
- Tag: `SpeexDSP-1.2.1`, commit `1b28a0f61bc31162979e1f26f3981fc3637095c8`
- Build configuration:
  - `FIXED_POINT` defined.
  - `USE_KISS_FFT` defined (the default FFT for fixed-point in `configure.ac`).
  - `USE_SSE`, `USE_SSE2`, `USE_NEON` **undefined**. SpeexDSP's own SIMD paths
    are not the reference.
  - `RESAMPLE_FULL_SINC_TABLE` **undefined**: the resampler chooses direct or
    interpolated filter tables via `use_direct` (`resample.c:647`) exactly as
    upstream does.
  - `FIXED_DEBUG` undefined.
- The harness compiles the needed `.c` files directly with a checked-in
  `config.h`, rather than relying on autotools, so the configuration is
  explicit.
- Compiler flags: `-O2 -fwrapv -ffp-contract=off`. `-fwrapv` makes signed
  overflow wrap, matching Go. `-ffp-contract=off` prevents fused multiply-add in
  the floating-point initialisation code. Additionally, build once with
  `-fsanitize=undefined` and record any reported overflows or other undefined
  behaviour in `docs/testing.md`; each one is a place where the C result depends
  on compiler behaviour.
- Record the compiler, version, flags, OS and architecture with every fixture
  set.

### 4.2 Reference harness and fixtures

- Location: `reference/` (C sources and scripts only; no `.go` files there, so
  the Go toolchain ignores it). The upstream source is fetched at the pinned
  commit into a git-ignored directory; it is not vendored.
- Canonical fixture environment: a Linux container with a pinned image digest
  and pinned compiler. Cross-check the same fixtures on the macOS host
  toolchain; any difference is a finding to document, not something to average
  away. If Docker is unavailable, use the host toolchain and record that.
- Fixtures live in `testdata/speexdsp/<component>/` as little-endian raw `int16`
  plus a JSON manifest (config, input generator and seed, reference commit,
  compiler, flags, platform, SHA-256 of each file).
- Keep fixtures small and deterministic: individual files ≲ 1 MB, total
  committed fixtures ≲ 20 MB. Long-duration comparisons generate inputs at test
  time from seeds and compare against hashes or against harness output
  regenerated locally.
- `make fixtures` (or a documented script) regenerates everything. Ordinary
  `go test` and CI never need a C compiler or network access.
- If the agent cannot fetch upstream or run a C compiler, it must not invent
  fixtures. It implements the component, marks parity as **unverified** in
  `docs/status.md`, and continues.

### 4.3 Porting rules for bit-exactness

The fixed-point paths are integer arithmetic and are expected to be bit-exact.
The traps are:

- **Integer semantics:** Go integer types must match the C widths of
  `spx_word16_t`, `spx_word32_t`, `spx_int16_t`, `spx_int32_t`, `spx_uint32_t`.
  Signed right shift is arithmetic in both. Integer division truncates toward
  zero in both. Conversions to narrower types wrap in both (with `-fwrapv`).
- **Macro fidelity:** every `MULT16_16_Q15`, `PSHR32`, `SATURATE`, `EXTRACT16`
  and so on is ported to a function in `internal/fixed` with exactly the generic
  (`fixed_generic.h`) semantics, including rounding and saturation. Use small
  inlinable functions and confirm inlining with `-gcflags=-m` on hot paths.
- **Float in initialisation code:** parts of the fixed-point build still use C
  `float`/`double` and libm during setup (for example the resampler sinc table,
  `resample.c:284`, and `QCONST16` with runtime arguments,
  `fixed_generic.h:38`). The fixed-point `filterbank.c`, `preprocess.c` and
  KISS FFT twiddles use integer approximations instead (`spx_atan`,
  `spx_exp`, `kf_cexp2`); check each source rather than assuming.
  Replicate C's expression types exactly: `float` vs `double`, literal suffixes,
  implicit promotions, truncating casts. Prevent Go's fused multiply-add on
  arm64 by wrapping intermediate products in explicit
  `float64(...)`/`float32(...)` conversions.
- **libm:** Go's `math` package and the platform libm can differ in the last
  bit. Every table derived from libm calls (sinc tables, filterbank weights,
  window tables) must have a test comparing it entry-by-entry with the harness
  output for every supported configuration. A libm-induced mismatch is the only
  sanctioned source of non-exactness. If one is found, document it, and prefer
  embedding the reference-generated table (with provenance) over accepting a
  tolerance.

### 4.4 Parity classes

- **Bit-exact:** output equals the pinned reference exactly for the specified
  configuration. This is a regression-tested requirement for every MVP component
  except `buffer` and `agc`.
- **Behavioural:** no reference exists. Acceptance criteria are written before
  implementation (§9) and tested with signal metrics.
- Never relax a bit-exact requirement because a test fails. Investigate, find
  the divergence point (first differing sample and the state that produced it),
  fix it or document a root-caused exception approved by the project owner.

### 4.5 Compatibility vs extensions

- The default configuration of every ported component is the compatibility path.
- Improvements (better delay estimation, gradual drift control, alternative
  suppression, dereverb, own AGC) are explicit options or separate types,
  documented as extensions, and never change the compatibility path's output.

---

## 5. Component requirements

### 5.1 `internal/fixed`

- Port every macro and approximation used by the MVP components from `arch.h`,
  `fixed_generic.h`, `math_approx.h` (`spx_sqrt`, `spx_exp`, `spx_atan`,
  `spx_cos`, and so on) and `pseudofloat.h` (`spx_float_t` used by `mdf.c`).
- Test against the harness: exhaustive over all `int16` inputs for unary
  operations; edge values plus large seeded random samples for binary
  operations.
- The package is append-only during parallel work: work packages may add
  operations, never change existing semantics.

### 5.2 `resample`

- Port of `speex_resampler_*`: quality levels 0–10, integer and fractional rate
  ratios (`set_rate_frac`), multichannel with strides, `skip_zeros`,
  input/output latency queries, `reset_mem`, and the reference's
  consumed/produced accounting and output-exhaustion behaviour.
- `int16` and `float32` (16-bit scale) process methods.
- Must handle chunks too small to produce output and arbitrary chunk sequences;
  chunking must not change output (test: identical output for any partition of
  the same input).
- Rates: at least 8, 16, 24, 32, 44.1, 48 and 96 kHz in every pairing; arbitrary
  ratios within the reference's limits.
- Measure and document passband ripple, stopband attenuation, aliasing and
  latency per quality level, using the Go implementation (bit-exact means these
  equal the reference's).
- Rate changes follow the reference semantics (`update_filter`). A gradual drift
  controller is stretch item 2 and is an extension.

### 5.3 `buffer`

- Ring buffer for `int16` samples: fixed capacity, explicit full/empty and
  overflow semantics (reject or overwrite, chosen explicitly by config),
  contiguous read/write views where possible, correct wrap-around, safe
  zero-length operations, zero allocations after construction.
- Two separate types:
  1. A single-owner ring buffer with no synchronisation.
  2. A lock-free single-producer/single-consumer ring buffer for hand-off
     between the audio path and another goroutine. Both sides are wait-free
     (atomic index loads and stores only; no mutex, no compare-and-swap retry
     loop, no blocking), with a documented memory-ordering argument and `-race`
     stress tests.

### 5.4 `internal/fft`

- Port KISS FFT fixed-point (`kiss_fft.c`, `kiss_fftr.c`) and the fixed-point
  `fftwrap.c` behaviour (`spx_fft`, `spx_ifft`, including its scaling and
  range-maximising shifts), for every size the MVP components use.
- Document transform conventions, scaling and layout.
- Test round trips, known vectors, and bit-exact agreement with the harness.
- Keep it internal in the MVP. Exporting a public FFT is a later decision with
  its own ADR.

### 5.5 `preprocess` (noise suppression + VAD)

- Port `speex_preprocess_*` for the fixed-point build: denoise, VAD
  (`prob_start`/`prob_continue`), noise-suppress level, echo-suppress levels,
  residual-echo coupling with `echo`.
- VAD is exposed through `preprocess`, as in the reference; VAD-only operation
  (denoise disabled) must work. Do not call the speech probability a calibrated
  probability.
- AGC controls do not exist in this build and are not mirrored; AGC is the
  separate `agc` package.
- Beyond bit-exactness, include sanity signal tests showing noise reduction and
  speech preservation so regressions are visible in human terms.

### 5.6 `echo` (MDF AEC)

- Port `speex_echo_*` (MDF, `mdf.c`) for the fixed-point build, mono:
  `cancellation`, `capture`/`playback` buffered mode with its documented
  semantics, `reset`, frame size, filter length, sampling rate, and the
  residual-echo spectrum used by `preprocess`.
- Document reference alignment requirements: what delay the filter length
  covers, and what happens when the acoustic delay exceeds it. Do not claim
  delay estimation.
- Beyond bit-exactness, include the synthetic scenarios below with measured
  ERLE, convergence time and near-end preservation. These are sanity and
  regression measures; the bit-exact port defines correctness. Scenarios:
  far-end only, near-end only, double-talk, silence transitions, low-level
  noise, short and long impulse-response echo paths, sudden and gradual path
  changes, misaligned reference, long runs (≥ 10 minutes of audio),
  divergence-provoking input, reset mid-stream.

### 5.7 `agc` (own design, behavioural)

- Integer fixed-point AGC on 16-bit scale samples: configurable target level,
  attack/release, gain limits, clipping protection, noise-amplification
  protection (gate or VAD input), stable silence handling, reset semantics, zero
  allocations.
- Speex's floating-point AGC may be studied as design input; it is not a parity
  target, and no code is ported from it without notices.
- Acceptance criteria (§9) must cover level convergence, pumping (gain
  modulation on steady speech), overshoot on transients, clipping counts, and
  noise-floor amplification.

### 5.8 Stretch components

- **`jitter`:** port of `jitter.c` semantics (timestamp-based playout,
  reordering, late/duplicate/missing handling, adaptive delay, statistics),
  independent of RTP parsing and codecs. Loss is reported as a concrete "packet
  missing" result for the caller to conceal; any hook needs owner approval (§2).
  Parity class decided in its brief (`jitter.c` uses some `float`).
- **Drift controller:** extension that adjusts the resampler ratio gradually
  from caller-supplied observations (queue-depth error, timestamps, or an
  external rate estimate), bounded and audibly smooth. Must not change
  fixed-ratio resampler output.
- **SIMD:** see §6.

---

## 6. Platforms and optimisation

- Supported: Linux (including Debian containers), macOS (Apple Silicon),
  Windows; amd64 and arm64, with portable fallbacks building on all Go-supported
  architectures.
- Must pass: `CGO_ENABLED=0 go vet ./...` and `CGO_ENABLED=0 go test ./...`.
- Architecture kernels are stretch work and need a profile showing the hot spot
  plus a benchmark showing the gain, recorded in `docs/performance.md`. No
  assembly to satisfy a checklist.
- **Optimised kernels must be bit-exact with the portable code.** For integer
  arithmetic this is achievable: wrapping int32 accumulation is
  order-independent, so vectorised sums match provided shifts, rounding and
  saturation happen at the same points.
- Runtime CPU detection via `golang.org/x/sys/cpu` (BSD-3-Clause, the only
  pre-approved dependency). Build tags select code, not CPU support. AVX2 must
  never run on a CPU without it; VMware VMs may hide AVX2.
- An environment variable or build tag (`purego`) forces the portable path for
  tests, benchmarks and debugging. Every kernel test runs both paths and
  compares outputs.
- No overreads or overwrites; tails, odd lengths, alignment and aliasing tested;
  no caller alignment requirements.

---

## 7. Performance and allocations

- Benchmarks for every hot path: `ns/op`, `allocs/op`, `B/op`, samples/s, and
  processing time as a fraction of frame duration. Use `b.ReportMetric` for the
  derived values.
- Workloads: 10 ms frames at 8/16/32/48 kHz (80/160/320/480 samples), 20 ms
  frames, odd chunk sizes, and expensive configurations (resampler quality 10,
  AEC filter length covering ≥ 200 ms).
- Allocation tests (`testing.AllocsPerRun`) assert zero allocations in steady
  state. Construction cost is measured separately.
- **Real-time budget:** a 10 ms frame through the combined chain (resample +
  echo + preprocess + agc) must use well under 10 ms on both reference
  environments. Report p50, p99 and the maximum observed over at least 10
  minutes of audio.
- **Reference environments:**
  1. Apple Silicon Mac (native arm64; record the model, macOS and Go versions).
  2. Linux amd64 VMware VM (record `/proc/cpuinfo` flags; AVX2 may be absent).
- Published numbers include the hardware, CPU features, OS, Go version, exact
  command and configuration. Distinguish measured numbers from targets.

---

## 8. Testing

Every component needs tests for: normal operation, empty and minimum inputs,
invalid config, boundaries, reset and reinitialisation, chunk-partition
invariance, output exhaustion, silence and low-level input, full-scale input,
long runs (state and memory must not grow), and, for `float32` entry points, the
non-finite policy.

- **Reference tests:** bit-exact comparisons against fixtures for every
  bit-exact component; report the first mismatching index and context on
  failure.
- **Signal tests:** silence, impulses, sines, sweeps, white/pink noise, clipped
  signals, low-level signals, speech, echo-contaminated and reverberant signals
  as relevant. Shared generators and metrics live in `internal/testutil`.
- **Speech:** committed fixtures are synthetic. Real speech comes from
  LibriSpeech (CC BY 4.0) through a download script into the git-ignored
  `.cache/speech/` directory; tests using it skip cleanly when it is absent and
  say so. CI fetches and caches it on one job (Linux amd64, the newest Go),
  so those tests run on every CI run that changes code. Record attribution
  in `THIRD_PARTY_NOTICES.md`.
- **Fuzzing:** Go fuzz tests for configuration handling, chunk sequences, buffer
  boundaries and the jitter buffer's packet sequences; they check for panics and
  documented invariants (for example chunk-partition invariance). Keep each
  package's seed corpus in its own `testdata/fuzz/` directory, where Go expects
  it.
- **Race:** `go test -race ./...` runs on macOS (no cgo needed) and in CI. On
  Linux the race detector needs cgo and a C toolchain; this is a
  development/CI-only exception to the no-C rule.
- **Static checks:** `gofmt -l` must print nothing; `go vet`; `staticcheck` in
  CI.
- **Tests that can fail:** for a claim that something is bounded, safe or
  correct, write the test first and show it failing against a deliberately
  broken version. Record mutation checks as "caught" or "not caught".
- Never delete failing tests, loosen comparisons, skip cases or trim benchmark
  work to get green. Investigate and document.

---

## 9. Acceptance criteria are pre-registered

Before implementing a component, its work package commits
`docs/acceptance/<component>.md` stating:

- the parity class and exactly which configurations are covered;
- for behavioural criteria, numeric thresholds and the test signals (for
  example: "AGC output RMS within ±2 dB of target 1 s after a 20 dB step; gain
  modulation ≤ 1 dB on steady speech; zero clipped samples on the transient
  suite");
- the benchmark targets.

This commit must come before the implementation commits. Later changes to the
criteria need a written justification in the same file and must not be made in
the same commit that makes a failing test pass.

---

## 10. CI

GitHub Actions, using native runners where available: `ubuntu-24.04` (amd64),
`ubuntu-24.04-arm` (arm64), `macos-15` (arm64), `windows-2025` (amd64).

Runner labels name an explicit image version, never `-latest`, so a runner
image changes only by a deliberate commit that updates this list and
`.github/workflows/ci.yml` together.

CI does not run for a change that touches only documentation: Markdown files,
`docs/`, `prompts/`, `LICENSE` and `LICENSES/`. A change that also touches
anything else runs the full workflow.

- gofmt check, vet, staticcheck, `CGO_ENABLED=0 go test ./...` on every runner.
- Race tests on Linux amd64 and macOS.
- `-tags purego` test run, to prove the portable paths build and pass on their
  own.
- Cross-compile GOOS/GOARCH combinations that have no runner (for example
  `linux/arm`, `linux/riscv64`) and report them as **build-only**.
- Native-dependency guard: build with `CGO_ENABLED=0` and fail if any package
  imports `C` or `go list -deps` shows unexpected modules.
- Allocation tests run in CI; benchmarks run in short mode as a smoke test only
  (CI timing is not published).
- Fixture regeneration is a separate, manually triggered workflow.

Never report a platform as tested because it cross-compiled.

---

## 11. Dependencies and licensing

- Project licence: BSD 3-Clause (`LICENSE`: "Copyright (c) 2026, Daniel
  Donoghue"), the same licence as upstream SpeexDSP.
- Prefer the Go standard library. A third-party dependency is used only when
  the standard library cannot do the job.
- Pre-approved dependency: `golang.org/x/sys` (for `cpu`). Any other dependency
  needs an ADR covering licence, purity under `CGO_ENABLED=0`, maintenance,
  platforms, performance, and why a small own implementation is worse.
- **Bit-exact ports are derived works of SpeexDSP.** Every Go file ported or
  adapted from SpeexDSP carries a header naming the upstream file(s) and
  revision, the upstream copyright lines, and a pointer to the BSD-3-Clause
  text. Upstream `COPYING` is reproduced in `LICENSES/SpeexDSP-COPYING`. KISS
  FFT carries its own BSD notice (Mark Borgerding, Jean-Marc Valin) in
  `LICENSES/kissfft-COPYING`.
- `THIRD_PARTY_NOTICES.md` lists every upstream source used, every derived file
  or package, dependencies, and fixture/speech provenance. The README states the
  project licence and points to the notices, so that the upstream copyright
  holders of derived code are visible.
- Own-design components (`buffer`, `agc`, extensions) contain no copied upstream
  code; say so in their package docs.
- Never copy code of unknown or incompatible licence.

---

## 12. Repository layout

| Path | Contents |
|---|---|
| `CLAUDE.md` | Working rules for agents |
| `PROMPT.md` | Work packages and how to launch agents |
| `prompts/` | One brief per work package |
| `README.md`, `LICENSE`, `THIRD_PARTY_NOTICES.md` | Project front matter |
| `LICENSES/` | Third-party licence texts |
| `Makefile`, `go.mod` | Build entry points: check, test, race, bench, fixtures |
| `resample/`, `buffer/`, `preprocess/`, `echo/`, `agc/` | Public packages (`jitter/` when started) |
| `internal/fixed/` | Fixed-point operations (shared, append-only) |
| `internal/fft/` | Fixed-point FFT |
| `internal/testutil/` | Signal generators, metrics, fixture loader, allocation helpers |
| `internal/kernels/` | Architecture-specific code (stretch), isolated |
| `reference/` | C harness, `config.h`, scripts (no Go files) |
| `testdata/speexdsp/` | Committed fixtures and manifests |
| `docs/requirements.md` | This file |
| `docs/status.md` | Coordination board and implementation status |
| `docs/contracts.md` | Cross-package interfaces and conventions (created by WP0) |
| `docs/acceptance/` | Pre-registered acceptance criteria |
| `docs/architecture.md`, `algorithms.md`, `performance.md`, `testing.md` | Reference documentation |
| `docs/decisions/` | Architecture decision records (ADRs) |
| `.github/workflows/` | CI |

Create packages only when there is code for them. `go.mod` targets the oldest Go
release still supported by the Go team when the module is initialised (Go 1.26
as of October 2026); CI tests both supported releases.

---

## 13. Documentation

- **README:** what the library is, the no-CGO design, the SpeexDSP 1.2.1
  fixed-point compatibility statement, supported platforms (tested vs
  build-only), component status table (linking `docs/status.md`), install, a
  minimal example, how to run tests and benchmarks, limitations, licence.
- **architecture.md:** packages, data flow, typical VoIP pipeline ordering
  (capture → echo → preprocess → agc → encode; decode → jitter → resample →
  playback), dispatch, and how the clock-drift, jitter, resampling, latency and
  echo-alignment problems differ.
- **algorithms.md:** per component: algorithm, reference behaviour, parameters,
  latency, numerical notes, known deviations.
- **performance.md:** measured results with environment and commands.
- **testing.md:** strategy, harness, fixture provenance, UB-sanitiser findings,
  libm findings, fuzzing, special requirements.
- **ADRs** for significant decisions.
- **Developer documentation (WP8):** GitHub is the primary form: Markdown
  pages in `docs/` with Mermaid diagrams and flow charts, and a glossary that
  defines every term the packages use. Go doc is a simpler, text-only
  companion: every exported identifier has a doc comment and a runnable
  example with checked output, package docs state the lifecycle, rules and
  limitations, and they link to the GitHub pages (full URLs) for diagrams
  and longer explanations. A root package, `govoipdsp`, gives go doc an
  overview and the end-to-end examples.

---

## 14. Status levels

Each component in `docs/status.md` is one of: **Planned**, **In progress**,
**Implemented** (functional, unit-tested), **Validated** (meets its
pre-registered acceptance criteria, including bit-exact fixtures where
applicable), **Optimised** (kernels validated and benchmarked). Compiling is
never validation.

---

## 15. Definition of done (per component)

Real implementation; documented API and limitations; unit, boundary, reset and
chunking tests; acceptance criteria met; reference comparisons where applicable;
signal tests where applicable; benchmarks and zero-alloc tests; portable path
works under `-tags purego`; optimised paths (if any) bit-exact with portable;
licence headers and notices done; repository builds and tests with
`CGO_ENABLED=0`; no placeholder behaviour.

Anything short of this is reported as incomplete or partially validated.

---

## 16. Real-time and memory-safety rules

The **audio path** is every method that a caller invokes per frame or per chunk
(process, cancel, run, read, write, push, pop and the like). On the audio path:

1. **Never block.** No I/O, syscalls, channel operations, sleeps, timers, or
   waiting on another goroutine.
2. **Lock-free as far as is practical.** No mutexes, `sync.RWMutex`,
   `sync.Cond`, `sync.Once` or `sync.Pool`. Single-owner types use no
   synchronisation. Hand-off between goroutines uses the lock-free SPSC ring
   buffer (§5.3). Any other shared state needs an ADR that shows why lock-free
   is impractical, and it must still never block the audio side.
3. **No contention.** No compare-and-swap retry loops and no spinning on the
   audio side. Atomics are plain loads and stores of state owned by one writer.
4. **No allocation and no goroutines.** This includes hidden allocation: `fmt`,
   `log`, `errors.New` per call, interface boxing, closures that escape,
   `append` growth, map growth. Return preallocated sentinel errors.
5. **Bounded work.** The cost of a call is proportional to its input length,
   with a documented worst case. No input-dependent unbounded loops or retries.
6. **No panics for any input.** Bounds are proven by tests, including fuzzing.
   Invalid input returns an error or follows a documented policy.
7. **No use-after-free equivalents.** Go has a garbage collector, but the same
   bugs appear as stale or shared slices:
   - Never retain a caller's slice after a call returns, unless the API
     documents it.
   - A view returned to a caller stays valid until a documented point (for
     example the next call). The processor never writes into memory that a
     caller may still be reading, and tests prove it.
   - `Reset` and reconfiguration must not reallocate or reuse memory that a
     caller may still hold a view of, unless the API documents that views expire
     there.
   - Input and output may alias only where the API says so, and that case is
     tested.
   - No `unsafe` on the audio path without an ADR. Code that uses it is tested
     with `-race` and `-gcflags=all=-d=checkptr`.
8. **Concurrency claims are traced.** For any type used by two goroutines, write
   down the interleaving of the two sides at each shared access, and test it
   under `-race`.

Document ownership and concurrency for each type. Steady-state zero allocation
keeps the garbage collector's work off the audio path. Go is not a hard
real-time platform, so measure and report tail latency rather than claiming
guarantees.

---

## 17. Integrity rules

- Never fabricate test results, benchmark numbers, hardware availability or
  validation.
- Never mark something tested on a platform where it was only cross-compiled.
- Never ship stubs or advertise planned features as done.
- If time runs out, leave the branch building and passing, finish or cleanly
  back out the current unit, and record exactly what remains in
  `docs/status.md`.
