/* Reference driver for echo: runs the mono speex_echo_* API of mdf.c over
 * the cases that echo/reference_test.go writes into the input directory
 * (docs/acceptance/echo.md), and writes testdata/speexdsp/echo/.
 *
 * harness-sources: fftwrap.c kiss_fft.c kiss_fftr.c
 * harness-inputs: ./echo
 *
 * Input directory:
 *   cases.txt          one case per line:
 *                      <name> <frame> <filter> <rate> <base> <out_prefix> <res_prefix>
 *   <base>.mic.raw     int16 microphone samples
 *   <base>.far.raw     int16 loudspeaker samples
 *   <name>.ops.raw     int32 pairs (op, arg): 0 cancellation, 1 playback,
 *                      2 capture, 3 reset, 4 SET_SAMPLING_RATE to arg
 *
 * Each cancellation or playback takes the next frame of far; each
 * cancellation or capture takes the next frame of mic, writes a frame of
 * out and then the residual spectrum. The impulse response is written at
 * the end.
 *
 * Tracing: with HARNESS_ECHO_TRACE=<dir>, every case whose name matches
 * HARNESS_ECHO_TRACE_CASE (all cases if unset) writes <dir>/<case>.trace,
 * one uint64 FNV-1a hash per state variable after every op, in the order
 * of trace_state below (echo/trace_test.go reads it). With
 * HARNESS_ECHO_DUMP_OP=<k> it also writes <dir>/<case>.dump, every state
 * variable after op k as int32 values. */
#include "config.h"

#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* Count the reference's warnings instead of printing them. */
#define OVERRIDE_SPEEX_WARNING
#define OVERRIDE_SPEEX_WARNING_INT
static long warn_no_playback, warn_corruption, warn_discard_first, warn_discard,
	warn_autofill, warn_reset, warn_divu, warn_other;
static void speex_warning(const char *str)
{
	if (strstr(str, "No playback frame"))
		warn_no_playback++;
	else if (strstr(str, "corruption"))
		warn_corruption++;
	else if (strstr(str, "discarded first"))
		warn_discard_first++;
	else if (strstr(str, "Had to discard"))
		warn_discard++;
	else if (strstr(str, "Auto-filling"))
		warn_autofill++;
	else if (strstr(str, "acting funny"))
		warn_reset++;
	else
		warn_other++;
}
static void speex_warning_int(const char *str, int val)
{
	(void)val;
	if (strstr(str, "divide"))
		warn_divu++;
	else
		warn_other++;
}

/* The state struct is private to mdf.c; include it to read the state. */
#include "mdf.c"

#include "harness.h"

#define MAX_CASES 2048

/* ---- tracing ---------------------------------------------------------- */

static FILE *trace_fp, *dump_fp;
static long dump_op = -1;

static uint64_t fnv_init(void) { return 0xcbf29ce484222325ULL; }

static uint64_t fnv_i32(uint64_t h, int32_t v)
{
	uint32_t u = (uint32_t)v;
	for (int b = 0; b < 4; b++) {
		h ^= (u >> (8 * b)) & 0xff;
		h *= 0x100000001b3ULL;
	}
	return h;
}

static void emit_vec(const int32_t *v, size_t n, int dump)
{
	uint64_t h = fnv_init();
	for (size_t i = 0; i < n; i++)
		h = fnv_i32(h, v[i]);
	fwrite(&h, sizeof h, 1, trace_fp);
	if (dump)
		fwrite(v, sizeof v[0], n, dump_fp);
}

static int32_t *tbuf;
static size_t tcap;

static int32_t *tvec(size_t n)
{
	if (n > tcap) {
		tcap = n;
		tbuf = realloc(tbuf, n * sizeof *tbuf);
		if (!tbuf)
			hz_die("out of memory");
	}
	return tbuf;
}

static void emit16(const spx_word16_t *a, size_t n, int dump)
{
	int32_t *v = tvec(n);
	for (size_t i = 0; i < n; i++)
		v[i] = a[i];
	emit_vec(v, n, dump);
}

static void emit32(const spx_word32_t *a, size_t n, int dump)
{
	int32_t *v = tvec(n);
	for (size_t i = 0; i < n; i++)
		v[i] = a[i];
	emit_vec(v, n, dump);
}

static void emitf(const spx_float_t *a, size_t n, int dump)
{
	int32_t *v = tvec(2 * n);
	for (size_t i = 0; i < n; i++) {
		v[2 * i] = a[i].m;
		v[2 * i + 1] = a[i].e;
	}
	emit_vec(v, 2 * n, dump);
}

static void emit1(int32_t x, int dump) { emit_vec(&x, 1, dump); }

/* The order and widening here must match traceVars in echo/trace_test.go. */
static void trace_state(SpeexEchoState *st, int dump)
{
	int N = st->window_size, M = st->M, fs = st->frame_size;
	emit1(st->cancel_count, dump);
	emit1(st->adapted, dump);
	emit1(st->saturated, dump);
	emit1(st->screwed_up, dump);
	emit1(st->sampling_rate, dump);
	emit1(st->spec_average, dump);
	emit1(st->beta0, dump);
	emit1(st->beta_max, dump);
	emit1(st->sum_adapt, dump);
	emit1(st->leak_estimate, dump);
	emit1(st->Davg1, dump);
	emit1(st->Davg2, dump);
	emitf(&st->Dvar1, 1, dump);
	emitf(&st->Dvar2, 1, dump);
	emitf(&st->Pey, 1, dump);
	emitf(&st->Pyy, 1, dump);
	emit1(st->memX[0], dump);
	emit1(st->memD[0], dump);
	emit1(st->memE[0], dump);
	emit1(st->preemph, dump);
	emit1(st->notch_radius, dump);
	emit32(st->notch_mem, 2, dump);
	emit1(st->play_buf_pos, dump);
	emit1(st->play_buf_started, dump);
	emit16(st->e, N, dump);
	emit16(st->x, N, dump);
	emit16(st->X, (M + 1) * N, dump);
	emit16(st->input, fs, dump);
	emit16(st->y, N, dump);
	emit16(st->last_y, N, dump);
	emit16(st->Y, N, dump);
	emit16(st->E, N, dump);
	emit32(st->PHI, N, dump);
	emit32(st->W, M * N, dump);
	emit16(st->foreground, M * N, dump);
	emit32(st->power, fs + 1, dump);
	emitf(st->power_1, fs + 1, dump);
	emit16(st->wtmp, N, dump);
	emit16(st->wtmp2, N, dump);
	emit32(st->Rf, fs + 1, dump);
	emit32(st->Yf, fs + 1, dump);
	emit32(st->Xf, fs + 1, dump);
	emit32(st->Eh, fs + 1, dump);
	emit32(st->Yh, fs + 1, dump);
	emit16(st->window, N, dump);
	emit16(st->prop, M, dump);
	emit16(st->play_buf, (PLAYBACK_DELAY + 1) * fs, dump);
}

/* ---- pseudo-float probe ---------------------------------------------- */

/* The masked shifts of internal/fixed are checked here over exponents where
 * C is undefined. noinline keeps each operation scalar, as it is compiled
 * inside mdf.c; the lists must match echo/reference_test.go. */
static const int16_t probe_m[] = {-32768, -32767, -16385, -16384, -16383, -256, -2, -1, 0,
                                  1, 2, 255, 256, 16383, 16384, 16385, 32766, 32767};
static const int32_t probe_a[] = {-2147483647 - 1, -2147483647, -1073741825, -1073741824,
                                  -65537, -65536, -32769, -32768, -1, 1, 2, 32767, 32768,
                                  65535, 65536, 16777216, 1073741824, 2147483646, 2147483647};
static const int16_t probe_be[] = {-30, -1, 0, 1, 30};
#define NELEM(a) (int)(sizeof(a) / sizeof((a)[0]))

__attribute__((noinline)) static spx_int32_t probe_x32(spx_float_t a) { return FLOAT_EXTRACT32(a); }
__attribute__((noinline)) static spx_int16_t probe_x16(spx_float_t a) { return FLOAT_EXTRACT16(a); }
__attribute__((noinline)) static spx_float_t probe_div(spx_word32_t a, spx_float_t b)
{
	return FLOAT_DIV32_FLOAT(a, b);
}

static void probe_case(void)
{
	hz_case_begin("probe_pseudofloat");
	hz_stream *s_x = hz_stream_begin("extract", HZ_INT32, 0);
	hz_stream *s_d = hz_stream_begin("div32_float", HZ_INT32, 0);
	for (int i = 0; i < NELEM(probe_m); i++)
		for (int e = -80; e <= 40; e++) {
			spx_float_t a = {probe_m[i], (spx_int16_t)e};
			hz_put_i32(s_x, probe_x32(a));
			hz_put_i32(s_x, probe_x16(a));
		}
	for (int i = 0; i < NELEM(probe_a); i++)
		for (int j = 0; j < NELEM(probe_m); j++)
			for (int k = 0; k < NELEM(probe_be); k++) {
				spx_float_t b = {probe_m[j], probe_be[k]};
				if (b.m == 0)
					continue;
				spx_float_t r = probe_div(probe_a[i], b);
				hz_put_i32(s_d, r.m);
				hz_put_i32(s_d, r.e);
			}
	hz_case_end("{\"extract_e\": [-80, 40]}");
}

/* ---- cases ------------------------------------------------------------ */

typedef struct {
	char name[96], base[96];
	int frame, filter, rate;
	long out_prefix, res_prefix;
} ecase;

static void put16(hz_stream *s, const spx_int16_t *x, int n)
{
	for (int i = 0; i < n; i++)
		hz_put_i16(s, x[i]);
}

static void run_case(const char *in_dir, const ecase *c, const char *trace_dir,
                     const char *trace_case)
{
	char file[160], params[1024];
	size_t nmic, nfar, nops;
	int16_t *mic, *far;
	int32_t *ops;
	long mpos = 0, fpos = 0;
	int fs = c->frame;
	spx_int16_t out[2048];
	spx_int32_t res[2049];

	snprintf(file, sizeof file, "%s.mic.raw", c->base);
	mic = hz_read_input(in_dir, file, HZ_INT16, &nmic);
	snprintf(file, sizeof file, "%s.far.raw", c->base);
	far = hz_read_input(in_dir, file, HZ_INT16, &nfar);
	snprintf(file, sizeof file, "%s.ops.raw", c->name);
	ops = hz_read_input(in_dir, file, HZ_INT32, &nops);
	if (nops % 2)
		hz_die("%s: odd op stream", c->name);
	if (fs > 2048)
		hz_die("%s: frame size %d", c->name, fs);

	warn_no_playback = warn_corruption = warn_discard_first = warn_discard =
		warn_autofill = warn_reset = warn_divu = warn_other = 0;

	trace_fp = dump_fp = NULL;
	if (trace_dir && (!trace_case || strcmp(trace_case, c->name) == 0)) {
		snprintf(file, sizeof file, "%s/%s.trace", trace_dir, c->name);
		if (!(trace_fp = fopen(file, "wb")))
			hz_die("cannot write %s", file);
		if (dump_op >= 0) {
			snprintf(file, sizeof file, "%s/%s.dump", trace_dir, c->name);
			if (!(dump_fp = fopen(file, "wb")))
				hz_die("cannot write %s", file);
		}
	}

	SpeexEchoState *st = speex_echo_state_init(fs, c->filter);
	int rate = c->rate;
	speex_echo_ctl(st, SPEEX_ECHO_SET_SAMPLING_RATE, &rate);

	hz_case_begin(c->name);
	hz_stream *s_ops = hz_stream_begin("ops", HZ_INT32, 0);
	hz_stream *s_mic = hz_stream_begin("mic", HZ_INT16, 0);
	hz_stream *s_far = hz_stream_begin("far", HZ_INT16, 0);
	hz_stream *s_out = hz_stream_begin("out", HZ_INT16, c->out_prefix);
	hz_stream *s_res = hz_stream_begin("residual", HZ_INT32, c->res_prefix);
	hz_stream *s_ir = hz_stream_begin("ir", HZ_INT32, 0);

	long frames = 0;
	for (size_t k = 0; k < nops / 2; k++) {
		int32_t op = ops[2 * k], arg = ops[2 * k + 1];
		hz_put_i32(s_ops, op);
		hz_put_i32(s_ops, arg);
		if ((op == 0 || op == 2) && mpos + fs > (long)nmic)
			hz_die("%s: op %zu: mic input exhausted", c->name, k);
		if ((op == 0 || op == 1) && fpos + fs > (long)nfar)
			hz_die("%s: op %zu: far input exhausted", c->name, k);
		switch (op) {
		case 0:
			put16(s_mic, mic + mpos, fs);
			put16(s_far, far + fpos, fs);
			speex_echo_cancellation(st, mic + mpos, far + fpos, out);
			mpos += fs;
			fpos += fs;
			break;
		case 1:
			put16(s_far, far + fpos, fs);
			speex_echo_playback(st, far + fpos);
			fpos += fs;
			break;
		case 2:
			put16(s_mic, mic + mpos, fs);
			speex_echo_capture(st, mic + mpos, out);
			mpos += fs;
			break;
		case 3:
			speex_echo_state_reset(st);
			break;
		case 4:
			rate = arg;
			speex_echo_ctl(st, SPEEX_ECHO_SET_SAMPLING_RATE, &rate);
			break;
		default:
			hz_die("%s: op %zu: unknown op %d", c->name, k, op);
		}
		if (op == 0 || op == 2) {
			put16(s_out, out, fs);
			speex_echo_get_residual(st, res, fs + 1);
			for (int i = 0; i <= fs; i++)
				hz_put_i32(s_res, res[i]);
			frames++;
		}
		if (trace_fp)
			trace_state(st, dump_fp && (long)k == dump_op);
	}

	spx_int32_t irlen;
	speex_echo_ctl(st, SPEEX_ECHO_GET_IMPULSE_RESPONSE_SIZE, &irlen);
	spx_int32_t *ir = malloc((size_t)irlen * sizeof *ir);
	if (!ir)
		hz_die("out of memory");
	speex_echo_ctl(st, SPEEX_ECHO_GET_IMPULSE_RESPONSE, ir);
	for (long i = 0; i < irlen; i++)
		hz_put_i32(s_ir, ir[i]);

	snprintf(params, sizeof params,
	         "{\"frame_size\": %d, \"filter_length\": %d, \"rate\": %d, \"input\": \"%s\", "
	         "\"ops\": %zu, \"frames\": %ld, \"blocks\": %d, "
	         "\"warnings\": {\"no_playback\": %ld, \"corruption\": %ld, \"discarded_first\": %ld, "
	         "\"discarded\": %ld, \"auto_fill\": %ld, \"reset\": %ld, \"divu\": %ld, \"other\": %ld}}",
	         fs, c->filter, c->rate, c->base, nops / 2, frames, st->M, warn_no_playback,
	         warn_corruption, warn_discard_first, warn_discard, warn_autofill, warn_reset,
	         warn_divu, warn_other);
	hz_case_end(params);

	speex_echo_state_destroy(st);
	free(ir);
	free(mic);
	free(far);
	free(ops);
	if (trace_fp)
		fclose(trace_fp);
	if (dump_fp)
		fclose(dump_fp);
}

int main(int argc, char **argv)
{
	static ecase cases[MAX_CASES];
	char path[512];
	int ncases = 0;

	if (argc != 3)
		hz_die("usage: %s <out-dir> <in-dir>", argv[0]);
	snprintf(path, sizeof path, "%s/cases.txt", argv[2]);
	FILE *fp = fopen(path, "r");
	if (!fp)
		hz_die("cannot read %s", path);
	ecase c;
	while (fscanf(fp, "%95s %d %d %d %95s %ld %ld", c.name, &c.frame, &c.filter, &c.rate,
	              c.base, &c.out_prefix, &c.res_prefix) == 7) {
		if (ncases == MAX_CASES)
			hz_die("too many cases");
		cases[ncases++] = c;
	}
	if (!feof(fp))
		hz_die("%s: malformed line after case %d", path, ncases);
	fclose(fp);

	const char *trace_dir = getenv("HARNESS_ECHO_TRACE");
	const char *trace_case = getenv("HARNESS_ECHO_TRACE_CASE");
	const char *dump = getenv("HARNESS_ECHO_DUMP_OP");
	if (trace_dir && !*trace_dir)
		trace_dir = NULL;
	if (trace_case && !*trace_case)
		trace_case = NULL;
	if (dump && *dump)
		dump_op = strtol(dump, NULL, 10);

	hz_manifest_open(argv[1], "echo", "reference/drivers/echo.c");
	probe_case();
	for (int i = 0; i < ncases; i++)
		run_case(argv[2], &cases[i], trace_dir, trace_case);
	hz_manifest_close();
	free(tbuf);
	return 0;
}
