package integration

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danieldonoghue/govoip-dsp/agc"
	"github.com/danieldonoghue/govoip-dsp/buffer"
	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
	"github.com/danieldonoghue/govoip-dsp/preprocess"
)

// This file is the quick start of docs/audiosocket.md: one AudioSocket
// connection from Asterisk, served on one goroutine.

// Message kinds of the AudioSocket protocol.
const (
	kindHangup = 0x00 // end the session; no payload
	kindUUID   = 0x01 // the call's UUID, 16 bytes: Asterisk's first message
	kindDTMF   = 0x03 // one DTMF digit, 1 ASCII byte
	kindSlin   = 0x10 // audio: 16-bit signed mono PCM, little-endian, 8 kHz
	kindSlin16 = 0x12 // the same at 16 kHz; 0x11 and 0x13 to 0x18 are other rates
	kindLast   = 0x18 // the last audio kind, 192 kHz
	kindError  = 0xff // an error; the payload is an optional error code
)

var (
	errAudioSocket = errors.New("audiosocket: error message from Asterisk")
	errOtherRate   = errors.New("audiosocket: audio at another rate")
	errOddPayload  = errors.New("audiosocket: audio payload of an odd number of bytes")
)

// audioSocketCall is the send path of one AudioSocket connection. Use it
// from the connection's goroutine only.
type audioSocketCall struct {
	kind  byte // the audio kind of this connection, received and sent
	frame int  // samples in one 20 ms frame

	paced *pacedCall // echo, preprocess and agc, when audio is sent back
	pre   *preprocess.Preprocessor
	gc    *agc.AGC

	in            *buffer.Ring // received samples, until a whole frame is there
	hdr           [3]byte
	payload       []byte
	pcm           []int16
	mic, far, out []int16
	reply         []byte // one audio message: header and one frame
}

func newAudioSocketCall(rate int, sendsAudio bool) (*audioSocketCall, error) {
	a := &audioSocketCall{frame: rate * 20 / 1000}
	switch rate {
	case 8000:
		a.kind = kindSlin
	case 16000:
		a.kind = kindSlin16
	default:
		// echo supports 8 and 16 kHz only; see docs/usage.md for resampling.
		return nil, fmt.Errorf("audiosocket: %d Hz; this code takes 8 or 16 kHz", rate)
	}
	var err error
	if sendsAudio {
		// What is sent back can return as echo: make echo, preprocess and agc
		// as recipe A of docs/usage.md does, with a 200 ms echo filter.
		if a.paced, err = newPacedCall(rate, 20, 200); err != nil {
			return nil, err
		}
	} else {
		// Nothing is sent back, so there is no far end and no echo to cancel.
		if a.pre, err = preprocess.New(preprocess.Config{SampleRate: rate, FrameSize: a.frame}); err != nil {
			return nil, err
		}
		a.pre.SetVAD(true) // the VAD decision tells the AGC which frames are speech
		if a.gc, err = agc.New(agc.DefaultConfig(rate)); err != nil {
			return nil, err
		}
	}
	// A payload has at most 65535 bytes (32767 samples); less than one frame
	// can wait from the payload before it, so a whole payload always fits.
	if a.in, err = buffer.New(buffer.Config{Capacity: 32767 + a.frame, Overflow: buffer.Reject}); err != nil {
		return nil, err
	}
	a.payload = make([]byte, 65535)
	a.pcm = make([]int16, 32767)
	a.mic, a.far, a.out = make([]int16, a.frame), make([]int16, a.frame), make([]int16, a.frame)
	a.reply = make([]byte, 3+2*a.frame)
	return a, nil
}

// serveAudioSocket serves one connection until it ends. pending holds
// the audio to send, written by another goroutine; nil sends nothing back.
func serveAudioSocket(conn net.Conn, rate int, pending *buffer.SPSC, deliver func([]int16)) error {
	defer conn.Close()
	a, err := newAudioSocketCall(rate, pending != nil)
	if err != nil {
		return err
	}
	for {
		done, err := a.next(conn, pending, deliver)
		if done || err != nil {
			return err
		}
	}
}

// next reads one message and handles it; done reports the end of the session.
func (a *audioSocketCall) next(conn io.ReadWriter, pending *buffer.SPSC, deliver func([]int16)) (done bool, err error) {
	if _, err := io.ReadFull(conn, a.hdr[:]); err != nil {
		if err == io.EOF {
			return true, nil // Asterisk closed the connection
		}
		return true, err
	}
	// The header is the kind and the payload length, big-endian.
	payload := a.payload[:binary.BigEndian.Uint16(a.hdr[1:])]
	if _, err := io.ReadFull(conn, payload); err != nil {
		return true, err
	}
	switch k := a.hdr[0]; {
	case k == a.kind:
		return false, a.audio(conn, payload, pending, deliver)
	case k == kindHangup:
		return true, nil
	case k == kindError:
		return true, errAudioSocket
	case k >= kindSlin && k <= kindLast:
		return true, errOtherRate
	default:
		return false, nil // kindUUID, kindDTMF (payload[0] is the digit), others
	}
}

// audio cuts the received samples into 20 ms frames and processes each.
func (a *audioSocketCall) audio(w io.Writer, payload []byte, pending *buffer.SPSC, deliver func([]int16)) error {
	if len(payload)%2 != 0 {
		return errOddPayload
	}
	pcm := a.pcm[:len(payload)/2]
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(payload[2*i:]))
	}
	a.in.Write(pcm) // always fits: see newAudioSocketCall
	for a.in.Len() >= a.frame {
		a.in.Read(a.mic)
		if err := a.step(w, pending, deliver); err != nil {
			return err
		}
	}
	return nil
}

// step processes one received frame and, when audio is sent back, sends
// one frame first.
func (a *audioSocketCall) step(w io.Writer, pending *buffer.SPSC, deliver func([]int16)) error {
	if a.paced == nil {
		speech, err := a.pre.Run(a.mic) // rewrites mic in place
		if err != nil {
			return err
		}
		if err := a.gc.Process(a.mic, a.out, speech); err != nil {
			return err
		}
		deliver(a.out)
		return nil
	}
	// One frame back per frame received: the oldest pending frame, or
	// silence. Asterisk plays what it gets as it arrives, so this paces it.
	if pending.Len() >= a.frame {
		pending.Read(a.far)
	} else {
		clear(a.far)
	}
	a.reply[0] = a.kind
	binary.BigEndian.PutUint16(a.reply[1:], uint16(2*a.frame))
	for i, s := range a.far {
		binary.LittleEndian.PutUint16(a.reply[3+2*i:], uint16(s))
	}
	if _, err := w.Write(a.reply); err != nil {
		return err
	}
	// Cancel the echo of what was sent, this frame included, then denoise
	// and set the level (recipe A's received in docs/usage.md).
	if err := a.paced.received(a.mic, a.far, a.out); err != nil {
		return err
	}
	deliver(a.out)
	return nil
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
	s := callScenario(rate, 1)
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

	// The service: one goroutine per connection.
	var got []int16 // written by the service goroutine only, until wg.Wait
	serveErr := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := ln.Accept()
		if err != nil {
			serveErr <- err
			return
		}
		serveErr <- serveAudioSocket(conn, rate, pending, func(x []int16) { got = append(got, x...) })
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
	write(asMessage(kindUUID, make([]byte, 16)))
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
	_, want := replayPaced(t, rate, 20, 200, sent, mic)
	testutil.Compare(t, "replay, chain output", want, got)
	// A replay that cancels against silence must differ, or the comparison
	// above could not catch a wrong pairing.
	_, silentWant := replayPaced(t, rate, 20, 200, make([]int16, len(sent)), mic)
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
			s := callScenario(rate, 1)
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
