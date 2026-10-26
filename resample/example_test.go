package resample_test

import (
	"fmt"
	"log"

	"github.com/danieldonoghue/govoip-dsp/resample"
)

// Converts one 20 ms frame of 8 kHz telephony audio to 16 kHz. MaxOut sizes
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
	out := make([]int16, r.MaxOut(len(in)))  // always large enough for one call
	read, written, err := r.Process(in, out)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("read %d of %d frames, wrote %d into a buffer of %d\n", read, len(in), written, len(out))
	// Output:
	// read 160 of 160 frames, wrote 320 into a buffer of 321
}

// Converts interleaved stereo from 48 kHz to 16 kHz in 10 ms frames.
// MaxOut counts frames per channel, so the interleaved buffer holds
// MaxOut(frames) * channels samples.
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
	maxOut := r.MaxOut(frames)           // frames per channel
	out := make([]int16, maxOut*channels)
	read, written, err := r.Process(in, out)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("MaxOut(%d) = %d frames per channel, buffer of %d samples\n", frames, maxOut, len(out))
	fmt.Printf("read %d frames, wrote %d frames per channel\n", read, written)
	// Output:
	// MaxOut(480) = 161 frames per channel, buffer of 322 samples
	// read 480 frames, wrote 160 frames per channel
}
