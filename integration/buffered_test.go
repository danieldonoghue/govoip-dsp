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

	echoed   []int16 // the echo canceller's output, which the tests measure
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

// drain runs on the processing goroutine. For each received frame it queues
// one sent frame, or silence when none has arrived, then cleans the frame.
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
// mic and far available from micAt[k] and farAt[k] ms.
func runBuffered(t testing.TB, b *bufferedCall, mic, far []int16, micAt, farAt []int, frameMs int) (out, echoed []int16) {
	sent, rx, err := newBufferedRx(b)
	if err != nil {
		t.Fatal(err)
	}
	fb, mb := make([]int16, b.frame), make([]int16, b.frame)
	o := make([]int16, b.frame)
	deliver := func(x []int16) {
		out = append(out, x...)
		echoed = append(echoed, b.echoed...)
	}
	frames := len(mic) / b.frame
	km, kf := 0, 0
	for now := 0; km < frames; now += frameMs {
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
		if err := b.drain(sent, rx, fb, mb, o, deliver); err != nil {
			t.Fatal(err)
		}
	}
	return out, echoed
}

// TestBufferedUnevenTiming checks echo cancellation with jittered sent and
// received frames (U6), and reports a sender pause and burst (U10).
func TestBufferedUnevenTiming(t *testing.T) {
	const frameMs = 20
	for _, rate := range []int{8000, 16000} {
		t.Run(fmt.Sprintf("%dk", rate/1000), func(t *testing.T) {
			s := callScenario(rate, 1)
			frame := rate * frameMs / 1000
			n := len(s.mic) / frame
			micAt := arrivals(n, frameMs, 30, "integration/buffered/mic")
			farAt := arrivals(n, frameMs, 30, "integration/buffered/far")
			ratio := func(y []int16, from, to float64) float64 {
				return dB(energy(window(s.mic, rate, from, to)), energy(window(y, rate, from, to)))
			}
			b, err := newBufferedCall(rate, frameMs)
			if err != nil {
				t.Fatal(err)
			}
			out, e := runBuffered(t, b, s.mic, s.far, micAt, farAt, frameMs)
			stage := ratio(e, 8, 14)
			t.Logf("jitter 0 to 30 ms: echo stage %.1f dB, chain %.1f dB over 8 to 14 s", stage, ratio(out, 8, 14))
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
			_, e3 := runBuffered(t, b3, s.mic, s.far, micAt, pause, frameMs)
			t.Logf("sender pause of 100 ms at 6 s: echo stage %.1f dB over 8 to 14 s, %.1f dB over 6 to 8 s",
				ratio(e3, 8, 14), ratio(e3, 6, 8))
		})
	}
}

// TestBufferedIdleSender compares drain, which queues silence once the
// sender stops, with captures that find nothing queued (criterion U7).
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
			stop := 14 * 1000 / frameMs // the sender sends nothing from 14 s on
			b, err := newBufferedCall(rate, frameMs)
			if err != nil {
				t.Fatal(err)
			}
			_, withSilence := runBuffered(t, b, mic, far, at, at[:stop], frameMs)

			// The counterfactual, which drain never does: no Playback after 14 s.
			ec, err := echo.New(echo.Config{FrameSize: frame, FilterLength: rate / 5, SampleRate: rate})
			if err != nil {
				t.Fatal(err)
			}
			nothing := make([]int16, frames*frame)
			for k := range frames {
				if k < stop {
					if err := ec.Playback(far[k*frame : (k+1)*frame]); err != nil {
						t.Fatal(err)
					}
				}
				if err := ec.Capture(mic[k*frame:(k+1)*frame], nothing[k*frame:(k+1)*frame]); err != nil {
					t.Fatal(err)
				}
			}
			gain := dB(energy(window(nothing, rate, 14.0, 14.3)), energy(window(withSilence, rate, 14.0, 14.3)))
			t.Logf("echo tail over 14.0 to 14.3 s: %.1f dB lower when silence is queued", gain)
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
	// Sample 0 of each sent frame is its index plus 1, so the processing
	// goroutine can tell which one it queued; silence reads as 0.
	far := append([]int16(nil), s.far[:frames*b.frame]...)
	for k := range frames {
		far[k*b.frame] = int16(k + 1)
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
	out := make([]int16, b.frame)
	var got []int16
	var queued []int // per received frame: the sent frame's index plus 1, or 0
	deliver := func(x []int16) {
		got = append(got, x...)
		queued = append(queued, int(fb[0]))
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

	// Replay: one queued frame per received frame, the same one, in order.
	r, err := newBufferedCall(rate, frameMs)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]int16, 0, len(mic))
	o := make([]int16, b.frame)
	silence := make([]int16, b.frame)
	next, silent := 1, 0
	for k := range frames {
		f := silence
		if q := queued[k]; q != 0 {
			if q != next {
				t.Fatalf("received frame %d got sent frame %d, want %d (sent frames in order)", k, q-1, next-1)
			}
			f, next = far[(q-1)*b.frame:q*b.frame], next+1
		} else {
			silent++
		}
		if err := r.played(f); err != nil {
			t.Fatal(err)
		}
		if err := r.received(mic[k*b.frame:(k+1)*b.frame], o); err != nil {
			t.Fatal(err)
		}
		want = append(want, o...)
	}
	t.Logf("%d received frames, %d of them with silence queued", frames, silent)
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

// TestBufferedLagAndDelay reports the echo stage against the far end's lag,
// the echo delay and the jitter (reported only, added after a U6 result).
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
				b, err := newBufferedCall(rate, frameMs)
				if err != nil {
					t.Fatal(err)
				}
				_, e := runBuffered(t, b, mic, base.far, arrivals(n, frameMs, j, "integration/buffered/mic"),
					arrivals(n, frameMs, j, "integration/buffered/far"), frameMs)
				t.Logf("%d Hz, echo delay %d ms: drain, jitter 0 to %d ms, echo stage %.1f dB", rate, delayMs, j, erle(e))
			}
		}
	}
}
