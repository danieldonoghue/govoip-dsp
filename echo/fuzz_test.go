package echo

import (
	"slices"
	"testing"
)

var (
	fuzzFrames = []int{1, 2, 8, 17, 80, 160, 320, 480}
	fuzzRates  = []int{8000, 11025, 16000, 32000, 32767, 32768, 44100, 48000}
)

// FuzzCanceller drives random configurations, call sequences and sample
// values. It checks that nothing panics, that wrong lengths give
// ErrBufferSize, the divisor invariants of docs/algorithms.md, and that a
// Capture with an empty queue copies mic to out.
func FuzzCanceller(f *testing.F) {
	f.Add([]byte{4, 7, 0, 0, 0, 0, 0, 2, 2, 1, 2, 3, 4, 5, 6})
	f.Add([]byte{5, 30, 7, 0x80, 0x00, 0xff, 0x7f, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{0, 0, 3, 1, 2, 2, 2, 2, 1, 1, 1, 1, 0, 0, 6})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 3 {
			return
		}
		fs := fuzzFrames[int(data[0])%len(fuzzFrames)]
		filter := 1 + int(data[1])*fs/8
		rate := fuzzRates[int(data[2])%len(fuzzRates)]
		c, err := New(Config{FrameSize: fs, FilterLength: filter, SampleRate: min(rate, MaxSampleRate)})
		if err != nil {
			t.Fatal(err)
		}
		data = data[3:]
		mic, far, out := make([]int16, fs), make([]int16, fs), make([]int16, fs)
		res := make([]int32, c.ResidualLen())
		ir := make([]int32, c.ImpulseResponseLen())
		pos := 0
		fill := func(x []int16, mode byte) {
			for i := range x {
				switch mode % 4 {
				case 0: // samples from the input
					if pos+1 < len(data) {
						x[i] = int16(uint16(data[pos]) | uint16(data[pos+1])<<8)
						pos += 2
					} else {
						x[i] = 0
					}
				case 1:
					x[i] = -32768
				case 2:
					x[i] = int16(32767 - (i%2)*65535)
				default:
					x[i] = 0
				}
			}
		}
		for steps := 0; pos < len(data) && steps < 400; steps++ {
			op := data[pos]
			pos++
			fill(mic, op>>3)
			fill(far, op>>5)
			switch op % 8 {
			case 0, 1:
				err = c.Cancel(mic, far, out)
			case 2:
				err = c.Playback(far)
			case 3:
				empty := c.st.playBufPos < fs
				err = c.Capture(mic, out)
				if empty && !slices.Equal(out, mic) {
					t.Fatal("Capture with an empty queue did not copy mic")
				}
			case 4:
				c.Reset()
			case 5:
				err = c.SetSampleRate(fuzzRates[int(op>>3)%len(fuzzRates)])
				if err != nil && fuzzRates[int(op>>3)%len(fuzzRates)] <= MaxSampleRate {
					t.Fatal(err)
				}
				err = nil
			case 6:
				if err = c.Residual(res); err == nil {
					err = c.ImpulseResponse(ir)
				}
			default:
				if c.Cancel(mic[:fs-1], far, out) != ErrBufferSize || c.Playback(far[:0]) != ErrBufferSize ||
					c.Capture(mic, out[:fs-1]) != ErrBufferSize || c.Residual(res[:fs]) != ErrBufferSize {
					t.Fatal("wrong length not rejected")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			for i := range c.st.power {
				if c.st.power[i] < 0 || c.st.Xf[i] < 0 {
					t.Fatalf("power[%d] = %d, Xf = %d", i, c.st.power[i], c.st.Xf[i])
				}
			}
		}
	})
}
