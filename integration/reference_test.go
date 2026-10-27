package integration

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/echo"
	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
	"github.com/danieldonoghue/govoip-dsp/preprocess"
	"github.com/danieldonoghue/govoip-dsp/resample"
)

// The case table lives here; reference/drivers/integration.c runs whatever
// cases.txt lists, so the two cannot drift apart.

const fixtureDir = "../testdata/speexdsp/integration"

var harnessInputs = flag.String("harness-inputs", "", "directory to write harness input files into (used by reference/fixtures.sh)")

// prefixFrames is the number of leading frames committed per output stream.
const prefixFrames = 4

// refCase is one run of the C chain (docs/acceptance/integration.md, P).
type refCase struct {
	name            string
	netRate, rate   int
	frameMs, reps   int
	capture         bool
	quality, prefix int
	netFrame, frame int
	filter          int
	inputBase       string
}

func refCases() []refCase {
	var cs []refCase
	add := func(name string, netRate, rate, frameMs, reps int, capture bool) {
		cs = append(cs, refCase{
			name: name, netRate: netRate, rate: rate, frameMs: frameMs, reps: reps,
			capture: capture, quality: resample.QualityVoIP, prefix: prefixFrames,
			netFrame: netRate * frameMs / 1000, frame: rate * frameMs / 1000, filter: rate / 5,
			inputBase: fmt.Sprintf("call_%dk_%d", netRate/1000, reps),
		})
	}
	for _, mode := range []string{"cancel", "capture"} {
		for _, rate := range []int{8000, 16000} {
			for _, ms := range []int{10, 20} {
				add(fmt.Sprintf("%s_%dk_%dms", mode, rate/1000, ms), rate, rate, ms, 1, mode == "capture")
			}
		}
	}
	for _, ms := range []int{10, 20} {
		add(fmt.Sprintf("resampled_48k_16k_%dms", ms), 48000, 16000, ms, 1, false)
	}
	add("long_capture_8k_20ms", 8000, 8000, 20, 15, true)
	add("long_cancel_16k_20ms", 16000, 16000, 20, 15, false)
	return cs
}

var (
	lastBase    string
	lastSignals callSignals
)

// inputs returns the call scenario of c, regenerating only when the input
// base changes.
func inputs(c refCase) callSignals {
	if c.inputBase != lastBase {
		lastBase, lastSignals = c.inputBase, callScenario(c.netRate, c.reps)
	}
	return lastSignals
}

func sortedCases() []refCase {
	cs := refCases()
	slices.SortStableFunc(cs, func(a, b refCase) int { return strings.Compare(a.inputBase, b.inputBase) })
	return cs
}

// TestHarnessInputs writes the case list and the inputs for the driver. It
// runs only when reference/fixtures.sh passes -harness-inputs.
func TestHarnessInputs(t *testing.T) {
	if *harnessInputs == "" {
		t.Skip("writes harness inputs only when -harness-inputs is set (reference/fixtures.sh)")
	}
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(*harnessInputs, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var list strings.Builder
	written := map[string]bool{}
	for _, c := range sortedCases() {
		if !written[c.inputBase] {
			s := inputs(c)
			write(c.inputBase+".mic.raw", testutil.EncodeLE(nil, s.mic))
			write(c.inputBase+".far.raw", testutil.EncodeLE(nil, s.far))
			written[c.inputBase] = true
		}
		mode := 0
		if c.capture {
			mode = 1
		}
		fmt.Fprintf(&list, "%s %d %d %d %d %d %d %d %s\n", c.name, c.netRate, c.rate, c.frame, c.filter,
			mode, c.quality, c.prefix, c.inputBase)
	}
	write("cases.txt", []byte(list.String()))
}

func TestReferenceCaseList(t *testing.T) {
	m := testutil.LoadManifest(t, fixtureDir)
	var want, got []string
	for _, c := range refCases() {
		want = append(want, c.name)
	}
	for _, c := range m.Cases {
		got = append(got, c.Name)
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		t.Fatalf("manifest cases %v, Go table %v", got, want)
	}
}

// partialCheck reports whether the reference test compares mostly the
// committed prefixes: in -short mode and under the race detector.
func partialCheck() bool { return testing.Short() || raceEnabled }

// fullInPartial names the cases that partial runs still compare in full:
// one per echo mode and the resampled chain, each a few seconds of work.
func fullInPartial(c refCase) bool {
	return c.name == "cancel_8k_20ms" || c.name == "capture_8k_20ms" || c.name == "resampled_48k_16k_20ms"
}

// TestReferencePairing compares every stream of the Go chain with the C
// chain; partial runs compare prefixes, except the fullInPartial cases.
func TestReferencePairing(t *testing.T) {
	m := testutil.LoadManifest(t, fixtureDir)
	for _, c := range sortedCases() {
		t.Run(c.name, func(t *testing.T) {
			mc := m.Case(t, c.name)
			var params struct {
				NetRate int    `json:"net_rate"`
				Rate    int    `json:"rate"`
				Frame   int    `json:"frame_size"`
				Filter  int    `json:"filter_length"`
				Mode    string `json:"mode"`
				Quality int    `json:"quality"`
				Input   string `json:"input"`
				Frames  int    `json:"frames"`
			}
			mc.DecodeParams(t, &params)
			mode := map[bool]string{false: "cancel", true: "capture"}[c.capture]
			if params.NetRate != c.netRate || params.Rate != c.rate || params.Frame != c.frame || params.Filter != c.filter ||
				params.Mode != mode || params.Quality != c.quality || params.Input != c.inputBase {
				t.Fatalf("manifest params %+v do not match the Go case %+v", params, c)
			}
			partial := partialCheck() && !fullInPartial(c)
			if partial && c.reps > 1 {
				// One repetition covers the prefixes (TestScenarioIsPrefixSafe).
				c.reps, c.inputBase = 1, fmt.Sprintf("call_%dk_1", c.netRate/1000)
			}
			runPairing(t, m, mc, c, inputs(c), params.Frames, partial)
		})
	}
}

// sink receives one stream: a chunk-hash check in full runs, or a
// comparison with the committed prefix in partial runs.
type sink[T testutil.Elem] struct {
	t   *testing.T
	m   *testutil.Manifest
	s   *testutil.Stream
	k   *testutil.StreamChecker[T]
	got []T
}

func newSink[T testutil.Elem](t *testing.T, m *testutil.Manifest, mc *testutil.Case, name string, partial bool) *sink[T] {
	st := mc.Stream(t, name)
	if partial {
		return &sink[T]{t: t, m: m, s: st}
	}
	return &sink[T]{k: testutil.NewStreamChecker[T](t, m, mc, st, false)}
}

func (k *sink[T]) put(x ...T) {
	if k.k != nil {
		for _, v := range x {
			k.k.Put(v)
		}
		return
	}
	if k.s.File != nil && int64(len(k.got)) < k.s.File.Count {
		k.got = append(k.got, x...)
	}
}

// full reports whether a partial sink has its whole prefix.
func (k *sink[T]) full() bool {
	return k.k != nil || k.s.File == nil || int64(len(k.got)) >= k.s.File.Count
}

func (k *sink[T]) done() {
	if k.k != nil {
		k.k.Done()
		return
	}
	if k.s.File == nil {
		return
	}
	want := testutil.ReadFile[T](k.t, k.m, k.s)
	if int64(len(k.got)) < k.s.File.Count {
		k.t.Errorf("%s: produced %d values, the prefix has %d", k.s.Name, len(k.got), k.s.File.Count)
		return
	}
	testutil.Compare(k.t, k.s.Name, want, k.got[:len(want)])
}

// runPairing mirrors run_case in reference/drivers/integration.c.
func runPairing(t *testing.T, m *testutil.Manifest, mc *testutil.Case, c refCase, s callSignals, wantFrames int, partial bool) {
	ec, err := echo.New(echo.Config{FrameSize: c.frame, FilterLength: c.filter, SampleRate: c.rate})
	if err != nil {
		t.Fatal(err)
	}
	pp, err := preprocess.New(preprocess.Config{SampleRate: c.rate, FrameSize: c.frame})
	if err != nil {
		t.Fatal(err)
	}
	pp.SetVAD(true)
	var rm, rf *resample.Resampler
	if c.netRate != c.rate {
		cfg := resample.Config{Channels: 1, InRate: c.netRate, OutRate: c.rate, Quality: c.quality}
		if rm, err = resample.New(cfg); err != nil {
			t.Fatal(err)
		}
		if rf, err = resample.New(cfg); err != nil {
			t.Fatal(err)
		}
	}

	sMic := newSink[int16](t, m, mc, "mic", partial)
	sFar := newSink[int16](t, m, mc, "far", partial)
	sEcho := newSink[int16](t, m, mc, "echo_out", partial)
	sRes := newSink[int32](t, m, mc, "residual", partial)
	sOut := newSink[int16](t, m, mc, "out", partial)
	sVad := newSink[int16](t, m, mc, "vad", partial)
	sProb := newSink[int16](t, m, mc, "prob", partial)
	sinksFull := func() bool {
		return sEcho.full() && sRes.full() && sOut.full() && sVad.full() && sProb.full()
	}

	e := make([]int16, c.frame)
	res := make([]int32, ec.ResidualLen())
	rsOut := make([]int16, 4*c.frame+64)
	var pm, pf []int16
	feed := func(r *resample.Resampler, pending, x []int16) []int16 {
		if r == nil {
			return append(pending, x...)
		}
		read, n, err := r.Process(x, rsOut)
		if err != nil || read != len(x) {
			t.Fatalf("resampler read %d of %d: %v", read, len(x), err)
		}
		return append(pending, rsOut[:n]...)
	}
	frames := 0
	for k := 0; (k+1)*c.netFrame <= len(s.mic); k++ {
		if partial && sinksFull() {
			break
		}
		mic := s.mic[k*c.netFrame : (k+1)*c.netFrame]
		far := s.far[k*c.netFrame : (k+1)*c.netFrame]
		sMic.put(mic...)
		sFar.put(far...)
		pm, pf = feed(rm, pm, mic), feed(rf, pf, far)
		if len(pm) != len(pf) {
			t.Fatal("mic and far resampled to different lengths")
		}
		for len(pm) >= c.frame {
			if c.capture {
				err = ec.Playback(pf[:c.frame])
				if err == nil {
					err = ec.Capture(pm[:c.frame], e)
				}
			} else {
				err = ec.Cancel(pm[:c.frame], pf[:c.frame], e)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := ec.Residual(res); err != nil {
				t.Fatal(err)
			}
			sEcho.put(e...)
			sRes.put(res...)
			vad, err := pp.RunEcho(e, res)
			if err != nil {
				t.Fatal(err)
			}
			sOut.put(e...)
			sVad.put(int16(b2i(vad)))
			sProb.put(int16(pp.SpeechProb()))
			pm, pf = pm[c.frame:], pf[c.frame:]
			frames++
		}
	}
	if !partial && frames != wantFrames {
		t.Errorf("ran %d frames, the C chain %d", frames, wantFrames)
	}
	for _, d := range []interface{ done() }{sMic, sFar, sEcho, sRes, sOut, sVad, sProb} {
		d.done()
	}
	if partial {
		t.Logf("partial run (-short or -race): compared the committed prefixes, %d frames", frames)
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TestScenarioIsPrefixSafe checks that one repetition of the call scenario
// is the start of a longer run, so partial runs may generate only that.
func TestScenarioIsPrefixSafe(t *testing.T) {
	if partialCheck() {
		t.Skip("generates the 10-minute inputs; runs in the normal go test")
	}
	for _, rate := range []int{8000, 16000} {
		short, long := callScenario(rate, 1), callScenario(rate, 15)
		n := len(short.mic)
		if !slices.Equal(short.mic, long.mic[:n]) || !slices.Equal(short.far, long.far[:n]) {
			t.Errorf("%d Hz: one repetition is not a prefix of fifteen", rate)
		}
	}
}
