/* Replaces the configure-generated speexdsp_config_types.h on platforms where
 * speex/speexdsp_types.h falls back to it (Linux). macOS uses its own branch
 * in that header, with the same widths. */
#ifndef __SPEEX_TYPES_H__
#define __SPEEX_TYPES_H__

#include <stdint.h>

typedef int16_t spx_int16_t;
typedef uint16_t spx_uint16_t;
typedef int32_t spx_int32_t;
typedef uint32_t spx_uint32_t;

#endif
