/* Reference driver for internal/fixed: evaluates the SpeexDSP fixed-point
 * macros and inline functions over the input sets pre-registered in
 * docs/acceptance/fixed.md and writes testdata/speexdsp/fixed/.
 *
 * harness-sources:
 * harness-inputs: ./internal/fixed
 *
 * The case table must match internal/fixed/reference_test.go, which
 * regenerates the same inputs and checks the input hashes first. */
#include "config.h"

#include <inttypes.h>
#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* FLOAT_DIVU warns through this for a non-positive divisor; count instead of
 * printing. It does not affect any result. */
#define OVERRIDE_SPEEX_WARNING_INT
static long warnings;
static inline void speex_warning_int(const char *str, int val)
{
	(void)str;
	(void)val;
	warnings++;
}

#include "arch.h"
#include "math_approx.h"
#include "pseudofloat.h"

#include "harness.h"

#define RANDOM 10000000
#define PREFIX 4096
#define EXHAUSTIVE_MAX (1 << 24)

enum { T_I16, T_I32, T_U32, T_PFE };
typedef struct {
	int t;
	int64_t lo, hi;
} kind;

#define I16 {T_I16, -32768, 32767}
#define I32 {T_I32, INT32_MIN, INT32_MAX}
#define U32 {T_U32, 0, UINT32_MAX}
#define PFE {T_PFE, -32768, 32767}
#define R16(lo, hi) {T_I16, lo, hi}
#define R32(lo, hi) {T_I32, lo, hi}
#define RPFE(lo, hi) {T_PFE, lo, hi}

enum { M_EXHAUSTIVE, M_EDGES_RANDOM, M_EDGES_SWEEP_RANDOM, M_EDGES };
static const char *mode_names[] = {"exhaustive", "edges+random",
                                   "edges+sweep16+random", "edges"};

typedef void (*opfn)(const int64_t *a, int32_t *o);
typedef int (*validfn)(const int64_t *a);

typedef struct {
	const char *name, *macro, *upstream;
	int mode;
	int nargs;
	kind args[4];
	int nout;
	opfn op;
	validfn valid;
} fcase;

static spx_float_t pf(int64_t m, int64_t e)
{
	spx_float_t r;
	r.m = (spx_int16_t)m;
	r.e = (spx_int16_t)e;
	return r;
}

static void put_pf(spx_float_t r, int32_t *o)
{
	o[0] = r.m;
	o[1] = r.e;
}

#define W16(i) ((spx_word16_t)a[i])
#define W32(i) ((spx_word32_t)a[i])
#define PF(i) pf(a[i], a[(i) + 1])

/* Operations, one per case. */
static void op_abs16(const int64_t *a, int32_t *o) { spx_word16_t x = W16(0); o[0] = ABS16(x); }
static void op_neg16(const int64_t *a, int32_t *o) { spx_word16_t x = W16(0); o[0] = NEG16(x); }
static void op_extend32(const int64_t *a, int32_t *o) { o[0] = EXTEND32(W16(0)); }
static void op_cos(const int64_t *a, int32_t *o) { o[0] = spx_cos(W16(0)); }
static void op_exp(const int64_t *a, int32_t *o) { o[0] = spx_exp(W16(0)); }
static void op_exp2(const int64_t *a, int32_t *o) { o[0] = spx_exp2(W16(0)); }
static void op_atan01(const int64_t *a, int32_t *o) { o[0] = spx_atan01(W16(0)); }
static void op_cos_pi_2(const int64_t *a, int32_t *o) { o[0] = _spx_cos_pi_2(W16(0)); }
static void op_abs32(const int64_t *a, int32_t *o) { spx_word32_t x = W32(0); o[0] = ABS32(x); }
static void op_neg32(const int64_t *a, int32_t *o) { spx_word32_t x = W32(0); o[0] = NEG32(x); }
static void op_extract16(const int64_t *a, int32_t *o) { o[0] = EXTRACT16(W32(0)); }
static void op_word2int(const int64_t *a, int32_t *o) { spx_word32_t x = W32(0); o[0] = WORD2INT(x); }
static void op_sqrt(const int64_t *a, int32_t *o) { o[0] = spx_sqrt(W32(0)); }
static void op_atan(const int64_t *a, int32_t *o) { o[0] = spx_atan(W32(0)); }
static void op_cos_norm(const int64_t *a, int32_t *o) { o[0] = spx_cos_norm(W32(0)); }
static void op_pseudofloat(const int64_t *a, int32_t *o) { put_pf(PSEUDOFLOAT(W32(0)), o); }
static void op_ilog2(const int64_t *a, int32_t *o) { o[0] = spx_ilog2((spx_uint32_t)a[0]); }
static void op_ilog4(const int64_t *a, int32_t *o) { o[0] = spx_ilog4((spx_uint32_t)a[0]); }

static void op_add16(const int64_t *a, int32_t *o) { o[0] = ADD16(W16(0), W16(1)); }
static void op_sub16(const int64_t *a, int32_t *o) { o[0] = SUB16(W16(0), W16(1)); }
static void op_add32(const int64_t *a, int32_t *o) { o[0] = ADD32(W32(0), W32(1)); }
static void op_sub32(const int64_t *a, int32_t *o) { o[0] = SUB32(W32(0), W32(1)); }
static void op_min16(const int64_t *a, int32_t *o) { spx_word16_t x = W16(0), y = W16(1); o[0] = MIN16(x, y); }
static void op_max16(const int64_t *a, int32_t *o) { spx_word16_t x = W16(0), y = W16(1); o[0] = MAX16(x, y); }
static void op_min32(const int64_t *a, int32_t *o) { spx_word32_t x = W32(0), y = W32(1); o[0] = MIN32(x, y); }
static void op_max32(const int64_t *a, int32_t *o) { spx_word32_t x = W32(0), y = W32(1); o[0] = MAX32(x, y); }
static void op_mult16_16(const int64_t *a, int32_t *o) { o[0] = MULT16_16(W16(0), W16(1)); }
static void op_mult16_16_q14(const int64_t *a, int32_t *o) { o[0] = MULT16_16_Q14(W16(0), W16(1)); }
static void op_mult16_16_q15(const int64_t *a, int32_t *o) { o[0] = MULT16_16_Q15(W16(0), W16(1)); }
static void op_mult16_16_p13(const int64_t *a, int32_t *o) { o[0] = MULT16_16_P13(W16(0), W16(1)); }
static void op_mult16_16_p14(const int64_t *a, int32_t *o) { o[0] = MULT16_16_P14(W16(0), W16(1)); }
static void op_mult16_16_p15(const int64_t *a, int32_t *o) { o[0] = MULT16_16_P15(W16(0), W16(1)); }
static void op_mult16_32_q15(const int64_t *a, int32_t *o) { o[0] = MULT16_32_Q15(W16(0), W32(1)); }
static void op_mult16_32_p15(const int64_t *a, int32_t *o) { o[0] = MULT16_32_P15(W16(0), W32(1)); }
static void op_mac16_16(const int64_t *a, int32_t *o) { o[0] = MAC16_16(W32(0), W16(1), W16(2)); }
static void op_mac16_32_q15(const int64_t *a, int32_t *o) { o[0] = MAC16_32_Q15(W32(0), W16(1), W32(2)); }
static void op_shr16(const int64_t *a, int32_t *o) { spx_word16_t x = W16(0); o[0] = SHR16(x, (int)a[1]); }
static void op_shl16(const int64_t *a, int32_t *o) { spx_word16_t x = W16(0); o[0] = SHL16(x, (int)a[1]); }
static void op_pshr16(const int64_t *a, int32_t *o) { spx_word16_t x = W16(0); o[0] = PSHR16(x, (int)a[1]); }
static void op_shr32(const int64_t *a, int32_t *o) { spx_word32_t x = W32(0); o[0] = SHR32(x, (int)a[1]); }
static void op_shl32(const int64_t *a, int32_t *o) { spx_word32_t x = W32(0); o[0] = SHL32(x, (int)a[1]); }
static void op_pshr32(const int64_t *a, int32_t *o) { spx_word32_t x = W32(0); o[0] = PSHR32(x, (int)a[1]); }
static void op_vshr32(const int64_t *a, int32_t *o) { spx_word32_t x = W32(0); o[0] = VSHR32(x, (int)a[1]); }
static void op_saturate32(const int64_t *a, int32_t *o) { spx_word32_t x = W32(0), m = W32(1); o[0] = SATURATE32(x, m); }
static void op_saturate32pshr(const int64_t *a, int32_t *o) { spx_word32_t x = W32(0), m = W32(2); o[0] = SATURATE32PSHR(x, (int)a[1], m); }
static void op_div32_16(const int64_t *a, int32_t *o) { o[0] = DIV32_16(W32(0), W16(1)); }
static void op_pdiv32_16(const int64_t *a, int32_t *o) { o[0] = PDIV32_16(W32(0), W16(1)); }
static void op_div32(const int64_t *a, int32_t *o) { o[0] = DIV32(W32(0), W32(1)); }
static void op_pdiv32(const int64_t *a, int32_t *o) { o[0] = PDIV32(W32(0), W32(1)); }

static void op_float_add(const int64_t *a, int32_t *o) { put_pf(FLOAT_ADD(PF(0), PF(2)), o); }
static void op_float_sub(const int64_t *a, int32_t *o) { put_pf(FLOAT_SUB(PF(0), PF(2)), o); }
static void op_float_lt(const int64_t *a, int32_t *o) { o[0] = FLOAT_LT(PF(0), PF(2)); }
static void op_float_gt(const int64_t *a, int32_t *o) { o[0] = FLOAT_GT(PF(0), PF(2)); }
static void op_float_mult(const int64_t *a, int32_t *o) { put_pf(FLOAT_MULT(PF(0), PF(2)), o); }
static void op_float_amult(const int64_t *a, int32_t *o) { put_pf(FLOAT_AMULT(PF(0), PF(2)), o); }
static void op_float_shl(const int64_t *a, int32_t *o) { put_pf(FLOAT_SHL(PF(0), (int)a[2]), o); }
static void op_float_extract16(const int64_t *a, int32_t *o) { o[0] = FLOAT_EXTRACT16(PF(0)); }
static void op_float_extract32(const int64_t *a, int32_t *o) { o[0] = FLOAT_EXTRACT32(PF(0)); }
static void op_float_mul32(const int64_t *a, int32_t *o) { o[0] = FLOAT_MUL32(PF(0), W32(2)); }
static void op_float_mul32u(const int64_t *a, int32_t *o) { put_pf(FLOAT_MUL32U(W32(0), W32(1)), o); }
static void op_float_div32_float(const int64_t *a, int32_t *o) { put_pf(FLOAT_DIV32_FLOAT(W32(0), PF(1)), o); }
static void op_float_div32(const int64_t *a, int32_t *o) { put_pf(FLOAT_DIV32(W32(0), W32(1)), o); }
static void op_float_divu(const int64_t *a, int32_t *o) { put_pf(FLOAT_DIVU(PF(0), PF(2)), o); }
static void op_float_sqrt(const int64_t *a, int32_t *o) { put_pf(FLOAT_SQRT(PF(0)), o); }

/* Validity predicates exclude inputs whose C result is undefined. Unsigned
 * arithmetic keeps the driver's own code free of overflow. */
static int32_t wrap_add(int64_t x, int64_t y)
{
	return (int32_t)(uint32_t)((uint32_t)(int32_t)x + (uint32_t)(int32_t)y);
}
static int valid_div(const int64_t *a)
{
	return a[1] != 0 && !(a[0] == INT32_MIN && a[1] == -1);
}
/* PDIV32 and PDIV32_16 add the divisor's int16 half before dividing. */
static int valid_pdiv(const int64_t *a)
{
	return a[1] != 0 &&
	       !(wrap_add(a[0], (int16_t)a[1] >> 1) == INT32_MIN && a[1] == -1);
}

#define U1(k) 1, {k}
#define U2(k1, k2) 2, {k1, k2}
#define U3(k1, k2, k3) 3, {k1, k2, k3}
#define U4(k1, k2, k3, k4) 4, {k1, k2, k3, k4}

static const fcase cases[] = {
	{"abs16", "ABS16", "arch.h:74", M_EXHAUSTIVE, U1(I16), 1, op_abs16, NULL},
	{"neg16", "NEG16", "fixed_generic.h:41", M_EXHAUSTIVE, U1(I16), 1, op_neg16, NULL},
	{"extend32", "EXTEND32", "fixed_generic.h:44", M_EXHAUSTIVE, U1(I16), 1, op_extend32, NULL},
	{"cos", "spx_cos", "math_approx.h:188", M_EXHAUSTIVE, U1(I16), 1, op_cos, NULL},
	{"exp", "spx_exp", "math_approx.h:265", M_EXHAUSTIVE, U1(I16), 1, op_exp, NULL},
	{"exp2", "spx_exp2", "math_approx.h:250", M_EXHAUSTIVE, U1(I16), 1, op_exp2, NULL},
	{"atan01", "spx_atan01", "math_approx.h:279", M_EXHAUSTIVE, U1(I16), 1, op_atan01, NULL},
	{"cos_pi_2", "_spx_cos_pi_2", "math_approx.h:208", M_EXHAUSTIVE, U1(I16), 1, op_cos_pi_2, NULL},
	{"abs32", "ABS32", "arch.h:77", M_EDGES_SWEEP_RANDOM, U1(I32), 1, op_abs32, NULL},
	{"neg32", "NEG32", "fixed_generic.h:42", M_EDGES_SWEEP_RANDOM, U1(I32), 1, op_neg32, NULL},
	{"extract16", "EXTRACT16", "fixed_generic.h:43", M_EDGES_SWEEP_RANDOM, U1(I32), 1, op_extract16, NULL},
	{"word2int", "WORD2INT", "arch.h:104", M_EDGES_SWEEP_RANDOM, U1(I32), 1, op_word2int, NULL},
	{"sqrt", "spx_sqrt", "math_approx.h:142", M_EDGES_SWEEP_RANDOM, U1(I32), 1, op_sqrt, NULL},
	{"atan", "spx_atan", "math_approx.h:290", M_EDGES_SWEEP_RANDOM, U1(I32), 1, op_atan, NULL},
	{"cos_norm", "spx_cos_norm", "math_approx.h:216", M_EDGES_SWEEP_RANDOM, U1(I32), 1, op_cos_norm, NULL},
	{"pseudofloat", "PSEUDOFLOAT", "pseudofloat.h:64", M_EDGES_SWEEP_RANDOM, U1(I32), 2, op_pseudofloat, NULL},
	{"ilog2", "spx_ilog2", "math_approx.h:64", M_EDGES_SWEEP_RANDOM, U1(U32), 1, op_ilog2, NULL},
	{"ilog4", "spx_ilog4", "math_approx.h:94", M_EDGES_SWEEP_RANDOM, U1(U32), 1, op_ilog4, NULL},
	{"add16", "ADD16", "fixed_generic.h:65", M_EDGES_RANDOM, U2(I16, I16), 1, op_add16, NULL},
	{"sub16", "SUB16", "fixed_generic.h:66", M_EDGES_RANDOM, U2(I16, I16), 1, op_sub16, NULL},
	{"add32", "ADD32", "fixed_generic.h:67", M_EDGES_RANDOM, U2(I32, I32), 1, op_add32, NULL},
	{"sub32", "SUB32", "fixed_generic.h:68", M_EDGES_RANDOM, U2(I32, I32), 1, op_sub32, NULL},
	{"min16", "MIN16", "arch.h:75", M_EDGES_RANDOM, U2(I16, I16), 1, op_min16, NULL},
	{"max16", "MAX16", "arch.h:76", M_EDGES_RANDOM, U2(I16, I16), 1, op_max16, NULL},
	{"min32", "MIN32", "arch.h:78", M_EDGES_RANDOM, U2(I32, I32), 1, op_min32, NULL},
	{"max32", "MAX32", "arch.h:79", M_EDGES_RANDOM, U2(I32, I32), 1, op_max32, NULL},
	{"mult16_16", "MULT16_16", "fixed_generic.h:77", M_EDGES_RANDOM, U2(I16, I16), 1, op_mult16_16, NULL},
	{"mult16_16_q14", "MULT16_16_Q14", "fixed_generic.h:92", M_EDGES_RANDOM, U2(I16, I16), 1, op_mult16_16_q14, NULL},
	{"mult16_16_q15", "MULT16_16_Q15", "fixed_generic.h:93", M_EDGES_RANDOM, U2(I16, I16), 1, op_mult16_16_q15, NULL},
	{"mult16_16_p13", "MULT16_16_P13", "fixed_generic.h:95", M_EDGES_RANDOM, U2(I16, I16), 1, op_mult16_16_p13, NULL},
	{"mult16_16_p14", "MULT16_16_P14", "fixed_generic.h:96", M_EDGES_RANDOM, U2(I16, I16), 1, op_mult16_16_p14, NULL},
	{"mult16_16_p15", "MULT16_16_P15", "fixed_generic.h:97", M_EDGES_RANDOM, U2(I16, I16), 1, op_mult16_16_p15, NULL},
	{"mult16_32_q15", "MULT16_32_Q15", "fixed_generic.h:82", M_EDGES_RANDOM, U2(I16, I32), 1, op_mult16_32_q15, NULL},
	{"mult16_32_p15", "MULT16_32_P15", "fixed_generic.h:81", M_EDGES_RANDOM, U2(I16, I32), 1, op_mult16_32_p15, NULL},
	{"mac16_16", "MAC16_16", "fixed_generic.h:79", M_EDGES_RANDOM, U3(I32, I16, I16), 1, op_mac16_16, NULL},
	{"mac16_32_q15", "MAC16_32_Q15", "fixed_generic.h:83", M_EDGES_RANDOM, U3(I32, I16, I32), 1, op_mac16_32_q15, NULL},
	{"shr16", "SHR16", "fixed_generic.h:45", M_EXHAUSTIVE, U2(I16, R16(0, 15)), 1, op_shr16, NULL},
	{"shl16", "SHL16", "fixed_generic.h:46", M_EXHAUSTIVE, U2(I16, R16(0, 15)), 1, op_shl16, NULL},
	{"pshr16", "PSHR16", "fixed_generic.h:49", M_EXHAUSTIVE, U2(I16, R16(0, 15)), 1, op_pshr16, NULL},
	{"shr32", "SHR32", "fixed_generic.h:47", M_EDGES_RANDOM, U2(I32, R16(0, 31)), 1, op_shr32, NULL},
	{"shl32", "SHL32", "fixed_generic.h:48", M_EDGES_RANDOM, U2(I32, R16(0, 31)), 1, op_shl32, NULL},
	{"pshr32", "PSHR32", "fixed_generic.h:50", M_EDGES_RANDOM, U2(I32, R16(0, 30)), 1, op_pshr32, NULL},
	{"vshr32", "VSHR32", "fixed_generic.h:51", M_EDGES_RANDOM, U2(I32, R16(-31, 31)), 1, op_vshr32, NULL},
	{"saturate32", "SATURATE32", "fixed_generic.h:53", M_EDGES_RANDOM, U2(I32, R32(0, INT32_MAX)), 1, op_saturate32, NULL},
	{"saturate32pshr", "SATURATE32PSHR", "fixed_generic.h:55", M_EDGES_RANDOM, U3(I32, R16(0, 16), R16(0, 32767)), 1, op_saturate32pshr, NULL},
	{"div32_16", "DIV32_16", "fixed_generic.h:101", M_EDGES_RANDOM, U2(I32, I16), 1, op_div32_16, valid_div},
	{"pdiv32_16", "PDIV32_16", "fixed_generic.h:102", M_EDGES_RANDOM, U2(I32, I16), 1, op_pdiv32_16, valid_pdiv},
	{"div32", "DIV32", "fixed_generic.h:103", M_EDGES_RANDOM, U2(I32, I32), 1, op_div32, valid_div},
	{"pdiv32", "PDIV32", "fixed_generic.h:104", M_EDGES_RANDOM, U2(I32, I32), 1, op_pdiv32, valid_pdiv},
	{"float_add", "FLOAT_ADD", "pseudofloat.h:97", M_EDGES_RANDOM, U4(I16, PFE, I16, PFE), 2, op_float_add, NULL},
	{"float_sub", "FLOAT_SUB", "pseudofloat.h:132", M_EDGES_RANDOM, U4(I16, PFE, I16, PFE), 2, op_float_sub, NULL},
	{"float_lt", "FLOAT_LT", "pseudofloat.h:167", M_EDGES_RANDOM, U4(I16, PFE, I16, PFE), 1, op_float_lt, NULL},
	{"float_gt", "FLOAT_GT", "pseudofloat.h:180", M_EDGES_RANDOM, U4(I16, PFE, I16, PFE), 1, op_float_gt, NULL},
	{"float_mult", "FLOAT_MULT", "pseudofloat.h:185", M_EDGES_RANDOM, U4(I16, PFE, I16, PFE), 2, op_float_mult, NULL},
	{"float_amult", "FLOAT_AMULT", "pseudofloat.h:208", M_EDGES_RANDOM, U4(I16, PFE, I16, PFE), 2, op_float_amult, NULL},
	{"float_shl", "FLOAT_SHL", "pseudofloat.h:217", M_EDGES_RANDOM, U3(I16, PFE, I16), 2, op_float_shl, NULL},
	{"float_extract16", "FLOAT_EXTRACT16", "pseudofloat.h:225", M_EXHAUSTIVE, U2(I16, RPFE(-31, 31)), 1, op_float_extract16, NULL},
	{"float_extract32", "FLOAT_EXTRACT32", "pseudofloat.h:233", M_EXHAUSTIVE, U2(I16, RPFE(-31, 31)), 1, op_float_extract32, NULL},
	{"float_mul32", "FLOAT_MUL32", "pseudofloat.h:241", M_EDGES_RANDOM, U3(I16, RPFE(-46, 16), I32), 1, op_float_mul32, NULL},
	{"float_mul32u", "FLOAT_MUL32U", "pseudofloat.h:246", M_EDGES_RANDOM, U2(I32, I32), 2, op_float_mul32u, NULL},
	{"float_div32_float", "FLOAT_DIV32_FLOAT", "pseudofloat.h:264", M_EDGES_RANDOM, U3(I32, R16(1, 32767), PFE), 2, op_float_div32_float, NULL},
	{"float_div32", "FLOAT_DIV32", "pseudofloat.h:286", M_EDGES_RANDOM, U2(I32, R32(1, INT32_MAX)), 2, op_float_div32, NULL},
	{"float_divu", "FLOAT_DIVU", "pseudofloat.h:314", M_EDGES_RANDOM, U4(I16, PFE, R16(1, 32767), PFE), 2, op_float_divu, NULL},
	{"float_divu_nonpositive", "FLOAT_DIVU", "pseudofloat.h:314", M_EDGES, U4(I16, PFE, R16(-32768, 0), PFE), 2, op_float_divu, NULL},
	{"float_sqrt", "FLOAT_SQRT", "pseudofloat.h:337", M_EDGES_RANDOM, U2(I16, PFE), 2, op_float_sqrt, NULL},
};

/* Input generation, mirrored in internal/fixed/reference_test.go. */

static const int64_t edges16[] = {
	-32768, -32767, -16385, -16384, -16383, -256, -2, -1, 0,
	1, 2, 255, 256, 16383, 16384, 16385, 32766, 32767,
};
static const int64_t edges32_extra[] = {
	INT32_MIN, INT32_MIN + 1, -1073741825, -1073741824, -16777216, -65537,
	-65536, -65535, -32769, 32768, 65535, 65536, 65537, 16777216, 1073741824,
	INT32_MAX - 1, INT32_MAX,
};
static const int64_t edges_pfe[] = {
	-32768, -46, -31, -30, -16, -15, -14, -1, 0, 1, 14, 15, 16, 31, 32767,
};

typedef struct {
	int64_t v[256];
	int n;
} vlist;

static void vadd(vlist *l, int64_t x, const kind *k)
{
	int i;
	if (x < k->lo || x > k->hi)
		return;
	for (i = 0; i < l->n; i++)
		if (l->v[i] == x)
			return;
	if (l->n == (int)(sizeof l->v / sizeof l->v[0]))
		hz_die("edge list overflow");
	l->v[l->n++] = x;
}

static void edges_of(const kind *k, vlist *l)
{
	size_t i;
	int b;
	l->n = 0;
	if (k->hi - k->lo + 1 <= 64) {
		int64_t x;
		for (x = k->lo; x <= k->hi; x++)
			vadd(l, x, k);
		return;
	}
	switch (k->t) {
	case T_I16:
		for (i = 0; i < sizeof edges16 / sizeof edges16[0]; i++)
			vadd(l, edges16[i], k);
		break;
	case T_I32:
		for (i = 0; i < sizeof edges16 / sizeof edges16[0]; i++)
			vadd(l, edges16[i], k);
		for (i = 0; i < sizeof edges32_extra / sizeof edges32_extra[0]; i++)
			vadd(l, edges32_extra[i], k);
		break;
	case T_U32:
		vadd(l, 0, k);
		for (b = 1; b <= 31; b++) {
			vadd(l, ((int64_t)1 << b) - 1, k);
			vadd(l, (int64_t)1 << b, k);
			vadd(l, ((int64_t)1 << b) + 1, k);
		}
		vadd(l, UINT32_MAX, k);
		break;
	default:
		for (i = 0; i < sizeof edges_pfe / sizeof edges_pfe[0]; i++)
			vadd(l, edges_pfe[i], k);
		break;
	}
	vadd(l, k->lo, k);
	vadd(l, k->hi, k);
}

static int64_t mix16(uint64_t u)
{
	unsigned w;
	int64_t mag;
	if (u & 1)
		return (int16_t)(uint16_t)(u >> 48);
	w = (unsigned)((u >> 1) & 15);
	mag = (int64_t)((u >> 32) & ((1ULL << w) - 1));
	return ((u >> 8) & 1) ? -mag : mag;
}

static int64_t mix32(uint64_t u)
{
	unsigned w;
	int64_t mag;
	if (u & 1)
		return (int32_t)(uint32_t)(u >> 32);
	w = (unsigned)((u >> 1) & 31);
	mag = (int64_t)((u >> 32) & ((1ULL << w) - 1));
	return ((u >> 8) & 1) ? -mag : mag;
}

static int64_t mixu32(uint64_t u)
{
	unsigned w;
	if (u & 1)
		return (int64_t)(uint32_t)(u >> 32);
	w = (unsigned)((u >> 1) & 31) + 1;
	return (int64_t)((u >> 32) & ((1ULL << w) - 1));
}

static int64_t draw(hz_rng *r, const kind *k)
{
	uint64_t span = (uint64_t)(k->hi - k->lo) + 1;
	if (span <= 4096)
		return k->lo + (int64_t)(hz_next(r) % span);
	for (;;) {
		uint64_t u = hz_next(r);
		int64_t v;
		switch (k->t) {
		case T_I16: v = mix16(u); break;
		case T_I32: v = mix32(u); break;
		case T_U32: v = mixu32(u); break;
		default:
			v = ((u & 7) == 0) ? (int16_t)(uint16_t)(u >> 48)
			                   : -40 + (int64_t)((u >> 3) % 81);
			break;
		}
		if (v >= k->lo && v <= k->hi)
			return v;
	}
}

static uint64_t fnv1a(const char *s)
{
	uint64_t h = 0xcbf29ce484222325ULL;
	for (; *s; s++) {
		h ^= (unsigned char)*s;
		h *= 0x100000001b3ULL;
	}
	return h;
}

static const char *kind_str(const kind *k, char *buf, size_t n)
{
	static const char *names[] = {"int16", "int32", "uint32", "exponent"};
	snprintf(buf, n, "%s[%" PRId64 ",%" PRId64 "]", names[k->t], k->lo, k->hi);
	return buf;
}

typedef struct {
	const fcase *c;
	hz_stream *in, *out;
	uint64_t tuples;
} runner;

static void emit(runner *r, const int64_t *a)
{
	int32_t o[2];
	int i;
	if (r->c->valid && !r->c->valid(a))
		return;
	for (i = 0; i < r->c->nargs; i++)
		hz_put_i32(r->in, (int32_t)(uint32_t)(uint64_t)a[i]);
	r->c->op(a, o);
	for (i = 0; i < r->c->nout; i++)
		hz_put_i32(r->out, o[i]);
	r->tuples++;
}

/* Visits the Cartesian product of lists, last argument fastest. */
static uint64_t product(runner *r, vlist *lists)
{
	int idx[4] = {0, 0, 0, 0};
	int64_t a[4];
	int n = r->c->nargs, i;
	uint64_t before = r->tuples;
	for (;;) {
		for (i = 0; i < n; i++)
			a[i] = lists[i].v[idx[i]];
		emit(r, a);
		for (i = n - 1; i >= 0; i--) {
			if (++idx[i] < lists[i].n)
				break;
			idx[i] = 0;
		}
		if (i < 0)
			break;
	}
	return r->tuples - before;
}

static void run_exhaustive(runner *r)
{
	int64_t a[4];
	int n = r->c->nargs, i;
	const kind *k = r->c->args;
	uint64_t total = 1;
	for (i = 0; i < n; i++)
		total *= (uint64_t)(k[i].hi - k[i].lo + 1);
	if (total > EXHAUSTIVE_MAX)
		hz_die("case %s: domain too large for exhaustive", r->c->name);
	for (i = 0; i < n; i++)
		a[i] = k[i].lo;
	for (;;) {
		emit(r, a);
		for (i = n - 1; i >= 0; i--) {
			if (++a[i] <= k[i].hi)
				break;
			a[i] = k[i].lo;
		}
		if (i < 0)
			break;
	}
}

static void run_case(const fcase *c)
{
	runner r;
	vlist lists[4];
	hz_rng rng;
	uint64_t nedges = 0, nsweep = 0, nrandom = 0, seed = fnv1a(c->name);
	char params[1024], args[256], kb[64];
	int i;
	size_t len = 0;

	hz_case_begin(c->name);
	r.c = c;
	r.tuples = 0;
	r.in = hz_stream_begin("in", HZ_INT32, PREFIX);
	r.out = hz_stream_begin("out", HZ_INT32, PREFIX);
	warnings = 0;
	if (c->mode == M_EXHAUSTIVE) {
		run_exhaustive(&r);
	} else {
		for (i = 0; i < c->nargs; i++)
			edges_of(&c->args[i], &lists[i]);
		nedges = product(&r, lists);
		if (c->mode == M_EDGES_SWEEP_RANDOM) {
			int64_t x, lo = c->args[0].t == T_U32 ? 0 : -32768;
			for (x = lo; x < lo + 65536; x++)
				emit(&r, &x);
			nsweep = 65536;
		}
		if (c->mode != M_EDGES) {
			uint64_t want = r.tuples + RANDOM;
			rng.state = seed;
			while (r.tuples < want) {
				int64_t a[4];
				for (i = 0; i < c->nargs; i++)
					a[i] = draw(&rng, &c->args[i]);
				emit(&r, a);
			}
			nrandom = RANDOM;
		}
	}
	args[0] = 0;
	for (i = 0; i < c->nargs; i++) {
		len += (size_t)snprintf(args + len, sizeof args - len, "%s\"%s\"",
		                        i ? ", " : "", kind_str(&c->args[i], kb, sizeof kb));
	}
	snprintf(params, sizeof params,
	         "{\"macro\": \"%s\", \"upstream\": \"%s\", \"mode\": \"%s\", "
	         "\"args\": [%s], \"outputs\": %d, \"tuples\": %" PRIu64 ", "
	         "\"edges\": %" PRIu64 ", \"sweep\": %" PRIu64 ", \"random\": %" PRIu64 ", "
	         "\"seed\": \"0x%016" PRIx64 "\", \"warnings\": %ld}",
	         c->macro, c->upstream, mode_names[c->mode], args, c->nout, r.tuples,
	         nedges, nsweep, nrandom, seed, warnings);
	hz_case_end(params);
}

/* QCONST16 and QCONST32 at every literal argument pair in resample.c,
 * preprocess.c and mdf.c (filterbank.c, fftwrap.c and kiss_fft*.c have none),
 * with the FIXED_POINT values of SNR_SHIFT (8), EXPIN_SHIFT (11) and
 * NOISE_SHIFT (7). */
#define QCONST16_LIST(X) \
	X(-0.16667f, 15) X(-0.33333f, 15) X(.03, 15) X(.03f, 15) X(.05f, 15) \
	X(.075f, 15) X(.15f, 15) X(.199f, 15) X(.1f, 15) X(.2302585f, 11) \
	X(.25f, 15) X(.2f, 15) X(.3, 15) X(.333f, 15) X(.3f, 15) X(.4f, 15) \
	X(.5f, 15) X(.60f, 15) X(.6f, 15) X(.7, 15) X(.7f, 15) X(.85f, 15) \
	X(.899f, 15) X(.89f, 15) X(.8f, 15) X(.9, 15) X(.95f, 15) X(.982, 15) \
	X(.992, 15) X(.99f, 15) X(0.11513, 11) X(0.16667f, 15) X(0.20f, 15) \
	X(0.35f, 15) X(1.271903f, 14) X(1.f, 13) X(1.f, 8) X(100.f, 8) \
	X(2.4, 11) X(2.f, 13) X(3., 8) X(3.f, 13) X(4.f, 13)
#define QCONST32_LIST(X) \
	X(.1296, 23) X(1, 15) X(1, 19) X(1, 23) X(1.f, 11) X(1.f, 7)

typedef struct {
	const char *literal;
	int bits;
	int32_t value;
} qconst;

#define Q16(x, b) {#x, b, QCONST16(x, b)},
#define Q32(x, b) {#x, b, QCONST32(x, b)},
static const qconst q16[] = {QCONST16_LIST(Q16)};
static const qconst q32[] = {QCONST32_LIST(Q32)};

static void run_qconst(const char *name, const char *macro, const qconst *q,
                       size_t n)
{
	char params[4096];
	size_t len, i;
	hz_stream *out;
	hz_case_begin(name);
	out = hz_stream_begin("out", HZ_INT32, PREFIX);
	len = (size_t)snprintf(params, sizeof params,
	                       "{\"macro\": \"%s\", \"upstream\": \"fixed_generic.h:%d\", \"literals\": [",
	                       macro, macro[6] == '1' ? 38 : 39);
	for (i = 0; i < n; i++) {
		hz_put_i32(out, q[i].value);
		len += (size_t)snprintf(params + len, sizeof params - len, "%s[\"%s\", %d]",
		                        i ? ", " : "", q[i].literal, q[i].bits);
	}
	snprintf(params + len, sizeof params - len, "]}");
	hz_case_end(params);
}

/* WORD2INT on float and double inputs that the Go test writes. The
 * conversions match resample.c:1003 (float) and resample.c:280 (double). */
static void run_word2int_float(const char *in_dir)
{
	size_t n, i;
	float *f;
	double *d;
	hz_stream *in, *out;

	f = hz_read_input(in_dir, "word2int_f32.in.raw", HZ_FLOAT32, &n);
	hz_case_begin("word2int_f32");
	in = hz_stream_begin("in", HZ_FLOAT32, PREFIX);
	out = hz_stream_begin("out", HZ_INT32, PREFIX);
	for (i = 0; i < n; i++) {
		spx_word16_t r;
		if (isnan(f[i]))
			hz_die("word2int_f32: NaN input at %zu", i);
		r = WORD2INT(f[i]);
		hz_put_f32(in, f[i]);
		hz_put_i32(out, r);
	}
	hz_case_end("{\"macro\": \"WORD2INT\", \"upstream\": \"arch.h:104\", \"input\": \"word2int_f32.in.raw\"}");
	free(f);

	d = hz_read_input(in_dir, "word2int_f64.in.raw", HZ_FLOAT64, &n);
	hz_case_begin("word2int_f64");
	in = hz_stream_begin("in", HZ_FLOAT64, PREFIX);
	out = hz_stream_begin("out", HZ_INT32, PREFIX);
	for (i = 0; i < n; i++) {
		spx_word16_t r;
		if (isnan(d[i]))
			hz_die("word2int_f64: NaN input at %zu", i);
		r = WORD2INT(d[i]);
		hz_put_f64(in, d[i]);
		hz_put_i32(out, r);
	}
	hz_case_end("{\"macro\": \"WORD2INT\", \"upstream\": \"arch.h:104\", \"input\": \"word2int_f64.in.raw\"}");
	free(d);
}

int main(int argc, char **argv)
{
	size_t i;
	if (argc != 3)
		hz_die("usage: fixed OUT_DIR IN_DIR");
	hz_manifest_open(argv[1], "fixed", "reference/drivers/fixed.c");
	for (i = 0; i < sizeof cases / sizeof cases[0]; i++)
		run_case(&cases[i]);
	run_qconst("qconst16", "QCONST16", q16, sizeof q16 / sizeof q16[0]);
	run_qconst("qconst32", "QCONST32", q32, sizeof q32 / sizeof q32[0]);
	run_word2int_float(argv[2]);
	hz_manifest_close();
	return 0;
}
