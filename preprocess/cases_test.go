package preprocess

import (
	"fmt"
	"math"
	"strings"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

// The fixture case table (docs/acceptance/preprocess.md); TestHarnessInputs
// writes it for reference/drivers/preprocess.c, so it is the only copy.

// unset leaves a probability threshold at its default: setting the default
// percentage would not reproduce the default Q15 value.
const unset = -1

type refCase struct {
	name          string
	rate, n       int
	signal        string
	frames        int
	denoise, vad  bool
	ns, es, esa   int32
	pStart, pCont int
	mode          string // run, estimate, psd, echo, echo_alt
	group         string // echo group, for the echo modes
}

type echoGroup struct {
	name    string
	rate, n int
}

const (
	echoWarmup = 200 // frames the C echo canceller runs before recording
	echoFrames = 100 // recorded frames
)

var mvpConfigs = [][2]int{
	{8000, 80}, {8000, 160}, {16000, 160}, {16000, 320},
	{32000, 320}, {32000, 640}, {48000, 480}, {48000, 960},
}

var extraConfigs = [][2]int{
	{11025, 110}, {22050, 441}, {44100, 441}, {8000, 1}, {8000, 2},
	{48000, 2048}, {16000, 256}, {65535, 256}, {MinSampleRate, 64},
}

var edgeSignals = []string{"silence", "white_full", "tiny", "square_full", "sine_full", "impulses", "dc", "min", "steps", "noise_pink"}

func framesFor(rate, n int, seconds float64) int {
	return max(1, int(seconds*float64(rate))/n)
}

func base(name string, rate, n int, signal string, seconds float64) refCase {
	return refCase{
		name: name, rate: rate, n: n, signal: signal, frames: framesFor(rate, n, seconds),
		denoise: true, vad: true, ns: noiseSuppressDef, es: echoSuppressDef, esa: echoSuppressActDef,
		pStart: unset, pCont: unset, mode: "run",
	}
}

func buildCases() ([]refCase, []echoGroup) {
	var cs []refCase
	var gs []echoGroup
	for _, cfg := range mvpConfigs {
		rate, n := cfg[0], cfg[1]
		pre := fmt.Sprintf("%d_%d_", rate, n)
		cs = append(cs, base(pre+"long", rate, n, "speech_noise", 60))
		for _, dv := range [][2]bool{{true, false}, {false, true}, {false, false}} {
			c := base(fmt.Sprintf("%sdenoise%v_vad%v", pre, dv[0], dv[1]), rate, n, "speech_noise", 10)
			c.denoise, c.vad = dv[0], dv[1]
			cs = append(cs, c)
		}
		for _, ns := range []int32{0, -5, -30, -70, 25, math.MinInt32, math.MaxInt32} {
			c := base(fmt.Sprintf("%sns%d", pre, ns), rate, n, "speech_noise", 10)
			c.ns = ns
			cs = append(cs, c)
		}
		for _, pr := range [][2]int{{0, 0}, {100, 100}, {60, 10}, {77, 77}, {84, 84}} {
			c := base(fmt.Sprintf("%sprob%d_%d", pre, pr[0], pr[1]), rate, n, "speech_noise", 10)
			c.pStart, c.pCont = pr[0], pr[1]
			cs = append(cs, c)
		}
		for _, s := range edgeSignals {
			cs = append(cs, base(pre+s, rate, n, s, 3))
		}
		c := base(pre+"estimate", rate, n, "speech_noise", 10)
		c.mode = "estimate"
		cs = append(cs, c)
		c = base(pre+"psd", rate, n, "speech_noise", 2)
		c.mode = "psd"
		cs = append(cs, c)

		g := echoGroup{name: fmt.Sprintf("echo_%d_%d", rate, n), rate: rate, n: n}
		gs = append(gs, g)
		ev := func(suffix string, f func(*refCase)) {
			c := base(g.name+"_"+suffix, rate, n, "", 0)
			c.frames, c.signal, c.mode, c.group = echoFrames, "echo", "echo", g.name
			f(&c)
			cs = append(cs, c)
		}
		ev("default", func(*refCase) {})
		ev("es20_5", func(c *refCase) { c.es, c.esa = -20, -5 })
		ev("es60_30", func(c *refCase) { c.es, c.esa = -60, -30 })
		ev("ns30_es10", func(c *refCase) { c.ns, c.es, c.esa = -30, -10, -10 })
		ev("nodenoise", func(c *refCase) { c.denoise = false })
		ev("alternate", func(c *refCase) { c.mode = "echo_alt" })
	}
	// Past 10000 and 20000 frames update_noise_prob and the nb_adapt cap
	// take other branches, which the 60 s runs do not reach.
	cs = append(cs, base("8000_80_vlong", 8000, 80, "speech_noise", 210),
		base("16000_320_pink_long", 16000, 320, "noise_pink", 120))
	for _, cfg := range extraConfigs {
		rate, n := cfg[0], cfg[1]
		cs = append(cs, base(fmt.Sprintf("%d_%d_extra", rate, n), rate, n, "speech_noise", 3))
	}
	return cs, gs
}

// configs returns every (rate, n) the cases use, in first-use order.
func configs(cs []refCase) [][2]int {
	seen := map[[2]int]bool{}
	var out [][2]int
	for _, c := range cs {
		k := [2]int{c.rate, c.n}
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// sweepConfigs is the filterbank table sweep.
func sweepConfigs() [][2]int {
	rates := []int{MinSampleRate, MinSampleRate + 1, 5, 100, 999, 11025, 22050, 44100, 65534, MaxSampleRate}
	for r := 1000; r <= 65000; r += 1000 {
		rates = append(rates, r)
	}
	var out [][2]int
	for _, n := range []int{1, 7, 64, 160, 441, 2048} {
		for _, r := range rates {
			out = append(out, [2]int{r, n})
		}
	}
	return out
}

// ctlOps is the settings case: each op sets a value and reads it back.
var ctlOps = func() [][2]string {
	vals := []int64{0, 1, -1, 15, -15, 35, 99, 100, 101, -101, 1000, 32767, 32768, -32768, -32769, math.MaxInt32, math.MinInt32}
	var ops [][2]string
	for _, op := range []string{"prob_start", "prob_continue", "noise_suppress", "echo_suppress", "echo_suppress_active"} {
		for _, v := range vals {
			ops = append(ops, [2]string{op, fmt.Sprint(v)})
		}
	}
	return ops
}()

func inputName(signal string, rate, samples int) string {
	return fmt.Sprintf("%s_%d_%d.raw", signal, rate, samples)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// casesText is cases.txt for the driver: one case per line.
func casesText(cs []refCase) string {
	var b strings.Builder
	for _, c := range cs {
		group := c.group
		if group == "" {
			group = "-"
		}
		file := "-"
		if c.signal != "echo" {
			file = inputName(c.signal, c.rate, c.frames*c.n)
		}
		fmt.Fprintf(&b, "%s %d %d %s %d %d %d %d %d %d %d %d %s %s\n", c.name, c.rate, c.n, file, c.frames,
			b2i(c.denoise), b2i(c.vad), c.ns, c.es, c.esa, c.pStart, c.pCont, c.mode, group)
	}
	return b.String()
}

// signal generates a named input of n samples at rate.
func signal(name string, rate, n int) []int16 {
	seed := testutil.Seed(fmt.Sprintf("%s_%d_%d", name, rate, n))
	fr := float64(rate)
	switch name {
	case "speech_noise":
		return testutil.Add(testutil.SpeechLike(n, fr, 12000, seed), testutil.PinkNoise(n, 1500, seed+1))
	case "noise_pink":
		return testutil.PinkNoise(n, 3000, seed)
	case "silence":
		return testutil.Silence(n)
	case "white_full":
		return testutil.WhiteNoise(n, 32767, seed)
	case "tiny":
		return testutil.WhiteNoise(n, 2, seed)
	case "square_full", "impulses", "dc", "min":
		x := make([]int16, n)
		period := max(2, rate/250)
		for i := range x {
			switch name {
			case "square_full":
				x[i] = 32767
				if i%period >= period/2 {
					x[i] = -32768
				}
			case "impulses":
				if k := max(1, rate/10); i%k == 0 {
					x[i] = 32767
					if i/k%2 == 1 {
						x[i] = -32768
					}
				}
			case "dc":
				x[i] = 32767
			case "min":
				x[i] = -32768
			}
		}
		return x
	case "sine_full":
		return testutil.Sine(n, fr, fr/16, 32767, 0)
	case "steps":
		q := n / 4
		x := testutil.Silence(n)
		copy(x[q:], testutil.PinkNoise(q, 1000, seed))
		copy(x[2*q:], testutil.WhiteNoise(q, 30000, seed+1))
		return x
	}
	panic("unknown signal " + name)
}

// echoSignals returns an echo group's far end, and the microphone: an 8 ms
// echo, low noise, and near-end speech from mid-recording (double talk).
func echoSignals(g echoGroup) (far, mic []int16) {
	total := (echoWarmup + echoFrames) * g.n
	seed := testutil.Seed(g.name)
	far = testutil.SpeechLike(total, float64(g.rate), 16000, seed)
	taps := max(1, g.rate/125)
	delay := g.rate / 500
	r := testutil.SplitMix64{State: seed + 2}
	ir := make([]int32, taps)
	for k := range ir {
		ir[k] = int32(r.Intn(32769)-16384) * int32(taps-k) / int32(taps)
	}
	near := testutil.SpeechLike(total, float64(g.rate), 6000, seed+3)
	noise := testutil.PinkNoise(total, 300, seed+4)
	mic = make([]int16, total)
	for i := range mic {
		var acc int64
		for k, h := range ir {
			if j := i - delay - k; j >= 0 {
				acc += int64(h) * int64(far[j])
			}
		}
		v := acc>>16 + int64(noise[i])
		if i >= (echoWarmup+echoFrames/2)*g.n {
			v += int64(near[i])
		}
		mic[i] = int16(max(-32768, min(32767, v)))
	}
	return far, mic
}
