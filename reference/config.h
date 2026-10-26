/* Reference build configuration for govoip-dsp: SpeexDSP 1.2.1 fixed point.
 * Checked in so the configuration is explicit (docs/requirements.md §4.1).
 * It mirrors what `configure --enable-fixed-point` defines with clang, minus
 * anything that does not affect the compiled arithmetic. */
#ifndef GOVOIP_REFERENCE_CONFIG_H
#define GOVOIP_REFERENCE_CONFIG_H

#define FIXED_POINT
#define USE_KISS_FFT
#define VAR_ARRAYS
#define EXPORT

/* Deliberately not defined: FLOATING_POINT, USE_SSE, USE_SSE2, USE_NEON,
 * RESAMPLE_FULL_SINC_TABLE, FIXED_DEBUG, DISABLE_FLOAT_API, ARM4_ASM,
 * ARM5E_ASM, BFIN_ASM, TI_C55X. The guards below fail the build if a
 * compiler flag defines one of them. */
#if defined(FLOATING_POINT) || defined(USE_SSE) || defined(USE_SSE2) || \
    defined(USE_NEON) || defined(RESAMPLE_FULL_SINC_TABLE) || \
    defined(FIXED_DEBUG) || defined(DISABLE_FLOAT_API) || \
    defined(ARM4_ASM) || defined(ARM5E_ASM) || defined(BFIN_ASM) || \
    defined(TI_C55X)
#error "reference/config.h: a forbidden configuration macro is defined"
#endif

#endif
