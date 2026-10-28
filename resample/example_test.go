package resample_test

import (
	"errors"
	"fmt"
	"log"
	"math"

	"github.com/danieldonoghue/govoip-dsp/resample"
)

// Converts one 20 ms frame of 8 kHz telephony audio to 16 kHz. OutLen sizes
// the output so that one call always consumes the whole frame.
func ExampleResampler_Process() {
	const inRate, outRate, frameMs = 8000, 16000, 20

	r, err := resample.New(resample.Config{
		Channels: 1, InRate: inRate, OutRate: outRate, Quality: 5,
	})
	if err != nil {
		log.Fatal(err)
	}
	in := make([]int16, inRate*frameMs/1000) // one 20 ms frame
	out := make([]int16, r.OutLen(len(in)))  // always large enough for one call
	read, written, err := r.Process(in, out)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("read %d of %d frames, wrote %d into a buffer of %d\n", read, len(in), written, len(out))
	// Output:
	// read 160 of 160 frames, wrote 320 into a buffer of 321
}

// Converts interleaved stereo from 48 kHz to 16 kHz in 10 ms frames.
// OutLen counts frames per channel, so the interleaved buffer holds
// OutLen(frames) * channels samples.
func ExampleResampler_Process_stereo() {
	const inRate, outRate, channels, frameMs = 48000, 16000, 2, 10

	r, err := resample.New(resample.Config{
		Channels: channels, InRate: inRate, OutRate: outRate, Quality: 5,
	})
	if err != nil {
		log.Fatal(err)
	}
	frames := inRate * frameMs / 1000    // 480 frames per channel
	in := make([]int16, frames*channels) // 960 interleaved samples
	outLen := r.OutLen(frames)           // frames per channel
	out := make([]int16, outLen*channels)
	read, written, err := r.Process(in, out)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("OutLen(%d) = %d frames per channel, buffer of %d samples\n", frames, outLen, len(out))
	fmt.Printf("read %d frames, wrote %d frames per channel\n", read, written)
	// Output:
	// OutLen(480) = 161 frames per channel, buffer of 322 samples
	// read 480 frames, wrote 160 frames per channel
}

// tone returns n samples of a 1 kHz square wave of amplitude 8000 at rate.
func tone(n, rate int) []int16 {
	x := make([]int16, n)
	for i := range x {
		if i*2000/rate%2 == 0 {
			x[i] = 8000
		} else {
			x[i] = -8000
		}
	}
	return x
}

// The receive path of a call: decoded 8 kHz audio up to a 48 kHz device, in
// 20 ms frames. OutLen sizes the output once; every call then consumes its
// whole input. Output per call varies by a frame around in*OutRate/InRate
// at fractional ratios, but here 48000/8000 is exactly 6.
func Example() {
	r, err := resample.New(resample.Config{
		Channels: 1, InRate: 8000, OutRate: 48000, Quality: resample.QualityVoIP,
	})
	if err != nil {
		log.Fatal(err) // wraps ErrConfig or ErrAllocFailed
	}
	in := make([]int16, 160)                // one 20 ms frame at 8 kHz
	out := make([]int16, r.OutLen(len(in))) // made once, reused every call
	total := 0
	for range 50 { // one second of frames
		read, written, err := r.Process(in, out)
		if err != nil {
			log.Fatal(err)
		}
		if read != len(in) {
			log.Fatal("input left over") // cannot happen with OutLen
		}
		total += written // pass out[:written] to the device
	}
	fmt.Println("buffer", len(out), "samples; one second gave", total)
	// Output:
	// buffer 961 samples; one second gave 48000
}

// New returns an error that wraps ErrConfig for an invalid Config, and
// ErrAllocFailed where the filter would exceed the size limits, such as
// more than 256 channels.
func ExampleNew() {
	_, err := resample.New(resample.Config{Channels: 1, InRate: 8000, OutRate: 16000, Quality: 11})
	fmt.Println(errors.Is(err, resample.ErrConfig), err)
	_, err = resample.New(resample.Config{Channels: 1, InRate: 0, OutRate: 16000})
	fmt.Println(errors.Is(err, resample.ErrConfig), err)
	_, err = resample.New(resample.Config{Channels: 257, InRate: 8000, OutRate: 16000})
	fmt.Println(errors.Is(err, resample.ErrAllocFailed))
	// Output:
	// true resample: invalid argument: quality 11
	// true resample: invalid argument: ratio 0:16000
	// true
}

// An exact ratio: 44.1 kHz to 48 kHz is 147/160. Setting RatioNum and
// RatioDen gives the ratio directly; InRate and OutRate are then only
// reported back by Rate.
func ExampleConfig() {
	r, err := resample.New(resample.Config{
		Channels: 2, RatioNum: 441, RatioDen: 480, InRate: 44100, OutRate: 48000,
		Quality: resample.QualityDesktop,
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	num, den := r.Ratio()
	in, out := r.Rate()
	fmt.Println("ratio", num, den, "rates", in, out, "channels", r.Channels(), "quality", r.Quality())
	// Output:
	// ratio 147 160 rates 44100 48000 channels 2 quality 5
}

// A Resampler keeps its filter history between calls, so cutting a stream
// into chunks of any size gives the same output as one call.
func ExampleResampler() {
	cfg := resample.Config{Channels: 1, InRate: 48000, OutRate: 16000, Quality: resample.QualityVoIP}
	whole, err := resample.New(cfg)
	if err != nil {
		fmt.Println(err)
		return
	}
	chunked, err := resample.New(cfg)
	if err != nil {
		fmt.Println(err)
		return
	}
	x := tone(4800, 48000)
	a := make([]int16, whole.OutLen(len(x)))
	_, na, err := whole.Process(x, a)
	if err != nil {
		fmt.Println(err)
		return
	}
	var b []int16
	buf := make([]int16, 200)
	for i := 0; i < len(x); {
		n := min(37, len(x)-i) // odd chunk sizes
		read, written, err := chunked.Process(x[i:i+n], buf)
		if err != nil {
			fmt.Println(err)
			return
		}
		b = append(b, buf[:written]...)
		i += read
	}
	fmt.Println(na, len(b), fmt.Sprint(a[:na]) == fmt.Sprint(b))
	// Output:
	// 1600 1600 true
}

// Channels is fixed at New.
func ExampleResampler_Channels() {
	r, err := resample.New(resample.Config{Channels: 2, InRate: 48000, OutRate: 16000})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(r.Channels())
	// Output:
	// 2
}

// The latency is half the filter, in input and in output samples; it
// grows with the quality.
func ExampleResampler_InputLatency() {
	for _, q := range []int{resample.QualityMin, resample.QualityVoIP, resample.QualityDefault, resample.QualityMax} {
		r, err := resample.New(resample.Config{Channels: 1, InRate: 8000, OutRate: 16000, Quality: q})
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Printf("quality %d: %d input samples = %.2f ms\n", q, r.InputLatency(), float64(r.InputLatency())*1000/8000)
	}
	// Output:
	// quality 0: 4 input samples = 0.50 ms
	// quality 3: 24 input samples = 3.00 ms
	// quality 4: 32 input samples = 4.00 ms
	// quality 10: 128 input samples = 16.00 ms
}

// OutputLatency is the same delay as InputLatency, in output samples.
func ExampleResampler_OutputLatency() {
	r, err := resample.New(resample.Config{Channels: 1, InRate: 48000, OutRate: 16000, Quality: resample.QualityVoIP})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(r.InputLatency(), "input samples,", r.OutputLatency(), "output samples")
	// Output:
	// 72 input samples, 24 output samples
}

// OutLen gives the output space one call needs to consume n input frames.
// From 8000 to 11025 Hz, 20 ms gives 220.5 output frames, so the count
// written, and OutLen, differ from call to call.
func ExampleResampler_OutLen() {
	r, err := resample.New(resample.Config{Channels: 1, InRate: 8000, OutRate: 11025, Quality: resample.QualityVoIP})
	if err != nil {
		fmt.Println(err)
		return
	}
	in := make([]int16, 160) // 20 ms at 8 kHz
	out := make([]int16, 1024)
	for range 3 {
		n := r.OutLen(len(in))
		read, written, err := r.Process(in, out[:n])
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println("OutLen", n, "read", read, "wrote", written)
	}
	fmt.Println("OutLen(0) =", r.OutLen(0))
	// Output:
	// OutLen 222 read 160 wrote 221
	// OutLen 221 read 160 wrote 220
	// OutLen 222 read 160 wrote 221
	// OutLen(0) = 0
}

// With out too short, a call stops when out is full. The unread input
// must be offered again in a later call.
func ExampleResampler_Process_shortOutput() {
	r, err := resample.New(resample.Config{Channels: 1, InRate: 8000, OutRate: 16000, Quality: resample.QualityVoIP})
	if err != nil {
		fmt.Println(err)
		return
	}
	in := tone(160, 8000)
	out := make([]int16, 100) // OutLen(160) would be 321
	for len(in) > 0 {
		read, written, err := r.Process(in, out)
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println("read", read, "wrote", written)
		in = in[read:] // offer the rest again
	}
	// Output:
	// read 50 wrote 100
	// read 50 wrote 100
	// read 50 wrote 100
	// read 10 wrote 20
}

// Interleaved buffers must hold whole frames: a length that is not a
// multiple of Channels is rejected with ErrBufferSize.
func ExampleResampler_Process_errors() {
	r, err := resample.New(resample.Config{Channels: 2, InRate: 8000, OutRate: 16000})
	if err != nil {
		fmt.Println(err)
		return
	}
	_, _, err = r.Process(make([]int16, 3), make([]int16, 8))
	fmt.Println(errors.Is(err, resample.ErrBufferSize))

	var zero resample.Resampler // not made by New
	_, _, err = zero.Process(nil, nil)
	fmt.Println(errors.Is(err, resample.ErrConfig))
	// Output:
	// true
	// true
}

// float32 samples are on the 16-bit scale, not [-1, 1]. Input saturates
// to int16 and truncates toward zero, and NaN becomes 0, so this input
// gives exactly the output of the int16 input beside it. Output values
// are copies of the int16 results.
func ExampleResampler_ProcessFloat() {
	cfg := resample.Config{Channels: 1, InRate: 8000, OutRate: 16000, Quality: resample.QualityVoIP}
	rf, err := resample.New(cfg)
	if err != nil {
		fmt.Println(err)
		return
	}
	ri, err := resample.New(cfg)
	if err != nil {
		fmt.Println(err)
		return
	}
	nan, inf := float32(math.NaN()), float32(math.Inf(1))
	inF := []float32{1000.9, -1000.9, 40000, -inf, nan, 0.5, -0.5, 7}
	inI := []int16{1000, -1000, 32767, -32768, 0, 0, 0, 7}
	outF := make([]float32, rf.OutLen(len(inF)))
	outI := make([]int16, ri.OutLen(len(inI)))
	_, nf, err := rf.ProcessFloat(inF, outF)
	if err != nil {
		fmt.Println(err)
		return
	}
	_, ni, err := ri.Process(inI, outI)
	if err != nil {
		fmt.Println(err)
		return
	}
	same := nf == ni
	for k := range ni {
		same = same && outF[k] == float32(outI[k])
	}
	fmt.Println("wrote", nf, "samples, same as int16:", same)
	// Output:
	// wrote 16 samples, same as int16: true
}

// ProcessChannel resamples one channel of a buffer through strides, so
// channels can advance independently. Here it takes the left channel of
// an interleaved stereo frame into a mono buffer.
func ExampleResampler_ProcessChannel() {
	r, err := resample.New(resample.Config{Channels: 2, InRate: 16000, OutRate: 8000, Quality: resample.QualityVoIP})
	if err != nil {
		fmt.Println(err)
		return
	}
	stereo := make([]int16, 2*320) // 20 ms of interleaved L R L R ...
	left := make([]int16, r.OutLen(320))
	read, written, err := r.ProcessChannel(0, stereo, 2, left, 1)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("left: read", read, "wrote", written)
	_, _, err = r.ProcessChannel(2, stereo, 2, left, 1)
	fmt.Println(errors.Is(err, resample.ErrChannel))
	_, _, err = r.ProcessChannel(1, stereo, 0, left, 1)
	fmt.Println(errors.Is(err, resample.ErrStride))
	// Output:
	// left: read 320 wrote 160
	// true
	// true
}

// ProcessChannelFloat is ProcessChannel for float32 samples on the 16-bit
// scale.
func ExampleResampler_ProcessChannelFloat() {
	r, err := resample.New(resample.Config{Channels: 1, InRate: 8000, OutRate: 16000, Quality: resample.QualityVoIP})
	if err != nil {
		fmt.Println(err)
		return
	}
	in := make([]float32, 160)
	out := make([]float32, r.OutLen(len(in)))
	read, written, err := r.ProcessChannelFloat(0, in, 1, out, 1)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("read", read, "wrote", written)
	// Output:
	// read 160 wrote 320
}

// Quality reads back the quality level; QualityDefault is the SpeexDSP
// default, QualityVoIP is enough for speech.
func ExampleResampler_Quality() {
	r, err := resample.New(resample.Config{Channels: 1, InRate: 8000, OutRate: 16000, Quality: resample.QualityDefault})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(r.Quality())
	// Output:
	// 4
}

// SetQuality rebuilds the filter on the fly and keeps the stream; the
// latency changes with it.
func ExampleResampler_SetQuality() {
	r, err := resample.New(resample.Config{Channels: 1, InRate: 8000, OutRate: 16000, Quality: resample.QualityVoIP})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(r.Quality(), r.InputLatency())
	if err := r.SetQuality(resample.QualityMax); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(r.Quality(), r.InputLatency())
	fmt.Println(errors.Is(r.SetQuality(-1), resample.ErrConfig))
	// Output:
	// 3 24
	// 10 128
	// true
}

// Rate reads back the rates last set.
func ExampleResampler_Rate() {
	r, err := resample.New(resample.Config{Channels: 1, InRate: 48000, OutRate: 16000})
	if err != nil {
		fmt.Println(err)
		return
	}
	in, out := r.Rate()
	fmt.Println(in, out)
	// Output:
	// 48000 16000
}

// Ratio is the ratio in lowest terms.
func ExampleResampler_Ratio() {
	r, err := resample.New(resample.Config{Channels: 1, InRate: 48000, OutRate: 16000})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(r.Ratio())
	// Output:
	// 3 1
}

// SetRate changes the rates on the fly with the reference semantics: the
// stream position and history are kept. Setting the same rates does
// nothing.
func ExampleResampler_SetRate() {
	r, err := resample.New(resample.Config{Channels: 1, InRate: 48000, OutRate: 16000})
	if err != nil {
		fmt.Println(err)
		return
	}
	if err := r.SetRate(48000, 8000); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(r.Ratio())
	fmt.Println(errors.Is(r.SetRate(0, 8000), resample.ErrConfig))
	// Output:
	// 6 1
	// true
}

// SetRateFrac sets an exact ratio. A change that would overflow the
// fractional position returns ErrOverflow and changes nothing.
func ExampleResampler_SetRateFrac() {
	r, err := resample.New(resample.Config{
		Channels: 1, RatioNum: 1000003, RatioDen: 999983, InRate: 48000, OutRate: 48000,
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	in := make([]int16, 7001)
	if _, _, err := r.Process(in, make([]int16, r.OutLen(len(in)))); err != nil {
		fmt.Println(err)
		return
	}
	err = r.SetRateFrac(999983, 1000003, 1, 2)
	fmt.Println(errors.Is(err, resample.ErrOverflow))
	fmt.Println(r.Ratio())
	if err := r.SetRateFrac(147, 160, 44100, 48000); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(r.Ratio())
	// Output:
	// true
	// 1000003 999983
	// 147 160
}

// SkipZeros drops the filter delay at the start of a stream: without it,
// the first OutputLatency output samples are the filter warming up.
func ExampleResampler_SkipZeros() {
	for _, skip := range []bool{false, true} {
		r, err := resample.New(resample.Config{Channels: 1, InRate: 8000, OutRate: 16000, Quality: resample.QualityVoIP})
		if err != nil {
			fmt.Println(err)
			return
		}
		if skip {
			r.SkipZeros() // before the first process call
		}
		in := tone(160, 8000)
		out := make([]int16, r.OutLen(len(in)))
		_, written, err := r.Process(in, out)
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println("skip", skip, "wrote", written, "first samples", out[:3])
	}
	// Output:
	// skip false wrote 320 first samples [0 0 0]
	// skip true wrote 272 first samples [8013 9582 7960]
}

// Reset restarts the stream and keeps the rates and quality. For a mono
// stream the output then equals that of a new Resampler.
func ExampleResampler_Reset() {
	r, err := resample.New(resample.Config{Channels: 1, InRate: 8000, OutRate: 16000, Quality: resample.QualityVoIP})
	if err != nil {
		fmt.Println(err)
		return
	}
	in := tone(160, 8000)
	first := make([]int16, r.OutLen(len(in)))
	if _, _, err := r.Process(in, first); err != nil {
		fmt.Println(err)
		return
	}
	r.Reset()
	second := make([]int16, r.OutLen(len(in)))
	if _, _, err := r.Process(in, second); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("same output:", fmt.Sprint(first) == fmt.Sprint(second))
	// Output:
	// same output: true
}
