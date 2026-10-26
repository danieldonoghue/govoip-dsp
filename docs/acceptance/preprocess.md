# Acceptance criteria: `preprocess`

Pre-registered before the implementation, as requirements §9 demands. Changes
after the first implementation commit need a written justification in the
"Changes" section at the end of this file.

## Parity class

**Bit-exact** with SpeexDSP 1.2.1 (commit `1b28a0f6`), FIXED_POINT build,
`USE_KISS_FFT`, no SIMD, compiled by the reference harness with `-O2 -fwrapv
-ffp-contract=off`.

The reference is `preprocess.c` and `filterbank.c`, over `fftwrap.c` (ported
as `internal/fft`). Ported entry points:

| Reference | Go |
|---|---|
| `speex_preprocess_state_init` | `New` |
| `speex_preprocess_run` without an echo state | `Run` |
| `speex_preprocess_run` with `SET_ECHO_STATE` | `RunEcho`, with the residual spectrum as `[]int32` (`docs/contracts.md` section 12) |
| `speex_preprocess_estimate_update` | `EstimateUpdate` |
| `ctl` `SET/GET_DENOISE`, `SET/GET_VAD`, `SET/GET_PROB_START`, `SET/GET_PROB_CONTINUE`, `SET/GET_NOISE_SUPPRESS`, `SET/GET_ECHO_SUPPRESS`, `SET/GET_ECHO_SUPPRESS_ACTIVE` | typed setter and getter methods |
| `ctl` `GET_PSD_SIZE`, `GET_PSD`, `GET_NOISE_PSD_SIZE`, `GET_NOISE_PSD`, `GET_PROB` | methods |

Not ported, with the reason:

- AGC requests: compiled out of the FIXED_POINT build (`preprocess.c:1057`).
- `SET/GET_DEREVERB`, `SET/GET_DEREVERB_LEVEL`, `SET/GET_DEREVERB_DECAY`: the
  level and decay setters are commented out upstream (`preprocess.c:1112-1128`),
  so the reverb estimate is always zero. Nothing is exposed. The zero
  `reverb_estimate` term is still ported where the reference uses it
  (`preprocess.c:793` and `preprocess.c:1040`).
- `GET_ECHO_STATE`: there is no stored pointer in Go.
- `speex_preprocess`: a deprecated alias of `speex_preprocess_run` that ignores
  its third argument.

For every supported configuration and every input, the Go port must give:

1. the same output samples as `speex_preprocess_run`, frame after frame;
2. the same return value (the VAD decision);
3. the same raw speech probability (`st->speech_prob`, Q15) after each frame;
4. the same values from every ported getter;
5. the same filterbank tables (`bank_left`, `bank_right`, `filter_left`,
   `filter_right`) and analysis window as the reference builds at init.

## Supported configurations

- **Frame size:** every `N` for which the FFT of `2*N` samples is supported
  (`internal/fft`): 1 to 2048 with no prime factor of `N` above 17. Others
  make `spx_fft_init` fail; `New` returns an error.
- **Sampling rate:** every rate for which `filterbank_new` is defined: at most
  65535 Hz (`SHL32(sampling,15)` must not overflow and `sampling/2` must fit
  `EXTRACT16`), and at least the smallest rate for which the divisor
  `EXTRACT16(PSHR32(mel_interval,15))` (`filterbank.c:85`) is nonzero.
  The exact lower bound is derived from that expression, tested at the
  boundary, and stated in the package doc. `New` returns an error outside.
- Settings: any `int32` value for the suppress levels, and any `int` for the
  probabilities (clamped to 0 to 100, as the reference clamps them).

## Fixture matrix

The Go test owns the case table and writes it, with every Go-generated input,
into the harness input directory; the driver reads both. Every input is
recorded as a stream, so a generator mismatch shows as an input mismatch.
Each case records the output samples, the VAD decision per frame and the raw
speech probability per frame.

**MVP configurations:** 8, 16, 32 and 48 kHz, each at 10 ms and 20 ms frames.
For each:

1. `long`: at least 60 s of speech-like signal in pink noise with pauses,
   denoise on, VAD on, default levels; compared by SHA-256 chunk hashes with a
   committed prefix.
2. The other three denoise × VAD combinations (on/off, off/on, off/off).
3. Noise-suppress levels 0, -5, -30 and -70 dB, a positive level (which the
   reference negates), and the extreme `int32` values; and VAD thresholds
   (`prob_start`, `prob_continue`) of (0, 0), (100, 100) and (60, 10).
4. Edge signals with default settings: silence, full-scale white noise,
   noise of amplitude 2, a full-scale square wave, a full-scale sine, an
   impulse train of 32767 and -32768, constant 32767, constant -32768, and
   level steps (silence, noise, loud, silence).
5. `EstimateUpdate` interleaved with `Run` (a leading run of
   `EstimateUpdate` frames, then every fourth frame).
6. A case that records `PSD`, `NoisePSD` and the percent speech probability
   after every frame.
7. **With an echo residual:** the driver runs the real C echo canceller
   (`mdf.c`) on Go-generated far-end and microphone signals, warms it up, then
   records the echo canceller's output (the preprocessor input) and the
   residual spectrum of each frame (`speex_echo_get_residual`, the first
   `frame_size` values), both committed in full. Variants run against that
   recording: default levels; echo suppress -20/-5 and -60/-30 (suppress,
   active); noise suppress -30 with echo suppress -10 (the other branch of
   `compute_gain_floor`); denoise off with VAD on; and switching between the
   residual and no residual every 25 frames. The Go test feeds the recorded
   residual to `RunEcho`, standing in for WP6 until WP7 runs the real pairing.

**Other configurations,** default settings, a few seconds each: 11025 Hz with
`N` = 110, 22050 Hz and 44100 Hz with `N` = 441, 8000 Hz with `N` = 1 and 2,
48000 Hz with `N` = 2048, 16000 Hz with `N` = 256, 65535 Hz with `N` = 256,
and the lowest supported rate with `N` = 64.

**Tables:** the window and the four filterbank tables for every configuration
above, and a sweep of the filterbank tables over many rates (every 1000 Hz
from the lowest supported rate to 65000 Hz, and the boundary rates) at
several frame sizes, hashed.

**Settings:** a `ctl` case that sets and reads back each ported setting over
default, boundary, out-of-range and extreme `int32` values.

Echo cases are short (100 frames after warm-up) because their residuals must
be committed: the Go side cannot regenerate them without WP6. The 60 s runs
are without a residual.

## Pass criteria

1. Every case matches: committed prefixes element by element, and the
   SHA-256 of every chunk of every stream.
2. The canonical fixtures come from the pinned Linux container. The host
   clang build gives identical hashes, or each difference is documented as a
   finding in `docs/testing.md`.
3. The `-fsanitize=undefined` build gives identical outputs to the plain
   build; its reports are listed in `docs/testing.md`.
4. The fixed-point build of `preprocess.c` and `filterbank.c` is checked for
   floating point and libm in init; every table built from them is compared
   entry by entry with the harness (criterion 5 of the parity list).
5. `New` returns an error, and does not panic, for every unsupported frame
   size in [-1, 2100], for rates just outside the supported range, and for
   zero, negative and very large values.
6. `Run`, `RunEcho` and `EstimateUpdate` return `ErrBufferSize`, changing
   nothing, for a wrong frame length or a short residual. They never panic: a
   fuzz test over frame contents, residual contents (any `int32`) and
   settings runs without failure.
7. Every division the port makes either has a divisor shown nonzero for every
   input, tested at the closest boundary, or is listed, with how a zero can
   arise, under "Open questions" in `docs/status.md` (contracts section 8).
8. No method allocates after `New` (`testutil.AssertNoAllocs`).
9. `Reset` gives the output of a fresh instance with the same settings.
10. Switching between `Run` and `RunEcho` matches the C sequence of setting and
    clearing the echo state (fixture 7).

## Sanity signal tests (reported, not thresholded)

Bit-exactness defines correctness, so these are reported in the test log and
in `docs/algorithms.md`, not used as pass thresholds:

1. Noise reduction in dB on noise-only input (white and pink noise), after
   the first second.
2. Segmental SNR improvement on speech in noise at 0, 5 and 10 dB SNR:
   LibriSpeech when `make speech` has run (the test skips that part and says
   so otherwise), and `testutil.SpeechLike` always.
3. VAD decision rates (fraction of frames reported as speech) on silence,
   noise and speech.

## Benchmark targets

No numeric targets. `Run` and `RunEcho` are benchmarked at the eight MVP
configurations with `testutil.ReportFrameMetrics`, and `New` once; the
results go into `docs/performance.md`.

## Changes

(none)
