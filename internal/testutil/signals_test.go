package testutil

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"testing"
)

func TestDsinAccuracy(t *testing.T) {
	r := SplitMix64{State: 1}
	for _, tc := range []struct {
		span, tol float64
	}{
		{10, 1e-15},
		{1e3, 1e-13},
		{1e7, 2e-9},
	} {
		worst := 0.0
		for i := 0; i < 200000; i++ {
			x := (float64(r.Next()>>11)/(1<<53)*2 - 1) * tc.span
			worst = max(worst, math.Abs(dsin(x)-math.Sin(x)))
		}
		if worst > tc.tol {
			t.Errorf("|x| <= %g: max error %g, want <= %g", tc.span, worst, tc.tol)
		}
		t.Logf("|x| <= %g: max |dsin - math.Sin| = %.3g", tc.span, worst)
	}
}

// Hashes recorded on darwin/arm64; tests regenerate harness inputs from
// seeds, so every platform must reproduce them.
func TestGeneratorsAreDeterministic(t *testing.T) {
	for _, tc := range []struct {
		name string
		x    []int16
		want string
	}{
		{"sine", Sine(48000, 48000, 997, 30000, 0.25), "91b998a4bd9489745356f5206c6bb705ba1086aa31781f1460b117bb46928288"},
		{"sweep", Sweep(48000, 48000, 20, 20000, 20000), "9b2b303381e3eda65729971e5415805aa534647c6dbb449931b79a26cca6d498"},
		{"white", WhiteNoise(48000, 12000, 7), "b116339acb26cfc93eb9f555867098b84451f2dca3fe3bc612b32037a5a40eeb"},
		{"pink", PinkNoise(48000, 12000, 7), "8c78e960e92c8cb20c2ccdc5161445214cc0a136f0c5786ce2401aa54dd33a83"},
		{"speech", SpeechLike(48000, 16000, 20000, 7), "970a4cbe9d178ae88b47d8a3799ba49586514b241176e72b8ab1be2df226b2cd"},
		{"scale", Scale(Sine(4800, 48000, 440, 32767, 0), 1, 1000), "bb183dba7f088c6a2e8f6051fbab8ad3269250da09f7c0492c5faff5ec8dece3"},
	} {
		sum := sha256.Sum256(EncodeLE(nil, tc.x))
		if got := hex.EncodeToString(sum[:]); got != tc.want {
			t.Errorf("%s: SHA-256 %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestSineProperties(t *testing.T) {
	x := Sine(48000, 48000, 1000, 10000, 0)
	if x[0] != 0 || x[12] != 10000 || x[36] != -10000 {
		t.Errorf("samples 0, 12, 36: %d %d %d, want 0 10000 -10000", x[0], x[12], x[36])
	}
	if rms := RMS(x); math.Abs(rms-10000/math.Sqrt2) > 1 {
		t.Errorf("RMS %g, want %g", rms, 10000/math.Sqrt2)
	}
	if y := Sine(100, 8000, 1000, 1e6, 0); y[2] != 32767 || y[6] != -32768 {
		t.Errorf("saturation: %d %d", y[2], y[6])
	}
}

func TestSweepCoversBand(t *testing.T) {
	// Zero crossings per 10 ms window measure the instantaneous frequency.
	x := Sweep(48000, 48000, 100, 10000, 20000)
	crossings := func(w []int16) int {
		c := 0
		for i := 1; i < len(w); i++ {
			if (w[i-1] < 0) != (w[i] < 0) {
				c++
			}
		}
		return c
	}
	if c := crossings(x[:480]); c < 1 || c > 4 {
		t.Errorf("first 10 ms: %d crossings, want about 2 (100 to 200 Hz)", c)
	}
	if c := crossings(x[len(x)-480:]); c < 190 || c > 200 {
		t.Errorf("last 10 ms: %d crossings, want about 198 (10 kHz)", c)
	}
}

func TestNoiseProperties(t *testing.T) {
	w := WhiteNoise(100000, 1000, 3)
	lo, hi, sum := int16(0), int16(0), 0
	for _, v := range w {
		lo, hi = min(lo, v), max(hi, v)
		sum += int(v)
	}
	if lo != -1000 || hi != 1000 {
		t.Errorf("white range [%d, %d], want [-1000, 1000]", lo, hi)
	}
	if mean := float64(sum) / float64(len(w)); math.Abs(mean) > 10 {
		t.Errorf("white mean %g, want about 0", mean)
	}
	p := PinkNoise(100000, 1000, 3)
	for i, v := range p {
		if v > 1000 || v < -1000 {
			t.Fatalf("pink sample %d = %d exceeds the amplitude", i, v)
		}
	}
	fw, fp := SpectralFlatness(w, 1024), SpectralFlatness(p, 1024)
	if !(fw > 0.5 && fp < fw) {
		t.Errorf("flatness white %g, pink %g: want white > 0.5 and pink < white", fw, fp)
	}
	if a, b := WhiteNoise(64, 100, 9), WhiteNoise(64, 100, 9); !Compare(t, "same seed", a, b) {
		t.Error("white noise is not reproducible")
	}
}

func TestSpeechLikeProperties(t *testing.T) {
	x := SpeechLike(5*16000, 16000, 20000, 11)
	peak, silent := int16(0), 0
	for _, v := range x {
		peak = max(peak, v, -v)
		if v == 0 {
			silent++
		}
	}
	if peak < 19990 || peak > 20000 {
		t.Errorf("peak %d, want 20000", peak)
	}
	if frac := float64(silent) / float64(len(x)); frac < 0.05 || frac > 0.6 {
		t.Errorf("%.2f of samples are exactly zero, want pauses but mostly signal", frac)
	}
}

func TestClipScaleAdd(t *testing.T) {
	x := []int16{-32768, -200, -3, 0, 3, 200, 32767}
	Compare(t, "Clip", []int16{-100, -100, -3, 0, 3, 100, 100}, Clip(x, 100))
	Compare(t, "Scale", []int16{-328, -2, 0, 0, 0, 2, 328}, Scale(x, 1, 100))
	Compare(t, "Scale saturates", []int16{-32768, -400, -6, 0, 6, 400, 32767}, Scale(x, 2, 1))
	Compare(t, "Add", []int16{-32768, -400, 32767}, Add([]int16{-32768, -200, 32767}, []int16{-1, -200, 1, 5}))
	if y := Impulse(4, 2, 7); y[2] != 7 || y[0] != 0 || len(Impulse(3, 9, 1)) != 3 {
		t.Errorf("Impulse: %v", y)
	}
}
