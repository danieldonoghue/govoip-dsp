package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danieldonoghue/govoip-dsp/agc"
	"github.com/danieldonoghue/govoip-dsp/buffer"
	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
	"github.com/danieldonoghue/govoip-dsp/preprocess"
)

// skipUnderRace skips single-goroutine work under the race detector.
func skipUnderRace(t testing.TB) {
	if raceEnabled {
		t.Skip("single-goroutine work, skipped under -race (docs/contracts.md section 9); the normal go test runs it")
	}
}

// callSignals is the first 6 s of the integration package's call scenario:
// far starts at 2 s, and mic is its echo through a room plus noise.
type callSignals struct{ far, mic []int16 }

func callScenario(rate int) callSignals {
	n := 6 * rate
	far := make([]int16, 0, n)
	for b := 0; len(far) < n; b++ { // 2 s blocks of speech-like sound, each with its own seed
		far = append(far, testutil.SpeechLike(2*rate, float64(rate), 16000, testutil.Seed(fmt.Sprintf("integration/far/%d", b)))...)
	}
	clear(far[:2*rate])
	echo := convolve(far, room(rate, "integration/path", 32, 80, 16384))
	noise := testutil.WhiteNoise(n, 30, testutil.Seed("integration/noise"))
	mic := make([]int16, n)
	for i := range mic {
		mic[i] = int16(max(-32768, min(32767, int64(echo[i])+int64(noise[i]))))
	}
	return callSignals{far: far, mic: mic}
}

// tap is one nonzero Q15 coefficient of a room response.
type tap struct {
	off int
	v   int32
}

// room returns an echo path: a bulk delay, a dense 2 ms early part and 48
// sparse reflections decaying linearly to tailMs; gain is the Q15 peak tap.
func room(rate int, seed string, delayMs, tailMs int, gain int32) []tap {
	r := testutil.SplitMix64{State: testutil.Seed(seed)}
	d := rate * delayMs / 1000
	early := max(1, rate*2/1000)
	tail := max(d+early+1, rate*tailMs/1000)
	var taps []tap
	for k := range early {
		v := gain * int32(early-k) / int32(early)
		v = v * int32(50+r.Intn(51)) / 100
		if r.Intn(2) == 1 {
			v = -v
		}
		taps = append(taps, tap{d + k, v})
	}
	for range 48 {
		off := d + early + r.Intn(tail-d-early)
		v := gain / 3 * int32(tail-off) / int32(tail-d)
		v = v * int32(r.Intn(101)) / 100
		if r.Intn(2) == 1 {
			v = -v
		}
		taps = append(taps, tap{off, v})
	}
	return taps
}

// convolve returns x filtered by h, rounded from Q15 and saturated.
func convolve(x []int16, h []tap) []int16 {
	y := make([]int16, len(x))
	for i := range y {
		var acc int64
		for _, t := range h {
			if j := i - t.off; j >= 0 {
				acc += int64(t.v) * int64(x[j])
			}
		}
		y[i] = int16(max(-32768, min(32767, (acc+16384)>>15)))
	}
	return y
}

// replayPaced passes sent frame k with received frame k to a new pacedCall
// with 20 ms frames and a 200 ms filter, and returns the chain's output.
func replayPaced(t testing.TB, rate int, sent, mic []int16) []int16 {
	b, err := newPacedCall(rate, 20, 200)
	if err != nil {
		t.Fatal(err)
	}
	f := b.frame
	out := make([]int16, 0, len(mic))
	o := make([]int16, f)
	for k := 0; (k+1)*f <= len(mic); k++ {
		if err := b.received(mic[k*f:(k+1)*f], sent[k*f:(k+1)*f], o); err != nil {
			t.Fatal(err)
		}
		out = append(out, o...)
	}
	return out
}

// asMessage returns one AudioSocket message.
func asMessage(kind byte, payload []byte) []byte {
	m := []byte{kind, 0, 0}
	binary.BigEndian.PutUint16(m[1:], uint16(len(payload)))
	return append(m, payload...)
}

// asAudio returns the audio message of samples x.
func asAudio(kind byte, x []int16) []byte {
	p := make([]byte, 2*len(x))
	for i, s := range x {
		binary.LittleEndian.PutUint16(p[2*i:], uint16(s))
	}
	return asMessage(kind, p)
}

// TestAudioSocketQuickStart serves one connection from a fake Asterisk
// over loopback TCP: a UUID, audio in messages of 10 to 40 ms with a DTMF
// digit among them, then a hangup or a closed connection. With audio sent
// back, an application goroutine writes it into pending meanwhile.
func TestAudioSocketQuickStart(t *testing.T) {
	for _, rate := range []int{8000, 16000} {
		for _, sends := range []bool{true, false} {
			for _, end := range []string{"hangup", "close"} {
				t.Run(fmt.Sprintf("%dk/sends=%v/%s", rate/1000, sends, end), func(t *testing.T) {
					t.Parallel() // each subtest waits on sleeps, not on the CPU
					audioSocketQuickStart(t, rate, sends, end)
				})
			}
		}
	}
}

func audioSocketQuickStart(t *testing.T, rate int, sends bool, end string) {
	// The far end is silent for its first 2 s (farOn), so the application
	// sends 4 s of it and the call lasts 6 s: the canceller adapts.
	const seconds = 6
	kind := byte(kindSlin)
	if rate == 16000 {
		kind = kindSlin16
	}
	f := rate * 20 / 1000
	frames := seconds * 50
	s := callScenario(rate)
	mic := s.mic[:frames*f]
	// Sample 0 of each application frame is its index plus 1; silence reads 0.
	appFrames := frames * 2 / 3
	app := append([]int16(nil), s.far[:appFrames*f]...)
	for k := range appFrames {
		app[k*f] = int16(k + 1)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var pending *buffer.SPSC
	if sends {
		if pending, err = buffer.NewSPSC(buffer.SPSCConfig{Capacity: rate}); err != nil { // 1 s
			t.Fatal(err)
		}
	}
	var stop atomic.Bool
	var wg sync.WaitGroup
	defer wg.Wait()
	defer stop.Store(true) // also when a check fails

	// The service: serveAudioSocket, as acceptAudioSockets runs it for each
	// connection, so that the test sees its error.
	var got []int16 // written by the service goroutine only, until wg.Wait
	var gotID uuid
	starts, ends := 0, 0
	serveErr := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := ln.Accept()
		if err != nil {
			serveErr <- err
			return
		}
		serveErr <- serveAudioSocket(conn, rate, func(id uuid) (session, error) {
			gotID = id
			starts++
			return session{
				pending: pending,
				deliver: func(x []int16) { got = append(got, x...) },
				end:     func() { ends++ },
			}, nil
		})
	}()
	// The application: writes the audio to send at uneven times.
	if sends {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := testutil.SplitMix64{State: testutil.Seed(t.Name() + "/app")}
			for k := 0; k < appFrames && !stop.Load(); {
				for n := 1 + r.Intn(4); n > 0 && k < appFrames; n-- {
					if !onPacket(pending, app[k*f:(k+1)*f]) {
						break
					}
					k++
				}
				time.Sleep(time.Duration(r.Intn(40000)) * time.Microsecond)
			}
		}()
	}

	// The fake Asterisk.
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var replies [][]byte // read by the reader goroutine only, until it ends
	readDone := make(chan error, 1)
	go func() {
		for {
			hdr := make([]byte, 3)
			if _, err := io.ReadFull(conn, hdr); err != nil {
				if err == io.EOF {
					err = nil // the service closed the connection
				}
				readDone <- err
				return
			}
			m := append(hdr, make([]byte, binary.BigEndian.Uint16(hdr[1:]))...)
			if _, err := io.ReadFull(conn, m[3:]); err != nil {
				readDone <- err
				return
			}
			replies = append(replies, m)
		}
	}()
	write := func(m []byte) {
		if _, err := conn.Write(m); err != nil {
			t.Fatal(err)
		}
	}
	id := uuid{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0, 1, 2, 3, 4, 5, 6, 7, 8}
	write(asMessage(kindUUID, id[:]))
	r := testutil.SplitMix64{State: testutil.Seed(t.Name() + "/asterisk")}
	for lo := 0; lo < len(mic); {
		hi := min(lo+(1+r.Intn(4))*f/2, len(mic)) // 10 to 40 ms
		write(asAudio(kind, mic[lo:hi]))
		if lo == len(mic)/2 {
			write(asMessage(kindDTMF, []byte{'5'}))
		}
		lo = hi
		time.Sleep(time.Duration(r.Intn(20000)) * time.Microsecond)
	}
	if end == "hangup" {
		write([]byte{kindHangup, 0, 0})
	} else {
		conn.(*net.TCPConn).CloseWrite()
	}
	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("serveAudioSocket: %v", err)
		}
	case <-time.After(time.Minute):
		t.Fatal("serveAudioSocket did not return after the end of the call")
	}
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}
	stop.Store(true)
	wg.Wait()

	if starts != 1 || ends != 1 || gotID != id {
		t.Fatalf("start called %d times with UUID %v, end %d times; want once with %v, once", starts, gotID, ends, id)
	}
	if len(got) != len(mic) {
		t.Fatalf("processed %d of %d received samples", len(got), len(mic))
	}
	if !sends {
		if len(replies) != 0 {
			t.Fatalf("%d messages sent back, want none", len(replies))
		}
		want := make([]int16, 0, len(mic))
		pre, err := preprocess.New(preprocess.Config{SampleRate: rate, FrameSize: f})
		if err != nil {
			t.Fatal(err)
		}
		pre.SetVAD(true)
		gc, err := agc.New(agc.DefaultConfig(rate))
		if err != nil {
			t.Fatal(err)
		}
		frame, out := make([]int16, f), make([]int16, f)
		for k := range frames {
			copy(frame, mic[k*f:(k+1)*f])
			speech, err := pre.Run(frame)
			if err != nil {
				t.Fatal(err)
			}
			if err := gc.Process(frame, out, speech); err != nil {
				t.Fatal(err)
			}
			want = append(want, out...)
		}
		testutil.Compare(t, "chain output", want, got)
		return
	}
	if len(replies) != frames {
		t.Fatalf("%d messages sent back for %d received frames, want one per frame", len(replies), frames)
	}
	sent := make([]int16, 0, len(mic))
	for k, m := range replies {
		if m[0] != kind || len(m) != 3+2*f {
			t.Fatalf("message %d sent back: kind %#x, %d bytes; want %#x, %d", k, m[0], len(m), kind, 3+2*f)
		}
		for i := range f {
			sent = append(sent, int16(binary.LittleEndian.Uint16(m[3+2*i:])))
		}
	}
	next, silent := 1, 0
	for k := range frames {
		switch m := int(sent[k*f]); {
		case m == 0:
			silent++
		case m != next:
			t.Fatalf("sent frame %d is application frame %d, want %d (in order, none twice)", k, m-1, next-1)
		default:
			testutil.Compare(t, fmt.Sprintf("sent frame %d", k), app[(m-1)*f:m*f], sent[k*f:(k+1)*f])
			next++
		}
	}
	t.Logf("%d received frames, %d with silence sent", frames, silent)
	if next-1 != appFrames {
		t.Errorf("sent %d of the %d application frames", next-1, appFrames)
	}
	if silent == 0 {
		t.Error("no silence was sent; the test did not reach the idle branch")
	}
	want := replayPaced(t, rate, sent, mic)
	testutil.Compare(t, "replay, chain output", want, got)
	// A replay that cancels against silence must differ, or the comparison
	// above could not catch a wrong pairing.
	silentWant := replayPaced(t, rate, make([]int16, len(sent)), mic)
	if slices.Equal(silentWant, got) {
		t.Error("the output equals a replay against silence; the test cannot see the pairing")
	}
}

// asLoop replays one message for every Read and discards every Write.
type asLoop struct {
	msg []byte
	off int
}

func (l *asLoop) Read(p []byte) (int, error) {
	n := copy(p, l.msg[l.off:])
	l.off = (l.off + n) % len(l.msg)
	return n, nil
}

func (l *asLoop) Write(p []byte) (int, error) { return len(p), nil }

// TestNoAllocsAudioSocket checks that handling one audio message, with and
// without audio sent back, allocates nothing.
func TestNoAllocsAudioSocket(t *testing.T) {
	for _, sends := range []bool{true, false} {
		t.Run(fmt.Sprintf("sends=%v", sends), func(t *testing.T) {
			skipUnderRace(t)
			const rate = 8000
			a, err := newAudioSocketCall(rate, sends)
			if err != nil {
				t.Fatal(err)
			}
			pending, err := buffer.NewSPSC(buffer.SPSCConfig{Capacity: rate})
			if err != nil {
				t.Fatal(err)
			}
			s := callScenario(rate)
			// 30 ms messages: steps alternate between one and two frames.
			conn := &asLoop{msg: asAudio(kindSlin, s.mic[:3*rate/100])}
			deliver := func([]int16) {}
			k := 0
			handle := func() {
				if k%4 != 3 { // every 4th message adds no frame, so some frames find none
					onPacket(pending, s.far[k*a.frame:(k+1)*a.frame])
				}
				k = (k + 1) % 100
				if done, err := a.next(conn, pending, deliver); done || err != nil {
					t.Fatalf("next: done %v, %v", done, err)
				}
			}
			for range 12 * 50 { // past adaptation
				handle()
			}
			testutil.AssertNoAllocs(t, 200, handle)
		})
	}
	// recorder.deliver storing frames, with its writing goroutine running.
	t.Run("recorder stores", func(t *testing.T) {
		skipUnderRace(t)
		r, err := newRecorder(nopCloser{io.Discard}, 400*160) // room for every frame
		if err != nil {
			t.Fatal(err)
		}
		x := callScenario(8000).mic[:160]
		for range 10 {
			r.deliver(x)
		}
		testutil.AssertNoAllocs(t, 200, func() { r.deliver(x) })
		if dropped, err := r.close(); dropped != 0 || err != nil {
			t.Fatalf("%d frames dropped, %v; want none dropped, so that the store is measured", dropped, err)
		}
	})
	// recorder.deliver dropping frames: no writing goroutine, so frames stays full.
	t.Run("recorder drops", func(t *testing.T) {
		skipUnderRace(t)
		frames, err := buffer.NewSPSC(buffer.SPSCConfig{Capacity: 4 * 160})
		if err != nil {
			t.Fatal(err)
		}
		r := &recorder{frames: frames}
		x := callScenario(8000).mic[:160]
		for range 4 {
			r.deliver(x)
		}
		if r.dropped != 0 || frames.Free() != 0 {
			t.Fatalf("%d frames dropped, %d samples free; want 0 and a full buffer", r.dropped, frames.Free())
		}
		testutil.AssertNoAllocs(t, 200, func() { r.deliver(x) })
		if r.dropped != 201 {
			t.Errorf("%d of 201 measured frames dropped, want all", r.dropped)
		}
	})
}

// nopCloser is a WriteCloser whose Close does nothing.
type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

// TestAudioSocketRecording serves one call through recording and checks
// that the file holds exactly the cleaned frames that deliver got.
func TestAudioSocketRecording(t *testing.T) {
	for _, rate := range []int{8000, 16000} {
		for _, sends := range []bool{true, false} {
			t.Run(fmt.Sprintf("%dk/sends=%v", rate/1000, sends), func(t *testing.T) {
				audioSocketRecording(t, rate, sends)
			})
		}
	}
}

func audioSocketRecording(t *testing.T, rate int, sends bool) {
	var logged syncBuffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	kind := byte(kindSlin)
	if rate == 16000 {
		kind = kindSlin16
	}
	f := rate * 20 / 1000
	const frames = 150 // 3 s, more than the recorder holds: a writer that never drains drops
	mic := callScenario(rate).mic[:frames*f]
	dir := t.TempDir()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var got []int16 // the cleaned frames; written by the service goroutine until serveErr
	start := recording(dir, rate, func(id uuid) (session, error) {
		var pending *buffer.SPSC
		if sends { // nothing is written into pending, so silence is sent back
			var err error
			if pending, err = buffer.NewSPSC(buffer.SPSCConfig{Capacity: f}); err != nil {
				return session{}, err
			}
		}
		return session{
			pending: pending,
			deliver: func(x []int16) { got = append(got, x...) },
			end:     func() {},
		}, nil
	})
	serveErr := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			serveErr <- err
			return
		}
		serveErr <- serveAudioSocket(conn, rate, start)
	}()

	id := uuid{0xa1, 0xb2, 0xc3, 0xd4, 0xe5, 0xf6, 0x07, 0x18, 0x29, 0x3a, 0x4b, 0x5c, 0x6d, 0x7e, 0x8f, 0x90}
	c := dialAudioSocket(t, ln.Addr().String(), id)
	for lo, k := 0, 0; lo < len(mic); k++ {
		hi := lo + f/2 // messages of 10 and 30 ms, in turn
		if k%2 == 1 {
			hi = lo + 3*f/2
		}
		c.write(asAudio(kind, mic[lo:hi]))
		lo = hi
		time.Sleep(10 * time.Millisecond)
	}
	c.write([]byte{kindHangup, 0, 0})
	replies := c.wait(t)
	if err := <-serveErr; err != nil {
		t.Fatal(err)
	}
	wantReplies := 0
	if sends {
		wantReplies = frames
	}
	if len(replies) != wantReplies {
		t.Errorf("%d frames sent back, want %d", len(replies), wantReplies)
	}
	if len(got) != frames*f {
		t.Fatalf("deliver got %d samples, want %d", len(got), frames*f)
	}
	if !slices.ContainsFunc(got, func(s int16) bool { return s != 0 }) {
		t.Fatal("the cleaned audio is all zero; the comparison would not see a byte-order error")
	}
	b, err := os.ReadFile(filepath.Join(dir, id.String()+".raw"))
	if err != nil {
		t.Fatal(err)
	}
	want := make([]byte, 2*len(got))
	for i, s := range got {
		want[2*i], want[2*i+1] = byte(s), byte(uint16(s)>>8) // little-endian
	}
	if !bytes.Equal(b, want) {
		t.Errorf("the file has %d bytes that differ from the %d bytes of the cleaned frames", len(b), len(want))
	}
	if !strings.Contains(logged.String(), "0 frames dropped, error <nil>") {
		t.Errorf("log %q, want 0 frames dropped and no error", logged.String())
	}
}

// TestAudioSocketRecorderDrops blocks the writing goroutine and checks that
// deliver drops what does not fit and that the rest is written in order.
func TestAudioSocketRecorderDrops(t *testing.T) {
	const f, delivered = 160, 20
	pr, pw := io.Pipe() // Write blocks until the test reads
	r, err := newRecorder(pw, 4*f)
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]int16, f)
	for k := range delivered {
		frame[0] = int16(k + 1)
		r.deliver(frame)
	}
	read := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(pr)
		read <- b
	}()
	dropped, err := r.close()
	if err != nil {
		t.Fatal(err)
	}
	b := <-read
	// At most two buffers of frames are stored: one in the pipe write, one in frames.
	if dropped < delivered-8 {
		t.Errorf("%d frames dropped, want at least %d", dropped, delivered-8)
	}
	if len(b) != 2*f*(delivered-dropped) {
		t.Fatalf("%d bytes written, want %d for %d frames", len(b), 2*f*(delivered-dropped), delivered-dropped)
	}
	last := 0
	for k := range delivered - dropped {
		n := int(binary.LittleEndian.Uint16(b[2*f*k:]))
		if n <= last {
			t.Fatalf("frame %d written is frame %d, after frame %d", k, n, last)
		}
		last = n
	}
}

// asBytes is a connection that reads r and records what is written.
type asBytes struct {
	r io.Reader
	w []byte
}

func (c *asBytes) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c *asBytes) Write(p []byte) (int, error) { c.w = append(c.w, p...); return len(p), nil }

// TestAudioSocketMessages checks next on hand-written messages, so that the
// framing does not depend on the helpers that build the other tests' messages.
func TestAudioSocketMessages(t *testing.T) {
	frame := make([]byte, 320) // one 20 ms frame at 8 kHz: samples 0x0201, then zeros
	frame[0], frame[1] = 0x01, 0x02
	cases := []struct {
		name  string
		msg   []byte
		sends bool
		done  bool
		err   error
		in    []int16 // samples waiting in the ring afterwards
		reply []byte  // bytes sent back
	}{
		{name: "audio", msg: []byte{0x10, 0x00, 0x04, 0x01, 0x00, 0xff, 0xff}, in: []int16{1, -1}},
		{name: "frame sent back", msg: append([]byte{0x10, 0x01, 0x40}, frame...), sends: true,
			reply: append([]byte{0x10, 0x01, 0x40, 0x34, 0x12}, make([]byte, 318)...)},
		{name: "uuid", msg: append([]byte{0x01, 0x00, 0x10}, make([]byte, 16)...)},
		{name: "dtmf", msg: []byte{0x03, 0x00, 0x01, '5'}},
		{name: "hangup", msg: []byte{0x00, 0x00, 0x00}, done: true},
		{name: "end of file", msg: nil, done: true},
		{name: "cut mid-message", msg: []byte{0x10, 0x00, 0x04, 0x01}, done: true, err: io.ErrUnexpectedEOF},
		{name: "error", msg: []byte{0xff, 0x00, 0x00}, done: true, err: errAudioSocket},
		{name: "12 kHz audio", msg: []byte{0x11, 0x00, 0x02, 0x00, 0x00}, done: true, err: errOtherRate},
		{name: "192 kHz audio", msg: []byte{0x18, 0x00, 0x02, 0x00, 0x00}, done: true, err: errOtherRate},
		{name: "odd payload", msg: []byte{0x10, 0x00, 0x03, 0x00, 0x00, 0x00}, err: errOddPayload},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, err := newAudioSocketCall(8000, c.sends)
			if err != nil {
				t.Fatal(err)
			}
			pending, err := buffer.NewSPSC(buffer.SPSCConfig{Capacity: 8000})
			if err != nil {
				t.Fatal(err)
			}
			if c.sends {
				p := make([]int16, a.frame)
				p[0] = 0x1234
				onPacket(pending, p)
			}
			conn := &asBytes{r: bytes.NewReader(c.msg)}
			delivered := 0
			done, err := a.next(conn, pending, func([]int16) { delivered++ })
			if done != c.done || err != c.err {
				t.Fatalf("next: done %v, %v; want %v, %v", done, err, c.done, c.err)
			}
			in := make([]int16, a.in.Len())
			a.in.Read(in)
			if !slices.Equal(in, c.in) {
				t.Errorf("samples waiting: %v, want %v", in, c.in)
			}
			if !bytes.Equal(conn.w, c.reply) {
				t.Errorf("sent back % x, want % x", conn.w, c.reply)
			}
			if c.reply != nil && delivered != 1 {
				t.Errorf("%d frames delivered, want 1", delivered)
			}
		})
	}
}

// TestAudioSocketAccept runs acceptAudioSockets with beeper and two calls at
// once; the first stays open until the second ends, so serial serving fails.
func TestAudioSocketAccept(t *testing.T) {
	const rate = 8000
	f := rate * 20 / 1000
	var logged syncBuffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var started []uuid
	ended := make(chan uuid, 2)
	beep := beeper(rate)
	acceptErr := make(chan error, 1)
	go func() {
		acceptErr <- acceptAudioSockets(ln, rate, func(id uuid) (session, error) {
			mu.Lock()
			started = append(started, id)
			mu.Unlock()
			s, err := beep(id)
			end := s.end
			s.end = func() { end(); ended <- id }
			return s, err
		})
	}()

	ids := []uuid{{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}, {0xf0, 0xe0, 0xd0, 0xc0, 0xb0, 0xa0, 0x90, 0x80, 0x70, 0x60, 0x50, 0x40, 0x30, 0x20, 0x10, 0x01}}
	frames := 50 // 1 s
	mic := callScenario(rate).mic[:frames*f]
	first := dialAudioSocket(t, ln.Addr().String(), ids[0])
	first.write(asAudio(kindSlin, mic[:f]))
	second := dialAudioSocket(t, ln.Addr().String(), ids[1])
	for k := range frames {
		second.write(asAudio(kindSlin, mic[k*f:(k+1)*f]))
		time.Sleep(20 * time.Millisecond)
	}
	second.write([]byte{kindHangup, 0, 0})
	for _, c := range []*fakeAsterisk{second, first} {
		if c == first {
			first.write([]byte{kindHangup, 0, 0})
		}
		replies := c.wait(t)
		want := frames
		if c == first {
			want = 1
		}
		if len(replies) != want {
			t.Fatalf("call %v: %d frames sent back, want %d", c.id, len(replies), want)
		}
		beeps := 0
		for k, m := range replies {
			if m[0] != kindSlin || len(m) != 3+2*f {
				t.Fatalf("call %v, frame %d sent back: kind %#x, %d bytes; want %#x, %d", c.id, k, m[0], len(m), kindSlin, 3+2*f)
			}
			if slices.ContainsFunc(m[3:], func(b byte) bool { return b != 0 }) {
				beeps++
			}
		}
		t.Logf("call %v: %d of %d frames sent back carry the beep", c.id, beeps, len(replies))
		if c == second && (beeps == 0 || beeps > 10) {
			t.Errorf("call %v: %d frames of 1 s carry the beep, want 1 to 10 (one 200 ms beep)", c.id, beeps)
		}
	}
	for range 2 {
		select {
		case <-ended:
		case <-time.After(10 * time.Second):
			t.Fatal("a session did not end after its hangup")
		}
	}
	ln.Close()
	if err := <-acceptErr; !errors.Is(err, net.ErrClosed) {
		t.Errorf("acceptAudioSockets returned %v after the listener closed, want net.ErrClosed", err)
	}
	mu.Lock()
	defer mu.Unlock()
	slices.SortFunc(started, func(a, b uuid) int { return bytes.Compare(a[:], b[:]) })
	if !slices.Equal(started, ids) { // ids is sorted; the two starts may run in either order
		t.Errorf("sessions started with UUIDs %v, want %v", started, ids)
	}
	for _, id := range ids {
		if !strings.Contains(logged.String(), fmt.Sprintf("call %v:", id)) {
			t.Errorf("no log line at the end of call %v; logged %q", id, logged.String())
		}
	}
}

// fakeAsterisk is one AudioSocket connection seen from Asterisk's side.
type fakeAsterisk struct {
	id      uuid
	conn    net.Conn
	replies chan [][]byte // every message sent back, once the service closes
	t       *testing.T
}

// dialAudioSocket connects to addr and sends the UUID, as Asterisk does.
func dialAudioSocket(t *testing.T, addr string, id uuid) *fakeAsterisk {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	c := &fakeAsterisk{id: id, conn: conn, replies: make(chan [][]byte, 1), t: t}
	go func() {
		var replies [][]byte
		for {
			hdr := make([]byte, 3)
			if _, err := io.ReadFull(conn, hdr); err != nil {
				c.replies <- replies
				return
			}
			m := append(hdr, make([]byte, binary.BigEndian.Uint16(hdr[1:]))...)
			if _, err := io.ReadFull(conn, m[3:]); err != nil {
				c.replies <- replies
				return
			}
			replies = append(replies, m)
		}
	}()
	c.write(asMessage(kindUUID, id[:]))
	return c
}

func (c *fakeAsterisk) write(m []byte) {
	if _, err := c.conn.Write(m); err != nil {
		c.t.Fatal(err)
	}
}

// wait returns the messages sent back once the service closes the connection.
func (c *fakeAsterisk) wait(t *testing.T) [][]byte {
	select {
	case r := <-c.replies:
		return r
	case <-time.After(10 * time.Second):
		t.Fatalf("call %v: the service did not close the connection", c.id)
		return nil
	}
}

// syncBuffer is a bytes.Buffer that the log package and the test share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestReadUUID checks readUUID and the String form on hand-written bytes.
func TestReadUUID(t *testing.T) {
	msg := []byte{0x01, 0x00, 0x10, 0x6b, 0xa7, 0xb8, 0x10, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}
	id, err := readUUID(bytes.NewReader(msg))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := id.String(), "6ba7b810-9dad-11d1-80b4-00c04fd430c8"; got != want {
		t.Errorf("UUID %s, want %s", got, want)
	}
	for _, c := range []struct {
		name string
		msg  []byte
		err  error
	}{
		{"audio first", []byte{0x10, 0x00, 0x02, 0x00, 0x00}, errNoUUID},
		{"short UUID", []byte{0x01, 0x00, 0x0f}, errNoUUID},
		{"cut UUID", msg[:10], io.ErrUnexpectedEOF},
		{"nothing", nil, io.EOF},
	} {
		if _, err := readUUID(bytes.NewReader(c.msg)); err != c.err {
			t.Errorf("%s: readUUID returned %v, want %v", c.name, err, c.err)
		}
	}
}
