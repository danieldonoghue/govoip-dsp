package agc

import (
	"math"
	"slices"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

// recorder feeds an AGC one sample at a time and records, for every output
// sample, the gain applied to it and the AGC gain after it.
type recorder struct {
	a       *AGC
	in, out []int16
	applied []int32
	agcGain []int32
	fine    []int32 // AGC gain in Q22 after each input sample
	levels  []int32 // level estimate after each input sample
	speech  []bool  // per input sample
}

func newRecorder(t testing.TB, cfg Config) *recorder {
	t.Helper()
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &recorder{a: a}
}

func (r *recorder) feed(t testing.TB, x []int16, speech bool) {
	t.Helper()
	var y [1]int16
	for i := range x {
		r.applied = append(r.applied, r.a.ramp+r.a.step*r.a.n)
		if err := r.a.Process(x[i:i+1], y[:], speech); err != nil {
			t.Fatal(err)
		}
		r.agcGain = append(r.agcGain, r.a.gain)
		r.fine = append(r.fine, r.a.fine)
		r.levels = append(r.levels, r.a.level)
		r.out = append(r.out, y[0])
		r.in = append(r.in, x[i])
		r.speech = append(r.speech, speech)
	}
}

// now returns the number of input samples fed so far.
func (r *recorder) now() int { return len(r.in) }

func (r *recorder) frameLen() int { return int(r.a.frameLen) }

func (r *recorder) frameSeconds(cfg Config) float64 {
	return float64(r.frameLen()) / float64(cfg.SampleRate)
}

// frames returns the number of whole input frames whose delayed output is
// complete.
func (r *recorder) frames() int {
	return (len(r.out) - r.a.Latency()) / r.frameLen()
}

func (r *recorder) inFrame(f int) []int16 {
	F := r.frameLen()
	return r.in[f*F : (f+1)*F]
}

func (r *recorder) outFrame(f int) []int16 {
	F, d := r.frameLen(), r.a.Latency()
	return r.out[f*F+d : (f+1)*F+d]
}

// level returns the dBFS of output frame f.
func (r *recorder) level(f int) float64 { return testutil.DBFS(testutil.RMS(r.outFrame(f))) }

// frameGain returns the gain of frame f in dB, and false when the input
// frame is not at least 10 dB above gate.
func (r *recorder) frameGain(f, gate int) (float64, bool) {
	in := testutil.RMS(r.inFrame(f))
	if testutil.DBFS(in) < float64(gate+10) {
		return 0, false
	}
	return 20 * math.Log10(testutil.RMS(r.outFrame(f))/in), true
}

// convergence returns the time from frame f0 until every frame level in
// [f0, f1) stays within tol dB of target.
func (r *recorder) convergence(f0, f1 int, target, tol float64, cfg Config) float64 {
	last := f0 - 1
	for f := f0; f < f1; f++ {
		if math.Abs(r.level(f)-target) > tol {
			last = f
		}
	}
	return float64(last+1-f0) * r.frameSeconds(cfg)
}

// convergenceGain is convergence on the gain-corrected frame level:
// nominal plus the frame gain.
func (r *recorder) convergenceGain(f0, f1 int, nominal, target, tol float64, cfg Config) float64 {
	last := f0 - 1
	for f := f0; f < f1; f++ {
		g, _ := r.frameGain(f, -1000)
		if math.Abs(nominal+g-target) > tol {
			last = f
		}
	}
	return float64(last+1-f0) * r.frameSeconds(cfg)
}

// gainsDB returns the frame gains in [f0, f1) that frameGain accepts.
func (r *recorder) gainsDB(f0, f1, gate int) []float64 {
	var g []float64
	for f := f0; f < f1; f++ {
		if v, ok := r.frameGain(f, gate); ok {
			g = append(g, v)
		}
	}
	return g
}

// percentile returns the nearest-rank p-th percentile (0 < p <= 100).
func percentile(v []float64, p float64) float64 {
	s := slices.Clone(v)
	slices.Sort(s)
	k := int(math.Ceil(p/100*float64(len(s)))) - 1
	return s[max(k, 0)]
}

func q15dB(g int32) float64 { return 20 * math.Log10(float64(g)/unity) }

// scaled returns x times g, rounded and saturated.
func scaled(x []int16, g float64) []int16 {
	y := make([]int16, len(x))
	for i, v := range x {
		y[i] = int16(max(-32768, min(32767, math.Round(float64(v)*g))))
	}
	return y
}

// toRMS scales x to an RMS of dbfs.
func toRMS(x []int16, dbfs float64) []int16 {
	return scaled(x, math.Pow(10, dbfs/20)*32768/testutil.RMS(x))
}

// activeRMS returns the RMS over the 10 ms frames of x whose RMS is at
// least gate dBFS, and 0 if there are none.
func activeRMS(x []int16, frame, gate int) float64 {
	var sum float64
	n := 0
	for f := 0; (f+1)*frame <= len(x); f++ {
		w := x[f*frame : (f+1)*frame]
		if rms := testutil.RMS(w); testutil.DBFS(rms) >= float64(gate) {
			sum += rms * rms * float64(len(w))
			n += len(w)
		}
	}
	if n == 0 {
		return 0
	}
	return math.Sqrt(sum / float64(n))
}

// toActive scales x so that its active RMS (frames at or above gate) is
// dbfs. The active set depends on the scale, so this iterates.
func toActive(x []int16, frame, gate int, dbfs float64) []int16 {
	y := x
	for range 6 {
		y = scaled(y, math.Pow(10, dbfs/20)*32768/activeRMS(y, frame, gate))
	}
	return y
}

// sine returns n samples of a 1 kHz sine with an RMS of dbfs.
func sine(n, rate int, dbfs float64) []int16 {
	amp := math.Sqrt2 * 32768 * math.Pow(10, dbfs/20)
	return testutil.Sine(n, float64(rate), 1000, amp, 0)
}

// white returns n samples of white noise with an RMS of dbfs.
func white(n int, dbfs float64, seed uint64) []int16 {
	return toRMS(testutil.WhiteNoise(n, 20000, seed), dbfs)
}

// checkCeiling fails t for any output sample above the ceiling, at 32767
// or -32768, or with the opposite sign to its input (a wrap).
func checkCeiling(t testing.TB, r *recorder, name string, allowFull bool) {
	t.Helper()
	d := r.a.Latency()
	over, clipped, wrapped := 0, 0, 0
	for n := d; n < len(r.out); n++ {
		y, x := int32(r.out[n]), int32(r.in[n-d])
		if max(y, -y) > r.a.ceiling {
			over++
		}
		if y == -32768 || (y == 32767 && !allowFull) {
			clipped++
		}
		if (y > 0 && x <= 0) || (y < 0 && x >= 0) {
			wrapped++
		}
	}
	if over+clipped+wrapped > 0 {
		t.Errorf("%s: %d samples above ceiling %d, %d clipped, %d wrapped", name, over, r.a.ceiling, clipped, wrapped)
	}
}

// checkGainBounds fails t if the AGC gain leaves [MinGain, MaxGain] or the
// applied gain exceeds MaxGain.
func checkGainBounds(t testing.TB, r *recorder, name string) {
	t.Helper()
	for n := range r.agcGain {
		if g := r.agcGain[n]; g < r.a.minGain || g > r.a.maxGain {
			t.Fatalf("%s: AGC gain %d at sample %d outside [%d, %d]", name, g, n, r.a.minGain, r.a.maxGain)
		}
		if g := r.applied[n]; g > r.a.maxGain || g <= 0 {
			t.Fatalf("%s: applied gain %d at sample %d outside (0, %d]", name, g, n, r.a.maxGain)
		}
	}
}

// frameActive reports whether input frame f was active.
func (r *recorder) frameActive(f int) bool {
	F := r.frameLen()
	for _, v := range r.speech[f*F : (f+1)*F] {
		if !v {
			return false
		}
	}
	return frameRMS(r.inFrame(f)) >= r.a.gate
}

// checkRates checks R1 and R2 on every frame and returns the frames where
// the gain fell faster than FallRate (immediate cuts). Loudness is judged
// from a float RMS of the input, not from the AGC's own rms.
func checkRates(t testing.TB, r *recorder, name string, cfg Config) []int {
	t.Helper()
	F := r.frameLen()
	sec := float64(F) / float64(cfg.SampleRate)
	riseMax := float64(cfg.RiseRate)*sec + 0.01
	fallMax := float64(cfg.FallRate)*sec + 0.01
	var cuts []int
	prevGain, prevLevel := int32(unity<<7), r.a.target
	var levels []float64 // level before each frame, Q12
	for f := 0; (f+1)*F <= len(r.in); f++ {
		levels = append(levels, float64(prevLevel))
		g := r.fine[(f+1)*F-1]
		d := 20 * math.Log10(float64(g)/float64(prevGain))
		active := r.frameActive(f)
		if g > prevGain && !active {
			t.Errorf("%s R1: gain rose %.4f dB on inactive frame %d", name, d, f)
		}
		if g > prevGain && d > riseMax {
			t.Errorf("%s R1: gain rose %.4f dB on frame %d (limit %.4f)", name, d, f, riseMax)
		}
		if g < prevGain && -d > fallMax {
			// R2: only after riseRun loud active frames in a row...
			ok := f >= r.a.riseRun-1
			for k := f - r.a.riseRun + 1; ok && k <= f; k++ {
				e := testutil.RMS(r.inFrame(k)) * 4096
				ok = r.frameActive(k) && e*e > cutRatio*levels[k]*levels[k]
			}
			// ...and exactly to the target gain of the new level.
			lvl := int64(r.levels[(f+1)*F-1])
			want := int32(min(max((int64(r.a.target)<<15)/lvl, int64(r.a.minGain)), int64(r.a.maxGain))) << 7
			if !ok || g != min(prevGain, want) {
				t.Errorf("%s R2: gain fell %.4f dB on frame %d (sustained rise %v, gain %d, want %d)",
					name, -d, f, ok, g, min(prevGain, want))
			}
			cuts = append(cuts, f)
		}
		prevGain, prevLevel = g, r.levels[(f+1)*F-1]
	}
	return cuts
}

// frameRMS is the AGC's frame RMS of x, in Q12.
func frameRMS(x []int16) int32 {
	var e int64
	for _, v := range x {
		e += int64(v) * int64(v)
	}
	return rms(e, int64(len(x)))
}
