# Glossary

This page defines the terms that the package docs and the pages in `docs/`
use. Each term has one meaning everywhere, except "frame", which has the
three senses given under [Frame](#frame), "block", which has the two
senses given under [Block](#block), and "call", which names a
[call](#call) of a VoIP application or, where a method is the subject,
one invocation of that method.

The terms are grouped by topic. Terms link to the terms they use.

## Signals and samples

### Sample

One number that gives the signal level at one instant. The packages take
`int16` samples on the [16-bit scale](#16-bit-scale).

### 16-bit scale

The range of an `int16`, -32768 to 32767. Full scale is 32768. The `float32`
methods of `resample` use the same scale, not -1 to 1.

### Sample rate

How many samples one channel has in each second, in Hz. Telephony uses
8000 Hz (narrowband) or 16000 Hz (wideband); devices often use 48000 Hz.
A rate can carry sound up to half its value, the Nyquist frequency: 4000 Hz
at 8000 Hz.

### Channel

One signal of the audio: the samples from one microphone, or for one
loudspeaker. Mono audio has one channel; stereo has two, left and right;
surround layouts such as 5.1 and 7.1 have more. `echo`, `preprocess` and
`agc` are mono: one channel. `resample` takes 1 to 256 channels,
interleaved or, with `ProcessChannel`, one channel by [stride](#stride).

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
   `preprocess` process in one method call, `Config.FrameSize`. The send chain
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

The samples you pass in one method call, for a method that takes any
length (`resample`, `buffer`, `agc`). You cut a stream into chunks, and
for these packages the cut does not change the output. Each package doc
gives the conditions. `agc` needs each sample to get the same `speech`
flag however the stream is cut, `resample` needs all of its input to be
read in the end, and `buffer` needs that no write overflows.

### Chunk-partition invariance

Any way of cutting a stream into chunks gives the same output.
`resample`, `buffer` and `agc` have this and test it. `echo` and
`preprocess` take whole processing frames only.

### Level

How loud a signal is over a span of samples. It is usually given as its
[RMS](#rms) in [dBFS](#dbfs): a louder signal has a higher level, and
quiet levels are negative numbers. The `agc` target and gate levels are
RMS levels; the limit level is a peak.

### dB

Decibels: a way to give a ratio between two sizes as a small number.
+6 dB is about twice the amplitude and -20 dB is a tenth. Exactly, it is
`20·log10` of an amplitude ratio, or `10·log10` of a power ratio.

### dBFS

[dB](#db) measured against full scale of the [16-bit scale](#16-bit-scale).
0 dBFS is full scale, the loudest level; quieter signals are negative, and
-6 dBFS is about half of full scale. All-zero [silence](#silence) is minus infinity.
Exactly, it is `20·log10(level/32768)`, so 0 dBFS is amplitude 32768, one
step above the largest `int16` sample. A sample beyond the `int16` range
clips.

Full scale is a peak. The `agc` target and gate levels are [RMS](#rms), so
on them a full-scale sine wave is about -3 dBFS (AES17 calls that level
0 dBFS). The `agc` levels are whole dBFS. If a `float32` sample sent to
`resample` is louder than full scale, it is cut to the largest value an
`int16` can hold.

Other scales have other zero points: a mixing desk meters in dBu, where
0 dBu is 0.775 V RMS, or in VU, where 0 VU is a nominal operating level
with headroom above it, and sound in air is in dB SPL.

### RMS

Root mean square: a measure of the average size of a signal over a span
of samples. Square each sample, take the mean, then take the square root.
The `agc` target and gate levels are RMS levels.

### Silence

Samples that are all zero. "Queue silence" means pass a frame of zeros.

### Clipping

A signal that is too loud for the [16-bit scale](#16-bit-scale). It
would pass -32768 or 32767, so it is cut flat at that end, which sounds
distorted. The AGC's [limiter](#limiter) keeps its output from clipping.

## The call

### Call

One two-way exchange of audio between a near end and a far end. It
carries one [stream](#stream) in each direction.

### Stream

Audio going one way, as samples of one or more [channels](#channel): the
microphone signal, the far-end signal, the decoded audio.

### Send path and receive path

The two directions of a call. The send path takes the near-end signal
(the microphone signal on a device, the received audio on a server)
through the stages the call needs, in order, to the encoder or to the
code that uses it: `echo` (where what is sent can come back), then
`preprocess` and `agc`, each optional. The receive path takes decoded
audio through `resample` to the loudspeaker.

### Pipeline

The chain of [stages](#stage) audio passes through, in order, such as
the send path. The developer guide draws it.

### Stage

One step of the [pipeline](#pipeline), such as removing echo or
converting the rate. A [processor](#processor) does the work of a stage.

### Processor

A value that does one job on the audio, one frame or chunk per method call:
`resample.Resampler`, `echo.Canceller`, `preprocess.Preprocessor` or
`agc.AGC`. Each is created by its package's `New` function, keeps state
between method calls and is [single owner](#single-owner). The `buffer` types
store samples and are not processors.

### RTP

Real-time Transport Protocol (RFC 3550): the protocol that carries the
encoded audio of a call over the network, usually one packet per frame.

### Encoder and decoder

The two halves of a codec, which sit around this library: the encoder
compresses the cleaned send-path audio into packets, the decoder turns
received packets back into samples. Codecs are not part of govoip-dsp.

### Jitter buffer

A buffer that turns packets arriving unevenly (see [jitter](#jitter))
into a steady stream of frames. Not built yet; planned for this library.

## Fixed-point arithmetic

### Fixed point

Arithmetic that uses whole numbers to stand for fractions, with a fixed
number of bits after the point, instead of floating point. The SpeexDSP
FIXED_POINT build, and every package here, process audio this way.

### Q-format

The way to say how many bits of a [fixed-point](#fixed-point) number are
after the point. A Qn number `v` stands for the fraction `v/2^n`. Q15 in
an `int16` covers -1 to just under 1; Q12 in an `int32` has 12 bits after
the point.

### Q15

[Q-format](#q-format) with 15 bits after the point: a stored number `v`
stands for `v/32768`. Gains and filter coefficients of the ports are often
Q15.

### Saturation

Cutting a result to the largest or smallest value its type can hold,
instead of letting it [wrap](#wrap). A sum above 32767 that is stored in
an `int16` becomes 32767. The ports do this exactly where the SpeexDSP
code does, and nowhere else except at the deviations their package docs
list.

### Wrap

What a whole number does when it gets too big and is not
[saturated](#saturation): it starts again at the other end of its range
(32767 + 1 becomes -32768 in an `int16`). Go integers wrap, and the
reference is built with `-fwrapv` so that its C integers wrap the same
way.

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

The side of a call whose audio arrives at your code and goes through the
send path. On a device it is the person at the microphone. On a server it
is the caller's side: the audio Asterisk sends over AudioSocket, or the
RTP stream a media server receives. The near-end signal, which these docs
also call the microphone signal and `echo` calls `mic`, is that audio.
"Near-end speech" is that side talking.

### Far end

The other side of a call. The far-end signal, or far-end reference, is the
audio your code sends toward the near end: on a device, what its
loudspeaker plays; on a server, what it writes back to Asterisk or sends
as RTP. It is the source of the [echo](#echo). `echo` calls it `far`.

### Echo

Far-end audio that comes back in the near-end signal, so that the far end
would hear itself. On a device the microphone picks up what the
loudspeaker plays. On a server, what it sends comes back from the
caller's side: a speakerphone caller's microphone picks it up, or an
acoustic or line echo on their side returns it.

### Echo path

The way the far-end signal takes from `far` to the near-end samples: on
a device, the loudspeaker, the room, the microphone and any buffering on
the way; on a server, the network and the caller's side as well.
An adaptive filter models it as an [impulse response](#impulse-response).

### Acausal

An echo that would arrive in the microphone signal before the far-end
frame that causes it has been passed to the canceller. No filter can
cancel it; pass the far end earlier.

### Bulk delay

The part of the echo path that is pure delay: the time before the first
sound, usually the direct sound from the loudspeaker, reaches the
microphone signal. It is the [echo delay](#echo-delay) seen as a property
of the path. If it is larger than the filter length, the filter cannot
see the echo, so nothing is cancelled.

### Echo delay

The time from a far-end sample being passed to the canceller to its echo
first arriving in the microphone signal. It includes the device and
network buffering, not only the sound's travel time.

### Reverberation time

How long sound takes to fade away in a room, measured as the time to
fall by 60 dB. The Speex manual sizes the echo filter at about a third of
it.

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

What comes out of a system when you put in a single sample of size 1 and
then zeros. It shows how the system spreads that one sample over time. The
echo canceller's filter is an estimate of this for the echo path (the echo
that one far-end sample causes, over time); `echo.Canceller.ImpulseResponse`
returns it.

### Adaptive filter

A filter that changes its settings (its coefficients) while it runs, so
that its output follows the echo in the microphone signal. `echo` uses
the MDF filter.

### MDF

The echo-cancelling algorithm of SpeexDSP `mdf.c`, which `echo` ports.
The name is short for multidelay block frequency-domain adaptive filter.
It splits the filter into blocks of one processing frame and adapts them
with [FFTs](#fft).

### Convergence

The adaptive filter settling on a good model of the echo path. After
`New`, `Reset` or a change of the echo path, little echo is removed until
the filter has converged. That typically takes seconds.

### Double talk

Both ends talking at once. The microphone signal then holds near-end
speech and echo together. The canceller adapts more slowly, so that it
does not learn the near-end speech as echo.

### Residual echo

The echo that the canceller did not remove, still in its output.

### Residual-echo spectrum

The canceller's estimate of how much [residual echo](#residual-echo) is
at each frequency, as a [power spectrum](#power-spectrum), for the frame it
last processed:
`echo.Canceller.Residual`, `FrameSize()+1` values. `preprocess` takes it
in `RunEcho` to suppress that echo further, as SpeexDSP does with
`SPEEX_PREPROCESS_SET_ECHO_STATE`.

### ERLE

Echo return loss enhancement: how much the canceller lowers the echo, in
[dB](#db). 0 dB means no cancellation. Exactly, it is
`10·log10(echo energy / residual energy)` over the same span.

### Playback queue

The queue of far-end frames of the canceller's buffered mode
(`Playback` and `Capture`), as in `speex_echo_playback` and
`speex_echo_capture`. It holds up to three frames.

### Leak estimate

The echo canceller's running guess of how much echo is left in its output.
It finds it by fitting a straight line (linear regression) of the output
against the echo estimate. It scales the residual-echo spectrum.

### DC notch

A filter that removes the constant offset (the zero-frequency part) of
the microphone signal before the canceller models the echo. The
[sample rate](#sample-rate) sets its cut-off.

## Noise suppression and voice activity

### Noise

Any sound in the microphone signal that is not speech or echo, such as a
fan or line hiss. Also called background noise. See
[noise suppression](#noise-suppression).

### Dereverberation

Removing the room's reverberation from the microphone signal. SpeexDSP
1.2.1 has a switch for it whose settings do not work, so `preprocess`
does not offer it.

### Noise suppression

Lowering steady background noise, such as fan or line hiss, while keeping
speech. `preprocess` does it per frequency: per [bin](#bin) and per
[filterbank](#filterbank) band. SpeexDSP and `preprocess` call it
denoise.

### Analysis window

The span of samples that `preprocess` looks at per frame: the current
frame and the one before it, `2*FrameSize` samples. The samples are
weighted so that consecutive windows join smoothly. This is why
`preprocess` delays its output by one frame.

### Bin

One frequency of an [FFT](#fft)'s output, like one slot on a frequency
scale. An FFT of `2*FrameSize` samples gives bins spaced
`SampleRate/(2*FrameSize)` Hz apart, from 0 Hz upwards.
`echo.Canceller.Residual` gives one value per bin from 0 Hz to half the
rate, `FrameSize()+1` values; `preprocess.PSD` gives the first
`FrameSize()` bins.

### Filterbank

The grouping of the FFT bins of `preprocess` into 24 bands, spaced like
human hearing (`filterbank.c` in SpeexDSP). The noise estimate is updated
per bin and added up into the bands. The speech probability is computed
from the bands.

### Synthesis

The last step of `preprocess` for a frame: it turns the processed
frequencies back into samples and adds the overlapping windows together.
"Analysis and synthesis" is the whole way in and out of the frequency
view.

### White noise and pink noise

Random noise with the same power at every frequency (white), or with
power that falls by 3 dB for each octave, so that more of it is at low
frequencies (pink). The tests use both as stationary noise.

### Stationary noise

Noise whose loudness and tone change slowly, such as hiss or hum. Noise
suppression estimates and lowers it. It cannot tell it apart from speech
when the speech is just as steady.

### Power spectrum

How much power a frame has at each frequency, one value per
[bin](#bin). `preprocess.PSD` returns it for the last frame and `NoisePSD`
the noise estimate, both `FrameSize()` values.

### FFT

Fast Fourier transform: a method that turns a span of samples into how
much of each frequency it holds (the [bins](#bin)), and back. `echo` and
`preprocess` use a fixed-point FFT of `2*FrameSize` samples, which limits
the supported frame sizes.

### VAD

Voice activity detection: a per-frame decision whether a frame is speech.
`preprocess` returns it from `Run` and `RunEcho` when `SetVAD(true)`; the
send chain passes it to `agc` as `speech`. The SpeexDSP VAD also reports
speech on [stationary noise](#stationary-noise) and on residual echo.

### Speech probability

The score behind the VAD decision, 0 to 100 percent,
`preprocess.Preprocessor.SpeechProb`. It is a rule of thumb of the
reference, not a true probability. Speech starts when the score is above
`ProbStart` and goes on while it is above `ProbContinue`.

## Gain control

### Gain

The factor by which a stage multiplies its input, given in [dB](#db):
0 dB leaves the level unchanged, +6 dB roughly doubles the amplitude and
-6 dB about halves it. When stages follow each other, their gains in dB
add up (their factors multiply).

### Attenuation

The opposite of gain: how much a stage lowers a level, in [dB](#db).
`preprocess` settings give the most attenuation of noise and residual
echo as negative dB.

### AGC

Automatic gain control: a stage that adjusts its gain so that speech
comes out at a steady level, whatever the input level.

### Target level

The output RMS level in dBFS that the AGC drives a steady signal towards,
`agc.Config.TargetLevel`.

### Gate level

The level, in dBFS, below which a frame cannot raise the AGC gain:
`agc.Config.GateLevel`. It is compared with the [RMS](#rms) of each frame.
A frame below it never raises the gain or moves the level estimate,
though a gain that is falling may go on falling. It is the AGC's only
protection from raising noise in pauses. The guide says
[how to choose it](guide.md#the-send-path-frame-by-frame), under "Gate
above the noise".

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

How fast a smoothed value follows a change: the time it takes to move
about 63% of the way to a new value. `agc.Config.LevelTime` is the time
constant of the level estimate; the limiter recovers with 20 ms.

### Power mean

A kind of average in which large values count more than in a plain mean.
Raise each value to a power, take the mean, then take the matching root:
the order-4 power mean of `x` is the fourth root of the mean of `x^4`.
The AGC's level estimate is one.

### Level estimate

The AGC's running measure of the speech level it sees, from which it
derives the gain. The `agc` package doc gives its definition.

## Resampling

### Downsampling and upsampling

Resampling to a lower rate (downsampling, for example 48000 to
16000 Hz) or to a higher one (upsampling). Downsampling needs a longer
filter, in proportion to the factor.

### Taps

The numbers (coefficients) of a filter, one for each input sample it
weighs. A filter of 64 taps combines 64 input samples into each output
sample.

### Sinc table

The table of filter coefficients that `resample` builds at `New` and at
each rate or quality change. Sinc is the function `sin(πx)/(πx)`; it is
the [impulse response](#impulse-response) of the ideal low-pass filter.
The table holds a windowed sinc (a sinc cut off smoothly at both ends)
sampled at the filter's phases.

### Magic samples

In `resample`, input samples that are left over after a rate or quality
change makes the filter shorter. The next `Process` calls handle them
first; `OutLen` counts them.

### Filter delay

In `resample`, the delay of its low-pass filter, `InputLatency` input
samples. The first output samples of a stream are the filter warming up;
`SkipZeros` drops them.

### Quality level

The `resample` setting from 0 to 10. A higher quality gives less noise
and distortion, and costs more time and adds more latency. The tested
recipes of [usage.md](usage.md) use `QualityVoIP` (3).

### Ratio

The input rate divided by the output rate, as a fraction `num/den` in
lowest terms: 48000 to 16000 Hz is 3/1.

## Timing

### Latency

The delay a stage adds between its input and its output. `preprocess`
delays by one processing frame, `agc` by 2 ms, `resample` by half its
filter (`InputLatency`, `OutputLatency`), `echo` by nothing, and a buffer
by the samples stored in it.

### Jitter

Packets or callbacks that arrive at uneven times, early or late against
their regular period.

### Clock drift

Two clocks that should run at the same rate slowly drift apart, for
example two sound cards that both say 48000 Hz. Nothing in the library
corrects it yet.

## Buffers, concurrency and errors

### FIFO

First in, first out: samples come out in the order they went in.

### Ring buffer

A [FIFO](#fifo) of fixed size whose storage wraps around: writing past
the end continues at the start. `buffer.Ring` and `buffer.SPSC` are ring buffers
of samples.

### Slot and index

A slot is the place for one sample in a [ring buffer](#ring-buffer)'s
storage. An index is a count that names a slot: the slot number is the
remainder of the index divided by the capacity. For example, with a
capacity of 100, index 250 is slot 50. `buffer.SPSC` keeps a write index,
which says where the next sample is written, and a read index, which says
where the next is read.

### Overflow policy

What `buffer.Ring` does with samples that do not fit. `Reject` keeps the
stored samples and stores only what fits. `Overwrite` discards the oldest
unread samples. `buffer.SPSC` always rejects.

### View

A slice that a method returns and that points into the buffer's own
storage, such as `ReadSlices` or `WriteSlices` of `buffer`. A view is
valid until a call that the doc names, and not after it. A view comes as
two segments, because the stored samples may wrap around the end of the
storage. The second segment is empty unless they do.

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

A method used by two goroutines is wait-free if it always finishes in a
bounded number of its own steps, whatever the other goroutine does. It
never waits, locks or retries. Every `buffer.SPSC` method is wait-free.

### Snapshot

A value that one goroutine reads about state that another goroutine
changes, such as `buffer.SPSC.Len` called by the producer. It was true
when read and may be out of date by the time it is used.

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
stateful type here is single-owner except `buffer.SPSC`. Make one set of
processors per call and never share one between calls; `echo` takes the
two streams of one call, microphone and far end, together.

### Aliasing

Two slices that share memory, also called overlap. A method's doc says
whether its input and output may overlap; `agc.AGC.Process` allows them
to be the same slice, the other process methods do not. `preprocess`
works in place on its one frame slice.

### Retention

Keeping a reference to a caller's slice after the method returns. No
method here does this.

### Zero value

A value that `New` did not make, such as `var c echo.Canceller`. It is not
usable, but no method panics on it. Methods with an error result return
`ErrConfig`. The others return zero values and change nothing, except that
the `preprocess` setters store their setting and the getters of those
settings return what was stored. A zero `Ring` or `SPSC` is an empty buffer
of capacity 0.

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
