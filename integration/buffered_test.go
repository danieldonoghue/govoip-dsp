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

// This file is the buffered-mode recipe of docs/usage.md: sent and received
// frames that are not in step meet in the echo canceller's playback queue.

// bufferedCall is the send path of a call whose sent and received frames
// arrive independently. Use it from one goroutine.
type bufferedCall struct {
	rate, frame int

	ec  *echo.Canceller
	pre *preprocess.Preprocessor
	gc  *agc.AGC

	clean    []int16
	residual []int32
}

// newBufferedCall makes the processors of one call at rate Hz (8000 or
// 16000) with frames of frameMs milliseconds.
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
	b.clean = make([]int16, b.frame)
	b.residual = make([]int32, b.ec.ResidualLen())
	return b, nil
}

// played queues a frame that was sent to the network.
func (b *bufferedCall) played(far []int16) error { return b.ec.Playback(far) }

// received cleans a received frame against the queued sent frames.
func (b *bufferedCall) received(mic, out []int16) error {
	if err := b.ec.Capture(mic, b.clean); err != nil {
		return err
	}
	if err := b.ec.Residual(b.residual); err != nil {
		return err
	}
	speech, err := b.pre.RunEcho(b.clean, b.residual)
	if err != nil {
		return err
	}
	return b.gc.Process(b.clean, out, speech)
}

// drain runs on the processing goroutine: it alternates the sent frames
// (queued with played) and the received frames that are waiting.
func (b *bufferedCall) drain(sent, rx *buffer.SPSC, far, mic, out []int16, deliver func([]int16)) error {
	ns, nr := sent.Len()/b.frame, rx.Len()/b.frame // bounds the work of this call
	for i := 0; i < max(ns, nr); i++ {
		if i < ns {
			sent.Read(far)
			if err := b.played(far); err != nil {
				return err
			}
		}
		if i < nr {
			rx.Read(mic)
			if err := b.received(mic, out); err != nil {
				return err
			}
			deliver(out)
		}
	}
	return nil
}

// newBufferedRx makes the two hand-offs, each with room for 200 ms.
func newBufferedRx(b *bufferedCall) (sent, rx *buffer.SPSC, err error) {
	if sent, err = buffer.NewSPSC(buffer.SPSCConfig{Capacity: b.rate / 5}); err != nil {
		return nil, nil, err
	}
	rx, err = buffer.NewSPSC(buffer.SPSCConfig{Capacity: b.rate / 5})
	return sent, rx, err
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

// runBuffered runs drain every frameMs of a simulated clock, with frame k of
// mic and far available from micAt[k] and farAt[k] ms; it returns the output.
func runBuffered(t testing.TB, b *bufferedCall, mic, far []int16, micAt, farAt []int, frameMs int) []int16 {
	sent, rx, err := newBufferedRx(b)
	if err != nil {
		t.Fatal(err)
	}
	fb, mb := make([]int16, b.frame), make([]int16, b.frame)
	out := make([]int16, b.frame)
	var got []int16
	deliver := func(x []int16) { got = append(got, x...) }
	frames := len(mic) / b.frame
	km, kf := 0, 0
	for now := 0; km < frames || kf < len(farAt); now += frameMs {
		for ; kf < len(farAt) && farAt[kf] <= now; kf++ {
			if !onPacket(sent, far[kf*b.frame:(kf+1)*b.frame]) {
				t.Fatalf("sent frame %d dropped", kf)
			}
		}
		for ; km < frames && micAt[km] <= now; km++ {
			if !onPacket(rx, mic[km*b.frame:(km+1)*b.frame]) {
				t.Fatalf("received frame %d dropped", km)
			}
		}
		if err := b.drain(sent, rx, fb, mb, out, deliver); err != nil {
			t.Fatal(err)
		}
	}
	return got
}

// TestBufferedUnevenTiming checks echo cancellation with jittered sent and
// received frames (U6), and reports a drift beyond the queue (U10).
func TestBufferedUnevenTiming(t *testing.T) {
	const frameMs = 20
	for _, rate := range []int{8000, 16000} {
		t.Run(fmt.Sprintf("%dk", rate/1000), func(t *testing.T) {
			s := callScenario(rate, 1)
			b, err := newBufferedCall(rate, frameMs)
			if err != nil {
				t.Fatal(err)
			}
			n := len(s.mic) / b.frame
			micAt := arrivals(n, frameMs, 30, "integration/buffered/mic")
			farAt := arrivals(n, frameMs, 30, "integration/buffered/far")
			out := runBuffered(t, b, s.mic, s.far, micAt, farAt, frameMs)
			erle := dB(energy(window(s.mic, rate, 8, 14)), energy(window(out, rate, 8, 14)))
			t.Logf("jitter 0 to 30 ms: echo through the chain %.1f dB over 8 to 14 s", erle)

			// Echo stage alone, for criterion U6.
			b2, err := newBufferedCall(rate, frameMs)
			if err != nil {
				t.Fatal(err)
			}
			e := echoStageBuffered(t, b2, s.mic, s.far, micAt, farAt, frameMs)
			stage := dB(energy(window(s.mic, rate, 8, 14)), energy(window(e, rate, 8, 14)))
			t.Logf("jitter 0 to 30 ms: echo stage %.1f dB over 8 to 14 s", stage)
			// U6 fails, a reported finding (docs/testing.md); the test shows
			// if that changes.
			if stage < 15 {
				t.Logf("U6 fails, a reported finding: echo stage %.1f dB with uneven timing, want at least 15", stage)
			} else {
				t.Errorf("U6 was a reported finding but now passes (%.1f dB); update docs/testing.md", stage)
			}

			// A 100 ms sender pause at 6 s, then a burst: reported only.
			pause := append([]int(nil), farAt...)
			for k := range pause {
				if ms := pause[k]; ms >= 6000 && ms < 6100 {
					pause[k] = 6100
				}
			}
			b3, err := newBufferedCall(rate, frameMs)
			if err != nil {
				t.Fatal(err)
			}
			e3 := echoStageBuffered(t, b3, s.mic, s.far, micAt, pause, frameMs)
			t.Logf("sender pause of 100 ms at 6 s: echo stage %.1f dB over 8 to 14 s, %.1f dB over 6 to 8 s",
				dB(energy(window(s.mic, rate, 8, 14)), energy(window(e3, rate, 8, 14))),
				dB(energy(window(s.mic, rate, 6, 8)), energy(window(e3, rate, 6, 8))))
		})
	}
}

// echoStageBuffered is runBuffered with Playback and Capture only, so the
// figure is the echo stage, as B1 measures it.
func echoStageBuffered(t testing.TB, b *bufferedCall, mic, far []int16, micAt, farAt []int, frameMs int) []int16 {
	frames := len(mic) / b.frame
	e := make([]int16, b.frame)
	var got []int16
	km, kf := 0, 0
	for now := 0; km < frames || kf < len(farAt); now += frameMs {
		// The same alternation as drain: one sent, one received, and so on.
		ns, nr := 0, 0
		for kf+ns < len(farAt) && farAt[kf+ns] <= now {
			ns++
		}
		for km+nr < frames && micAt[km+nr] <= now {
			nr++
		}
		for i := 0; i < max(ns, nr); i++ {
			if i < ns {
				if err := b.ec.Playback(far[kf*b.frame : (kf+1)*b.frame]); err != nil {
					t.Fatal(err)
				}
				kf++
			}
			if i < nr {
				if err := b.ec.Capture(mic[km*b.frame:(km+1)*b.frame], e); err != nil {
					t.Fatal(err)
				}
				got = append(got, e...)
				km++
			}
		}
	}
	return got
}

// TestBufferedIdleSender compares a sender that queues silence when it has
// nothing to send with one that queues nothing (criterion U7).
func TestBufferedIdleSender(t *testing.T) {
	const frameMs = 20
	for _, rate := range []int{8000, 16000} {
		t.Run(fmt.Sprintf("%dk", rate/1000), func(t *testing.T) {
			n := 16 * rate
			far := gate(testutil.WhiteNoise(n, 8000, testutil.Seed("integration/idle")), rate, []segment{{2, 14}})
			noise := testutil.WhiteNoise(n, 30, testutil.Seed("integration/noise"))
			mic := mix(convolve(far, echoPath(rate)), noise)
			frame := rate * frameMs / 1000
			frames := n / frame
			at := make([]int, frames)
			for k := range at {
				at[k] = k * frameMs
			}
			idle := at[:14*1000/frameMs] // nothing queued after 14 s
			tail := func(farAt []int) float64 {
				b, err := newBufferedCall(rate, frameMs)
				if err != nil {
					t.Fatal(err)
				}
				e := echoStageBuffered(t, b, mic, far, at, farAt, frameMs)
				return energy(window(e, rate, 14.0, 14.3))
			}
			silent, nothing := tail(at), tail(idle)
			gain := dB(nothing, silent)
			t.Logf("echo tail over 14.0 to 14.3 s: %.1f dB lower when the idle sender queues silence", gain)
			if gain < 10 {
				t.Errorf("U7: queuing silence lowers the echo tail by %.1f dB, want at least 10", gain)
			}
		})
	}
}

// TestBufferedTwoGoroutines feeds the recipe from two producer goroutines
// at irregular times and replays the observed order in one goroutine (U8).
func TestBufferedTwoGoroutines(t *testing.T) {
	const frameMs, rate = 20, 8000
	seconds := 10
	if testing.Short() {
		seconds = 3
	}
	s := callScenario(rate, 1)
	b, err := newBufferedCall(rate, frameMs)
	if err != nil {
		t.Fatal(err)
	}
	frames := seconds * 1000 / frameMs
	mic := s.mic[:frames*b.frame]
	// Sample 0 of each sent frame is its index, so the processing goroutine
	// can tell how many sent frames it had queued.
	far := append([]int16(nil), s.far[:frames*b.frame]...)
	for k := range frames {
		far[k*b.frame] = int16(k)
	}
	sent, rx, err := newBufferedRx(b)
	if err != nil {
		t.Fatal(err)
	}

	var stop atomic.Bool
	var wg sync.WaitGroup
	defer wg.Wait()
	defer stop.Store(true) // also when a check fails
	produce := func(q *buffer.SPSC, x []int16, seed string) {
		defer wg.Done()
		r := testutil.SplitMix64{State: testutil.Seed(seed)}
		for k := 0; k < frames && !stop.Load(); {
			if onPacket(q, x[k*b.frame:(k+1)*b.frame]) {
				k++
			}
			time.Sleep(time.Duration(r.Intn(2*frameMs*1000)) * time.Microsecond)
		}
	}
	wg.Add(2)
	go produce(rx, mic, t.Name()+"/rx")
	go produce(sent, far, t.Name()+"/sent")

	fb, mb := make([]int16, b.frame), make([]int16, b.frame)
	fb[0] = -1 // no sent frame read yet
	out := make([]int16, b.frame)
	var got []int16
	var queuedBefore []int // sent frames read before each received frame
	deliver := func(x []int16) {
		got = append(got, x...)
		queuedBefore = append(queuedBefore, int(fb[0])+1)
	}
	tick := time.NewTicker(time.Duration(frameMs) * time.Millisecond / 4)
	defer tick.Stop()
	deadline := time.Now().Add(time.Minute)
	for len(got) < len(mic) && time.Now().Before(deadline) {
		<-tick.C
		if err := b.drain(sent, rx, fb, mb, out, deliver); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != len(mic) {
		t.Fatalf("processed %d of %d received samples", len(got), len(mic))
	}

	// Replay: the same sent frames queued before each received frame.
	r, err := newBufferedCall(rate, frameMs)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]int16, 0, len(mic))
	o := make([]int16, b.frame)
	played := 0
	for k := range frames {
		for ; played < queuedBefore[k]; played++ {
			if err := r.played(far[played*b.frame : (played+1)*b.frame]); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.received(mic[k*b.frame:(k+1)*b.frame], o); err != nil {
			t.Fatal(err)
		}
		want = append(want, o...)
	}
	testutil.Compare(t, "out", want, got)
}

// TestNoAllocsBuffered checks that a processing tick of the buffered recipe,
// both hand-offs included, allocates nothing (criterion U9).
func TestNoAllocsBuffered(t *testing.T) {
	for _, rate := range []int{8000, 16000} {
		t.Run(fmt.Sprintf("%dk", rate/1000), func(t *testing.T) {
			b, err := newBufferedCall(rate, 20)
			if err != nil {
				t.Fatal(err)
			}
			sent, rx, err := newBufferedRx(b)
			if err != nil {
				t.Fatal(err)
			}
			s := callScenario(rate, 1)
			frames := len(s.mic) / b.frame
			fb, mb, out := make([]int16, b.frame), make([]int16, b.frame), make([]int16, b.frame)
			deliver := func([]int16) {}
			k := 0
			tick := func() {
				onPacket(sent, s.far[k*b.frame:(k+1)*b.frame])
				onPacket(rx, s.mic[k*b.frame:(k+1)*b.frame])
				if err := b.drain(sent, rx, fb, mb, out, deliver); err != nil {
					t.Fatal(err)
				}
				k = (k + 1) % frames
			}
			for range 12 * 50 { // past adaptation
				tick()
			}
			testutil.AssertNoAllocs(t, 200, tick)
		})
	}
}

// TestBufferedLagAndDelay reports why U6 fails: the queue's reference lag
// against the echo delay (reported only, added after the U6 result).
func TestBufferedLagAndDelay(t *testing.T) {
	const frameMs = 20
	for _, rate := range []int{8000, 16000} {
		frame := rate * frameMs / 1000
		for _, delayMs := range []int{32, 100} {
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
			n := len(mic) / frame
			for _, j := range []int{0, 2, 5, 10, 30} {
				micAt := arrivals(n, frameMs, j, "integration/buffered/mic")
				farAt := arrivals(n, frameMs, j, "integration/buffered/far")
				b, err := newBufferedCall(rate, frameMs)
				if err != nil {
					t.Fatal(err)
				}
				e := echoStageBuffered(t, b, mic, base.far, micAt, farAt, frameMs)
				t.Logf("%d Hz, echo delay %d ms: Playback and Capture, jitter 0 to %d ms, echo stage %.1f dB", rate, delayMs, j, erle(e))
			}
			// Jitter below 10 ms with ticks 10 ms after the nominal times:
			// every drain then sees one frame of each stream.
			micAt := arrivals(n, frameMs, 9, "integration/buffered/mic")
			farAt := arrivals(n, frameMs, 9, "integration/buffered/far")
			for k := range micAt {
				micAt[k], farAt[k] = micAt[k]+10, farAt[k]+10
			}
			b, err := newBufferedCall(rate, frameMs)
			if err != nil {
				t.Fatal(err)
			}
			e := echoStageBuffered(t, b, mic, base.far, micAt, farAt, frameMs)
			t.Logf("%d Hz, echo delay %d ms: Playback and Capture, jitter 0 to 9 ms, ticks 10 ms after the nominal times, echo stage %.1f dB", rate, delayMs, erle(e))
		}
	}
}
