package preprocess_test

import (
	"errors"
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

// newExample returns a Preprocessor for 20 ms frames at 8 kHz.
func newExample() *preprocess.Preprocessor {
	p, err := preprocess.New(preprocess.Config{SampleRate: 8000, FrameSize: 160})
	if err != nil {
		panic(err) // cannot happen: the Config is valid
	}
	return p
}

// The life of a Preprocessor: New with the rate and frame size, settings,
// then Run on each frame in place, and Reset for a new stream.
func Example() {
	const rate, frameMs = 16000, 20
	frameSize := rate * frameMs / 1000 // 320 samples
	p, err := preprocess.New(preprocess.Config{SampleRate: rate, FrameSize: frameSize})
	if err != nil {
		fmt.Println(err)
		return
	}
	p.SetVAD(true) // report speech per frame; denoise is on by default

	x := noisyTone(3*rate, 0, 2*rate, rate) // 2 s of noise, then a tone in it
	speech := 0
	for f := 0; f+frameSize <= len(x); f += frameSize {
		isSpeech, err := p.Run(x[f : f+frameSize])
		if err != nil {
			fmt.Println(err) // ErrBufferSize: the frame has the wrong length
			return
		}
		if isSpeech {
			speech++
		}
	}
	fmt.Println("frames:", len(x)/frameSize, "speech:", speech)
	p.Reset() // the next call starts a new stream with the same settings
	// Output:
	// frames: 150 speech: 150
}

// Check a frame size before New, for example one computed from a frame
// duration. The FFT of 2*n samples must exist: n may have no prime factor
// above 17.
func ExampleSupportedFrameSize() {
	// 161 = 7·23 and 2·2049 = 2·3·683: prime factors above 17.
	for _, n := range []int{160, 441, 882, 1000, 161, 2049} {
		fmt.Println(n, preprocess.SupportedFrameSize(n))
	}
	fmt.Println("largest:", preprocess.MaxFrameSize)
	// Output:
	// 160 true
	// 441 true
	// 882 true
	// 1000 true
	// 161 false
	// 2049 false
	// largest: 2048
}

// New rejects a frame size without an FFT and a rate out of range, with
// an error that wraps ErrConfig.
func ExampleNew() {
	_, err := preprocess.New(preprocess.Config{SampleRate: 8000, FrameSize: 23 * 7})
	fmt.Println(errors.Is(err, preprocess.ErrConfig))
	fmt.Println(err)
	_, err = preprocess.New(preprocess.Config{SampleRate: preprocess.MaxSampleRate + 1, FrameSize: 160})
	fmt.Println(err)
	_, err = preprocess.New(preprocess.Config{SampleRate: preprocess.MinSampleRate - 1, FrameSize: 160})
	fmt.Println(err)
	// Output:
	// true
	// preprocess: invalid configuration: frame size 161 (want 1 to 2048 with no prime factor above 17)
	// preprocess: invalid configuration: sample rate 65536 Hz (want 4 to 65535)
	// preprocess: invalid configuration: sample rate 3 Hz (want 4 to 65535)
}

// A Config from a rate and a frame duration.
func ExampleConfig() {
	const rate, frameMs = 48000, 10
	cfg := preprocess.Config{SampleRate: rate, FrameSize: rate * frameMs / 1000}
	p, err := preprocess.New(cfg)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(p.SampleRate(), "Hz,", p.FrameSize(), "samples per frame")
	// Output:
	// 48000 Hz, 480 samples per frame
}

// A Preprocessor holds one stream's state. Its output is delayed by one
// frame: the first frame out is silence, and the input comes out a frame
// later. With denoise off, the analysis and synthesis still round, so a
// sample may differ from the input by one.
func ExamplePreprocessor() {
	p := newExample()
	p.SetDenoise(false) // pass the signal through the analysis unchanged
	frame := make([]int16, p.FrameSize())
	for i := range frame {
		frame[i] = 1000
	}
	first := append([]int16(nil), frame...)
	if _, err := p.Run(first); err != nil {
		fmt.Println(err)
		return
	}
	second := append([]int16(nil), frame...)
	if _, err := p.Run(second); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("first frame out, first samples:", first[:3])
	fmt.Println("second frame out, middle sample:", second[80])
	// Output:
	// first frame out, first samples: [0 0 0]
	// second frame out, middle sample: 999
}

func ExamplePreprocessor_FrameSize() {
	p := newExample()
	fmt.Println(p.FrameSize())
	// Output:
	// 160
}

func ExamplePreprocessor_SampleRate() {
	p := newExample()
	fmt.Println(p.SampleRate())
	// Output:
	// 8000
}

// Denoise reads the setting back; it is on after New.
func ExamplePreprocessor_Denoise() {
	p := newExample()
	fmt.Println(p.Denoise())
	// Output:
	// true
}

// With denoise off, the VAD still works: a VAD-only preprocessor.
func ExamplePreprocessor_SetDenoise() {
	p := newExample()
	p.SetDenoise(false)
	p.SetVAD(true)
	fmt.Println("denoise", p.Denoise(), "vad", p.VAD())
	// Output:
	// denoise false vad true
}

// VAD reads the setting back; it is off after New.
func ExamplePreprocessor_VAD() {
	p := newExample()
	fmt.Println(p.VAD())
	// Output:
	// false
}

// With the VAD off, Run reports speech for every frame, even silence. With
// it on, silence is not speech.
func ExamplePreprocessor_SetVAD() {
	for _, on := range []bool{false, true} {
		p := newExample()
		p.SetVAD(on)
		speech := 0
		for range 50 {
			isSpeech, err := p.Run(make([]int16, p.FrameSize()))
			if err != nil {
				fmt.Println(err)
				return
			}
			if isSpeech {
				speech++
			}
		}
		fmt.Println("VAD", on, "speech frames in 1 s of silence:", speech)
	}
	// Output:
	// VAD false speech frames in 1 s of silence: 50
	// VAD true speech frames in 1 s of silence: 0
}

// SpeechProb is the score behind the VAD decision of the last frame, in
// percent. It is a heuristic, not a calibrated probability: here it is
// above 0 on silence and high on stationary noise.
func ExamplePreprocessor_SpeechProb() {
	p := newExample()
	p.SetVAD(true)
	x := noisyTone(2*8000, 8000, 2*8000, 8000) // 1 s of silence, 1 s of noise
	for f := 0; f+160 <= len(x); f += 160 {
		if _, err := p.Run(x[f : f+160]); err != nil {
			fmt.Println(err)
			return
		}
		if f == 8000-160 || f == len(x)-160 {
			fmt.Println("after", (f+160)/8, "ms:", p.SpeechProb(), "%")
		}
	}
	// Output:
	// after 1000 ms: 12 %
	// after 2000 ms: 92 %
}

// ProbStart reads back the threshold that starts speech, 35 after New.
// The conversion truncates, as the reference does, so a value set with
// SetProbStart(35) reads back as 34.
func ExamplePreprocessor_ProbStart() {
	p := newExample()
	fmt.Println(p.ProbStart())
	p.SetProbStart(35)
	fmt.Println(p.ProbStart())
	// Output:
	// 35
	// 34
}

// A higher start threshold makes the VAD slower to report speech. Values
// are clamped to 0 to 100.
func ExamplePreprocessor_SetProbStart() {
	p := newExample()
	p.SetProbStart(60)
	fmt.Println(p.ProbStart())
	p.SetProbStart(150)
	fmt.Println(p.ProbStart())
	// Output:
	// 59
	// 99
}

// ProbContinue reads back the threshold that keeps speech going; it is 20
// after New.
func ExamplePreprocessor_ProbContinue() {
	p := newExample()
	fmt.Println(p.ProbContinue())
	// Output:
	// 20
}

func ExamplePreprocessor_SetProbContinue() {
	p := newExample()
	p.SetProbContinue(10)
	fmt.Println(p.ProbContinue())
	p.SetProbContinue(-5) // clamped to 0
	fmt.Println(p.ProbContinue())
	// Output:
	// 9
	// 0
}

// NoiseSuppress reads back the maximum noise attenuation in dB, stored as
// a negative number.
func ExamplePreprocessor_NoiseSuppress() {
	p := newExample()
	fmt.Println(p.NoiseSuppress())
	// Output:
	// -15
}

// More suppression lowers stationary noise further, at more risk to the
// speech. The sign is ignored: 25 and -25 both mean 25 dB.
func ExamplePreprocessor_SetNoiseSuppress() {
	for _, dB := range []int32{-15, 25} {
		p := newExample()
		p.SetNoiseSuppress(dB)
		x := noisyTone(4*8000, 0, 4*8000, 8000) // 4 s of noise
		for f := 0; f+160 <= len(x); f += 160 {
			if _, err := p.Run(x[f : f+160]); err != nil {
				fmt.Println(err)
				return
			}
		}
		fmt.Println("suppress", p.NoiseSuppress(), "dB: noise out", meanAbs(x[3*8000:]))
	}
	// Output:
	// suppress -15 dB: noise out 52
	// suppress -25 dB: noise out 24
}

// EchoSuppress reads back the residual-echo attenuation without near-end
// speech.
func ExamplePreprocessor_EchoSuppress() {
	p := newExample()
	fmt.Println(p.EchoSuppress())
	// Output:
	// -40
}

func ExamplePreprocessor_SetEchoSuppress() {
	p := newExample()
	p.SetEchoSuppress(50)
	fmt.Println(p.EchoSuppress())
	// Output:
	// -50
}

// EchoSuppressActive reads back the residual-echo attenuation during
// near-end speech.
func ExamplePreprocessor_EchoSuppressActive() {
	p := newExample()
	fmt.Println(p.EchoSuppressActive())
	// Output:
	// -15
}

func ExamplePreprocessor_SetEchoSuppressActive() {
	p := newExample()
	p.SetEchoSuppressActive(-20)
	fmt.Println(p.EchoSuppressActive())
	// Output:
	// -20
}

// EstimateUpdate learns the noise from a frame without producing output,
// for frames whose output is not needed. It does not change the frame.
func ExamplePreprocessor_EstimateUpdate() {
	p := newExample()
	x := noisyTone(8000, 0, 8000, 8000) // 1 s of noise
	before := fmt.Sprint(x)
	for f := 0; f+160 <= len(x); f += 160 {
		if err := p.EstimateUpdate(x[f : f+160]); err != nil {
			fmt.Println(err)
			return
		}
	}
	fmt.Println("frame unchanged:", fmt.Sprint(x) == before)
	err := p.EstimateUpdate(x[:100])
	fmt.Println(errors.Is(err, preprocess.ErrBufferSize))
	// Output:
	// frame unchanged: true
	// true
}

// PSDLen is the length of the spectra PSD and NoisePSD write: FrameSize.
func ExamplePreprocessor_PSDLen() {
	p := newExample()
	fmt.Println(p.PSDLen())
	// Output:
	// 160
}

// PSD gives the power spectrum of the last frame, one value per bin. Bin i
// is centred on i*SampleRate/(2*FrameSize) Hz, here 25 Hz per bin.
func ExamplePreprocessor_PSD() {
	p := newExample()
	x := noisyTone(8000, 8000, 0, 8000) // 1 s of a 500 Hz square tone
	for f := 0; f+160 <= len(x); f += 160 {
		if _, err := p.Run(x[f : f+160]); err != nil {
			fmt.Println(err)
			return
		}
	}
	psd := make([]int32, p.PSDLen())
	if err := p.PSD(psd); err != nil {
		fmt.Println(err)
		return
	}
	peak := 0
	for i := range psd {
		if psd[i] > psd[peak] {
			peak = i
		}
	}
	fmt.Println("strongest bin:", peak, "=", peak*8000/(2*160), "Hz")
	fmt.Println(errors.Is(p.PSD(psd[:10]), preprocess.ErrBufferSize))
	// Output:
	// strongest bin: 20 = 500 Hz
	// true
}

// NoisePSD gives the current noise estimate in the same bins as PSD. It
// starts flat at 1 and learns the noise.
func ExamplePreprocessor_NoisePSD() {
	p := newExample()
	noise := make([]int32, p.PSDLen())
	if err := p.NoisePSD(noise); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("after New:", noise[10], noise[80], noise[150])
	x := noisyTone(3*8000, 0, 3*8000, 8000)
	for f := 0; f+160 <= len(x); f += 160 {
		if _, err := p.Run(x[f : f+160]); err != nil {
			fmt.Println(err)
			return
		}
	}
	if err := p.NoisePSD(noise); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("after 3 s of noise:", noise[10], noise[80], noise[150])
	// Output:
	// after New: 1 1 1
	// after 3 s of noise: 134 132 79
}

// Reset starts a new stream and keeps the settings: the output then equals
// that of a new Preprocessor with the same settings.
func ExamplePreprocessor_Reset() {
	p := newExample()
	p.SetVAD(true)
	p.SetNoiseSuppress(20)
	x := noisyTone(8000, 0, 4000, 8000)
	run := func(p *preprocess.Preprocessor) string {
		y := append([]int16(nil), x...)
		for f := 0; f+160 <= len(y); f += 160 {
			if _, err := p.Run(y[f : f+160]); err != nil {
				return err.Error()
			}
		}
		return fmt.Sprint(y)
	}
	first := run(p)
	p.Reset()
	fmt.Println("same as the first stream:", run(p) == first)
	fmt.Println("settings kept:", p.VAD(), p.NoiseSuppress())

	var zero preprocess.Preprocessor // not made by New
	_, err := zero.Run(x[:160])
	fmt.Println(errors.Is(err, preprocess.ErrConfig))
	// Output:
	// same as the first stream: true
	// settings kept: true -20
	// true
}
