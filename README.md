# govoip-dsp

Pure-Go real-time audio DSP for VoIP, WebRTC-style communication and
streaming media: resampling, echo cancellation, noise suppression with voice
activity detection, and automatic gain control.

**Status: pre-release.** All five public packages are validated against their
acceptance criteria ([work packages](docs/architecture.md#work-packages)).
Releases are tagged as v0.x; the API may change until v1.0.

## Features

| Package | What it does | Typical use | SpeexDSP status ([bit-exact](docs/glossary.md#bit-exact) means the same output samples as the C library) | Docs |
|---|---|---|---|---|
| `echo` | Acoustic echo cancellation: removes the echo of the audio a call sends (the [far-end reference](docs/glossary.md#far-end)) from the audio it receives (the [near-end signal](docs/glossary.md#near-end)) | Send path of a call where what is sent can come back: a device whose microphone hears its loudspeaker, or a server whose callers may be on a speakerphone; 8 or 16 kHz, 10 or 20 ms frames | Bit-exact port of `mdf.c` | [pkg.go.dev](https://pkg.go.dev/github.com/danieldonoghue/govoip-dsp/echo), [guide](docs/guide.md#the-send-path-frame-by-frame) |
| `preprocess` | Noise suppression, voice activity detection ([VAD](docs/glossary.md#vad)) and suppression of the [residual echo](docs/glossary.md#residual-echo) | Send path, after `echo` where there is one, on the same frames; its VAD decision feeds `agc` | Bit-exact port of `preprocess.c` and `filterbank.c` | [pkg.go.dev](https://pkg.go.dev/github.com/danieldonoghue/govoip-dsp/preprocess), [guide](docs/guide.md#the-send-path-frame-by-frame) |
| `agc` | Automatic gain control ([AGC](docs/glossary.md#agc)) with a lookahead [limiter](docs/glossary.md#limiter) | Last stage of the send path, before the encoder | Own design; SpeexDSP has no fixed-point AGC | [pkg.go.dev](https://pkg.go.dev/github.com/danieldonoghue/govoip-dsp/agc), [guide](docs/guide.md#the-send-path-frame-by-frame) |
| `resample` | Sample-rate conversion, any ratio within its size limits, 1 to 256 channels, any chunk size | Decoder rate to device rate; 48 kHz down to 16 kHz for `echo` | Bit-exact port of `resample.c` | [pkg.go.dev](https://pkg.go.dev/github.com/danieldonoghue/govoip-dsp/resample), [guide](docs/guide.md#the-receive-path) |
| `buffer` | `Ring`, a sample FIFO for one goroutine, and `SPSC`, a [wait-free](docs/glossary.md#wait-free) [FIFO](docs/glossary.md#fifo) between two goroutines | Handing frames from a network or device goroutine to the processing goroutine | Own design; `buffer.c` was design input only | [pkg.go.dev](https://pkg.go.dev/github.com/danieldonoghue/govoip-dsp/buffer), [guide](docs/guide.md#handing-audio-between-goroutines) |

## How they fit together

An application uses only the packages its use case needs; the developer
guide lists the [packages for each use case](docs/guide.md#which-packages-you-need).
The near-end signal is the audio that arrives at your code from the
caller's side: a device's microphone signal, the audio Asterisk sends
over AudioSocket, or the RTP stream a media server receives. The
far-end reference is the audio your code sends toward that side. For a
call, whether it needs `echo` depends on whether what it sends can come
back in what it receives:

- **Speakerphone, hands-free, conference room.** The microphone picks
  up what the loudspeaker plays, so the far end would hear itself. The
  send path cleans the microphone signal one frame at a time: `echo`
  removes the echo, `preprocess` suppresses noise and the echo that is
  left and decides whether the frame is speech (voice activity
  detection, VAD), and `agc` sets the level, raising it only on frames
  that the VAD calls speech and that are above its gate level.
- **A server that sends audio to the caller**, such as a service on
  Asterisk AudioSocket that plays prompts. A speakerphone caller, or an
  acoustic or line echo on the caller's side, returns what the server
  sent, so the send path is the same, run on the received audio. There
  the [echo delay](docs/glossary.md#echo-delay) includes the network
  round trip to the caller's device, so the 200 ms start value of the
  filter length may be too short, and echo later than the filter length
  is not cancelled. Echo through a server and line echo have not been
  measured.
- **Headset, or a handset at the ear; a server that sends nothing
  back**, such as speech recognition. There is no echo, so the send path
  can be `preprocess` then `agc`, with `SetVAD(true)` on the
  preprocessor so that `agc` receives the speech decision.

`preprocess` and `agc` are optional. Leave out `agc` when the level is
already right, for example a source of known, fixed gain; `agc` still
works without `preprocess`, with its `speech` argument true, but then it
raises noise above its gate level like speech. Leave out `preprocess`
when the audio is already clean and you don't need a VAD; after `echo`,
that also drops the suppression of the echo that is left.

On a noisy line, set the AGC's gate level before you rely on `agc`: with
the default gate, `agc` can make steady noise louder. The guide's
[send path](docs/guide.md#the-send-path-frame-by-frame), "Gate above the
noise", gives the steps.

With 20 ms frames the send path delays the audio by about 22 ms: `echo`
adds no delay, `preprocess` one frame and `agc` 2 ms
([latency](docs/guide.md#the-send-path-frame-by-frame)); `resample`, where
used, adds its filter delay.

The receive path converts decoded audio to the device rate with
`resample`. `buffer.SPSC` carries audio between goroutines without
blocking either side. The developer guide draws the
[pipeline](docs/guide.md#the-pipeline).

## Getting started

For a Go service on Asterisk AudioSocket, start with the
[AudioSocket quick start](docs/audiosocket.md): the protocol, the
processors for each connection and the calls for each frame, in tested
code. Otherwise, start with the
[developer guide](docs/guide.md#start-here), which links
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
- **SpeexDSP compatibility.** SpeexDSP is a C library for the same jobs.
  The resampler, the echo canceller, the preprocessor, the shared
  [fixed-point](docs/glossary.md#fixed-point) arithmetic
  (`internal/fixed`) and the [FFT](docs/glossary.md#fft) (`internal/fft`)
  are [bit-exact](docs/glossary.md#bit-exact) ports of SpeexDSP 1.2.1,
  fixed-point build (commit `1b28a0f6`). They are checked against output
  files (fixtures) from a C reference harness. The ring buffers and the
  AGC are own designs.
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
out := make([]int16, r.OutLen(len(decoded))) // 961: room for one Process's output
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

- Echo cancellation and preprocessing are mono (one channel) in the first
  release.
- The echo canceller is supported at 8 and 16 kHz only. Resample higher
  rates down first.
- **The AGC can make noise louder.** The VAD reports speech on
  [stationary noise](docs/glossary.md#stationary-noise) and on residual
  echo, so the AGC's level gate is its only protection. Set `GateLevel`
  above the noise left after noise suppression
  ([how to choose it](docs/guide.md#the-send-path-frame-by-frame), under
  "Gate above the noise"). The default gate is -50 dBFS. With it, the
  AGC raised what noise suppression left of white noise (from about
  -40 dBFS at the input) and of pink noise (from about -52 dBFS) by 16 to
  23 dB. That noise left the chain 3.5 to 18.6 dB louder than it entered
  ([measurements](docs/testing.md#whole-chain-measurements-integration-wp7)).
- **Two criteria fail on a chain resampled from 48 kHz.** The whole
  chain, tested in `integration`, passes every pipeline criterion at 8
  and 16 kHz. On a chain resampled from 48 kHz, two behavioural criteria
  fail: the echo stage cancels less and the AGC holds the near end lower
  ([measurements](docs/testing.md#whole-chain-measurements-integration-wp7)).
- **Short echo delays are not cancelled well with jitter.** The
  recipes of [docs/usage.md](docs/usage.md) were tested in simulated calls
  with up to 30 ms of jitter. Recipe A (paced sending, `Cancel`) cancelled
  echo delays of 40 to 200 ms. Recipe B (separate
  callbacks, `Playback` and `Capture`, one frame late) cancelled 60 to
  200 ms, and cancelled almost nothing at 50 ms and less (0.3 to
  1.2 dB).
- **A 32 ms echo with that jitter is not cancelled by either recipe.**
  This is the "uneven timing" criterion, U6 in the
  [acceptance criteria](docs/acceptance/integration.md). It fails, and the
  test pins it as a reported finding. For recipe A, the
  [caveats](docs/usage.md#caveats) under "Echo delay" explain why. For
  recipe B, its caveats explain it by the far end's lag, one frame plus
  the shift that jitter adds: the canceller models echo delays only from
  that lag on. Without jitter, recipe B cancels a 32 ms echo by only 7.6
  and 11.9 dB, and why has not been traced.
- **Go is not a hard real-time platform.** A frame can occasionally take
  much longer than usual. The slowest frames are measured and reported
  ([docs/performance.md](docs/performance.md)), not guaranteed.
- The Linux amd64 reference environment has not been measured yet.

## Contributing

This project does not accept contributions yet.

## Licence

BSD 3-Clause ([LICENSE](LICENSE)). Parts are derived from SpeexDSP and
KISS FFT, also BSD 3-Clause; their notices are in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
