# Acceptance criteria: `internal/fft`

Pre-registered before the implementation, as requirements §9 demands. Changes
after the first implementation commit need a written justification in the
"Changes" section at the end of this file.

## Parity class

**Bit-exact** with SpeexDSP 1.2.1 (commit `1b28a0f6`), FIXED_POINT build,
`USE_KISS_FFT`, no SIMD, compiled by the reference harness with `-O2 -fwrapv
-ffp-contract=off`.

The reference entry points are `spx_fft_init`, `spx_fft` and `spx_ifft` of
`fftwrap.c`, which call `kiss_fftr_alloc`, `kiss_fftr2` and `kiss_fftri2` of
`kiss_fftr.c`, and through them `kiss_fft_alloc` and `kiss_fft` of
`kiss_fft.c`. These are the only FFT functions that `mdf.c` and
`preprocess.c` call; `filterbank.c` calls none. The Go port must give, for
every supported size and every input:

1. the same forward output as `spx_fft`, element by element;
2. the same contents of the input buffer after the forward call as `spx_fft`
   leaves there (it scales the input up and back in place, which is not
   always the identity);
3. the same inverse output as `spx_ifft`, element by element;
4. the same twiddle tables as `kiss_fft_alloc` and `kiss_fftr_alloc` build,
   for the forward and the inverse state.

## Supported sizes

Every even size `N` from 2 to 4096 for which `N/2` has no prime factor above
17. Other sizes make the reference fail (`kiss_fftr_alloc` refuses an odd
size, and `kf_bfly_generic` calls `speex_fatal` for a radix above 17); the Go
constructor returns an error for them and for sizes outside the range.

The MVP sizes are the window sizes that `mdf.c` (`2*frame_size`) and
`preprocess.c` (`2*frame_size`) use for 10 ms and 20 ms frames at 8, 16, 32
and 48 kHz: 160, 320, 640, 960, 1280 and 1920.

## Input sets

**Per MVP size**, one case per signal. Each input is used as the forward
input, and also, read as a half-complex spectrum, as an inverse input. The
forward output is also fed to the inverse (the round trip). Signals:

1. silence;
2. impulse of 32767 at index 0;
3. impulse of -32768 at index `N/2-1`;
4. sine of amplitude 16000;
5. sine of amplitude 3 (exercises large range-maximising shifts);
6. white noise over the full `int16` range;
7. full-scale square wave alternating between 32767 and -32768;
8. every sample -32768 (the wrap in `maximize_range`);
9. constant 32767;
10. impulse of 16000 at index 1 and
11. impulse of 16001 at index 1 (the two sides of the `maximize_range`
    bound);
12. the white noise of item 6 with its last sample set to -32768 (the wrap
    that reaches the shift loop).

Sines come from `testutil.Sine` and are written by the Go test into the
harness input directory; every input is recorded as a stream, so a generator
mismatch shows as an input mismatch.

**Sweep over every supported size**: for each size in increasing order, the
twiddle tables, the forward transform of SplitMix64 noise over the full
`int16` range, the input after the call, and the inverse transform of an
independent noise vector. Each quantity is one stream concatenated over all
sizes and checked through chunk hashes.

**Twiddles per MVP size**: the four twiddle tables as a committed stream.

## Pass criteria

1. Every case matches: committed prefixes element by element, and the
   SHA-256 of every chunk of every stream.
2. The canonical fixtures come from the pinned Linux container. The host
   clang build gives identical hashes, or each difference is documented as a
   finding in `docs/testing.md`.
3. The `-fsanitize=undefined` build gives identical outputs to the plain
   build; its reports are listed in `docs/testing.md`.
4. `New` returns an error, and does not panic, for every unsupported size in
   [-1, 4100], and for a few large and negative sizes.
5. `Forward` and `Inverse` return `ErrBufferSize`, writing nothing, when
   either slice length differs from `N`, and never panic: a fuzz test over
   sizes and contents runs without failure.
6. `Forward` and `Inverse` do not allocate (`testutil.AssertNoAllocs`), and
   carry no state from one call to the next: a call gives the same result
   whatever calls came before it, which a test checks.
7. Every division the port makes has a divisor that is nonzero for every
   input, shown from the code and tested at the closest boundary (`N = 2`).

## Round trip

`Inverse(Forward(x))` is documented, not tolerance-tested: the fixed-point
scaling defines it, and its exact values are covered by the bit-exact round
trip streams above. `docs/algorithms.md` states the scaling of each
direction and the measured round-trip error for the MVP sizes.

## Benchmark targets

No numeric targets. `Forward` and `Inverse` are benchmarked at every MVP size
and the results go into `docs/performance.md`.

## Changes

### Signals 10 to 12 added (after the first implementation commit)

Mutation checks on the first implementation showed three changes to
`maximize_range` that no input caught: a bound of 32767 instead of 32000, the
comparison `<` instead of `<=`, and a shift loop that does not wrap. The
original nine signals never put the peak in (16000, 16383], and in the
-32768 signals the wrapped peak either is overwritten by a later sample or
meets inputs that round to zero either way. Signals 10 to 12 add those inputs.
This widens the tested domain and relaxes nothing.
