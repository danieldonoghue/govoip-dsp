/* Reference driver for preprocess: runs speex_preprocess_* (preprocess.c
 * and filterbank.c, FIXED_POINT) over the case table that
 * preprocess/cases_test.go writes, and writes testdata/speexdsp/preprocess/.
 *
 * harness-sources: filterbank.c fftwrap.c kiss_fft.c kiss_fftr.c mdf.c
 * harness-inputs: ./preprocess
 *
 * preprocess.c is #included, unmodified, rather than listed above, so the
 * driver can read its private state (window, filterbank, speech_prob).
 * Input files: cases.txt, configs.txt, sweep.txt, ctl.txt, groups.txt, one
 * raw int16 file per signal, and far_/mic_<group>.raw per echo group. */
#include "config.h"

#include "../speexdsp/libspeexdsp/preprocess.c"

#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "speex/speex_echo.h"

#include "harness.h"

#define PREFIX_FRAMES 4 /* committed output frames */
#define PREFIX_DECISIONS 200 /* committed VAD and probability values */

typedef struct {
	char name[96], file[96], mode[16], group[64];
	int rate, n, frames, denoise, vad, pstart, pcont;
	spx_int32_t ns, es, esa;
} rcase;

static FILE *open_in(const char *dir, const char *name)
{
	char path[512];
	snprintf(path, sizeof path, "%s/%s", dir, name);
	FILE *f = fopen(path, "r");
	if (!f)
		hz_die("cannot open %s", path);
	return f;
}

static rcase *read_cases(const char *dir, int *count)
{
	FILE *f = open_in(dir, "cases.txt");
	int cap = 64, n = 0;
	rcase *cs = malloc(cap * sizeof *cs);
	rcase c;
	while (fscanf(f, "%95s %d %d %95s %d %d %d %" SCNd32 " %" SCNd32 " %" SCNd32 " %d %d %15s %63s",
	              c.name, &c.rate, &c.n, c.file, &c.frames, &c.denoise, &c.vad, &c.ns, &c.es,
	              &c.esa, &c.pstart, &c.pcont, c.mode, c.group) == 14) {
		if (n == cap)
			cs = realloc(cs, (cap *= 2) * sizeof *cs);
		cs[n++] = c;
	}
	fclose(f);
	*count = n;
	return cs;
}

static void put_bank(hz_stream *bl, hz_stream *br, hz_stream *fl, hz_stream *fr, FilterBank *b)
{
	for (int i = 0; i < b->len; i++) {
		hz_put_i32(bl, b->bank_left[i]);
		hz_put_i32(br, b->bank_right[i]);
	}
	for (int i = 0; i < b->len; i++)
		hz_put_i16(fl, b->filter_left[i]);
	for (int i = 0; i < b->len; i++)
		hz_put_i16(fr, b->filter_right[i]);
}

static void tables(const char *dir)
{
	FILE *f = open_in(dir, "configs.txt");
	int rate, n;
	char name[64];
	while (fscanf(f, "%d %d", &rate, &n) == 2) {
		SpeexPreprocessState *st = speex_preprocess_state_init(n, rate);
		snprintf(name, sizeof name, "tables_%d_%d", rate, n);
		hz_case_begin(name);
		hz_stream *w = hz_stream_begin("window", HZ_INT16, 2 * n);
		for (int i = 0; i < 2 * n; i++)
			hz_put_i16(w, st->window[i]);
		put_bank(hz_stream_begin("bank_left", HZ_INT32, n), hz_stream_begin("bank_right", HZ_INT32, n),
		         hz_stream_begin("filter_left", HZ_INT16, n), hz_stream_begin("filter_right", HZ_INT16, n),
		         st->bank);
		char params[96];
		snprintf(params, sizeof params, "{\"rate\": %d, \"frame_size\": %d}", rate, n);
		hz_case_end(params);
		speex_preprocess_state_destroy(st);
	}
	fclose(f);
}

static void sweep(const char *dir)
{
	FILE *f = open_in(dir, "sweep.txt");
	int rate, n, count = 0;
	hz_case_begin("sweep");
	hz_stream *cfg = hz_stream_begin("configs", HZ_INT32, 0);
	hz_stream *bl = hz_stream_begin("bank_left", HZ_INT32, 0);
	hz_stream *br = hz_stream_begin("bank_right", HZ_INT32, 0);
	hz_stream *fl = hz_stream_begin("filter_left", HZ_INT16, 0);
	hz_stream *fr = hz_stream_begin("filter_right", HZ_INT16, 0);
	while (fscanf(f, "%d %d", &rate, &n) == 2) {
		FilterBank *b = filterbank_new(NB_BANDS, rate, n, 1);
		hz_put_i32(cfg, rate);
		hz_put_i32(cfg, n);
		put_bank(bl, br, fl, fr, b);
		filterbank_destroy(b);
		count++;
	}
	fclose(f);
	char params[64];
	snprintf(params, sizeof params, "{\"configs\": %d}", count);
	hz_case_end(params);
}

static spx_int32_t get(SpeexPreprocessState *st, int req)
{
	spx_int32_t v = 0;
	speex_preprocess_ctl(st, req, &v);
	return v;
}

static void put_all_gets(hz_stream *s, SpeexPreprocessState *st)
{
	static const int reqs[] = {SPEEX_PREPROCESS_GET_DENOISE, SPEEX_PREPROCESS_GET_VAD,
	                           SPEEX_PREPROCESS_GET_PROB_START, SPEEX_PREPROCESS_GET_PROB_CONTINUE,
	                           SPEEX_PREPROCESS_GET_NOISE_SUPPRESS, SPEEX_PREPROCESS_GET_ECHO_SUPPRESS,
	                           SPEEX_PREPROCESS_GET_ECHO_SUPPRESS_ACTIVE, SPEEX_PREPROCESS_GET_PSD_SIZE,
	                           SPEEX_PREPROCESS_GET_PROB};
	for (size_t i = 0; i < sizeof reqs / sizeof reqs[0]; i++)
		hz_put_i32(s, get(st, reqs[i]));
}

static void ctl(const char *dir)
{
	static const struct {
		const char *name;
		int set, get;
	} ops[] = {
		{"prob_start", SPEEX_PREPROCESS_SET_PROB_START, SPEEX_PREPROCESS_GET_PROB_START},
		{"prob_continue", SPEEX_PREPROCESS_SET_PROB_CONTINUE, SPEEX_PREPROCESS_GET_PROB_CONTINUE},
		{"noise_suppress", SPEEX_PREPROCESS_SET_NOISE_SUPPRESS, SPEEX_PREPROCESS_GET_NOISE_SUPPRESS},
		{"echo_suppress", SPEEX_PREPROCESS_SET_ECHO_SUPPRESS, SPEEX_PREPROCESS_GET_ECHO_SUPPRESS},
		{"echo_suppress_active", SPEEX_PREPROCESS_SET_ECHO_SUPPRESS_ACTIVE,
		 SPEEX_PREPROCESS_GET_ECHO_SUPPRESS_ACTIVE},
	};
	SpeexPreprocessState *st = speex_preprocess_state_init(160, 16000);
	FILE *f = open_in(dir, "ctl.txt");
	char op[32];
	long long v;
	hz_case_begin("ctl");
	hz_stream *s = hz_stream_begin("get", HZ_INT32, 4096);
	put_all_gets(s, st);
	while (fscanf(f, "%31s %lld", op, &v) == 2) {
		size_t k;
		for (k = 0; k < sizeof ops / sizeof ops[0]; k++)
			if (!strcmp(op, ops[k].name))
				break;
		if (k == sizeof ops / sizeof ops[0])
			hz_die("ctl.txt: unknown op %s", op);
		spx_int32_t x = (spx_int32_t)v;
		speex_preprocess_ctl(st, ops[k].set, &x);
		hz_put_i32(s, get(st, ops[k].get));
	}
	fclose(f);
	spx_int32_t off = 0, on = 1;
	speex_preprocess_ctl(st, SPEEX_PREPROCESS_SET_DENOISE, &off);
	speex_preprocess_ctl(st, SPEEX_PREPROCESS_SET_VAD, &on);
	put_all_gets(s, st);
	hz_case_end(NULL);
	speex_preprocess_state_destroy(st);
}

static SpeexPreprocessState *new_state(const rcase *c)
{
	SpeexPreprocessState *st = speex_preprocess_state_init(c->n, c->rate);
	spx_int32_t v;
	v = c->denoise;
	speex_preprocess_ctl(st, SPEEX_PREPROCESS_SET_DENOISE, &v);
	v = c->vad;
	speex_preprocess_ctl(st, SPEEX_PREPROCESS_SET_VAD, &v);
	v = c->ns;
	speex_preprocess_ctl(st, SPEEX_PREPROCESS_SET_NOISE_SUPPRESS, &v);
	v = c->es;
	speex_preprocess_ctl(st, SPEEX_PREPROCESS_SET_ECHO_SUPPRESS, &v);
	v = c->esa;
	speex_preprocess_ctl(st, SPEEX_PREPROCESS_SET_ECHO_SUPPRESS_ACTIVE, &v);
	if (c->pstart >= 0) {
		v = c->pstart;
		speex_preprocess_ctl(st, SPEEX_PREPROCESS_SET_PROB_START, &v);
	}
	if (c->pcont >= 0) {
		v = c->pcont;
		speex_preprocess_ctl(st, SPEEX_PREPROCESS_SET_PROB_CONTINUE, &v);
	}
	return st;
}

static void case_params(char *buf, size_t size, const rcase *c)
{
	snprintf(buf, size,
	         "{\"rate\": %d, \"frame_size\": %d, \"frames\": %d, \"input\": \"%s\", \"denoise\": %d, "
	         "\"vad\": %d, \"noise_suppress\": %" PRId32 ", \"echo_suppress\": %" PRId32
	         ", \"echo_suppress_active\": %" PRId32 ", \"prob_start\": %d, \"prob_continue\": %d, "
	         "\"mode\": \"%s\", \"echo_group\": \"%s\"}",
	         c->rate, c->n, c->frames, c->file, c->denoise, c->vad, c->ns, c->es, c->esa, c->pstart,
	         c->pcont, c->mode, c->group);
}

/* The Go test mirrors these schedules (estimateFrame, echoOff). */
static int estimate_frame(int f) { return f < 50 || f % 4 == 3; }
static int echo_off(int f) { return f / 25 % 2 == 1; }

static void run_case(const char *dir, const rcase *c)
{
	size_t count;
	int16_t *x = hz_read_input(dir, c->file, HZ_INT16, &count);
	int n = c->n;
	if (count != (size_t)c->frames * n)
		hz_die("%s: %zu samples, want %d", c->file, count, c->frames * n);
	SpeexPreprocessState *st = new_state(c);
	spx_int32_t *buf = malloc(n * sizeof *buf);
	int psd = !strcmp(c->mode, "psd"), est = !strcmp(c->mode, "estimate");

	hz_case_begin(c->name);
	hz_stream *s_in = hz_stream_begin("in", HZ_INT16, 0);
	hz_stream *s_out = hz_stream_begin("out", HZ_INT16, PREFIX_FRAMES * n);
	hz_stream *s_vad = hz_stream_begin("vad", HZ_INT16, PREFIX_DECISIONS);
	hz_stream *s_prob = hz_stream_begin("prob", HZ_INT16, PREFIX_DECISIONS);
	hz_stream *s_psd = NULL, *s_npsd = NULL, *s_pct = NULL;
	if (psd) {
		s_psd = hz_stream_begin("psd", HZ_INT32, PREFIX_FRAMES * n);
		s_npsd = hz_stream_begin("noise_psd", HZ_INT32, PREFIX_FRAMES * n);
		s_pct = hz_stream_begin("prob_pct", HZ_INT32, PREFIX_DECISIONS);
	}
	for (int f = 0; f < c->frames; f++) {
		spx_int16_t *fr = x + (size_t)f * n;
		for (int i = 0; i < n; i++)
			hz_put_i16(s_in, fr[i]);
		int v = -1;
		if (est && estimate_frame(f))
			speex_preprocess_estimate_update(st, fr);
		else
			v = speex_preprocess_run(st, fr);
		for (int i = 0; i < n; i++)
			hz_put_i16(s_out, fr[i]);
		hz_put_i16(s_vad, v);
		hz_put_i16(s_prob, st->speech_prob);
		if (psd) {
			speex_preprocess_ctl(st, SPEEX_PREPROCESS_GET_PSD, buf);
			for (int i = 0; i < n; i++)
				hz_put_i32(s_psd, buf[i]);
			speex_preprocess_ctl(st, SPEEX_PREPROCESS_GET_NOISE_PSD, buf);
			for (int i = 0; i < n; i++)
				hz_put_i32(s_npsd, buf[i]);
			hz_put_i32(s_pct, get(st, SPEEX_PREPROCESS_GET_PROB));
		}
	}
	char params[512];
	case_params(params, sizeof params, c);
	hz_case_end(params);
	speex_preprocess_state_destroy(st);
	free(buf);
	free(x);
}

/* Runs one echo group: the C echo canceller and, in lockstep, a
 * preprocessor per variant case that uses it through SET_ECHO_STATE. */
static void echo_group(const char *dir, const char *gname, int rate, int n, int warmup, int frames,
                       const rcase *cs, int ncases)
{
	char file[160];
	size_t nfar, nmic;
	snprintf(file, sizeof file, "far_%s.raw", gname);
	int16_t *far = hz_read_input(dir, file, HZ_INT16, &nfar);
	snprintf(file, sizeof file, "mic_%s.raw", gname);
	int16_t *mic = hz_read_input(dir, file, HZ_INT16, &nmic);
	size_t total = (size_t)(warmup + frames) * n;
	if (nfar != total || nmic != total)
		hz_die("%s: %zu and %zu samples, want %zu", gname, nfar, nmic, total);

	int nv = 0;
	const rcase *vs[16];
	SpeexPreprocessState *sts[16];
	for (int i = 0; i < ncases; i++) {
		if (strcmp(cs[i].group, gname))
			continue;
		if (nv == 16 || cs[i].frames != frames || cs[i].rate != rate || cs[i].n != n)
			hz_die("%s: bad variant %s", gname, cs[i].name);
		vs[nv] = &cs[i];
		sts[nv] = new_state(&cs[i]);
		nv++;
	}
	size_t rec = (size_t)frames * n;
	int16_t *echo_in = malloc(rec * sizeof *echo_in);
	spx_int32_t *residual = malloc(rec * sizeof *residual);
	spx_int32_t *res = malloc((n + 1) * sizeof *res);
	int16_t *out = malloc(nv * rec * sizeof *out);
	int16_t *vad = malloc(nv * frames * sizeof *vad);
	int16_t *prob = malloc(nv * frames * sizeof *prob);
	spx_int16_t *e = malloc(n * sizeof *e);

	SpeexEchoState *es = speex_echo_state_init(n, rate / 10);
	speex_echo_ctl(es, SPEEX_ECHO_SET_SAMPLING_RATE, &rate);
	for (int f = 0; f < warmup + frames; f++) {
		speex_echo_cancellation(es, mic + (size_t)f * n, far + (size_t)f * n, e);
		if (f < warmup)
			continue;
		int r = f - warmup;
		memcpy(echo_in + (size_t)r * n, e, n * sizeof *e);
		speex_echo_get_residual(es, res, n);
		memcpy(residual + (size_t)r * n, res, n * sizeof *res);
		for (int k = 0; k < nv; k++) {
			spx_int16_t *fr = out + (size_t)k * rec + (size_t)r * n;
			memcpy(fr, e, n * sizeof *e);
			int off = !strcmp(vs[k]->mode, "echo_alt") && echo_off(r);
			speex_preprocess_ctl(sts[k], SPEEX_PREPROCESS_SET_ECHO_STATE, off ? NULL : es);
			vad[k * frames + r] = speex_preprocess_run(sts[k], fr);
			prob[k * frames + r] = sts[k]->speech_prob;
		}
	}

	char params[512];
	hz_case_begin(gname);
	hz_stream *s_far = hz_stream_begin("far", HZ_INT16, 0);
	hz_stream *s_mic = hz_stream_begin("mic", HZ_INT16, 0);
	for (size_t i = 0; i < total; i++) {
		hz_put_i16(s_far, far[i]);
		hz_put_i16(s_mic, mic[i]);
	}
	hz_stream *s_ein = hz_stream_begin("echo_in", HZ_INT16, rec);
	hz_stream *s_res = hz_stream_begin("residual", HZ_INT32, rec);
	for (size_t i = 0; i < rec; i++) {
		hz_put_i16(s_ein, echo_in[i]);
		hz_put_i32(s_res, residual[i]);
	}
	snprintf(params, sizeof params,
	         "{\"rate\": %d, \"frame_size\": %d, \"filter_length\": %d, \"warmup_frames\": %d, "
	         "\"frames\": %d}",
	         rate, n, rate / 10, warmup, frames);
	hz_case_end(params);

	for (int k = 0; k < nv; k++) {
		hz_case_begin(vs[k]->name);
		hz_stream *s_in = hz_stream_begin("in", HZ_INT16, 0);
		hz_stream *s_out = hz_stream_begin("out", HZ_INT16, PREFIX_FRAMES * n);
		hz_stream *s_vad = hz_stream_begin("vad", HZ_INT16, frames);
		hz_stream *s_prob = hz_stream_begin("prob", HZ_INT16, frames);
		for (size_t i = 0; i < rec; i++) {
			hz_put_i16(s_in, echo_in[i]);
			hz_put_i16(s_out, out[k * rec + i]);
		}
		for (int r = 0; r < frames; r++) {
			hz_put_i16(s_vad, vad[k * frames + r]);
			hz_put_i16(s_prob, prob[k * frames + r]);
		}
		case_params(params, sizeof params, vs[k]);
		hz_case_end(params);
		speex_preprocess_state_destroy(sts[k]);
	}
	speex_echo_state_destroy(es);
	free(far);
	free(mic);
	free(echo_in);
	free(residual);
	free(res);
	free(out);
	free(vad);
	free(prob);
	free(e);
}

int main(int argc, char **argv)
{
	if (argc != 3)
		hz_die("usage: %s <out-dir> <in-dir>", argv[0]);
	const char *dir = argv[2];
	int ncases;
	rcase *cs = read_cases(dir, &ncases);

	hz_manifest_open(argv[1], "preprocess", "reference/drivers/preprocess.c");
	tables(dir);
	sweep(dir);
	ctl(dir);
	for (int i = 0; i < ncases; i++)
		if (strcmp(cs[i].group, "-") == 0)
			run_case(dir, &cs[i]);

	FILE *f = open_in(dir, "groups.txt");
	char gname[64];
	int rate, n, warmup, frames;
	while (fscanf(f, "%63s %d %d %d %d", gname, &rate, &n, &warmup, &frames) == 5)
		echo_group(dir, gname, rate, n, warmup, frames, cs, ncases);
	fclose(f);
	hz_manifest_close();
	free(cs);
	return 0;
}
