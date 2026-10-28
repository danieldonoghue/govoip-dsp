# govoip-dsp

Pure-Go real-time audio DSP for VoIP, WebRTC-style communication and
streaming media: resampling, echo cancellation, noise suppression with voice
activity detection, and automatic gain control.

**Status: pre-release.** All five public packages are validated against their
acceptance criteria ([work packages](docs/architecture.md#work-packages)).
There is no tagged release yet, and the API may change until v1.0.

## Features

| Package | What it does | Typical use | SpeexDSP status | Docs |
|---|---|---|---|---|
| `echo` | Acoustic echo cancellation: removes the far end's voice, played by the loudspeaker, from the microphone signal | Send path of a call where the microphone hears the loudspeaker, at 8 or 16 kHz, 10 or 20 ms frames | Bit-exact port of `mdf.c` | [pkg.go.dev](https://pkg.go.dev/github.com/danieldonoghue/govoip-dsp/echo), [guide](docs/guide.md#the-send-path-frame-by-frame) |
| `preprocess` | Noise suppression, voice activity detection (VAD) and residual-echo suppression | Send path, after `echo` where there is one, on the same frames; its VAD decision feeds `agc` | Bit-exact port of `preprocess.c` and `filterbank.c` | [pkg.go.dev](https://pkg.go.dev/github.com/danieldonoghue/govoip-dsp/preprocess), [guide](docs/guide.md#the-send-path-frame-by-frame) |
| `agc` | Automatic gain control with a lookahead limiter | Last stage of the send path, before the encoder | Own design; SpeexDSP has no fixed-point AGC | [pkg.go.dev](https://pkg.go.dev/github.com/danieldonoghue/govoip-dsp/agc), [guide](docs/guide.md#the-send-path-frame-by-frame) |
| `resample` | Sample-rate conversion, any ratio within its size limits, 1 to 256 channels, any chunk size | Decoder rate to device rate; 48 kHz down to 16 kHz for `echo` | Bit-exact port of `resample.c` | [pkg.go.dev](https://pkg.go.dev/github.com/danieldonoghue/govoip-dsp/resample), [guide](docs/guide.md#the-receive-path) |
| `buffer` | `Ring`, a sample FIFO for one goroutine, and `SPSC`, a wait-free FIFO between two | Handing frames from a network or device goroutine to the processing goroutine | Own design; `buffer.c` was design input only | [pkg.go.dev](https://pkg.go.dev/github.com/danieldonoghue/govoip-dsp/buffer), [guide](docs/guide.md#handing-audio-between-goroutines) |

## How they fit together

A call needs only some of the packages, and which ones depends on
whether its microphone can hear its loudspeaker.

- **Speakerphone, hands-free, conference room.** The microphone picks
  up what the loudspeaker plays, so the far end would hear itself. The
  send path cleans the microphone signal one frame at a time: `echo`
  removes the echo, `preprocess` suppresses noise and the echo that is
  left and decides whether the frame is speech, and `agc` sets the
  level, raising it only on speech.
- **Headset, or a handset at the ear.** There is no echo, so the send
  path is `preprocess` then `agc`.

The receive path converts decoded audio to the device rate with
`resample`. `buffer.SPSC` carries audio between goroutines without
blocking either side. The developer guide lists the
[packages for each use case](docs/guide.md#which-packages-you-need) and
draws the [pipeline](docs/guide.md#the-pipeline).

## Getting started

Start with the [developer guide](docs/guide.md#start-here), which links
the [glossary](docs/glossary.md) and the tested wiring recipes of
[docs/usage.md](docs/usage.md). The root package's
[examples](example_test.go) run the send chain, the receive chain and a
goroutine hand-off end to end:

```sh
go test github.com/danieldonoghue/govoip-dsp -run Example -v
```

## Design

- **No cgo.** The library builds and runs with `CGO_ENABLED=0`; no C
  compiler, native library or external program is needed to build, test or
  use it.
- **SpeexDSP compatibility.** The resampler, the echo canceller, the
  preprocessor, the shared fixed-point arithmetic (`internal/fixed`) and the
  FFT (`internal/fft`) are bit-exact ports of SpeexDSP 1.2.1, fixed-point
  build (commit `1b28a0f6`), checked against fixtures from a C reference
  harness. The ring buffers and the AGC are own designs.
- **Real-time safety.** No allocation, locking or blocking on the
  per-frame path
  ([requirements §16](docs/requirements.md#16-real-time-and-memory-safety-rules)).
  Every streaming type has a zero-allocation test.
- **Samples** are `int16` on the 16-bit scale; `float32` entry points use the
  same scale.

## Component status

| Component | Package | Status |
|---|---|---|
| Resampler | `resample` | Validated (bit-exact with SpeexDSP 1.2.1) |
| Ring buffers | `buffer` | Validated (own design; lock-free SPSC variant) |
| Echo canceller | `echo` | Validated (bit-exact with SpeexDSP 1.2.1; supported at 8 and 16 kHz) |
| Noise suppression and VAD | `preprocess` | Validated (bit-exact with SpeexDSP 1.2.1) |
| AGC | `agc` | Validated (own design, behavioural criteria) |

The internal packages are listed in
[docs/architecture.md](docs/architecture.md#packages), and the work
packages that built them under
[Work packages](docs/architecture.md#work-packages). The chain as a
whole is tested in `integration`; its results are under
[Limitations](#limitations).

Status levels are defined in
[requirements §14](docs/requirements.md#14-status-levels). Decisions are
recorded as ADRs in [docs/decisions/](docs/decisions/README.md).

## Platforms

| Platform | Status |
|---|---|
| macOS arm64 | Tested locally: all tests |
| Linux amd64, Linux arm64, macOS arm64, Windows amd64 | CI runs all tests on every push that changes code, with Go 1.26 and 1.27 (LibriSpeech tests on Linux amd64 with Go 1.27 only), plus race tests on Linux amd64 and macOS arm64 ([results](https://github.com/danieldonoghue/govoip-dsp/actions/workflows/ci.yml)) |
| linux/arm, linux/riscv64, linux/ppc64le, linux/s390x, linux/mips64le, freebsd/amd64, darwin/amd64, windows/arm64, wasip1/wasm | Build-only: cross-compiled with `make cross`, never run |

## Install

```sh
go get github.com/danieldonoghue/govoip-dsp
```

Requires Go 1.26 or later. Converting one 20 ms frame of 8 kHz
telephony audio to a 48 kHz device:

```go
r, err := resample.New(resample.Config{
	Channels: 1, InRate: 8000, OutRate: 48000, Quality: resample.QualityVoIP,
})
if err != nil {
	log.Fatal(err)
}
decoded := make([]int16, 160)                // one 20 ms frame at 8 kHz
out := make([]int16, r.OutLen(len(decoded))) // 961: room for one call's output
read, written, err := r.Process(decoded, out)
if err != nil {
	log.Fatal(err)
}
fmt.Println(read, written) // 160 960: hand out[:written] to the device
```

`Process` keeps its filter state between calls, accepts chunks of any
size and does not allocate. See [Getting started](#getting-started) for
the send chain.

## Tests and benchmarks

```sh
make help    # list every target
make hooks   # once per clone: run make check-short before every git push
make check   # gofmt, vet, tests (also with -tags purego), dependency guard, staticcheck
make race    # race detector
make bench   # short benchmarks
```

Reference fixtures are committed, so tests need neither a C compiler nor the
network, except `make race` on Linux, which needs cgo. Regenerating them
(`make fixtures`) needs Docker or a host clang; see
[docs/testing.md](docs/testing.md).

## Limitations

- Mono echo cancellation and preprocessing only in the first release.
- The echo canceller is supported at 8 and 16 kHz only; resample higher
  rates down first.
- The VAD reports speech on stationary noise and on residual echo, so the
  AGC's level gate is its only noise protection. With the default gate,
  the AGC raises what noise suppression leaves of white noise from about
  -40 dBFS and pink noise from about -52 dBFS at the input by 16 to
  23 dB, so that noise leaves the chain 3.5 to 18.6 dB louder than it
  entered; set `GateLevel` above the residual noise
  ([measurements](docs/testing.md#whole-chain-measurements-integration-wp7)).
- The whole chain, tested in `integration`, passes every pipeline
  criterion at 8 and 16 kHz. On the chain resampled from 48 kHz two
  behavioural criteria fail: the echo stage cancels less and the AGC
  holds the near end lower
  ([measurements](docs/testing.md#whole-chain-measurements-integration-wp7)).
- The recipes of [docs/usage.md](docs/usage.md) cancel echo within the
  delays measured in simulated calls with up to 30 ms of jitter: 40 to
  200 ms for recipe A (paced sending, `Cancel`), 60 to 200 ms for recipe
  B (separate callbacks, `Playback` and `Capture`, one frame late). With
  a 32 ms echo and that jitter neither does (U6 fails).
- Go is not a hard real-time platform; tail latencies are measured and
  reported ([docs/performance.md](docs/performance.md)), not guaranteed.
  The Linux amd64 reference environment has not been measured yet.

## Contributing

This project does not accept contributions yet.

## Licence

BSD 3-Clause ([LICENSE](LICENSE)). Parts are derived from SpeexDSP and
KISS FFT, also BSD 3-Clause; their notices are in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
