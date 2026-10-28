# Decisions

Architecture decision records (ADRs) for govoip-dsp. Each one records a
decision, its context, its consequences and, where they were recorded,
the alternatives considered. Names start with the work package that raised the
decision (`docs/architecture.md`, "Work packages"), where there is one.
Some records of small, early decisions are brief, because little context
was written down at the time.

| ADR | Decision |
|---|---|
| [codecs-separate-repo](codecs-separate-repo.md) | Codecs live in a separate repository |
| [wp0-dependencies](wp0-dependencies.md) | Dependency policy |
| [wp0-go-version](wp0-go-version.md) | Go language version |
| [wp0-harness](wp0-harness.md) | Reference harness and fixture format |
| [wp0-int16-samples](wp0-int16-samples.md) | int16 samples on the 16-bit scale |
| [wp0-macro-names](wp0-macro-names.md) | Go names for the fixed-point macros |
| [wp0-reference-build](wp0-reference-build.md) | Reference build configuration and toolchain |
| [wp0-residual-handoff](wp0-residual-handoff.md) | Residual echo hand-off as a plain slice |
| [wp1-api](wp1-api.md) | Resampler reset and integer types |
| [wp1-deviations](wp1-deviations.md) | Resampler deviations where C is undefined or stalls |
| [wp1-resource-cap](wp1-resource-cap.md) | Resource cap in the resampler |
| [wp1-stopband](wp1-stopband.md) | Resampler stopband |
| [wp1c-outlen-name](wp1c-outlen-name.md) | Name of the resampler's output-length helper |
| [wp2-spsc-overflow](wp2-spsc-overflow.md) | SPSC overflow policy |
| [wp3-integer-twiddles](wp3-integer-twiddles.md) | Integer twiddles in the fixed-point FFT |
| [wp3-size-limit](wp3-size-limit.md) | FFT size limit |
| [wp3-unported-functions](wp3-unported-functions.md) | FFT functions left unported |
| [wp4-noise-protection](wp4-noise-protection.md) | AGC noise protection |
| [wp4-step-response](wp4-step-response.md) | AGC response to +10 to +13 dB steps |
| [wp5-reset](wp5-reset.md) | What the preprocessor's Reset keeps |
| [wp5-zero-divisor](wp5-zero-divisor.md) | Zero divisor in the noise suppressor |
| [wp6-rates-and-shifts](wp6-rates-and-shifts.md) | Echo canceller rates and out-of-range shifts |
| [wp6b-parallel-tests](wp6b-parallel-tests.md) | Parallel reference subtests |
| [wp7-agc-noise](wp7-agc-noise.md) | AGC noise after denoise is documented only |
| [wp7-integration-fixtures](wp7-integration-fixtures.md) | Fixtures for the whole chain |
| [wp7-race-skips](wp7-race-skips.md) | Single-goroutine tests skipped under -race |
| [wp7-recipes](wp7-recipes.md) | Two wiring recipes, with `Cancel` for paced sending |
| [wp7-report-flag](wp7-report-flag.md) | Reported-only measurements behind -report |
| [wp7-teaching-comments](wp7-teaching-comments.md) | Longer comments in teaching code |
| [wp7-zero-value-safety](wp7-zero-value-safety.md) | Zero values never panic |
