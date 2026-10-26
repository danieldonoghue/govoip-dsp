/* Reference driver for internal/fft: runs spx_fft_init, spx_fft and spx_ifft
 * (fftwrap.c with USE_KISS_FFT) over the input sets pre-registered in
 * docs/acceptance/fft.md and writes testdata/speexdsp/fft/.
 *
 * harness-sources: fftwrap.c kiss_fft.c kiss_fftr.c
 * harness-inputs: ./internal/fft
 *
 * The size and signal tables, the sweep generator and the supported-size rule
 * must match internal/fft/reference_test.go; the input streams check this. */
#include "config.h"

#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "arch.h"
#include "fftwrap.h"
#include "kiss_fftr.h"
#include "_kiss_fft_guts.h"

#include "harness.h"

/* Private layouts of fftwrap.c:296 and kiss_fftr.c:23, mirrored to read the
 * twiddle tables. The upstream commit is pinned, so they cannot drift. */
struct kiss_config {
	kiss_fftr_cfg forward;
	kiss_fftr_cfg backward;
	int N;
};
struct kiss_fftr_state {
	kiss_fft_cfg substate;
	kiss_fft_cpx *tmpbuf;
	kiss_fft_cpx *super_twiddles;
};

#define MAX_SIZE 4096
#define MAX_RADIX 17

static const int mvp_sizes[] = {160, 320, 640, 960, 1280, 1920};
static const char *signals[] = {"silence", "impulse_first", "impulse_mid_neg",
                                "sine_16000", "sine_3", "noise", "square",
                                "min", "dc", "impulse_16000",
                                "impulse_16001", "noise_min_last"};
#define NSIZES (int)(sizeof(mvp_sizes) / sizeof(mvp_sizes[0]))
#define NSIGNALS (int)(sizeof(signals) / sizeof(signals[0]))

static uint64_t fnv1a(const char *s)
{
	uint64_t h = 0xcbf29ce484222325ULL;
	for (; *s; s++) {
		h ^= (unsigned char)*s;
		h *= 0x100000001b3ULL;
	}
	return h;
}

/* kf_factor's rule (kiss_fft.c:437): every radix of n must be at most 17. */
static int supported(int size)
{
	int n, p = 4;
	if (size < 2 || size > MAX_SIZE || size % 2)
		return 0;
	n = size / 2;
	do {
		while (n % p) {
			switch (p) {
			case 4: p = 2; break;
			case 2: p = 3; break;
			default: p += 2; break;
			}
			if (p > 32000 || (spx_int32_t)p * (spx_int32_t)p > n)
				p = n;
		}
		if (p > MAX_RADIX)
			return 0;
		n /= p;
	} while (n > 1);
	return 1;
}

static void put_cpx(hz_stream *s, const kiss_fft_cpx *c, int n)
{
	for (int i = 0; i < n; i++) {
		hz_put_i16(s, c[i].r);
		hz_put_i16(s, c[i].i);
	}
}

/* Forward and inverse complex twiddles, then forward and inverse super
 * twiddles, each N/2 complex values. */
static void put_twiddles(hz_stream *s, void *table, int n)
{
	struct kiss_config *t = table;
	put_cpx(s, t->forward->substate->twiddles, n / 2);
	put_cpx(s, t->backward->substate->twiddles, n / 2);
	put_cpx(s, t->forward->super_twiddles, n / 2);
	put_cpx(s, t->backward->super_twiddles, n / 2);
}

static void put_all(hz_stream *s, const spx_word16_t *x, int n)
{
	for (int i = 0; i < n; i++)
		hz_put_i16(s, x[i]);
}

static void mvp_cases(const char *in_dir)
{
	spx_word16_t in[MAX_SIZE], spec[MAX_SIZE], out[MAX_SIZE];
	char name[64], file[96];

	for (int z = 0; z < NSIZES; z++) {
		int n = mvp_sizes[z];
		void *table = spx_fft_init(n);

		snprintf(name, sizeof name, "twiddles_%d", n);
		hz_case_begin(name);
		put_twiddles(hz_stream_begin("twiddles", HZ_INT16, 4 * n), table, n);
		snprintf(file, sizeof file, "{\"size\": %d}", n);
		hz_case_end(file);

		for (int g = 0; g < NSIGNALS; g++) {
			size_t count;
			snprintf(name, sizeof name, "%d_%s", n, signals[g]);
			snprintf(file, sizeof file, "%s.in.raw", name);
			int16_t *x = hz_read_input(in_dir, file, HZ_INT16, &count);
			if (count != (size_t)n)
				hz_die("%s: %zu samples, want %d", file, count, n);

			hz_case_begin(name);
			hz_stream *s_in = hz_stream_begin("in", HZ_INT16, n);
			hz_stream *s_fwd = hz_stream_begin("fwd", HZ_INT16, n);
			hz_stream *s_after = hz_stream_begin("in_after", HZ_INT16, n);
			hz_stream *s_inv = hz_stream_begin("inv", HZ_INT16, n);
			hz_stream *s_rt = hz_stream_begin("roundtrip", HZ_INT16, n);

			memcpy(in, x, n * sizeof in[0]);
			put_all(s_in, in, n);
			spx_fft(table, in, spec);
			put_all(s_fwd, spec, n);
			put_all(s_after, in, n);

			memcpy(in, x, n * sizeof in[0]);
			spx_ifft(table, in, out);
			put_all(s_inv, out, n);

			spx_ifft(table, spec, out);
			put_all(s_rt, out, n);

			snprintf(file, sizeof file, "{\"size\": %d, \"signal\": \"%s\"}", n,
			         signals[g]);
			hz_case_end(file);
			free(x);
		}
		spx_fft_destroy(table);
	}
}

/* Every supported size in increasing order, each with fresh SplitMix64 noise
 * over the full int16 range: N forward inputs, then N inverse inputs. */
static void sweep(void)
{
	spx_word16_t in[MAX_SIZE], out[MAX_SIZE];
	char params[160];
	uint64_t seed = fnv1a("sweep");
	hz_rng rng = {seed};
	int nsizes = 0;

	hz_case_begin("sweep");
	hz_stream *s_size = hz_stream_begin("sizes", HZ_INT32, 0);
	hz_stream *s_tw = hz_stream_begin("twiddles", HZ_INT16, 0);
	hz_stream *s_in = hz_stream_begin("fwd_in", HZ_INT16, 0);
	hz_stream *s_fwd = hz_stream_begin("fwd", HZ_INT16, 0);
	hz_stream *s_after = hz_stream_begin("in_after", HZ_INT16, 0);
	hz_stream *s_iin = hz_stream_begin("inv_in", HZ_INT16, 0);
	hz_stream *s_inv = hz_stream_begin("inv", HZ_INT16, 0);

	for (int n = 2; n <= MAX_SIZE; n += 2) {
		if (!supported(n))
			continue;
		nsizes++;
		void *table = spx_fft_init(n);
		hz_put_i32(s_size, n);
		put_twiddles(s_tw, table, n);

		for (int i = 0; i < n; i++)
			in[i] = (spx_word16_t)(int16_t)(hz_next(&rng) >> 48);
		put_all(s_in, in, n);
		spx_fft(table, in, out);
		put_all(s_fwd, out, n);
		put_all(s_after, in, n);

		for (int i = 0; i < n; i++)
			in[i] = (spx_word16_t)(int16_t)(hz_next(&rng) >> 48);
		put_all(s_iin, in, n);
		spx_ifft(table, in, out);
		put_all(s_inv, out, n);
		spx_fft_destroy(table);
	}
	snprintf(params, sizeof params,
	         "{\"max_size\": %d, \"max_radix\": %d, \"sizes\": %d, \"seed\": \"0x%016" PRIx64 "\"}",
	         MAX_SIZE, MAX_RADIX, nsizes, seed);
	hz_case_end(params);
}

int main(int argc, char **argv)
{
	if (argc != 3)
		hz_die("usage: %s <out-dir> <in-dir>", argv[0]);
	hz_manifest_open(argv[1], "fft", "reference/drivers/fft.c");
	mvp_cases(argv[2]);
	sweep();
	hz_manifest_close();
	return 0;
}
