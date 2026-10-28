// Package fft ports the fixed-point real FFT that SpeexDSP 1.2.1 uses in
// its echo canceller and preprocessor: spx_fft_init, spx_fft and spx_ifft of
// fftwrap.c (FIXED_POINT, USE_KISS_FFT), with kiss_fftr2 and kiss_fftri2 of
// kiss_fftr.c and kiss_fft of kiss_fft.c beneath them.
//
// Parity: bit-exact with SpeexDSP 1.2.1 spx_fft and spx_ifft (FIXED_POINT)
// for every supported size, including the contents spx_fft leaves in its
// input buffer and the twiddle tables (testdata/speexdsp/fft).
//
// # Sizes
//
// Config.Size is the number of real samples: even, 2 to MaxSize, with no
// prime factor above 17 in Size/2 (Supported). The reference refuses odd
// sizes and aborts on a larger radix. mdf.c and preprocess.c both transform
// 2*frame_size samples, so the MVP frame sizes of 10 ms and 20 ms at 8, 16,
// 32 and 48 kHz need 160, 320, 640, 960, 1280 and 1920.
//
// # Layout and scaling
//
// Forward writes the half-complex spectrum out[0] = DC, then for each bin k
// in 1 to Size/2-1 the real part at out[2k-1] and the imaginary part at
// out[2k], then out[Size-1] = Nyquist; the sign of the exponent is negative.
// Forward scales by 1/Size: every butterfly stage divides by its radix.
// Before transforming, it shifts the input left by the largest count that
// keeps its peak at most 32000, and afterwards shifts input and output
// right by the same count with rounding (maximize_range, renorm_range).
// Inverse reads the same layout, is unscaled, and has no range shift, so
// Inverse(Forward(x)) approximates x; docs/algorithms.md gives the measured
// error. Arithmetic wraps on overflow as the reference does with -fwrapv.
//
// The reference's own quirk is kept: a -32768 input sample wraps the peak
// computed by maximize_range, which can leave the input changed after
// Forward and the output computed from a wrapped input.
//
// # Buffers, aliasing and state
//
// Forward and Inverse take caller slices of exactly Size samples and return
// ErrBufferSize, touching nothing, for any other length. On a zero FFT (not
// made by New) they return ErrConfig and Size returns 0. Forward rewrites
// its input as described above; Inverse leaves its input unchanged. Input
// and output must not overlap. Neither retains a slice. No state carries
// from one call to the next, so there is no Reset.
//
// # Concurrency and allocation
//
// An FFT is not safe for concurrent use, because the transforms share
// scratch buffers. Use one per stream. New allocates; no other method
// allocates. Work per call is proportional to Size times the sum of the
// radices of Size/2 (each at most 17).
package fft
