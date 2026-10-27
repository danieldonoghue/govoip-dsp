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
| `Sqrt` | Q(2n), x in [0, 2^30); meaningless outside | Q(n), `int16` |
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
- Where C is undefined (division by zero, oversized shift counts, out-of-range
  float to `int16` conversion) Go follows its own rules; see the package doc.
  The exceptions follow the compiled reference: the `...OrZero` divisions return
  0 (owner decision 13), and the `...Masked` functions take shift counts modulo
  32 (owner decision 18). `QConst16` saturates out-of-range results; no literal
  in the fixed-point build is out of range.

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

**Dropping without reading.** After a stall the consumer can skip its
backlog with `Consume(n)`, taking no read view first, or keep only the
newest k samples with `Consume(Len() - k)`. Such a drop is C1, C2 and C4
with no C3: `Consume` loads `r` and `w`, clips n to count(`w`, `r`), and
stores `r` + n.

1. No slot is read, so there is no read for rule 2 to order. The store of
   `r` still releases the dropped slots, and the producer may write into
   them as soon as its P2 observes the new `r`. The consumer never touches
   them again. A read view taken before the drop expires at `Consume`, and
   the caller must not use it afterwards: `TestSPSCDropExpiresReadView`
   shows the producer overwriting it.
2. The clip is what keeps the drop safe: `r` + n never passes the `w` that
   C2 observed, and every slot below that `w` was published by rule 1. If
   `r` could pass it, the consumer would skip slots the producer has yet to
   write, and its next count would be wrong; the mutation checks in
   `docs/testing.md` show the tests catch this.
3. In the keep-newest form, `Len` and `Consume` each load `w`. If the
   producer publishes between them, the drop still removes only `Len() - k`
   of the oldest samples, so at least the newest k remain, plus whatever
   arrived in between.

| Interleaving (drop) | What the other side sees | Why it is safe |
|---|---|---|
| C2 before P4 | the old `w` | The drop stops at the old `w`; the samples P3 is writing stay unread and are kept. |
| C2 after P4 | the new `w` | The drop may include the new samples. They were complete (rule 1) and are released unread. |
| P2 before C4 | the old `r`: less free space | The producer writes only [`w`, old `r`+Cap), which excludes the slots being dropped. |
| P2 after C4 | the new `r` | The producer reuses the dropped slots; the consumer neither reads them nor holds a view of them. |

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
  quality 10 (table size). Below those limits C can build very large
  filters: 4000000:1 at quality 10 needs a table of about 10^9 entries
  (2 GB). The Go port rejects those with its own cap (below).

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

**Deviations.** See the package doc and `docs/acceptance/resample.md`.

- `SetRateFrac` checks for overflow before changing state; C changes part
  of it.
- Offered lengths are clamped to 2^31-1 frames.
- After a rejected rate change the input position saturates at 2^31-1
  where C's overflows. With a ratio of 2^31 or more, the count of the
  zeros produced can then differ from the `-fwrapv` reference.
- Resource cap: a configuration whose sinc table plus all channels'
  history needs more than 16 × 1024 × 1024 entries, or more than 256
  channels, returns `ErrAllocFailed` with no state change. The largest
  supported need (96k→8k, quality 10, 32 channels) is 106464 entries, so
  the cap is about 158 times that. C accepts filters up to about 10^9
  entries. A filter length that wraps to 0 in `uint32` (quality 0, for
  example 1073741823:2; undefined in C) is rejected the same way.
- NaN `float32` input becomes 0; there is no `NULL`-input form.

**Extension: `OutLen`.** SpeexDSP has no way to ask how much output a call
needs. Each kernel writes one output per position `L + floor((s + k·num)/den)`,
k = 0, 1, …, that lies below the input it holds, where `L` is `last_sample`, `s`
is `samp_frac_num` and `num/den` the reduced ratio; the loop's integer and
fractional steps add up to exactly that. The magic samples, processed first, and
the offered input form one span of `t = magic + n` frames, so a call can write
`ceil(((t - L)·den - s)/num)` frames (0 if `t <= L`), per channel. `OutLen`
returns the largest of these over the channels, plus 1 when `n > 0`: a call
stops as soon as its output is full, so with exactly the count it can stop
before a later internal input chunk (`mem_alloc_size - (filt_len - 1)` samples,
160 by default), or before the input after the magic samples, even when those
would write nothing. The extra frame keeps the output from filling. The count is
exact, so the slack is 1 (measured over 671 configurations and the event and
fuzz tests), except that a `float32` call offered no input writes nothing, as
upstream `process_float` does, and the zero-output state can write fewer after
the input position saturates. Where `den - 1 + (num mod den)` reaches 2^32, the
fractional position can wrap and the output is not bounded by the input, so
`OutLen` returns 2^31-1. The result is clamped to 2^31-1 in every case, the
largest length a call accepts; above that the guarantee does not hold, for
example for 1:1000 with 3·10^6 input frames, or 1:4000000000 with one. In the
zero-output state after a rejected change with a ratio of 2^31 or more, the
saturated position could in theory allow one more output than counted if more
than about 2^31 input frames were offered at once; this was analysed by review,
not run.

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

## Preprocessor (`preprocess`, WP5)

**Algorithm.** Per frame of `N` samples: a `2N` window with 50% overlap
(square root of a raised-cosine, `conj_window`), a real FFT, the power
spectrum, and its projection onto 24 Bark bands (`filterbank.c`). A
minimum-statistics tracker (Cohen and Berdugo) decides per bin whether the
noise estimate may update. The a posteriori and a priori SNR (Ephraim and
Malah decision-directed) give a loudness-domain MMSE gain per band and per
bin, bounded below by a gain floor that mixes the noise and residual-echo
suppress levels. The per-frame speech probability `Pframe` comes from the
mean smoothed a priori SNR over the bands; it weights the gain and drives
the VAD. The output is the inverse FFT, windowed again and overlap-added.

**Reference behaviour.** `speex_preprocess_state_init`,
`speex_preprocess_run`, `speex_preprocess_estimate_update` and the
FIXED_POINT `speex_preprocess_ctl` requests of `preprocess.c`, with
`filterbank_new`, `filterbank_compute_bank32` and
`filterbank_compute_psd16` of `filterbank.c`. `ps_size` equals
`frame_size` (`preprocess.c:418`), so `N3 = N` and `N4 = 0`.

**Parameters.** Rates 4 to 65535 Hz, frame sizes 1 to 2048 with no prime
factor above 17, at any rate. `SupportedFrameSize` reports whether `New`
accepts a size, and `New` uses it; it accepts the 10 and 20 ms sizes at 8,
16, 32 and 48 kHz (80, 160, 320, 480, 640, 960). Defaults: denoise on,
VAD off, `prob_start` 35% (Q15 11469), `prob_continue` 20% (6554), noise
suppress -15 dB, echo suppress -40 dB, echo suppress while active -15 dB.
The getters for the thresholds truncate: the default `prob_start` reads
back as 34.

**Floating point and libm.** None in the fixed-point build. `toBARK` uses
the integer `spx_atan`; `toMEL` and the `exp` calls are in float-build
branches only. Checked by compiling `preprocess.c` and `filterbank.c` with
the reference flags (host Apple clang): neither object references a libm
symbol, and the only instructions on floating-point registers are `fmov`
moves and two `fneg.4s` that build integer sign masks for `eor`. None is
float arithmetic. The window and filterbank tables are still compared entry by
entry with the harness for every fixture configuration and a rate sweep.

**Latency.** `N` samples: the output of a frame completes the overlap-add of
the previous one.

**Numerical notes.**

- Loud inputs overflow 32-bit arithmetic inside the reference:
  `SHL32(ps,NOISE_SHIFT)` (`preprocess.c:778`), the band sums in
  `filterbank_compute_bank32` (`filterbank.c:144`) and `SHL32(a,15)` in
  `DIV32_16_Q15` (`preprocess.c:139`). The port wraps as the reference does
  with `-fwrapv`; the fixtures exercise all three.
- `DIV32_16_Q8(a, b)` returns -32768 for `a = 128b - 1` with `b >= 512`
  (the quotient 32768 truncates), so `post` and `prior` can become large and
  negative from ordinary input. The port keeps this.
- The filterbank divisor `PSHR32(mel_interval,15)` is rounded (773 at
  8 kHz, 1138 at 48 kHz). Where it rounds down, the weight `val` can reach
  32768 at a band edge and wrap to a negative `int16`: one bin each at
  22050 and 44100 Hz with `N` = 2048, and at 463 of the supported rates
  with `N` = 64 (measured with the port). The port keeps this; the table
  tests and the rate sweep cover it.
- The VAD is the reference's "kludge": on stationary noise the speech
  probability stays above the 35% start threshold. Measured on 16 kHz,
  20 ms frames: 0% speech frames on silence, 100% on white noise at -30
  dBFS and on pink noise, with mean `SpeechProb` over the last 10 s of 40%
  (white, 20 s), 63% (pink, 20 s) and 76% (pink, 120 s). The 120 s pink
  noise case in the fixtures matches C, so this is the reference's
  behaviour.

**Divisions.** Contracts section 8 requires each divisor to be shown
nonzero. Shown nonzero, with the test that covers the closest boundary:

| Site | Divisor | Bound |
|---|---|---|
| `conj_window` | `2N` | at least 2 |
| `filterbank_new` | `sampling/2` | 2 |
| `SET_PROB_START`, `SET_PROB_CONTINUE` | 100 | 100 |
| `filterbank_new` | `2*len`, `banks-1`, `mel_interval` | at least 2, 23, and 16384 or more for rates of 4 Hz and up |
| `filterbank_new` (`filterbank.c:96`) | `EXTRACT16(PSHR32(mel_interval,15))` | nonzero exactly for rates of 4 Hz and up (`TestFilterbankDivisors`, every rate) |
| `beta` (`preprocess.c:740`) | `nb_adapt` | 1 to 20000 |
| `Pframe` | `nbands` | 24 |
| `qcurve` | `MAX16(x,1)`; `512 + ...` | at least 1; 512 to 20172 |
| `hypergeom_gain` | `SHR32(xx,3)` for `xx >= 20480`; `spx_sqrt(xx<<15 + 6711)` | 2560 to 32512 for every reachable `theta`; positive (`TestRunDivisors`, every reachable `theta`) |
| `preprocess.c:861` | `Q15_ONE-q` = `MULT16_16_Q15(Pframe,P1)` | at least 882 (`TestRunDivisors`, from the exhaustive `qcurve` range) |

Not shown nonzero, so the port uses `fixed.Div32x16OrZero` and
`fixed.PDiv32x16OrZero` there, which give 0 for a zero divisor as AArch64
`SDIV` does on the canonical reference (C is undefined):

| Site | Divisor | How it could be zero |
|---|---|---|
| `DIV32_16_Q8` (`preprocess.c:114`) | `tot_noise`, range-reduced, as `spx_word16_t` | `tot_noise` is 0, or wraps to a multiple of 65536 below -2^15, with `ps` or `old_ps` negative |
| `DIV32_16_Q15` (`preprocess.c:140`) | `old_ps + tot_noise`, or the gain-floor denominator | the same, with a negative dividend |
| `prior_ratio` (`preprocess.c:846`, `:887`) | `ADD16(prior, 256)` | `prior` = -256 |
| band `gain2` (`preprocess.c:862`) | `ADD16(256, tmp)` | `tmp` = -256, which needs `prior` < -256 |

Each needs a value that is nonnegative in normal operation to go negative,
which happens through the wraps above and the -32768 of `DIV32_16_Q8`.

- **Reached:** the gain-floor `DIV32_16_Q15`, through `RunEcho`. After
  silence a band's noise estimate is 0; a residual that wraps the band's
  echo estimate to -1 (three bins: two at 2^31-1, one solved) gives
  `DIV32_16_Q15(-1, 1 + 0 - 1)`. The case is in
  `testdata/speexdsp/preprocess_zero`: the sanitizer reports the division
  by zero at `preprocess.c:140`, and the canonical arm64 build gives gain
  floor 0 there, as the port does. On amd64 the C reference would trap.
  Residuals from a real echo canceller are bounded by Parseval and have
  not been shown to reach this.
- **Not seen otherwise:** the sanitizer reports no division by zero over
  the other 310 fixture cases. A counting build of the port found none in
  about 50 minutes of stress audio (edge signals, loud tones, bursts,
  random residuals) or in 528 000 fuzz executions, and the independent
  review found none in 1.28 million frames of random input checked
  against C.

The owner accepted this policy (owner decision 13 in `docs/status.md`).

**Sanity results** (`go test ./preprocess -run Sanity -v`, 2026-10-26),
reported, not thresholded:

- Noise only, after the first second, default -15 dB: white noise at -35
  dBFS is reduced by 13.3 to 14.1 dB, pink noise at -37 dBFS by 3.7 to 5.8
  dB, over the eight MVP configurations.
- Speech in white noise at 16 kHz, 10 and 20 ms frames: segmental SNR
  improves by 5.3 to 6.0 dB at 0 dB SNR, 4.9 to 5.1 dB at 5 dB and 4.0 to
  4.3 dB at 10 dB, for `SpeechLike` and for 62 s of LibriSpeech.
- With denoise off, the output matches the input delayed by one frame to
  39.6 to 54.5 dB SNR: the analysis and synthesis are not transparent.
- VAD: see the numerical notes above.

**Deviations.** None in output. `New` returns an error where the reference
would divide by zero (rates below 4 Hz), overflow (above 65535 Hz) or fail
in `spx_fft_init` (unsupported frame sizes). `Reset` has no reference
counterpart; it equals a new instance with the same settings.

## Automatic gain control (`agc`, WP4)

**Algorithm (design v2).** Own design; no SpeexDSP code. The ideas of the
level estimate and the gain limits come from the floating-point AGC of
SpeexDSP 1.2.1 (`preprocess.c:565-611`), reimplemented in integer
fixed-point arithmetic. A feed-forward AGC with a lookahead limiter:

1. The input is split into blocks of `K = SampleRate/1000` samples and
   frames of `10K` samples, counted from the start of the stream.
2. Each frame's RMS `r` comes from the sum of squares (`int64`), divided
   into a Q20 mean square, normalised to [2^26, 2^28) and passed to
   `fixed.Sqrt`, in Q12 (at most 0.23 % error from −102 dBFS up,
   `TestRMS`).
3. A frame is active when `r` is at or above `GateLevel` and every sample
   in it was passed with `speech = true`. Inactive frames do not change
   the level estimate and cannot raise the gain; the gain may still finish
   falling during them.
4. The level estimate `L` is a power mean of order 4: `E` is an
   exponential mean, over active frames, of `min(r, 2L)⁴`, with time
   constant `LevelTime`, and `L = E^(1/4)`. During the first `LevelTime` of
   active frames the weight is `1/n` for the `n`-th frame, so `E` is the
   plain mean of what has been heard; the first frame is clamped against
   the initial level, the target. `E` is a `fixed.Float` updated as
   `E − c·E + c·x`, and the fourth root is two `fixed.FloatSqrt` calls.
5. Consecutive active frames with energy above `20·L²` (+13 dB), one more
   than a 10 ms click can touch (3 at rates that are multiples of 1000, 4
   at 9999 Hz), set `E` to the 4th power of the smallest of their `r` and
   cut the gain at once. An inactive frame ends the run. `LevelTime/3` of
   active frames in a row below `L/2` (−6 dB) set `E` to the 4th power of
   the largest of their `r`.
6. The target gain is `TargetLevel / L`, clamped to `[MinGain, MaxGain]`.
   The AGC gain starts at 0 dB and moves towards the target gain by at most
   `RiseRate` dB/s up, on active frames only, and `FallRate` dB/s down, on
   any frame, except for the cut in item 5.
7. For each block, the limiter gain is the smallest of the AGC gain,
   `ceiling / peak` and the previous limiter gain released towards the AGC
   gain (20 ms time constant). The gain applied to block `j` ramps linearly
   between `min(e[j−1], e[j])` and `min(e[j], e[j+1])`, which needs one
   block of lookahead.

**Design choices.** Each was measured on the P4 LibriSpeech signal at
several active levels (P4 is registered at −34 dBFS only; the other levels
are reported, `TestAcceptanceLibriSpeech`).

- **Order 4 instead of Speex's 5.** Order 4 needs only squarings and two
  square roots, which `internal/fixed` has; a fifth root does not exist
  there. Order 4 gives P4 2.05 to 2.55 dB from −38 to −20 dBFS. Order 2
  gives less at quiet levels (1.60 dB at −38, 2.14 dB at −24) but 6.78 dB
  at −20 dBFS, because its lower level lets ordinary loud speech trigger
  the cut. Order 4 was kept for that robustness; no registered criterion
  separates the two (mutation check: not caught).
- **Cut threshold and run.** Speex cuts after one jump of 10 times the
  loudness. Here a run is needed: with 2 frames, a 10 ms click straddling
  two frames cut the gain by 32 dB (prototype), and frames that are not
  consecutive in time let clicks in pauses add up. The threshold is
  +13 dB: with +10 dB, P4 rose to 3.41, 3.50 and 5.39 dB at −27, −24 and
  −20 dBFS, with 1 to 3 cuts on ordinary speech; with +13 dB there are no
  cuts on speech, while the +20 dB steps still cut after 30 ms (T3 0.03 s).
- **Warm-up.** It makes the estimate start from the speech heard rather
  than from the target. Without it, S1 on `SpeechLike` is −27.0 dBFS
  instead of −25.4 dBFS; P3 and P4 pass either way (mutation check: caught
  only by `TestSpeechFlag`).
- **Sustained drop.** An order-4 mean needs about 18·`LevelTime` to fall
  20 dB. The reset after `LevelTime/3` of quieter frames gives C2 2.45 s.
  It fired 0 times on the P3 and P4 signals (counted in a probe).
- **Cautious start.** Speex grows an absolute maximum gain as speech is
  confirmed. Here the gain starts at 0 dB and rises only on active frames,
  at most `RiseRate` dB/s, which has a similar effect; R3 checks the bound,
  and the gain came within 0.002 dB of it.

**Parameters.** `DefaultConfig`: target −24 dBFS, ceiling −1 dBFS, gate
−50 dBFS, gain −12 to +24 dB, rise 12 dB/s, fall 40 dB/s (Speex's
defaults), `LevelTime` 3 s. On a prototype with the warm-up and a +10 dB
cut, P4 measured 3.91, 3.16, 2.81, 2.24 and 1.93 dB for `LevelTime` 1,
1.5, 2, 3 and 4 s (2 and 4 s with a longer drop window), and C2 1.78, 1.94
and 2.44 s for 1, 1.5 and 3 s; 3 s leaves margin on both. These were
measured on the P4 files themselves, so the P4 margin is in-sample.
"Target level" is the output RMS that a stationary signal above the gate
converges to. For speech, the AGC holds the order-4 power mean of active
frame RMS at the target, which lies above the active-speech RMS: measured
on LibriSpeech, the output's active-speech RMS is 1.6 dB below the target
(S1).

**Latency.** `2K` samples: 2 ms at rates that are multiples of 1000. The
first `2K` output samples of a stream are zero.

**Numerical notes.**

- Gains are Q15 in `int32` (up to 40 dB, 3,276,800), levels Q12 amplitudes.
  The output is `fixed.Mult16x32Q15(x, gain)`, which floors, so with a gain
  at most `ceiling/peak` no output magnitude exceeds the ceiling. A final
  `fixed.Saturate32` to ±32767 is a guard that the limiter makes
  unreachable (mutation check: removing it is not caught).
- Every division has a non-zero divisor: the block length (at least 8), the
  frame length (at least 80), the block peak (the zero case is a separate
  branch), and `L`, which is clamped to at least the gate (at least 13422
  in Q12). `TestConfigEdgeAccuracy` drives `L` to just above a −80 dBFS
  gate.
- The 4th power and its root use `fixed.Float` (15-bit normalised
  mantissa). Updating `E` as `E − c·E + c·x` keeps the truncation of
  `FloatMult` in the small terms: the steady-state level error on a sine is
  −0.024 dB at most for `LevelTime` 100 ms to 10 s (`TestLevelBias`;
  `(1 − c)·E + c·x` drifted to −0.20 dB at 10 s).
- The gain is kept in Q22, so the rate limits hold at low gains: at
  −36 dB with 1 dB/s, the worst measured rate is 1.02 dB/s, from Q15
  rounding of the per-frame factor (`TestLowGainRates`). The rise step is
  at least one Q22 unit. The limiter release rounds towards its target.
- `New` derives the constants with `math.Pow` and `math.Expm1`, rounded to
  integers; `TestConstants` pins them for one configuration.

**Known limitations.**

- **Noise protection is the level gate only.** The `speech` flag helps only if
  the caller's VAD rejects noise, and the VAD of the SpeexDSP 1.2.1 fixed-point
  preprocessor reports speech on every frame of stationary white and pink noise
  (section "Preprocessor" above). Stationary noise whose 10 ms frames reach the
  gate is therefore driven to the target: with `speech = true`, white or pink
  noise at −45 to −24 dBFS ends at −23.98 to −25.18 dBFS, with gains up to +21
  dB, and pink noise at −52 dBFS gets +23.86 dB (N5). Pink noise at −55 dBFS
  long-term, whose loudest frames just cross the −50 dBFS gate, reaches +23.16
  dB (N4, reported; design v1 reached +9.99 dB because it rose more slowly).
  Noise whose frames all stay below the gate keeps 0 dB. Raising `GateLevel`
  above the noise is the only protection. The owner accepted this
  (2026-10-26). The AGC belongs after `preprocess` with denoising on,
  which lowers stationary noise before the AGC sees it; callers set
  `GateLevel` above the residual noise. At the default suppression
  (−15 dB), `preprocess` reduced white noise at −35 dBFS by 13.3 to 14.1 dB
  but pink noise at −37 dBFS by only 3.7 to 5.8 dB (section
  "Preprocessor", sanity results), so the residual depends on the noise.
  WP7 measures the real chain.
- **A noise-floor safeguard was tried and rejected.** Allowing a rise only
  on frames more than 10 dB above a 1.5 s minimum-statistics floor failed
  C2 to C6, because a steady sine or white noise sits at its own floor,
  and did not help N4 at all (still +23.16 dB): the frame RMS of that pink
  noise spreads by 10 to 16 dB within every 1.5 s window, so its loud
  frames are always more than 10 dB above the raw minimum
  (`docs/acceptance/agc.md`, change 7). For white noise, a gain near 0 dB
  also contradicts C4 and C5, which require white noise passed with
  `speech = true` to be raised to the target. No criterion requires pink
  noise to be raised; there, 0 dB is ruled out by the level-based design,
  not by a criterion.
- In pauses the gain cannot rise, but it may finish falling, so the noise
  in a pause gets at most the gain of the speech before it.
- **Steps just below the cut threshold.** A step up of +14 dB or more
  cuts at once (0.03 s to within 2 dB). Steps of +10 to +13 dB do not; the
  gain then follows the order-4 level estimate, about 10 dB in the first
  second (+13 dB step: 20.00, 18.95, 14.73 and 10.97 dB at 0, 0.1, 0.5 and
  1 s), not the 40 dB/s fall limit. They are within 2 dB of the target
  after 1.71, 1.83, 1.90 and 2.00 s, and more than 6 dB above it for 0.38,
  0.48, 0.57 and 0.67 s (measured on a sine from −44 dBFS). All four
  exceed the 1.0 s that C1 allows for the registered +20 dB step, and +11
  to +13 dB exceed T3's 0.45 s. It is the cost of the +13 dB threshold,
  which keeps ordinary speech from triggering cuts. The owner accepted it
  (2026-10-26); guards G5 (a +14 dB step cuts) and G6 (these times plus at
  most 0.05 s) pin it.
- **Small steps down.** A downward step smaller than 6 dB does not trigger
  the reset, so only the slow order-4 mean follows it. Measured on a sine:
  a −5 dB step takes 5.21 s to come within 2 dB of the target and 7.76 s to
  come within 1 dB, against 1.62 s and 1.70 s for −10 dB.
- **A click as the first frame.** The level is clamped to twice the target,
  but one loud frame still dominates the order-4 warm-up mean until the
  drop reset clears it. On 5 s of `SpeechLike` at an active RMS of
  −44 dBFS (seed `agc-lim`) after a 160-sample full-scale white-noise
  click (seed 71), the gain 1, 2, 3 and 4 s into the speech is +0.69,
  +3.15, +8.37 and +13.17 dB, against +4.56, +9.96, +16.81 and +19.41 dB
  without the click.
- **A loud start does not cut.** The warm-up mean rises by up to 6 dB per
  frame, so the third frame of a loud start is no longer 13 dB above the
  level. The gain falls from 0 dB at the fall limit instead: a sine
  starting at −12, −6 or −3 dBFS never cuts, and the gain is −4 dB after
  0.1 s and within 0.1 dB of −12 dB (MinGain) after 0.3 s.
- **Rates are those of the internal gain.** R1 and R2 check the Q22 gain;
  the applied Q15 gain moves in whole units, so at −40 dB a single frame
  can change it by 0.027 dB (2.6 dB/s for that frame at a 1 dB/s limit),
  while the average over a second stays within the limit.
- **Test oracle.** The R2 check reads the AGC's own level estimate and
  its gate decision, so a wrong level estimate would also move the check.
- Levels and gains are whole dB, rates whole dB/s.
- The limiter can hold a stationary signal below the target when its peaks
  at the target gain would exceed the ceiling (a sine needs
  `LimitLevel − TargetLevel ≥ 3 dB`).

**Design v1 (fallback).** The first design used a one-pole mean of frame
RMS with attack and release time constants (500 ms and 1500 ms), and no
rate limits. It met every criterion except P4 (3.64 dB). Its last code
state is the commit "agc: check the ceiling and exact gain limits in more
acceptance tests"; its measurements are in `docs/acceptance/agc.md`.

## Acoustic echo canceller (`echo`, WP6)

**Algorithm.** MDF, the multidelay block frequency-domain adaptive filter
(Soo and Pang, 1990), in the alternately updated variant (AUMDF), with the
variable learning rate of Valin (2007). Per frame of `F` samples, with
`N = 2F` and `M` = filter length / `F` blocks:

1. The microphone frame passes a DC notch and a pre-emphasis (0.9); the
   far-end frame gets the same pre-emphasis and joins the last frame in
   `x`, whose spectrum becomes block 0 of the far-end history `X`.
2. Two filters run on `X`: the foreground (16-bit weights, gives the
   output) and the background (32-bit weights, adapts). Their error
   energies decide whether the background is copied to the foreground or
   reset from it.
3. The background adapts with a per-bin step from the leak estimate (how
   much of the filter output is still in the error), the residual-to-error
   ratio and the smoothed far-end power. Before the filter has "adapted",
   a simpler step from the far-end to error energy ratio is used.
4. Each frame, block 0 and one other block are constrained to a causal
   response (two FFTs each): this is the AUMDF part.
5. The output is the foreground error with de-emphasis.

**Reference behaviour.** `mdf.c` with `FIXED_POINT` and `TWO_PATH`, one
microphone and one loudspeaker. Nine FFTs of `N` points per frame (seven
for `M = 1`).

**Supported rates.** 8 and 16 kHz only (owner decision, 2026-10-26).
Callers resample higher rates down first. The port accepts rates up to
48 kHz and is bit-exact there, but the two reference defects below make
the canceller weak at 32 and 48 kHz (see "Measured behaviour"). A
correction for high rates would be an extension outside the compatibility
path; none is built.

**Parameters.** Frame size, filter length (rounded up to `M` whole frames),
sampling rate. The rate sets `spec_average = F/rate`, `beta0 = 2F/rate`
and `beta_max = F/(2·rate)` (Q15) and the notch radius (0.9 below 12 kHz,
0.982 below 24 kHz, 0.992 above).

**16-bit rate truncation.** `DIV32_16` casts its divisor to
`spx_word16_t`, so the rate is truncated to 16 bits in those three
constants (`mdf.c:1233-1236`). At 44.1 kHz it becomes -21436 and at 48 kHz
-17536, which makes the constants negative:

| Rate, frame | `int16(rate)` | `spec_average` | `beta0` | `beta_max` | Intended |
|---|---|---|---|---|---|
| 32000, 320 | 32000 | 327 | 655 | 163 | same |
| 44100, 441 | -21436 | -674 | -1348 | -337 | 327, 655, 163 |
| 48000, 480 | -17536 | -896 | -1793 | -448 | 327, 655, 163 |
| 48000, 960 | -17536 | -1793 | -3587 | -896 | 655, 1310, 327 |

The port reproduces this, and the 48 kHz fixtures match it. It is upstream
behaviour of the fixed-point build, not a property of this port.

**Internal resets.** The reference resets itself (as
`speex_echo_state_reset`) when an energy sum is negative or the
foreground error is much larger than the input for 50 frames. The energy
sums are 32-bit (`mdf_inner_prod`). With frames of 480 samples or more
(10 ms at 48 kHz, 20 ms at 32 and 48 kHz) and speech peaks of 16000, the
error energy `See` and the echo-estimate energy `Syy` overflow, so the
reference resets every few seconds in ordinary scenarios (1 to 22 resets
per case in the fixtures; a temporary count over `farend` and `doubletalk`
at 32 and 48 kHz with 20 ms frames found `See` negative 43 times and `Syy`
14 times). At 8 and 16 kHz the resets occur only in the `diverge`
scenario. At 32 kHz with 10 ms frames there are neither resets nor a
truncated rate, yet `farend` reaches only 9 to 10 dB; that cause has not
been investigated.

**Shifts outside [0, 31].** In C a shift by 32 or more is undefined. The
sanitizer build reports such shifts in `FLOAT_EXTRACT32` (`pseudofloat.h:236`,
from `mdf.c:1108`, exponent 32 and below), when `Sey` is near 0 and `Syy`
is large. clang 19 (container) and Apple clang 21 (host) compile
`FLOAT_EXTRACT16`, `FLOAT_EXTRACT32` and `FLOAT_DIV32_FLOAT` in `mdf.c` to
scalar `LSLV`/`ASRV` on 32-bit registers, which take the count modulo 32,
so the port uses the masked variants of `internal/fixed` there. A
temporary count over all fixture cases found 53 frames with such an
exponent, 3 of them where the two semantics give a different comparison;
in all 3 the later clamp to `See/2` made the result equal, so the fixtures
do not tell the two apart, and the disassembly is the evidence.
`FLOAT_MUL32` in `weighted_spectral_mul_conj` differs between the two
toolchains: clang 19 vectorises it for the interior bins (NEON
`SSHL`/`USHL`, which saturate like Go's shifts) and keeps the edge bins
scalar (modulo 32), while Apple clang 21 keeps it all scalar. So for a
count outside [-31, 31] the C result depends on the compiler and the bin,
and no single Go behaviour matches both builds. The port uses the unmasked
`FloatMul32`, which equals clang 19's vector lanes. No fixture reaches
such a count (the sanitizer reports none at `pseudofloat.h:243`). The
owner accepted this on 2026-10-26: saturation is a documented deviation
where the two reference compilers disagree, and is unreachable in the
fixtures.

**Divisions.** Every division the port makes, and why its divisor is not 0:

| Site | Divisor | Why nonzero |
|---|---|---|
| `setRate` (`mdf.c:1233-1236`) | `int16(rate)` | `New` and `SetSampleRate` accept 8000 to 48000 Hz; none is a multiple of 65536 |
| window (`mdf.c:468`) | `int16(N)` | `N = 2F` in [2, 4096] |
| decay and `ss` (`mdf.c:482`, `:709`) | `int16(M)` | `New` keeps `M` in [1, 32767] |
| initial `prop` (`mdf.c:492`) | `sum` | at least `prop[0]` = 22937 |
| `mdf_adjust_prop` (`mdf.c:373`) | `prop_sum` | 1 plus non-negative terms, each below 32767, so in [1, 2^31) for `M` at most 32767 |
| `FLOAT_DIV32(tmp32, See)` (alpha, `RER`, `adapt_rate`) | `See` | floored at `N*100/64` >= 3 (`mdf.c:1040`) before every use |
| `FLOAT_DIV32(adapt_rate, power+10)` | `power[i]+10` | `power` stays >= 0: it starts at 0 and adds `Xf` >= 0, which is below 2^31 because the forward FFT is scaled by `1/N`, so `|X| <= 32767`; `TestDivisorInvariantsUnderExtremes` and `FuzzCanceller` check `power >= 0` and `Xf >= 0` after every frame |
| `FLOAT_DIV32_FLOAT(r, e*(power+10))` | mantissa of `FLOAT_MUL32U` | `e = 8*Rf+1` is odd, so not 0, and `power+10 >= 10`; `FLOAT_MUL32U` of two nonzero values has `|m| >= 8192`. A negative `e` (from `Rf >= 2^28`) gives a negative mantissa, which the masked division handles as the compiled reference does |
| `FLOAT_DIVU` | `b.m` | guarded in `internal/fixed`, as upstream: `b.m <= 0` returns 1 |

**Measured behaviour.** From `go test ./echo -run TestScenarioMeasurements`
(figures pinned in `echo/testdata/metrics.golden`, one line per case;
definitions in `docs/acceptance/echo.md`). Ranges are over the six frame
and filter combinations of each rate. ERLE and gains in dB, times in
seconds at 0.5 s resolution (convergence "0.0" means the first window
already reached 10 dB); -1 means not reached within the 8 s searched.

| Scenario | Figure | 8 kHz | 16 kHz | 32 kHz | 48 kHz |
|---|---|---|---|---|---|
| `farend` | steady-state ERLE | 34.6 to 37.8 | 30.3 to 35.3 | 4.7 to 10.0 | 6.4 to 18.9 |
| `farend` | convergence | 0.0 to 0.5 | 0.5 | 0.0 to 1.5 | 0.0 to 3.5 |
| `ir_short` | ERLE | 33.6 to 36.9 | 25.9 to 26.4 | 17.7 to 20.4 | 13.8 to 15.1 |
| `ir_long` | ERLE | 13.2 to 34.3 | 10.6 to 30.5 | 3.5 to 14.2 | 9.6 to 16.0 |
| `doubletalk` | ERLE before double talk | 29.6 to 33.5 | 23.7 to 30.8 | 10.1 to 18.7 | 11.0 to 17.0 |
| `doubletalk` | near-to-residual gain | 22.8 to 32.7 | 19.2 to 22.2 | 4.9 to 15.3 | 10.3 to 19.7 |
| `silence` | ERLE, last segment | 34.6 to 36.3 | 29.0 to 34.8 | 4.9 to 10.0 | 13.1 to 19.8 |
| `path_sudden` | ERLE before change | 32.5 to 36.5 | 27.2 to 34.8 | 8.8 to 16.6 | 12.2 to 19.0 |
| `path_sudden` | ERLE after change | 30.1 to 35.0 | 23.6 to 29.8 | 3.5 to 31.8 | 3.8 to 15.6 |
| `path_sudden` | reconvergence | 1.0 | 0.5 to 1.0 | 1.0 to 5.0 | -1 to 8.0 |
| `path_gradual` | ERLE after the fade | 31.4 to 34.2 | 24.2 to 29.5 | 13.5 to 34.1 | 11.8 to 31.4 |
| `misaligned_late` | ERLE | 0.5 to 1.0 | -0.1 to 0.0 | 0.1 to 0.3 | 0.3 to 0.6 |
| `misaligned_early` | ERLE | 0.8 to 1.3 | -0.1 to 0.1 | -0.4 to 0.1 | 0.4 to 0.7 |
| `diverge` | ERLE, last 4 s | 6.6 to 20.8 | 6.2 to 22.6 | 8.2 to 23.6 | 7.7 to 18.3 |
| `reset` | ERLE after reset | 23.8 to 33.2 | 24.8 to 30.1 | 5.5 to 10.1 | 8.2 to 17.8 |
| `rate_switch` | ERLE after switch | 29.3 to 32.1 | 29.8 to 35.1 | 7.1 to 10.5 | 14.2 to 17.7 |
| `capture` | ERLE | 0.5 to 1.3 | -0.2 to 1.1 | 0.1 to 0.5 | 0.4 to 1.2 |
| `nearend` | level change against `ref` | 0.0 | 0.0 | 0.0 | 0.0 |
| `nearend` | level change against the raw near end | -1.0 | -0.1 | -0.1 | -0.3 |
| `lownoise` | ERLE without threshold | 5.1 | 1.6 | 3.4 | 4.3 |

Notes on the figures:

- `misaligned_late` (echo beyond the filter) and `misaligned_early`
  (acausal echo) are near 0 dB, as the stream alignment rules predict.
- `capture` changes the queue on about 10% of frames (underruns and
  overruns) to cover those code paths, so the alignment keeps jumping and
  the filter does not converge. With the calls strictly alternating,
  `TestCaptureEqualsDelayedCancel` shows that `Capture` equals `Cancel`
  with the second-most-recent `Playback` frame, and measures 33.3 to 35.1
  dB at 8 and 16 kHz.
- `lownoise` (far end peak 200, about -44 dBFS) stays below the reference's
  adaptation threshold (`Sxx > N*1000/64`, `mdf.c:1153`): the filter never
  adapts, and the figure is the DC notch alone. Its echo is also below the
  -50 dBFS ERLE window threshold, so it has no pre-registered ERLE.
- `nearend` against `ref` is 0.0 by construction: `ref` is the same input
  without the mic noise, and with a silent far end the filter does
  nothing. The change against the raw near end is the DC notch.
- The 32 kHz figures are lower than the 48 kHz ones despite the 48 kHz
  constant truncation; the cause has not been investigated.

**Speech.** `TestSpeechScenario` (LibriSpeech dev-clean, 16 kHz; skips when
the files are absent) is reported in `docs/testing.md`.

**Deviations.** None in the fixtures. For `FLOAT_MUL32` shift counts
outside [-31, 31], which no fixture reaches, the port saturates as the
clang 19 vector lanes do; the Apple clang build and clang 19's edge bins
shift modulo 32 instead. The reference's warnings (`speex_warning`)
are not printed or reported. `New` rejects configurations for which the
reference would divide by zero or which exceed the documented limits.

**Latency.** None: each call returns the processed frame.
