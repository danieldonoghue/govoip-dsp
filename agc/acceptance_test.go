package agc

// Tests for the pre-registered criteria in docs/acceptance/agc.md. Each test
// logs its measured values; the IDs match the criteria.

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

var defaultRates = []int{8000, 16000, 44100, 48000}

func fastConfig() Config {
	c := DefaultConfig(16000)
	c.RiseRate, c.FallRate, c.LevelTime = 60, 200, 600*time.Millisecond
	return c
}

type stepCase struct {
	name     string
	cfg      Config
	from, to float64 // dBFS
	signal   func(n, rate int, dbfs float64) []int16
	// gainBased selects the gain-corrected frame level (C4, change 2).
	gainBased bool
}

func sineSignal(n, rate int, dbfs float64) []int16 { return sine(n, rate, dbfs) }

func whiteSignal(n, _ int, dbfs float64) []int16 { return white(n, dbfs, testutil.Seed("agc-step")) }

// runStep feeds 1500 frames at from, then 1500 frames at to, and returns the
// recorder and the first frame of the second segment.
func runStep(t *testing.T, c stepCase) (*recorder, int) {
	r := newRecorder(t, c.cfg)
	n := 1500 * r.frameLen()
	r.feed(t, c.signal(n, c.cfg.SampleRate, c.from), true)
	r.feed(t, c.signal(n+r.a.Latency(), c.cfg.SampleRate, c.to), true)
	return r, 1500
}

func stepCases() []stepCase {
	var cs []stepCase
	for _, rate := range defaultRates {
		cfg := DefaultConfig(rate)
		cs = append(cs,
			stepCase{fmt.Sprintf("C1/sine/%d", rate), cfg, -44, -24, sineSignal, false},
			stepCase{fmt.Sprintf("C2/sine/%d", rate), cfg, -24, -44, sineSignal, false})
	}
	cs = append(cs,
		stepCase{"C1/sine/fast", fastConfig(), -44, -24, sineSignal, false},
		stepCase{"C2/sine/fast", fastConfig(), -24, -44, sineSignal, false},
		stepCase{"C3/sine/up", DefaultConfig(16000), -34, -14, sineSignal, false},
		stepCase{"C3/sine/down", DefaultConfig(16000), -14, -34, sineSignal, false},
		stepCase{"C4/white/up", DefaultConfig(16000), -44, -24, whiteSignal, true},
		stepCase{"C4/white/down", DefaultConfig(16000), -24, -44, whiteSignal, true})
	return cs
}

// TestAcceptanceConvergence covers C1 to C4, T3, and T1 on the same signals.
func TestAcceptanceConvergence(t *testing.T) {
	for _, c := range stepCases() {
		t.Run(c.name, func(t *testing.T) {
			r, f0 := runStep(t, c)
			cfg := c.cfg
			// Registered numbers (design v2, test-structure change 1).
			up, down, over6 := 1.0, 5.8, 0.45
			if cfg == fastConfig() {
				up, down, over6 = 0.28, 1.24, 0.13
			}
			limit := up
			if c.to < c.from {
				limit = down
			}
			target := float64(cfg.TargetLevel)
			got := r.convergence(f0, r.frames(), target, 2, cfg)
			if c.gainBased {
				t.Logf("%s: output frame level convergence %.3f s (reported only)", c.name, got)
				got = r.convergenceGain(f0, r.frames(), c.to, target, 2, cfg)
			}
			t.Logf("%s: convergence %.3f s (threshold %.3f s)", c.name, got, limit)
			if got > limit {
				t.Errorf("convergence %.3f s > %.3f s", got, limit)
			}
			if c.to > c.from {
				over := 0
				for f := f0; f < r.frames(); f++ {
					if r.level(f) > target+6 {
						over++
					}
				}
				dur, lim := float64(over)*r.frameSeconds(cfg), over6
				t.Logf("T3 %s: %.3f s above target+6 dB (threshold %.3f s)", c.name, dur, lim)
				if dur > lim {
					t.Errorf("T3: %.3f s above target+6 dB > %.3f s", dur, lim)
				}
			}
			cuts := checkRates(t, r, c.name, cfg)
			if strings.HasPrefix(c.name, "C1/") {
				// R4: one cut, within 50 ms of the step.
				late := len(cuts) > 0 && float64(cuts[0]+1-f0)*r.frameSeconds(cfg) > 0.05
				t.Logf("R4 %s: cuts at frames %v, step at frame %d", c.name, cuts, f0)
				if len(cuts) != 1 || cuts[0] < f0 || late {
					t.Errorf("R4: cuts %v, want one within 50 ms of frame %d", cuts, f0)
				}
			}
			checkCeiling(t, r, c.name, false)
			checkGainBounds(t, r, c.name)
		})
	}
}

// TestAcceptanceSteadyState covers C5.
func TestAcceptanceSteadyState(t *testing.T) {
	cfg := DefaultConfig(16000)
	for _, sig := range []struct {
		name string
		gen  func(n, rate int, dbfs float64) []int16
		tol  float64
	}{{"sine", sineSignal, 0.25}, {"white", whiteSignal, 0.5}} {
		for _, lvl := range []float64{-44, -34, -24, -14} {
			name := fmt.Sprintf("%s/%v", sig.name, lvl)
			t.Run(name, func(t *testing.T) {
				r := newRecorder(t, cfg)
				r.feed(t, sig.gen(1500*r.frameLen()+r.a.Latency(), cfg.SampleRate, lvl), true)
				nf := r.frames()
				sum := 0.0
				for f := nf - 200; f < nf; f++ {
					sum += r.level(f)
				}
				err := sum/200 - float64(cfg.TargetLevel)
				t.Logf("C5 %s: steady-state error %+.3f dB (threshold ±%.2f dB)", name, err, sig.tol)
				if math.Abs(err) > sig.tol {
					t.Errorf("steady-state error %+.3f dB", err)
				}
				checkCeiling(t, r, name, false)
				checkGainBounds(t, r, name)
			})
		}
	}
}

// TestAcceptanceGainLimits covers C6.
func TestAcceptanceGainLimits(t *testing.T) {
	cfg := DefaultConfig(16000)
	for _, c := range []struct {
		in, want float64
		atMax    bool
	}{{-49, -25, true}, {-6, -18, false}} {
		name := fmt.Sprintf("in%v", c.in)
		t.Run(name, func(t *testing.T) {
			r := newRecorder(t, cfg)
			r.feed(t, sine(1500*r.frameLen()+r.a.Latency(), cfg.SampleRate, c.in), true)
			nf := r.frames()
			sum := 0.0
			for f := nf - 200; f < nf; f++ {
				sum += r.level(f)
			}
			got := sum / 200
			t.Logf("C6 input %v dBFS: output %.3f dBFS (want %v ±0.25), final AGC gain %.3f dB",
				c.in, got, c.want, q15dB(r.agcGain[len(r.agcGain)-1]))
			if math.Abs(got-c.want) > 0.25 {
				t.Errorf("output %.3f dBFS, want %v", got, c.want)
			}
			g, want := r.agcGain[len(r.agcGain)-1], r.a.minGain
			if c.atMax {
				want = r.a.maxGain
			}
			if g != want {
				t.Errorf("final AGC gain %d, want %d", g, want)
			}
			checkCeiling(t, r, name, false)
			checkGainBounds(t, r, name)
		})
	}
}

// speechLike returns sec seconds of SpeechLike with an active RMS of dbfs.
func speechLike(sec float64, rate int, dbfs float64, seed string, cfg Config) []int16 {
	n := int(sec * float64(rate))
	x := testutil.SpeechLike(n, float64(rate), 20000, testutil.Seed(seed))
	return toActive(x, 10*(rate/1000), cfg.GateLevel, dbfs)
}

// pumping reports P and S metrics for x after 10 s of settling.
func pumping(t *testing.T, name string, cfg Config, x []int16, maxSpread float64, speech bool) {
	r := newRecorder(t, cfg)
	r.feed(t, x, true)
	r.feed(t, make([]int16, r.a.Latency()), true)
	f0 := int(10 / r.frameSeconds(cfg))
	g := r.gainsDB(f0, r.frames(), cfg.GateLevel)
	spread := percentile(g, 95) - percentile(g, 5)
	t.Logf("P %s: p95-p5 %.3f dB (threshold %.2f dB), max-min %.3f dB, %d frames",
		name, spread, maxSpread, percentile(g, 100)-percentile(g, 0.0001), len(g))
	if spread > maxSpread {
		t.Errorf("p95-p5 gain spread %.3f dB > %.2f dB", spread, maxSpread)
	}
	if speech {
		var e float64
		n := 0
		for f := f0; f < r.frames(); f++ {
			if testutil.DBFS(testutil.RMS(r.inFrame(f))) >= float64(cfg.GateLevel) {
				rms := testutil.RMS(r.outFrame(f))
				e += rms * rms
				n++
			}
		}
		lvl := testutil.DBFS(math.Sqrt(e / float64(n)))
		t.Logf("S1 %s: output active RMS %.3f dBFS (target %d ±3 dB)", name, lvl, cfg.TargetLevel)
		if math.Abs(lvl-float64(cfg.TargetLevel)) > 3 {
			t.Errorf("S1: active RMS %.3f dBFS", lvl)
		}
	}
	if speech {
		t.Logf("R1/R2 %s: cuts at frames %v", name, checkRates(t, r, name, cfg))
	}
	checkCeiling(t, r, name, false)
	checkGainBounds(t, r, name)
}

// TestAcceptancePumping covers P1 to P3 and S1 on the synthetic signal.
func TestAcceptancePumping(t *testing.T) {
	cfg := DefaultConfig(16000)
	n := 40 * cfg.SampleRate
	t.Run("P1/sine", func(t *testing.T) { pumping(t, "P1 sine", cfg, sine(n, cfg.SampleRate, -34), 0.1, false) })
	t.Run("P2/white", func(t *testing.T) {
		pumping(t, "P2 white", cfg, white(n, -34, testutil.Seed("agc-p2")), 0.5, false)
	})
	t.Run("P3/speechlike", func(t *testing.T) {
		pumping(t, "P3 SpeechLike", cfg, speechLike(70, cfg.SampleRate, -34, "agc-p3", cfg), 3, true)
	})
}

// TestAcceptanceLibriSpeech covers P4 and S1 on real speech.
func TestAcceptanceLibriSpeech(t *testing.T) {
	paths := testutil.SpeechFiles(t)
	cfg := DefaultConfig(testutil.SpeechRate)
	var x []int16
	for _, p := range paths {
		x = append(x, toActive(testutil.LoadSpeech(t, p), 10*(testutil.SpeechRate/1000), cfg.GateLevel, -34)...)
	}
	t.Logf("P4: %d utterances, %.1f s", len(paths), float64(len(x))/testutil.SpeechRate)
	pumping(t, "P4 LibriSpeech", cfg, x, 3, true)

	// G1 to G4: regression guards registered after measurement.
	for _, lvl := range []float64{-38, -30, -24, -20} {
		var y []int16
		for _, p := range paths {
			y = append(y, toActive(testutil.LoadSpeech(t, p), 10*(testutil.SpeechRate/1000), cfg.GateLevel, lvl)...)
		}
		r := newRecorder(t, cfg)
		r.feed(t, y, true)
		r.feed(t, make([]int16, r.a.Latency()), true)
		g := r.gainsDB(int(10/r.frameSeconds(cfg)), r.frames(), cfg.GateLevel)
		spread := percentile(g, 95) - percentile(g, 5)
		t.Logf("G at %v dBFS: p95-p5 %.3f dB (threshold 3 dB)", lvl, spread)
		if spread > 3 {
			t.Errorf("G at %v dBFS: p95-p5 %.3f dB > 3 dB", lvl, spread)
		}
	}
}

// transientSuite returns the T1 suite at rate: speech, then each event
// followed by 2 s of speech.
func transientSuite(rate int, cfg Config) ([]int16, int) {
	sp := func(sec float64, seed string) []int16 { return speechLike(sec, rate, -44, seed, cfg) }
	ms := func(d float64) int { return int(d * float64(rate) / 1000) }
	x := sp(10, "agc-t-lead")
	events := [][]int16{
		sine(ms(2000), rate, -24),
		testutil.WhiteNoise(ms(500), 32767, testutil.Seed("agc-t-noise")),
		square(ms(500), rate, 100),
		testutil.Impulse(ms(100), ms(50), 32767),
		testutil.Impulse(ms(100), ms(50), -32768),
		testutil.WhiteNoise(ms(10), 32767, testutil.Seed("agc-t-click")),
		testutil.Sine(ms(500), float64(rate), 50, 32767, 0),
		dc(ms(100), -32768),
	}
	click := 0
	for i, e := range events {
		if i == 5 {
			click = len(x)
		}
		x = append(x, e...)
		x = append(x, sp(2, fmt.Sprintf("agc-t-%d", i))...)
	}
	return x, click
}

func square(n, rate int, freq float64) []int16 {
	x := make([]int16, n)
	half := int(float64(rate) / freq / 2)
	for i := range x {
		x[i] = 32767
		if (i/half)%2 == 1 {
			x[i] = -32767
		}
	}
	return x
}

func dc(n int, v int16) []int16 {
	x := make([]int16, n)
	for i := range x {
		x[i] = v
	}
	return x
}

// TestAcceptanceTransients covers T1, T2 and T2b.
func TestAcceptanceTransients(t *testing.T) {
	for _, rate := range defaultRates {
		t.Run(fmt.Sprint(rate), func(t *testing.T) {
			cfg := DefaultConfig(rate)
			r := newRecorder(t, cfg)
			x, click := transientSuite(rate, cfg)
			r.feed(t, x, true)
			cuts := checkRates(t, r, "T1 suite", cfg)
			cf := click / r.frameLen()
			for _, f := range cuts {
				if f >= cf-1 && f <= cf+r.a.riseRun+1 {
					t.Errorf("R5: cut at frame %d near the 10 ms click at frame %d", f, cf)
				}
			}
			t.Logf("R1/R2/R5 %d Hz: cuts at frames %v, click at frame %d", rate, cuts, cf)
			peak := int32(0)
			for _, y := range r.out {
				peak = max(peak, int32(y), -int32(y))
			}
			t.Logf("T1/T2 %d Hz: output peak %d, ceiling %d", rate, peak, r.a.ceiling)
			checkCeiling(t, r, "T1/T2", false)
			checkGainBounds(t, r, "T1/T2")
		})
	}
	t.Run("T2b/fullscale", func(t *testing.T) {
		cfg := DefaultConfig(16000)
		cfg.LimitLevel = 0
		r := newRecorder(t, cfg)
		x, _ := transientSuite(16000, cfg)
		r.feed(t, x, true)
		peak := int32(0)
		for _, y := range r.out {
			peak = max(peak, int32(y), -int32(y))
		}
		t.Logf("T2b: output peak %d, ceiling %d", peak, r.a.ceiling)
		checkCeiling(t, r, "T2b", true)
	})
}

// TestAcceptanceClick covers T4.
func TestAcceptanceClick(t *testing.T) {
	cfg := DefaultConfig(16000)
	for _, offset := range []int{0, 80} {
		name := fmt.Sprintf("offset%d", offset)
		t.Run(name, func(t *testing.T) {
			r := newRecorder(t, cfg)
			F := r.frameLen()
			r.feed(t, sine(1500*F+offset, cfg.SampleRate, -44), true)
			start := r.now()
			r.feed(t, testutil.WhiteNoise(F, 32767, testutil.Seed("agc-t4")), true)
			end := r.now()
			r.feed(t, sine(200*F, cfg.SampleRate, -44), true)

			before := r.agcGain[start-1]
			lowest := before
			for _, g := range r.agcGain[start:] {
				lowest = min(lowest, g)
			}
			drop := q15dB(before) - q15dB(lowest)

			d := r.a.Latency()
			pre := r.applied[start+d-F]
			recovered := end + d
			for n := end + d; n < len(r.applied); n++ {
				if math.Abs(q15dB(r.applied[n])-q15dB(pre)) > 1.5 {
					recovered = n + 1
				}
			}
			rec := float64(recovered-end-d) / float64(cfg.SampleRate)
			t.Logf("T4 %s: AGC gain drop %.3f dB (threshold 1.25), applied gain within 1.5 dB after %.1f ms (threshold 100)",
				name, drop, rec*1000)
			if drop > 1.25 {
				t.Errorf("AGC gain drop %.3f dB", drop)
			}
			if rec > 0.1 {
				t.Errorf("recovery %.1f ms", rec*1000)
			}
			if cuts := checkRates(t, r, name, cfg); len(cuts) != 0 {
				t.Errorf("R5: cuts at frames %v", cuts)
			}
			checkCeiling(t, r, name, false)
		})
	}
}

// TestAcceptanceClickRuns covers R5 for clicks in pauses and for a click
// at a rate whose frames are shorter than 10 ms.
func TestAcceptanceClickRuns(t *testing.T) {
	t.Run("pauses", func(t *testing.T) {
		cfg := DefaultConfig(16000)
		r := newRecorder(t, cfg)
		r.feed(t, speechLike(10, cfg.SampleRate, -44, "agc-r5", cfg), true)
		for i := range 3 {
			// 1 s of zeros, then a 10 ms click straddling two frames.
			r.feed(t, make([]int16, cfg.SampleRate+80*i), true)
			r.feed(t, testutil.WhiteNoise(160, 32767, testutil.Seed(fmt.Sprint("agc-r5-", i))), true)
		}
		r.feed(t, make([]int16, cfg.SampleRate), true)
		cuts := checkRates(t, r, "R5 pauses", cfg)
		t.Logf("R5 clicks in pauses: cuts %v, AGC gain %.2f dB", cuts, q15dB(r.a.gain))
		if len(cuts) != 0 {
			t.Errorf("R5: cuts at frames %v", cuts)
		}
	})
	t.Run("9999Hz", func(t *testing.T) {
		cfg := DefaultConfig(9999)
		r := newRecorder(t, cfg)
		F := r.frameLen()
		r.feed(t, sine(1500*F-5, cfg.SampleRate, -44), true) // the click touches 3 frames
		r.feed(t, testutil.WhiteNoise((cfg.SampleRate+99)/100, 32767, testutil.Seed("agc-r5-odd")), true)
		r.feed(t, sine(100*F, cfg.SampleRate, -44), true)
		cuts := checkRates(t, r, "R5 9999 Hz", cfg)
		t.Logf("R5 9999 Hz click over 3 frames: run %d frames, cuts %v", r.a.riseRun, cuts)
		if len(cuts) != 0 {
			t.Errorf("R5: cuts at frames %v", cuts)
		}
	})
}

// TestAcceptanceNoise covers N1 to N3.
func TestAcceptanceNoise(t *testing.T) {
	cfg := DefaultConfig(16000)
	n := 60 * cfg.SampleRate
	pink := toRMS(testutil.PinkNoise(n, 20000, testutil.Seed("agc-n1-pink")), -55)
	t.Run("N1/pink-55-reported", func(t *testing.T) {
		// Changes 3 and 7: frames of this signal cross the gate; reported
		// only, as N1's original pink case and as N4.
		r := newRecorder(t, cfg)
		r.feed(t, pink, true)
		maxApplied := int32(0)
		for _, g := range r.applied {
			maxApplied = max(maxApplied, g)
		}
		t.Logf("N1 pink at -55 dBFS long-term (reported only): loudest frame %.2f dBFS, max applied gain %.3f dB",
			loudestFrame(pink, r.frameLen()), q15dB(maxApplied))
	})
	// Change 3: the loudest frame is 1 dB below the gate.
	pinkBelow := scaled(pink, math.Pow(10, (float64(cfg.GateLevel)-1-loudestFrame(pink, 10*(cfg.SampleRate/1000)))/20))
	for _, c := range []struct {
		name   string
		x      []int16
		speech bool
	}{
		{"N1/white", white(n, -55, testutil.Seed("agc-n1")), true},
		{"N1/pink", pinkBelow, true},
		{"N2/white", white(n, -30, testutil.Seed("agc-n2")), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRecorder(t, cfg)
			r.feed(t, c.x, c.speech)
			r.feed(t, make([]int16, r.a.Latency()), c.speech)
			t.Logf("%s: input %.2f dBFS, loudest frame %.2f dBFS", c.name,
				testutil.DBFS(testutil.RMS(c.x)), loudestFrame(c.x, r.frameLen()))
			maxApplied := int32(0)
			for _, g := range r.applied {
				maxApplied = max(maxApplied, g)
			}
			d := r.a.Latency()
			ratio := 20 * math.Log10(testutil.RMS(r.out[d:])/testutil.RMS(c.x))
			t.Logf("%s: max applied gain %d (%.4f dB, threshold 0 dB), output/input %+.4f dB (threshold +0.1)",
				c.name, maxApplied, q15dB(maxApplied), ratio)
			if maxApplied > unity {
				t.Errorf("applied gain %d above 0 dB", maxApplied)
			}
			if ratio > 0.1 {
				t.Errorf("output %+.4f dB above input", ratio)
			}
			checkCeiling(t, r, c.name, false)
		})
	}
	t.Run("N3", func(t *testing.T) {
		r := newRecorder(t, cfg)
		r.feed(t, speechLike(15, cfg.SampleRate, -44, "agc-n3", cfg), true)
		start := r.now()
		ref := r.agcGain[start-1]
		r.feed(t, white(n, -60, testutil.Seed("agc-n3-noise")), true)
		d := r.a.Latency()
		agcChange, maxApplied := int32(0), int32(0)
		for i := start; i < len(r.agcGain); i++ {
			agcChange = max(agcChange, r.agcGain[i]-ref, ref-r.agcGain[i])
		}
		for i := start + d; i < len(r.applied); i++ {
			maxApplied = max(maxApplied, r.applied[i])
		}
		lvl := testutil.DBFS(testutil.RMS(r.out[start+d:]))
		t.Logf("N3: AGC gain %.3f dB, change %d (threshold 0), max applied %d vs AGC %d, noise output %.2f dBFS (threshold -36)",
			q15dB(ref), agcChange, maxApplied, ref, lvl)
		if agcChange != 0 || maxApplied > ref || maxApplied > r.a.maxGain {
			t.Errorf("gain moved during noise: AGC change %d, max applied %d, AGC %d", agcChange, maxApplied, ref)
		}
		if lvl > -36 {
			t.Errorf("noise output %.2f dBFS above -36", lvl)
		}
		checkRates(t, r, "N3", cfg)
		checkCeiling(t, r, "N3", false)
		checkGainBounds(t, r, "N3")
	})
}

// loudestFrame returns the dBFS of the loudest frame of x.
func loudestFrame(x []int16, frame int) float64 {
	h := math.Inf(-1)
	for f := 0; (f+1)*frame <= len(x); f++ {
		h = max(h, testutil.DBFS(testutil.RMS(x[f*frame:(f+1)*frame])))
	}
	return h
}

// TestAcceptanceSilence covers Q1 to Q4.
func TestAcceptanceSilence(t *testing.T) {
	cfg := DefaultConfig(16000)
	t.Run("Q1", func(t *testing.T) {
		r := newRecorder(t, cfg)
		r.feed(t, make([]int16, 60*cfg.SampleRate), true)
		for n := range r.out {
			if r.out[n] != 0 || r.applied[n] != unity {
				t.Fatalf("sample %d: output %d, applied gain %d", n, r.out[n], r.applied[n])
			}
		}
		checkCeiling(t, r, "Q1", false)
		t.Logf("Q1: output all zero, applied gain %d throughout", unity)
	})
	gap := 60 * cfg.SampleRate
	dither := testutil.WhiteNoise(gap, 1, testutil.Seed("agc-q3"))
	for _, c := range []struct {
		name  string
		gap   []int16
		after float64 // dB relative to the speech before
	}{{"Q2", make([]int16, gap), 0}, {"Q3", dither, 0}, {"Q4", make([]int16, gap), 20}} {
		t.Run(c.name, func(t *testing.T) {
			r := newRecorder(t, cfg)
			r.feed(t, speechLike(15, cfg.SampleRate, -44, "agc-q", cfg), true)
			start := r.now()
			ref := r.agcGain[start-1]
			r.feed(t, c.gap, true)
			resume := r.now()
			r.feed(t, speechLike(10, cfg.SampleRate, -44+c.after, "agc-q", cfg), true)
			d := r.a.Latency()
			F := r.frameLen()

			agcChange, maxApplied := int32(0), int32(0)
			for i := start; i < resume; i++ {
				agcChange = max(agcChange, r.agcGain[i]-ref, ref-r.agcGain[i])
			}
			nonzero := 0
			for i := start + d; i < resume+d; i++ {
				maxApplied = max(maxApplied, r.applied[i])
				if r.out[i] != 0 && c.name != "Q3" {
					nonzero++
				}
			}
			if agcChange != 0 || maxApplied > ref || nonzero != 0 {
				t.Errorf("during gap: AGC change %d, max applied %d vs AGC %d, %d nonzero outputs", agcChange, maxApplied, ref, nonzero)
			}
			checkRates(t, r, c.name, cfg)
			checkCeiling(t, r, c.name, false)
			if c.after != 0 {
				t.Logf("%s: AGC change %d, max applied %d vs AGC %d; T1/T2 checked", c.name, agcChange, maxApplied, ref)
				return
			}
			highest := func(f0, f1 int) float64 {
				h := math.Inf(-1)
				for f := f0; f < f1; f++ {
					h = max(h, r.level(f))
				}
				return h
			}
			fs := cfg.SampleRate / F
			before := highest(start/F-5*fs, start/F)
			after := highest(resume/F, resume/F+fs)
			t.Logf("%s: AGC change %d, max applied %d vs AGC %d; highest frame %.2f dBFS after vs %.2f before (threshold +1 dB)",
				c.name, agcChange, maxApplied, ref, after, before)
			if after > before+1 {
				t.Errorf("burst: %.2f dBFS after vs %.2f before", after, before)
			}
		})
	}
}

// TestAcceptanceCautiousStart covers R3.
func TestAcceptanceCautiousStart(t *testing.T) {
	cfg := DefaultConfig(16000)
	r := newRecorder(t, cfg)
	r.feed(t, speechLike(20, cfg.SampleRate, -48, "agc-r3", cfg), true)
	F := r.frameLen()
	step := float64(cfg.RiseRate) * r.frameSeconds(cfg)
	n, worst := 0, math.Inf(-1)
	for f := 0; (f+1)*F <= len(r.in); f++ {
		if r.frameActive(f) {
			n++
		}
		if n == 0 {
			continue // the bound is trivial before the first active frame
		}
		g := 20 * math.Log10(float64(r.fine[(f+1)*F-1])/(unity<<7))
		worst = max(worst, g-(float64(n)*step+0.01))
	}
	g := q15dB(r.agcGain[len(r.agcGain)-1])
	t.Logf("R3: %d active frames, final gain %.2f dB, largest excess over n·RiseRate·F/R %.4f dB (threshold 0)", n, g, worst)
	if worst > 0 {
		t.Errorf("R3: gain exceeded the cautious-start bound by %.4f dB", worst)
	}
}

// TestAcceptanceStationaryNoise covers N5.
func TestAcceptanceStationaryNoise(t *testing.T) {
	cfg := DefaultConfig(16000)
	n := 120 * cfg.SampleRate
	for _, kind := range []string{"white", "pink"} {
		for _, lvl := range []float64{-60, -52, -45, -35, -24} {
			name := fmt.Sprintf("%s/%v", kind, lvl)
			t.Run(name, func(t *testing.T) {
				var x []int16
				if kind == "pink" {
					x = toRMS(testutil.PinkNoise(n, 20000, testutil.Seed("agc-n5-p")), lvl)
				} else {
					x = white(n, lvl, testutil.Seed("agc-n5-w"))
				}
				r := newRecorder(t, cfg)
				r.feed(t, x, true)
				maxApplied := int32(0)
				for _, g := range r.applied {
					maxApplied = max(maxApplied, g)
				}
				out := testutil.DBFS(testutil.RMS(r.out[len(r.out)-10*cfg.SampleRate:]))
				below := loudestFrame(x, r.frameLen()) < float64(cfg.GateLevel)
				t.Logf("N5 %s: loudest frame below gate %v, max applied %.2f dB, output %.2f dBFS (target %d)",
					name, below, q15dB(maxApplied), out, cfg.TargetLevel)
				switch {
				case below && maxApplied > unity:
					t.Errorf("N5: applied gain %.2f dB on noise below the gate", q15dB(maxApplied))
				case !below && (out > float64(cfg.TargetLevel)+0.5 || maxApplied > r.a.maxGain):
					t.Errorf("N5: output %.2f dBFS, max applied gain %d", out, maxApplied)
				}
				checkCeiling(t, r, name, false)
			})
		}
	}
}

// TestAcceptanceCutThreshold covers G5 and G6, regression guards for the
// cut threshold registered after measurement.
func TestAcceptanceCutThreshold(t *testing.T) {
	type limit struct{ within, over float64 }
	g6 := map[float64]limit{10: {1.76, 0.43}, 11: {1.88, 0.53}, 12: {1.95, 0.62}, 13: {2.05, 0.72}}
	for _, rate := range defaultRates {
		cfg := DefaultConfig(rate)
		for _, step := range []float64{10, 11, 12, 13, 14} {
			name := fmt.Sprintf("%d/+%v", rate, step)
			t.Run(name, func(t *testing.T) {
				r, f0 := runStep(t, stepCase{name, cfg, -44, -44 + step, sineSignal, false})
				target := float64(cfg.TargetLevel)
				within := r.convergence(f0, r.frames(), target, 2, cfg)
				over := 0
				for f := f0; f < r.frames(); f++ {
					if r.level(f) > target+6 {
						over++
					}
				}
				overSec := float64(over) * r.frameSeconds(cfg)
				cuts := checkRates(t, r, name, cfg)
				t.Logf("+%v dB at %d Hz: within 2 dB %.3f s, above target+6 dB %.3f s, cuts %v",
					step, rate, within, overSec, cuts)
				if step == 14 {
					late := len(cuts) > 0 && float64(cuts[0]+1-f0)*r.frameSeconds(cfg) > 0.05
					if len(cuts) != 1 || cuts[0] < f0 || late || within > 0.05 {
						t.Errorf("G5: cuts %v (step at frame %d), within 2 dB after %.3f s", cuts, f0, within)
					}
					return
				}
				if l := g6[step]; within > l.within || overSec > l.over {
					t.Errorf("G6: within 2 dB after %.3f s (limit %.2f), above target+6 dB %.3f s (limit %.2f)",
						within, l.within, overSec, l.over)
				}
			})
		}
	}
}
