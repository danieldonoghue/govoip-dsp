// Command audiosocket is a service for Asterisk AudioSocket. It accepts each
// call's TCP connection, sends a beep back every 2 s, cleans the caller's
// audio with echo cancellation, noise suppression and automatic gain control,
// and writes the cleaned audio to <UUID>.raw in the working directory.
//
// Run it from the repository root with
//
//	go run ./examples/audiosocket
//
// It listens on TCP port 9092 and takes 8 kHz calls. docs/audiosocket.md
// explains the program, the dialplan that connects a call to it, and the
// AudioSocket protocol.
package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/danieldonoghue/govoip-dsp/agc"
	"github.com/danieldonoghue/govoip-dsp/buffer"
	"github.com/danieldonoghue/govoip-dsp/echo"
	"github.com/danieldonoghue/govoip-dsp/preprocess"
)

func main() {
	// The dialplan connects a call with, for example, Answer() and then
	// AudioSocket(40325ec2-5efd-4bd3-805f-53576e581d13,127.0.0.1:9092).
	ln, err := net.Listen("tcp", ":9092")
	if err != nil {
		log.Fatal(err)
	}
	// beeper is the example application; recording also writes each call's
	// cleaned audio to <UUID>.raw in the working directory.
	const rate = 8000 // 16000 for a dial string with c(slin16)
	log.Fatal(acceptAudioSockets(ln, rate, recording(".", rate, beeper(rate))))
}

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

// pacedCall holds echo, preprocess and agc for one call whose sending is
// paced off the received frames, as in recipe A of docs/usage.md. Use it
// from one goroutine.
type pacedCall struct {
	frame int

	ec  *echo.Canceller
	pre *preprocess.Preprocessor
	gc  *agc.AGC

	echoed   []int16 // the echo canceller's output, before preprocess
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
	b := &pacedCall{frame: rate * frameMs / 1000}
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

// onPacket stores the whole frame p in rx, or nothing when it does not
// fit, so that rx holds whole frames. It reports whether p was stored.
func onPacket(rx *buffer.SPSC, p []int16) bool {
	// SPSC.Write stores as much as fits. A partial frame would shift every
	// later frame boundary, so store the whole frame or nothing.
	if rx.Free() < len(p) {
		return false // no room: store nothing
	}
	rx.Write(p)
	return true
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

// recording wraps start: it also writes each call's cleaned audio to the
// file <dir>/<UUID>.raw, raw 16-bit little-endian mono PCM at rate Hz.
func recording(dir string, rate int, start func(id uuid) (session, error)) func(id uuid) (session, error) {
	return func(id uuid) (session, error) {
		s, err := start(id)
		if err != nil {
			return session{}, err
		}
		name := filepath.Join(dir, id.String()+".raw")
		f, err := os.Create(name)
		if err != nil {
			s.end()
			return session{}, err
		}
		// Up to 2 s of audio can wait for the disk; deliver drops what does not fit.
		r, err := newRecorder(f, 2*rate)
		if err != nil {
			f.Close()
			s.end()
			return session{}, err
		}
		deliver, end := s.deliver, s.end
		s.deliver = func(x []int16) {
			deliver(x)
			r.deliver(x)
		}
		s.end = func() {
			end()
			dropped, err := r.close()
			log.Printf("call %v: recorded to %s, %d frames dropped, error %v", id, name, dropped, err)
		}
		return s, nil
	}
}

// recorder writes frames to a file on its own goroutine, so that deliver,
// on the connection's goroutine, never waits for the disk.
type recorder struct {
	frames  *buffer.SPSC  // samples from deliver to the writing goroutine
	dropped int           // frames that did not fit; used by deliver's goroutine only
	done    chan struct{} // closed after the last deliver
	written chan error    // the writing goroutine's result
}

// newRecorder starts the goroutine that writes the delivered samples to w;
// capacity is the number of samples that can wait for it.
func newRecorder(w io.WriteCloser, capacity int) (*recorder, error) {
	frames, err := buffer.NewSPSC(buffer.SPSCConfig{Capacity: capacity})
	if err != nil {
		return nil, err
	}
	r := &recorder{frames: frames, done: make(chan struct{}), written: make(chan error, 1)}
	go func() { r.written <- writePCM(w, frames, r.done) }()
	return r, nil
}

// deliver copies the frame x for the writing goroutine, or drops it when
// that goroutine has fallen behind. It never waits.
func (r *recorder) deliver(x []int16) {
	if !onPacket(r.frames, x) {
		r.dropped++
	}
}

// close waits until the writing goroutine has written every stored sample
// and closed w. Call it once, after the last deliver.
func (r *recorder) close() (dropped int, err error) {
	close(r.done)
	return r.dropped, <-r.written
}

// writePCM writes the samples in frames to w as 16-bit little-endian PCM
// until done is closed and frames is empty, then closes w.
func writePCM(w io.WriteCloser, frames *buffer.SPSC, done <-chan struct{}) error {
	pcm := make([]int16, frames.Cap())
	b := make([]byte, 2*len(pcm))
	for {
		last := false
		select {
		case <-done:
			last = true // nothing more is delivered: write what is stored, then stop
		default:
		}
		n := frames.Read(pcm)
		if n == 0 {
			if last {
				return w.Close()
			}
			time.Sleep(20 * time.Millisecond)
			continue
		}
		for i, s := range pcm[:n] {
			binary.LittleEndian.PutUint16(b[2*i:], uint16(s))
		}
		if _, err := w.Write(b[:2*n]); err != nil {
			w.Close()
			return err
		}
	}
}
