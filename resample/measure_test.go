package resample

import (
	"flag"
	"fmt"
	"math"
	"strings"
	"testing"
)

// The measurements reported in docs/algorithms.md. They use float64
// analysis and math.Sin, and are reported, not compared bit for bit:
//
//	go test ./resample -run TestMeasure -v -args -measure

var measure = flag.Bool("measure", false, "run the behavioural measurements of docs/acceptance/resample.md")

const toneAmp = 8192

func tone(n int, rate, f float64) []int16 {
	x := make([]int16, n)
	for i := range x {
		x[i] = int16(math.Round(toneAmp * math.Sin(2*math.Pi*f*float64(i)/rate)))
	}
	return x
}

// run resamples x in one call and returns the output.
func run(t *testing.T, in, out, q int, x []int16) []int16 {
	r := mustNew(t, Config{Channels: 1, InRate: in, OutRate: out, Quality: q})
	y := make([]int16, len(x)*out/in+64)
	_, n, err := r.Process(x, y)
	if err != nil {
		t.Fatal(err)
	}
	return y[:n]
}

// fit returns the least-squares amplitude of a tone at f over y, and the RMS
// of the residual.
func fit(y []int16, rate, f float64) (amp, resid float64) {
	var cc, ss, cs, yc, ys float64
	for i, v := range y {
		w := 2 * math.Pi * f * float64(i) / rate
		c, s := math.Cos(w), math.Sin(w)
		cc += c * c
		ss += s * s
		cs += c * s
		yc += float64(v) * c
		ys += float64(v) * s
	}
	det := cc*ss - cs*cs
	a := (yc*ss - ys*cs) / det
	b := (ys*cc - yc*cs) / det
	var e float64
	for i, v := range y {
		w := 2 * math.Pi * f * float64(i) / rate
		d := float64(v) - a*math.Cos(w) - b*math.Sin(w)
		e += d * d
	}
	return math.Hypot(a, b), math.Sqrt(e / float64(len(y)))
}

func rms(y []int16) float64 {
	var e float64
	for _, v := range y {
		e += float64(v) * float64(v)
	}
	return math.Sqrt(e / float64(len(y)))
}

func db(x float64) float64 { return 20 * math.Log10(x) }

// steady returns the part of y after the filter has filled.
func steady(t *testing.T, y []int16, skip int) []int16 {
	if len(y) < skip+4096 {
		t.Fatalf("output of %d samples too short", len(y))
	}
	return y[skip : skip+4096]
}

func toneGain(t *testing.T, in, out, q int, f float64) float64 {
	n := in / 4 * 2 // 0.5 s
	y := run(t, in, out, q, tone(n, float64(in), f))
	skip := 2*mustNew(t, Config{Channels: 1, InRate: in, OutRate: out, Quality: q}).OutputLatency() + 64
	a, _ := fit(steady(t, y, skip), float64(out), f)
	return db(a / toneAmp)
}

func TestMeasure(t *testing.T) {
	if !*measure {
		t.Skip("run with -args -measure")
	}
	pairs := [][2]int{{48000, 16000}, {16000, 48000}, {44100, 48000}, {8000, 16000}}
	var b strings.Builder

	fmt.Fprintf(&b, "\nLatency (input samples, output samples, measured impulse peak in output samples)\n")
	fmt.Fprintf(&b, "| q | 48k→16k | 16k→48k | 44.1k→48k | 8k→16k |\n|---|---|---|---|---|\n")
	for q := 0; q <= 10; q++ {
		fmt.Fprintf(&b, "| %d ", q)
		for _, p := range pairs {
			r := mustNew(t, Config{Channels: 1, InRate: p[0], OutRate: p[1], Quality: q})
			x := make([]int16, p[0]/10)
			x[0] = 30000
			y := run(t, p[0], p[1], q, x)
			peak := 0
			for i, v := range y {
				if v > y[peak] {
					peak = i
				}
			}
			fmt.Fprintf(&b, "| %d, %d, %d ", r.InputLatency(), r.OutputLatency(), peak)
		}
		fmt.Fprintf(&b, "|\n")
	}

	fmt.Fprintf(&b, "\nPassband (f_N = lower Nyquist): ripple to 0.5 f_N (dB), -3 dB edge (f_N), gain at 0.8 and 0.9 f_N (dB)\n")
	fmt.Fprintf(&b, "| q | 48k→16k | 16k→48k | 44.1k→48k | 8k→16k |\n|---|---|---|---|---|\n")
	for q := 0; q <= 10; q++ {
		fmt.Fprintf(&b, "| %d ", q)
		for _, p := range pairs {
			fn := float64(min(p[0], p[1])) / 2
			lo, hi := math.Inf(1), math.Inf(-1)
			edge := math.NaN()
			prevF, prevG := 0.0, 0.0
			for k := 1; k <= 64; k++ {
				f := 0.95 * fn * float64(k) / 64
				g := toneGain(t, p[0], p[1], q, f)
				if f <= 0.5*fn {
					lo, hi = min(lo, g), max(hi, g)
				}
				if math.IsNaN(edge) && g < -3 && k > 1 {
					edge = (prevF + (f-prevF)*(-3-prevG)/(g-prevG)) / fn
				}
				prevF, prevG = f, g
			}
			edgeS := "> 0.95"
			if !math.IsNaN(edge) {
				edgeS = fmt.Sprintf("%.3f", edge)
			}
			fmt.Fprintf(&b, "| %.3f, %s, %.1f, %.1f ", hi-lo, edgeS,
				toneGain(t, p[0], p[1], q, 0.8*fn), toneGain(t, p[0], p[1], q, 0.9*fn))
		}
		fmt.Fprintf(&b, "|\n")
	}

	fmt.Fprintf(&b, "\nStopband (downsampling): highest output level relative to the input tone (dB) for tones above 1.1, 1.25, 1.5 f_N\n")
	fmt.Fprintf(&b, "| q | 48k→16k | 48k→8k |\n|---|---|---|\n")
	for q := 0; q <= 10; q++ {
		fmt.Fprintf(&b, "| %d ", q)
		for _, p := range [][2]int{{48000, 16000}, {48000, 8000}} {
			fn := float64(p[1]) / 2
			top := 0.98 * float64(p[0]) / 2
			worst := [3]float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
			skip := 2*mustNew(t, Config{Channels: 1, InRate: p[0], OutRate: p[1], Quality: q}).OutputLatency() + 64
			for f := 1.1 * fn; f <= top; f += fn / 40 {
				x := tone(p[0], float64(p[0]), f)
				y := steady(t, run(t, p[0], p[1], q, x), skip)
				l := db(rms(y) / (toneAmp / math.Sqrt2))
				for i, lim := range []float64{1.1, 1.25, 1.5} {
					if f >= lim*fn {
						worst[i] = max(worst[i], l)
					}
				}
			}
			fmt.Fprintf(&b, "| %.1f, %.1f, %.1f ", worst[0], worst[1], worst[2])
		}
		fmt.Fprintf(&b, "|\n")
	}

	fmt.Fprintf(&b, "\nImaging (upsampling): highest residual (image and noise) relative to the tone (dB), tones up to 0.9 f_N\n")
	fmt.Fprintf(&b, "| q | 16k→48k | 8k→16k |\n|---|---|---|\n")
	for q := 0; q <= 10; q++ {
		fmt.Fprintf(&b, "| %d ", q)
		for _, p := range [][2]int{{16000, 48000}, {8000, 16000}} {
			fn := float64(p[0]) / 2
			worst := math.Inf(-1)
			skip := 2*mustNew(t, Config{Channels: 1, InRate: p[0], OutRate: p[1], Quality: q}).OutputLatency() + 64
			for k := 1; k <= 36; k++ {
				f := 0.9 * fn * float64(k) / 36
				y := steady(t, run(t, p[0], p[1], q, tone(p[0], float64(p[0]), f)), skip)
				a, res := fit(y, float64(p[1]), f)
				worst = max(worst, db(res/(a/math.Sqrt2)))
			}
			fmt.Fprintf(&b, "| %.1f ", worst)
		}
		fmt.Fprintf(&b, "|\n")
	}
	t.Log(b.String())
}
