/* Reference driver for preprocess: a reachable zero divisor. A stand-in
 * speex_echo_get_residual returns a Go-crafted residual, so that after
 * silence a band's echo estimate wraps to -1 and compute_gain_floor
 * divides by zero (preprocess.c:140). Writes testdata/speexdsp/preprocess_zero/.
 *
 * harness-sources: filterbank.c fftwrap.c kiss_fft.c kiss_fftr.c
 * harness-inputs: ./preprocess
 *
 * Inputs (preprocess/zero_test.go): zero_input.raw (int16, frames*N) and
 * zero_residual.raw (int32, N), used on frame zeroEchoFrame only. */
#include "config.h"

#include "../speexdsp/libspeexdsp/preprocess.c"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "harness.h"

#define RATE 16000
#define N 160
#define ECHO_FRAME 300
#define BAND 18

static spx_int32_t *residual;

/* Replaces mdf.c, which this driver does not link. */
void speex_echo_get_residual(SpeexEchoState *st, spx_word32_t *Yout, int len)
{
	(void)st;
	memcpy(Yout, residual, len * sizeof *Yout);
}

int main(int argc, char **argv)
{
	if (argc != 3)
		hz_die("usage: %s <out-dir> <in-dir>", argv[0]);
	size_t count, nres;
	int16_t *x = hz_read_input(argv[2], "zero_input.raw", HZ_INT16, &count);
	residual = hz_read_input(argv[2], "zero_residual.raw", HZ_INT32, &nres);
	if (nres != N || count % N)
		hz_die("zero inputs: %zu residual values, %zu samples", nres, count);
	int frames = (int)(count / N);

	hz_manifest_open(argv[1], "preprocess_zero", "reference/drivers/preprocess_zero.c");
	SpeexPreprocessState *st = speex_preprocess_state_init(N, RATE);
	spx_int32_t on = 1;
	speex_preprocess_ctl(st, SPEEX_PREPROCESS_SET_VAD, &on);
	hz_case_begin("zero_divisor_16000_160");
	hz_stream *s_in = hz_stream_begin("in", HZ_INT16, 0);
	hz_stream *s_res = hz_stream_begin("residual", HZ_INT32, N);
	hz_stream *s_out = hz_stream_begin("out", HZ_INT16, count);
	hz_stream *s_vad = hz_stream_begin("vad", HZ_INT16, frames);
	hz_stream *s_prob = hz_stream_begin("prob", HZ_INT16, frames);
	hz_stream *s_band = hz_stream_begin("band", HZ_INT32, 4);
	for (int i = 0; i < N; i++)
		hz_put_i32(s_res, residual[i]);
	for (int f = 0; f < frames; f++) {
		spx_int16_t *fr = x + (size_t)f * N;
		for (int i = 0; i < N; i++)
			hz_put_i16(s_in, fr[i]);
		speex_preprocess_ctl(st, SPEEX_PREPROCESS_SET_ECHO_STATE,
		                     f == ECHO_FRAME ? (void *)1 : NULL);
		int v = speex_preprocess_run(st, fr);
		if (f == ECHO_FRAME) {
			/* The divisor 1 + PSHR32(noise,7) + echo and the result. */
			hz_put_i32(s_band, st->noise[N + BAND]);
			hz_put_i32(s_band, st->echo_noise[N + BAND]);
			hz_put_i32(s_band, 1 + PSHR32(st->noise[N + BAND], NOISE_SHIFT) + st->echo_noise[N + BAND]);
			hz_put_i32(s_band, st->gain_floor[N + BAND]);
		}
		for (int i = 0; i < N; i++)
			hz_put_i16(s_out, fr[i]);
		hz_put_i16(s_vad, v);
		hz_put_i16(s_prob, st->speech_prob);
	}
	char params[160];
	snprintf(params, sizeof params,
	         "{\"rate\": %d, \"frame_size\": %d, \"frames\": %d, \"echo_frame\": %d, \"band\": %d}",
	         RATE, N, frames, ECHO_FRAME, BAND);
	hz_case_end(params);
	speex_preprocess_state_destroy(st);
	hz_manifest_close();
	free(x);
	free(residual);
	return 0;
}
