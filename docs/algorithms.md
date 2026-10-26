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
factors, and the generic butterfly handles radices 7 to 17 (and radix 1
at size 2). `spx_fft` wraps
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

## Ring buffers (`buffer`, WP2)

**Algorithm.** Two fixed-capacity FIFOs of `int16` samples over one
preallocated slice, with no wasted slot.

- `Ring` (single owner) keeps the read position `r` and the count `n`. The
  write position is `r + n` modulo the capacity. `Write` copies into at most
  two segments; under `Overwrite` it first advances `r` past the oldest
  samples it must discard, and a write longer than the capacity keeps only
  its last `Cap` samples.
- `SPSC` keeps two indices, `w` (stored only by the producer) and `r`
  (stored only by the consumer), each in [0, 2·Cap). The count is
  `w − r` modulo 2·Cap, so `w == r` is empty and a count of `Cap` is full.
  The slot of index i is i modulo Cap. Each index is an `atomic.Uint32` on
  its own cache line.

**Reference behaviour.** Own design; no SpeexDSP code. SpeexDSP's
`buffer.c` (`speex_buffer_*`) was read as design input. It differs in
these ways:

| SpeexDSP `buffer.c` | `buffer` |
|---|---|
| Counts bytes | Counts `int16` samples |
| `speex_buffer_writezeros` writes silence | No counterpart; write a zero slice |
| Always overwrites the oldest data on overflow | `Ring` chooses `Reject` or `Overwrite` in `Config`; `SPSC` rejects |
| `speex_buffer_read` zero-fills a short read and returns the bytes it copied | `Read` returns the count and leaves the rest of the caller's slice untouched |
| No zero-copy access | Two-segment read and write views |
| `speex_buffer_resize` is unfinished upstream (FIXME comments) | Capacity is fixed; make a new buffer |
| Single owner only | `SPSC` for a hand-off between two goroutines |

**Parameters.** `Capacity` (1 to `MaxCapacity`, 2^29 samples, so the SPSC
index arithmetic, which reaches 3·Cap, fits a 32-bit `int`) and, for `Ring`,
`Overflow`. A free-running 32-bit sample counter was rejected: it wraps
after 2^32 samples (about 25 hours at 48 kHz), and with a capacity that is
not a power of two the slot mapping breaks at the wrap. Indices modulo 2·Cap
never wrap that way. 64-bit atomics were rejected because Go 1.26
implements them with a lock on `mips` and `mipsle`, and on `arm` before
ARMv7 (`internal/runtime/atomic`).

**SPSC overflow.** Overwriting the oldest samples would mean the producer
moving `r`, which the consumer also stores. That needs a compare-and-swap
loop or a lock, both forbidden on the audio path (requirements §16), so the
SPSC rejects the excess.

**Latency.** Exactly the samples stored; the buffers add no processing
delay.

### SPSC memory ordering

The argument uses the Go memory model
([go.dev/ref/mem](https://go.dev/ref/mem), "Atomic Values"): if atomic
operation B observes the value stored by atomic operation A, then A is
synchronized before B, and all atomic operations behave as if in one
sequentially consistent order. The race detector checks the same
happens-before relation.

Each side's steps, for `Write` and `Read` (the view methods split the same
steps across two calls: `WriteSlices` is P1 and P2, the caller's fill is P3,
`Commit` repeats P1 and does P4, clipped to the view so it needs no new `r`;
`ReadSlices` is C1 and C2, the caller's use of the view is C3, `Consume`
repeats C1 and C2 and does C4):

| Step | Producer (`Write`) | Step | Consumer (`Read`) |
|---|---|---|---|
| P1 | load `w` (its own index) | C1 | load `r` (its own index) |
| P2 | load `r`; free = Cap − count(`w`, `r`) | C2 | load `w`; available = count(`w`, `r`) |
| P3 | copy n ≤ free samples into the slots of [`w`, `w`+n) | C3 | copy m ≤ available samples out of the slots of [`r`, `r`+m) |
| P4 | store `w` + n | C4 | store `r` + m |

**Which store publishes what.**

1. The producer's store of `w` (P4) publishes the samples copied in P3. P3 is
   sequenced before P4, and a consumer load C2 that observes the new `w` is
   synchronized after P4, so P3 happens before the consumer's read C3 of
   those slots.
2. The consumer's store of `r` (C4) releases the slots read in C3 back to
   the producer. C3 is sequenced before C4, and a producer load P2 that
   observes the new `r` is synchronized after C4, so C3 happens before the
   producer's next write P3 into those slots.

**Interleavings at each index update.** The only shared accesses are P2
against C4, C2 against P4, and the slot accesses P3 and C3.

| Interleaving | What the other side sees | Why it is safe |
|---|---|---|
| C2 before P4 | the old `w`: fewer samples available | The consumer reads only [`r`, old `w`); P3 writes only [old `w`, old `w`+n), which is disjoint. The new samples are read on a later call. |
| C2 after P4 | the new `w` | Rule 1: the samples are complete before C3 reads them. |
| P2 before C4 | the old `r`: less free space | The producer writes only [`w`, old `r`+Cap); the consumer reads only [old `r`, `w`). Both ranges fit within one lap, so they are disjoint. The freed slots are used on a later call. |
| P2 after C4 | the new `r` | Rule 2: the consumer has finished copying out of the freed slots. |
| Full (count = Cap) | free = 0 | The producer writes nothing and stores nothing; the consumer is unaffected. |
| Empty (`w` = `r`) | available = 0 | The consumer reads nothing and stores nothing. |
| Wrap of a copy | no change | A copy that crosses the end of the slice is two `copy` calls inside P3 or C3, before the index store, so the rules above cover both segments. |
| Wrap of an index | an index in [0, 2·Cap) | Each index is reduced modulo 2·Cap in the store itself, so the other side never observes an out-of-range value. |

**Why count modulo 2·Cap is exact.** Each index only advances, so the
values one side observes of the other's index never go backwards. The
consumer's own `r` is current, and the `w` it observes lies between the
`w` it saw last (at least `r`) and the current `w`, which is at most
`r` + Cap because the producer never writes past the `r` it observed plus
Cap. So the true difference is in [0, Cap], and modulo 2·Cap, which exceeds
Cap, it is recovered exactly. The producer's argument is symmetric. With
indices modulo Cap instead, full and empty would both read as a count of 0;
the mutation checks in `docs/testing.md` show the tests catch that.

**Wait-freedom.** Every method except `Reset` (which needs both sides
quiescent and stores both indices) is at most two atomic loads, one atomic
store of the caller's own index and at most two `copy` calls, with no loop,
no lock and no read-modify-write atomic (`TestWaitFreeSource` checks the
source). Neither side ever waits for the other; a side that finds the
buffer full or empty returns 0 at once.

## Resampler (`resample`, WP1)

**Algorithm.** Bandlimited interpolation (Smith's method) with a
Kaiser-windowed sinc, as in SpeexDSP `resample.c`. The ratio in/out is
reduced to `num/den`. Each output sample is an inner product of `filt_len`
input samples with one phase of the filter. Two kernels exist, and
`update_filter` picks the one with the smaller table (`resample.c:647`):

- **Direct:** one table row per phase, `filt_len*den` entries. Chosen when
  `den` is small, for example 48k→16k (3:1) or 8k→16k (1:2).
- **Interpolated:** `filt_len*oversample+8` entries; four neighbouring
  table entries are blended with cubic coefficients from the fractional
  phase. Chosen for large `den`, for example 44.1k→48k (147:160).

**Parameters.** Quality q sets the base filter length (8, 16, 32, 48, 64,
80, 96, 128, 160, 192, 256 taps for q = 0 to 10), the table oversampling,
the Kaiser window and the bandwidth (0.83 to 0.975 of the lower Nyquist
frequency). For downsampling by a factor d, the filter is d times longer,
rounded up to a multiple of 8.

**Reference behaviour kept.**

- Rate and quality changes keep the stream: the history is moved, and a
  shorter filter leaves "magic" samples that later calls process first.
- `Reset` (`reset_mem`) clears only the first `channels*(filt_len-1)`
  memory samples, so channels after the first may keep history.
- With `den + (num mod den)` above 2^32, the fractional position wraps in
  `uint32` and the input position stops advancing (case
  `wrap_q*_frac_4294967294_4294967295`): the output grows without
  consuming input. Ratios that large are not useful; the port reproduces it.
- Constructor and rate-change limits come from `multiply_frac` and the
  `INT_MAX` size checks. Downsampling fails when `num` exceeds
  2^32/`base_length`, because `multiply_frac` scales the base length by
  `num` exactly: 20000001:20000000 fails at quality 10 and works at
  quality 0. Downsampling by more than about 2·10^6 to 4·10^6 fails at
  quality 10 (table size). Below those limits the filter can be very
  large: 4000000:1 at quality 10 builds a table of about 10^9 entries
  (2 GB), as C does.

**Numerical notes.**

- The sinc table is built in `float` and `double` as C does it, with Go's
  `math.Sin` for libm `sin`. Every table of the fixture matrix (652
  configurations, all qualities) equals the harness table entry for entry;
  see `docs/testing.md`, "libm findings".
- Accumulation is wrapping `int32`; output saturates in `SATURATE32PSHR`.
- The stopband levels below sit at about -55 to -70 dB for every quality
  from 1 up. An interpretation, not verified separately: the 16-bit
  (Q15) filter table and the 16-bit output limit the fixed-point build;
  a tone of amplitude 2048 at 12 to 20 kHz through 48k→16k at quality 10
  gives all-zero output.

**Deviations.** `SetRateFrac` checks for overflow before changing state
(C changes part of it); offered lengths are clamped to 2^31-1 frames;
after a rejected rate change the input position saturates at 2^31-1
where C's overflows; NaN `float32` input becomes 0; there is no
`NULL`-input form. See the package
doc and `docs/acceptance/resample.md`.

**Measurements.** From the Go implementation, which is bit-exact with the
reference, so these are the reference's figures. Mac, 2026-10-26:

```sh
go test ./resample -run TestMeasure -v -args -measure
```

Latency: `InputLatency`, `OutputLatency`, and the measured output position
of the peak response to an impulse at input sample 0. The measured peak
equals `OutputLatency` in every case.

| q | 48k→16k | 16k→48k | 44.1k→48k | 8k→16k |
|---|---|---|---|---|
| 0 | 12, 4, 4 | 4, 12, 12 | 4, 4, 4 | 4, 8, 8 |
| 1 | 24, 8, 8 | 8, 24, 24 | 8, 9, 9 | 8, 16, 16 |
| 2 | 48, 16, 16 | 16, 48, 48 | 16, 17, 17 | 16, 32, 32 |
| 3 | 72, 24, 24 | 24, 72, 72 | 24, 26, 26 | 24, 48, 48 |
| 4 | 96, 32, 32 | 32, 96, 96 | 32, 35, 35 | 32, 64, 64 |
| 5 | 120, 40, 40 | 40, 120, 120 | 40, 44, 44 | 40, 80, 80 |
| 6 | 144, 48, 48 | 48, 144, 144 | 48, 52, 52 | 48, 96, 96 |
| 7 | 192, 64, 64 | 64, 192, 192 | 64, 70, 70 | 64, 128, 128 |
| 8 | 240, 80, 80 | 80, 240, 240 | 80, 87, 87 | 80, 160, 160 |
| 9 | 288, 96, 96 | 96, 288, 288 | 96, 104, 104 | 96, 192, 192 |
| 10 | 384, 128, 128 | 128, 384, 384 | 128, 139, 139 | 128, 256, 256 |

Passband, with f_N the lower Nyquist frequency and tones of amplitude 8192:
ripple up to 0.5 f_N (dB), the -3 dB edge (fraction of f_N; "> 0.95"
means above the highest tone measured), and the gain at 0.8 f_N and
0.9 f_N (dB).

| q | 48k→16k | 16k→48k | 44.1k→48k | 8k→16k |
|---|---|---|---|---|
| 0 | 0.291, 0.721, -5.0, -8.8 | 0.181, 0.751, -4.2, -7.5 | 0.185, 0.750, -4.2, -7.6 | 0.182, 0.751, -4.2, -7.5 |
| 1 | 0.008, 0.795, -3.2, -10.2 | 0.006, 0.825, -2.0, -7.5 | 0.006, 0.825, -2.0, -7.6 | 0.006, 0.825, -2.0, -7.5 |
| 2 | 0.004, 0.854, -0.3, -8.9 | 0.002, 0.882, -0.0, -4.7 | 0.004, 0.882, -0.1, -4.8 | 0.003, 0.882, -0.0, -4.7 |
| 3 | 0.006, 0.874, -0.0, -7.0 | 0.001, 0.896, -0.0, -3.4 | 0.001, 0.896, -0.0, -3.5 | 0.002, 0.896, 0.0, -3.4 |
| 4 | 0.005, 0.905, 0.0, -2.3 | 0.001, 0.924, -0.0, -0.7 | 0.001, 0.924, -0.0, -0.7 | 0.002, 0.924, -0.0, -0.7 |
| 5 | 0.004, 0.907, -0.0, -1.9 | 0.001, 0.925, -0.0, -0.5 | 0.001, 0.925, -0.0, -0.5 | 0.001, 0.925, -0.0, -0.5 |
| 6 | 0.008, 0.927, -0.0, -0.2 | 0.002, 0.933, -0.0, -0.1 | 0.001, 0.933, -0.0, -0.1 | 0.002, 0.933, 0.0, -0.1 |
| 7 | 0.005, 0.940, -0.0, -0.0 | 0.002, 0.940, -0.0, -0.0 | 0.001, 0.940, -0.0, -0.0 | 0.002, 0.940, -0.0, -0.0 |
| 8 | 0.006, > 0.95, -0.0, -0.0 | 0.002, > 0.95, -0.0, -0.0 | 0.001, > 0.95, -0.0, -0.0 | 0.003, > 0.95, -0.0, -0.0 |
| 9 | 0.006, > 0.95, -0.0, -0.0 | 0.003, > 0.95, -0.0, 0.0 | 0.001, > 0.95, -0.0, -0.0 | 0.002, > 0.95, -0.0, -0.0 |
| 10 | 0.008, > 0.95, -0.0, 0.0 | 0.002, > 0.95, -0.0, -0.0 | 0.001, > 0.95, -0.0, -0.0 | 0.003, > 0.95, -0.0, -0.0 |

Stopband (downsampling): the highest output level relative to the input
tone (dB), over tones above 1.1 f_N, 1.25 f_N and 1.5 f_N, where f_N is
the output Nyquist frequency. The level includes the output's own
rounding.

| q | 48k→16k | 48k→8k |
|---|---|---|
| 0 | -22.1, -43.0, -61.7 | -22.1, -42.8, -61.6 |
| 1 | -62.6, -66.0, -68.6 | -61.0, -67.0, -67.0 |
| 2 | -66.4, -70.5, -70.9 | -66.2, -66.2, -66.2 |
| 3 | -68.2, -68.2, -68.2 | -63.6, -63.6, -63.6 |
| 4 | -69.0, -69.0, -69.0 | -61.1, -61.1, -61.1 |
| 5 | -64.3, -64.3, -64.3 | -60.7, -60.7, -60.7 |
| 6 | -65.3, -65.3, -65.3 | -57.9, -57.9, -57.9 |
| 7 | -64.4, -64.4, -64.4 | -62.3, -62.3, -62.3 |
| 8 | -61.0, -61.0, -61.0 | -59.8, -59.8, -59.8 |
| 9 | -61.1, -61.1, -61.1 | -55.7, -55.7, -55.7 |
| 10 | -66.0, -66.0, -66.0 | -63.7, -63.7, -63.7 |

Imaging (upsampling): the highest residual after removing the fitted tone
(images and noise), relative to the tone (dB), over tones up to 0.9 f_N,
where f_N is the input Nyquist frequency.

| q | 16k→48k | 8k→16k |
|---|---|---|
| 0 | -11.9 | -11.9 |
| 1 | -39.9 | -39.9 |
| 2 | -66.3 | -65.1 |
| 3 | -76.6 | -77.9 |
| 4 | -72.3 | -71.7 |
| 5 | -74.3 | -72.9 |
| 6 | -74.2 | -71.7 |
| 7 | -71.1 | -73.8 |
| 8 | -70.8 | -73.3 |
| 9 | -68.4 | -72.6 |
| 10 | -75.0 | -71.4 |
