package preprocess

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

const fixtureDir = "../testdata/speexdsp/preprocess"

var harnessInputs = flag.String("harness-inputs", "", "directory to write harness input files into (used by reference/fixtures.sh)")

// partialCheck is true when long streams are compared only up to their
// first chunk: in -short mode and under the race detector.
func partialCheck() bool { return testing.Short() || raceEnabled }

func writeFile(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(*harnessInputs, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestHarnessInputs writes the case table and Go-generated inputs for the
// drivers; it runs only when reference/fixtures.sh passes -harness-inputs.
func TestHarnessInputs(t *testing.T) {
	if *harnessInputs == "" {
		t.Skip("writes harness inputs only when -harness-inputs is set (reference/fixtures.sh)")
	}
	cs, gs := buildCases()
	writeFile(t, "cases.txt", []byte(casesText(cs)))
	var b strings.Builder
	for _, c := range configs(cs) {
		fmt.Fprintf(&b, "%d %d\n", c[0], c[1])
	}
	writeFile(t, "configs.txt", []byte(b.String()))
	b.Reset()
	for _, c := range sweepConfigs() {
		fmt.Fprintf(&b, "%d %d\n", c[0], c[1])
	}
	writeFile(t, "sweep.txt", []byte(b.String()))
	b.Reset()
	for _, op := range ctlOps {
		fmt.Fprintf(&b, "%s %s\n", op[0], op[1])
	}
	writeFile(t, "ctl.txt", []byte(b.String()))
	b.Reset()
	for _, g := range gs {
		fmt.Fprintf(&b, "%s %d %d %d %d\n", g.name, g.rate, g.n, echoWarmup, echoFrames)
		far, mic := echoSignals(g)
		writeFile(t, "far_"+g.name+".raw", testutil.EncodeLE(nil, far))
		writeFile(t, "mic_"+g.name+".raw", testutil.EncodeLE(nil, mic))
	}
	writeFile(t, "groups.txt", []byte(b.String()))
	writeFile(t, "zero_input.raw", testutil.EncodeLE(nil, zeroInput()))
	writeFile(t, "zero_residual.raw", testutil.EncodeLE(nil, zeroResidual(t)))
	done := map[string]bool{}
	for _, c := range cs {
		if c.signal == "echo" {
			continue
		}
		name := inputName(c.signal, c.rate, c.frames*c.n)
		if !done[name] {
			done[name] = true
			writeFile(t, name, testutil.EncodeLE(nil, signal(c.signal, c.rate, c.frames*c.n)))
		}
	}
}

type checker[T testutil.Elem] = *testutil.StreamChecker[T]

func stream[T testutil.Elem](t *testing.T, m *testutil.Manifest, c *testutil.Case, name string, partial bool) checker[T] {
	return testutil.NewStreamChecker[T](t, m, c, c.Stream(t, name), partial)
}

func putAll[T testutil.Elem](k checker[T], x []T) {
	for _, v := range x {
		k.Put(v)
	}
}

// decisions checks a per-frame stream. Such a stream is shorter than one
// chunk, so a partial run compares only its committed prefix.
type decisions struct {
	t         *testing.T
	label     string
	k         checker[int16]
	want, got []int16
}

func newDecisions(t *testing.T, m *testutil.Manifest, c *testutil.Case, name string, partial bool) *decisions {
	if !partial {
		return &decisions{k: stream[int16](t, m, c, name, false)}
	}
	return &decisions{t: t, label: c.Name + "/" + name, want: testutil.ReadFile[int16](t, m, c.Stream(t, name))}
}

func (d *decisions) Put(v int16) {
	if d.k != nil {
		d.k.Put(v)
		return
	}
	d.got = append(d.got, v)
}

func (d *decisions) Done() {
	if d.k != nil {
		d.k.Done()
		return
	}
	n := min(len(d.want), len(d.got))
	testutil.Compare(d.t, d.label+" (committed prefix)", d.want[:n], d.got[:n])
}

func TestReferenceCaseSet(t *testing.T) {
	m := testutil.LoadManifest(t, fixtureDir)
	cs, gs := buildCases()
	want := []string{"sweep", "ctl"}
	for _, c := range cs {
		want = append(want, c.name)
	}
	for _, g := range gs {
		want = append(want, g.name)
	}
	for _, c := range configs(cs) {
		want = append(want, fmt.Sprintf("tables_%d_%d", c[0], c[1]))
	}
	var got []string
	for _, c := range m.Cases {
		got = append(got, c.Name)
	}
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(want, "\n") != strings.Join(got, "\n") {
		t.Errorf("manifest has %d cases, the table %d; they differ", len(got), len(want))
	}
}

// tableStreams adds the filterbank tables of b to their checkers.
func tableStreams(bl, br checker[int32], fl, fr checker[int16], b *filterbank) {
	for i := range b.left {
		bl.Put(int32(b.left[i]))
		br.Put(int32(b.right[i]))
	}
	putAll(fl, b.filterLeft)
	putAll(fr, b.filterRight)
}

func TestReferenceTables(t *testing.T) {
	m := testutil.LoadManifest(t, fixtureDir)
	cs, _ := buildCases()
	for _, cfg := range configs(cs) {
		name := fmt.Sprintf("tables_%d_%d", cfg[0], cfg[1])
		t.Run(name, func(t *testing.T) {
			c := m.Case(t, name)
			p, err := New(Config{SampleRate: cfg[0], FrameSize: cfg[1]})
			if err != nil {
				t.Fatal(err)
			}
			w := stream[int16](t, m, c, "window", false)
			putAll(w, p.window)
			w.Done()
			bl, br := stream[int32](t, m, c, "bank_left", false), stream[int32](t, m, c, "bank_right", false)
			fl, fr := stream[int16](t, m, c, "filter_left", false), stream[int16](t, m, c, "filter_right", false)
			tableStreams(bl, br, fl, fr, p.bank)
			bl.Done()
			br.Done()
			fl.Done()
			fr.Done()
		})
	}
}

func TestReferenceSweep(t *testing.T) {
	m := testutil.LoadManifest(t, fixtureDir)
	c := m.Case(t, "sweep")
	cfgs := stream[int32](t, m, c, "configs", false)
	bl, br := stream[int32](t, m, c, "bank_left", false), stream[int32](t, m, c, "bank_right", false)
	fl, fr := stream[int16](t, m, c, "filter_left", false), stream[int16](t, m, c, "filter_right", false)
	for _, cfg := range sweepConfigs() {
		cfgs.Put(int32(cfg[0]))
		cfgs.Put(int32(cfg[1]))
		tableStreams(bl, br, fl, fr, newFilterbank(nbBands, int32(cfg[0]), cfg[1]))
	}
	for _, k := range []checker[int32]{cfgs, bl, br} {
		k.Done()
	}
	fl.Done()
	fr.Done()
}

func TestReferenceCtl(t *testing.T) {
	m := testutil.LoadManifest(t, fixtureDir)
	c := m.Case(t, "ctl")
	k := stream[int32](t, m, c, "get", false)
	p, err := New(Config{SampleRate: 16000, FrameSize: 160})
	if err != nil {
		t.Fatal(err)
	}
	all := func() {
		for _, v := range []int32{int32(b2i(p.Denoise())), int32(b2i(p.VAD())), int32(p.ProbStart()), int32(p.ProbContinue()),
			p.NoiseSuppress(), p.EchoSuppress(), p.EchoSuppressActive(), int32(p.PSDLen()), int32(p.SpeechProb())} {
			k.Put(v)
		}
	}
	all()
	for _, op := range ctlOps {
		var v int64
		fmt.Sscan(op[1], &v)
		switch op[0] {
		case "prob_start":
			p.SetProbStart(int(v))
			k.Put(int32(p.ProbStart()))
		case "prob_continue":
			p.SetProbContinue(int(v))
			k.Put(int32(p.ProbContinue()))
		case "noise_suppress":
			p.SetNoiseSuppress(int32(v))
			k.Put(p.NoiseSuppress())
		case "echo_suppress":
			p.SetEchoSuppress(int32(v))
			k.Put(p.EchoSuppress())
		case "echo_suppress_active":
			p.SetEchoSuppressActive(int32(v))
			k.Put(p.EchoSuppressActive())
		}
	}
	p.SetDenoise(false)
	p.SetVAD(true)
	all()
	k.Done()
}

// newCase makes a Preprocessor configured as the driver configures c.
func newCase(t testing.TB, c refCase) *Preprocessor {
	p, err := New(Config{SampleRate: c.rate, FrameSize: c.n})
	if err != nil {
		t.Fatal(err)
	}
	p.SetDenoise(c.denoise)
	p.SetVAD(c.vad)
	p.SetNoiseSuppress(c.ns)
	p.SetEchoSuppress(c.es)
	p.SetEchoSuppressActive(c.esa)
	if c.pStart != unset {
		p.SetProbStart(c.pStart)
	}
	if c.pCont != unset {
		p.SetProbContinue(c.pCont)
	}
	return p
}

// estimateFrame reports whether the estimate mode calls EstimateUpdate on
// frame f instead of Run (reference/drivers/preprocess.c).
func estimateFrame(f int) bool { return f < 50 || f%4 == 3 }

// echoOff reports whether the echo_alt mode runs frame f without residual.
func echoOff(f int) bool { return f/25%2 == 1 }

func TestReferenceRun(t *testing.T) {
	m := testutil.LoadManifest(t, fixtureDir)
	cs, gs := buildCases()
	type groupData struct {
		in       []int16
		residual []int32
	}
	groups := map[string]groupData{}
	for _, g := range gs {
		c := m.Case(t, g.name)
		// The echo canceller inputs are Go-generated; check they are the
		// ones the driver ran.
		far, mic := echoSignals(g)
		kf, km := stream[int16](t, m, c, "far", false), stream[int16](t, m, c, "mic", false)
		putAll(kf, far)
		putAll(km, mic)
		kf.Done()
		km.Done()
		groups[g.name] = groupData{
			in:       testutil.ReadFile[int16](t, m, c.Stream(t, "echo_in")),
			residual: testutil.ReadFile[int32](t, m, c.Stream(t, "residual")),
		}
	}
	for _, rc := range cs {
		t.Run(rc.name, func(t *testing.T) {
			t.Parallel()
			mc := m.Case(t, rc.name)
			n := rc.n
			var input []int16
			var residual []int32
			if rc.group != "" {
				g := groups[rc.group]
				if len(g.in) != rc.frames*n || len(g.residual) != rc.frames*n {
					t.Fatalf("echo group %s holds %d samples and %d residual values, want %d", rc.group, len(g.in), len(g.residual), rc.frames*n)
				}
				input, residual = g.in, g.residual
			} else {
				input = signal(rc.signal, rc.rate, rc.frames*n)
			}
			// Only the 60 s runs exceed one chunk, so only they are shortened.
			partial := partialCheck() && int64(len(input)) > mc.Stream(t, "out").Chunk
			in := stream[int16](t, m, mc, "in", partial)
			out := stream[int16](t, m, mc, "out", partial)
			vad := newDecisions(t, m, mc, "vad", partial)
			prob := newDecisions(t, m, mc, "prob", partial)
			var psd, noisePSD, pct checker[int32]
			var buf []int32
			if rc.mode == "psd" {
				psd, noisePSD, pct = stream[int32](t, m, mc, "psd", false), stream[int32](t, m, mc, "noise_psd", false), stream[int32](t, m, mc, "prob_pct", false)
				buf = make([]int32, n)
			}
			p := newCase(t, rc)
			frame := make([]int16, n)
			for f := range rc.frames {
				if partial && out.Count() >= mc.Stream(t, "out").Chunk {
					break
				}
				copy(frame, input[f*n:(f+1)*n])
				putAll(in, frame)
				v := int16(-1)
				var ok bool
				var err error
				switch {
				case rc.mode == "estimate" && estimateFrame(f):
					err = p.EstimateUpdate(frame)
				case rc.mode == "echo" || rc.mode == "echo_alt" && !echoOff(f):
					ok, err = p.RunEcho(frame, residual[f*n:(f+1)*n])
					v = int16(b2i(ok))
				default:
					ok, err = p.Run(frame)
					v = int16(b2i(ok))
				}
				if err != nil {
					t.Fatal(err)
				}
				putAll(out, frame)
				vad.Put(v)
				prob.Put(p.speechProb)
				if psd != nil {
					if err := p.PSD(buf); err != nil {
						t.Fatal(err)
					}
					putAll(psd, buf)
					if err := p.NoisePSD(buf); err != nil {
						t.Fatal(err)
					}
					putAll(noisePSD, buf)
					pct.Put(int32(p.SpeechProb()))
				}
			}
			in.Done()
			out.Done()
			vad.Done()
			prob.Done()
			if psd != nil {
				psd.Done()
				noisePSD.Done()
				pct.Done()
			}
		})
	}
}
