package govoipdsp_test

import (
	"errors"
	"fmt"
	"log"
	"math"

	"github.com/danieldonoghue/govoip-dsp/agc"
	"github.com/danieldonoghue/govoip-dsp/buffer"
	"github.com/danieldonoghue/govoip-dsp/echo"
	"github.com/danieldonoghue/govoip-dsp/preprocess"
	"github.com/danieldonoghue/govoip-dsp/resample"
)

// noise returns n samples of noise-like audio in [-amp, amp), in integer
// arithmetic only, so every example prints the same on every platform.
func noise(n, amp int, seed uint32) []int16 {
	x := make([]int16, n)
	for i := range x {
		seed = seed*1664525 + 1013904223
		x[i] = int16(int64(seed>>16)*int64(2*amp)/65536 - int64(amp))
	}
	return x
}

// energy returns the sum of squares of x.
func energy(x []int16) float64 {
	var e float64
	for _, v := range x {
		e += float64(v) * float64(v)
	}
	return max(e, 1)
}

// dB returns 10·log10(energy(a)/energy(b)) in whole dB.
func dB(a, b []int16) int { return int(math.Round(10 * math.Log10(energy(a)/energy(b)))) }

// dBFS returns the RMS level of x in whole dBFS (0 dBFS is amplitude 32768).
func dBFS(x []int16) int {
	return int(math.Round(10 * math.Log10(energy(x)/float64(len(x))/(32768*32768))))
}

// sendChain holds one call's send-path processors and scratch buffers,
// all made once in newSendChain, so the per-frame path never allocates.
type sendChain struct {
	ec       *echo.Canceller
	pre      *preprocess.Preprocessor
	gc       *agc.AGC
	clean    []int16 // the echo canceller's output, then cleaned in place
	residual []int32 // the residual-echo spectrum, ResidualLen() values
}

func newSendChain(rate, frameMs, filterMs int) (*sendChain, error) {
	// A processing frame is the same duration in every stage: 20 ms is 160
	// samples at 8 kHz and 320 at 16 kHz.
	frame := rate * frameMs / 1000
	// The preprocessor's FFT needs a supported frame size; check it before
	// making anything, so a bad duration fails here and not mid-call.
	if !preprocess.SupportedFrameSize(frame) {
		return nil, fmt.Errorf("no preprocessor for %d-sample frames", frame)
	}
	ec, err := echo.New(echo.Config{
		FrameSize: frame,
		// The filter must cover the echo delay plus the echo tail.
		FilterLength: rate * filterMs / 1000,
		SampleRate:   rate, // 8000 or 16000: the rates echo supports
	})
	if err != nil {
		return nil, err
	}
	pre, err := preprocess.New(preprocess.Config{SampleRate: rate, FrameSize: frame})
	if err != nil {
		return nil, err
	}
	// The VAD decision tells the AGC which frames are speech; denoise is
	// on by default.
	pre.SetVAD(true)
	gc, err := agc.New(agc.DefaultConfig(rate))
	if err != nil {
		return nil, err
	}
	return &sendChain{
		ec: ec, pre: pre, gc: gc,
		clean:    make([]int16, frame),
		residual: make([]int32, ec.ResidualLen()),
	}, nil
}

// step cleans one microphone frame. far is the loudspeaker frame played
// while mic was captured; out receives the frame to encode and send.
func (s *sendChain) step(mic, far, out []int16) (speech bool, err error) {
	// 1. Remove the echo of far from mic.
	if err := s.ec.Cancel(mic, far, s.clean); err != nil {
		return false, err
	}
	// 2. Read the echo Cancel left in this frame, before the next Cancel
	//    replaces it.
	if err := s.ec.Residual(s.residual); err != nil {
		return false, err
	}
	// 3. Suppress noise and that residual echo, in place, and decide
	//    whether the frame is speech.
	speech, err = s.pre.RunEcho(s.clean, s.residual)
	if err != nil {
		return false, err
	}
	// 4. Bring the level towards the target. The AGC raises its gain only
	//    on frames passed with speech true and above its gate level.
	return speech, s.gc.Process(s.clean, out, speech)
}

// The send chain of one call at 20 ms frames: echo, then preprocess with
// the residual echo, then agc with the VAD decision. The far end plays
// noise-like audio for 6 s, whose echo returns 30 ms later at half the
// level; the near end is silent until it talks from 4 s on.
func Example_sendChain() {
	const rate, frameMs, filterMs = 8000, 20, 100
	chain, err := newSendChain(rate, frameMs, filterMs)
	if err != nil {
		log.Fatal(err)
	}
	frame := rate * frameMs / 1000

	far := noise(6*rate, 8000, 1)
	near := noise(6*rate, 1500, 2)
	mic := make([]int16, len(far))
	delay := rate * 30 / 1000
	for i := range mic {
		if i >= delay {
			mic[i] = far[i-delay] / 2 // the echo
		}
		if i >= 4*rate {
			mic[i] += near[i] // the near end talks
		}
	}

	out := make([]int16, len(mic))
	speech := map[bool]int{}
	for f := 0; f+frame <= len(mic); f += frame {
		isSpeech, err := chain.step(mic[f:f+frame], far[f:f+frame], out[f:f+frame])
		if err != nil {
			log.Fatal(err)
		}
		if f >= 2*rate && f < 4*rate {
			speech[isSpeech]++
		}
	}
	// From 2 s to 4 s, after the filter has converged, only echo reaches
	// the microphone and the chain removes it. What is left is below the
	// AGC's gate level, so the gain does not rise there in any case.
	fmt.Println("echo removed, 2 s to 4 s:", dB(mic[2*rate:4*rate], out[2*rate:4*rate]), "dB")
	fmt.Println("speech frames, 2 s to 4 s:", speech[true], "of", speech[true]+speech[false])
	// From 4 s the near end talks over the echo (double talk). It is sent,
	// its level rising towards the AGC target of -24 dBFS by at most 12 dB
	// per second.
	fmt.Println("near end in, sent out, 5 s to 6 s:", dBFS(near[5*rate:]), dBFS(out[5*rate:]), "dBFS")
	// Output:
	// echo removed, 2 s to 4 s: 52 dB
	// speech frames, 2 s to 4 s: 0 of 100
	// near end in, sent out, 5 s to 6 s: -32 -27 dBFS
}

// The receive chain: decoded 8 kHz frames up to a 48 kHz device. OutLen,
// read on a new Resampler, sizes the output once: until a setter changes
// the filter, every call of one frame then consumes the whole frame. After
// a setter, read OutLen again (it does not allocate).
func Example_receiveChain() {
	const netRate, devRate, frameMs = 8000, 48000, 20
	r, err := resample.New(resample.Config{
		Channels: 1, InRate: netRate, OutRate: devRate, Quality: resample.QualityVoIP,
	})
	if err != nil {
		log.Fatal(err)
	}
	decoded := make([]int16, netRate*frameMs/1000)  // one 20 ms frame, 160 samples
	device := make([]int16, r.OutLen(len(decoded))) // made once
	written := 0
	for f := range 50 { // one second of frames
		copy(decoded, noise(len(decoded), 4000, uint32(f))) // stands in for the decoder
		read, n, err := r.Process(decoded, device)
		if err != nil {
			log.Fatal(err)
		}
		if read != len(decoded) {
			log.Fatal("input left over") // cannot happen: no setter was called
		}
		written += n // hand device[:n] to the sound card
	}
	fmt.Println("frame", len(decoded), "samples; output buffer", len(device))
	fmt.Println("one second in, samples out:", written)
	fmt.Println("delay:", r.OutputLatency(), "samples at 48 kHz")
	// Output:
	// frame 160 samples; output buffer 961
	// one second in, samples out: 48000
	// delay: 144 samples at 48 kHz
}

// A network goroutine hands received frames to the processing goroutine
// through a buffer.SPSC, which never blocks either side. After a stall of
// the processing goroutine the SPSC holds a backlog of stale audio; the
// consumer drops it and keeps only the newest frame. A stall longer than
// the SPSC holds loses the newest frames instead, because the SPSC rejects
// what does not fit. Where each received frame pairs with a far-end frame,
// drop as many far-end frames too.
func Example_handoff() {
	const frame = 160 // 20 ms at 8 kHz
	// 2000 samples, 250 ms: twelve whole frames and half a frame. The
	// producer stores only whole frames, so the half frame stays unused,
	// and a stall of up to 240 ms loses nothing.
	rx, err := buffer.NewSPSC(buffer.SPSCConfig{Capacity: 2000})
	if err != nil {
		log.Fatal(err)
	}

	// The network goroutine (producer) writes frame k as 160 samples of
	// value k, whole frames only. The channels exist only to make this
	// example's output deterministic; the SPSC itself never waits.
	send := make(chan int)
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		packet := make([]int16, frame)
		k := 0
		for n := range send {
			for range n {
				for i := range packet {
					packet[i] = int16(k)
				}
				if rx.Free() >= frame { // a whole frame or nothing
					rx.Write(packet)
				} else {
					fmt.Println("no room: frame", k, "skipped")
				}
				k++
			}
			sent <- struct{}{}
		}
	}()

	// The processing goroutine (consumer), here the example itself.
	in := make([]int16, frame)
	process := func() {
		// Keep at most one frame: older audio is stale for a live call.
		if rx.Len() > frame {
			fmt.Println("dropped", rx.Consume(rx.Len()-frame)/frame, "stale frames")
		}
		for rx.Len() >= frame {
			rx.Read(in)
			fmt.Println("processing frame", in[0], "whole:", in[0] == in[frame-1])
		}
	}

	send <- 1 // one frame arrives, and is processed in time
	<-sent
	process()
	send <- 6 // the processor stalls for 120 ms while six frames arrive
	<-sent
	process()
	send <- 14 // a stall of 280 ms: more than the SPSC holds
	<-sent
	process()
	close(send)
	<-sent
	// Output:
	// processing frame 0 whole: true
	// dropped 5 stale frames
	// processing frame 6 whole: true
	// no room: frame 19 skipped
	// no room: frame 20 skipped
	// dropped 11 stale frames
	// processing frame 18 whole: true
}

// Every constructor checks its Config and returns an error that wraps the
// package's ErrConfig; test it with errors.Is. Per-frame methods return
// unwrapped sentinel errors, which never allocate.
func Example_errors() {
	_, err := echo.New(echo.Config{FrameSize: 160, FilterLength: 1600, SampleRate: 0})
	fmt.Println(errors.Is(err, echo.ErrConfig))

	pre, err := preprocess.New(preprocess.Config{SampleRate: 8000, FrameSize: 160})
	if err != nil {
		log.Fatal(err)
	}
	_, err = pre.Run(make([]int16, 100)) // not a whole frame
	fmt.Println(err == preprocess.ErrBufferSize)
	// Output:
	// true
	// true
}
