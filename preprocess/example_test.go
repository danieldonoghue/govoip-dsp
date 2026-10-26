package preprocess_test

import (
	"fmt"

	"github.com/danieldonoghue/govoip-dsp/preprocess"
)

// noisyTone returns n samples: silence until noiseFrom, then noise, with a
// 500 Hz square tone from toneFrom on. Integer arithmetic only.
func noisyTone(n, noiseFrom, toneFrom, rate int) []int16 {
	x := make([]int16, n)
	seed := uint32(1)
	for i := range x {
		seed = seed*1664525 + 1013904223
		var v int32
		if i >= noiseFrom {
			v = int32(seed>>22) - 512 // noise in [-512, 511]
		}
		if i >= toneFrom {
			if i*1000/rate%2 == 0 {
				v += 8000
			} else {
				v -= 8000
			}
		}
		x[i] = int16(v)
	}
	return x
}

func meanAbs(x []int16) int {
	sum := 0
	for _, v := range x {
		sum += max(int(v), -int(v))
	}
	return sum / len(x)
}

// Denoising and voice activity detection on 20 ms frames at 8 kHz.
func ExamplePreprocessor_Run() {
	const rate = 8000
	frameSize := rate * 20 / 1000
	if !preprocess.SupportedFrameSize(frameSize) {
		fmt.Println("unsupported frame size", frameSize)
		return
	}
	p, err := preprocess.New(preprocess.Config{SampleRate: rate, FrameSize: frameSize})
	if err != nil {
		fmt.Println(err)
		return
	}
	p.SetVAD(true) // denoise is on by default

	// 1 s of silence, 3 s of noise, then 1 s of tone in noise.
	x := noisyTone(5*rate, rate, 4*rate, rate)
	noiseIn := meanAbs(x[3*rate : 4*rate])
	var speech [3]int // frames reported as speech per part
	for f := 0; f+frameSize <= len(x); f += frameSize {
		isSpeech, err := p.Run(x[f : f+frameSize]) // in place
		if err != nil {
			fmt.Println(err)
			return
		}
		part := 1
		switch {
		case f < rate:
			part = 0
		case f >= 4*rate:
			part = 2
		}
		if isSpeech {
			speech[part]++
		}
	}
	// The VAD tells silence from signal, but like the reference it also
	// flags stationary noise as speech (docs/algorithms.md).
	fmt.Println("frame size:", frameSize)
	fmt.Println("noise level in the 4th second, in and out:", noiseIn, meanAbs(x[3*rate:4*rate]))
	fmt.Println("speech frames, silence, noise and tone:", speech[0], speech[1], speech[2])
	// Output:
	// frame size: 160
	// noise level in the 4th second, in and out: 256 56
	// speech frames, silence, noise and tone: 0 150 50
}

// Residual-echo suppression: each frame goes with the residual-echo
// spectrum of the echo canceller frame that produced it, here a stand-in.
func ExamplePreprocessor_RunEcho() {
	const rate, frameSize = 16000, 320
	withEcho, err := preprocess.New(preprocess.Config{SampleRate: rate, FrameSize: frameSize})
	if err != nil {
		fmt.Println(err)
		return
	}
	plain, err := preprocess.New(preprocess.Config{SampleRate: rate, FrameSize: frameSize})
	if err != nil {
		fmt.Println(err)
		return
	}
	// echo.Canceller.Residual writes FrameSize()+1 values; RunEcho reads
	// the first FrameSize().
	residual := make([]int32, frameSize+1)
	for i := range residual {
		residual[i] = 1 << 20
	}
	a := noisyTone(2*rate, 0, rate, rate)
	b := append([]int16(nil), a...)
	for f := 0; f+frameSize <= len(a); f += frameSize {
		if _, err := withEcho.RunEcho(a[f:f+frameSize], residual); err != nil {
			fmt.Println(err)
			return
		}
		if _, err := plain.Run(b[f : f+frameSize]); err != nil {
			fmt.Println(err)
			return
		}
	}
	fmt.Println("tone level, Run and RunEcho:", meanAbs(b[3*rate/2:]), meanAbs(a[3*rate/2:]))
	_, err = withEcho.RunEcho(a[:frameSize], residual[:10])
	fmt.Println("short residual:", err)
	// Output:
	// tone level, Run and RunEcho: 6171 994
	// short residual: preprocess: buffer length does not match the frame size
}
