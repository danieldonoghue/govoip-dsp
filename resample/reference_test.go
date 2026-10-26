package resample

import (
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

// Replays reference/drivers/resample.c. The signal and call generators must
// stay identical to the driver's; TestReferenceSignal checks the signals,
// and the trace streams check every call.

const fixtureDir = "../testdata/speexdsp/resample"

const (
	signalLen      = 262144
	mixedCalls     = 3
	maxCalls       = 200000
	outCap         = 1 << 20
	outStrideSplit = 3
)

// Trace record types and event operations, as in the driver.
const (
	tProcess = 0
	tInit    = 10
	tSplit   = 100

	eQuality  = 1
	eRate     = 2
	eRateFrac = 3
	eReset    = 4
	eSkip     = 5
)

// sig16 is the driver's sig16.
type sig16 struct {
	r                          testutil.SplitMix64
	left, typ, pos, half, step uint32
}

func (s *sig16) next() int16 {
	if s.left == 0 {
		s.left = 64 + uint32(s.r.Next()%512)
		s.typ = uint32(s.r.Next() % 6)
		s.pos = 0
		s.half = 1 + uint32(s.r.Next()%32)
		s.step = uint32(s.r.Next() % 2048)
	}
	var v int16
	switch s.typ {
	case 0:
		v = int16(uint16(s.r.Next() >> 48))
	case 1:
		v = -32768
		if (s.pos/s.half)&1 != 0 {
			v = 32767
		}
	case 2:
		v = int16(int32(s.r.Next()%33) - 16)
	case 3:
	case 4:
		if s.pos%(s.half+5) == 0 {
			v = 32767
		}
	default:
		v = int16(uint16(s.pos * s.step))
	}
	s.pos++
	s.left--
	return v
}

var specials = [15]float32{
	float32(math.Inf(1)), float32(math.Inf(-1)), float32(math.Copysign(0, -1)), 0.5, -0.5,
	0.99999994, -0.99999994, 32766.0, 32766.5, 32767.0, -32767.0, -32767.5, -32768.0, 1e30, -1e30}

var signal16, signalF = makeSignals()

func makeSignals() ([]int16, []float32) {
	a := make([]int16, signalLen)
	s := sig16{r: testutil.SplitMix64{State: testutil.Seed("signal")}}
	for i := range a {
		a[i] = s.next()
	}
	b := make([]float32, signalLen)
	r := testutil.SplitMix64{State: testutil.Seed("signal_f32")}
	for i := range b {
		if i%97 == 96 {
			b[i] = specials[r.Next()%15]
		} else {
			b[i] = float32(int32(r.Next()%100001)-50000) / 1.5
		}
	}
	return a, b
}

// drawCall is the driver's draw_call.
func drawCall(r *testutil.SplitMix64, num, den int) (in, out uint32) {
	k := r.Next() % 16
	switch {
	case k == 0:
	case k <= 2:
		in = 1 + uint32(r.Next()%4)
	case k <= 4:
		in = 161 + uint32(r.Next()%1000)
	default:
		in = 1 + uint32(r.Next()%480)
	}
	switch m := r.Next() % 8; m {
	case 0:
	case 1:
		out = 1 + uint32(r.Next()%8)
	default:
		o := uint64(in)*uint64(den)/uint64(num) + 1 + r.Next()%64
		out = uint32(min(o, outCap))
	}
	return in, out
}

type caseState struct {
	FiltLen      uint32 `json:"filt_len"`
	Oversample   uint32 `json:"oversample"`
	Direct       int    `json:"direct"`
	IntAdvance   int32  `json:"int_advance"`
	FracAdvance  int32  `json:"frac_advance"`
	CutoffBits   uint32 `json:"cutoff_bits"`
	NumRate      uint32 `json:"num_rate"`
	DenRate      uint32 `json:"den_rate"`
	MemAllocSize uint32 `json:"mem_alloc_size"`
	TableLen     uint32 `json:"table_len"`
}

type caseParams struct {
	Mode     string     `json:"mode"`
	Frac     int        `json:"frac"`
	Q        int        `json:"q"`
	Ch       int        `json:"ch"`
	Num      int64      `json:"num"`
	Den      int64      `json:"den"`
	InRate   int64      `json:"in_rate"`
	OutRate  int64      `json:"out_rate"`
	Frames   int        `json:"frames"`
	MaxCalls int        `json:"max_calls"`
	Events   [][6]int64 `json:"events"`
	InitErr  int        `json:"init_err"`
	State    *caseState `json:"state"`
}

// config returns the configuration, or false if a value does not fit in
// int on this platform (ratios above 2^31-1 on 32-bit platforms).
func (p *caseParams) config() (Config, bool) {
	for _, v := range []int64{p.Num, p.Den, p.InRate, p.OutRate} {
		if v > math.MaxInt {
			return Config{}, false
		}
	}
	if p.Frac == 1 {
		return Config{Channels: p.Ch, InRate: int(p.InRate), OutRate: int(p.OutRate), RatioNum: int(p.Num),
			RatioDen: int(p.Den), Quality: p.Q}, true
	}
	return Config{Channels: p.Ch, InRate: int(p.InRate), OutRate: int(p.OutRate), Quality: p.Q}, true
}

// errCode maps an error to the C return code.
func errCode(err error) int32 {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrAllocFailed):
		return 1
	case errors.Is(err, ErrConfig):
		return 3
	case errors.Is(err, ErrOverflow):
		return 5
	}
	return -1
}

type tracer struct {
	k *testutil.StreamChecker[int32]
}

func (t tracer) rec(v ...int32) {
	for _, x := range v {
		t.k.Put(x)
	}
}

func (t tracer) state(typ, ret int32, r *Resampler) {
	num, den := r.Ratio()
	t.rec(typ, ret, int32(r.InputLatency()), int32(r.OutputLatency()), int32(uint32(num)), int32(uint32(den)))
}

func applyEvent(r *Resampler, e [6]int64) error {
	switch e[1] {
	case eQuality:
		return r.SetQuality(int(e[2]))
	case eRate:
		return r.SetRate(int(e[2]), int(e[3]))
	case eRateFrac:
		return r.SetRateFrac(int(e[2]), int(e[3]), int(e[4]), int(e[5]))
	case eReset:
		r.Reset()
		return nil
	case eSkip:
		r.SkipZeros()
		return nil
	}
	panic(fmt.Sprintf("unknown event %d", e[1]))
}

// outSink receives output samples of either type.
type outSink struct {
	i16 *testutil.StreamChecker[int16]
	f32 *testutil.StreamChecker[float32]
}

func runReferenceCase(t *testing.T, m *testutil.Manifest, c *testutil.Case) {
	var p caseParams
	c.DecodeParams(t, &p)
	cfg, ok := p.config()
	if !ok {
		t.Skip("the ratio does not fit in int on this platform")
	}
	tr := tracer{testutil.NewStreamChecker[int32](t, m, c, c.Stream(t, "trace"), false)}
	isFloat := p.Mode == "f" || p.Mode == "g"
	var sink outSink
	if isFloat {
		sink.f32 = testutil.NewStreamChecker[float32](t, m, c, c.Stream(t, "out"), false)
	} else {
		sink.i16 = testutil.NewStreamChecker[int16](t, m, c, c.Stream(t, "out"), false)
	}
	r, err := New(cfg)
	if err != nil {
		if p.State != nil {
			t.Fatalf("New: %v, the reference succeeded", err)
		}
		tr.rec(tInit, errCode(err), 0, 0, 0, 0)
	} else {
		if p.State == nil {
			t.Fatalf("New succeeded, the reference returned %d", p.InitErr)
		}
		checkState(t, r, p.State)
		for _, s := range c.Streams {
			if s.Name == "table" {
				k := testutil.NewStreamChecker[int16](t, m, c, &s, false)
				for _, v := range r.sincTable[:p.State.TableLen] {
					k.Put(v)
				}
				k.Done()
			}
		}
		tr.state(tInit, 0, r)
		if p.Mode == "i" || p.Mode == "f" || p.Mode == "m" {
			replayInterleaved(t, c.Name, &p, r, tr, sink)
		} else {
			replaySplit(t, c.Name, &p, r, tr, sink)
		}
	}
	tr.k.Done()
	if isFloat {
		sink.f32.Done()
	} else {
		sink.i16.Done()
	}
}

func checkState(t *testing.T, r *Resampler, s *caseState) {
	t.Helper()
	got := caseState{
		FiltLen: r.filtLen, Oversample: r.oversample, IntAdvance: r.intAdvance, FracAdvance: r.fracAdvance,
		CutoffBits: math.Float32bits(r.cutoff), NumRate: r.numRate, DenRate: r.denRate, MemAllocSize: r.memAllocSize,
		TableLen: s.TableLen,
	}
	if r.kernel == kernelDirect {
		got.Direct = 1
	}
	if got != *s {
		t.Errorf("state after New:\n got  %+v\n want %+v", got, *s)
	}
}

func replayInterleaved(t *testing.T, name string, p *caseParams, r *Resampler, tr tracer, sink outSink) {
	rng := testutil.SplitMix64{State: testutil.Seed(name)}
	nch := p.Ch
	pos, calls, ev := 0, 0, 0
	if p.Frames*nch > signalLen {
		t.Fatalf("signal too short for %d frames", p.Frames)
	}
	if p.Mode == "m" {
		p0 := 0
		for range mixedCalls {
			num, den := r.Ratio()
			inOff, outOff := drawCall(&rng, num, den)
			inLen := min(int(inOff), p.Frames-p0)
			start, end := p0*nch, p0*nch
			if inLen > 0 {
				end = start + (inLen-1)*nch + 1
			}
			buf := make([]int16, outOff)
			il, ol, err := r.ProcessChannel(0, signal16[start:end], nch, buf, 1)
			for _, v := range buf[:ol] {
				sink.i16.Put(v)
			}
			tr.rec(tSplit, int32(inLen), int32(outOff), int32(il), int32(ol), errCode(err))
			p0 += il
		}
	}
	for pos < p.Frames {
		stop := false
		for ; ev < len(p.Events) && p.Events[ev][0] == int64(calls); ev++ {
			err := applyEvent(r, p.Events[ev])
			if errors.Is(err, ErrOverflow) {
				tr.rec(int32(p.Events[ev][1]), errCode(err), 0, 0, 0, 0)
				stop = true
				break
			}
			tr.state(int32(p.Events[ev][1]), errCode(err), r)
		}
		if stop {
			break
		}
		num, den := r.Ratio()
		inOff, outOff := drawCall(&rng, num, den)
		inLen := min(int(inOff), p.Frames-pos)
		outLen := int(outOff)
		var il, ol int
		var err error
		if p.Mode != "f" {
			buf := make([]int16, outLen*nch)
			il, ol, err = r.Process(signal16[pos*nch:(pos+inLen)*nch], buf)
			for _, v := range buf[:ol*nch] {
				sink.i16.Put(v)
			}
		} else {
			buf := make([]float32, outLen*nch)
			il, ol, err = r.ProcessFloat(signalF[pos*nch:(pos+inLen)*nch], buf)
			for _, v := range buf[:ol*nch] {
				sink.f32.Put(v)
			}
		}
		tr.rec(tProcess, int32(inLen), int32(outLen), int32(il), int32(ol), errCode(err))
		pos += il
		calls++
		if calls == p.MaxCalls {
			break
		}
		if calls > maxCalls {
			t.Fatalf("no progress after %d calls", maxCalls)
		}
	}
	if ev < len(p.Events) && !overflowed(p) {
		t.Errorf("only %d of %d events applied in %d calls", ev, len(p.Events), calls)
	}
}

// overflowed reports whether the case contains an overflowing ratio change,
// after which it ends early.
func overflowed(p *caseParams) bool {
	for _, e := range p.Events {
		if e[1] == eRateFrac && e[2] == 999983 {
			return true
		}
	}
	return false
}

func replaySplit(t *testing.T, name string, p *caseParams, r *Resampler, tr tracer, sink outSink) {
	nch := p.Ch
	rngs := make([]testutil.SplitMix64, nch)
	pos := make([]int, nch)
	for ch := range rngs {
		rngs[ch].State = testutil.Seed(name) + uint64(ch)
	}
	calls := 0
	for left := true; left; {
		left = false
		for ch := 0; ch < nch; ch++ {
			if pos[ch] >= p.Frames {
				continue
			}
			left = true
			num, den := r.Ratio()
			inOff, outOff := drawCall(&rngs[ch], num, den)
			inLen := min(int(inOff), p.Frames-pos[ch])
			start := pos[ch]*nch + ch
			inEnd, outLen := start, 0
			if inLen > 0 {
				inEnd = start + (inLen-1)*nch + 1
			}
			if outOff > 0 {
				outLen = (int(outOff)-1)*outStrideSplit + 1
			}
			var il, ol int
			var err error
			if p.Mode == "s" {
				buf := make([]int16, outLen)
				il, ol, err = r.ProcessChannel(ch, signal16[start:inEnd], nch, buf, outStrideSplit)
				for i := 0; i < ol; i++ {
					sink.i16.Put(buf[i*outStrideSplit])
				}
			} else {
				buf := make([]float32, outLen)
				il, ol, err = r.ProcessChannelFloat(ch, signalF[start:inEnd], nch, buf, outStrideSplit)
				for i := 0; i < ol; i++ {
					sink.f32.Put(buf[i*outStrideSplit])
				}
			}
			tr.rec(tSplit+int32(ch), int32(inLen), int32(outOff), int32(il), int32(ol), errCode(err))
			pos[ch] += il
			if calls++; calls > maxCalls {
				t.Fatalf("no progress after %d calls", maxCalls)
			}
		}
	}
}

func TestReferenceSignal(t *testing.T) {
	m := testutil.LoadManifest(t, fixtureDir)
	c := m.Case(t, "signal")
	a := testutil.NewStreamChecker[int16](t, m, c, c.Stream(t, "int16"), false)
	for _, v := range signal16 {
		a.Put(v)
	}
	a.Done()
	b := testutil.NewStreamChecker[float32](t, m, c, c.Stream(t, "float32"), false)
	for _, v := range signalF {
		b.Put(v)
	}
	b.Done()
}

// TestReferenceCases replays every case of the fixture set; the table
// streams in it are the libm comparison of every sinc table.
func TestReferenceCases(t *testing.T) {
	m := testutil.LoadManifest(t, fixtureDir)
	for i := range m.Cases {
		c := &m.Cases[i]
		if c.Name == "signal" {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			runReferenceCase(t, m, c)
		})
	}
}

// TestReferenceCoverage checks that the fixture set holds the matrix
// pre-registered in docs/acceptance/resample.md.
func TestReferenceCoverage(t *testing.T) {
	m := testutil.LoadManifest(t, fixtureDir)
	have := make(map[string]caseParams)
	for i := range m.Cases {
		if m.Cases[i].Name == "signal" {
			continue
		}
		var p caseParams
		m.Cases[i].DecodeParams(t, &p)
		have[m.Cases[i].Name] = p
	}
	rates := []int{8000, 16000, 24000, 32000, 44100, 48000, 96000}
	want := func(name, mode string, q, ch int, num, den int64, table bool) {
		t.Helper()
		p, ok := have[name]
		if !ok {
			t.Errorf("missing case %s", name)
			return
		}
		if p.Mode != mode || p.Q != q || p.Ch != ch || p.Num != num || p.Den != den {
			t.Errorf("case %s: params %+v", name, p)
		}
		if hasTable := hasStream(m, name, "table"); hasTable != table {
			t.Errorf("case %s: table stream %v, want %v", name, hasTable, table)
		}
	}
	fracs := [][2]int64{{48000, 47999}, {47999, 48000}, {44101, 44100}, {1000003, 999983}, {7, 5}, {5, 7},
		{1, 100}, {100, 1}, {2147483646, 2147483647}, {1000000007, 3000000019}}
	pairs2 := [][2]int{{8000, 16000}, {16000, 8000}, {16000, 48000}, {48000, 16000}, {44100, 48000},
		{48000, 44100}, {48000, 8000}, {96000, 44100}}
	for q := 0; q <= 10; q++ {
		for _, a := range rates {
			for _, b := range rates {
				want(fmt.Sprintf("q%d_%d_%d", q, a, b), "i", q, 1, int64(a), int64(b), true)
			}
		}
		for _, f := range fracs {
			want(fmt.Sprintf("q%d_frac_%d_%d", q, f[0], f[1]), "i", q, 1, f[0], f[1], true)
		}
		for _, pr := range pairs2 {
			want(fmt.Sprintf("q%d_%d_%d_2ch", q, pr[0], pr[1]), "i", q, 2, int64(pr[0]), int64(pr[1]), false)
		}
	}
	for _, q := range []int{0, 3, 5, 8, 10} {
		for _, pr := range [][2]int{{8000, 96000}, {48000, 16000}, {44100, 48000}, {16000, 48000}} {
			want(fmt.Sprintf("f_q%d_%d_%d", q, pr[0], pr[1]), "f", q, 1, int64(pr[0]), int64(pr[1]), false)
		}
		want(fmt.Sprintf("f_q%d_44100_48000_2ch", q), "f", q, 2, 44100, 48000, false)
	}
	for _, q := range []int{3, 10} {
		for ch := 2; ch <= 3; ch++ {
			want(fmt.Sprintf("split_q%d_48000_16000_%dch", q, ch), "s", q, ch, 48000, 16000, false)
			want(fmt.Sprintf("split_q%d_44100_48000_%dch", q, ch), "s", q, ch, 44100, 48000, false)
		}
	}
	want("q0_frac_270000001_270000000", "i", 0, 1, 270000001, 270000000, true)
	for _, q := range []int{0, 10} {
		name := fmt.Sprintf("wrap_q%d_frac_4294967294_4294967295", q)
		want(name, "i", q, 1, 4294967294, 4294967295, true)
		if have[name].MaxCalls != 50 {
			t.Errorf("case %s: max_calls %d, want 50", name, have[name].MaxCalls)
		}
	}
	want("mixed_q5_44100_48000_2ch", "m", 5, 2, 44100, 48000, false)
	want("mixed_q10_48000_16000_3ch", "m", 10, 3, 48000, 16000, false)
	want("splitf_q5_44100_48000_2ch", "g", 5, 2, 44100, 48000, false)
	want("splitf_q10_48000_16000_3ch", "g", 10, 3, 48000, 16000, false)
	for _, name := range []string{"initfail_q0_frac_600000000_1", "initfail_q10_frac_600000000_1",
		"initfail_q5_frac_2147483647_2147483646", "initfail_q5_frac_4294967295_4294967294",
		"initfail_q10_frac_6300001_3", "initfail_q0_48000_48000_7000000ch",
		"initfail_q1_frac_270000001_270000000"} {
		if p, ok := have[name]; !ok || p.InitErr != 1 {
			t.Errorf("case %s: missing or init_err %d, want 1", name, p.InitErr)
		}
	}
	events := map[string][]int{} // case prefix -> event operations that must occur
	events["skip_"] = []int{eSkip}
	events["qchange_"] = []int{eQuality}
	events["qburst_"] = []int{eQuality}
	events["rate_"] = []int{eRate, eRateFrac}
	events["drift_"] = []int{eRateFrac}
	events["reset_"] = []int{eReset}
	events["resetq_"] = []int{eReset, eQuality}
	events["fail_"] = []int{eRateFrac, eRate}
	events["overflow_"] = []int{eRateFrac}
	events["f_qdrop_"] = []int{eQuality}
	events["f_fail_"] = []int{eRateFrac, eRate}
	for prefix, ops := range events {
		n := 0
		for name, p := range have {
			if len(name) < len(prefix) || name[:len(prefix)] != prefix {
				continue
			}
			n++
			for _, op := range ops {
				found := false
				for _, e := range p.Events {
					found = found || e[1] == int64(op)
				}
				if !found {
					t.Errorf("case %s: no event %d", name, op)
				}
			}
		}
		if n == 0 {
			t.Errorf("no %s cases", prefix)
		}
	}
}

func hasStream(m *testutil.Manifest, name, stream string) bool {
	for _, c := range m.Cases {
		if c.Name == name {
			for _, s := range c.Streams {
				if s.Name == stream {
					return true
				}
			}
		}
	}
	return false
}
