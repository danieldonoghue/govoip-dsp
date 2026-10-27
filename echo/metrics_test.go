package echo

import (
	"flag"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

// Scenario measurements (docs/acceptance/echo.md), pinned in
// testdata/metrics.golden; -update-metrics rewrites it after an intended change.

var updateMetrics = flag.Bool("update-metrics", false, "rewrite testdata/metrics.golden")

const (
	metricsGolden = "testdata/metrics.golden"
	activeRMS     = 103.6 // -50 dBFS RMS on the 16-bit scale
	convergeDB    = 10
)

type window struct{ lo, hi int }

// runScenario returns the output of a fresh Canceller over s, driven by
// the op stream of case c.
func runScenario(t testing.TB, c refCase, s signals) []int16 {
	ec := mustNew(t, c.frame, c.filter, c.rate)
	o := ops(c, s)
	fs := c.frame
	out := make([]int16, 0, len(s.mic))
	buf := make([]int16, fs)
	mpos, fpos := 0, 0
	for k := 0; k < len(o)/2; k++ {
		switch o[2*k] {
		case opCancel:
			_ = ec.Cancel(s.mic[mpos:mpos+fs], s.far[fpos:fpos+fs], buf)
			out = append(out, buf...)
			mpos += fs
			fpos += fs
		case opPlayback:
			_ = ec.Playback(s.far[fpos : fpos+fs])
			fpos += fs
		case opCapture:
			_ = ec.Capture(s.mic[mpos:mpos+fs], buf)
			out = append(out, buf...)
			mpos += fs
		case opReset:
			ec.Reset()
		case opSetRate:
			_ = ec.SetSampleRate(int(o[2*k+1]))
		}
	}
	return out
}

func energy(x []int16) float64 {
	var e float64
	for _, v := range x {
		e += float64(float64(v) * float64(v))
	}
	return e
}

func rmsOf(x []int16) float64 {
	if len(x) == 0 {
		return 0
	}
	return math.Sqrt(energy(x) / float64(len(x)))
}

func db(num, den float64) float64 {
	if den == 0 {
		return math.Inf(1)
	}
	if num == 0 {
		return math.Inf(-1)
	}
	return 10 * math.Log10(num/den)
}

// windows splits [lo, hi) seconds into 0.5 s windows within n samples.
func windows(rate int, lo, hi float64, n int) []window {
	var w []window
	step := rate / 2
	for a := sec(rate, lo); a+step <= min(sec(rate, hi), n); a += step {
		w = append(w, window{a, a + step})
	}
	return w
}

type measurer struct {
	rate int
	s    signals
	out  []int16
}

// farOnly reports whether w is an ERLE window: no near end, echo above
// -50 dBFS RMS.
func (m measurer) farOnly(w window) bool {
	return energy(m.s.near[w.lo:w.hi]) == 0 && rmsOf(m.s.echo[w.lo:w.hi]) > activeRMS
}

// erle pools the far-only windows of [lo, hi) seconds.
func (m measurer) erle(lo, hi float64) (float64, bool) {
	var num, den float64
	found := false
	for _, w := range windows(m.rate, lo, hi, len(m.out)) {
		if m.farOnly(w) {
			num += energy(m.s.mic[w.lo:w.hi])
			den += energy(m.out[w.lo:w.hi])
			found = true
		}
	}
	return db(num, den), found
}

// convergence is the start, in seconds after from, of the first far-only
// window in [from, to) with ERLE of at least 10 dB, or -1 if none.
func (m measurer) convergence(from, to float64) float64 {
	for _, w := range windows(m.rate, from, to, len(m.out)) {
		if m.farOnly(w) && db(energy(m.s.mic[w.lo:w.hi]), energy(m.out[w.lo:w.hi])) >= convergeDB {
			return float64(w.lo-sec(m.rate, from)) / float64(m.rate)
		}
	}
	return -1
}

type figures struct {
	steady, converge float64
	hasSteady        bool
	nearChange       float64 // near-only windows: out against ref (dB)
	nearRaw          float64 // near-only windows: out against the raw near end (dB)
	hasNear          bool
	dtOut, dtIn      float64 // double talk: near-to-residual ratio of out and of the mic
	hasDT            bool
	after, reconv    float64 // path_sudden: ERLE and convergence after the change
	hasAfter         bool
	erleAll          float64 // lownoise: ERLE over every window, without the -50 dBFS threshold
	hasAll           bool
}

// steadyRange is the last 4 s of far-end-only activity before a later
// condition change; convFrom is where convergence is counted from.
var (
	steadyRange = map[string][2]float64{
		"farend": {12, 16}, "ir_short": {8, 12}, "ir_long": {12, 16}, "lownoise": {12, 16},
		"path_sudden": {6, 10}, "path_gradual": {16, 20}, "misaligned_late": {12, 16},
		"misaligned_early": {12, 16}, "doubletalk": {4, 8}, "silence": {12, 16}, "diverge": {16, 20},
		"reset": {12, 16}, "capture": {12, 16}, "rate_switch": {12, 16},
	}
	convFrom = map[string]float64{"reset": 8, "diverge": 15}
)

func measure(t testing.TB, c refCase, s signals) figures {
	out := runScenario(t, c, s)
	m := measurer{rate: c.rate, s: s, out: out}
	var f figures
	if r, ok := steadyRange[c.scenario]; ok {
		f.steady, f.hasSteady = m.erle(r[0], r[1])
		from := convFrom[c.scenario]
		f.converge = m.convergence(from, from+8)
	}
	if c.scenario == "lownoise" {
		// Not pre-registered: the echo is below the window threshold.
		var num, den float64
		for _, w := range windows(c.rate, 12, 16, len(out)) {
			num += energy(s.mic[w.lo:w.hi])
			den += energy(out[w.lo:w.hi])
		}
		f.erleAll, f.hasAll = db(num, den), true
	}
	if c.scenario == "path_sudden" {
		f.after, _ = m.erle(16, 20)
		f.reconv, f.hasAfter = m.convergence(10, 20), true
	}
	if c.scenario == "nearend" || c.scenario == "doubletalk" {
		// ref: a fresh canceller fed the near end alone, far end silent.
		rc := c
		rc.scenario = "nearend"
		ref := runScenario(t, rc, signals{mic: s.near, far: make([]int16, len(s.near))})
		var outE, refE, rawE, dtRef, dtErr, dtNear, dtMicErr float64
		for _, w := range windows(c.rate, 0, math.MaxInt32, len(out)) {
			if rmsOf(s.near[w.lo:w.hi]) <= activeRMS {
				continue
			}
			if rmsOf(s.echo[w.lo:w.hi]) <= activeRMS {
				outE += energy(out[w.lo:w.hi])
				refE += energy(ref[w.lo:w.hi])
				rawE += energy(s.near[w.lo:w.hi])
				continue
			}
			for i := w.lo; i < w.hi; i++ {
				d := float64(out[i]) - float64(ref[i])
				dtErr += float64(d * d)
				dm := float64(s.mic[i]) - float64(s.near[i])
				dtMicErr += float64(dm * dm)
			}
			dtRef += energy(ref[w.lo:w.hi])
			dtNear += energy(s.near[w.lo:w.hi])
			f.hasDT = true
		}
		if refE > 0 {
			f.nearChange, f.nearRaw, f.hasNear = db(outE, refE), db(outE, rawE), true
		}
		if f.hasDT {
			f.dtOut, f.dtIn = db(dtRef, dtErr), db(dtNear, dtMicErr)
		}
	}
	return f
}

func (f figures) String() string {
	var b strings.Builder
	if f.hasSteady {
		fmt.Fprintf(&b, " erle=%.2f converge=%.1f", f.steady, f.converge)
	}
	if f.hasAll {
		fmt.Fprintf(&b, " erle_unthresholded=%.2f", f.erleAll)
	}
	if f.hasAfter {
		fmt.Fprintf(&b, " erle_after=%.2f reconverge=%.1f", f.after, f.reconv)
	}
	if f.hasNear {
		fmt.Fprintf(&b, " near_change=%.2f near_vs_raw=%.2f", f.nearChange, f.nearRaw)
	}
	if f.hasDT {
		fmt.Fprintf(&b, " dt_out=%.2f dt_in=%.2f dt_gain=%.2f", f.dtOut, f.dtIn, f.dtOut-f.dtIn)
	}
	return b.String()
}

// TestScenarioMeasurements measures every scenario in every configuration,
// checks the sanity floors at 8 and 16 kHz and compares the figures with
// testdata/metrics.golden. Under -short and -race it measures one
// configuration.
func TestScenarioMeasurements(t *testing.T) {
	var lines []string
	failed := 0
	for _, c := range sortedCases() {
		if c.longBlocks > 0 || raceEnabled && c.scenario != "farend" {
			continue // under -race only farend: run time, no race coverage
		}
		if partialCheck() && !(c.rate == 16000 && c.frameMs == 10 && c.filterMs == 128) {
			continue
		}
		f := measure(t, c, inputs(c))
		lines = append(lines, c.name+f.String())
		if c.rate > 16000 {
			continue
		}
		// Sanity floors (docs/acceptance/echo.md).
		switch c.scenario {
		case "farend", "ir_short":
			if !f.hasSteady || f.steady < 10 || f.converge < 0 || f.converge > 8 {
				t.Errorf("%s: steady-state ERLE %.2f dB, convergence %.1f s; floor 10 dB within 8 s", c.name, f.steady, f.converge)
				failed++
			}
		case "nearend":
			if !f.hasNear || math.Abs(f.nearChange) > 2 {
				t.Errorf("%s: near-end level change %.2f dB; floor ±2 dB", c.name, f.nearChange)
				failed++
			}
		case "doubletalk":
			if !f.hasDT || f.dtOut-f.dtIn < 3 {
				t.Errorf("%s: double-talk gain %.2f dB; floor 3 dB", c.name, f.dtOut-f.dtIn)
				failed++
			}
		}
	}
	got := strings.Join(lines, "\n") + "\n"
	if *updateMetrics {
		if partialCheck() {
			t.Fatal("-update-metrics needs a full run (no -short, no -race)")
		}
		if err := os.WriteFile(metricsGolden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	data, err := os.ReadFile(metricsGolden)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		want[strings.Fields(l)[0]] = l
	}
	for _, l := range lines {
		if w := want[strings.Fields(l)[0]]; w != l {
			t.Errorf("figures changed:\n got  %s\n want %s", l, w)
		}
	}
	if !partialCheck() && len(lines) != len(want) {
		t.Errorf("%d measured cases, %d in %s", len(lines), len(want), metricsGolden)
	}
}

// TestDivergeReachesReset checks that the diverge scenario reaches the
// reference's internal reset (criterion 4 of the sanity floors).
func TestDivergeReachesReset(t *testing.T) {
	for _, c := range sortedCases() {
		if c.scenario != "diverge" || c.rate != 16000 || c.frameMs != 20 || c.filterMs != 128 {
			continue
		}
		s := inputs(c)
		ec := mustNew(t, c.frame, c.filter, c.rate)
		out := make([]int16, c.frame)
		resets := 0
		for k := 0; (k+1)*c.frame <= len(s.mic); k++ {
			before := ec.st.cancelCount
			_ = ec.Cancel(s.mic[k*c.frame:(k+1)*c.frame], s.far[k*c.frame:(k+1)*c.frame], out)
			if ec.st.cancelCount != before+1 {
				resets++
			}
		}
		if resets == 0 {
			t.Fatalf("%s never reached the internal reset", c.name)
		}
		t.Logf("%s: %d internal resets", c.name, resets)
	}
}

// TestSpeechScenario measures LibriSpeech far and near ends at 16 kHz: far
// end only for the first half, then double talk. It skips when the speech
// files are absent.
func TestSpeechScenario(t *testing.T) {
	files := testutil.SpeechFiles(t)
	if len(files) < 2 {
		t.Skip("needs two speech files")
	}
	cat := func(paths []string, n int) []int16 {
		var x []int16
		for _, p := range paths {
			x = append(x, testutil.LoadSpeech(t, p)...)
			if len(x) >= n {
				break
			}
		}
		return x[:min(n, len(x))]
	}
	const rate = 16000
	n := 30 * rate
	far := cat(files[0:len(files)/2], n)
	near := cat(files[len(files)/2:], n)
	n = min(len(far), len(near))
	far, near = far[:n], near[:n]
	if n < 20*rate {
		t.Skipf("only %d samples of speech", n)
	}
	echo := convolve(far, pathA(rate))
	near = gate(near, [2]int{n / 2, n})
	noise := testutil.WhiteNoise(n, noiseA, testutil.Seed("speech/noise"))
	s := signals{mic: mix(echo, near, noise), far: far, near: near, echo: echo}
	for _, cfg := range []struct{ frameMs, filterMs int }{{10, 128}, {20, 256}} {
		c := refCase{name: fmt.Sprintf("speech_16k_%dms_%dms", cfg.frameMs, cfg.filterMs), scenario: "speech",
			rate: rate, frame: msToSamples(rate, cfg.frameMs), filter: msToSamples(rate, cfg.filterMs)}
		out := runScenario(t, c, s)
		m := measurer{rate: rate, s: s, out: out}
		half := float64(n/2) / rate
		steady, _ := m.erle(half-4, half)
		conv := m.convergence(0, half)
		// Double talk over the second half.
		rc := c
		ref := runScenario(t, rc, signals{mic: near, far: make([]int16, n)})
		var dtRef, dtErr, dtNear, dtMicErr float64
		for _, w := range windows(rate, half, float64(n)/rate, n) {
			if rmsOf(near[w.lo:w.hi]) <= activeRMS || rmsOf(echo[w.lo:w.hi]) <= activeRMS {
				continue
			}
			for i := w.lo; i < w.hi; i++ {
				d := float64(out[i]) - float64(ref[i])
				dtErr += float64(d * d)
				dm := float64(s.mic[i]) - float64(near[i])
				dtMicErr += float64(dm * dm)
			}
			dtRef += energy(ref[w.lo:w.hi])
			dtNear += energy(near[w.lo:w.hi])
		}
		t.Logf("%s (%d files, %.1f s): erle=%.2f converge=%.1f dt_out=%.2f dt_in=%.2f dt_gain=%.2f",
			c.name, len(files), float64(n)/rate, steady, conv, db(dtRef, dtErr), db(dtNear, dtMicErr), db(dtRef, dtErr)-db(dtNear, dtMicErr))
	}
}

// TestCaptureEqualsDelayedCancel checks the buffered-mode alignment of the
// package doc: with Playback and Capture strictly alternating, each Capture
// equals Cancel with the frame of the second-most-recent Playback, in both
// call orders. It also reports the ERLE of buffered mode on the capture
// scenario, whose echo arrives two frames after the far end.
func TestCaptureEqualsDelayedCancel(t *testing.T) {
	for _, rate := range []int{8000, 16000} {
		for _, playFirst := range []bool{true, false} {
			fs := rate / 100
			s := scenario("capture", rate, 128, fs)
			buffered := mustNew(t, fs, rate/8, rate)
			direct := mustNew(t, fs, rate/8, rate)
			o1, o2 := make([]int16, fs), make([]int16, fs)
			zero := make([]int16, fs)
			out := make([]int16, 0, len(s.mic))
			var played [][]int16
			for k := 0; (k+1)*fs <= len(s.mic); k++ {
				play := func() {
					f := s.far[k*fs : (k+1)*fs]
					if err := buffered.Playback(f); err != nil {
						t.Fatal(err)
					}
					if k > 0 || !playFirst {
						played = append(played, f) // the first call, before any Capture, is discarded
					}
				}
				if playFirst {
					play()
				}
				if err := buffered.Capture(s.mic[k*fs:(k+1)*fs], o1); err != nil {
					t.Fatal(err)
				}
				// The queue starts with two frames of silence, then holds the
				// accepted Playback frames in order.
				far := zero
				if k >= 2 {
					far = played[k-2]
				}
				if err := direct.Cancel(s.mic[k*fs:(k+1)*fs], far, o2); err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(o1, o2) {
					t.Fatalf("%d Hz, playback first %v, frame %d: Capture differs from Cancel with the second-most-recent Playback frame", rate, playFirst, k)
				}
				out = append(out, o1...)
				if !playFirst {
					play()
				}
			}
			m := measurer{rate: rate, s: s, out: out}
			e, _ := m.erle(12, 16)
			t.Logf("%d Hz buffered mode, playback first %v: ERLE %.2f dB, convergence %.1f s", rate, playFirst, e, m.convergence(0, 8))
		}
	}
}
