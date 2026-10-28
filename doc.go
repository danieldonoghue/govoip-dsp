// Package govoipdsp is the overview of govoip-dsp, a pure-Go real-time
// audio DSP library for VoIP: resampling, echo cancellation, noise
// suppression with voice activity detection (VAD), automatic gain control
// (AGC) and ring buffers for handing audio between goroutines. This
// package holds no code, only this overview and the end-to-end examples;
// import the packages below.
//
// The library builds with CGO_ENABLED=0 and needs no C compiler or native
// library. Its resampler, echo canceller and preprocessor are bit-exact
// ports of SpeexDSP 1.2.1, fixed-point build: for the same input and a
// tested configuration they give the same output samples as the C
// library, except at the deviations each package doc lists. The AGC and
// the buffers are own designs.
//
// The [developer guide] explains the pieces with diagrams, the [glossary]
// defines every term these docs use, and the [usage guide] gives tested recipes
// for wiring a call. Here "frame" means a processing frame, except for
// resample, where it means a resampler frame (see the glossary). go doc does
// not print example code: read the examples named below in example_test.go at
// the module root, or run them with go test -run Example -v.
//
// # Packages
//
//   - resample: sample-rate conversion, for example from a 48 kHz device
//     to 16 kHz for the echo canceller, or from a decoder's 8 kHz to the
//     device. Any rates within its size limits, 1 to 256 channels, any
//     chunk size.
//   - echo: acoustic echo cancellation. It removes the far end's voice,
//     played by the loudspeaker and picked up again by the microphone,
//     from the microphone signal. Mono; it works well at 8 and 16 kHz
//     only, although New accepts 8000 to 48000 Hz.
//   - preprocess: noise suppression, VAD and suppression of the echo that
//     echo left. Fixed frame sizes, mono.
//   - agc: automatic gain control. It brings speech to a steady level and
//     keeps peaks under a ceiling. Any chunk size.
//   - buffer: Ring, a FIFO of samples for one goroutine, and SPSC, a
//     wait-free FIFO between one producer and one consumer goroutine.
//
// The packages under internal/ (fixed-point arithmetic, the FFT, test
// support) are used by these packages and are not for callers.
//
// # How they fit together
//
// An application uses only the packages its use case needs; the
// [developer guide] has a table of use cases and the packages each needs. For a
// call, whether it needs package echo depends on whether its microphone can
// hear its loudspeaker. On a speakerphone or in a conference room it can: the
// microphone picks up what the loudspeaker plays, so the far end would hear
// itself, and package echo removes it. With a headset, or a handset at the ear,
// there is no echo, and the send path can be preprocess.Preprocessor.Run
// followed by agc.AGC.Process, with preprocess.Preprocessor.SetVAD(true) so
// that agc receives the speech decision.
//
// preprocess and agc are optional. Leave out agc when the level is already
// right, for example a source of known, fixed gain. agc still works without
// preprocess: pass speech true, and noise above GateLevel is then raised like
// speech. Leave out preprocess when the audio is already clean and you don't
// need a VAD; after echo, that also drops the suppression of the echo that is
// left.
//
// With echo cancellation, the send path cleans the microphone signal of one
// call before it is encoded and sent, one frame at a time:
//
//	microphone -> echo.Canceller.Cancel -> preprocess.Preprocessor.RunEcho
//	           -> agc.AGC.Process -> encoder
//
// For each microphone frame, echo needs the loudspeaker frame (the far end)
// played while that microphone frame was captured; the filter covers echo
// delays of 0 to about its FilterLength. After Cancel, echo.Canceller.Residual
// gives the residual-echo spectrum that RunEcho takes for the same frame;
// RunEcho returns the VAD decision that agc.AGC.Process takes. The packages do
// not import each other: the caller passes these values on. Example_sendChain
// shows the whole path.
//
// The receive path brings decoded audio to the device rate with
// resample.Resampler.Process, its output sized with OutLen
// (Example_receiveChain). With echo cancellation, where the microphone and
// loudspeaker streams run at a rate other than 8 or 16 kHz, usually the
// device rate, resample both to 16 kHz before the send path, each with its
// own Resampler. Where the encoder runs at another rate than the send
// path, resample the send path's output to the encoder rate.
//
// Between goroutines, for example a network reader and the processing
// goroutine, use buffer.SPSC; it never blocks either side, and after a
// stall the consumer can drop the backlog (Example_handoff). Where each
// received frame pairs with a far-end frame, drop the same number of
// far-end frames too, so the pairs stay aligned, as drain does in
// docs/usage.md.
//
// # Rules shared by every package
//
//   - Make each processor and buffer once, with New and a Config (NewSPSC and
//     an SPSCConfig for buffer.SPSC), before the call starts. New allocates
//     and returns an error wrapping the package's ErrConfig for an invalid
//     Config (resample also ErrAllocFailed, for a filter beyond its size
//     limits, which its package doc lists); test it with errors.Is.
//   - Per-frame and per-chunk methods (the audio path) do not allocate,
//     block or panic. They return only unwrapped sentinel errors, such as
//     ErrBufferSize for a slice of the wrong length; with buffers sized
//     once from the processor (FrameSize, ResidualLen) these do not occur,
//     so treat them as programming errors (Example_errors). The exception
//     is resample's ErrAllocFailed while a rejected rate or quality change
//     is in force (see the resample package doc). A resample output too
//     short gives no error but can leave input unread. A size from OutLen(n)
//     on a new Resampler covers every call of up to n frames while
//     SkipZeros and the setters are not used; after SkipZeros or any
//     setter, call OutLen before each call (it does not allocate).
//   - Every processor, and Ring, serves one stream of one VoIP call (an
//     echo.Canceller: the call's two streams) and one goroutine at a time; it
//     is not safe for concurrent use. Only buffer.SPSC is shared, by exactly
//     two goroutines.
//   - Samples are int16 on the 16-bit scale, -32768 to 32767.
//   - No method retains a caller's slice. Input and output must not
//     overlap, except where a method says so (preprocess rewrites its
//     frame in place; agc may work in place).
//   - Reset starts a new stream without allocating; each package says
//     what its Reset keeps.
//   - A zero value (a processor not made by New) never panics, but is not
//     usable: its methods with an error result return ErrConfig, and the
//     others return zero values and change nothing, except that the
//     preprocess setters store their setting and the getters of those
//     settings return what was stored.
//
// # Limitations
//
// Echo cancellation and preprocessing are mono. The echo canceller works
// well at 8 and 16 kHz only; it does not estimate the echo delay or correct
// clock drift, so the caller keeps the far end aligned with the
// microphone. The VAD reports speech on stationary noise, so the AGC's
// gate level is its only protection against raising noise. Go is not a
// hard real-time platform; tail latencies are measured, not guaranteed.
// The README lists the current status and limitations.
//
// [developer guide]: https://github.com/danieldonoghue/govoip-dsp/blob/master/docs/guide.md
// [glossary]: https://github.com/danieldonoghue/govoip-dsp/blob/master/docs/glossary.md
// [usage guide]: https://github.com/danieldonoghue/govoip-dsp/blob/master/docs/usage.md
package govoipdsp
