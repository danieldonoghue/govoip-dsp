# Wiring one VoIP call

This page gives two recipes for the send path of one call with 20 ms
frames: echo cancellation, noise suppression with voice activity
detection (VAD), then automatic gain control. The audio the application
receives is the "microphone" signal; the audio it sends is the far end,
whose echo may return in the received audio.

| Recipe | Sent and received frames | Echo canceller calls |
|---|---|---|
| [A, in step](#recipe-a-sent-and-received-frames-in-step) | one sent frame for every received frame, at the same time | `Cancel` |
| [B, paced by the received frames](#recipe-b-sending-paced-by-the-received-frames) | sent when there is audio, queued one per received frame | `Playback` and `Capture` |

Each recipe opens with the situations it fits and does not fit, then its
caveats, then the code. Every Go block on this page is copied from the
tests in `integration/` (`call_test.go`, `usage_test.go` and
`buffered_test.go`), and `TestUsageCodeIsTested` fails if one is not. Each
section names the tests that back it.

## Before either recipe

- **Samples.** The packages take `[]int16` samples on the 16-bit scale.
  AudioSocket audio messages carry 16-bit signed linear mono samples,
  little-endian, at 8 kHz in the classic format; other formats and rates
  depend on the Asterisk version and channel setup. Converting payload
  bytes to `[]int16` is the application's job and is not part of this
  library.
- **Rates.** The echo canceller supports 8 and 16 kHz only
  ([ADR `wp6-rates-and-shifts`](decisions/wp6-rates-and-shifts.md)). At any
  other network rate recipe A resamples to 16 kHz first; recipe B takes 8
  or 16 kHz only.
- **One call, one goroutine.** Every processor is single-owner: make one
  set per call and use it from one goroutine. Only `buffer.SPSC` is shared,
  between exactly two goroutines.
- **Receive only.** An application that only receives, such as a recorder
  or a monitor, sends nothing whose echo could return. Neither recipe is
  needed there: `preprocess.Preprocessor.Run` and `agc.AGC.Process` alone
  clean the audio.

## Recipe A: sent and received frames in step

### Use cases

Fits:

- A softphone or a device whose sound card drives capture and playback
  in step: each period delivers one captured frame and takes one frame to
  play.
- A network application that sends exactly one frame, audio or silence,
  for each frame it receives, and can hand both to the processing
  goroutine together.
- Network rates other than 8 and 16 kHz: the recipe resamples to 16 kHz.

Does not fit:

- An Asterisk AudioSocket or external-media application that sends audio
  only when it has some (TTS prompts, a bridged leg), so that there is not
  always a sent frame for a received one: use recipe B.
- A recorder or monitor that only receives: see "Receive only" above.

### Caveats

- Each received frame must reach `Cancel` with the frame sent at the same
  time. The recipe pairs them by order: the n-th processed received frame
  takes the n-th sent frame from `far`.
- A received frame that is dropped before processing shifts that pairing
  by one frame for good, and nothing reports it. `onPacket` drops a frame
  when the SPSC is full, which any stall longer than its capacity (200 ms)
  causes. The canceller does no delay estimation, so each shift moves the
  echo by a frame within its 200 ms filter, and once the echo leaves it,
  no echo is cancelled.
- After a backlog drop in `drain` the pairing holds, because `drain` takes
  one far frame for each frame it drops; that needs `far` to return the
  sent frames in order.
- The caller keeps aligned: the order of the sent frames given to `far`,
  one per received frame, including for every received frame that was
  dropped anywhere before `drain`.

### Threads (recipe A)

```mermaid
flowchart LR
  net[Network goroutine] -- onPacket --> rx[(buffer.SPSC)]
  rx -- drain, per tick --> proc[Processing goroutine]
  proc --> sink[ASR, recorder or encoder]
```

The network goroutine reads packets from the socket and hands each frame
to the SPSC ring buffer. The processing goroutine wakes on its own clock,
for example a 20 ms ticker, and processes every whole frame waiting. The
SPSC is wait-free on both sides, so neither goroutine can block the other
(requirements §16). Audio sent back to the network can use a second SPSC
the same way.

### A1. Make the processors and size the buffers

```go
func newCall(netRate, frameMs int) (*call, error) {
	rate := netRate
	if rate != 8000 && rate != 16000 {
		rate = 16000 // the echo canceller supports 8 and 16 kHz only
	}
	c := &call{
		netRate: netRate, rate: rate,
		netFrame: netRate * frameMs / 1000, frame: rate * frameMs / 1000,
	}
	if !preprocess.SupportedFrameSize(c.frame) {
		return nil, fmt.Errorf("call: no preprocessor for %d-sample frames", c.frame)
	}

	var err error
	c.ec, err = echo.New(echo.Config{
		FrameSize:    c.frame,
		FilterLength: rate / 5, // models echo delays up to 200 ms
		SampleRate:   rate,
	})
	if err != nil {
		return nil, err
	}
	c.pre, err = preprocess.New(preprocess.Config{SampleRate: rate, FrameSize: c.frame})
	if err != nil {
		return nil, err
	}
	c.pre.SetVAD(true) // denoise is on by default
	c.gc, err = agc.New(agc.DefaultConfig(rate))
	if err != nil {
		return nil, err
	}
	c.clean = make([]int16, c.frame)
	c.residual = make([]int32, c.ec.ResidualLen())

	if rate != netRate {
		rs := resample.Config{Channels: 1, InRate: netRate, OutRate: rate, Quality: resample.QualityVoIP}
		if c.micRS, err = resample.New(rs); err != nil {
			return nil, err
		}
		if c.farRS, err = resample.New(rs); err != nil {
			return nil, err
		}
		c.rsOut = make([]int16, c.micRS.OutLen(c.netFrame))
		// Less than one frame waits between calls, plus one call's output.
		q := buffer.Config{Capacity: c.frame + len(c.rsOut), Overflow: buffer.Reject}
		if c.micQ, err = buffer.New(q); err != nil {
			return nil, err
		}
		if c.farQ, err = buffer.New(q); err != nil {
			return nil, err
		}
		c.micBuf = make([]int16, c.frame)
		c.farBuf = make([]int16, c.frame)
	}
	return c, nil
}
```

Every size comes from the rate and the frame duration:

| Buffer | Size | At 8 kHz | At 48 kHz (processed at 16 kHz) |
|---|---|---|---|
| Network frame | `netRate * frameMs / 1000` | 160 | 960 |
| Processing frame | `rate * frameMs / 1000`, checked with `preprocess.SupportedFrameSize` | 160 | 320 |
| Echo filter | `rate / 5` samples (200 ms) | 1600 | 3200 |
| Residual spectrum | `echo.Canceller.ResidualLen()`, frame + 1 | 161 | 321 |
| Resampler output | `resample.Resampler.OutLen(netFrame)` | none | 321 |
| Resampled queues (`buffer.Ring`) | frame + resampler output | none | 641 |
| SPSC hand-off | 200 ms of network audio | 1600 | 9600 |
| Output | 2 × frame (one frame per call in practice) | 320 | 640 |

`newCall` allocates everything here once. Nothing on the per-frame path
allocates (`TestNoAllocsRecipe`). `Example_call` prints the sizes for 8,
16 and 48 kHz.

The 200 ms filter is the echo delay the canceller can model, from the far
frame passed to it to the echo in the received frame. A network round trip
longer than that is not cancelled: the canceller does no delay estimation
(see the `echo` package doc, "Stream alignment"). A longer filter costs
more per frame; `docs/performance.md` has the figures.

### A2. Hand frames from the network goroutine

```go
func newRx(c *call) (*buffer.SPSC, error) {
	return buffer.NewSPSC(buffer.SPSCConfig{Capacity: c.netRate / 5})
}
```

```go
func onPacket(rx *buffer.SPSC, p []int16) bool {
	if rx.Free() < len(p) {
		return false // the processor is behind: drop this packet
	}
	rx.Write(p)
	return true
}
```

`onPacket` stores a whole frame or none of it. The SPSC never overwrites
unread samples (ADR `wp2-spsc-overflow`), and a partial frame would shift
every later frame boundary.

### A3. Process every waiting frame

```go
func (c *call) drain(rx *buffer.SPSC, maxBacklog int, mic, out []int16,
	far func() []int16, deliver func([]int16)) error {
	if rx.Len() > maxBacklog {
		// After a stall keep the newest frame, and skip the far frames of
		// the dropped ones so that each frame keeps its far frame.
		for range rx.Consume(rx.Len()-c.netFrame) / c.netFrame {
			far()
		}
	}
	for rx.Len() >= c.netFrame {
		rx.Read(mic)
		n, err := c.process(mic, far(), out)
		if err != nil {
			return err
		}
		deliver(out[:n])
	}
	return nil
}
```

- `far` returns the far-end frame that the application sends to the
  network with each received frame. The canceller needs it exactly as it
  was sent.
- After a stall the SPSC holds a backlog of old audio. `drain` drops all
  but the newest frame (`buffer.SPSC.Consume`, "Dropping a backlog" in the
  `buffer` package doc), and takes and discards one far frame for each
  dropped frame, so that if `far` returns the sent frames in order, every
  processed frame keeps the far frame sent with it. The far end played
  during the stall then has no echo reference, and the canceller does no
  delay estimation, so keeping this pairing is the caller's job.
- A packet that `onPacket` drops has no processed frame, so its far frame
  must be skipped too; the recipe does not do that (see "Caveats").

`TestRecipeThroughSPSC` runs this with a network goroutine writing at
irregular times, at 8, 16 and 48 kHz, under `-race` in `make race`, and
checks that the output equals the same processors run frame by frame in
one goroutine. `TestDrainDropsBacklog` checks the backlog drop and the far
frames it skips.

### A4. The per-frame order

```go
func (c *call) step(mic, far, out []int16) error {
	if err := c.ec.Cancel(mic, far, c.clean); err != nil {
		return err
	}
	if err := c.ec.Residual(c.residual); err != nil {
		return err
	}
	speech, err := c.pre.RunEcho(c.clean, c.residual)
	if err != nil {
		return err
	}
	return c.gc.Process(c.clean, out, speech)
}
```

1. `Cancel` removes the echo of `far` from `mic`.
2. `Residual` reads the residual echo spectrum of that same frame.
3. `RunEcho` suppresses noise and residual echo in place and returns the
   VAD decision. This is the pairing of `docs/contracts.md` section 12.
   `TestReferencePairing` checks it bit for bit against the C chain, in
   which the preprocessor holds the echo state.
4. `Process` applies the AGC, with the VAD decision as `speech`.

At 48 kHz, `process` first resamples the received frame and the far frame
to 16 kHz, each through its own resampler, and runs `step` once per whole
16 kHz frame:

```go
func (c *call) process(mic, far, out []int16) (int, error) {
	if c.micRS == nil {
		return c.frame, c.step(mic, far, out[:c.frame])
	}
	if err := c.resampleInto(c.micRS, c.micQ, mic); err != nil {
		return 0, err
	}
	if err := c.resampleInto(c.farRS, c.farQ, far); err != nil {
		return 0, err
	}
	n := 0
	for c.micQ.Len() >= c.frame && n+c.frame <= len(out) {
		c.micQ.Read(c.micBuf)
		c.farQ.Read(c.farBuf)
		if err := c.step(c.micBuf, c.farBuf, out[n:n+c.frame]); err != nil {
			return n, err
		}
		n += c.frame
	}
	return n, nil
}
```

`resampleInto` checks that each resampler consumed the whole frame and
that its queue took every sample; the sizes of `newCall` rule out both
errors:

```go
func (c *call) resampleInto(r *resample.Resampler, q *buffer.Ring, x []int16) error {
	read, written, err := r.Process(x, c.rsOut)
	if err != nil {
		return err
	}
	if read != len(x) {
		return errShortRead
	}
	if q.Write(c.rsOut[:written]) != written {
		return errQueueFull
	}
	return nil
}
```

With 8, 16 and 48 kHz and 10 or 20 ms frames the ratio divides the frame,
so each call writes exactly one frame; the tests check this on every call
at 8, 16 and 48 kHz with 10 and 20 ms frames. The queues are there for
ratios that do not divide the frame, such as 44.1 kHz; no such rate is
tested. `TestReceivePath` checks that a 20 ms frame per call with an
`OutLen` buffer always consumes the whole frame.

## Recipe B: sending paced by the received frames

### Use cases

Fits:

- An application on Asterisk AudioSocket or external media that sends
  audio only when it has some (TTS prompts, a bridged leg, hold music).
  In the Asterisk sources (`apps/app_audiosocket.c` and
  `channels/chan_audiosocket.c`), Asterisk forwards the call's frames to
  the application on the call's own clock and writes the application's
  audio to the call as it arrives, without re-timing it or adding
  silence. So the application has to pace what it sends. Recipe B paces
  it off the received frames: for each received frame it queues one sent
  frame, or silence when none is ready, and the far end the canceller
  sees is then what was sent.
- Any source where the sent and the received frames reach the processing
  goroutine from two different goroutines, with jitter between them.

Does not fit:

- Network rates other than 8 and 16 kHz: the recipe has no resampler.
  Resample both streams first, as recipe A does.
- An echo that returns within the far end's lag after the frame was sent:
  one frame period plus the shift described under "Caveats". The echo is
  then partly ahead of the far end the canceller sees. Measured with a
  32 ms echo at 20 ms frames: 7.6 dB at 8 kHz without jitter, 0.3 to
  0.7 dB with jitter (U6 fails).
- A recorder or monitor that only receives: see "Receive only" above.

### Caveats

- **One sent frame per received frame.** `drain` queues exactly one sent
  frame with `Playback` before each `Capture`. When no sent frame has
  arrived, it queues silence, and the late frame waits for the next
  received frame. The canceller's queue therefore never runs dry or
  overflows, whatever the jitter.
- **The far end's shift.** `Playback` before `Capture` makes the far end
  the canceller cancels against one frame later than the received frame.
  Each time a sent frame arrives after the received frame it belongs
  with, silence takes its place and every later sent frame moves one
  frame later; the shift never shrinks. While the sender sends one frame
  per received frame, it stays at most the largest delay of a sent frame
  behind its received frame, rounded up to whole frames. A sender pause
  followed by a burst adds the length of the pause. The shift is bounded
  by the sent SPSC (200 ms); beyond that `onPacket` drops sent frames.
- **Echo delay.** The canceller models echo delays from that lag to about
  200 ms after it (the `echo` package doc, "Stream alignment"). With a
  100 ms echo the measured echo stage is 24.7 to 26.2 dB at 8 kHz and
  19.1 to 20.3 dB at 16 kHz for any jitter from 0 to 30 ms; with a 32 ms
  echo it is 0.3 to 7.6 dB (`docs/testing.md`, "Recipe B").
- **Silence when there is nothing to send.** The silence that `drain`
  queues also keeps the echo tail of the last real audio cancelled after
  the sender stops (criterion U7, `TestBufferedIdleSender`). Captures
  that find the queue empty would pass the received frames through.
- **Single owner.** `played` and `received` call one canceller, so both
  run on the processing goroutine. The sent frames reach it through their
  own SPSC, written by the goroutine that writes them to the network.
- **Drops.** A received frame that `onPacket` drops is never processed,
  and its sent frame then goes with the next received frame: the shift
  grows by one frame. Nothing reports it.

### Threads (recipe B)

```mermaid
flowchart LR
  rd[Network reader] -- onPacket --> rx[(rx SPSC)]
  wr[Network writer] -- onPacket, after sending --> sent[(sent SPSC)]
  rx -- drain --> proc[Processing goroutine]
  sent -- drain --> proc
```

The network reader hands each received frame to the rx SPSC. The goroutine
that writes frames to the network hands each frame it has sent to the sent
SPSC. The processing goroutine drains both on its own clock.

### B1. Make the processors and the hand-offs

```go
func newBufferedCall(rate, frameMs int) (*bufferedCall, error) {
	if rate != 8000 && rate != 16000 {
		return nil, fmt.Errorf("call: %d Hz; the echo canceller supports 8 and 16 kHz only", rate)
	}
	b := &bufferedCall{rate: rate, frame: rate * frameMs / 1000}
	if !preprocess.SupportedFrameSize(b.frame) {
		return nil, fmt.Errorf("call: no preprocessor for %d-sample frames", b.frame)
	}
	var err error
	b.ec, err = echo.New(echo.Config{FrameSize: b.frame, FilterLength: rate / 5, SampleRate: rate})
	if err != nil {
		return nil, err
	}
	b.pre, err = preprocess.New(preprocess.Config{SampleRate: rate, FrameSize: b.frame})
	if err != nil {
		return nil, err
	}
	b.pre.SetVAD(true)
	b.gc, err = agc.New(agc.DefaultConfig(rate))
	if err != nil {
		return nil, err
	}
	b.echoed = make([]int16, b.frame)
	b.clean = make([]int16, b.frame)
	b.residual = make([]int32, b.ec.ResidualLen())
	return b, nil
}
```

```go
func newBufferedRx(b *bufferedCall) (sent, rx *buffer.SPSC, err error) {
	if sent, err = buffer.NewSPSC(buffer.SPSCConfig{Capacity: b.rate / 5}); err != nil {
		return nil, nil, err
	}
	rx, err = buffer.NewSPSC(buffer.SPSCConfig{Capacity: b.rate / 5})
	return sent, rx, err
}
```

Both goroutines that feed the SPSCs use `onPacket` from recipe A, which
stores a whole frame or none of it.

### B2. Queue sent frames and clean received ones

```go
func (b *bufferedCall) played(far []int16) error { return b.ec.Playback(far) }
```

```go
func (b *bufferedCall) received(mic, out []int16) error {
	if err := b.ec.Capture(mic, b.echoed); err != nil {
		return err
	}
	copy(b.clean, b.echoed)
	if err := b.ec.Residual(b.residual); err != nil {
		return err
	}
	speech, err := b.pre.RunEcho(b.clean, b.residual)
	if err != nil {
		return err
	}
	return b.gc.Process(b.clean, out, speech)
}
```

The per-frame order after `Capture` is that of recipe A: `Residual`,
`RunEcho`, then the AGC with the VAD decision. `received` keeps the
canceller's output in `echoed`, which the tests measure.

### B3. Drain the received frames, one sent frame each

```go
func (b *bufferedCall) drain(sent, rx *buffer.SPSC, far, mic, out []int16, deliver func([]int16)) error {
	for range rx.Len() / b.frame { // bounds the work of this call
		if sent.Len() >= b.frame {
			sent.Read(far)
		} else {
			clear(far) // nothing sent yet; a later sent frame waits for the next
		}
		if err := b.played(far); err != nil {
			return err
		}
		rx.Read(mic)
		if err := b.received(mic, out); err != nil {
			return err
		}
		deliver(out)
	}
	return nil
}
```

Tests:

- `TestBufferedUnevenTiming`: each frame of both streams is up to 30 ms
  late, independently; the echo stage must reach 15 dB (U6), and does not
  with the scenario's 32 ms echo (a reported finding). It also reports a
  100 ms sender pause followed by a burst.
- `TestBufferedLagAndDelay`: the echo stage against the far end's lag,
  the echo delay and the jitter (reported only).
- `TestBufferedIdleSender`: the silence `drain` queues after the sender
  stops, against captures with nothing queued (U7).
- `TestBufferedTwoGoroutines`: two producer goroutines and the processing
  goroutine, compared with a replay that queues the same sent frame or
  silence for each received frame (U8).
- `TestNoAllocsBuffered`: a tick allocates nothing (U9).

## What the measurements show

`docs/testing.md`, section "`integration`", has the figures. In short:

- At 8 and 16 kHz recipe A passes every pipeline criterion of
  `docs/acceptance/integration.md` on the synthetic call and on
  LibriSpeech speech.
- The VAD reports speech on stationary noise and on residual echo, so
  `GateLevel` is the AGC's only protection. With the default gate of
  -50 dBFS, white noise from about -40 dBFS and pink noise from about
  -52 dBFS at the input are raised by 16 to 23 dB. Set `GateLevel` above
  the residual noise of the line.
- On the chain resampled from 48 kHz the echo canceller cancelled less
  (16 dB) and the AGC held the near end lower (-33 dBFS), a reported
  finding.
- On an Apple M1 Pro the 99th percentile of the whole chain is 0.4% to
  2.5% of the frame duration. The slowest single frame of a 10-minute run
  varied between runs, up to 65% of a 10 ms frame
  (`docs/performance.md`).
- Recipe B keeps cancelling under jitter of up to 30 ms when the echo
  returns well after the far end's lag (100 ms: 19 to 26 dB), and does
  not when the echo returns within it (32 ms: U6 fails, a reported
  finding).
