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
