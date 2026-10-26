/* Shared support for reference harness drivers. See harness.h. */
#include "harness.h"

#include <stdarg.h>
#include <stdlib.h>
#include <string.h>

void hz_die(const char *fmt, ...)
{
	va_list ap;
	va_start(ap, fmt);
	fputs("harness: ", stderr);
	vfprintf(stderr, fmt, ap);
	fputc('\n', stderr);
	va_end(ap);
	exit(1);
}

uint64_t hz_next(hz_rng *r)
{
	uint64_t z = (r->state += 0x9e3779b97f4a7c15ULL);
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9ULL;
	z = (z ^ (z >> 27)) * 0x94d049bb133111ebULL;
	return z ^ (z >> 31);
}

/* SHA-256 as specified in FIPS 180-4. */

static const uint32_t sha_k[64] = {
	0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1,
	0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
	0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786,
	0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
	0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147,
	0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
	0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b,
	0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
	0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a,
	0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
	0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
};

#define ROR(x, n) (((x) >> (n)) | ((x) << (32 - (n))))

static void sha_block(hz_sha256 *s, const unsigned char *p)
{
	uint32_t w[64], a, b, c, d, e, f, g, h;
	int i;
	for (i = 0; i < 16; i++)
		w[i] = (uint32_t)p[4 * i] << 24 | (uint32_t)p[4 * i + 1] << 16 |
		       (uint32_t)p[4 * i + 2] << 8 | (uint32_t)p[4 * i + 3];
	for (i = 16; i < 64; i++) {
		uint32_t s0 = ROR(w[i - 15], 7) ^ ROR(w[i - 15], 18) ^ (w[i - 15] >> 3);
		uint32_t s1 = ROR(w[i - 2], 17) ^ ROR(w[i - 2], 19) ^ (w[i - 2] >> 10);
		w[i] = w[i - 16] + s0 + w[i - 7] + s1;
	}
	a = s->h[0]; b = s->h[1]; c = s->h[2]; d = s->h[3];
	e = s->h[4]; f = s->h[5]; g = s->h[6]; h = s->h[7];
	for (i = 0; i < 64; i++) {
		uint32_t S1 = ROR(e, 6) ^ ROR(e, 11) ^ ROR(e, 25);
		uint32_t ch = (e & f) ^ (~e & g);
		uint32_t t1 = h + S1 + ch + sha_k[i] + w[i];
		uint32_t S0 = ROR(a, 2) ^ ROR(a, 13) ^ ROR(a, 22);
		uint32_t maj = (a & b) ^ (a & c) ^ (b & c);
		uint32_t t2 = S0 + maj;
		h = g; g = f; f = e; e = d + t1;
		d = c; c = b; b = a; a = t1 + t2;
	}
	s->h[0] += a; s->h[1] += b; s->h[2] += c; s->h[3] += d;
	s->h[4] += e; s->h[5] += f; s->h[6] += g; s->h[7] += h;
}

void hz_sha256_init(hz_sha256 *s)
{
	static const uint32_t h0[8] = {
		0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
		0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19,
	};
	memcpy(s->h, h0, sizeof h0);
	s->len = 0;
	s->nbuf = 0;
}

void hz_sha256_update(hz_sha256 *s, const void *data, size_t n)
{
	const unsigned char *p = data;
	s->len += n;
	if (s->nbuf > 0) {
		size_t take = 64 - s->nbuf;
		if (take > n)
			take = n;
		memcpy(s->buf + s->nbuf, p, take);
		s->nbuf += take;
		p += take;
		n -= take;
		if (s->nbuf < 64)
			return;
		sha_block(s, s->buf);
		s->nbuf = 0;
	}
	while (n >= 64) {
		sha_block(s, p);
		p += 64;
		n -= 64;
	}
	memcpy(s->buf, p, n);
	s->nbuf = n;
}

void hz_sha256_final_hex(hz_sha256 *s, char hex[65])
{
	static const char digits[] = "0123456789abcdef";
	uint64_t bits = s->len * 8;
	unsigned char pad[72];
	size_t npad = (s->nbuf < 56) ? 56 - s->nbuf : 120 - s->nbuf;
	int i;
	memset(pad, 0, sizeof pad);
	pad[0] = 0x80;
	for (i = 0; i < 8; i++)
		pad[npad + i] = (unsigned char)(bits >> (56 - 8 * i));
	hz_sha256_update(s, pad, npad + 8);
	for (i = 0; i < 32; i++) {
		unsigned char byte = (unsigned char)(s->h[i / 4] >> (24 - 8 * (i % 4)));
		hex[2 * i] = digits[byte >> 4];
		hex[2 * i + 1] = digits[byte & 15];
	}
	hex[64] = 0;
}

static void sha_selftest(void)
{
	static const struct {
		const char *in, *want;
	} v[] = {
		{"", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"abc", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{"abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq",
		 "248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1"},
	};
	size_t i;
	for (i = 0; i < sizeof v / sizeof v[0]; i++) {
		hz_sha256 s;
		char hex[65];
		hz_sha256_init(&s);
		hz_sha256_update(&s, v[i].in, strlen(v[i].in));
		hz_sha256_final_hex(&s, hex);
		if (strcmp(hex, v[i].want) != 0)
			hz_die("SHA-256 self-test failed on \"%s\"", v[i].in);
	}
}

/* Manifest writer. */

static FILE *manifest;
static char out_dir[512];
static const char *full_dir;
static int ncases;
static char case_name[64];
static hz_stream *streams[HZ_MAX_STREAMS];
static int nstreams;

static const char *type_name(hz_type t)
{
	switch (t) {
	case HZ_INT16: return "int16";
	case HZ_INT32: return "int32";
	case HZ_FLOAT32: return "float32";
	case HZ_FLOAT64: return "float64";
	}
	return "?";
}

static size_t type_size(hz_type t)
{
	return t == HZ_INT16 ? 2 : (t == HZ_FLOAT64 ? 8 : 4);
}

/* Writes s as a JSON string. Only printable ASCII is expected. */
static void json_str(const char *s)
{
	fputc('"', manifest);
	for (; *s; s++) {
		if (*s == '"' || *s == '\\')
			fputc('\\', manifest);
		if ((unsigned char)*s < 0x20)
			hz_die("control character in manifest string");
		fputc(*s, manifest);
	}
	fputc('"', manifest);
}

static const char *env_or(const char *name, const char *def)
{
	const char *v = getenv(name);
	return (v && *v) ? v : def;
}

void hz_manifest_open(const char *dir, const char *component,
                      const char *driver)
{
	char path[600];
	union {
		uint16_t u;
		unsigned char b[2];
	} endian = {1};
	if (endian.b[0] != 1)
		hz_die("big-endian hosts are not supported");
	sha_selftest();
	if (strlen(dir) >= sizeof out_dir)
		hz_die("output directory name too long");
	strcpy(out_dir, dir);
	full_dir = getenv("HARNESS_FULL_OUT");
	if (full_dir && !*full_dir)
		full_dir = NULL;
	snprintf(path, sizeof path, "%s/manifest.json", out_dir);
	manifest = fopen(path, "w");
	if (!manifest)
		hz_die("cannot create %s", path);
	fputs("{\n  \"schema\": 1,\n  \"component\": ", manifest);
	json_str(component);
	fputs(",\n  \"driver\": ", manifest);
	json_str(driver);
	fputs(",\n  \"reference\": {\n    \"repository\": \"https://github.com/xiph/speexdsp\",\n"
	      "    \"tag\": \"SpeexDSP-1.2.1\",\n"
	      "    \"commit\": \"1b28a0f61bc31162979e1f26f3981fc3637095c8\"\n  },\n",
	      manifest);
	fputs("  \"build\": {\n    \"mode\": ", manifest);
	json_str(env_or("HARNESS_MODE", "unknown"));
	fputs(",\n    \"image\": ", manifest);
	json_str(env_or("HARNESS_IMAGE", ""));
	fputs(",\n    \"compiler\": ", manifest);
	json_str(env_or("HARNESS_CC_VERSION", "unknown"));
	fputs(",\n    \"cflags\": ", manifest);
	json_str(env_or("HARNESS_CFLAGS", "unknown"));
	fputs(",\n    \"os\": ", manifest);
	json_str(env_or("HARNESS_OS", "unknown"));
	fputs(",\n    \"arch\": ", manifest);
	json_str(env_or("HARNESS_ARCH", "unknown"));
	fputs(",\n    \"config_h_sha256\": ", manifest);
	json_str(env_or("HARNESS_CONFIG_SHA256", "unknown"));
	fputs("\n  },\n  \"cases\": [", manifest);
	ncases = 0;
}

void hz_manifest_close(void)
{
	fputs("\n  ]\n}\n", manifest);
	if (fclose(manifest) != 0)
		hz_die("cannot write manifest");
	manifest = NULL;
}

void hz_case_begin(const char *name)
{
	if (strlen(name) >= sizeof case_name)
		hz_die("case name too long: %s", name);
	strcpy(case_name, name);
	fputs(ncases++ ? ",\n    {\n      \"name\": " : "\n    {\n      \"name\": ", manifest);
	json_str(name);
	nstreams = 0;
}

static void flush_buf(hz_stream *s)
{
	hz_sha256_update(&s->chunk, s->buf, s->nbuf);
	s->nbuf = 0;
}

static void chunk_done(hz_stream *s)
{
	char hex[65];
	flush_buf(s);
	if (s->nchunks == s->capchunks) {
		s->capchunks = s->capchunks ? 2 * s->capchunks : 16;
		s->chunks = realloc(s->chunks, s->capchunks * sizeof *s->chunks);
		if (!s->chunks)
			hz_die("out of memory");
	}
	hz_sha256_final_hex(&s->chunk, hex);
	s->chunks[s->nchunks] = malloc(65);
	if (!s->chunks[s->nchunks])
		hz_die("out of memory");
	memcpy(s->chunks[s->nchunks++], hex, 65);
	hz_sha256_init(&s->chunk);
	s->in_chunk = 0;
}

static void stream_end(hz_stream *s)
{
	size_t i;
	if (s->in_chunk > 0)
		chunk_done(s);
	if (s->fp) {
		if (s->count < s->prefix)
			s->prefix = s->count;
		if (fclose(s->fp) != 0)
			hz_die("cannot write %s", s->path);
		hz_sha256_final_hex(&s->file, s->file_hex);
	}
	if (s->full_fp && fclose(s->full_fp) != 0)
		hz_die("cannot write full output of %s", s->name);
	fputs("        {\n          \"name\": ", manifest);
	json_str(s->name);
	fprintf(manifest, ",\n          \"type\": \"%s\",\n          \"count\": %llu,\n"
	        "          \"chunk\": %d,\n          \"chunks\": [",
	        type_name(s->type), (unsigned long long)s->count, HZ_CHUNK);
	for (i = 0; i < s->nchunks; i++) {
		fprintf(manifest, "%s\"%s\"", i ? ", " : "", s->chunks[i]);
		free(s->chunks[i]);
	}
	free(s->chunks);
	fputs("]", manifest);
	if (s->fp) {
		fputs(",\n          \"file\": {\"path\": ", manifest);
		json_str(s->path);
		fprintf(manifest, ", \"count\": %llu, \"sha256\": \"%s\"}",
		        (unsigned long long)s->prefix, s->file_hex);
	}
	fputs("\n        }", manifest);
}

void hz_case_end(const char *params_json)
{
	int i;
	if (params_json) {
		fputs(",\n      \"params\": ", manifest);
		fputs(params_json, manifest);
	}
	fputs(",\n      \"streams\": [\n", manifest);
	for (i = 0; i < nstreams; i++) {
		if (i)
			fputs(",\n", manifest);
		stream_end(streams[i]);
		free(streams[i]);
	}
	fputs("\n      ]\n    }", manifest);
	nstreams = 0;
}

hz_stream *hz_stream_begin(const char *name, hz_type type, uint64_t prefix)
{
	hz_stream *s;
	char path[800];
	if (nstreams == HZ_MAX_STREAMS)
		hz_die("too many streams in case %s", case_name);
	s = calloc(1, sizeof *s);
	if (!s)
		hz_die("out of memory");
	if (strlen(name) >= sizeof s->name)
		hz_die("stream name too long: %s", name);
	strcpy(s->name, name);
	s->type = type;
	s->prefix = prefix;
	hz_sha256_init(&s->chunk);
	hz_sha256_init(&s->file);
	if (prefix > 0) {
		snprintf(s->path, sizeof s->path, "%s.%s.raw", case_name, name);
		snprintf(path, sizeof path, "%s/%s", out_dir, s->path);
		s->fp = fopen(path, "wb");
		if (!s->fp)
			hz_die("cannot create %s", path);
	}
	if (full_dir) {
		snprintf(path, sizeof path, "%s/%s.%s.raw", full_dir, case_name, name);
		s->full_fp = fopen(path, "wb");
		if (!s->full_fp)
			hz_die("cannot create %s", path);
	}
	streams[nstreams++] = s;
	return s;
}

static void put_bytes(hz_stream *s, const unsigned char *b, size_t n)
{
	if (s->nbuf + n > sizeof s->buf)
		flush_buf(s);
	memcpy(s->buf + s->nbuf, b, n);
	s->nbuf += n;
	if (s->fp && s->count < s->prefix) {
		hz_sha256_update(&s->file, b, n);
		if (fwrite(b, 1, n, s->fp) != n)
			hz_die("write failed for %s", s->path);
	}
	if (s->full_fp && fwrite(b, 1, n, s->full_fp) != n)
		hz_die("write failed for full output of %s", s->name);
	s->count++;
	if (++s->in_chunk == HZ_CHUNK)
		chunk_done(s);
}

static void put_le(hz_stream *s, uint64_t v, size_t n)
{
	unsigned char b[8];
	size_t i;
	if (n != type_size(s->type))
		hz_die("stream %s: element size mismatch", s->name);
	for (i = 0; i < n; i++)
		b[i] = (unsigned char)(v >> (8 * i));
	put_bytes(s, b, n);
}

void hz_put_i16(hz_stream *s, int16_t v) { put_le(s, (uint16_t)v, 2); }
void hz_put_i32(hz_stream *s, int32_t v) { put_le(s, (uint32_t)v, 4); }

void hz_put_f32(hz_stream *s, float v)
{
	uint32_t u;
	memcpy(&u, &v, 4);
	put_le(s, u, 4);
}

void hz_put_f64(hz_stream *s, double v)
{
	uint64_t u;
	memcpy(&u, &v, 8);
	put_le(s, u, 8);
}

void *hz_read_input(const char *in_dir, const char *name, hz_type type,
                    size_t *count)
{
	char path[800];
	FILE *fp;
	long n = 0;
	void *buf;
	size_t size = type_size(type);
	snprintf(path, sizeof path, "%s/%s", in_dir, name);
	fp = fopen(path, "rb");
	if (!fp)
		hz_die("cannot open input %s", path);
	if (fseek(fp, 0, SEEK_END) != 0 || (n = ftell(fp)) < 0 || fseek(fp, 0, SEEK_SET) != 0)
		hz_die("cannot size input %s", path);
	if ((size_t)n % size != 0)
		hz_die("input %s: size %ld is not a multiple of %zu", path, n, size);
	buf = malloc(n ? (size_t)n : 1);
	if (!buf)
		hz_die("out of memory");
	if (fread(buf, 1, (size_t)n, fp) != (size_t)n)
		hz_die("cannot read input %s", path);
	fclose(fp);
	*count = (size_t)n / size;
	return buf;
}
