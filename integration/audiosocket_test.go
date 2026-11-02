package integration

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"go/format"
	"io"
	"log"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	errNoUUID      = errors.New("audiosocket: the first message is not a UUID")
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

// uuid is the call's UUID, from Asterisk's first message.
type uuid [16]byte

// String returns the UUID in the form the dialplan writes it.
func (id uuid) String() string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
}

// session is the application's side of one call.
type session struct {
	pending *buffer.SPSC  // audio to send, written by the application; nil sends nothing back
	deliver func([]int16) // takes each cleaned frame; must not keep the slice
	end     func()        // called once, on the connection's goroutine, when the session ends
}

// acceptAudioSockets accepts connections until ln is closed and serves each
// on its own goroutine; start makes the application's side of each call.
func acceptAudioSockets(ln net.Listener, rate int, start func(id uuid) (session, error)) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go func() {
			if err := serveAudioSocket(conn, rate, start); err != nil {
				log.Printf("audiosocket: %v", err)
			}
		}()
	}
}

// serveAudioSocket serves one connection until the session ends.
func serveAudioSocket(conn net.Conn, rate int, start func(id uuid) (session, error)) error {
	defer conn.Close()
	id, err := readUUID(conn)
	if err != nil {
		return err
	}
	s, err := start(id)
	if err != nil {
		return err
	}
	defer s.end()
	a, err := newAudioSocketCall(rate, s.pending != nil)
	if err != nil {
		return err
	}
	for {
		done, err := a.next(conn, s.pending, s.deliver)
		if done || err != nil {
			return err
		}
	}
}

// readUUID reads Asterisk's first message, the call's UUID.
func readUUID(r io.Reader) (id uuid, err error) {
	var hdr [3]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return id, err
	}
	if hdr[0] != kindUUID || binary.BigEndian.Uint16(hdr[1:]) != 16 {
		return id, errNoUUID
	}
	_, err = io.ReadFull(r, id[:])
	return id, err
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

// beeper is an example application: for each call it sends a 200 ms beep
// every 2 s, and logs how many frames it received when the call ends.
func beeper(rate int) func(id uuid) (session, error) {
	frame := rate * 20 / 1000
	cycle := make([]int16, 2*rate) // 2 s: the beep, then silence; shared by every call
	for i := range rate / 5 {
		cycle[i] = int16(8000 * math.Sin(2*math.Pi*440*float64(i)/float64(rate)))
	}
	return func(id uuid) (session, error) {
		// pending holds at most 200 ms, so what is sent lags the
		// application by at most 200 ms.
		pending, err := buffer.NewSPSC(buffer.SPSCConfig{Capacity: rate / 5})
		if err != nil {
			return session{}, err
		}
		stop := make(chan struct{})
		go func() { // the application's goroutine, the only writer of pending
			for k := 0; ; {
				select {
				case <-stop:
					return
				default:
				}
				// Whole frames only. When pending is full, wait and offer the
				// same frame again, so no part of the beep is lost.
				if onPacket(pending, cycle[k*frame:(k+1)*frame]) {
					k = (k + 1) % (len(cycle) / frame)
				} else {
					time.Sleep(10 * time.Millisecond)
				}
			}
		}()
		frames := 0 // used on the connection's goroutine only
		return session{
			pending: pending,
			deliver: func([]int16) { frames++ },
			end: func() {
				close(stop)
				log.Printf("call %v: %d frames received", id, frames)
			},
		}, nil
	}
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
	mic := callScenario(rate, 1).mic[:frames*f]
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

// TestAudioSocketPageBuilds joins the Go blocks of docs/audiosocket.md into
// one main.go and checks that it is gofmt-clean, builds and passes go vet.
func TestAudioSocketPageBuilds(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go command not found")
	}
	root := testutil.RepoRoot(t)
	doc, err := os.ReadFile(filepath.Join(root, "docs", "audiosocket.md"))
	if err != nil {
		t.Fatal(err)
	}
	var src strings.Builder
	for i, b := range regexp.MustCompile("(?s)```go\n(.*?)```").FindAllStringSubmatch(string(doc), -1) {
		if i == 0 && !strings.HasPrefix(b[1], "package main\n") {
			t.Fatal("the first Go block of docs/audiosocket.md does not start with package main")
		}
		if i > 0 {
			src.WriteString("\n")
		}
		src.WriteString(b[1])
	}
	formatted, err := format.Source([]byte(src.String()))
	if err != nil {
		t.Fatalf("the page's Go blocks do not parse as one file: %v", err)
	}
	if string(formatted) != src.String() {
		t.Error("the page's Go blocks, joined, are not gofmt-clean")
	}
	dir := t.TempDir()
	mod := "module audiosocketpage\n\ngo 1.26\n\nrequire github.com/danieldonoghue/govoip-dsp v0.0.0\n\n" +
		"replace github.com/danieldonoghue/govoip-dsp => " + root + "\n"
	for name, data := range map[string]string{"go.mod": mod, "main.go": src.String()} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"build", "-o", filepath.Join(dir, "page"), "."}, {"vet", "."}} {
		cmd := exec.Command(gobin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=", "GOPROXY=off", "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}
