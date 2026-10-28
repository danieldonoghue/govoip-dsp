package agc_test

import (
	"errors"
	"fmt"
	"math"

	"github.com/danieldonoghue/govoip-dsp/agc"
)

// square returns n samples of a 500 Hz square wave of the given amplitude
// at rate. Integer arithmetic only, so the output is the same everywhere.
func square(n, amplitude, rate int) []int16 {
	x := make([]int16, n)
	for i := range x {
		if i*1000/rate%2 == 0 {
			x[i] = int16(amplitude)
		} else {
			x[i] = int16(-amplitude)
		}
	}
	return x
}

// dBFS returns the RMS level of x in whole dBFS (0 dBFS is amplitude 32768).
func dBFS(x []int16) int {
	var sum float64
	for _, v := range x {
		sum += float64(v) * float64(v)
	}
	return int(math.Round(10 * math.Log10(sum/float64(len(x))/(32768*32768))))
}

// The AGC brings a quiet talker up to the target level. Here a steady tone
// at -40 dBFS, marked as speech, comes out at the default target of
// -24 dBFS once the gain has risen, at most RiseRate (12 dB/s) per second.
func Example() {
	const rate = 8000
	a, err := agc.New(agc.DefaultConfig(rate))
	if err != nil {
		fmt.Println(err)
		return
	}
	in := square(3*rate, 328, rate) // 328 is about -40 dBFS
	out := make([]int16, len(in))
	frame := rate * 20 / 1000 // 20 ms chunks, as a send chain passes them
	for f := 0; f < len(in); f += frame {
		// true: the VAD said this chunk is speech, so the gain may rise.
		if err := a.Process(in[f:f+frame], out[f:f+frame], true); err != nil {
			fmt.Println(err)
			return
		}
	}
	fmt.Println("input:", dBFS(in), "dBFS")
	fmt.Println("output, first second:", dBFS(out[:rate]), "dBFS")
	fmt.Println("output, third second:", dBFS(out[2*rate:]), "dBFS")
	// Output:
	// input: -40 dBFS
	// output, first second: -33 dBFS
	// output, third second: -24 dBFS
}

// New checks every field, and the error wraps ErrConfig and names the field
// that is out of range.
func ExampleNew() {
	cfg := agc.DefaultConfig(8000)
	cfg.GateLevel = -20 // must be below TargetLevel (-24)
	_, err := agc.New(cfg)
	fmt.Println(errors.Is(err, agc.ErrConfig))
	fmt.Println(err)

	var zero agc.Config // the zero Config is not valid either
	_, err = agc.New(zero)
	fmt.Println(err)
	// Output:
	// true
	// agc: invalid configuration: gate level -20 dBFS
	// agc: invalid configuration: sample rate 0 Hz
}

// DefaultConfig gives the recommended settings for a rate; change single
// fields as needed.
func ExampleDefaultConfig() {
	cfg := agc.DefaultConfig(16000)
	fmt.Printf("%+v\n", cfg)
	// Output:
	// {SampleRate:16000 TargetLevel:-24 LimitLevel:-1 GateLevel:-50 MaxGain:24 MinGain:-12 RiseRate:12 FallRate:40 LevelTime:3s}
}

// A Config for a line with more noise. Stationary noise at -45 dBFS,
// marked as speech as the SpeexDSP VAD marks it, is raised with the
// default gate of -50 dBFS. A gate of -40 dBFS, above the noise, keeps
// the gain at 0 dB.
func ExampleConfig() {
	const rate = 8000
	noise := square(4*rate, 184, rate) // 184 is about -45 dBFS
	for _, gate := range []int{-50, -40} {
		cfg := agc.DefaultConfig(rate)
		cfg.GateLevel = gate
		a, err := agc.New(cfg)
		if err != nil {
			fmt.Println(err)
			return
		}
		out := make([]int16, len(noise))
		if err := a.Process(noise, out, true); err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println("gate", gate, "dBFS: in", dBFS(noise), "dBFS, last second out", dBFS(out[3*rate:]), "dBFS")
	}
	// Output:
	// gate -50 dBFS: in -45 dBFS, last second out -24 dBFS
	// gate -40 dBFS: in -45 dBFS, last second out -45 dBFS
}

// An AGC holds the state of one stream. Frames passed with speech false
// never raise the gain, so pauses are not turned up.
func ExampleAGC() {
	const rate = 8000
	a, err := agc.New(agc.DefaultConfig(rate))
	if err != nil {
		fmt.Println(err)
		return
	}
	in := square(2*rate, 328, rate) // -40 dBFS
	out := make([]int16, len(in))
	// The VAD reported no speech for all of it.
	if err := a.Process(in, out, false); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("not speech: in", dBFS(in), "dBFS, out", dBFS(out[rate:]), "dBFS")
	// Output:
	// not speech: in -40 dBFS, out -40 dBFS
}

// Process takes any number of samples, writes exactly as many, and may
// work in place. A short out is rejected with ErrBufferSize.
func ExampleAGC_Process() {
	a, err := agc.New(agc.DefaultConfig(8000))
	if err != nil {
		fmt.Println(err)
		return
	}
	frame := []int16{100, 200, 300, 400, 500, 600, 700, 800}
	// In place: in and out are the same slice.
	if err := a.Process(frame, frame, true); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(frame) // still inside the 2 ms delay, so zeros

	err = a.Process(make([]int16, 160), make([]int16, 100), true)
	fmt.Println(errors.Is(err, agc.ErrBufferSize))

	var zero agc.AGC // not made by New
	fmt.Println(errors.Is(zero.Process(frame, frame, true), agc.ErrConfig))
	// Output:
	// [0 0 0 0 0 0 0 0]
	// true
	// true
}

// The output is the input delayed by Latency samples, 2 ms; the first
// Latency output samples of a stream are zero.
func ExampleAGC_Latency() {
	for _, rate := range []int{8000, 16000, 48000} {
		a, err := agc.New(agc.DefaultConfig(rate))
		if err != nil {
			fmt.Println(err)
			return
		}
		in := make([]int16, a.Latency()+3)
		for i := range in {
			in[i] = int16(i + 1)
		}
		out := make([]int16, len(in))
		if err := a.Process(in, out, true); err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println(rate, "Hz:", a.Latency(), "samples; out[Latency():] =", out[a.Latency():])
	}
	// Output:
	// 8000 Hz: 16 samples; out[Latency():] = [1 2 3]
	// 16000 Hz: 32 samples; out[Latency():] = [1 2 3]
	// 48000 Hz: 96 samples; out[Latency():] = [1 2 3]
}

// Reset starts a new stream with the same Config: the gain is back at
// 0 dB and the delay line is cleared, so the output equals that of a new
// AGC.
func ExampleAGC_Reset() {
	const rate = 8000
	a, err := agc.New(agc.DefaultConfig(rate))
	if err != nil {
		fmt.Println(err)
		return
	}
	in := square(3*rate, 328, rate)
	first := make([]int16, len(in))
	if err := a.Process(in, first, true); err != nil {
		fmt.Println(err)
		return
	}
	a.Reset()
	second := make([]int16, len(in))
	if err := a.Process(in, second, true); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("same output after Reset:", fmt.Sprint(first) == fmt.Sprint(second))
	// Output:
	// same output after Reset: true
}
