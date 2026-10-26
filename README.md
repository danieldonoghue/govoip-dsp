# govoip-dsp

Pure-Go real-time audio DSP for VoIP, WebRTC-style communication and
streaming media: resampling, echo cancellation, noise suppression with voice
activity detection, and automatic gain control.

**Status: under construction.** The resampler (`resample`), the ring
buffers (`buffer`) and noise suppression with VAD (`preprocess`) are usable;
the echo canceller and AGC are still in progress. See the table below and
[docs/status.md](docs/status.md).

## Design

- **No cgo.** The library builds and runs with `CGO_ENABLED=0`; no C
  compiler, native library or external program is needed to build, test or
  use it.
- **SpeexDSP compatibility (goal).** The resampler, echo canceller and
  preprocessor are to be bit-exact ports of SpeexDSP 1.2.1, fixed-point build
  (commit `1b28a0f6`), checked against fixtures from a C reference harness.
  Today the resampler, the preprocessor, the shared fixed-point arithmetic
  (`internal/fixed`) and the FFT (`internal/fft`) exist, and all four are
  bit-exact. The ring
  buffers are an own design, and so will be the AGC.
- **Real-time safety (requirement).** No allocation, locking or blocking on
  the per-frame path, each to be verified by tests
  ([requirements §16](docs/requirements.md#16-real-time-and-memory-safety-rules)).
- **Samples** are `int16` on the 16-bit scale; `float32` entry points use the
  same scale.

## Component status

| Component | Package | Status |
|---|---|---|
| Fixed-point arithmetic | `internal/fixed` | Validated (bit-exact with the harness) |
| Test support | `internal/testutil` | Implemented |
| Resampler | `resample` | Validated (bit-exact with SpeexDSP 1.2.1) |
| Ring buffer | `buffer` | Validated (own design; lock-free SPSC variant) |
| FFT | `internal/fft` | Validated (bit-exact with the harness) |
| AGC | `agc` | Planned |
| Noise suppression and VAD | `preprocess` | Validated (bit-exact with SpeexDSP 1.2.1) |
| Echo canceller | `echo` | Planned |

Status levels are defined in
[requirements §14](docs/requirements.md#14-status-levels). The live board is
[docs/status.md](docs/status.md).

## Platforms

| Platform | Status |
|---|---|
| macOS arm64 | Tested locally: all tests |
| Linux amd64, Linux arm64, macOS arm64, Windows amd64 | CI runs all tests on every push that changes code, with Go 1.26 and 1.27, plus race tests on Linux amd64 and macOS arm64 ([results](https://github.com/danieldonoghue/govoip-dsp/actions/workflows/ci.yml)) |
| linux/arm, linux/riscv64, linux/ppc64le, linux/s390x, linux/mips64le, freebsd/amd64, darwin/amd64, windows/arm64, wasip1/wasm | Build-only: cross-compiled with `make cross`, never run |

## Install

```sh
go get github.com/danieldonoghue/govoip-dsp
```

Requires Go 1.26 or later. Converting one 20 ms frame of 8 kHz telephony
audio to 16 kHz:

```go
const inRate, outRate, frameMs = 8000, 16000, 20

r, err := resample.New(resample.Config{
	Channels: 1, InRate: inRate, OutRate: outRate, Quality: 5,
})
if err != nil {
	log.Fatal(err)
}
in := make([]int16, inRate*frameMs/1000) // one 20 ms frame
out := make([]int16, r.OutLen(len(in)))  // always large enough for one call
read, written, err := r.Process(in, out)
```

`Process` keeps its filter state between calls, accepts chunks of any size
and does not allocate.

## Tests and benchmarks

```sh
make help    # list every target
make check   # gofmt, vet, tests (also with -tags purego), dependency guard, staticcheck
make race    # race detector
make bench   # short benchmarks
```

Reference fixtures are committed, so tests need neither a C compiler nor
the network. Regenerating them (`make fixtures`) needs Docker or a host
clang; see [docs/testing.md](docs/testing.md).

## Limitations

- Mono echo cancellation and preprocessing only in the first release.
- Go is not a hard real-time platform; tail latencies will be measured and
  reported, not guaranteed.

## Licence

The project is licensed under the BSD 3-Clause licence ([LICENSE](LICENSE)).
Code derived from SpeexDSP and KISS FFT also keeps the upstream copyright
notices and BSD 3-Clause terms; see
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) and [LICENSES/](LICENSES/).
