package testutil

import "math"

// Generators return int16 samples on the 16-bit scale. Amplitudes are peak
// values on that scale. Results saturate to [-32768, 32767].

// Silence returns n zero samples.
func Silence(n int) []int16 {
	return make([]int16, n)
}

// Impulse returns n samples that are zero except x[pos] = amp. A pos outside
// [0, n) gives silence.
func Impulse(n, pos int, amp int16) []int16 {
	x := make([]int16, n)
	if pos >= 0 && pos < n {
		x[pos] = amp
	}
	return x
}

// Sine returns amp*sin(2*pi*freq*i/rate + phase), rounded to the nearest
// integer, for i in [0, n).
func Sine(n int, rate, freq, amp, phase float64) []int16 {
	x := make([]int16, n)
	w := float64(2*math.Pi*freq) / rate
	for i := range x {
		x[i] = sat16(float64(amp * dsin(float64(w*float64(i))+phase)))
	}
	return x
}

// Sweep returns a linear chirp from f0 to f1 Hz over n samples with peak amp.
func Sweep(n int, rate, f0, f1, amp float64) []int16 {
	x := make([]int16, n)
	if n == 0 {
		return x
	}
	dur := float64(n) / rate
	k := (f1 - f0) / dur
	for i := range x {
		t := float64(i) / rate
		// phase = 2*pi*(f0*t + k*t*t/2)
		ph := float64(2*math.Pi) * (float64(f0*t) + float64(float64(k*t)*t)/2)
		x[i] = sat16(float64(amp * dsin(ph)))
	}
	return x
}

// WhiteNoise returns n samples uniformly distributed in [-amp, amp].
func WhiteNoise(n int, amp int16, seed uint64) []int16 {
	r := SplitMix64{State: seed}
	x := make([]int16, n)
	span := 2*int(amp) + 1
	for i := range x {
		x[i] = int16(r.Intn(span) - int(amp))
	}
	return x
}

// PinkNoise returns n samples of approximately 1/f noise (Voss-McCartney with
// 16 rows of uniform noise), scaled so that no sample exceeds amp in
// magnitude.
func PinkNoise(n int, amp int16, seed uint64) []int16 {
	const rows = 16
	r := SplitMix64{State: seed}
	var row [rows]int64
	var sum int64
	for i := range row {
		row[i] = int64(r.Intn(65536)) - 32768
		sum += row[i]
	}
	x := make([]int16, n)
	for i := range x {
		// Row k is redrawn every 2^k samples: pick it by trailing zeros of i+1.
		k := 0
		for v := uint64(i + 1); v&1 == 0 && k < rows-1; v >>= 1 {
			k++
		}
		sum -= row[k]
		row[k] = int64(r.Intn(65536)) - 32768
		sum += row[k]
		// |sum| <= rows * 32768, so this keeps |x| <= amp.
		x[i] = int16(sum * int64(amp) / (rows * 32768))
	}
	return x
}

// Clip returns a copy of x saturated to [-limit, limit].
func Clip(x []int16, limit int16) []int16 {
	y := make([]int16, len(x))
	for i, v := range x {
		y[i] = max(-limit, min(limit, v))
	}
	return y
}

// Scale returns a copy of x multiplied by num/den, rounded to nearest (halves
// away from zero) and saturated. It produces low-level signals without
// floating point.
func Scale(x []int16, num, den int) []int16 {
	y := make([]int16, len(x))
	for i, v := range x {
		p := int64(v) * int64(num)
		q := (abs64(p) + int64(den)/2) / int64(den)
		if p < 0 {
			q = -q
		}
		y[i] = int16(max(-32768, min(32767, q)))
	}
	return y
}

// Add returns the sample-wise saturated sum of a and b, as long as the
// shorter of the two.
func Add(a, b []int16) []int16 {
	y := make([]int16, min(len(a), len(b)))
	for i := range y {
		y[i] = int16(max(-32768, min(32767, int32(a[i])+int32(b[i]))))
	}
	return y
}

// ToFloat32 converts samples to float32 on the same 16-bit scale.
func ToFloat32(x []int16) []float32 {
	y := make([]float32, len(x))
	for i, v := range x {
		y[i] = float32(v)
	}
	return y
}

// SpeechLike returns n samples of a synthetic speech-like signal at the given
// rate: voiced segments (harmonics of a gliding 90 to 230 Hz pitch with a
// falling spectrum and three formant peaks), unvoiced noise bursts and
// pauses, each under a raised-cosine envelope, scaled to peak amp. It is a
// deterministic stand-in for speech, not a speech model.
func SpeechLike(n int, rate float64, amp int16, seed uint64) []int16 {
	r := SplitMix64{State: seed}
	acc := make([]float64, n)
	ms := func(lo, hi int) int { return int(float64(lo+r.Intn(hi-lo+1)) * rate / 1000) }
	for pos := 0; pos < n; {
		var seg int
		switch kind := r.Intn(10); {
		case kind < 6:
			seg = ms(120, 320)
			voiced(acc, pos, seg, rate, &r)
		case kind < 8:
			seg = ms(40, 110)
			unvoiced(acc, pos, seg, &r)
		default:
			seg = ms(60, 250)
		}
		pos += seg + 1
	}
	peak := 0.0
	for _, v := range acc {
		peak = max(peak, v, -v)
	}
	x := make([]int16, n)
	if peak == 0 {
		return x
	}
	g := float64(amp) / peak
	for i, v := range acc {
		x[i] = sat16(float64(v * g))
	}
	return x
}

// formantGain emphasises harmonics near 500, 1500 and 2500 Hz.
func formantGain(f float64) float64 {
	g := 0.2
	for _, c := range [...]float64{500, 1500, 2500} {
		d := (f - c) / 150
		g += 1 / (1 + float64(d*d))
	}
	return g
}

func voiced(acc []float64, pos, seg int, rate float64, r *SplitMix64) {
	f0a := float64(90 + r.Intn(141))
	f0b := float64(90 + r.Intn(141))
	level := float64(5+r.Intn(6)) / 10
	phase := 0.0
	for i := 0; i < seg && pos+i < len(acc); i++ {
		frac := float64(i) / float64(seg)
		f0 := f0a + float64((f0b-f0a)*frac)
		phase += float64(2*math.Pi*f0) / rate
		if phase > 2*math.Pi {
			phase -= 2 * math.Pi
		}
		v := 0.0
		for k := 1; float64(k)*f0 < rate/2 && k <= 40; k++ {
			fk := float64(k) * f0
			v += float64(float64(formantGain(fk)/float64(k)) * dsin(float64(float64(k)*phase)))
		}
		acc[pos+i] += float64(float64(level*hann(frac)) * v)
	}
}

func unvoiced(acc []float64, pos, seg int, r *SplitMix64) {
	level := float64(2+r.Intn(4)) / 10
	prev := 0.0
	for i := 0; i < seg && pos+i < len(acc); i++ {
		w := float64(r.Intn(65536)-32768) / 32768
		// A first difference tilts the noise towards high frequencies.
		acc[pos+i] += float64(float64(level*hann(float64(i)/float64(seg))) * (w - prev))
		prev = w
	}
}

// hann is the raised cosine 0.5 - 0.5*cos(2*pi*u) for u in [0, 1].
func hann(u float64) float64 {
	return 0.5 - float64(0.5*dsin(float64(2*math.Pi*u)+math.Pi/2))
}

// dsin is a sine that gives the same bits on every platform: argument
// reduction and a degree-19 Taylor polynomial using only IEEE basic
// operations, with each product rounded explicitly. TestDsinAccuracy bounds
// its error; argument reduction dominates for large arguments.
func dsin(x float64) float64 {
	const twoPi = 2 * math.Pi
	k := math.Round(float64(x * (1 / twoPi)))
	r := x - float64(k*twoPi)
	if r > math.Pi/2 {
		r = math.Pi - r
	} else if r < -math.Pi/2 {
		r = -math.Pi - r
	}
	r2 := float64(r * r)
	// Coefficients (-1)^j / (2j+1)! for j = 9 down to 0.
	c := [...]float64{
		-1.0 / 121645100408832000, 1.0 / 355687428096000, -1.0 / 1307674368000, 1.0 / 6227020800,
		-1.0 / 39916800, 1.0 / 362880, -1.0 / 5040, 1.0 / 120, -1.0 / 6, 1,
	}
	p := c[0]
	for _, cj := range c[1:] {
		p = float64(p*r2) + cj
	}
	return float64(p * r)
}

func sat16(v float64) int16 {
	v = math.Round(v)
	if v >= 32767 {
		return 32767
	}
	if v <= -32768 {
		return -32768
	}
	return int16(v)
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
