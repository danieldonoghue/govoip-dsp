/* Shared support for reference harness drivers: the SplitMix64 generator,
 * SHA-256, hashed output streams and the fixture manifest writer. The
 * manifest format is defined in docs/contracts.md ("Fixtures"), and the Go
 * reader is internal/testutil/fixture.go. */
#ifndef GOVOIP_HARNESS_H
#define GOVOIP_HARNESS_H

#include <stddef.h>
#include <stdint.h>
#include <stdio.h>

/* SplitMix64, identical to testutil.SplitMix64 in Go. */
typedef struct {
	uint64_t state;
} hz_rng;

uint64_t hz_next(hz_rng *r);

typedef struct {
	uint32_t h[8];
	uint64_t len;
	unsigned char buf[64];
	size_t nbuf;
} hz_sha256;

void hz_sha256_init(hz_sha256 *s);
void hz_sha256_update(hz_sha256 *s, const void *data, size_t n);
/* Writes 64 lowercase hex digits and a NUL into hex. */
void hz_sha256_final_hex(hz_sha256 *s, char hex[65]);

/* Element types of a stream, as named in the manifest. */
typedef enum { HZ_INT16, HZ_INT32, HZ_FLOAT32, HZ_FLOAT64 } hz_type;

#define HZ_CHUNK 1048576 /* elements per chunk hash */
#define HZ_MAX_STREAMS 8

/* A stream of little-endian elements. Its first prefix elements go into a
 * committed file; the manifest records the SHA-256 of every chunk of
 * HZ_CHUNK elements (the last chunk may be shorter). */
typedef struct {
	char name[64];
	char path[160];
	hz_type type;
	uint64_t count;
	uint64_t prefix;
	hz_sha256 chunk, file;
	uint64_t in_chunk;
	unsigned char buf[4096]; /* pending bytes of the current chunk */
	size_t nbuf;
	char **chunks;
	size_t nchunks, capchunks;
	FILE *fp;      /* committed prefix, or NULL */
	FILE *full_fp; /* complete stream when HARNESS_FULL_OUT is set */
	char file_hex[65];
} hz_stream;

/* Opens out_dir/manifest.json. The build block comes from the HARNESS_*
 * environment variables that reference/fixtures.sh sets. */
void hz_manifest_open(const char *out_dir, const char *component,
                      const char *driver);
void hz_manifest_close(void);

/* Cases hold streams. params_json is a JSON object, or NULL for none; it is
 * written at the end so it can report what the case observed. */
void hz_case_begin(const char *name);
void hz_case_end(const char *params_json);

/* Starts a stream of the current case. prefix is the number of leading
 * elements committed as <case>.<stream>.raw; 0 commits none. */
hz_stream *hz_stream_begin(const char *name, hz_type type, uint64_t prefix);
void hz_put_i16(hz_stream *s, int16_t v);
void hz_put_i32(hz_stream *s, int32_t v);
void hz_put_f32(hz_stream *s, float v);
void hz_put_f64(hz_stream *s, double v);

/* Reads a whole little-endian file of elements of the given type from
 * in_dir/name. Returns a malloc'd buffer and sets *count. Exits on error. */
void *hz_read_input(const char *in_dir, const char *name, hz_type type,
                    size_t *count);

__attribute__((noreturn, format(printf, 1, 2))) void hz_die(const char *fmt, ...);

#endif
