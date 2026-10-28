# Glossary

This page defines the terms that the package docs and the pages in `docs/`
use. Each term has one meaning everywhere, except "frame", which has the
three senses given under [Frame](#frame), and "block", which has the two
senses given under [Block](#block).

The terms are grouped by topic. Terms link to the terms they use.

## Signals and samples

### Sample

One number that gives the signal level at one instant. The packages take
`int16` samples on the [16-bit scale](#16-bit-scale).

### 16-bit scale

The range of an `int16`, -32768 to 32767. Full scale is 32768. The `float32`
methods of `resample` use the same scale, not -1 to 1.

### Sample rate

The number of samples per second of one channel, in Hz. Telephony uses
8000 Hz (narrowband) or 16000 Hz (wideband); devices often use 48000 Hz.
The highest frequency a rate can carry is half the rate (the Nyquist
frequency).

### Channel

One signal of a multichannel stream, such as the left or right signal of
stereo. `echo`, `preprocess` and `agc` are mono: one channel. `resample`
takes 1 to 256 channels.

### Interleaved

A layout of a multichannel stream in one slice: the first sample of every
channel, then the second sample of every channel, and so on. Stereo is
L0 R0 L1 R1 and so on.

### Stride

The distance in a slice between two consecutive samples of one channel.
In an interleaved stereo slice the stride is 2.

### Frame

"Frame" has three senses. Each package doc says which one it uses.

1. **Processing frame.** The fixed number of samples that `echo` and
   `preprocess` process in one call, `Config.FrameSize`. The send chain
   uses the same processing frame in every stage.
2. **Resampler frame.** In `resample`, one sample of every channel: an
   interleaved stereo slice of 960 samples is 480 frames. For mono, a frame
   is one sample.
3. **AGC frame.** Inside `agc`, ten [blocks](#block), about 10 ms. The
   caller does not see it; `agc.AGC.Process` takes any length.

### Frame size

The number of samples in one processing frame, `Config.FrameSize` of
`echo` and `preprocess`. It is the [sample rate](#sample-rate) times the
[frame duration](#frame-duration): 160 samples for 20 ms at 8 kHz, 320 for
20 ms at 16 kHz.

### Frame duration

The time one processing frame covers, usually 10 or 20 ms. VoIP codecs
and [RTP](#rtp) packets commonly carry 20 ms.

### Block

"Block" has two senses, one per package:

1. **AGC block.** Inside `agc`, `SampleRate/1000` samples: 1 ms at rates
   that are multiples of 1000 Hz. The AGC's limiter and gain ramp work
   per block.
2. **MDF block.** Inside `echo`, one partition of the adaptive filter,
   one processing frame long (see [MDF](#mdf)). The caller does not see
   it.

### Chunk

The samples passed in one call to a method that takes any length
(`resample`, `buffer`, `agc`). A stream is cut into chunks; for these
packages the cut does not change the output, under conditions each
package doc states: `agc` needs the same `speech` flag for every
sample, `resample` needs all input eventually consumed, and `buffer`
needs no write to overflow.

### Chunk-partition invariance

The property that any way of cutting a stream into chunks gives the same
output. `resample`, `buffer` and `agc` have it and test it. `echo` and
`preprocess` take whole processing frames only.

### dB

Decibels: a ratio on a log scale, `10·log10` of a power ratio or
`20·log10` of an amplitude ratio. +6 dB is about twice the amplitude,
-20 dB a tenth.

### dBFS

Decibels relative to full scale on the [16-bit scale](#16-bit-scale):
`20·log10(level/32768)`. 0 dBFS is amplitude 32768; -6 dBFS is about half
of it. The `agc` levels are whole dBFS.

### RMS

Root mean square: the square root of the mean of the squared samples. It
measures the level of a signal over a span of samples. The `agc` levels
are RMS levels.

### Silence

Samples that are all zero. "Queue silence" means pass a frame of zeros.

## The call

### Send path and receive path

The two directions of a call. The send path takes the microphone signal
through `echo`, `preprocess` and `agc` to the encoder; the receive path
takes decoded audio through `resample` to the loudspeaker.

### Pipeline

The chain of stages audio passes through, in order, such as the send
path. The developer guide draws it.

### RTP

Real-time Transport Protocol (RFC 3550): the protocol that carries the
encoded audio of a call over the network, usually one packet per frame.

### Encoder and decoder

The codec stages around this library: the encoder compresses the
cleaned send-path audio into packets, the decoder turns received packets
back into samples. Codecs are not part of govoip-dsp.

### Jitter buffer

A buffer that turns packets arriving unevenly into a steady stream of
frames. Not built yet; planned for this library.

## Fixed-point arithmetic

### Fixed point

Arithmetic on integers that stand for fractions with a fixed number of
fraction bits, instead of floating point. The SpeexDSP FIXED_POINT build,
and every package here, process audio this way.

### Q-format

The notation for a [fixed-point](#fixed-point) number's fraction bits.
A Qn value `v` stands for `v/2^n`. Q15 in an `int16` covers -1 to just
under 1; Q12 in an `int32` has 12 fraction bits.

### Q15

[Q-format](#q-format) with 15 fraction bits: `v/32768`. Gains and filter
coefficients of the ports are often Q15.

### Saturation

Clamping a result to the range of its type instead of letting it
[wrap](#wrap): a sum above 32767 that is stored in an `int16` becomes
32767. The ports saturate exactly where the SpeexDSP code does, and
nowhere else except at the deviations their package docs list.

### Wrap

What an integer does on overflow when it is not saturated: it continues
from the other end of its range (32767 + 1 becomes -32768 in an
`int16`). Go integers wrap; the reference is built with `-fwrapv` so that
its C integers wrap the same way.

## Compatibility

### Reference

SpeexDSP 1.2.1, FIXED_POINT build, commit
`1b28a0f61bc31162979e1f26f3981fc3637095c8`, without SIMD and without
`RESAMPLE_FULL_SINC_TABLE`. "C" and "upstream" mean this build. The
sources are on
[gitlab.xiph.org](https://gitlab.xiph.org/xiph/speexdsp/-/tree/1b28a0f61bc31162979e1f26f3981fc3637095c8).

### Bit-exact

Producing exactly the same output values as the [reference](#reference)
for the same input and configuration, sample for sample, not just output
that sounds the same. `resample`, `echo` and `preprocess` are bit-exact;
tests compare them with fixtures that the C reference wrote
([testing.md](testing.md)).

### Reference harness and fixtures

The C program that runs the [reference](#reference) on test inputs, and
the output files it writes, which are committed in `testdata/speexdsp/`.
The bit-exact tests compare the Go output with these fixtures.

### Behavioural

A component with no reference to match. Its correctness is defined by
signal measurements with thresholds written before the implementation
(`docs/acceptance/`). `agc` is behavioural; `buffer` and `agc` are own
designs.

### Own design

Code that contains no SpeexDSP code: `buffer` and `agc`.

### Compatibility path

The behaviour of a ported package that matches the reference. A
configuration with every extension field at its zero value selects it.

### Extension

A feature with no SpeexDSP equivalent, such as `resample.Resampler.OutLen`.
An extension never changes the output of the compatibility path.

### Deviation

A documented place where a port does not do what C does, because C is
undefined there (for example a NaN input) or would stop working. Each
package doc lists its deviations.

## Echo cancellation

### Near end

The local side of a call: the person at the microphone whose voice is
sent. "Near-end speech" is that person talking.

### Far end

The remote side of a call. The far-end signal is what the local
loudspeaker plays, and so the source of the [echo](#echo). `echo` calls
it `far`.

### Echo

Far-end sound that the loudspeaker plays and the microphone picks up
again, so that the far end would hear itself. In a network call, echo can
also come from the network or the remote phone.

### Echo path

The way the far-end signal takes from `far` to the microphone samples:
the loudspeaker, the room, the microphone and any buffering on the way.
An adaptive filter models it as an [impulse response](#impulse-response).

### Acausal

An echo path whose echo would arrive in the microphone signal before the
far-end frame that causes it is passed to the canceller. No filter can
cancel it; the caller must pass the far end earlier.

### Bulk delay

The pure-delay part of the echo path: the time during which the echo
path passes nothing, before the first sound, usually the direct sound
from the loudspeaker, reaches the microphone signal. It is the
[echo delay](#echo-delay) seen as a property of the path; a bulk delay
larger than the filter length leaves nothing to cancel.

### Echo delay

The time from a far-end sample being passed to the canceller to its echo
first arriving in the microphone signal. It includes the device and
network buffering, not only the sound's travel time.

### Reverberation time

How long sound takes to die away in a room, usually measured as the time
to fall by 60 dB. The Speex manual sizes the echo filter at about a
third of it.

### Echo tail

The time over which the echo of one far-end sample keeps arriving, from
the first reflection to the last one that matters. A room's tail lasts
tens to hundreds of milliseconds.

### Filter length

The span of echo the canceller models, in samples, `echo.Config.FilterLength`,
rounded up to whole processing frames. It must cover the
[echo delay](#echo-delay) plus the [echo tail](#echo-tail). Echo outside it
is not cancelled. A longer filter costs more per frame and adapts more
slowly. The Speex manual calls it the tail length.

### Impulse response

The output of a linear system for a single unit sample at its input. The
echo canceller's filter is an estimate of the echo path's impulse
response; `echo.Canceller.ImpulseResponse` returns it.

### Adaptive filter

A filter that changes its coefficients while it runs, so that its output
follows the echo in the microphone signal. `echo` uses the MDF filter.

### MDF

Multidelay block frequency-domain adaptive filter: the echo-cancelling
algorithm of SpeexDSP `mdf.c`, which `echo` ports. It splits the filter
into blocks of one processing frame and adapts them with FFTs.

### Convergence

The adaptive filter's approach to a good model of the echo path. After
`New`, `Reset` or a change of the echo path, little echo is removed until
the filter has converged, typically seconds.

### Double talk

Both ends talking at once. The microphone then carries near-end speech
and echo together; the canceller slows its adaptation so that it does not
learn the near-end speech as echo.

### Residual echo

The echo that the canceller did not remove, still in its output.

### Residual-echo spectrum

The canceller's estimate of the [power spectrum](#power-spectrum) of the
[residual echo](#residual-echo) in the frame it last processed:
`echo.Canceller.Residual`, `FrameSize()+1` values. `preprocess` takes it
in `RunEcho` to suppress that echo further, as SpeexDSP does with
`SPEEX_PREPROCESS_SET_ECHO_STATE`.

### ERLE

Echo return loss enhancement: how much the canceller lowers the echo, in
dB, `10·log10(echo energy / residual energy)` over the same span. 0 dB
means no cancellation.

### Playback queue

The queue of far-end frames of the canceller's buffered mode
(`Playback` and `Capture`), as in `speex_echo_playback` and
`speex_echo_capture`. It holds up to three frames.

### Leak estimate

The echo canceller's running estimate, by linear regression of its
output on its echo estimate, of how much echo is left in its output. It
scales the residual-echo spectrum.

### DC notch

A filter that removes the constant (zero-frequency) part of the
microphone signal before the canceller models the echo. The rate sets its
cut-off.

## Noise suppression and voice activity

### Dereverberation

Removing the room's reverberation from the microphone signal. SpeexDSP
1.2.1 has a switch for it whose settings do not work, so `preprocess`
does not offer it.

### Noise suppression

Lowering stationary background noise, such as fan or line hiss, while
keeping speech. `preprocess` does it in the frequency domain, per
[bin](#bin) and per [filterbank](#filterbank) band; SpeexDSP and
`preprocess` call it denoise.

### Analysis window

The span of samples that `preprocess` transforms per frame: the current
frame and the one before it, `2*FrameSize` samples, weighted so that
consecutive windows add up smoothly. It is why `preprocess` delays its
output by one frame.

### Bin

One frequency of an [FFT](#fft)'s output. An FFT of `2*FrameSize`
samples gives bins spaced `SampleRate/(2*FrameSize)` Hz apart, from 0 Hz
upwards. `echo.Canceller.Residual` gives one value per bin from 0 Hz to
half the rate, `FrameSize()+1` values; `preprocess.PSD` gives the first
`FrameSize()` bins.

### Filterbank

The grouping of the FFT bins of `preprocess` into 24 bands spaced like
human hearing; `filterbank.c` in SpeexDSP. The noise estimate is updated
per bin and summed into the bands; the speech probability is computed
from the bands.

### Synthesis

The step after the per-band processing that turns the spectrum back into
samples and adds the overlapping windows together. "Analysis and
synthesis" is the whole transform in and out of `preprocess`.

### White noise and pink noise

Random noise with equal power at every frequency (white), or with power
falling by 3 dB per octave, so more of it is at low frequencies (pink).
The tests use both as stationary noise.

### Stationary noise

Noise whose level and spectrum change slowly, such as hiss or hum.
Noise suppression estimates and lowers it; it cannot tell it apart from
speech when the speech is just as steady.

### Power spectrum

The power of a frame in each frequency [bin](#bin). `preprocess.PSD`
returns the power spectrum of the last frame and `NoisePSD` the noise
estimate, both `FrameSize()` values.

### FFT

Fast Fourier transform: the step that turns a span of samples into its
frequency bins and back. `echo` and `preprocess` use a fixed-point FFT of
`2*FrameSize` samples, which limits the supported frame sizes.

### VAD

Voice activity detection: a per-frame decision whether a frame is speech.
`preprocess` returns it from `Run` and `RunEcho` when `SetVAD(true)`; the
send chain passes it to `agc` as `speech`. The SpeexDSP VAD also reports
speech on [stationary noise](#stationary-noise) and on residual echo.

### Speech probability

The score behind the VAD decision, 0 to 100 percent,
`preprocess.Preprocessor.SpeechProb`. It is a heuristic of the reference,
not a calibrated probability. A frame starts speech when the score is
above `ProbStart`, and keeps speech going while it is above
`ProbContinue`.

## Gain control

### Gain

The factor by which a stage multiplies its input, in dB: 0 dB leaves the
level unchanged, +6 dB roughly doubles it.

### Attenuation

The opposite of gain: how much a stage lowers a level, in dB.
`preprocess` settings give the most attenuation of noise and residual
echo as negative dB.

### AGC

Automatic gain control: a stage that adjusts its gain so that speech
comes out at a steady level, whatever the input level.

### Target level

The output RMS level in dBFS that the AGC drives a steady signal towards,
`agc.Config.TargetLevel`.

### Gate level

The frame RMS level in dBFS below which a frame cannot raise the AGC
gain: `agc.Config.GateLevel`. A frame below it never raises the gain or
moves the level estimate, though a gain that is falling may go on
falling. It is the AGC's only protection from raising noise in
pauses.

### Limit level

The highest output magnitude the AGC lets through, in dBFS,
`agc.Config.LimitLevel`. A [limiter](#limiter) enforces it.

### Active frame

An AGC frame whose RMS is at or above the gate level and whose samples
were all passed with `speech` true. Only active frames raise the gain
and update the level estimate.

### Limiter

The part of the AGC that lowers the gain of a block whose peak would
exceed the limit level, before that block is output. It looks ahead at
input not yet output, so it is called a lookahead limiter, and it needs
the AGC's 2 ms of [latency](#latency).

### Rise rate and fall rate

How fast the AGC gain may move, in dB per second: up (on speech only)
and down. Other AGCs call them attack and release.

### Time constant

The time a one-pole smoother takes to move about 63% of the way to a new
value. `agc.Config.LevelTime` is the time constant of the level
estimate; the limiter recovers with 20 ms.

### Power mean

The mean of values raised to a power, then the root of that power: the
order-4 power mean of `x` is the fourth root of the mean of `x^4`. Large
values weigh more than in a plain mean. The AGC's level estimate is one.

### Level estimate

The AGC's running measure of the speech level it sees, from which it
derives the gain. The `agc` package doc gives its definition.

## Resampling

### Downsampling and upsampling

Resampling to a lower rate (downsampling, for example 48000 to
16000 Hz) or to a higher one (upsampling). Downsampling needs a longer
filter, in proportion to the factor.

### Taps

The coefficients of a filter, one per input sample it weighs. A filter
of 64 taps combines 64 input samples into each output sample.

### Sinc table

The table of filter coefficients that `resample` builds at `New` and at
each rate or quality change: a windowed sinc (the ideal low-pass
filter's impulse response) sampled at the filter's phases.

### Magic samples

In `resample`, input samples left over after a rate or quality change
shortens the filter. The next process calls process them first;
`OutLen` counts them.

### Filter delay

In `resample`, the delay of its low-pass filter, `InputLatency` input
samples. The first output samples of a stream are the filter warming up;
`SkipZeros` drops them.

### Quality level

The `resample` setting from 0 to 10: a higher quality gives less noise
and distortion at more cost and latency. The tested recipes of [usage.md](usage.md) use
`QualityVoIP` (3).

### Ratio

The resampling ratio `num/den` of the input and output rates, reduced to
lowest terms: 48000 to 16000 Hz is 3/1.

## Timing

### Latency

The delay a stage adds between its input and its output. `preprocess`
delays by one processing frame, `agc` by 2 ms, `resample` by half its
filter (`InputLatency`, `OutputLatency`), `echo` by nothing, and a buffer
by the samples stored in it.

### Jitter

Uneven arrival times of packets or callbacks around their nominal
period.

### Clock drift

A slow difference between two clocks with the same nominal rate, for
example two sound cards both nominally 48000 Hz. Nothing in the library
corrects it yet.

## Buffers, concurrency and errors

### FIFO

First in, first out: samples come out in the order they went in.

### Ring buffer

A fixed-size FIFO whose storage wraps around: writing past the end
continues at the start. `buffer.Ring` and `buffer.SPSC` are ring buffers
of samples.

### Overflow policy

What `buffer.Ring` does with samples that do not fit: `Reject` keeps the
stored samples and stores only what fits; `Overwrite` discards the
oldest unread samples. `buffer.SPSC` always rejects.

### View

A slice that a method returns which points into the buffer's own
storage, such as `ReadSlices` or `WriteSlices` of `buffer`. Each view
stays valid until a documented call, and expires there. A view comes as
two segments, because the stored region may wrap around the end of the
storage; the second segment is empty unless it does.

### Zero-copy

Reading or writing a buffer through its [views](#view) instead of copying
into or out of a caller slice.

### SPSC

Single producer, single consumer: a buffer shared by exactly two
goroutines, one that only writes (the [producer](#producer-and-consumer))
and one that only reads (the consumer). `buffer.SPSC` is one.

### Producer and consumer

The two sides of an SPSC: the producer writes samples in, the consumer
reads them out. Each side owns one index and is the only goroutine that
stores it.

### Wait-free

A property of a method used by two goroutines: it finishes in a bounded
number of its own steps whatever the other goroutine does. It never
waits, locks or retries. Every `buffer.SPSC` method is wait-free.

### Snapshot

A value one goroutine reads about state that another goroutine changes,
such as `buffer.SPSC.Len` called by the producer. It was true when read
and may be stale by the time it is used.

### Quiescent

Not running any method. `buffer.SPSC.Reset` needs both sides quiescent,
for example after their goroutines have been joined.

### Backlog

Samples that pile up in a buffer while its consumer is stalled. For a
live call they are stale; `buffer.SPSC.Consume` drops them.

### Audio path

Every method a caller calls per frame or per chunk: the process, cancel,
run, read and write methods. On it nothing allocates, blocks or panics
([requirements §16](requirements.md#16-real-time-and-memory-safety-rules)).

### Single owner

A type used by one goroutine at a time, with no locking inside. Every
stateful type here is single-owner except `buffer.SPSC`. Use one
instance per stream.

### Aliasing

Two slices that share memory, also called overlap. A method's doc says
whether its input and output may overlap; `agc.AGC.Process` allows them
to be the same slice, the other process methods do not. `preprocess`
works in place on its one frame slice.

### Retention

Keeping a reference to a caller's slice after a method returns. No
method here retains a caller slice.

### Zero value

A value of a type that `New` did not make, such as `var c echo.Canceller`. No
method panics on a zero value: methods with an error result return `ErrConfig`,
and the others return zero values and change nothing, except that the
`preprocess` setters store their setting and the getters of those settings
return what was stored. A zero `Ring` or `SPSC` is an empty buffer of
capacity 0. It is not a usable instance.

### Sentinel error

A predeclared error value, such as `echo.ErrBufferSize`, that a method
returns as it is. Test it with `errors.Is`. Audio-path methods return
only sentinel errors, so the error path does not allocate.

### Wrapped error

An error that carries another error inside it, made with
`fmt.Errorf("%w: ...", err)`. `errors.Is(err, echo.ErrConfig)` finds the
inner error; `err == echo.ErrConfig` does not. Constructors return
wrapped errors, per-frame methods unwrapped sentinels.

### Reset

A method that clears an instance's stream state without allocating, so
it can start a new stream. What each `Reset` keeps differs: `echo` keeps
some learned state, and `resample` with more than one channel may keep
old history. Each package doc says.
