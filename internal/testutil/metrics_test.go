package testutil

import (
	"math"
	"strings"
	"testing"
)

func near(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %g, want %g ± %g", name, got, want, tol)
	}
}

func TestRMSAndDBFS(t *testing.T) {
	near(t, "RMS", RMS([]int16{3, -4, 3, -4}), math.Sqrt(12.5), 1e-12)
	near(t, "RMS empty", RMS(nil), 0, 0)
	near(t, "DBFS full scale", DBFS(32768), 0, 1e-12)
	near(t, "DBFS half", DBFS(16384), -6.0206, 1e-4)
	if !math.IsInf(DBFS(0), -1) {
		t.Error("DBFS(0) is not -Inf")
	}
}

func TestSNRAndERLE(t *testing.T) {
	ref := []int16{100, -100, 100, -100}
	test := []int16{110, -90, 110, -90} // error energy 4*100, signal 4*10000
	near(t, "SNR", SNR(ref, test), 20, 1e-12)
	if !math.IsInf(SNR(ref, ref), 1) || !math.IsInf(SNR([]int16{0}, []int16{1}), -1) || !math.IsNaN(SNR([]int16{0}, []int16{0})) {
		t.Error("SNR special cases")
	}
	near(t, "ERLE", ERLE([]int16{1000, -1000}, []int16{10, -10}), 40, 1e-12)
}

func TestSegmentalSNR(t *testing.T) {
	ref := []int16{100, 100, 0, 0, 100, 100}
	test := []int16{100, 100, 5, 5, 101, 101} // frames: +Inf, skipped, 40 dB
	near(t, "SegmentalSNR", SegmentalSNR(ref, test, 2), (35+35)/2.0, 1e-12)
	test[0] = 0 // first frame: 10*log10(20000/10000) = 3.01 dB
	near(t, "SegmentalSNR", SegmentalSNR(ref, test, 2), (10*math.Log10(2)+35)/2, 1e-9)
	if !math.IsNaN(SegmentalSNR([]int16{0, 0}, []int16{1, 1}, 2)) {
		t.Error("all-silent reference should give NaN")
	}
}

func TestSpectralFlatness(t *testing.T) {
	tone := SpectralFlatness(Sine(8192, 8000, 1000, 10000, 0), 1024)
	white := SpectralFlatness(WhiteNoise(8192, 10000, 5), 1024)
	if !(tone < 0.01 && white > 0.5) {
		t.Errorf("flatness tone %g (want < 0.01), white %g (want > 0.5)", tone, white)
	}
	if !math.IsNaN(SpectralFlatness(make([]int16, 1024), 1024)) || !math.IsNaN(SpectralFlatness(nil, 1000)) {
		t.Error("silence or a non-power-of-two frame should give NaN")
	}
}

func TestFFTMatchesDFT(t *testing.T) {
	x := make([]complex128, 16)
	for i := range x {
		x[i] = complex(float64(i*i%7)-3, float64(i%3))
	}
	y := append([]complex128(nil), x...)
	fft(y)
	for k := range x {
		var s complex128
		for n := range x {
			a := -2 * math.Pi * float64(k*n) / 16
			s += x[n] * complex(math.Cos(a), math.Sin(a))
		}
		if d := y[k] - s; math.Hypot(real(d), imag(d)) > 1e-9 {
			t.Fatalf("bin %d: fft %v, dft %v", k, y[k], s)
		}
	}
}

func TestAssertNoAllocs(t *testing.T) {
	var sink []int
	ok := run(t, func(tb testing.TB) { AssertNoAllocs(tb, 10, func() {}) })
	if len(ok.errs) > 0 {
		t.Errorf("non-allocating function reported: %s", ok.text())
	}
	bad := run(t, func(tb testing.TB) {
		AssertNoAllocs(tb, 10, func() { sink = make([]int, 100) })
	})
	if !strings.Contains(bad.text(), "allocations per run") {
		t.Errorf("allocating function not reported: %q", bad.text())
	}
	_ = sink
}
