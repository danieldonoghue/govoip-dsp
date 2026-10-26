package preprocess

import (
	"math"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

func TestSupportedFrameSize(t *testing.T) {
	for _, rate := range []int{MinSampleRate, 8000, 48000, MaxSampleRate} {
		for n := -1; n <= MaxFrameSize+2; n++ {
			_, err := New(Config{SampleRate: rate, FrameSize: n})
			if got := SupportedFrameSize(n); got != (err == nil) {
				t.Fatalf("rate %d: SupportedFrameSize(%d) = %v, New error %v", rate, n, got, err)
			}
		}
	}
	// New uses SupportedFrameSize, so pin the bounds independently. For
	// math.MinInt+80, 2*n wraps to 160, which the FFT accepts.
	for n, want := range map[int]bool{
		math.MinInt: false, math.MinInt + 80: false, -1: false, 0: false, 1: true, 17: true, 19: false,
		80: true, 160: true, 320: true, 480: true, 640: true, 960: true,
		MaxFrameSize: true, MaxFrameSize + 1: false, MaxFrameSize + 2: false, math.MaxInt: false,
	} {
		if got := SupportedFrameSize(n); got != want {
			t.Errorf("SupportedFrameSize(%d) = %v, want %v", n, got, want)
		}
	}
}

func TestNoAllocsSupportedFrameSize(t *testing.T) {
	n := 0
	testutil.AssertNoAllocs(t, 100, func() {
		n = (n + 37) % (MaxFrameSize + 3)
		_ = SupportedFrameSize(n)
	})
}
