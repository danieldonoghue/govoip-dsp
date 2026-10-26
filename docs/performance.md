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
