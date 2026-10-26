/* Reference driver for resample: runs SpeexDSP 1.2.1 resample.c over the
 * configurations pre-registered in docs/acceptance/resample.md and writes
 * testdata/speexdsp/resample/.
 *
 * harness-sources:
 * harness-inputs:
 *
 * resample.c is included rather than linked so that the driver can read the
 * filter state and the sinc table. resample/reference_test.go replays every
 * case from its parameters; the signal and call generators below must stay
 * identical to the Go ones, which the "signal" case hashes check. */
#include "config.h"

#include "speexdsp/libspeexdsp/resample.c" /* via -Ireference; "resample.c" would be this file */

#include <inttypes.h>
#include <stdarg.h>
#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "harness.h"

#define SIGNAL_LEN 262144
#define EV_FRAMES 48000
#define MIXED_CALLS 3
#define MAX_CALLS 200000
#define MAX_EVENTS 64
#define OUT_CAP (1 << 20)
#define OUT_STRIDE_SPLIT 3

/* Trace record types; every record has 6 int32 fields. */
enum { T_PROCESS = 0, T_INIT = 10, T_SPLIT = 100 };
enum { E_QUALITY = 1, E_RATE = 2, E_RATE_FRAC = 3, E_RESET = 4, E_SKIP = 5 };

static uint64_t fnv1a(const char *s)
{
	uint64_t h = 0xcbf29ce484222325ULL;
	for (; *s; s++) {
		h ^= (unsigned char)*s;
		h *= 0x100000001b3ULL;
	}
	return h;
}

/* The int16 test signal: segments of noise, square waves, low-level noise,
 * silence, impulse trains and sawtooth ramps. */
typedef struct {
	hz_rng r;
	uint32_t left, type, pos, half, step;
} sig16;

static int16_t sig16_next(sig16 *s)
{
	int16_t v = 0;
	if (s->left == 0) {
		s->left = 64 + (uint32_t)(hz_next(&s->r) % 512);
		s->type = (uint32_t)(hz_next(&s->r) % 6);
		s->pos = 0;
		s->half = 1 + (uint32_t)(hz_next(&s->r) % 32);
		s->step = (uint32_t)(hz_next(&s->r) % 2048);
	}
	switch (s->type) {
	case 0: v = (int16_t)(uint16_t)(hz_next(&s->r) >> 48); break;
	case 1: v = ((s->pos / s->half) & 1) ? 32767 : -32768; break;
	case 2: v = (int16_t)((int32_t)(hz_next(&s->r) % 33) - 16); break;
	case 3: v = 0; break;
	case 4: v = (s->pos % (s->half + 5) == 0) ? 32767 : 0; break;
	default: v = (int16_t)(uint16_t)(s->pos * s->step); break;
	}
	s->pos++;
	s->left--;
	return v;
}

static const float specials[15] = {
	INFINITY, -INFINITY, -0.0f, 0.5f, -0.5f, 0.99999994f, -0.99999994f,
	32766.0f, 32766.5f, 32767.0f, -32767.0f, -32767.5f, -32768.0f, 1e30f,
	-1e30f};

static int16_t signal16[SIGNAL_LEN];
static float signalf[SIGNAL_LEN];

static void make_signals(void)
{
	sig16 s;
	hz_rng r;
	int i;
	memset(&s, 0, sizeof s);
	s.r.state = fnv1a("signal");
	for (i = 0; i < SIGNAL_LEN; i++)
		signal16[i] = sig16_next(&s);
	r.state = fnv1a("signal_f32");
	for (i = 0; i < SIGNAL_LEN; i++) {
		if (i % 97 == 96)
			signalf[i] = specials[hz_next(&r) % 15];
		else
			signalf[i] = (float)((int32_t)(hz_next(&r) % 100001) - 50000) / 1.5f;
	}
}

static void signal_case(void)
{
	hz_stream *a, *b;
	int i;
	hz_case_begin("signal");
	a = hz_stream_begin("int16", HZ_INT16, 4096);
	b = hz_stream_begin("float32", HZ_FLOAT32, 4096);
	for (i = 0; i < SIGNAL_LEN; i++) {
		hz_put_i16(a, signal16[i]);
		hz_put_f32(b, signalf[i]);
	}
	hz_case_end("{\"length\": 262144}");
}

/* Draws the next call's offered input and output lengths, in frames. */
static void draw_call(hz_rng *r, uint32_t num, uint32_t den, uint32_t *in, uint32_t *out)
{
	uint64_t k = hz_next(r) % 16, m, o;
	if (k == 0)
		*in = 0;
	else if (k <= 2)
		*in = 1 + (uint32_t)(hz_next(r) % 4);
	else if (k <= 4)
		*in = 161 + (uint32_t)(hz_next(r) % 1000);
	else
		*in = 1 + (uint32_t)(hz_next(r) % 480);
	m = hz_next(r) % 8;
	if (m == 0) {
		*out = 0;
	} else if (m == 1) {
		*out = 1 + (uint32_t)(hz_next(r) % 8);
	} else {
		o = (uint64_t)*in * den / num + 1 + hz_next(r) % 64;
		*out = (uint32_t)(o > OUT_CAP ? OUT_CAP : o);
	}
}

typedef struct {
	int call, op;
	uint32_t a, b, c, d;
} event;

typedef struct {
	const char *name;
	char mode; /* i/f: interleaved int/float, s/g: per channel int/float, m: mixed (see run_interleaved) */
	int frac;  /* 1: speex_resampler_init_frac with num:den, else init with the rates */
	int q;
	uint32_t ch, num, den, in_rate, out_rate, frames;
	int nev;
	event ev[MAX_EVENTS];
	int commit; /* commit prefixes of the table, output and trace */
	uint32_t max_calls; /* 0: the case must consume all input */
} rcase;

static void put_rec(hz_stream *t, int32_t a, int32_t b, int32_t c, int32_t d, int32_t e, int32_t f)
{
	hz_put_i32(t, a);
	hz_put_i32(t, b);
	hz_put_i32(t, c);
	hz_put_i32(t, d);
	hz_put_i32(t, e);
	hz_put_i32(t, f);
}

static void put_state_rec(hz_stream *t, int32_t type, int ret, SpeexResamplerState *st)
{
	spx_uint32_t num, den;
	speex_resampler_get_ratio(st, &num, &den);
	put_rec(t, type, ret, speex_resampler_get_input_latency(st),
	        speex_resampler_get_output_latency(st), (int32_t)num, (int32_t)den);
}

static int apply_event(SpeexResamplerState *st, const event *e)
{
	switch (e->op) {
	case E_QUALITY: return speex_resampler_set_quality(st, (int)e->a);
	case E_RATE: return speex_resampler_set_rate(st, e->a, e->b);
	case E_RATE_FRAC: return speex_resampler_set_rate_frac(st, e->a, e->b, e->c, e->d);
	case E_RESET: return speex_resampler_reset_mem(st);
	case E_SKIP: return speex_resampler_skip_zeros(st);
	}
	hz_die("unknown event %d", e->op);
}

static size_t json_events(char *p, size_t n, const rcase *c)
{
	size_t len = 0;
	int i;
	len += (size_t)snprintf(p + len, n - len, "[");
	for (i = 0; i < c->nev; i++)
		len += (size_t)snprintf(p + len, n - len, "%s[%d, %d, %" PRIu32 ", %" PRIu32 ", %" PRIu32 ", %" PRIu32 "]",
		                        i ? ", " : "", c->ev[i].call, c->ev[i].op, c->ev[i].a, c->ev[i].b, c->ev[i].c, c->ev[i].d);
	len += (size_t)snprintf(p + len, n - len, "]");
	return len;
}

static void run_interleaved(const rcase *c, SpeexResamplerState *st, hz_stream *out, hz_stream *tr)
{
	hz_rng r;
	uint32_t pos = 0, calls = 0, nch = c->ch;
	int ev = 0, stop = 0;
	r.state = fnv1a(c->name);
	/* Mixed mode: channel 0 alone first, so the channels' states differ. */
	if (c->mode == 'm') {
		uint32_t p0 = 0, k;
		speex_resampler_set_input_stride(st, nch);
		speex_resampler_set_output_stride(st, 1);
		for (k = 0; k < MIXED_CALLS; k++) {
			uint32_t in_off, out_off, i;
			spx_uint32_t num, den, il, ol;
			spx_int16_t *buf;
			int ret;
			speex_resampler_get_ratio(st, &num, &den);
			draw_call(&r, num, den, &in_off, &out_off);
			il = in_off < c->frames - p0 ? in_off : c->frames - p0;
			ol = out_off;
			buf = malloc(sizeof *buf * ((size_t)out_off + 1));
			if (!buf)
				hz_die("out of memory");
			ret = speex_resampler_process_int(st, 0, signal16 + (size_t)p0 * nch, &il, buf, &ol);
			for (i = 0; i < ol; i++)
				hz_put_i16(out, buf[i]);
			free(buf);
			put_rec(tr, T_SPLIT, (int32_t)(in_off < c->frames - p0 ? in_off : c->frames - p0), (int32_t)out_off,
			        (int32_t)il, (int32_t)ol, ret);
			p0 += il;
		}
		speex_resampler_set_input_stride(st, 1);
	}
	while (pos < c->frames && !stop) {
		uint32_t in_off, out_off, in_len, out_len, i;
		spx_uint32_t num, den;
		int ret;
		for (; ev < c->nev && c->ev[ev].call == (int)calls; ev++) {
			ret = apply_event(st, &c->ev[ev]);
			/* After an overflow the C state is partly updated (see the
			 * acceptance criteria); record only the return code and stop. */
			if (ret == RESAMPLER_ERR_OVERFLOW) {
				put_rec(tr, c->ev[ev].op, ret, 0, 0, 0, 0);
				stop = 1;
				break;
			}
			put_state_rec(tr, c->ev[ev].op, ret, st);
		}
		if (stop)
			break;
		speex_resampler_get_ratio(st, &num, &den);
		draw_call(&r, num, den, &in_off, &out_off);
		in_len = in_off < c->frames - pos ? in_off : c->frames - pos;
		out_len = out_off;
		{
			spx_uint32_t il = in_len, ol = out_len;
			if (c->mode != 'f') {
				spx_int16_t *buf = malloc(sizeof *buf * ((size_t)out_len * nch + 1));
				if (!buf)
					hz_die("out of memory");
				ret = speex_resampler_process_interleaved_int(st, signal16 + (size_t)pos * nch, &il, buf, &ol);
				for (i = 0; i < ol * nch; i++)
					hz_put_i16(out, buf[i]);
				free(buf);
			} else {
				float *buf = malloc(sizeof *buf * ((size_t)out_len * nch + 1));
				if (!buf)
					hz_die("out of memory");
				ret = speex_resampler_process_interleaved_float(st, signalf + (size_t)pos * nch, &il, buf, &ol);
				for (i = 0; i < ol * nch; i++)
					hz_put_f32(out, buf[i]);
				free(buf);
			}
			put_rec(tr, T_PROCESS, (int32_t)in_len, (int32_t)out_len, (int32_t)il, (int32_t)ol, ret);
			pos += il;
		}
		if (++calls == c->max_calls)
			break;
		if (calls > MAX_CALLS)
			hz_die("case %s: no progress after %d calls", c->name, MAX_CALLS);
	}
}

/* Each channel has its own position and call sequence; the input is read
 * with stride ch and the output written with stride OUT_STRIDE_SPLIT. */
static void run_split(const rcase *c, SpeexResamplerState *st, hz_stream *out, hz_stream *tr)
{
	hz_rng r[8];
	uint32_t pos[8], ch, nch = c->ch, calls = 0;
	int left = 1;
	if (nch > 8)
		hz_die("case %s: too many channels for split mode", c->name);
	for (ch = 0; ch < nch; ch++) {
		r[ch].state = fnv1a(c->name) + ch;
		pos[ch] = 0;
	}
	speex_resampler_set_input_stride(st, nch);
	speex_resampler_set_output_stride(st, OUT_STRIDE_SPLIT);
	while (left) {
		left = 0;
		for (ch = 0; ch < nch; ch++) {
			uint32_t in_off, out_off, in_len, i;
			spx_uint32_t num, den, il, ol;
			int ret;
			if (pos[ch] >= c->frames)
				continue;
			left = 1;
			speex_resampler_get_ratio(st, &num, &den);
			draw_call(&r[ch], num, den, &in_off, &out_off);
			in_len = in_off < c->frames - pos[ch] ? in_off : c->frames - pos[ch];
			il = in_len;
			ol = out_off;
			if (c->mode == 's') {
				spx_int16_t *buf = calloc((size_t)out_off * OUT_STRIDE_SPLIT + 1, sizeof *buf);
				if (!buf)
					hz_die("out of memory");
				ret = speex_resampler_process_int(st, ch, signal16 + (size_t)pos[ch] * nch + ch, &il, buf, &ol);
				for (i = 0; i < ol; i++)
					hz_put_i16(out, buf[i * OUT_STRIDE_SPLIT]);
				free(buf);
			} else {
				float *buf = calloc((size_t)out_off * OUT_STRIDE_SPLIT + 1, sizeof *buf);
				if (!buf)
					hz_die("out of memory");
				ret = speex_resampler_process_float(st, ch, signalf + (size_t)pos[ch] * nch + ch, &il, buf, &ol);
				for (i = 0; i < ol; i++)
					hz_put_f32(out, buf[i * OUT_STRIDE_SPLIT]);
				free(buf);
			}
			put_rec(tr, T_SPLIT + (int32_t)ch, (int32_t)in_len, (int32_t)out_off, (int32_t)il, (int32_t)ol, ret);
			pos[ch] += il;
			if (++calls > MAX_CALLS)
				hz_die("case %s: no progress after %d calls", c->name, MAX_CALLS);
		}
	}
}

static void run_case(const rcase *c)
{
	SpeexResamplerState *st;
	hz_stream *table = NULL, *out, *tr;
	int err = 0;
	char params[4096], ev[2048], state[1024] = "null";
	uint64_t prefix_table = c->commit ? 1 << 16 : 0;
	uint64_t prefix_out = c->commit ? 2048 : 0;
	uint64_t prefix_tr = c->commit ? 4096 : 0;
	int is_float = c->mode == 'f' || c->mode == 'g';

	if ((uint64_t)c->frames * c->ch > SIGNAL_LEN && c->ch < 1000)
		hz_die("case %s: signal too short", c->name);
	hz_case_begin(c->name);
	if (c->frac)
		st = speex_resampler_init_frac(c->ch, c->num, c->den, c->in_rate, c->out_rate, c->q, &err);
	else
		st = speex_resampler_init(c->ch, c->in_rate, c->out_rate, c->q, &err);
	if (st && c->mode == 'i' && c->ch == 1 && c->nev == 0)
		table = hz_stream_begin("table", HZ_INT16, prefix_table);
	out = hz_stream_begin("out", is_float ? HZ_FLOAT32 : HZ_INT16, prefix_out);
	tr = hz_stream_begin("trace", HZ_INT32, prefix_tr);
	if (st) {
		int direct = st->resampler_ptr == resampler_basic_direct_single;
		uint32_t used = direct ? st->filt_len * st->den_rate : st->filt_len * st->oversample + 8, i;
		uint32_t cutoff_bits;
		memcpy(&cutoff_bits, &st->cutoff, sizeof cutoff_bits);
		if (table)
			for (i = 0; i < used; i++)
				hz_put_i16(table, st->sinc_table[i]);
		snprintf(state, sizeof state,
		         "{\"filt_len\": %" PRIu32 ", \"oversample\": %" PRIu32 ", \"direct\": %d, "
		         "\"int_advance\": %d, \"frac_advance\": %d, \"cutoff_bits\": %" PRIu32 ", "
		         "\"num_rate\": %" PRIu32 ", \"den_rate\": %" PRIu32 ", \"mem_alloc_size\": %" PRIu32 ", "
		         "\"table_len\": %" PRIu32 "}",
		         st->filt_len, st->oversample, direct, st->int_advance, st->frac_advance, cutoff_bits,
		         st->num_rate, st->den_rate, st->mem_alloc_size, used);
		put_state_rec(tr, T_INIT, err, st);
		if (c->mode == 'i' || c->mode == 'f' || c->mode == 'm')
			run_interleaved(c, st, out, tr);
		else
			run_split(c, st, out, tr);
		speex_resampler_destroy(st);
	} else {
		put_rec(tr, T_INIT, err, 0, 0, 0, 0);
	}
	json_events(ev, sizeof ev, c);
	snprintf(params, sizeof params,
	         "{\"mode\": \"%c\", \"frac\": %d, \"q\": %d, \"ch\": %" PRIu32 ", \"num\": %" PRIu32 ", "
	         "\"den\": %" PRIu32 ", \"in_rate\": %" PRIu32 ", \"out_rate\": %" PRIu32 ", "
	         "\"frames\": %" PRIu32 ", \"max_calls\": %" PRIu32 ", \"events\": %s, \"init_err\": %d, \"state\": %s}",
	         c->mode, c->frac, c->q, c->ch, c->num, c->den, c->in_rate, c->out_rate, c->frames, c->max_calls, ev, err,
	         state);
	hz_case_end(params);
}

/* Case table. */
static rcase cases[1024];
static int ncases;
static char names[1024][96];

static const uint32_t rates[7] = {8000, 16000, 24000, 32000, 44100, 48000, 96000};

static rcase *add(char mode, int frac, int q, uint32_t ch, uint32_t num, uint32_t den,
                  uint32_t in_rate, uint32_t out_rate, uint32_t frames, const char *fmt, ...)
	__attribute__((format(printf, 10, 11)));

static rcase *add(char mode, int frac, int q, uint32_t ch, uint32_t num, uint32_t den,
                  uint32_t in_rate, uint32_t out_rate, uint32_t frames, const char *fmt, ...)
{
	va_list ap;
	rcase *c;
	if (ncases == (int)(sizeof cases / sizeof cases[0]))
		hz_die("too many cases");
	c = &cases[ncases];
	memset(c, 0, sizeof *c);
	va_start(ap, fmt);
	vsnprintf(names[ncases], sizeof names[0], fmt, ap);
	va_end(ap);
	c->name = names[ncases++];
	c->mode = mode;
	c->frac = frac;
	c->q = q;
	c->ch = ch;
	c->num = num;
	c->den = den;
	c->in_rate = in_rate;
	c->out_rate = out_rate;
	c->frames = frames;
	return c;
}

static rcase *pair(char mode, int q, uint32_t ch, uint32_t in, uint32_t out, uint32_t frames, const char *fmt, ...)
	__attribute__((format(printf, 7, 8)));

static rcase *pair(char mode, int q, uint32_t ch, uint32_t in, uint32_t out, uint32_t frames, const char *fmt, ...)
{
	char name[96];
	va_list ap;
	va_start(ap, fmt);
	vsnprintf(name, sizeof name, fmt, ap);
	va_end(ap);
	return add(mode, 0, q, ch, in, out, in, out, frames, "%s", name);
}

static void ev(rcase *c, int call, int op, uint32_t a, uint32_t b, uint32_t cc, uint32_t d)
{
	event *e;
	if (c->nev == MAX_EVENTS)
		hz_die("too many events in %s", c->name);
	e = &c->ev[c->nev++];
	e->call = call;
	e->op = op;
	e->a = a;
	e->b = b;
	e->c = cc;
	e->d = d;
}

static int is_commit(int q, uint32_t in, uint32_t out)
{
	return (q == 3 && in == 48000 && out == 16000) || (q == 10 && in == 44100 && out == 48000) ||
	       (q == 5 && in == 8000 && out == 16000) || (q == 10 && in == 96000 && out == 8000) ||
	       (q == 0 && in == 16000 && out == 48000);
}

static void build_cases(void)
{
	static const uint32_t fr[10][2] = {
		{48000, 47999}, {47999, 48000}, {44101, 44100}, {1000003, 999983}, {7, 5},
		{5, 7}, {1, 100}, {100, 1}, {2147483646u, 2147483647u}, {1000000007u, 3000000019u}};
	static const uint32_t st2[8][2] = {
		{8000, 16000}, {16000, 8000}, {16000, 48000}, {48000, 16000},
		{44100, 48000}, {48000, 44100}, {48000, 8000}, {96000, 44100}};
	static const uint32_t fl[4][2] = {{8000, 96000}, {48000, 16000}, {44100, 48000}, {16000, 48000}};
	static const int fq[5] = {0, 3, 5, 8, 10};
	static const uint32_t ev3[3][2] = {{48000, 16000}, {16000, 48000}, {44100, 48000}};
	int q, i, j, call;
	uint32_t ch;
	rcase *c;

	/* 1. Rate pairs, mono. */
	for (q = 0; q <= 10; q++)
		for (i = 0; i < 7; i++)
			for (j = 0; j < 7; j++) {
				c = pair('i', q, 1, rates[i], rates[j], rates[i] * 12 / 100, "q%d_%u_%u", q, rates[i], rates[j]);
				c->commit = is_commit(q, rates[i], rates[j]);
			}
	/* 2. Fractional ratios, mono. */
	for (q = 0; q <= 10; q++)
		for (i = 0; i < 10; i++) {
			c = add('i', 1, q, 1, fr[i][0], fr[i][1], fr[i][0], fr[i][1], 4800, "q%d_frac_%u_%u", q, fr[i][0], fr[i][1]);
			c->commit = q == 4 && i == 0;
		}
	/* 3. Constructor failures. */
	add('i', 1, 0, 1, 600000000u, 1, 600000000u, 1, 4800, "initfail_q0_frac_600000000_1");
	add('i', 1, 10, 1, 600000000u, 1, 600000000u, 1, 4800, "initfail_q10_frac_600000000_1");
	add('i', 1, 5, 1, 2147483647u, 2147483646u, 2147483647u, 2147483646u, 4800, "initfail_q5_frac_2147483647_2147483646");
	add('i', 1, 5, 1, 4294967295u, 4294967294u, 4294967295u, 4294967294u, 4800, "initfail_q5_frac_4294967295_4294967294");
	add('i', 1, 10, 1, 6300001, 3, 6300001, 3, 4800, "initfail_q10_frac_6300001_3");
	add('i', 0, 0, 7000000, 48000, 48000, 48000, 48000, 1, "initfail_q0_48000_48000_7000000ch");
	/* 16*den_rate wraps in uint32 and halves the oversampling at quality 0;
	 * at quality 1 the filter length overflows multiply_frac. */
	add('i', 1, 0, 1, 270000001u, 270000000u, 270000001u, 270000000u, 4800, "q0_frac_270000001_270000000");
	add('i', 1, 1, 1, 270000001u, 270000000u, 270000001u, 270000000u, 4800, "initfail_q1_frac_270000001_270000000");
	pair('m', 5, 2, 44100, 48000, 5292, "mixed_q5_44100_48000_2ch");
	pair('m', 10, 3, 48000, 16000, 5760, "mixed_q10_48000_16000_3ch");
	/* den_rate + frac_advance above 2^32: samp_frac_num wraps and the input
	 * position stops advancing, so the case runs a fixed number of calls. */
	for (q = 0; q <= 10; q += 10) {
		c = add('i', 1, q, 1, 4294967294u, 4294967295u, 4294967294u, 4294967295u, 4800,
		        "wrap_q%d_frac_4294967294_4294967295", q);
		c->max_calls = 50;
	}
	/* 4. Stereo. */
	for (q = 0; q <= 10; q++)
		for (i = 0; i < 8; i++) {
			c = pair('i', q, 2, st2[i][0], st2[i][1], st2[i][0] * 12 / 100, "q%d_%u_%u_2ch", q, st2[i][0], st2[i][1]);
			c->commit = q == 5 && i == 4;
		}
	/* 5. float32 interleaved. */
	for (q = 0; q < 5; q++) {
		for (i = 0; i < 4; i++)
			pair('f', fq[q], 1, fl[i][0], fl[i][1], fl[i][0] * 12 / 100, "f_q%d_%u_%u", fq[q], fl[i][0], fl[i][1]);
		pair('f', fq[q], 2, 44100, 48000, 5292, "f_q%d_44100_48000_2ch", fq[q]);
	}
	/* 6. Per-channel calls with strides. */
	for (q = 3; q <= 10; q += 7)
		for (ch = 2; ch <= 3; ch++) {
			pair('s', q, ch, 48000, 16000, 5760, "split_q%d_48000_16000_%uch", q, ch);
			pair('s', q, ch, 44100, 48000, 5292, "split_q%d_44100_48000_%uch", q, ch);
		}
	pair('g', 5, 2, 44100, 48000, 5292, "splitf_q5_44100_48000_2ch");
	pair('g', 10, 3, 48000, 16000, 5760, "splitf_q10_48000_16000_3ch");
	/* 7. Events. */
	for (q = 0; q <= 10; q += 5)
		for (i = 0; i < 3; i++) {
			c = pair('i', q, 1, ev3[i][0], ev3[i][1], EV_FRAMES, "skip_q%d_%u_%u", q, ev3[i][0], ev3[i][1]);
			ev(c, 0, E_SKIP, 0, 0, 0, 0);
		}
	for (i = 0; i < 4; i++) {
		static const uint32_t qc[4][3] = {{48000, 16000, 1}, {16000, 48000, 1}, {44100, 48000, 1}, {44100, 48000, 2}};
		c = pair('i', 3, qc[i][2], qc[i][0], qc[i][1], EV_FRAMES, "qchange_%u_%u_%uch", qc[i][0], qc[i][1], qc[i][2]);
		ev(c, 20, E_QUALITY, 10, 0, 0, 0);
		ev(c, 40, E_QUALITY, 0, 0, 0, 0);
		ev(c, 60, E_QUALITY, 7, 0, 0, 0);
	}
	for (ch = 1; ch <= 2; ch++) {
		c = pair('i', 5, ch, 44100, 48000, EV_FRAMES, "qburst_44100_48000_%uch", ch);
		ev(c, 15, E_QUALITY, 10, 0, 0, 0);
		ev(c, 15, E_QUALITY, 0, 0, 0, 0);
		ev(c, 15, E_QUALITY, 10, 0, 0, 0);
		ev(c, 15, E_QUALITY, 3, 0, 0, 0);
		ev(c, 30, E_QUALITY, 3, 0, 0, 0);
		ev(c, 45, E_QUALITY, 0, 0, 0, 0);
		ev(c, 45, E_QUALITY, 9, 0, 0, 0);
		ev(c, 60, E_QUALITY, 10, 0, 0, 0);
		ev(c, 61, E_QUALITY, 5, 0, 0, 0);
		ev(c, 61, E_QUALITY, 0, 0, 0, 0);
	}
	for (i = 0; i < 3; i++) {
		q = i == 2 ? 10 : 5;
		ch = i == 1 ? 2 : 1;
		c = pair('i', q, ch, 48000, 16000, EV_FRAMES, "rate_q%d_48000_16000_%uch", q, ch);
		ev(c, 20, E_RATE, 48000, 8000, 0, 0);
		ev(c, 40, E_RATE, 48000, 32000, 0, 0);
		ev(c, 60, E_RATE, 44100, 48000, 0, 0);
		ev(c, 70, E_RATE, 44100, 48000, 0, 0);
		ev(c, 70, E_RATE_FRAC, 44100, 48000, 44100, 48000);
		ev(c, 80, E_RATE, 48000, 16000, 0, 0);
		ev(c, 90, E_RATE, 16000, 48000, 0, 0);
		ev(c, 100, E_RATE, 48000, 8000, 0, 0);
		ev(c, 101, E_RATE, 48000, 16000, 0, 0);
		ev(c, 101, E_RATE, 48000, 32000, 0, 0);
	}
	for (i = 0; i < 3; i++) {
		q = i == 2 ? 10 : 5;
		ch = i == 1 ? 2 : 1;
		c = pair('i', q, ch, 48000, 48000, EV_FRAMES, "drift_q%d_48000_48000_%uch", q, ch);
		for (call = 5, j = 0; call <= 100; call += 5, j++) {
			static const uint32_t num[3] = {48001, 47999, 48000};
			ev(c, call, E_RATE_FRAC, num[j % 3], 48000, 48000, 48000);
		}
	}
	c = pair('i', 5, 1, 48000, 16000, EV_FRAMES, "reset_q5_48000_16000_1ch");
	ev(c, 30, E_RESET, 0, 0, 0, 0);
	c = pair('i', 5, 2, 48000, 16000, EV_FRAMES, "reset_q5_48000_16000_2ch");
	ev(c, 30, E_RESET, 0, 0, 0, 0);
	c = pair('i', 10, 2, 16000, 48000, EV_FRAMES, "reset_q10_16000_48000_2ch");
	ev(c, 30, E_RESET, 0, 0, 0, 0);
	c = pair('i', 3, 3, 44100, 48000, EV_FRAMES, "reset_q3_44100_48000_3ch");
	ev(c, 30, E_RESET, 0, 0, 0, 0);
	c = pair('i', 5, 1, 48000, 16000, EV_FRAMES, "resetq_q5_48000_16000_1ch");
	ev(c, 30, E_RESET, 0, 0, 0, 0);
	ev(c, 30, E_QUALITY, 8, 0, 0, 0);
	for (ch = 1; ch <= 2; ch++) {
		c = pair('i', 10, ch, 44100, 48000, EV_FRAMES, "fail_q10_44100_48000_%uch", ch);
		ev(c, 20, E_RATE_FRAC, 600000000u, 1, 44100, 48000);
		ev(c, 30, E_RATE, 44100, 48000, 0, 0);
	}
	for (ch = 1; ch <= 2; ch++) {
		c = add('i', 1, 5, ch, 1000003, 999983, 48000, 48000, EV_FRAMES, "overflow_q5_frac_%uch", ch);
		ev(c, 25, E_RATE_FRAC, 999983, 1000003, 48000, 48000);
	}
}

int main(int argc, char **argv)
{
	int i;
	if (argc != 3)
		hz_die("usage: resample OUT_DIR IN_DIR");
	make_signals();
	build_cases();
	hz_manifest_open(argv[1], "resample", "reference/drivers/resample.c");
	signal_case();
	for (i = 0; i < ncases; i++)
		run_case(&cases[i]);
	hz_manifest_close();
	return 0;
}
