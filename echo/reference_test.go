package echo

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/fixed"
	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

// The case table lives here (refCases); reference/drivers/echo.c runs
// whatever cases.txt lists, so the two cannot drift apart.

const fixtureDir = "../testdata/speexdsp/echo"

var harnessInputs = flag.String("harness-inputs", "", "directory to write harness input files into (used by reference/fixtures.sh)")

// sortedCases returns refCases ordered by input base, so each input set is
// generated once.
func sortedCases() []refCase {
	cs := refCases()
	slices.SortStableFunc(cs, func(a, b refCase) int { return strings.Compare(a.base, b.base) })
	return cs
}

var (
	lastBase    string
	lastSignals signals
)

// inputs returns c's signals, regenerating only when the base changes.
func inputs(c refCase) signals {
	if c.base != lastBase {
		lastBase, lastSignals = c.base, inputsUncached(c)
	}
	return lastSignals
}

var (
	lastCappedBase    string
	lastCappedSignals signals
)

// refInputs returns the inputs of a reference run: the full signals, or in
// a partial run signals generated only over their first shortSeconds+1 s.
func refInputs(c refCase) signals {
	if !partialCheck() {
		return inputs(c)
	}
	if c.base != lastCappedBase {
		genCap = sec(c.rate, shortSeconds+1)
		lastCappedBase, lastCappedSignals = c.base, inputsUncached(c)
		genCap = 0
	}
	return lastCappedSignals
}

// TestCappedInputsArePrefixes checks that capped generation, used by
// partial runs, gives exactly the leading samples of the full inputs.
func TestCappedInputsArePrefixes(t *testing.T) {
	if partialCheck() {
		t.Skip("compares against full inputs; runs in the normal go test")
	}
	seen := map[string]bool{}
	for _, c := range sortedCases() {
		if seen[c.base] || c.longBlocks > 0 {
			continue
		}
		seen[c.base] = true
		full := inputs(c)
		genCap = sec(c.rate, shortSeconds+1)
		capped := inputsUncached(c)
		genCap = 0
		n := sec(c.rate, shortSeconds+1)
		if !slices.Equal(full.mic[:n], capped.mic[:n]) || !slices.Equal(full.far[:n], capped.far[:n]) ||
			len(full.mic) != len(capped.mic) || len(full.far) != len(capped.far) {
			t.Errorf("%s: capped inputs are not a prefix of the full inputs", c.base)
		}
	}
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
		s := inputs(c)
		if !written[c.base] {
			write(c.base+".mic.raw", testutil.EncodeLE(nil, s.mic))
			write(c.base+".far.raw", testutil.EncodeLE(nil, s.far))
			written[c.base] = true
		}
		write(c.name+".ops.raw", testutil.EncodeLE(nil, ops(c, s)))
		fmt.Fprintf(&list, "%s %d %d %d %s %d %d\n", c.name, c.frame, c.filter, c.rate, c.base, c.outPrefix, c.resPrefix)
	}
	write("cases.txt", []byte(list.String()))
}

// partialCheck reports whether reference tests compare only a leading part
// of each case: in -short mode and under the race detector.
func partialCheck() bool { return testing.Short() || raceEnabled }

// shortSeconds is the leading part of each case that partialCheck runs
// compare: it covers the committed prefixes (at most 0.4 s) at every rate.
const shortSeconds = 1

func TestReferenceCaseList(t *testing.T) {
	m := testutil.LoadManifest(t, fixtureDir)
	want := []string{probeCase}
	var got []string
	for _, c := range refCases() {
		want = append(want, c.name)
	}
	for _, c := range m.Cases {
		got = append(got, c.Name)
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		t.Fatalf("manifest has %d cases, the Go table %d, or the names differ", len(got), len(want))
	}
}

func TestReferenceEcho(t *testing.T) {
	m := testutil.LoadManifest(t, fixtureDir)
	for _, c := range sortedCases() {
		if partialCheck() && c.longBlocks > 0 {
			// A leading part of a long run is the same as a 60 s case's.
			c.longBlocks = 1
			c.base += "_short"
		}
		t.Run(c.name, func(t *testing.T) {
			runReferenceCase(t, m, c, true)
		})
	}
}

// sink receives one stream of a case. In a full run it feeds a chunk-hash
// checker; in a partial run it keeps the values to compare with the
// committed prefix (testutil's partial checker needs a whole chunk).
type sink[T testutil.Elem] struct {
	t      *testing.T
	m      *testutil.Manifest
	s      *testutil.Stream
	k      *testutil.StreamChecker[T]
	prefix bool
	got    []T
}

func newSink[T testutil.Elem](t *testing.T, m *testutil.Manifest, mc *testutil.Case, name string, partial bool) *sink[T] {
	st := mc.Stream(t, name)
	if !partial {
		return &sink[T]{k: testutil.NewStreamChecker[T](t, m, mc, st, false)}
	}
	return &sink[T]{t: t, m: m, s: st, prefix: st.File != nil}
}

func (k *sink[T]) put(x ...T) {
	if k.k != nil {
		for _, v := range x {
			k.k.Put(v)
		}
	} else if k.prefix {
		k.got = append(k.got, x...)
	}
}

func (k *sink[T]) done() {
	if k.k != nil {
		k.k.Done()
		return
	}
	if !k.prefix {
		return
	}
	want := testutil.ReadFile[T](k.t, k.m, k.s)
	if len(k.got) < len(want) {
		k.t.Fatalf("%s: partial run produced %d values, fewer than the committed prefix of %d", k.s.Name, len(k.got), len(want))
	}
	testutil.Compare(k.t, k.s.Name+" (committed prefix)", want, k.got[:len(want)])
}

// runReferenceCase checks every stream of case c: all chunk hashes in a
// full run, the committed prefixes of out and residual in a partial run
// (partialCheck). With residual false it skips the Residual calls and
// checks only out, which shows that Residual does not change later output.
func runReferenceCase(t *testing.T, m *testutil.Manifest, c refCase, residual bool) {
	mc := m.Case(t, c.name)
	s := refInputs(c)
	o := ops(c, s)
	ec, err := New(Config{FrameSize: c.frame, FilterLength: c.filter, SampleRate: c.rate})
	if err != nil {
		t.Fatal(err)
	}
	partial := partialCheck()
	limitOps := len(o) / 2
	if partial {
		limitOps = min(limitOps, opsForSamples(o, c.frame, sec(c.rate, shortSeconds)))
	}

	kOps := newSink[int32](t, m, mc, "ops", partial)
	kMic := newSink[int16](t, m, mc, "mic", partial)
	kFar := newSink[int16](t, m, mc, "far", partial)
	kOut := newSink[int16](t, m, mc, "out", partial)
	var kRes, kIR *sink[int32]
	if residual {
		kRes = newSink[int32](t, m, mc, "residual", partial)
		kIR = newSink[int32](t, m, mc, "ir", partial)
	}
	tr := openTrace(t, c.name, ec)

	fs := c.frame
	out := make([]int16, fs)
	res := make([]int32, ec.ResidualLen())
	mpos, fpos := 0, 0
	for k := 0; k < limitOps; k++ {
		op, arg := o[2*k], o[2*k+1]
		kOps.put(op, arg)
		var err error
		switch op {
		case opCancel:
			mic, far := s.mic[mpos:mpos+fs], s.far[fpos:fpos+fs]
			kMic.put(mic...)
			kFar.put(far...)
			err = ec.Cancel(mic, far, out)
			mpos += fs
			fpos += fs
		case opPlayback:
			far := s.far[fpos : fpos+fs]
			kFar.put(far...)
			err = ec.Playback(far)
			fpos += fs
		case opCapture:
			mic := s.mic[mpos : mpos+fs]
			kMic.put(mic...)
			err = ec.Capture(mic, out)
			mpos += fs
		case opReset:
			ec.Reset()
		case opSetRate:
			err = ec.SetSampleRate(int(arg))
		}
		if err != nil {
			t.Fatalf("op %d: %v", k, err)
		}
		if op == opCancel || op == opCapture {
			kOut.put(out...)
			if residual {
				if err := ec.Residual(res); err != nil {
					t.Fatal(err)
				}
				kRes.put(res...)
			}
		}
		if tr != nil && !tr.check(k) {
			break
		}
	}
	if residual && limitOps == len(o)/2 {
		ir := make([]int32, ec.ImpulseResponseLen())
		if err := ec.ImpulseResponse(ir); err != nil {
			t.Fatal(err)
		}
		kIR.put(ir...)
	}
	kOps.done()
	kMic.done()
	kFar.done()
	kOut.done()
	if residual {
		kRes.done()
		if limitOps == len(o)/2 {
			kIR.done()
		}
	}
	if tr != nil {
		tr.close()
	}
}

// opsForSamples returns how many ops consume at most n mic samples.
func opsForSamples(o []int32, fs, n int) int {
	used := 0
	for k := 0; k < len(o)/2; k++ {
		if op := o[2*k]; op == opCancel || op == opCapture {
			if used+fs > n {
				return k
			}
			used += fs
		}
	}
	return len(o) / 2
}

func TestReferenceWithoutResidualCalls(t *testing.T) {
	m := testutil.LoadManifest(t, fixtureDir)
	for _, c := range sortedCases() {
		if c.longBlocks > 0 || c.rate > 16000 && !strings.HasPrefix(c.name, "capture") {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			runReferenceCase(t, m, c, false)
		})
	}
}

// The pseudo-float probe of reference/drivers/echo.c: the masked operations
// over exponents where C is undefined. The lists match the driver's.

const probeCase = "probe_pseudofloat"

var (
	probeM  = []int16{-32768, -32767, -16385, -16384, -16383, -256, -2, -1, 0, 1, 2, 255, 256, 16383, 16384, 16385, 32766, 32767}
	probeA  = []int32{-2147483648, -2147483647, -1073741825, -1073741824, -65537, -65536, -32769, -32768, -1, 1, 2, 32767, 32768, 65535, 65536, 16777216, 1073741824, 2147483646, 2147483647}
	probeBE = []int16{-30, -1, 0, 1, 30}
)

func TestReferencePseudoFloatMasked(t *testing.T) {
	m := testutil.LoadManifest(t, fixtureDir)
	c := m.Case(t, probeCase)
	kx := testutil.NewStreamChecker[int32](t, m, c, c.Stream(t, "extract"), false)
	for _, mm := range probeM {
		for e := -80; e <= 40; e++ {
			a := fixed.Float{M: mm, E: int16(e)}
			kx.Put(fixed.FloatExtract32Masked(a))
			kx.Put(int32(fixed.FloatExtract16Masked(a)))
		}
	}
	kx.Done()
	kd := testutil.NewStreamChecker[int32](t, m, c, c.Stream(t, "div32_float"), false)
	for _, a := range probeA {
		for _, bm := range probeM {
			for _, be := range probeBE {
				if bm == 0 {
					continue
				}
				r := fixed.FloatDiv32FloatMasked(a, fixed.Float{M: bm, E: be})
				kd.Put(int32(r.M))
				kd.Put(int32(r.E))
			}
		}
	}
	kd.Done()
}
