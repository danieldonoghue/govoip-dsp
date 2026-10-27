package integration

import (
	"fmt"
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

// This file is recipe B of docs/usage.md: the processing goroutine sends one
// frame per received frame and passes the same frame to Playback.

// bufferedCall is the send path of a call whose sent and received frames
// arrive independently. Use it from one goroutine.
type bufferedCall struct {
	rate, frame int
	cancel      bool // the tests' variant (ii), see drainWith

	ec  *echo.Canceller
	pre *preprocess.Preprocessor
	gc  *agc.AGC

	echoed   []int16 // the echo canceller's output, which the tests measure
	clean    []int16
	residual []int32
}

// newBufferedCall makes the processors of one call at rate Hz (8000 or
// 16000), frames of frameMs and an echo filter of filterMs milliseconds.
func newBufferedCall(rate, frameMs, filterMs int) (*bufferedCall, error) {
	if rate != 8000 && rate != 16000 {
		return nil, fmt.Errorf("call: %d Hz; the echo canceller supports 8 and 16 kHz only", rate)
	}
	b := &bufferedCall{rate: rate, frame: rate * frameMs / 1000}
	if !preprocess.SupportedFrameSize(b.frame) {
		return nil, fmt.Errorf("call: no preprocessor for %d-sample frames", b.frame)
	}
	var err error
	b.ec, err = echo.New(echo.Config{FrameSize: b.frame, FilterLength: rate * filterMs / 1000, SampleRate: rate})
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

// played queues a frame that was sent to the network.
func (b *bufferedCall) played(far []int16) error { return b.ec.Playback(far) }

// received cleans a received frame against the queued sent frames.
func (b *bufferedCall) received(mic, out []int16) error {
	if err := b.ec.Capture(mic, b.echoed); err != nil {
		return err
	}
	return b.finish(out)
}

// receivedWith cleans a received frame against the frame sent with it.
func (b *bufferedCall) receivedWith(mic, far, out []int16) error {
	if err := b.ec.Cancel(mic, far, b.echoed); err != nil {
		return err
	}
	return b.finish(out)
}

// finish runs the rest of the send path on the canceller's output.
func (b *bufferedCall) finish(out []int16) error {
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

// drain runs on the processing goroutine. For each received frame it sends
// at most one pending frame, or silence, and passes the same frame to played.
func (b *bufferedCall) drain(pending, rx, toWriter *buffer.SPSC, far, mic, out []int16, deliver func([]int16)) error {
	for range rx.Len() / b.frame { // bounds the work of this call
		switch {
		case toWriter.Free() < b.frame:
			clear(far) // the writer is behind: send nothing, keep the pending frame
		case pending.Len() >= b.frame:
			pending.Read(far)
			toWriter.Write(far)
		default:
			clear(far) // nothing to send: send silence
			toWriter.Write(far)
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

// drainWith is drain with Cancel against the frame just sent instead of
// Playback and Capture: variant (ii), which the tests measure for comparison.
func (b *bufferedCall) drainWith(pending, rx, toWriter *buffer.SPSC, far, mic, out []int16, deliver func([]int16)) error {
	for range rx.Len() / b.frame {
		switch {
		case toWriter.Free() < b.frame:
			clear(far)
		case pending.Len() >= b.frame:
			pending.Read(far)
			toWriter.Write(far)
		default:
			clear(far)
			toWriter.Write(far)
		}
		rx.Read(mic)
		if err := b.receivedWith(mic, far, out); err != nil {
			return err
		}
		deliver(out)
	}
	return nil
}

// drainVariant runs drain, or drainWith for variant (ii).
func (b *bufferedCall) drainVariant(pending, rx, toWriter *buffer.SPSC, far, mic, out []int16, deliver func([]int16)) error {
	if b.cancel {
		return b.drainWith(pending, rx, toWriter, far, mic, out, deliver)
	}
	return b.drain(pending, rx, toWriter, far, mic, out, deliver)
}

// newBufferedHandoffs makes the three hand-offs: pending audio with room for
// pendingMs, and the received and the to-send frames with room for 200 ms.
func newBufferedHandoffs(b *bufferedCall, pendingMs int) (pending, rx, toWriter *buffer.SPSC, err error) {
	if pending, err = buffer.NewSPSC(buffer.SPSCConfig{Capacity: b.rate * pendingMs / 1000}); err != nil {
		return nil, nil, nil, err
	}
	if rx, err = buffer.NewSPSC(buffer.SPSCConfig{Capacity: b.rate / 5}); err != nil {
		return nil, nil, nil, err
	}
	toWriter, err = buffer.NewSPSC(buffer.SPSCConfig{Capacity: b.rate / 5})
	return pending, rx, toWriter, err
}

// arrivals returns when each of n frames becomes available: k*frameMs plus
// a seeded delay of 0 to maxMs ms, never before frame k-1.
func arrivals(n, frameMs, maxMs int, seed string) []int {
	r := testutil.SplitMix64{State: testutil.Seed(seed)}
	at := make([]int, n)
	last := 0
	for k := range at {
		ms := max(last, k*frameMs+r.Intn(maxMs+1))
		at[k], last = ms, ms
	}
	return at
}

// simCall simulates recipe B in a call: sent frame k plays in the slot of
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

func (c simCall) run(t testing.TB, b *bufferedCall) simResult {
	pending, rx, toWriter, err := newBufferedHandoffs(b, c.pendingMs)
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
		if err := b.drainVariant(pending, rx, toWriter, fb, mb, o, deliver); err != nil {
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

// scenarioCall is the call scenario at rate as a simCall: the application
// writes far-end frame k at k*frameMs plus up to jitterMs, and so on.
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

// TestBufferedUnevenTiming checks echo cancellation with jittered pending and
// received frames (U6), and reports a sender pause and burst (U10).
func TestBufferedUnevenTiming(t *testing.T) {
	const frameMs = 20
	for _, rate := range []int{8000, 16000} {
		for _, v := range variants {
			t.Run(fmt.Sprintf("%dk_%s", rate/1000, v.name), func(t *testing.T) {
				skipUnderRace(t)
				c := scenarioCall(rate, frameMs, 30, fixedPath(rate, 32))
				r := c.run(t, newVariant(t, rate, frameMs, 200, v.cancel))
				stage := r.stage(rate, 8, 14)
				chain := dB(energy(window(r.mic, rate, 8, 14)), energy(window(r.out, rate, 8, 14)))
				t.Logf("32 ms echo, jitter 0 to 30 ms: echo stage %.1f dB, chain %.1f dB over 8 to 14 s; %d frames with echo of unsent frames",
					stage, chain, r.missing)
				// U6 fails in both variants, a reported finding (docs/testing.md); the
				// test shows if that changes.
				if stage < 15 {
					t.Logf("U6 fails, a reported finding: echo stage %.1f dB, want at least 15", stage)
				} else {
					t.Errorf("U6 was a reported finding but now passes (%.1f dB); update docs/testing.md", stage)
				}

				// A 100 ms application pause at 6 s, then a burst: reported only.
				c.appAt = append([]int(nil), c.appAt...)
				for k, ms := range c.appAt {
					if ms >= 6000 && ms < 6100 {
						c.appAt[k] = 6100
					}
				}
				r3 := c.run(t, newVariant(t, rate, frameMs, 200, v.cancel))
				t.Logf("application pause of 100 ms at 6 s: echo stage %.1f dB over 8 to 14 s, %.1f dB over 6 to 8 s",
					r3.stage(rate, 8, 14), r3.stage(rate, 6, 8))
			})
		}
	}
}

// TestBufferedIdleSender compares drain, which sends and queues silence once
// the application stops, with captures that find nothing queued (U7).
func TestBufferedIdleSender(t *testing.T) {
	const frameMs = 20
	for _, rate := range []int{8000, 16000} {
		for _, v := range variants {
			t.Run(fmt.Sprintf("%dk_%s", rate/1000, v.name), func(t *testing.T) {
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
				r := c.run(t, newVariant(t, rate, frameMs, 200, v.cancel))
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
				t.Logf("echo tail over 14.0 to 14.3 s: %.1f dB lower when silence is sent and queued", gain)
				if gain < 10 {
					t.Errorf("U7: queuing silence lowers the echo tail by %.1f dB, want at least 10", gain)
				}
			})
		}
	}
}

// variants are the two ways recipe B is measured: (i) Playback then Capture,
// (ii) Cancel with the frame just sent.
var variants = []struct {
	name   string
	cancel bool
}{{"playback", false}, {"cancel", true}}

// newVariant is newBufferedCall for one of the variants.
func newVariant(t testing.TB, rate, frameMs, filterMs int, cancel bool) *bufferedCall {
	b, err := newBufferedCall(rate, frameMs, filterMs)
	if err != nil {
		t.Fatal(err)
	}
	b.cancel = cancel
	return b
}

// replay passes sent frame k with received frame k to a new call of the same
// variant, as drain does, and returns the canceller's output.
func replay(t testing.TB, rate, frameMs, filterMs int, cancel bool, sent, mic []int16) []int16 {
	b := newVariant(t, rate, frameMs, filterMs, cancel)
	f := b.frame
	out := make([]int16, f)
	var got []int16
	for k := 0; (k+1)*f <= len(mic); k++ {
		var err error
		if cancel {
			err = b.receivedWith(mic[k*f:(k+1)*f], sent[k*f:(k+1)*f], out)
		} else if err = b.played(sent[k*f : (k+1)*f]); err == nil {
			err = b.received(mic[k*f:(k+1)*f], out)
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, b.echoed...)
	}
	return got
}

// TestBufferedBurstSender writes 12 s of far end into the pending SPSC at
// once and checks that the writer sends what Playback gets (U12).
func TestBufferedBurstSender(t *testing.T) {
	const frameMs, filterMs = 20, 256
	for _, rate := range []int{8000, 16000} {
		for _, v := range variants {
			t.Run(fmt.Sprintf("%dk_%s", rate/1000, v.name), func(t *testing.T) {
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
				r := c.run(t, newVariant(t, rate, frameMs, filterMs, v.cancel))
				if len(r.sent) != len(r.mic) {
					t.Fatalf("writer sent %d samples for %d received", len(r.sent), len(r.mic))
				}
				testutil.Compare(t, "replay of the sent frames", replay(t, rate, frameMs, filterMs, v.cancel, r.sent, r.mic), r.echoed)
				stage := r.stage(rate, 8, 14)
				t.Logf("burst of 12 s at 2 s, 100 ms echo, jitter 0 to 30 ms: echo stage %.1f dB over 8 to 14 s; %d frames with echo of unsent frames",
					stage, r.missing)
				if stage < 15 {
					t.Errorf("U12: echo stage %.1f dB, want at least 15", stage)
				}
			})
		}
	}
}

// TestBufferedTwoGoroutines runs the application, the network reader, the
// writer and the processing goroutine concurrently (U8).
func TestBufferedTwoGoroutines(t *testing.T) {
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) { twoGoroutines(t, v.cancel) })
	}
}

func twoGoroutines(t *testing.T, cancel bool) {
	const frameMs, rate = 20, 8000
	seconds := 10
	if testing.Short() {
		seconds = 3
	}
	s := callScenario(rate, 1)
	b := newVariant(t, rate, frameMs, 200, cancel)
	f := b.frame
	frames := seconds * 1000 / frameMs
	mic := s.mic[:frames*f]
	// Sample 0 of each application frame is its index plus 1; silence reads 0.
	app := append([]int16(nil), s.far[:frames*f]...)
	for k := range frames {
		app[k*f] = int16(k + 1)
	}
	pending, rx, toWriter, err := newBufferedHandoffs(b, 1000)
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
			for i := 0; i < 1+r.Intn(burst) && k < frames; i++ {
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
	var got, played []int16
	deliver := func(x []int16) {
		got = append(got, b.echoed...)
		played = append(played, fb...)
	}
	tick := time.NewTicker(time.Duration(frameMs) * time.Millisecond / 4)
	defer tick.Stop()
	deadline := time.Now().Add(time.Minute)
	for len(got) < len(mic) && time.Now().Before(deadline) {
		<-tick.C
		if err := b.drainVariant(pending, rx, toWriter, fb, mb, out, deliver); err != nil {
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
		t.Fatalf("writer sent %d samples, Playback got %d (the writer fell behind)", len(written), len(played))
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
	testutil.Compare(t, "replay", replay(t, rate, frameMs, 200, cancel, written, mic), got)
}

// TestNoAllocsBuffered checks that a processing tick of recipe B, all three
// hand-offs included, allocates nothing (U9).
func TestNoAllocsBuffered(t *testing.T) {
	for _, rate := range []int{8000, 16000} {
		t.Run(fmt.Sprintf("%dk", rate/1000), func(t *testing.T) {
			skipUnderRace(t)
			for _, v := range variants {
				noAllocsBuffered(t, rate, v.cancel)
			}
		})
	}
}

func noAllocsBuffered(t *testing.T, rate int, cancel bool) {
	b := newVariant(t, rate, 20, 200, cancel)
	pending, rx, toWriter, err := newBufferedHandoffs(b, 200)
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
		if err := b.drainVariant(pending, rx, toWriter, fb, mb, out, deliver); err != nil {
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

// TestBufferedLagAndDelay reports the echo stage against the far end's lag,
// the echo delay and the jitter (reported only).
func TestBufferedLagAndDelay(t *testing.T) {
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
			for _, v := range variants {
				for _, j := range []int{0, 30} {
					r := scenarioCall(rate, frameMs, j, fixedPath(rate, delayMs)).run(t, newVariant(t, rate, frameMs, 200, v.cancel))
					t.Logf("%d Hz, echo delay %d ms: drain (%s), jitter 0 to %d ms, echo stage %.1f dB, %d frames with echo of unsent frames",
						rate, delayMs, v.name, j, r.stage(rate, 8, 14), r.missing)
				}
			}
		}
	}
}

// TestBufferedLongDelay checks recipe B at echo delays of 100 and 200 ms
// with a 256 ms filter (U11), and reports the recovery from a delay step.
func TestBufferedLongDelay(t *testing.T) {
	skipUnderRace(t)
	const frameMs, filterMs = 20, 256
	for _, rate := range []int{8000, 16000} {
		for _, v := range variants {
			run := func(path func(int) []tap) simResult {
				return scenarioCall(rate, frameMs, 30, path).run(t, newVariant(t, rate, frameMs, filterMs, v.cancel))
			}
			for _, delayMs := range []int{100, 200} {
				t.Run(fmt.Sprintf("%dk_%dms_%s", rate/1000, delayMs, v.name), func(t *testing.T) {
					r := run(fixedPath(rate, delayMs))
					stage := r.stage(rate, 8, 14)
					t.Logf("echo delay %d ms, jitter 0 to 30 ms, filter %d ms: echo stage %.1f dB over 8 to 14 s; %d frames with echo of unsent frames",
						delayMs, filterMs, stage, r.missing)
					if stage < 15 {
						t.Errorf("U11: echo stage %.1f dB, want at least 15", stage)
					}
				})
			}
			t.Run(fmt.Sprintf("%dk_step_100_140ms_%s", rate/1000, v.name), func(t *testing.T) {
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
}

// TestBufferedLagIsOneFrame checks that drain without jitter equals Cancel
// with the sent frames one frame late, the first one lost (mdf.c:656).
func TestBufferedLagIsOneFrame(t *testing.T) {
	skipUnderRace(t)
	const frameMs = 20
	for _, rate := range []int{8000, 16000} {
		t.Run(fmt.Sprintf("%dk", rate/1000), func(t *testing.T) {
			b, err := newBufferedCall(rate, frameMs, 200)
			if err != nil {
				t.Fatal(err)
			}
			r := scenarioCall(rate, frameMs, 0, fixedPath(rate, 100)).run(t, b)
			frame := b.frame
			n := len(r.mic) / frame

			// Received frame k cancels against sent frame k-1; frames 0 and 1
			// against silence, because Playback before the first Capture is lost.
			ref := make([]int16, n*frame)
			copy(ref[2*frame:], r.sent[frame:(n-1)*frame])
			ec, err := echo.New(echo.Config{FrameSize: frame, FilterLength: rate / 5, SampleRate: rate})
			if err != nil {
				t.Fatal(err)
			}
			want := make([]int16, n*frame)
			for i := 0; i < n*frame; i += frame {
				if err := ec.Cancel(r.mic[i:i+frame], ref[i:i+frame], want[i:i+frame]); err != nil {
					t.Fatal(err)
				}
			}
			testutil.Compare(t, "echoed", want, r.echoed)
		})
	}
}
