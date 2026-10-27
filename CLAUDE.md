# govoip-dsp

Pure-Go real-time audio DSP library for VoIP. Its compatibility paths are a
**bit-exact port of SpeexDSP 1.2.1, FIXED_POINT build** (commit `1b28a0f6`, no
SIMD, no `RESAMPLE_FULL_SINC_TABLE`). Module:
`github.com/danieldonoghue/govoip-dsp`.

These are the working rules for anyone changing this repository, people and
coding agents alike.

## Read first

1. `docs/requirements.md`: the spec. It wins over any other plan. Changes to
   it need approval.
2. `docs/contracts.md`: cross-package conventions and interfaces.
3. `docs/architecture.md`: the packages, how they fit together, and the work
   packages (WP) that build them.
4. `docs/decisions/`: architecture decision records (ADRs), indexed in its
   `README.md`.

## Commands

```sh
make help      # list every target (the default)
make hooks     # once per clone: run make check-short before every git push
make check     # gofmt -l, CGO_ENABLED=0 vet + test, purego test, deps guard, staticcheck
make race      # go test -race ./...  (works on macOS without cgo)
make bench     # short benchmarks
make fixtures  # regenerate SpeexDSP reference fixtures (needs clang or Docker, and network)
make cross     # cross-compile matrix
```

Run one test with `go test ./resample -run TestName -v`.

## Making a change

1. Work on a branch from `master`. Work packages use `wp/<id>-<name>`.
2. Stay inside the component you are changing. Shared areas:
   - `internal/fixed`, `internal/testutil`: append-only. Add new functions with
     tests, in their own commit. Never change existing semantics.
   - `docs/contracts.md`: append-only. Propose a change to an existing
     contract instead of editing it.
   - `THIRD_PARTY_NOTICES.md`, `docs/performance.md`, `docs/testing.md`,
     `docs/algorithms.md`: edit only your component's section.
   - `docs/decisions/`: name ADRs `<wp>-<slug>.md` (for example
     `wp1-sinc-table.md`), or `<slug>.md` for a decision outside any work
     package, so parallel branches never collide. Add each new ADR to
     `docs/decisions/README.md`.
   - `go.mod`: no new dependency without an ADR. Only `golang.org/x/sys` is
     pre-approved.
3. For a new component or work package, commit the pre-registered
   acceptance criteria (`docs/acceptance/<component>.md`) before any
   implementation commit.
4. Make small, coherent commits that each build and pass tests.
5. Before asking for review: rebase onto `master`, run `make check` (and
   `make race` where relevant), and have the diff reviewed by someone who did
   not write it (see "Verify before asserting").
6. Changes reach `master` only after approval; branches merge by
   fast-forward.

### Commit messages

- Format: a first line `<package>: <imperative summary>`, then a Markdown body
  that describes only the changes.
- Describe what the change is, not where uncommitted code lives.

## Before building

- Find what already does the job, and reuse or extend it. Search the code
  (`grep`, gopls) before designing each piece of a change. Check `go.mod`,
  `internal/fixed` and `internal/testutil` before hand-rolling anything.
- Call into another package's mechanism; never mirror it. For example, use
  `internal/fixed` for every fixed-point operation instead of re-deriving a
  macro locally.
- Size to the consumers that exist today (requirements §2, "Sizing rule"). Put a
  choice between a concrete type and a general mechanism (an interface, a
  registry, a hook) up for approval before building it.

## Verify before asserting

- State a fact only after the check that establishes it, and cite it, or mark
  the claim unverified. Keep "I ran X and it showed Y" apart from "I believe Y".
- The usual failure is verifying the code just written rather than the claim
  just made. Test the claim.
- For a claim that something is bounded, safe or correct, first write the test
  that fails if it is false, and show it failing.
- Read adjacent code and its comments before changing it. Find the competing
  precedent before citing one.
- Ask what would make your own test, benchmark or script lie. Examples: a
  bit-exact test comparing Go output against fixtures that Go generated; a
  benchmark whose work the compiler removed; an alloc test that skips warm-up or
  the expensive branch.
- Do not trust a "fixed" commit message. For concurrency, trace two goroutines
  interleaving.
- Before reporting work as done, have it reviewed by someone who did not write
  it. Give the reviewer the code and the requirement, not your conclusions.
- Bring options and evidence on design and scope calls, not verdicts.

## Audio path rules (requirements §16)

The audio path is every per-frame or per-chunk method. On it:

1. Never block: no I/O, syscalls, channel operations, sleeps or waits.
2. Lock-free as far as is practical: no mutexes or other `sync` primitives.
   Cross-goroutine hand-off uses the lock-free SPSC ring buffer. Any other
   shared state needs an ADR (requirements §16).
3. No lock contention, no compare-and-swap retry loops, no spinning.
4. No allocation (including hidden allocation from `fmt`, boxing, escaping
   closures, `append` or map growth) and no goroutines.
5. Bounded work, proportional to input length. No panics for any input.
6. No use-after-free equivalents: never retain caller slices; never write into a
   view a caller may still hold; `Reset` must not invalidate live views unless
   documented; aliasing only where documented and tested; no `unsafe` without an
   ADR and `checkptr` tests.

## Porting rules (requirements §4.3)

- Map SpeexDSP types to exact Go widths (`spx_word16_t` to `int16`,
  `spx_word32_t` to `int32`, and so on). Port every fixed-point macro through
  `internal/fixed` with `fixed_generic.h` semantics.
- Floating-point initialisation code reproduces C's expression types exactly:
  float vs double, literal suffixes, promotions, truncating casts. Wrap products
  in explicit `float64(...)` or `float32(...)` to stop Go fusing multiply-adds
  on arm64.
- Every libm-derived table has an equality test against the harness.
- On a bit-exact mismatch, find the first diverging sample and state variable.
  Never add a tolerance to make it pass.
- Files ported or adapted from SpeexDSP or KISS FFT carry the derived-file
  header from `docs/contracts.md`.

## Code conventions

These rules, like the audio path rules, are for committed code. Temporary debug
code is fine while you work, and is removed before a commit.

- Write idiomatic Go, following
  [Effective Go](https://go.dev/doc/effective_go) and
  [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments). Prefer
  plain functions, concrete types and early returns over abstraction.
- Prefer the standard library over third-party packages. Add a third-party
  dependency only when the standard library cannot do the job and a small own
  implementation would be worse, and record why in an ADR. Only
  `golang.org/x/sys` is pre-approved.
- API shape: `Config` plus `New(cfg) (*T, error)`, caller-provided buffers,
  errors instead of panics, a small exported surface, low-level code in
  `internal/`.
- Samples are `int16` on the 16-bit scale. `float32` entry points use the same
  scale (not `[-1, 1]`). Conversions follow the reference exactly (requirements
  §3): `float32` input goes through the fixed-point `WORD2INT`, and NaN input
  becomes 0.
- Every streaming type has a `testing.AllocsPerRun` zero-allocation test.
- Package docs state rates, frame sizes, chunking, aliasing, retention, `Reset`,
  concurrency and allocation behaviour.
- Sizes are written as `64 * 1024`, and "never" or "unbounded" as `math.MaxInt`,
  not as bit shifts. Exception: fixed-point arithmetic, where shifts are the
  semantics of the reference macros (`SHL32`, `PSHR32`, Q15 scaling).

### Comments

- Comment only where a reader would otherwise get it wrong: a non-obvious
  ordering, a surprising default, a settled trade-off, a non-obvious upstream
  correspondence (cite the upstream file and line). Never restate the code, and
  never turn an explanation from chat or review into a comment.
- New and changed comments are two lines at most. Leave old comments alone
  unless asked.
- Required licence headers on derived files are exempt from the two-line limit.

## Writing

- No em-dashes, anywhere.
- Docs and commit messages state what is true now, not a change log.
- Tracked content never points at a document a reader cannot open, such as local
  notes, working documents or scratch files. Public specs and upstream sources
  are fine.
- Diagrams in tracked docs are Mermaid, one idea each, explained in the text
  beside them, and rendered once before a commit (for example in the VS Code
  Markdown preview). Only a byte or memory layout, which Mermaid cannot show, is
  drawn as text.
- Prose wraps at 80 visible columns, not counting link targets. A code span or
  link text never splits across lines. Tables and code blocks are not wrapped.

## Integrity

Never fabricate results, numbers or platform coverage. Cross-compiled is not
tested. No stubs, and no planned features presented as done. Never delete or
weaken failing tests. If a step was skipped or could not run (for example no
network or no compiler), say so in the review request.

## Environment

The C reference harness uses the host clang (the Xcode Command Line Tools
clang on macOS) or the pinned Linux container when Docker is available
(`docs/testing.md`, "Toolchains").
