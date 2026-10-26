# Acceptance criteria: `internal/fixed`

Pre-registered before the implementation, as requirements §9 demands. Changes
after the first implementation commit need a written justification in the
"Changes" section at the end of this file.

## Parity class

**Bit-exact** with SpeexDSP 1.2.1 (commit `1b28a0f6`), FIXED_POINT build, no
`FIXED_DEBUG`, generic macros (`fixed_generic.h`, no ARM or Blackfin
overrides), compiled by the reference harness with `-O2 -fwrapv
-ffp-contract=off`.

Every Go function must return, for every input in its tested domain, exactly
the value the C macro or inline function returns in the harness build. For a C
result of type `int` (the type of most macro expressions after integer
promotion), the comparison is against the full 32-bit C value, so a Go
function that narrows a result must be lossless over the tested domain.

## Operations covered

Every macro and inline function from `arch.h` (fixed-point branch),
`fixed_generic.h`, `math_approx.h` and `pseudofloat.h` that `resample.c`,
`kiss_fft.c`, `kiss_fftr.c`, `_kiss_fft_guts.h`, `fftwrap.c`, `filterbank.c`,
`preprocess.c` or `mdf.c` uses, directly or through another covered function.
The package doc of `internal/fixed` holds the list and the name mapping.

## Input sets

Each case is one operation over one input set:

1. **Exhaustive** when the domain has at most 2^24 points. This covers every
   unary operation on `int16` (all 65536 inputs) and every `int16` operation
   with a shift count.
2. **Edges plus random** otherwise: the Cartesian product of the per-argument
   edge lists, then at least 10^7 seeded random inputs. This covers every
   binary and ternary operation on wider domains, and every unary operation on
   `int32` or `uint32`, which additionally gets all 65536 `int16` values.

Edge lists:

| Argument | Edges |
|---|---|
| `int16` | -32768, -32767, -16385, -16384, -16383, -256, -2, -1, 0, 1, 2, 255, 256, 16383, 16384, 16385, 32766, 32767 |
| `int32` | the `int16` edges, plus -2^31, -2^31+1, -2^30-1, -2^30, -2^24, -65537, -65536, -65535, -32769, 32768, 65535, 65536, 65537, 2^24, 2^30, 2^31-2, 2^31-1 |
| `uint32` | 0, and 2^k-1, 2^k, 2^k+1 for k = 1..31, and 2^32-1 |
| shift count | every value in the operation's defined range |
| pseudo-float mantissa | the `int16` edges |
| pseudo-float exponent | -32768, -46, -31, -30, -16, -15, -14, -1, 0, 1, 14, 15, 16, 31, 32767, clipped to the operation's defined range |

Random inputs come from SplitMix64 with a fixed seed per case. Half of the
integer draws are uniform over the full width; the other half are masked to a
random bit width with a random sign, so small magnitudes are well covered. The
C driver and the Go test generate the same inputs; the manifest records the
SHA-256 of the input stream, and the Go test checks it before comparing
outputs.

`float32` and `float64` inputs for `WORD2INT` are written by the Go test into
the harness input directory, so that boundary values built with
`math.Nextafter` are exact on both sides.

### Excluded inputs

Inputs on which the C expression is undefined and Go would behave differently
or panic are excluded from the tested domain and documented in the package
doc:

- division by zero, and division of -2^31 by -1;
- shift counts outside the operation's width (`PSHR32` and `SATURATE32PSHR`
  are tested up to the count at which `1 << shift` and `a << shift` still fit
  in `int32`);
- pseudo-float operations that shift by `-e` are tested only for exponents
  that keep the shift count in [0, 31];
- `FLOAT_DIV32_FLOAT` and `FLOAT_DIV32` with a non-positive divisor
  (upstream: "Do NOT attempt to divide by a negative number");
- NaN input to `WORD2INT`. Go returns 0 for NaN (requirements §3); this is a
  Go-only test, not a parity test.

Left shifts of negative values and signed overflow inside the tested domains
stay in: `-fwrapv` and the generated code give them two's-complement results,
which Go matches. The `-fsanitize=undefined` run records them in
`docs/testing.md`.

## Constants

`QCONST16` and `QCONST32` are checked against every literal argument pair that
appears in `resample.c`, `preprocess.c`, `mdf.c` and `filterbank.c`, evaluated
by the C compiler at the call sites' types (with the `f` suffix where upstream
has it). Any literal whose result is out of range is reported in
`docs/testing.md` with the value the harness produced.

## Pass criteria

1. Every case matches: committed output prefix element by element, and the
   SHA-256 of every output chunk.
2. The canonical fixtures come from the pinned Linux container. The same
   driver built with the macOS host clang produces identical output hashes, or
   each difference is documented as a finding.
3. The `-fsanitize=undefined` build produces identical output hashes to the
   plain build. Its reports are listed in `docs/testing.md`.
4. The NaN policy tests pass for both `WORD2INT` float variants.
5. Every port of a `fixed_generic.h` or `arch.h` macro, and every pseudo-float
   operation without a loop, inlines (`-gcflags=-m`), checked by a test.
6. All functions are allocation-free (they take and return values only).

## Benchmark targets

No numeric targets. The macro ports must inline (criterion 5), so they cost no
call. `Sqrt`, `Exp`, `Atan`, `Cos`, `CosNorm` and the pseudo-float divisions
are benchmarked, and the results go into `docs/performance.md` for
information.

## Changes

### Criterion 5 narrowed (before any test or implementation commit)

Criterion 5 as first registered required every pseudo-float operation
without a loop to inline. The first `-gcflags=-m=2` build showed that the Go
inliner budget (80) admits every `fixed_generic.h` and `arch.h` port and
`FloatLT`, `FloatMult`, `FloatAMult`, `FloatShl`, `FloatExtract16`,
`FloatExtract32` and `FloatMul32`, but not `FloatAdd`, `FloatSub` (cost 156),
`FloatGT` (83), `FloatMul32U` (147), `FloatDiv32` (189), `FloatDiv32Float`
(138), `FloatSqrt` (101) or `PseudoFloat` (94). Getting them under the budget
would mean restructuring them away from the line-by-line correspondence with
`pseudofloat.h`, which is what makes the port reviewable. `mdf.c` calls them
a few times per frequency bin per frame, next to two FFTs per frame, so a
call costs little. Criterion 5 now reads:

5. Every port of a `fixed_generic.h` or `arch.h` macro inlines, and so do
   `FloatLT`, `FloatMult`, `FloatAMult`, `FloatShl`, `FloatExtract16`,
   `FloatExtract32` and `FloatMul32` (`-gcflags=-m`), checked by a test. The
   other pseudo-float operations are benchmarked (see "Benchmark targets").
