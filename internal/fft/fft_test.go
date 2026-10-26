package fft

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

// largestPrimeFactor is an independent check of the kf_factor rule.
func largestPrimeFactor(n int) int {
	largest := 1
	for p := 2; p*p <= n; p++ {
		for n%p == 0 {
			largest = p
			n /= p
		}
	}
	return max(largest, n)
}

func TestSupported(t *testing.T) {
	count := 0
	for n := -1; n <= MaxSize+4; n++ {
		want := n >= 2 && n <= MaxSize && n%2 == 0 && largestPrimeFactor(n/2) <= 17
		if got := Supported(n); got != want {
			t.Errorf("Supported(%d) = %v, want %v", n, got, want)
		}
		_, err := New(Config{Size: n})
		if want != (err == nil) {
			t.Errorf("New(%d): error %v, want supported %v", n, err, want)
		}
		if err != nil && !errors.Is(err, ErrConfig) {
			t.Errorf("New(%d): error %v does not wrap ErrConfig", n, err)
		}
		if want {
			count++
		}
	}
	// The sweep fixture records the same count from the C rule.
	if count != 427 {
		t.Errorf("%d supported sizes, want 427", count)
	}
	for _, n := range []int{math.MinInt, -4096, 0, 8192, 1 << 20, math.MaxInt} {
		if _, err := New(Config{Size: n}); !errors.Is(err, ErrConfig) {
			t.Errorf("New(%d): error %v, want ErrConfig", n, err)
		}
	}
}

func TestBufferSize(t *testing.T) {
	const n = 160
	f, err := New(Config{Size: n})
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range []int{0, 1, n - 2, n - 1, n + 1, 2 * n} {
		for _, inIsBad := range []bool{true, false} {
			inLen, outLen := n, n
			if inIsBad {
				inLen = l
			} else {
				outLen = l
			}
			in := testutil.WhiteNoise(inLen, 1000, 1)
			out := slices.Repeat([]int16{7}, outLen)
			in0, out0 := slices.Clone(in), slices.Clone(out)
			if err := f.Forward(in, out); err != ErrBufferSize {
				t.Errorf("Forward(len %d, len %d) = %v, want ErrBufferSize", inLen, outLen, err)
			}
			if err := f.Inverse(in, out); err != ErrBufferSize {
				t.Errorf("Inverse(len %d, len %d) = %v, want ErrBufferSize", inLen, outLen, err)
			}
			if !slices.Equal(in, in0) || !slices.Equal(out, out0) {
				t.Errorf("len %d, len %d: buffers changed on error", inLen, outLen)
			}
		}
	}
	if err := f.Forward(nil, nil); err != ErrBufferSize {
		t.Errorf("Forward(nil, nil) = %v, want ErrBufferSize", err)
	}
}

func TestZeroValue(t *testing.T) {
	var f FFT
	if err := f.Forward(nil, nil); err != ErrConfig {
		t.Errorf("zero FFT Forward = %v, want ErrConfig", err)
	}
	if err := f.Inverse(nil, nil); err != ErrConfig {
		t.Errorf("zero FFT Inverse = %v, want ErrConfig", err)
	}
}

// TestStateless checks that a call does not depend on earlier calls: an
// instance that has transformed other data gives a fresh instance's result.
func TestStateless(t *testing.T) {
	for _, n := range mvpSizes {
		x := signal("noise", n)
		fresh, _ := New(Config{Size: n})
		used, _ := New(Config{Size: n})
		scratch := make([]int16, n)
		for _, s := range []string{"dc", "min", "square", "sine_16000"} {
			in := signal(s, n)
			_ = used.Forward(in, scratch)
			_ = used.Inverse(signal(s, n), scratch)
		}
		for _, dir := range []string{"forward", "inverse"} {
			a, b := slices.Clone(x), slices.Clone(x)
			oa, ob := make([]int16, n), make([]int16, n)
			if dir == "forward" {
				_ = fresh.Forward(a, oa)
				_ = used.Forward(b, ob)
			} else {
				_ = fresh.Inverse(a, oa)
				_ = used.Inverse(b, ob)
			}
			if !slices.Equal(oa, ob) || !slices.Equal(a, b) {
				t.Errorf("size %d %s: result depends on earlier calls", n, dir)
			}
		}
	}
}

// TestInputAfterForward checks the documented effect of Forward on its
// input: restored exactly unless the input holds -32768.
func TestInputAfterForward(t *testing.T) {
	for _, n := range mvpSizes {
		f, _ := New(Config{Size: n})
		out := make([]int16, n)
		for _, s := range signals {
			x := signal(s, n)
			in := slices.Clone(x)
			_ = f.Forward(in, out)
			if !slices.Contains(x, -32768) && !slices.Equal(in, x) {
				t.Errorf("size %d %s: input changed although it holds no -32768", n, s)
			}
		}
		// The square wave ends in -32768, so maximize_range shifts by one
		// with a wrapped peak and 32767 comes back as -1.
		in := signal("square", n)
		_ = f.Forward(in, out)
		if in[0] != -1 {
			t.Errorf("size %d square: input[0] = %d after Forward, want -1", n, in[0])
		}
	}
}

func TestInverseKeepsInput(t *testing.T) {
	for _, n := range mvpSizes {
		f, _ := New(Config{Size: n})
		x := signal("noise", n)
		in := slices.Clone(x)
		_ = f.Inverse(in, make([]int16, n))
		if !slices.Equal(in, x) {
			t.Errorf("size %d: Inverse changed its input", n)
		}
	}
}

// TestSmallestSize covers N = 2, where every divisor (nfft in the twiddle
// phase, the radix in C_FIXDIV) takes its smallest value, 1.
func TestSmallestSize(t *testing.T) {
	f, err := New(Config{Size: 2})
	if err != nil {
		t.Fatal(err)
	}
	if f.forward.sub.nfft != 1 || f.forward.sub.factors[0] != 1 {
		t.Fatalf("nfft %d, first radix %d; want 1 and 1", f.forward.sub.nfft, f.forward.sub.factors[0])
	}
	out := make([]int16, 2)
	for _, x := range [][]int16{{32767, 32767}, {-32768, -32768}, {32767, -32768}, {0, 0}} {
		if err := f.Forward(slices.Clone(x), out); err != nil {
			t.Fatal(err)
		}
		if err := f.Inverse(slices.Clone(x), out); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNoAllocsTransform(t *testing.T) {
	for _, n := range mvpSizes {
		f, _ := New(Config{Size: n})
		in := signal("noise", n)
		out := make([]int16, n)
		t.Run(fmt.Sprintf("Forward/%d", n), func(t *testing.T) {
			testutil.AssertNoAllocs(t, 100, func() { _ = f.Forward(in, out) })
		})
		t.Run(fmt.Sprintf("Inverse/%d", n), func(t *testing.T) {
			testutil.AssertNoAllocs(t, 100, func() { _ = f.Inverse(in, out) })
		})
		t.Run(fmt.Sprintf("Error/%d", n), func(t *testing.T) {
			testutil.AssertNoAllocs(t, 100, func() { _ = f.Forward(in[:1], out) })
		})
	}
}

// TestRoundTrip reports Inverse(Forward(x)) - x for docs/algorithms.md. It
// asserts nothing; the reference round-trip streams pin the values.
func TestRoundTrip(t *testing.T) {
	for _, n := range mvpSizes {
		f, _ := New(Config{Size: n})
		for _, s := range []string{"noise", "sine_16000", "sine_3", "impulse_first"} {
			x := signal(s, n)
			spec, y := make([]int16, n), make([]int16, n)
			_ = f.Forward(slices.Clone(x), spec)
			_ = f.Inverse(spec, y)
			maxErr, sum := 0, 0.0
			for i := range x {
				d := int(y[i]) - int(x[i])
				maxErr = max(maxErr, d, -d)
				sum += float64(d * d)
			}
			t.Logf("size %4d %-13s max |error| %5d, rms error %8.2f", n, s, maxErr, math.Sqrt(sum/float64(n)))
		}
	}
}

func FuzzTransform(f *testing.F) {
	f.Add(0, 0, []byte{})
	f.Add(3, 0, []byte{0x00, 0x80, 0xff, 0x7f})
	f.Add(426, -1, []byte{0x00, 0x80})
	var sizes []int
	for n := 2; n <= MaxSize; n += 2 {
		if Supported(n) {
			sizes = append(sizes, n)
		}
	}
	ffts := make(map[int]*FFT)
	f.Fuzz(func(t *testing.T, sel, delta int, data []byte) {
		n := sizes[uint(sel)%uint(len(sizes))]
		ft := ffts[n]
		if ft == nil {
			ft, _ = New(Config{Size: n})
			ffts[n] = ft
		}
		// A wrong length in either slice must give ErrBufferSize.
		if d := delta % 3; d != 0 {
			bad := make([]int16, max(0, n+d))
			if ft.Forward(bad, make([]int16, n)) != ErrBufferSize || ft.Inverse(make([]int16, n), bad) != ErrBufferSize {
				t.Fatal("wrong length accepted")
			}
		}
		x := make([]int16, n)
		for i := range x {
			if 2*i+1 < len(data) {
				x[i] = int16(uint16(data[2*i]) | uint16(data[2*i+1])<<8)
			}
		}
		in, out := slices.Clone(x), make([]int16, n)
		if err := ft.Forward(in, out); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(x, -32768) && !slices.Equal(in, x) {
			t.Fatal("Forward changed an input without -32768")
		}
		in = slices.Clone(x)
		if err := ft.Inverse(in, out); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(in, x) {
			t.Fatal("Inverse changed its input")
		}
	})
}

func BenchmarkFFTForward(b *testing.B) { benchTransform(b, (*FFT).Forward) }

func BenchmarkFFTInverse(b *testing.B) { benchTransform(b, (*FFT).Inverse) }

// benchTransform reports frame metrics for one hop of Size/2 samples, read
// as a 10 ms frame (rate = 100 * Size/2).
func benchTransform(b *testing.B, op func(*FFT, []int16, []int16) error) {
	for _, n := range mvpSizes {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			f, _ := New(Config{Size: n})
			x := signal("noise", n)
			in, out := make([]int16, n), make([]int16, n)
			b.ReportAllocs()
			for b.Loop() {
				copy(in, x)
				_ = op(f, in, out)
			}
			testutil.ReportFrameMetrics(b, n/2, float64(100*n/2))
		})
	}
}
