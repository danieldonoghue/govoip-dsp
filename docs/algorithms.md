# Algorithms

One section per component: the algorithm, the reference behaviour,
parameters, latency, numerical notes and known deviations. Each work package
edits only its own section.

## Fixed-point arithmetic (`internal/fixed`, WP0)

**Algorithm.** Ports of the SpeexDSP fixed-point macros (`arch.h`,
`fixed_generic.h`), the polynomial approximations (`math_approx.h`) and the
16-bit-mantissa pseudo-float (`pseudofloat.h`). The package doc lists every
function and its upstream name.

**Formats.** `Qn` means a value scaled by 2^n. The approximations take and
return:

| Function | Input | Output |
|---|---|---|
| `Sqrt` | Q(2n), any `int32` | Q(n), `int16` |
| `Cos` | angle in radians, Q13, upstream uses [0, π] | Q13 |
| `CosNorm` | x where the angle is π/2 · x/2^15; period 2^17 | Q15 |
| `Exp` | Q11 | Q16, saturating at 2^31-1 |
| `Atan` | non-negative Q15 | Q14 |
| `ILog2` | `uint32` | floor(log2 x), 0 for 0 |

`Float` is `M · 2^E` with a 16-bit mantissa that most operations normalise
to `|M|` in [16384, 32767]. It trades precision (about 14 bits) for range.

**Numerical notes.**

- Results have the C expression type after integer promotion. Truncation to
  16 bits happens only where C truncates, and callers write it explicitly.
- Signed overflow wraps, as with `-fwrapv`.
- `FloatSub` returns `b`, not `-b`, when `a` is zero. This is upstream
  behaviour and is kept.
- `FloatDivU` returns `FloatOne` for a non-positive divisor, as upstream,
  without upstream's warning on stderr.

**Deviations.**

- `Word2IntFloat32` and `Word2IntFloat64` return 0 for NaN; C is undefined.
- Where C is undefined (division by zero, oversized shift counts,
  out-of-range float to `int16` conversion) Go follows its own rules; see
  the package doc. `QConst16` saturates out-of-range results; no literal in
  the fixed-point build is out of range.

**Latency.** None; every function is pure.

## Fixed-point FFT (`internal/fft`, WP3)

**Algorithm.** A real FFT of `N` samples, computed as a complex KISS FFT of
`N/2` points on the even and odd samples packed as real and imaginary
parts, followed by a split step with "super twiddles" (`kiss_fftr2`); the
inverse reverses the split (`kiss_fftri2`). The complex FFT is mixed-radix,
decimation in time: `kf_factor` takes radix 4 first, then 2, then odd
factors, and the generic butterfly handles radices 7 to 17. `spx_fft` wraps
the forward transform with a range-maximising shift.

**Reference behaviour.** `spx_fft`, `spx_ifft` and `spx_fft_init` of
`fftwrap.c` (FIXED_POINT, USE_KISS_FFT). `mdf.c` and `preprocess.c` call
them on `2*frame_size` samples; the MVP sizes are 160, 320, 640, 960, 1280
and 1920. `kiss_fftr`, `kiss_fftri`, `spx_fft_float` and `spx_ifft_float`
are not ported: no upstream component calls them in the fixed-point build.

**Layout.** Half-complex: `X[0]` = DC, `X[2k-1]` and `X[2k]` the real and
imaginary parts of bin `k` for `k` in 1 to `N/2-1`, `X[N-1]` = Nyquist. The
forward transform uses `exp(-2πi·jk/N)`.

**Scaling.**

1. `maximize_range` shifts the input left by the largest `s` that keeps its
   peak at most 32000 (`s` = 0 for silence or a peak above 16000).
2. Every forward butterfly stage divides by its radix (radix 2 and 4 by
   shifts with rounding, others by multiplying by `32767/p` in Q15), and the
   split step halves once more, so the transform is scaled by `1/N`.
3. `renorm_range` shifts output and input right by `s` with rounding.
4. The inverse is unscaled and has no range shift, so
   `Inverse(Forward(x))` approximates `x`.

**Twiddles.** In the fixed-point build the twiddles come from the integer
`spx_cos_norm` (`kf_cexp2`), not from libm: the phase is
`(±i << 17) / nfft` for the complex FFT and `(±(i + nfft/2) << 16) / nfft`
for the super twiddles, in units where 2^17 is a full turn. The
floating-point `KISS_FFT_COS`/`KISS_FFT_SIN` macros are compiled out. The
tables are still compared entry by entry with the harness.

**Numerical notes.**

- Arithmetic is 16-bit with 32-bit products and wraps on overflow, as the
  reference does under `-fwrapv`. The inverse can wrap: a full-scale
  impulse (32767 at index 0) comes back wrapped at sizes 160, 320, 640 and
  1280 (as -32736 at size 160), and as 32640 at 960 and 1920.
- A -32768 input sample wraps `-in[i]` in `maximize_range`. If -32768 is
  the last sample to set the peak, the peak wraps and the shift loop wraps
  it to 0 after one step, so the input is shifted by one with wrapping, and
  comes back changed (32767 becomes -1). The port keeps this.
- Round trip `Inverse(Forward(x)) - x`, measured by `TestRoundTrip`
  (`go test ./internal/fft -run TestRoundTrip -v`, 2026-10-26):

| Size | Full-range noise, max / RMS | Sine 16000, max / RMS | Sine 3, max / RMS |
|---|---|---|---|
| 160 | 32 / 9.5 | 6 / 2.8 | 3 / 1.5 |
| 320 | 48 / 13.6 | 6 / 2.7 | 2 / 1.3 |
| 640 | 106 / 18.8 | 6 / 2.8 | 3 / 1.5 |
| 960 | 232 / 23.4 | 6 / 2.5 | 5 / 1.6 |
| 1280 | 290 / 27.8 | 6 / 2.7 | 2 / 1.3 |
| 1920 | 392 / 32.5 | 6 / 2.5 | 3 / 1.4 |

  The noise error grows with `N` because full-range noise leaves no room
  for the range shift, so each stage's division loses bits. The sines use
  `testutil.Sine` at 1000 Hz and a 16 kHz rate.

**Deviations.** None in output. `New` returns an error where the reference
returns NULL (odd sizes) or aborts (`speex_fatal` for a radix above 17); it
also rejects sizes above 4096, which no MVP component needs.

**Latency.** None; each call transforms one buffer.
