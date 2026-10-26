// Package fixed ports the fixed-point arithmetic of SpeexDSP 1.2.1 (FIXED_POINT
// build, generic macros): the macros of arch.h and fixed_generic.h, the
// approximations of math_approx.h and the pseudo-float of pseudofloat.h. Every
// function is bit-exact with the C macro or inline function it names over the
// domains listed in docs/acceptance/fixed.md, as checked against the
// reference harness (testdata/speexdsp/fixed).
//
// # Coverage
//
// The package holds every macro and inline function that resample.c,
// kiss_fft.c, kiss_fftr.c, _kiss_fft_guts.h, fftwrap.c, filterbank.c,
// preprocess.c or mdf.c uses, directly or through another function here.
// Macros that none of them use (MULT16_16_16, MAC16_16_Q11, MAC16_16_Q13,
// MAC16_16_P13, MULT16_16_Q11_32, MULT16_16_Q13, MUL_16_32_R15, SATURATE16,
// SATURATE, SHR, SHL, PSHR, spx_acos, speex_rand, FLOAT_HALF) are not ported;
// add them, with harness tests, when a component needs them.
//
// # Names
//
// Upstream names map mechanically: words in mixed caps, "16_16" as "16x16",
// "32_16" as "32x16". Unexported functions are used only inside the package.
//
//	arch.h          ABS16 Abs16, ABS32 and ABS Abs32, MIN16 Min16, MAX16 Max16,
//	                MIN32 Min32, MAX32 Max32, WORD2INT Word2Int (integer),
//	                Word2IntFloat32 (float), Word2IntFloat64 (double),
//	                Q15ONE and Q15_ONE Q15One
//	fixed_generic.h QCONST16 QConst16, QCONST32 QConst32, NEG16 Neg16,
//	                NEG32 Neg32, EXTRACT16 Extract16, EXTEND32 Extend32,
//	                SHR16 shr16, SHL16 Shl16, SHR32 Shr32, SHL32 Shl32,
//	                PSHR16 PShr16, PSHR32 PShr32, VSHR32 VShr32,
//	                SATURATE32 Saturate32, SATURATE32PSHR Saturate32PShr,
//	                ADD16 Add16, SUB16 Sub16, ADD32 Add32, SUB32 Sub32,
//	                MULT16_16 Mult16x16, MAC16_16 Mac16x16,
//	                MULT16_32_Q15 Mult16x32Q15, MULT16_32_P15 Mult16x32P15,
//	                MAC16_32_Q15 Mac16x32Q15, MULT16_16_Q14 Mult16x16Q14,
//	                MULT16_16_Q15 Mult16x16Q15, MULT16_16_P13 mult16x16P13,
//	                MULT16_16_P14 mult16x16P14, MULT16_16_P15 Mult16x16P15,
//	                DIV32_16 Div32x16, PDIV32_16 PDiv32x16, DIV32 Div32,
//	                PDIV32 PDiv32
//	math_approx.h   spx_ilog2 ILog2, spx_ilog4 ilog4, spx_sqrt Sqrt,
//	                spx_cos Cos, _spx_cos_pi_2 cosPi2, spx_cos_norm CosNorm,
//	                spx_exp2 exp2, spx_exp Exp, spx_atan01 atan01,
//	                spx_atan Atan
//	pseudofloat.h   spx_float_t Float, FLOAT_ZERO FloatZero, FLOAT_ONE FloatOne,
//	                PSEUDOFLOAT PseudoFloat, FLOAT_ADD FloatAdd,
//	                FLOAT_SUB FloatSub, FLOAT_LT FloatLT, FLOAT_GT FloatGT,
//	                FLOAT_MULT FloatMult, FLOAT_AMULT FloatAMult,
//	                FLOAT_SHL FloatShl, FLOAT_EXTRACT16 FloatExtract16,
//	                FLOAT_EXTRACT32 FloatExtract32, FLOAT_MUL32 FloatMul32,
//	                FLOAT_MUL32U FloatMul32U,
//	                FLOAT_DIV32_FLOAT FloatDiv32Float,
//	                FLOAT_DIV32 FloatDiv32, FLOAT_DIVU FloatDivU,
//	                FLOAT_SQRT FloatSqrt
//
// # Types
//
// Arguments have the types the macro casts its operands to (spx_word16_t is
// int16, spx_word32_t is int32). Where a macro casts nothing, the argument
// type is the one every upstream call site uses, and a call site with wider
// operands uses the 32-bit variant (MIN16 on an int operand is Min32). A
// result has the C expression type after integer promotion: most macros
// yield int, which is int32 here, and do not truncate. Where C assigns such
// a result to spx_word16_t, the Go caller writes int16(...), so every
// truncation is visible. Functions return int16 only where C truncates
// (ADD16, EXTRACT16, DIV32_16, the approximations) or where the int result
// always fits (SHR16, PSHR16, MIN16, MAX16).
//
// Shift counts are uint, so a negative count cannot panic; VShr32 takes a
// signed count, as VSHR32 does.
//
// # Undefined behaviour
//
// Where C is undefined, Go follows its own rules, and the harness tests
// exclude the input (docs/acceptance/fixed.md):
//
//   - Division by zero panics: Div32x16, PDiv32x16, Div32 and PDiv32 with
//     b = 0, FloatDiv32Float with b.M = 0 and FloatDiv32 with b = 0.
//     -2^31 / -1 gives -2^31. Components must keep divisors non-zero, as
//     upstream does.
//   - A shift count at or above the operand width gives 0 or -1 in Go.
//   - QConst16 and QConst32 saturate a result that does not fit, as the
//     harness compilers fold upstream's one such literal.
//   - Word2IntFloat32 and Word2IntFloat64 map NaN to 0 (requirements §3).
//
// Signed overflow wraps in both, since the harness builds with -fwrapv, and
// left shifts of negative values are two's-complement shifts in both.
//
// # Allocation and concurrency
//
// Every function is a pure function of its arguments: no state, no
// allocation, safe for concurrent use. The macro ports are small enough to
// inline, which TestInlining checks.
package fixed
