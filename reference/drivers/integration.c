/* Reference driver for integration: runs the send-path chain of a call,
 * resampling (resample.c), echo cancellation (mdf.c) and preprocessing with
 * the echo state attached (preprocess.c), over the cases that
 * integration/reference_test.go writes (docs/acceptance/integration.md),
 * and writes testdata/speexdsp/integration/.
 *
 * harness-sources: resample.c mdf.c preprocess.c filterbank.c fftwrap.c kiss_fft.c kiss_fftr.c
 * harness-inputs: ./integration
 *
 * Input directory:
 *   cases.txt          one case per line:
 *                      <name> <net_rate> <rate> <frame> <filter> <mode> <quality> <prefix_frames> <base>
 *   <base>.mic.raw     int16 microphone samples at net_rate
 *   <base>.far.raw     int16 far-end samples at net_rate
 *
 * mode 0 calls speex_echo_cancellation; mode 1 calls speex_echo_playback
 * then speex_echo_capture. When net_rate differs from rate, each network
 * frame (frame * net_rate / rate samples) of mic and far goes through its
 * own resampler, and the chain runs whenever a whole frame is buffered.
 * The preprocessor holds the echo state (SET_ECHO_STATE), so it calls
 * speex_echo_get_residual itself; the driver calls it again afterwards
 * only to record the spectrum. */
#include "config.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "speex/speex_echo.h"
#include "speex/speex_preprocess.h"
#include "speex/speex_resampler.h"

#include "harness.h"

/* No public header declares it; preprocess.c:715 and mdf.c:122 declare it
 * the same way (spx_word32_t is spx_int32_t in the FIXED_POINT build). */
void speex_echo_get_residual(SpeexEchoState *st, spx_int32_t *Yout, int len);

#define MAX_CASES 64
#define PREFIX_DECISIONS 200 /* committed VAD and probability values */

typedef struct {
	char name[96], base[96];
	int net_rate, rate, frame, filter, mode, quality, prefix_frames;
} icase;

typedef struct {
	SpeexResamplerState *rs;
	spx_int16_t *buf; /* samples at the processing rate, not yet framed */
	int len, cap;
} feed;

/* Appends one network frame to f, through the resampler if there is one. */
static void feed_put(feed *f, const spx_int16_t *x, int n, const char *name)
{
	if (!f->rs) {
		if (f->len + n > f->cap)
			hz_die("%s: feed overflow", name);
		memcpy(f->buf + f->len, x, n * sizeof *x);
		f->len += n;
		return;
	}
	spx_uint32_t in = n, out = f->cap - f->len;
	int err = speex_resampler_process_int(f->rs, 0, x, &in, f->buf + f->len, &out);
	if (err != RESAMPLER_ERR_SUCCESS || in != (spx_uint32_t)n)
		hz_die("%s: resampler consumed %u of %d (error %d)", name, in, n, err);
	f->len += out;
}

static void feed_drop(feed *f, int n)
{
	memmove(f->buf, f->buf + n, (f->len - n) * sizeof *f->buf);
	f->len -= n;
}

static void run_case(const char *in_dir, const icase *c)
{
	char file[160], params[512];
	size_t nmic, nfar;
	int fs = c->frame, nf = c->frame * c->net_rate / c->rate;
	int rate = c->rate, on = 1;

	snprintf(file, sizeof file, "%s.mic.raw", c->base);
	int16_t *mic = hz_read_input(in_dir, file, HZ_INT16, &nmic);
	snprintf(file, sizeof file, "%s.far.raw", c->base);
	int16_t *far = hz_read_input(in_dir, file, HZ_INT16, &nfar);
	if (nmic != nfar || nmic % nf)
		hz_die("%s: %zu mic and %zu far samples, frame %d", c->name, nmic, nfar, nf);
	if (c->net_rate * fs % c->rate)
		hz_die("%s: the network frame is not whole", c->name);

	feed fm = {0}, ff = {0};
	fm.cap = ff.cap = 4 * fs + 64;
	fm.buf = malloc(fm.cap * sizeof *fm.buf);
	ff.buf = malloc(ff.cap * sizeof *ff.buf);
	if (c->net_rate != c->rate) {
		int err;
		fm.rs = speex_resampler_init(1, c->net_rate, c->rate, c->quality, &err);
		ff.rs = speex_resampler_init(1, c->net_rate, c->rate, c->quality, &err);
		if (!fm.rs || !ff.rs)
			hz_die("%s: speex_resampler_init failed", c->name);
	}

	SpeexEchoState *es = speex_echo_state_init(fs, c->filter);
	speex_echo_ctl(es, SPEEX_ECHO_SET_SAMPLING_RATE, &rate);
	SpeexPreprocessState *pp = speex_preprocess_state_init(fs, rate);
	speex_preprocess_ctl(pp, SPEEX_PREPROCESS_SET_VAD, &on);
	speex_preprocess_ctl(pp, SPEEX_PREPROCESS_SET_ECHO_STATE, es);

	spx_int16_t *e = malloc(fs * sizeof *e), *o = malloc(fs * sizeof *o);
	spx_int32_t *res = malloc((fs + 1) * sizeof *res);
	if (!fm.buf || !ff.buf || !e || !o || !res)
		hz_die("out of memory");

	hz_case_begin(c->name);
	hz_stream *s_mic = hz_stream_begin("mic", HZ_INT16, 0);
	hz_stream *s_far = hz_stream_begin("far", HZ_INT16, 0);
	hz_stream *s_echo = hz_stream_begin("echo_out", HZ_INT16, (uint64_t)c->prefix_frames * fs);
	hz_stream *s_res = hz_stream_begin("residual", HZ_INT32, (uint64_t)c->prefix_frames * (fs + 1));
	hz_stream *s_out = hz_stream_begin("out", HZ_INT16, (uint64_t)c->prefix_frames * fs);
	hz_stream *s_vad = hz_stream_begin("vad", HZ_INT16, PREFIX_DECISIONS);
	hz_stream *s_prob = hz_stream_begin("prob", HZ_INT16, PREFIX_DECISIONS);

	long frames = 0;
	for (size_t k = 0; k < nmic / nf; k++) {
		const spx_int16_t *m = mic + k * nf, *f = far + k * nf;
		for (int i = 0; i < nf; i++) {
			hz_put_i16(s_mic, m[i]);
			hz_put_i16(s_far, f[i]);
		}
		feed_put(&fm, m, nf, c->name);
		feed_put(&ff, f, nf, c->name);
		if (fm.len != ff.len)
			hz_die("%s: mic and far resampled to different lengths", c->name);
		while (fm.len >= fs) {
			if (c->mode == 0) {
				speex_echo_cancellation(es, fm.buf, ff.buf, e);
			} else {
				speex_echo_playback(es, ff.buf);
				speex_echo_capture(es, fm.buf, e);
			}
			memcpy(o, e, fs * sizeof *e);
			int vad = speex_preprocess_run(pp, o);
			spx_int32_t prob = 0;
			speex_preprocess_ctl(pp, SPEEX_PREPROCESS_GET_PROB, &prob);
			speex_echo_get_residual(es, res, fs + 1);
			for (int i = 0; i < fs; i++) {
				hz_put_i16(s_echo, e[i]);
				hz_put_i16(s_out, o[i]);
			}
			for (int i = 0; i <= fs; i++)
				hz_put_i32(s_res, res[i]);
			hz_put_i16(s_vad, (int16_t)vad);
			hz_put_i16(s_prob, (int16_t)prob);
			feed_drop(&fm, fs);
			feed_drop(&ff, fs);
			frames++;
		}
	}

	snprintf(params, sizeof params,
	         "{\"net_rate\": %d, \"rate\": %d, \"frame_size\": %d, \"filter_length\": %d, "
	         "\"mode\": \"%s\", \"quality\": %d, \"input\": \"%s\", \"frames\": %ld}",
	         c->net_rate, c->rate, fs, c->filter, c->mode ? "capture" : "cancel", c->quality,
	         c->base, frames);
	hz_case_end(params);

	speex_preprocess_state_destroy(pp);
	speex_echo_state_destroy(es);
	if (fm.rs) {
		speex_resampler_destroy(fm.rs);
		speex_resampler_destroy(ff.rs);
	}
	free(fm.buf);
	free(ff.buf);
	free(e);
	free(o);
	free(res);
	free(mic);
	free(far);
}

int main(int argc, char **argv)
{
	static icase cases[MAX_CASES];
	char path[512];
	int ncases = 0;

	if (argc != 3)
		hz_die("usage: %s <out-dir> <in-dir>", argv[0]);
	snprintf(path, sizeof path, "%s/cases.txt", argv[2]);
	FILE *fp = fopen(path, "r");
	if (!fp)
		hz_die("cannot read %s", path);
	icase c;
	while (fscanf(fp, "%95s %d %d %d %d %d %d %d %95s", c.name, &c.net_rate, &c.rate, &c.frame,
	              &c.filter, &c.mode, &c.quality, &c.prefix_frames, c.base) == 9) {
		if (ncases == MAX_CASES)
			hz_die("too many cases");
		cases[ncases++] = c;
	}
	if (!feof(fp))
		hz_die("%s: malformed line after case %d", path, ncases);
	fclose(fp);

	hz_manifest_open(argv[1], "integration", "reference/drivers/integration.c");
	for (int i = 0; i < ncases; i++)
		run_case(argv[2], &cases[i]);
	hz_manifest_close();
	return 0;
}
