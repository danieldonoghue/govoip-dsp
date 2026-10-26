# Performance

Measured results only, each with its environment and command. Targets are
labelled as targets. Each work package edits only its own section.

## Reference environments

| Name | Hardware | OS | Go |
|---|---|---|---|
| Mac | MacBookPro18,1, Apple M1 Pro, arm64 | macOS 27.0.1 (26A434) | go1.26.5 darwin/arm64 |
| Linux VM | Linux amd64 VMware VM (AVX2 may be absent) | not measured yet | not measured yet |

CI timings are not published; CI runs benchmarks once as a smoke test.

## Fixed-point arithmetic (`internal/fixed`, WP0)

Mac, 2026-10-26:

```sh
go test -run '^$' -bench . -benchmem -count 1 ./internal/fixed
```

| Benchmark | ns/op | allocs/op |
|---|---|---|
| `Sqrt` | 3.73 | 0 |
| `Exp` | 3.04 | 0 |
| `Atan` | 4.88 | 0 |
| `Cos` | 3.27 | 0 |
| `CosNorm` | 3.99 | 0 |
| `Mult16x32Q15` (inlined) | 2.08 | 0 |
| `PseudoFloat` | 2.40 | 0 |
| `FloatAdd` | 2.65 | 0 |
| `FloatMul32U` | 3.28 | 0 |
| `FloatDiv32` | 9.26 | 0 |
| `FloatDiv32Float` | 7.61 | 0 |
| `FloatDivU` | 3.00 | 0 |
| `FloatSqrt` | 4.46 | 0 |

Each iteration includes a table load and a store to a package-level sink, so
the numbers are upper bounds on the operation itself. All macro ports, and
`FloatLT`, `FloatMult`, `FloatAMult`, `FloatShl`, `FloatExtract16`,
`FloatExtract32` and `FloatMul32`, inline (`TestInlining`).

## Fixed-point FFT (`internal/fft`, WP3)

Mac, 2026-10-26:

```sh
CGO_ENABLED=0 go test -run '^$' -bench . -benchmem -count 1 ./internal/fft
```

| Size | `Forward` ns/op | `Inverse` ns/op | allocs/op |
|---|---|---|---|
| 160 | 1774 | 1039 | 0 |
| 320 | 3762 | 2385 | 0 |
| 640 | 7669 | 4752 | 0 |
| 960 | 13272 | 8106 | 0 |
| 1280 | 16508 | 10716 | 0 |
| 1920 | 26688 | 16452 | 0 |

`Forward` includes copying the input into the buffer it rewrites. The
benchmark reads each size as one 10 ms hop of `Size/2` samples, so
`rt-fraction` for size 1920 (48 kHz) is 0.0027 for `Forward` and 0.0016 for
`Inverse`. One run, not averaged; no Linux VM numbers yet.

## Ring buffers (`buffer`, WP2)

Mac, 2026-10-26, while other work packages ran tests on the same machine
(load average about 78 on 10 cores), so these are upper bounds with large
noise, not quiet-machine numbers. A rerun on an idle machine is still to do.

```sh
go test -run '^$' -bench . -benchmem -count 1 ./buffer
```

Each iteration is one write of the chunk followed by one read of it. The
`wrap` cases use a capacity of chunk + 1 (160) or chunk + 7 (480), so most
cycles wrap; the others use 4 × chunk, or 1000 for the odd sizes.

| Benchmark | `Ring` reject ns/op | `Ring` overwrite ns/op | `Ring` views ns/op | `SPSC` ns/op |
|---|---|---|---|---|
| 80 | 41.6 | 35.3 | 64.2 | 130.1 |
| 160 | 39.0 | 39.6 | 87.3 | 136.0 |
| 480 | 81.9 | 102.4 | 124.1 | 231.9 |
| 4096 | 405.2 | 570.2 | 376.6 | 1141 |
| odd97 | 49.6 | 64.7 | 51.4 | 120.6 |
| odd331 | 97.0 | 72.7 | 90.4 | 183.2 |
| wrap160 | 52.5 | 55.3 | 100.6 | 155.7 |
| wrap480 | 113.3 | 98.5 | 168.5 | 214.3 |

Benchmarks: `BenchmarkRingWriteRead/<case>/<policy>`,
`BenchmarkRingWriteSlices/<case>` and `BenchmarkSPSCWriteRead/<case>`.
`BenchmarkSPSCRead/handoff_480`, with a real producer goroutine, measured
670.6 ns per 480-sample frame, most of it scheduler yields while one side
waits. Every benchmark reports 0 B/op and 0 allocs/op.

Targets (`docs/acceptance/buffer.md`): 0 allocs/op everywhere, met; a
480-sample write/read cycle under 500 ns for `Ring` and for `SPSC` on one
goroutine, met (81.9 ns and 231.9 ns even under load).

### SPSC backlog drop (WP2b)

Mac, 2026-10-26, load average about 9 on 10 cores (lighter than the table
above, so the two are not comparable):

```sh
go test -run '^$' -bench 'SPSCConsume|SPSCWriteRead/480$' -benchmem -count 3 ./buffer
```

`BenchmarkSPSCConsume` times one cycle of `WriteSlices`, `Commit` (a refill
with no copy), `Len`, `Consume(Len() - 80)` and a `Consume` of the 80 kept
samples.

| Benchmark | ns/op (3 runs) | allocs/op |
|---|---|---|
| `SPSCConsume/keep_newest_80_of_480` | 31.40, 31.62, 30.61 | 0 |
| `SPSCConsume/keep_newest_80_of_4096` | 30.55, 30.72, 30.67 | 0 |
| `SPSCWriteRead/480`, same run | 46.44, 47.55, 47.55 | 0 |
| `SPSCWriteRead/wrap480`, same run | 52.66, 52.74, 52.74 | 0 |

The drop costs the same for a 480- and a 4096-sample backlog, as the
target (`docs/acceptance/buffer.md`, D6) requires: it copies nothing.

## Resampler (`resample`, WP1)

Mac, 2026-10-26, Go 1.26.5, default benchtime:

```sh
CGO_ENABLED=0 go test ./resample -run '^$' -bench . -benchmem -count 1
```

Each cell is ns/op and `rt-fraction` (processing time over the duration
of the input chunk) for one `Process` call on a chunk of the given frames
(10 ms, 20 ms, 7 or 331 frames at the input rate). Every benchmark
reports 0 B/op and 0 allocs/op.

| Config | mono 10 ms | mono 20 ms | stereo 10 ms | stereo 20 ms | mono 7 | mono 331 |
|---|---|---|---|---|---|---|
| q3 8k→16k | 3540 ns, 0.0004 | 7593 ns, 0.0004 | 6510 ns, 0.0007 | 12913 ns, 0.0006 | 339 ns, 0.0004 | 14605 ns, 0.0004 |
| q5 8k→16k | 4851 ns, 0.0005 | 9653 ns, 0.0005 | 9724 ns, 0.0010 | 20717 ns, 0.0010 | 448 ns, 0.0005 | 20347 ns, 0.0005 |
| q8 8k→16k | 11274 ns, 0.0011 | 22625 ns, 0.0011 | 22632 ns, 0.0023 | 45218 ns, 0.0023 | 1096 ns, 0.0013 | 46931 ns, 0.0011 |
| q10 8k→16k | 17818 ns, 0.0018 | 35828 ns, 0.0018 | 37393 ns, 0.0037 | 71488 ns, 0.0036 | 1564 ns, 0.0018 | 74635 ns, 0.0018 |
| q3 16k→8k | 2724 ns, 0.0003 | 5464 ns, 0.0003 | 5541 ns, 0.0006 | 11084 ns, 0.0006 | 146 ns, 0.0003 | 5724 ns, 0.0003 |
| q5 16k→8k | 5088 ns, 0.0005 | 10055 ns, 0.0005 | 10403 ns, 0.0010 | 21555 ns, 0.0011 | 263 ns, 0.0006 | 10522 ns, 0.0005 |
| q8 16k→8k | 9202 ns, 0.0009 | 18725 ns, 0.0009 | 18462 ns, 0.0018 | 37629 ns, 0.0019 | 477 ns, 0.0011 | 19082 ns, 0.0009 |
| q10 16k→8k | 14031 ns, 0.0014 | 28473 ns, 0.0014 | 28629 ns, 0.0029 | 58239 ns, 0.0029 | 687 ns, 0.0016 | 29195 ns, 0.0014 |
| q3 16k→48k | 9872 ns, 0.0010 | 18719 ns, 0.0009 | 18886 ns, 0.0019 | 37134 ns, 0.0019 | 434 ns, 0.0010 | 19649 ns, 0.0009 |
| q5 16k→48k | 14127 ns, 0.0014 | 28426 ns, 0.0014 | 28233 ns, 0.0028 | 56504 ns, 0.0028 | 643 ns, 0.0015 | 29229 ns, 0.0014 |
| q8 16k→48k | 33657 ns, 0.0034 | 66510 ns, 0.0033 | 66804 ns, 0.0067 | 133863 ns, 0.0067 | 1469 ns, 0.0034 | 68621 ns, 0.0033 |
| q10 16k→48k | 49712 ns, 0.0050 | 100439 ns, 0.0050 | 101315 ns, 0.0101 | 208358 ns, 0.0104 | 2213 ns, 0.0051 | 104783 ns, 0.0051 |
| q3 48k→16k | 9519 ns, 0.0010 | 18856 ns, 0.0009 | 18748 ns, 0.0019 | 37932 ns, 0.0019 | 164 ns, 0.0011 | 6460 ns, 0.0009 |
| q5 48k→16k | 14402 ns, 0.0014 | 28944 ns, 0.0014 | 29665 ns, 0.0030 | 58940 ns, 0.0029 | 251 ns, 0.0017 | 9935 ns, 0.0014 |
| q8 48k→16k | 27404 ns, 0.0027 | 54019 ns, 0.0027 | 52740 ns, 0.0053 | 107599 ns, 0.0054 | 440 ns, 0.0030 | 18322 ns, 0.0027 |
| q10 48k→16k | 40916 ns, 0.0041 | 81785 ns, 0.0041 | 81970 ns, 0.0082 | 164264 ns, 0.0082 | 650 ns, 0.0045 | 28414 ns, 0.0041 |
| q3 44.1k→48k | 63258 ns, 0.0063 | 125071 ns, 0.0063 | 128891 ns, 0.0129 | 251062 ns, 0.0126 | 1016 ns, 0.0064 | 47141 ns, 0.0063 |
| q5 44.1k→48k | 104261 ns, 0.0104 | 209312 ns, 0.0105 | 211257 ns, 0.0211 | 431006 ns, 0.0215 | 1668 ns, 0.0105 | 78205 ns, 0.0104 |
| q8 44.1k→48k | 208041 ns, 0.0208 | 415480 ns, 0.0208 | 421731 ns, 0.0422 | 827483 ns, 0.0414 | 3333 ns, 0.0210 | 155976 ns, 0.0208 |
| q10 44.1k→48k | 331845 ns, 0.0332 | 662217 ns, 0.0331 | 667994 ns, 0.0668 | 1338719 ns, 0.0669 | 5302 ns, 0.0334 | 247805 ns, 0.0330 |
| q3 48k→8k | 8727 ns, 0.0009 | 17320 ns, 0.0009 | 17265 ns, 0.0017 | 34483 ns, 0.0017 | 154 ns, 0.0011 | 5976 ns, 0.0009 |
| q5 48k→8k | 13451 ns, 0.0013 | 27070 ns, 0.0014 | 26903 ns, 0.0027 | 53572 ns, 0.0027 | 226 ns, 0.0016 | 9363 ns, 0.0014 |
| q8 48k→8k | 25982 ns, 0.0026 | 50975 ns, 0.0025 | 51368 ns, 0.0051 | 102960 ns, 0.0051 | 422 ns, 0.0029 | 17823 ns, 0.0026 |
| q10 48k→8k | 40514 ns, 0.0041 | 80732 ns, 0.0040 | 80201 ns, 0.0080 | 159810 ns, 0.0080 | 640 ns, 0.0044 | 28098 ns, 0.0041 |

Target (docs/acceptance/resample.md): `rt-fraction` at most 0.1 for every
10 ms mono configuration. Met: the highest measured is 0.0332, for
quality 10, 44.1k→48k. The interpolated kernel (44.1k→48k, 147:160) does
four multiply-accumulates per tap, the direct kernel one.

Construction (`BenchmarkResamplerNew`), which builds the sinc table with
`math.Sin`, allocates 6 times:

| Config | ns/op | B/op |
|---|---|---|
| q3 48k→16k mono | 3890 | 19372 |
| q10 44.1k→48k mono | 129632 | 37772 |
| q10 48k→8k stereo | 23792 | 28312 |

`go build -gcflags=-m ./resample` shows `fixed.Mult16x16`,
`fixed.Mult16x32Q15`, `fixed.Saturate32PShr` and the other macro ports
inlined in the kernels; `cubicCoef`, called once per output sample, is
not inlined.

## Preprocessor (`preprocess`, WP5)

Mac (M1 Pro, macOS 27.0.1, Go 1.26.5), 2026-10-26:

```sh
go test ./preprocess -run '^$' -bench . -benchtime 1s -count 5
```

Medians of five runs, after 200 adapting frames of speech in noise, VAD on:

| Configuration | `Run` µs/frame | `RunEcho` µs/frame | `Run` rt-fraction | allocs/op |
|---|---|---|---|---|
| 8 kHz, 80 | 22.6 | 20.5 | 0.0023 | 0 |
| 8 kHz, 160 | 44.3 | 40.7 | 0.0022 | 0 |
| 16 kHz, 160 | 43.0 | 39.7 | 0.0043 | 0 |
| 16 kHz, 320 | 82.3 | 86.9 | 0.0041 | 0 |
| 32 kHz, 320 | 71.5 | 79.0 | 0.0072 | 0 |
| 32 kHz, 640 | 158.6 | 174.1 | 0.0079 | 0 |
| 48 kHz, 480 | 120.9 | 114.0 | 0.0121 | 0 |
| 48 kHz, 960 | 261.8 | 230.1 | 0.0131 | 0 |

`New` at 48 kHz with `N` = 960: 178 µs, 35 allocations, 115 KiB.

The machine was shared with other jobs during the run (load average 108 on
10 cores), and single runs varied by up to 30%, so these are upper bounds.
Cost follows the frame size, not the rate. No Linux VM numbers yet.

## Automatic gain control (`agc`, WP4)

Mac, 2026-10-26, at the commit "agc: check the ceiling and exact gain
limits in more acceptance tests":

```sh
CGO_ENABLED=0 go test -run '^$' -bench . -benchmem -count 1 ./agc
```

`DefaultConfig` at each rate, on full-scale `SpeechLike` input, so the
limiter and the level estimate both run. Other agents were running at the
same time (load average 86 on 10 cores), so these are upper bounds; an
independent run under load measured 3.7 µs for `48k_480`.

| Benchmark | ns/op | rt-fraction | samples/s | allocs/op |
|---|---|---|---|---|
| `AGCProcess/8k_80` | 254.8 | 0.0000255 | 314 M | 0 |
| `AGCProcess/16k_160` | 439.2 | 0.0000439 | 364 M | 0 |
| `AGCProcess/32k_320` | 857.3 | 0.0000857 | 373 M | 0 |
| `AGCProcess/48k_480` | 1257 | 0.0001257 | 382 M | 0 |
| `AGCProcess/16k_320` (20 ms) | 856.6 | 0.0000428 | 374 M | 0 |
| `AGCProcess/16k_37` | 108.8 | 0.0000471 | 340 M | 0 |
| `AGCNew` (48 kHz) | 266.7 | | | 2 (400 B) |

Target (`docs/acceptance/agc.md`): at most 10 µs per 10 ms frame at 48 kHz,
0 allocs/op. Measured 1.26 µs. Not measured on the Linux VM.
