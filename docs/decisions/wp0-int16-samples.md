# ADR wp0-int16-samples: int16 samples on the 16-bit scale

- **Status:** accepted (requirements §3); recorded by WP0.
- **Date:** 2026-10-26

## Context

The fixed-point reference works on `spx_int16_t` samples. Its `float` entry
points (`speex_resampler_process_float`) take values on the same 16-bit scale
and convert them with `WORD2INT`.

## Decision

- The native sample type is `int16` on `[-32768, 32767]`.
- `float32` entry points use the same scale, not `[-1, 1]`, and convert with
  `fixed.Word2IntFloat32` (saturate, then truncate toward zero), exactly as
  `resample.c:1003` does. NaN becomes 0, a documented deviation; `±Inf`
  saturates.
- `float32` output is an exact copy of the `int16` result.

## Consequences

- No scaling step exists between the Go API and the reference arithmetic, so
  bit-exactness carries over to the `float32` entry points.
- Callers with normalised audio multiply by 32768 themselves. The package
  docs say so.
- NaN handling is the one place where Go defines what C leaves undefined,
  and each `float32` entry point tests it.

## Alternatives considered

- **Normalised float32.** Matches common audio APIs, but adds a scaling step
  whose rounding is not in the reference, and would make the float path a
  different algorithm from the integer path.
- **int32 samples.** No reference behaviour to match, and twice the memory.
