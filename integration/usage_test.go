package integration

import (
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danieldonoghue/govoip-dsp/buffer"
	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

// newRx makes the hand-off from the network goroutine to the processing
// goroutine: room for 200 ms of network audio.
func newRx(c *call) (*buffer.SPSC, error) {
	// netRate/5 samples is 200 ms of network audio: room for a stall of
	// about ten 20 ms frames before onPacket has to drop.
	return buffer.NewSPSC(buffer.SPSCConfig{Capacity: c.netRate / 5})
}

// onPacket runs on the network goroutine for each received frame. It
// stores the whole frame or none of it, so the buffer holds whole frames.
func onPacket(rx *buffer.SPSC, p []int16) bool {
	// SPSC.Write stores as much as fits. A partial frame would shift every
	// later frame boundary, so store the whole frame or nothing.
	if rx.Free() < len(p) {
		return false // the processor is behind: drop this packet
	}
	rx.Write(p)
	return true
}

// drain runs on the processing goroutine, for example per 20 ms tick: far
// gives the frame sent with each received one, deliver takes the output.
func (c *call) drain(rx *buffer.SPSC, maxBacklog int, mic, out []int16,
	far func() []int16, deliver func([]int16)) error {
	// After a stall, old audio is useless for a live call: drop to the
	// newest frame instead of processing a growing backlog.
	if rx.Len() > maxBacklog {
		// After a stall keep the newest frame, and skip the far frames of
		// the dropped ones so that each frame keeps its far frame.
		for range rx.Consume(rx.Len()-c.netFrame) / c.netFrame {
			far()
		}
	}
	// Each received frame is processed with the frame sent at the same
	// time, which far returns in order.
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

// Example_call sizes the processors of one call at 20 ms frames.
func Example_call() {
	for _, netRate := range []int{8000, 16000, 48000} {
		c, err := newCall(netRate, 20)
		if err != nil {
			log.Fatal(err)
		}
		rx, err := newRx(c)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("network %d Hz: %d-sample frames, processing %d Hz: %d-sample frames, echo filter %d samples\n",
			netRate, c.netFrame, c.rate, c.frame, c.ec.FilterLength())
		queue := 0
		if c.micQ != nil {
			queue = c.micQ.Cap()
		}
		fmt.Printf("  residual %d, resampler output %d, queues %d, SPSC %d, output %d\n",
			len(c.residual), len(c.rsOut), queue, rx.Cap(), c.outLen())
	}
	// Output:
	// network 8000 Hz: 160-sample frames, processing 8000 Hz: 160-sample frames, echo filter 1600 samples
	//   residual 161, resampler output 0, queues 0, SPSC 1600, output 320
	// network 16000 Hz: 320-sample frames, processing 16000 Hz: 320-sample frames, echo filter 3200 samples
	//   residual 321, resampler output 0, queues 0, SPSC 3200, output 640
	// network 48000 Hz: 960-sample frames, processing 16000 Hz: 320-sample frames, echo filter 3200 samples
	//   residual 321, resampler output 321, queues 641, SPSC 9600, output 640
}

// runDirect runs the call scenario through c frame by frame in one
// goroutine and returns the output.
func runDirect(t testing.TB, c *call, s callSignals) []int16 {
	out := make([]int16, c.outLen())
	var got []int16
	for k := 0; (k+1)*c.netFrame <= len(s.mic); k++ {
		lo, hi := k*c.netFrame, (k+1)*c.netFrame
		n, err := c.process(s.mic[lo:hi], s.far[lo:hi], out)
		if err != nil {
			t.Fatal(err)
		}
		if n != c.frame {
			t.Fatalf("frame %d: process wrote %d samples, want one frame of %d", k, n, c.frame)
		}
		got = append(got, out[:n]...)
	}
	return got
}

// TestRecipeThroughSPSC feeds the recipe from a network goroutine at
// irregular times and compares with a direct frame-by-frame run (U3).
func TestRecipeThroughSPSC(t *testing.T) {
	seconds := 10.0
	if testing.Short() {
		seconds = 3
	}
	for _, netRate := range []int{8000, 16000, 48000} {
		t.Run(fmt.Sprintf("%dk", netRate/1000), func(t *testing.T) {
			s := callScenario(netRate, 1)
			n := int(seconds * float64(netRate))
			s.mic, s.far = s.mic[:n], s.far[:n]

			ref, err := newCall(netRate, 20)
			if err != nil {
				t.Fatal(err)
			}
			want := runDirect(t, ref, s)

			c, err := newCall(netRate, 20)
			if err != nil {
				t.Fatal(err)
			}
			rx, err := newRx(c)
			if err != nil {
				t.Fatal(err)
			}
			frames := len(s.mic) / c.netFrame
			var stop atomic.Bool
			var wg sync.WaitGroup
			defer wg.Wait()
			defer stop.Store(true) // also when a check fails
			wg.Add(1)
			go func() { // the network goroutine
				defer wg.Done()
				r := testutil.SplitMix64{State: testutil.Seed(t.Name())}
				for k := 0; k < frames && !stop.Load(); {
					// In this test a full buffer waits instead of dropping, so
					// the output can be compared.
					if onPacket(rx, s.mic[k*c.netFrame:(k+1)*c.netFrame]) {
						k++
					}
					if r.Intn(4) == 0 {
						time.Sleep(time.Duration(r.Intn(500)) * time.Microsecond)
					}
				}
			}()

			mic := make([]int16, c.netFrame)
			out := make([]int16, c.outLen())
			silent := make([]int16, c.netFrame)
			sent := 0
			far := func() []int16 {
				if sent >= frames {
					return silent
				}
				f := s.far[sent*c.netFrame : (sent+1)*c.netFrame]
				sent++
				return f
			}
			var got []int16
			deliver := func(x []int16) { got = append(got, x...) }
			tick := time.NewTicker(time.Millisecond)
			defer tick.Stop()
			deadline := time.Now().Add(time.Minute)
			for len(got) < len(want) && time.Now().Before(deadline) {
				<-tick.C
				if err := c.drain(rx, math.MaxInt, mic, out, far, deliver); err != nil {
					t.Fatal(err)
				}
			}
			if len(got) != len(want) {
				t.Fatalf("got %d samples through the SPSC, %d directly", len(got), len(want))
			}
			testutil.Compare(t, "out", want, got)
		})
	}
}

// TestDrainDropsBacklog checks that after a stall drain keeps only the
// newest frame and processes it.
func TestDrainDropsBacklog(t *testing.T) {
	c, err := newCall(8000, 20)
	if err != nil {
		t.Fatal(err)
	}
	rx, err := newRx(c)
	if err != nil {
		t.Fatal(err)
	}
	for k := range 8 {
		p := make([]int16, c.netFrame)
		p[0] = int16(k + 1)
		if !onPacket(rx, p) {
			t.Fatalf("packet %d rejected with %d free", k, rx.Free())
		}
	}
	if onPacket(rx, make([]int16, c.netFrame*3)) {
		t.Fatal("a packet larger than the free space was stored")
	}
	mic := make([]int16, c.netFrame)
	out := make([]int16, c.outLen())
	processed, farCalls := 0, 0
	far := func() []int16 { farCalls++; return make([]int16, c.netFrame) }
	err = c.drain(rx, 3*c.netFrame, mic, out, far, func([]int16) { processed++ })
	if err != nil {
		t.Fatal(err)
	}
	if processed != 1 || mic[0] != 8 || rx.Len() != 0 || farCalls != 8 {
		t.Fatalf("processed %d frames, last frame marker %d, %d samples left, %d far frames taken; want 1, 8, 0, 8",
			processed, mic[0], rx.Len(), farCalls)
	}
}

// TestNoAllocsRecipe checks that the per-frame step of the recipe,
// including the SPSC hand-off, allocates nothing (criterion U4).
func TestNoAllocsRecipe(t *testing.T) {
	for _, netRate := range []int{8000, 16000, 48000} {
		t.Run(fmt.Sprintf("%dk", netRate/1000), func(t *testing.T) {
			skipUnderRace(t)
			c, err := newCall(netRate, 20)
			if err != nil {
				t.Fatal(err)
			}
			rx, err := newRx(c)
			if err != nil {
				t.Fatal(err)
			}
			s := callScenario(netRate, 1)
			mic := make([]int16, c.netFrame)
			out := make([]int16, c.outLen())
			k, f := 0, 0
			frames := len(s.mic) / c.netFrame
			far := func() []int16 {
				f = (f + 1) % frames
				return s.far[f*c.netFrame : (f+1)*c.netFrame]
			}
			deliver := func([]int16) {}
			step := func() {
				// Every 8th step delivers a burst of 5 packets, so drain
				// also takes its backlog-drop branch.
				burst := 1
				if k%8 == 7 {
					burst = 5
				}
				for range burst {
					onPacket(rx, s.mic[k*c.netFrame:(k+1)*c.netFrame])
					k = (k + 1) % frames
				}
				if err := c.drain(rx, 3*c.netFrame, mic, out, far, deliver); err != nil {
					t.Fatal(err)
				}
			}
			for range 50 * 12 { // past adaptation: 12 s of far-end speech
				step()
			}
			testutil.AssertNoAllocs(t, 200, step)
		})
	}
}

// TestUsageCodeIsTested checks that every Go block of docs/usage.md
// appears verbatim in this package's tests (criterion U2).
func TestUsageCodeIsTested(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "docs", "usage.md"))
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	var src strings.Builder
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src.Write(b)
	}
	blocks := regexp.MustCompile("(?s)```go\n(.*?)```").FindAllStringSubmatch(string(doc), -1)
	if len(blocks) == 0 {
		t.Fatal("docs/usage.md has no Go blocks")
	}
	for i, b := range blocks {
		lines := slices.DeleteFunc(strings.Split(b[1], "\n"), func(s string) bool { return strings.TrimSpace(s) == "" })
		// Each run of non-blank lines must appear in the source.
		for _, l := range lines {
			if !strings.Contains(src.String(), l) {
				t.Errorf("docs/usage.md Go block %d: line %q is not in the integration tests", i+1, l)
			}
		}
		if !strings.Contains(src.String(), strings.TrimSpace(b[1])) {
			t.Errorf("docs/usage.md Go block %d is not verbatim in the integration tests", i+1)
		}
	}
}

// TestQueueFullIsReported checks that resampleInto reports a queue too
// short for the resampler's output instead of dropping samples.
func TestQueueFullIsReported(t *testing.T) {
	c, err := newCall(48000, 20)
	if err != nil {
		t.Fatal(err)
	}
	if c.micQ, err = buffer.New(buffer.Config{Capacity: c.frame - 1, Overflow: buffer.Reject}); err != nil {
		t.Fatal(err)
	}
	out := make([]int16, c.outLen())
	if _, err := c.process(make([]int16, c.netFrame), make([]int16, c.netFrame), out); err != errQueueFull {
		t.Fatalf("process with a short queue returned %v, want errQueueFull", err)
	}
}

// TestRecipeDropsUnderRace runs recipe A with bursts that make drain drop
// a backlog, and checks that every processed frame keeps its far frame.
func TestRecipeDropsUnderRace(t *testing.T) {
	c, err := newCall(8000, 20)
	if err != nil {
		t.Fatal(err)
	}
	rx, err := newRx(c)
	if err != nil {
		t.Fatal(err)
	}
	const frames = 400
	s := callScenario(8000, 1)
	mic := append([]int16(nil), s.mic[:frames*c.netFrame]...)
	farAll := append([]int16(nil), s.far[:frames*c.netFrame]...)
	for k := range frames { // sample 0 of each frame is its index
		mic[k*c.netFrame], farAll[k*c.netFrame] = int16(k), int16(k)
	}

	var stop atomic.Bool
	var wg sync.WaitGroup
	defer wg.Wait()
	defer stop.Store(true)
	wg.Add(1)
	go func() { // every 50 frames a 140 ms stall, then the frames in a burst
		defer wg.Done()
		for k := 0; k < frames && !stop.Load(); k++ {
			if !onPacket(rx, mic[k*c.netFrame:(k+1)*c.netFrame]) {
				t.Errorf("frame %d dropped by onPacket", k)
				return
			}
			if k%50 != 49 && k%50 > 6 {
				time.Sleep(20 * time.Millisecond)
			}
			if k%50 == 49 {
				time.Sleep(140 * time.Millisecond)
			}
		}
	}()

	buf := make([]int16, c.netFrame)
	out := make([]int16, c.outLen())
	next, lastFar := 0, -1
	far := func() []int16 {
		f := farAll[next*c.netFrame : (next+1)*c.netFrame]
		lastFar = int(f[0])
		next++
		return f
	}
	processed, mismatches := 0, 0
	deliver := func([]int16) {
		processed++
		if int(buf[0]) != lastFar {
			mismatches++
		}
	}
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	deadline := time.Now().Add(time.Minute)
	for next < frames && time.Now().Before(deadline) {
		<-tick.C
		if err := c.drain(rx, 3*c.netFrame, buf, out, far, deliver); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("%d of %d frames processed, %d dropped by drain, %d far mismatches", processed, frames, frames-processed, mismatches)
	if mismatches != 0 {
		t.Errorf("%d processed frames had another frame's far frame", mismatches)
	}
	if processed == frames {
		t.Error("drain dropped nothing; the test did not reach the backlog branch")
	}
}
