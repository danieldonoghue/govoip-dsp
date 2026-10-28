package echo_test

import (
	"errors"
	"fmt"
	"math"

	"github.com/danieldonoghue/govoip-dsp/echo"
)

// farEnd returns n samples of noise-like far-end audio in [-8192, 8191],
// in integer arithmetic only, so the examples print the same everywhere.
func farEnd(n int) []int16 {
	x := make([]int16, n)
	seed := uint32(7)
	for i := range x {
		seed = seed*1664525 + 1013904223
		x[i] = int16(int32(seed>>18) - 8192)
	}
	return x
}

// room returns what the microphone picks up from far: a copy delayed by
// delay samples at half the level, plus a weaker reflection 2 ms later.
func room(far []int16, delay, rate int) []int16 {
	mic := make([]int16, len(far))
	refl := delay + 2*rate/1000
	for i := range mic {
		var v int32
		if i >= delay {
			v += int32(far[i-delay]) / 2
		}
		if i >= refl {
			v += int32(far[i-refl]) / 8
		}
		mic[i] = int16(v)
	}
	return mic
}

// erle returns 10·log10(energy(mic)/energy(out)) in whole dB: how much of
// the echo the canceller removed.
func erle(mic, out []int16) int {
	var a, b float64
	for i := range mic {
		a += float64(mic[i]) * float64(mic[i])
		b += float64(out[i]) * float64(out[i])
	}
	return int(math.Round(10 * math.Log10(a/max(b, 1))))
}

// The echo canceller removes the far end's echo from the microphone
// signal, frame by frame. Here the echo arrives 30 ms after the far end
// and the filter covers 100 ms. The filter starts empty, so in the first
// 200 ms it removes only about half of the echo energy (3 dB); once it has
// converged, little is left.
func Example() {
	const rate, frameMs, filterMs = 8000, 20, 100
	c, err := echo.New(echo.Config{
		FrameSize:    rate * frameMs / 1000,  // 160 samples
		FilterLength: rate * filterMs / 1000, // 800 samples
		SampleRate:   rate,
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	far := farEnd(4 * rate)
	mic := room(far, rate*30/1000, rate)
	out := make([]int16, len(mic))
	n := c.FrameSize()
	for f := 0; f+n <= len(mic); f += n {
		// far[f:f+n] is the frame played while mic[f:f+n] was captured.
		if err := c.Cancel(mic[f:f+n], far[f:f+n], out[f:f+n]); err != nil {
			fmt.Println(err)
			return
		}
	}
	fmt.Println("first 200 ms:", erle(mic[:rate/5], out[:rate/5]), "dB")
	fmt.Println("last second:", erle(mic[3*rate:], out[3*rate:]), "dB")
	// Output:
	// first 200 ms: 3 dB
	// last second: 33 dB
}

// New checks the frame size, the filter length and the rate, and returns
// an error that wraps ErrConfig.
func ExampleNew() {
	_, err := echo.New(echo.Config{FrameSize: 161, FilterLength: 1600, SampleRate: 8000})
	fmt.Println(errors.Is(err, echo.ErrConfig))
	fmt.Println(err)
	_, err = echo.New(echo.Config{FrameSize: 160, FilterLength: echo.MaxFilterLength + 1, SampleRate: 8000})
	fmt.Println(err)
	_, err = echo.New(echo.Config{FrameSize: echo.MaxFrameSize, FilterLength: 1600, SampleRate: echo.MaxSampleRate + 1})
	fmt.Println(err)
	// Output:
	// true
	// echo: invalid configuration: frame size 161 (want 1 to 2048, with 2*frame size supported by the FFT)
	// echo: invalid configuration: filter length 65537 (want 1 to 65536 samples)
	// echo: invalid configuration: sample rate 48001 (want 8000 to 48000 Hz)
}

// A Config from a rate, a frame duration and the longest echo to cancel.
// The filter must cover the echo delay plus the echo tail; a longer one
// costs more and adapts more slowly.
func ExampleConfig() {
	const rate, frameMs = 16000, 20
	const maxDelayMs, tailMs = 100, 50
	cfg := echo.Config{
		FrameSize:    rate * frameMs / 1000,               // 320
		FilterLength: rate * (maxDelayMs + tailMs) / 1000, // 2400
		SampleRate:   rate,
	}
	c, err := echo.New(cfg)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(c.FrameSize(), c.FilterLength(), c.SampleRate(), echo.MinSampleRate)
	// Output:
	// 320 2560 16000 8000
}

// A Canceller holds the adaptive filter of one call. Echo outside its
// filter length is not cancelled: here the echo arrives 150 ms late, past
// a 100 ms filter.
func ExampleCanceller() {
	const rate = 8000
	c, err := echo.New(echo.Config{FrameSize: 160, FilterLength: 800, SampleRate: rate})
	if err != nil {
		fmt.Println(err)
		return
	}
	far := farEnd(4 * rate)
	mic := room(far, rate*150/1000, rate)
	out := make([]int16, len(mic))
	for f := 0; f+160 <= len(mic); f += 160 {
		if err := c.Cancel(mic[f:f+160], far[f:f+160], out[f:f+160]); err != nil {
			fmt.Println(err)
			return
		}
	}
	fmt.Println("150 ms echo, 100 ms filter:", erle(mic[3*rate:], out[3*rate:]), "dB")
	// Output:
	// 150 ms echo, 100 ms filter: 0 dB
}

// Cancel takes exactly one frame of each signal; any other length is
// rejected with ErrBufferSize and changes nothing.
func ExampleCanceller_Cancel() {
	c, err := echo.New(echo.Config{FrameSize: 160, FilterLength: 800, SampleRate: 8000})
	if err != nil {
		fmt.Println(err)
		return
	}
	mic, far, out := make([]int16, 160), make([]int16, 160), make([]int16, 160)
	fmt.Println(c.Cancel(mic, far, out))
	err = c.Cancel(mic[:80], far[:80], out[:80])
	fmt.Println(errors.Is(err, echo.ErrBufferSize))

	var zero echo.Canceller // not made by New
	fmt.Println(errors.Is(zero.Cancel(mic, far, out), echo.ErrConfig))
	// Output:
	// <nil>
	// true
	// true
}

// Buffered mode for audio APIs that deliver playback and capture on
// separate callbacks: Playback queues each played frame, Capture cancels
// each captured frame against the queue. With Playback first in each
// period, Capture cancels against the frame played one frame (20 ms)
// earlier, so the filter covers echo delays of 20 to about 120 ms, and
// this echo of 30 ms is inside that range.
func ExampleCanceller_Capture() {
	const rate = 8000
	c, err := echo.New(echo.Config{FrameSize: 160, FilterLength: 800, SampleRate: rate})
	if err != nil {
		fmt.Println(err)
		return
	}
	far := farEnd(4 * rate)
	mic := room(far, rate*30/1000, rate)
	out := make([]int16, len(mic))
	for f := 0; f+160 <= len(mic); f += 160 {
		if err := c.Playback(far[f : f+160]); err != nil { // the output callback
			fmt.Println(err)
			return
		}
		if err := c.Capture(mic[f:f+160], out[f:f+160]); err != nil { // the input callback
			fmt.Println(err)
			return
		}
	}
	fmt.Println("last second:", erle(mic[3*rate:], out[3*rate:]), "dB")
	// Output:
	// last second: 34 dB
}

// Playback copies its frame into the canceller's queue, which starts
// with two frames of silence. Played frames before the first Capture after
// New or Reset are discarded. A Capture that finds the queue empty copies
// mic to out unprocessed.
func ExampleCanceller_Playback() {
	c, err := echo.New(echo.Config{FrameSize: 4, FilterLength: 8, SampleRate: 8000})
	if err != nil {
		fmt.Println(err)
		return
	}
	mic := []int16{100, 200, 300, 400}
	out := make([]int16, 4)
	if err := c.Playback(mic); err != nil { // before the first Capture: discarded
		fmt.Println(err)
		return
	}
	for i := range 3 {
		if err := c.Capture(mic, out); err != nil {
			fmt.Println(err)
			return
		}
		// The first two Captures cancel against the two silent frames, and
		// the DC notch changes the output. The third finds nothing queued.
		fmt.Println("capture", i+1, out)
	}
	fmt.Println(errors.Is(c.Playback(make([]int16, 3)), echo.ErrBufferSize))
	// Output:
	// capture 1 [90 162 218 260]
	// capture 2 [-70 22 96 155]
	// capture 3 [100 200 300 400]
	// true
}

// Residual gives the power spectrum of the echo the last Cancel left in
// its output, ResidualLen() values, for preprocess.Preprocessor.RunEcho
// on the same frame. Calling it again gives the same values.
func ExampleCanceller_Residual() {
	const rate = 8000
	c, err := echo.New(echo.Config{FrameSize: 160, FilterLength: 800, SampleRate: rate})
	if err != nil {
		fmt.Println(err)
		return
	}
	far := farEnd(rate)
	mic := room(far, 240, rate)
	out := make([]int16, 160)
	residual := make([]int32, c.ResidualLen()) // made once, reused per frame
	for f := 0; f+160 <= len(mic); f += 160 {
		if err := c.Cancel(mic[f:f+160], far[f:f+160], out); err != nil {
			fmt.Println(err)
			return
		}
		// Read it now: the next Cancel replaces it.
		if err := c.Residual(residual); err != nil {
			fmt.Println(err)
			return
		}
		// Here a send chain calls pre.RunEcho(out, residual).
	}
	again := make([]int32, c.ResidualLen())
	if err := c.Residual(again); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("same values twice:", fmt.Sprint(again) == fmt.Sprint(residual))
	fmt.Println(errors.Is(c.Residual(residual[:160]), echo.ErrBufferSize))
	// Output:
	// same values twice: true
	// true
}

// ResidualLen is FrameSize()+1: one value per frequency bin from 0 Hz to
// half the sample rate.
func ExampleCanceller_ResidualLen() {
	c, err := echo.New(echo.Config{FrameSize: 160, FilterLength: 800, SampleRate: 8000})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(c.ResidualLen())
	// Output:
	// 161
}

// ImpulseResponse shows the echo path the filter has learned. Its largest
// tap sits at the echo delay, here 5 ms (40 samples at 8 kHz).
func ExampleCanceller_ImpulseResponse() {
	const rate = 8000
	c, err := echo.New(echo.Config{FrameSize: 160, FilterLength: 800, SampleRate: rate})
	if err != nil {
		fmt.Println(err)
		return
	}
	far := farEnd(4 * rate)
	mic := room(far, 40, rate)
	out := make([]int16, 160)
	for f := 0; f+160 <= len(mic); f += 160 {
		if err := c.Cancel(mic[f:f+160], far[f:f+160], out); err != nil {
			fmt.Println(err)
			return
		}
	}
	ir := make([]int32, c.ImpulseResponseLen())
	if err := c.ImpulseResponse(ir); err != nil {
		fmt.Println(err)
		return
	}
	peak := 0
	for i := range ir {
		if ir[i] > ir[peak] {
			peak = i
		}
	}
	fmt.Println("largest tap at", peak, "samples:", peak*1000/rate, "ms")
	// Output:
	// largest tap at 40 samples: 5 ms
}

// ImpulseResponseLen is FilterLength(): one tap per sample of the filter.
func ExampleCanceller_ImpulseResponseLen() {
	c, err := echo.New(echo.Config{FrameSize: 160, FilterLength: 800, SampleRate: 8000})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(c.ImpulseResponseLen(), c.FilterLength())
	// Output:
	// 800 800
}

// FilterLength is the configured length rounded up to whole frames.
func ExampleCanceller_FilterLength() {
	c, err := echo.New(echo.Config{FrameSize: 160, FilterLength: 1000, SampleRate: 8000})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(c.FilterLength(), "samples =", c.FilterLength()/c.FrameSize(), "frames")
	// Output:
	// 1120 samples = 7 frames
}

func ExampleCanceller_FrameSize() {
	c, err := echo.New(echo.Config{FrameSize: 320, FilterLength: 3200, SampleRate: 16000})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(c.FrameSize())
	// Output:
	// 320
}

func ExampleCanceller_SampleRate() {
	c, err := echo.New(echo.Config{FrameSize: 320, FilterLength: 3200, SampleRate: 16000})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(c.SampleRate())
	// Output:
	// 16000
}

// SetSampleRate changes the rate on the fly and keeps the filter; a rate
// out of range returns ErrConfig and changes nothing.
func ExampleCanceller_SetSampleRate() {
	c, err := echo.New(echo.Config{FrameSize: 160, FilterLength: 800, SampleRate: 8000})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(c.SetSampleRate(16000), c.SampleRate())
	fmt.Println(errors.Is(c.SetSampleRate(4000), echo.ErrConfig), c.SampleRate())
	// Output:
	// <nil> 16000
	// true 16000
}

// Reset clears the filter for a new call, without allocating. Like the
// reference it keeps the rate and some learned state (see the package
// doc), so cancellation must converge again.
func ExampleCanceller_Reset() {
	const rate = 8000
	c, err := echo.New(echo.Config{FrameSize: 160, FilterLength: 800, SampleRate: rate})
	if err != nil {
		fmt.Println(err)
		return
	}
	far := farEnd(4 * rate)
	mic := room(far, 240, rate)
	out := make([]int16, len(mic))
	run := func(from, to int) {
		for f := from; f+160 <= to; f += 160 {
			if err := c.Cancel(mic[f:f+160], far[f:f+160], out[f:f+160]); err != nil {
				fmt.Println(err)
				return
			}
		}
	}
	run(0, 3*rate)
	fmt.Println("before Reset:", erle(mic[2*rate:3*rate], out[2*rate:3*rate]), "dB")
	c.Reset()
	run(3*rate, 4*rate)
	fmt.Println("first 100 ms after Reset:", erle(mic[3*rate:3*rate+800], out[3*rate:3*rate+800]), "dB")
	fmt.Println("sample rate kept:", c.SampleRate())
	// Output:
	// before Reset: 31 dB
	// first 100 ms after Reset: 0 dB
	// sample rate kept: 8000
}
