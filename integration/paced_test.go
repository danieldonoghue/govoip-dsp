package integration

import (
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danieldonoghue/govoip-dsp/agc"
	"github.com/danieldonoghue/govoip-dsp/buffer"
	"github.com/danieldonoghue/govoip-dsp/echo"
	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
	"github.com/danieldonoghue/govoip-dsp/preprocess"
)

// This file is recipe A of docs/usage.md: the processing goroutine sends at
// most one frame per received frame and cancels against the frame it sent.

// pacedCall is the send path of a call whose sending the processing
// goroutine paces off the received frames. Use it from one goroutine.
type pacedCall struct {
	rate, frame int

	ec  *echo.Canceller
	pre *preprocess.Preprocessor
	gc  *agc.AGC

	echoed   []int16 // the echo canceller's output, which the tests measure
	clean    []int16
	residual []int32
}

// newPacedCall makes the processors of one call at rate Hz (8000 or
// 16000), frames of frameMs and an echo filter of filterMs milliseconds.
func newPacedCall(rate, frameMs, filterMs int) (*pacedCall, error) {
	// No resampler here: the echo canceller supports 8 and 16 kHz, so the
	// network must run at one of them.
	if rate != 8000 && rate != 16000 {
		return nil, fmt.Errorf("call: %d Hz; the echo canceller supports 8 and 16 kHz only", rate)
	}
	// One frame is the same duration on the network and here (20 ms: 160
	// samples at 8 kHz, 320 at 16 kHz).
	b := &pacedCall{rate: rate, frame: rate * frameMs / 1000}
	if !preprocess.SupportedFrameSize(b.frame) {
		return nil, fmt.Errorf("call: no preprocessor for %d-sample frames", b.frame)
	}
	var err error
	// The filter must cover the largest echo delay plus its tail; a longer
	// one costs more and adapts more slowly, so filterMs is the caller's.
	b.ec, err = echo.New(echo.Config{FrameSize: b.frame, FilterLength: rate * filterMs / 1000, SampleRate: rate})
	if err != nil {
		return nil, err
	}
	b.pre, err = preprocess.New(preprocess.Config{SampleRate: rate, FrameSize: b.frame})
	if err != nil {
		return nil, err
	}
	b.pre.SetVAD(true) // the VAD decision tells the AGC which frames are speech
	b.gc, err = agc.New(agc.DefaultConfig(rate))
	if err != nil {
		return nil, err
	}
	// Scratch buffers, made once so the per-frame path never allocates.
	b.echoed = make([]int16, b.frame)
	b.clean = make([]int16, b.frame)
	b.residual = make([]int32, b.ec.ResidualLen())
	return b, nil
}

// newPacedHandoffs makes the three hand-offs: pending audio with room for
// pendingMs, and the received and the to-send frames with room for 200 ms.
func newPacedHandoffs(b *pacedCall, pendingMs int) (pending, rx, toWriter *buffer.SPSC, err error) {
	// pending holds what the application wants to send, so its size is the
	// longest burst it may write at once (rate*ms/1000 samples).
	if pending, err = buffer.NewSPSC(buffer.SPSCConfig{Capacity: b.rate * pendingMs / 1000}); err != nil {
		return nil, nil, nil, err
	}
	// rx and toWriter each hold 200 ms (rate/5 samples): room for a stall
	// of the processing goroutine or of the writer.
	if rx, err = buffer.NewSPSC(buffer.SPSCConfig{Capacity: b.rate / 5}); err != nil {
		return nil, nil, nil, err
	}
	toWriter, err = buffer.NewSPSC(buffer.SPSCConfig{Capacity: b.rate / 5})
	return pending, rx, toWriter, err
}

// drain runs on the processing goroutine. For each received frame it sends
// at most one pending frame, or silence, and cancels against that frame.
func (b *pacedCall) drain(pending, rx, toWriter *buffer.SPSC, far, mic, out []int16, deliver func([]int16)) error {
	// One step per received frame waiting now; frames that arrive while
	// this runs wait for the next call, so the work is bounded.
	for range rx.Len() / b.frame {
		// At most one frame is sent per received frame, so sending runs at
		// the call's own pace, whatever the application writes.
		switch {
		case toWriter.Free() < b.frame:
			// The writer is behind: send nothing, keep the pending frame,
			// and assume silence plays in this slot.
			clear(far)
		case pending.Len() >= b.frame:
			pending.Read(far)
			toWriter.Write(far)
		default:
			// Nothing to send: send silence, so the call keeps a steady
			// stream and the canceller keeps a far end for this slot.
			clear(far)
			toWriter.Write(far)
		}
		rx.Read(mic)
		// Cancel against exactly the frame just handed to the writer (or
		// silence when none was): the far end it sees is what was sent.
		if err := b.received(mic, far, out); err != nil {
			return err
		}
		deliver(out)
	}
	return nil
}

// received cleans a received frame against the frame sent with it.
func (b *pacedCall) received(mic, sent, out []int16) error {
	// Cancel models the echo of the frames sent so far, the one sent with
	// this received frame included, and removes it from mic.
	if err := b.ec.Cancel(mic, sent, b.echoed); err != nil {
		return err
	}
	return b.finish(out)
}

// finish runs the rest of the send path on the canceller's output.
func (b *pacedCall) finish(out []int16) error {
	// Keep the canceller's output in echoed and work on a copy, because the
	// preprocessor rewrites its frame in place.
	copy(b.clean, b.echoed)
	// The residual is the echo the canceller left in this frame; read it
	// now, before the next frame replaces it.
	if err := b.ec.Residual(b.residual); err != nil {
		return err
	}
	speech, err := b.pre.RunEcho(b.clean, b.residual)
	if err != nil {
		return err
	}
	// The AGC raises the gain only on speech frames.
	return b.gc.Process(b.clean, out, speech)
}

// simCall simulates recipe A in a call: sent frame k plays in the slot of
// received frame k, and its echo reaches the microphone through path.
type simCall struct {
	frameMs   int
	app       []int16           // frames the application writes, in order
	appAt     []int             // when each is written to the pending SPSC, ms
	local     []int16           // the microphone without echo
	micAt     []int             // when each received frame becomes available, ms
	path      func(i int) []tap // the echo path for microphone sample i
	pendingMs int
}

// simResult is what a simulated call produced. missing counts received
// frames whose echo would have come from a frame not yet sent.
type simResult struct {
	out, echoed, mic, sent []int16
	missing                int
}

func (c simCall) run(t testing.TB, b *pacedCall) simResult {
	pending, rx, toWriter, err := newPacedHandoffs(b, c.pendingMs)
	if err != nil {
		t.Fatal(err)
	}
	f := b.frame
	var r simResult
	fb, mb, o := make([]int16, f), make([]int16, f), make([]int16, f)
	deliver := func(x []int16) {
		r.out = append(r.out, x...)
		r.echoed = append(r.echoed, b.echoed...)
	}
	frames := len(c.local) / f
	r.mic = make([]int16, frames*f)
	ka, km := 0, 0
	for now := 0; km < frames || rx.Len() > 0; now += c.frameMs {
		for ; ka < len(c.appAt) && c.appAt[ka] <= now; ka++ {
			if !onPacket(pending, c.app[ka*f:(ka+1)*f]) {
				t.Fatalf("pending frame %d dropped", ka)
			}
		}
		for ; km < frames && c.micAt[km] <= now; km++ {
			miss := false
			for i := km * f; i < (km+1)*f; i++ {
				var acc int64
				for _, tp := range c.path(i) {
					switch j := i - tp.off; {
					case j < 0:
					case j < len(r.sent):
						acc += int64(tp.v) * int64(r.sent[j])
					default:
						miss = true
					}
				}
				r.mic[i] = sat16(int64(c.local[i]) + int64(sat16((acc+16384)>>15)))
			}
			if miss {
				r.missing++
			}
			if !onPacket(rx, r.mic[km*f:(km+1)*f]) {
				t.Fatalf("received frame %d dropped", km)
			}
		}
		if err := b.drain(pending, rx, toWriter, fb, mb, o, deliver); err != nil {
			t.Fatal(err)
		}
		for toWriter.Len() > 0 { // the writer sends everything handed to it
			n := len(r.sent)
			r.sent = append(r.sent, make([]int16, toWriter.Len())...)
			toWriter.Read(r.sent[n:])
		}
	}
	return r
}

// fixedPath is the call scenario's echo path with its bulk delay at delayMs.
func fixedPath(rate, delayMs int) func(int) []tap {
	taps := room(rate, "integration/path", delayMs, delayMs+48, 16384)
	return func(int) []tap { return taps }
}

// scenarioCall is the call scenario at rate as a simCall: far-end frame k is
// written at k*frameMs plus up to jitterMs, received frame k likewise.
func scenarioCall(rate, frameMs, jitterMs int, path func(int) []tap) simCall {
	s := callScenario(rate, 1)
	f := rate * frameMs / 1000
	n := len(s.far) / f
	local := mix(s.near, testutil.WhiteNoise(len(s.near), 30, testutil.Seed("integration/noise")))
	return simCall{
		frameMs: frameMs, app: s.far, local: local, path: path, pendingMs: 200,
		appAt: arrivals(n, frameMs, jitterMs, "integration/buffered/far"),
		micAt: arrivals(n, frameMs, jitterMs, "integration/buffered/mic"),
	}
}

// stage is the echo stage over [from, to) seconds, as B1 measures it.
func (r simResult) stage(rate int, from, to float64) float64 {
	return dB(energy(window(r.mic, rate, from, to)), energy(window(r.echoed, rate, from, to)))
}

// TestPacedUnevenTiming checks echo cancellation with jittered pending and
// received frames (U6), and reports a sender pause and burst (U10).
func TestPacedUnevenTiming(t *testing.T) {
	const frameMs = 20
	for _, rate := range []int{8000, 16000} {
		t.Run(fmt.Sprintf("%dk", rate/1000), func(t *testing.T) {
			skipUnderRace(t)
			c := scenarioCall(rate, frameMs, 30, fixedPath(rate, 32))
			r := c.run(t, newPacedTest(t, rate, frameMs, 200))
			stage := r.stage(rate, 8, 14)
			chain := dB(energy(window(r.mic, rate, 8, 14)), energy(window(r.out, rate, 8, 14)))
			t.Logf("32 ms echo, jitter 0 to 30 ms: echo stage %.1f dB, chain %.1f dB over 8 to 14 s; %d frames with echo of unsent frames",
				stage, chain, r.missing)
			// U6 fails, a reported finding (docs/testing.md); the test shows
			// if that changes.
			if stage < 15 {
				t.Logf("U6 fails, a reported finding: echo stage %.1f dB, want at least 15", stage)
			} else {
				t.Errorf("U6 was a reported finding but now passes (%.1f dB); update docs/testing.md", stage)
			}

			if !*report {
				return // the rest only reports figures (-report)
			}
			// A 100 ms application pause at 6 s, then a burst: reported only.
			c.appAt = append([]int(nil), c.appAt...)
			for k, ms := range c.appAt {
				if ms >= 6000 && ms < 6100 {
					c.appAt[k] = 6100
				}
			}
			r3 := c.run(t, newPacedTest(t, rate, frameMs, 200))
			t.Logf("application pause of 100 ms at 6 s: echo stage %.1f dB over 8 to 14 s, %.1f dB over 6 to 8 s",
				r3.stage(rate, 8, 14), r3.stage(rate, 6, 8))
		})
	}
}

// TestPacedIdleSender compares drain, which sends silence once the
// application stops, with captures that find nothing queued (U7).
func TestPacedIdleSender(t *testing.T) {
	const frameMs = 20
	for _, rate := range []int{8000, 16000} {
		t.Run(fmt.Sprintf("%dk", rate/1000), func(t *testing.T) {
			skipUnderRace(t)
			n := 16 * rate
			frame := rate * frameMs / 1000
			frames := n / frame
			stop := 14 * 1000 / frameMs // the application writes nothing from 14 s on
			far := gate(testutil.WhiteNoise(n, 8000, testutil.Seed("integration/idle")), rate, []segment{{2, 14}})
			at := make([]int, frames)
			for k := range at {
				at[k] = k * frameMs
			}
			c := simCall{
				frameMs: frameMs, app: far[:stop*frame], appAt: at[:stop], micAt: at, pendingMs: 200,
				local: testutil.WhiteNoise(n, 30, testutil.Seed("integration/noise")),
				path:  fixedPath(rate, 32),
			}
			r := c.run(t, newPacedTest(t, rate, frameMs, 200))
			if len(r.sent) != len(r.mic) {
				t.Fatalf("writer sent %d samples for %d received, want one frame per received frame", len(r.sent), len(r.mic))
			}
			for i, x := range r.sent[stop*frame:] {
				if x != 0 {
					t.Fatalf("sample %d sent after the application stopped is %d, want silence", stop*frame+i, x)
				}
			}

			// The counterfactual, which drain never does: no Playback after 14 s.
			ec, err := echo.New(echo.Config{FrameSize: frame, FilterLength: rate / 5, SampleRate: rate})
			if err != nil {
				t.Fatal(err)
			}
			nothing := make([]int16, frames*frame)
			for k := range frames {
				if k < stop {
					if err := ec.Playback(r.sent[k*frame : (k+1)*frame]); err != nil {
						t.Fatal(err)
					}
				}
				if err := ec.Capture(r.mic[k*frame:(k+1)*frame], nothing[k*frame:(k+1)*frame]); err != nil {
					t.Fatal(err)
				}
			}
			gain := dB(energy(window(nothing, rate, 14.0, 14.3)), energy(window(r.echoed, rate, 14.0, 14.3)))
			t.Logf("echo tail over 14.0 to 14.3 s: %.1f dB lower when silence is sent", gain)
			if gain < 10 {
				t.Errorf("U7: queuing silence lowers the echo tail by %.1f dB, want at least 10", gain)
			}
		})
	}
}

// newPacedTest is newPacedCall for a test.
func newPacedTest(t testing.TB, rate, frameMs, filterMs int) *pacedCall {
	b, err := newPacedCall(rate, frameMs, filterMs)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// replayPaced passes sent frame k with received frame k to a new call, as
// drain does, and returns the canceller's output and the chain's.
func replayPaced(t testing.TB, rate, frameMs, filterMs int, sent, mic []int16) (echoed, out []int16) {
	b := newPacedTest(t, rate, frameMs, filterMs)
	f := b.frame
	o := make([]int16, f)
	for k := 0; (k+1)*f <= len(mic); k++ {
		if err := b.received(mic[k*f:(k+1)*f], sent[k*f:(k+1)*f], o); err != nil {
			t.Fatal(err)
		}
		echoed = append(echoed, b.echoed...)
		out = append(out, o...)
	}
	return echoed, out
}

// TestPacedBurstSender writes 12 s of far end into the pending SPSC at
// once and checks that the writer sends what the canceller gets (U12).
func TestPacedBurstSender(t *testing.T) {
	const frameMs, filterMs = 20, 256
	for _, rate := range []int{8000, 16000} {
		t.Run(fmt.Sprintf("%dk", rate/1000), func(t *testing.T) {
			skipUnderRace(t)
			c := scenarioCall(rate, frameMs, 30, fixedPath(rate, 100))
			f := rate * frameMs / 1000
			first, last := 2*1000/frameMs, 14*1000/frameMs
			c.app = c.app[first*f : last*f]
			c.appAt = make([]int, last-first)
			for k := range c.appAt {
				c.appAt[k] = 2000 // the whole burst at once
			}
			c.pendingMs = 15000
			r := c.run(t, newPacedTest(t, rate, frameMs, filterMs))
			if len(r.sent) != len(r.mic) {
				t.Fatalf("writer sent %d samples for %d received", len(r.sent), len(r.mic))
			}
			replayEchoed, _ := replayPaced(t, rate, frameMs, filterMs, r.sent, r.mic)
			testutil.Compare(t, "replay of the sent frames", replayEchoed, r.echoed)
			nonSilent := func(x []int16) []int16 {
				var y []int16
				for k := 0; (k+1)*f <= len(x); k++ {
					if slices.ContainsFunc(x[k*f:(k+1)*f], func(v int16) bool { return v != 0 }) {
						y = append(y, x[k*f:(k+1)*f]...)
					}
				}
				return y
			}
			testutil.Compare(t, "non-silent frames sent against the application's", nonSilent(c.app), nonSilent(r.sent))
			stage := r.stage(rate, 8, 14)
			t.Logf("burst of 12 s at 2 s, 100 ms echo, jitter 0 to 30 ms: echo stage %.1f dB over 8 to 14 s; %d frames with echo of unsent frames",
				stage, r.missing)
			if stage < 15 {
				t.Errorf("U12: echo stage %.1f dB, want at least 15", stage)
			}
		})
	}
}

// TestPacedFourGoroutines runs the application, the network reader, the
// writer and the processing goroutine concurrently (U8).
func TestPacedFourGoroutines(t *testing.T) {
	fourGoroutines(t)
}

func fourGoroutines(t *testing.T) {
	const frameMs, rate = 20, 8000
	seconds := 10
	if testing.Short() {
		seconds = 3
	}
	s := callScenario(rate, 1)
	b := newPacedTest(t, rate, frameMs, 200)
	f := b.frame
	frames := seconds * 1000 / frameMs
	mic := s.mic[:frames*f]
	// Sample 0 of each application frame is its index plus 1; silence reads 0.
	app := append([]int16(nil), s.far[:frames*f]...)
	for k := range frames {
		app[k*f] = int16(k + 1)
	}
	pending, rx, toWriter, err := newPacedHandoffs(b, 1000)
	if err != nil {
		t.Fatal(err)
	}

	var stop atomic.Bool
	var wg sync.WaitGroup
	defer wg.Wait()
	defer stop.Store(true) // also when a check fails
	produce := func(q *buffer.SPSC, x []int16, burst int, seed string) {
		defer wg.Done()
		r := testutil.SplitMix64{State: testutil.Seed(seed)}
		for k := 0; k < frames && !stop.Load(); {
			for n := 1 + r.Intn(burst); n > 0 && k < frames; n-- {
				if !onPacket(q, x[k*f:(k+1)*f]) {
					break
				}
				k++
			}
			time.Sleep(time.Duration(r.Intn(2*frameMs*1000)) * time.Microsecond)
		}
	}
	var written []int16 // read by the writer goroutine only, until wg.Wait
	writer := func() {
		defer wg.Done()
		buf := make([]int16, f)
		for !stop.Load() {
			if toWriter.Len() >= f {
				toWriter.Read(buf)
				written = append(written, buf...)
				continue
			}
			time.Sleep(time.Millisecond)
		}
	}
	wg.Add(3)
	go produce(rx, mic, 1, t.Name()+"/rx")
	go produce(pending, app, 4, t.Name()+"/app")
	go writer()

	fb, mb := make([]int16, f), make([]int16, f)
	out := make([]int16, f)
	var got, gotOut, played []int16
	deliver := func(x []int16) {
		got = append(got, b.echoed...)
		gotOut = append(gotOut, x...)
		played = append(played, fb...)
	}
	tick := time.NewTicker(time.Duration(frameMs) * time.Millisecond / 4)
	defer tick.Stop()
	deadline := time.Now().Add(time.Minute)
	for len(got) < len(mic) && time.Now().Before(deadline) {
		<-tick.C
		if err := b.drain(pending, rx, toWriter, fb, mb, out, deliver); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != len(mic) {
		t.Fatalf("processed %d of %d received samples", len(got), len(mic))
	}
	for toWriter.Len() > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	stop.Store(true)
	wg.Wait()

	if len(written) != len(played) {
		t.Fatalf("writer sent %d samples, the canceller got %d (the writer fell behind)", len(written), len(played))
	}
	testutil.Compare(t, "written against played", played, written)
	next, silent := 1, 0
	for k := range frames {
		switch m := int(written[k*f]); {
		case m == 0:
			silent++
		case m != next:
			t.Fatalf("sent frame %d is application frame %d, want %d (one per received frame, in order)", k, m-1, next-1)
		default:
			next++
		}
	}
	t.Logf("%d received frames, %d with silence sent", frames, silent)
	wantEchoed, wantOut := replayPaced(t, rate, frameMs, 200, written, mic)
	testutil.Compare(t, "replay, canceller output", wantEchoed, got)
	testutil.Compare(t, "replay, chain output", wantOut, gotOut)
}

// TestNoAllocsPaced checks that a processing tick of recipe A, all three
// hand-offs included, allocates nothing (U9).
func TestNoAllocsPaced(t *testing.T) {
	for _, rate := range []int{8000, 16000} {
		t.Run(fmt.Sprintf("%dk", rate/1000), func(t *testing.T) {
			skipUnderRace(t)
			noAllocsPaced(t, rate)
		})
	}
}

func noAllocsPaced(t *testing.T, rate int) {
	b := newPacedTest(t, rate, 20, 200)
	pending, rx, toWriter, err := newPacedHandoffs(b, 200)
	if err != nil {
		t.Fatal(err)
	}
	s := callScenario(rate, 1)
	frames := len(s.mic) / b.frame
	fb, mb, out, wb := make([]int16, b.frame), make([]int16, b.frame), make([]int16, b.frame), make([]int16, b.frame)
	deliver := func([]int16) {}
	k := 0
	tick := func() {
		if k%4 != 3 { // every 4th tick has nothing pending, so drain sends silence
			onPacket(pending, s.far[k*b.frame:(k+1)*b.frame])
		}
		onPacket(rx, s.mic[k*b.frame:(k+1)*b.frame])
		if err := b.drain(pending, rx, toWriter, fb, mb, out, deliver); err != nil {
			t.Fatal(err)
		}
		toWriter.Read(wb) // the writer
		k = (k + 1) % frames
	}
	for range 12 * 50 { // past adaptation
		tick()
	}
	testutil.AssertNoAllocs(t, 200, tick)
}

// TestPacedDelaySweep reports the echo stage against the far end's lag,
// the echo delay and the jitter (reported only).
func TestPacedDelaySweep(t *testing.T) {
	reportOnly(t)
	skipUnderRace(t)
	const frameMs = 20
	for _, rate := range []int{8000, 16000} {
		frame := rate * frameMs / 1000
		for _, delayMs := range []int{32, 40, 50, 60, 80, 100} {
			base := callScenario(rate, 1)
			mic := mix(convolve(base.far, room(rate, "integration/path", delayMs, delayMs+48, 16384)), base.near,
				testutil.WhiteNoise(len(base.far), 30, testutil.Seed("integration/noise")))
			erle := func(e []int16) float64 { return dB(energy(window(mic, rate, 8, 14)), energy(window(e, rate, 8, 14))) }
			for lag := 0; lag <= 2; lag++ {
				ec, err := echo.New(echo.Config{FrameSize: frame, FilterLength: rate / 5, SampleRate: rate})
				if err != nil {
					t.Fatal(err)
				}
				far := append(make([]int16, lag*frame), base.far...)
				e := make([]int16, len(mic)/frame*frame)
				for i := 0; i+frame <= len(e); i += frame {
					if err := ec.Cancel(mic[i:i+frame], far[i:i+frame], e[i:i+frame]); err != nil {
						t.Fatal(err)
					}
				}
				t.Logf("%d Hz, echo delay %d ms: Cancel with the far end %d frames late, echo stage %.1f dB", rate, delayMs, lag, erle(e))
			}
			for _, j := range []int{0, 30} {
				r := scenarioCall(rate, frameMs, j, fixedPath(rate, delayMs)).run(t, newPacedTest(t, rate, frameMs, 200))
				t.Logf("%d Hz, echo delay %d ms: drain, jitter 0 to %d ms, echo stage %.1f dB, %d frames with echo of unsent frames",
					rate, delayMs, j, r.stage(rate, 8, 14), r.missing)
			}
		}
	}
}

// TestPacedLongDelay checks recipe A at echo delays of 100 and 200 ms
// with a 256 ms filter (U11), and reports the recovery from a delay step.
func TestPacedLongDelay(t *testing.T) {
	skipUnderRace(t)
	const frameMs, filterMs = 20, 256
	for _, rate := range []int{8000, 16000} {
		run := func(path func(int) []tap) simResult {
			return scenarioCall(rate, frameMs, 30, path).run(t, newPacedTest(t, rate, frameMs, filterMs))
		}
		for _, delayMs := range []int{100, 200} {
			t.Run(fmt.Sprintf("%dk_%dms", rate/1000, delayMs), func(t *testing.T) {
				r := run(fixedPath(rate, delayMs))
				stage := r.stage(rate, 8, 14)
				t.Logf("echo delay %d ms, jitter 0 to 30 ms, filter %d ms: echo stage %.1f dB over 8 to 14 s; %d frames with echo of unsent frames",
					delayMs, filterMs, stage, r.missing)
				if stage < 15 {
					t.Errorf("U11: echo stage %.1f dB, want at least 15", stage)
				}
			})
		}
		t.Run(fmt.Sprintf("%dk_step_100_140ms", rate/1000), func(t *testing.T) {
			reportOnly(t)
			p100, p140 := fixedPath(rate, 100), fixedPath(rate, 140)
			stepAt := 8 * rate
			r := run(func(i int) []tap {
				if i < stepAt {
					return p100(i)
				}
				return p140(i)
			})
			recovered := -1.0
			for w := 8.0; w+0.5 <= 14; w += 0.5 {
				if r.stage(rate, w, w+0.5) >= 10 {
					recovered = w - 8
					break
				}
			}
			before := r.stage(rate, 6, 8)
			if recovered < 0 {
				t.Logf("delay step 100 to 140 ms at 8 s: echo stage %.1f dB over 6 to 8 s; 10 dB not reached again before 14 s", before)
				return
			}
			t.Logf("delay step 100 to 140 ms at 8 s: echo stage %.1f dB over 6 to 8 s; 10 dB again after %.1f s", before, recovered)
		})
	}
}

// TestPacedWriterFull checks drain's writer-full branch on an adapted
// canceller: nothing is sent, silence is cancelled against, the frame waits.
func TestPacedWriterFull(t *testing.T) {
	const rate, frameMs, after = 8000, 20, 60
	t.Run("8k", func(t *testing.T) {
		b := newPacedTest(t, rate, frameMs, 200)
		f := b.frame
		pending, rx, toWriter, err := newPacedHandoffs(b, 200)
		if err != nil {
			t.Fatal(err)
		}
		s := callScenario(rate, 1)
		frame := func(x []int16, k int) []int16 { return x[(100+k)*f : (101+k)*f] } // from 2 s, far end active
		warm := 200                                                                 // the canceller adapts; the full slot's frame must not be silent
		for !slices.ContainsFunc(frame(s.far, warm), func(x int16) bool { return x != 0 }) {
			warm++
		}
		fb, mb, out := make([]int16, f), make([]int16, f), make([]int16, f)
		var got, sent []int16
		deliver := func([]int16) { got = append(got, b.echoed...) }
		step := func(k int) {
			onPacket(rx, frame(s.mic, k))
			if err := b.drain(pending, rx, toWriter, fb, mb, out, deliver); err != nil {
				t.Fatal(err)
			}
		}
		writer := func() {
			for toWriter.Len() > 0 {
				n := len(sent)
				sent = append(sent, make([]int16, toWriter.Len())...)
				toWriter.Read(sent[n:])
			}
		}
		for k := range warm { // the canceller adapts
			onPacket(pending, frame(s.far, k))
			step(k)
			writer()
		}

		for toWriter.Free() >= f { // the writer stalls: its SPSC fills up
			onPacket(toWriter, make([]int16, f))
		}
		full := toWriter.Len()
		onPacket(pending, frame(s.far, warm))
		step(warm)
		if toWriter.Len() != full {
			t.Fatalf("writer SPSC holds %d samples after a drain with it full, want %d", toWriter.Len(), full)
		}
		if pending.Len() != f {
			t.Fatalf("pending holds %d samples, want the one waiting frame (%d)", pending.Len(), f)
		}
		toWriter.Consume(toWriter.Len()) // the writer catches up; the filler was never audio

		for k := warm + 1; k <= warm+after; k++ {
			onPacket(pending, frame(s.far, k))
			step(k)
			writer()
		}
		// Sent: the warm-up frames, nothing in the full slot, then the
		// waiting frame and the rest, one per received frame.
		wantSent := slices.Clone(s.far[100*f : (100+warm+after)*f])
		testutil.Compare(t, "sent", wantSent, sent)
		played := slices.Concat(s.far[100*f:(100+warm)*f], make([]int16, f), s.far[(100+warm)*f:(100+warm+after)*f])
		want, _ := replayPaced(t, rate, frameMs, 200, played, s.mic[100*f:(101+warm+after)*f])
		testutil.Compare(t, "canceller output", want, got)
	})
}
