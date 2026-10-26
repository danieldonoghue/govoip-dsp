package echo

import (
	"errors"
	"slices"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/fft"
	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

func mustNew(t testing.TB, fs, filter, rate int) *Canceller {
	t.Helper()
	c, err := New(Config{FrameSize: fs, FilterLength: filter, SampleRate: rate})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	bad := []Config{
		{0, 1024, 8000}, {-1, 1024, 8000}, {MaxFrameSize + 1, 4096, 8000}, {19, 1024, 8000},
		{160, 0, 8000}, {160, -1, 8000}, {160, MaxFilterLength + 1, 8000},
		{1, MaxFilterLength, 8000}, // 65536 blocks: M must fit spx_word16_t
		{160, 1024, 0}, {160, 1024, 7999}, {160, 1024, 48001}, {160, 1024, -8000}, {160, 1024, 65536},
		{},
	}
	for _, cfg := range bad {
		if c, err := New(cfg); !errors.Is(err, ErrConfig) || c != nil {
			t.Errorf("New(%+v) = %v, %v; want ErrConfig", cfg, c, err)
		}
	}
	good := []Config{
		{1, 1, 8000}, {2, 65534, 8000}, // 32767 blocks, the largest M
		{MaxFrameSize, MaxFilterLength, 48000}, {80, 512, 8000}, {480, 12288, 48000}, {17, 100, 16000},
	}
	for _, cfg := range good {
		if _, err := New(cfg); err != nil {
			t.Errorf("New(%+v): %v", cfg, err)
		}
	}
}

// Every frame size New accepts is exactly one whose window the FFT supports.
func TestNewFrameSizes(t *testing.T) {
	for fs := -2; fs <= MaxFrameSize+2; fs++ {
		_, err := New(Config{FrameSize: fs, FilterLength: 64, SampleRate: 8000})
		want := fs >= 1 && fft.Supported(2*fs)
		if (err == nil) != want {
			t.Fatalf("frame size %d: err %v, want accepted %v", fs, err, want)
		}
	}
}

func TestAccessors(t *testing.T) {
	c := mustNew(t, 160, 2000, 16000)
	if c.FrameSize() != 160 || c.SampleRate() != 16000 || c.ResidualLen() != 161 {
		t.Fatalf("accessors: %d %d %d", c.FrameSize(), c.SampleRate(), c.ResidualLen())
	}
	// 2000 samples round up to 13 frames.
	if c.FilterLength() != 13*160 || c.ImpulseResponseLen() != 13*160 {
		t.Fatalf("FilterLength %d, ImpulseResponseLen %d", c.FilterLength(), c.ImpulseResponseLen())
	}
	if err := c.SetSampleRate(8000); err != nil || c.SampleRate() != 8000 {
		t.Fatalf("SetSampleRate(8000): %v, rate %d", err, c.SampleRate())
	}
}

// stateOf snapshots every state variable.
func stateOf(c *Canceller) map[string][]int32 {
	m := map[string][]int32{}
	for _, v := range traceVars(c.st) {
		m[v.name] = slices.Clone(v.v)
	}
	return m
}

func diffState(a, b map[string][]int32) []string {
	var d []string
	for k, v := range a {
		if !slices.Equal(v, b[k]) {
			d = append(d, k)
		}
	}
	slices.Sort(d)
	return d
}

// warm runs n frames of the farend scenario (8 kHz) through c.
func warm(t testing.TB, c *Canceller, n int) {
	t.Helper()
	s := scenario("farend", 8000, 128, 80)
	fs := c.FrameSize()
	out := make([]int16, fs)
	for k := 0; k < n; k++ {
		i := (k * fs) % (len(s.mic) - fs)
		if err := c.Cancel(s.mic[i:i+fs], s.far[i:i+fs], out); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBufferSizeErrorsChangeNothing(t *testing.T) {
	c := mustNew(t, 80, 1024, 8000)
	warm(t, c, 200)
	before := stateOf(c)
	ok := make([]int16, 80)
	short, long := make([]int16, 79), make([]int16, 81)
	out := make([]int16, 80)
	for i := range out {
		out[i] = 1234
	}
	for _, err := range []error{
		c.Cancel(short, ok, out), c.Cancel(ok, long, out), c.Cancel(ok, ok, short), c.Cancel(nil, nil, nil),
		c.Capture(short, out), c.Capture(ok, long), c.Playback(short), c.Playback(nil),
		c.Residual(make([]int32, 80)), c.ImpulseResponse(make([]int32, c.ImpulseResponseLen()-1)),
	} {
		if err != ErrBufferSize {
			t.Errorf("got %v, want ErrBufferSize", err)
		}
	}
	if err := c.SetSampleRate(7999); err != ErrConfig {
		t.Errorf("SetSampleRate(7999) = %v, want ErrConfig", err)
	}
	if d := diffState(before, stateOf(c)); len(d) != 0 {
		t.Errorf("rejected calls changed %v", d)
	}
	for _, v := range out {
		if v != 1234 {
			t.Fatal("a rejected call wrote out")
		}
	}
}

func TestResidualIsRepeatableAndSideEffectFree(t *testing.T) {
	c := mustNew(t, 80, 1024, 8000)
	warm(t, c, 500)
	if !c.st.adapted {
		t.Fatal("not adapted after warm-up; the test needs the adapted path")
	}
	a, b := make([]int32, 81), make([]int32, 90)
	before := stateOf(c)
	if err := c.Residual(a); err != nil {
		t.Fatal(err)
	}
	if err := c.Residual(b); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a, b[:81]) || slices.ContainsFunc(b[81:], func(v int32) bool { return v != 0 }) {
		t.Fatal("two Residual calls differ, or Residual wrote past ResidualLen")
	}
	// Only the scratch buffers that Cancel overwrites before reading change.
	if d := diffState(before, stateOf(c)); !slices.Equal(d, []string{"Y", "y"}) && len(d) != 0 {
		t.Errorf("Residual changed %v, want only the scratch buffers y and Y", d)
	}
	ir := make([]int32, c.ImpulseResponseLen())
	before = stateOf(c)
	if err := c.ImpulseResponse(ir); err != nil {
		t.Fatal(err)
	}
	if d := diffState(before, stateOf(c)); !slices.Equal(d, []string{"wtmp", "wtmp2"}) && len(d) != 0 {
		t.Errorf("ImpulseResponse changed %v, want only the scratch buffers wtmp and wtmp2", d)
	}
}

// scratch lists the buffers that every Cancel writes before reading.
var scratch = []string{"E", "PHI", "Rf", "X", "Xf", "Y", "Yf", "e", "input", "wtmp", "wtmp2", "x", "y"}

func TestResetState(t *testing.T) {
	fresh := mustNew(t, 80, 1024, 8000)
	c := mustNew(t, 80, 1024, 8000)
	warm(t, c, 600)
	if !c.st.adapted {
		t.Fatal("not adapted after warm-up")
	}
	c.Reset()
	// Kept by speex_echo_state_reset, as the package doc says; the other
	// differences are scratch buffers.
	kept := []string{"last_y", "leak_estimate", "prop"}
	diff := diffState(stateOf(fresh), stateOf(c))
	for _, name := range kept {
		if !slices.Contains(diff, name) {
			t.Errorf("%s equals a new Canceller's after adaptation and Reset; the test no longer shows that Reset keeps it", name)
		}
	}
	for _, name := range diff {
		if !slices.Contains(kept, name) && !slices.Contains(scratch, name) {
			t.Errorf("Reset leaves %s different from a new Canceller", name)
		}
		// E, X and x are reset; check they are not among the differences.
		if name == "E" || name == "X" || name == "x" {
			t.Errorf("Reset did not clear %s", name)
		}
	}
	// last_y: only the older half is kept.
	if slices.ContainsFunc(c.st.lastY[:80], func(v int16) bool { return v != 0 }) {
		t.Error("Reset kept the newer half of last_y")
	}
}

// Before adaptation nothing that Reset keeps has changed, so the output
// after Reset equals a new Canceller's.
func TestResetMatchesNewBeforeAdaptation(t *testing.T) {
	s := scenario("farend", 8000, 128, 80)
	fresh := mustNew(t, 80, 1024, 8000)
	c := mustNew(t, 80, 1024, 8000)
	out := make([]int16, 80)
	for k := 0; k < 20; k++ {
		i := k * 80
		if err := c.Cancel(s.mic[i:i+80], s.far[i:i+80], out); err != nil {
			t.Fatal(err)
		}
	}
	if c.st.adapted {
		t.Fatal("adapted too early for this test")
	}
	c.Reset()
	o1, o2 := make([]int16, 80), make([]int16, 80)
	for k := 20; k < 1000; k++ {
		i := k * 80
		_ = c.Cancel(s.mic[i:i+80], s.far[i:i+80], o1)
		_ = fresh.Cancel(s.mic[i:i+80], s.far[i:i+80], o2)
		if !slices.Equal(o1, o2) {
			t.Fatalf("frame %d after Reset differs from a new Canceller", k)
		}
	}
}

func TestCaptureUnderrunPassesMicThrough(t *testing.T) {
	c := mustNew(t, 80, 512, 8000)
	mic := testutil.WhiteNoise(80, 3000, 1)
	out := make([]int16, 80)
	far := make([]int16, 80)
	// Two queued frames of silence, then the queue runs dry.
	for k := 0; k < 2; k++ {
		if err := c.Capture(mic, out); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Capture(mic, out); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(out, mic) {
		t.Fatal("Capture with an empty queue did not copy mic to out")
	}
	// A Playback into the empty queue is queued twice.
	if err := c.Playback(far); err != nil {
		t.Fatal(err)
	}
	if c.st.playBufPos != 160 {
		t.Fatalf("queue holds %d samples after auto-fill, want 160", c.st.playBufPos)
	}
}

func TestNoAllocs(t *testing.T) {
	c := mustNew(t, 160, 2048, 16000)
	s := scenario("farend", 16000, 128, 160)
	out := make([]int16, 160)
	res := make([]int32, c.ResidualLen())
	ir := make([]int32, c.ImpulseResponseLen())
	k := 0
	next := func() (mic, far []int16) {
		i := (k * 160) % (len(s.mic) - 160)
		k++
		return s.mic[i : i+160], s.far[i : i+160]
	}
	for c.st.cancelCount < 800 {
		mic, far := next()
		_ = c.Cancel(mic, far, out)
	}
	if !c.st.adapted {
		t.Fatal("not adapted after warm-up")
	}
	testutil.AssertNoAllocs(t, 200, func() {
		mic, far := next()
		_ = c.Cancel(mic, far, out)
		_ = c.Residual(res)
	})
	testutil.AssertNoAllocs(t, 200, func() {
		mic, far := next()
		_ = c.Playback(far)
		_ = c.Capture(mic, out)
	})
	testutil.AssertNoAllocs(t, 20, func() {
		_ = c.ImpulseResponse(ir)
		_ = c.SetSampleRate(16000)
		c.Reset()
		_ = c.Cancel(make([]int16, 0), nil, nil) // error path
	})
}

// The divisors of FLOAT_DIV32 (power+10) and FLOAT_DIV32_FLOAT
// (e*(power+10)) are nonzero because power stays at least 0 and the
// spectra Xf and Rf stay below 2^31 (docs/algorithms.md). This drives the
// extremes and checks those invariants after every frame.
func TestDivisorInvariantsUnderExtremes(t *testing.T) {
	configs := []Config{{80, 512, 8000}, {160, 2048, 16000}, {480, 12288, 48000}, {1, 1, 8000}, {2, 64, 8000}}
	r := testutil.SplitMix64{State: testutil.Seed("extremes")}
	for _, cfg := range configs {
		c := mustNew(t, cfg.FrameSize, cfg.FilterLength, cfg.SampleRate)
		fs := cfg.FrameSize
		mic, far, out := make([]int16, fs), make([]int16, fs), make([]int16, fs)
		for k := 0; k < 3000; k++ {
			kind := (k / 100) % 6
			for i := 0; i < fs; i++ {
				switch kind {
				case 0: // full-range noise
					mic[i], far[i] = int16(r.Next()>>48), int16(r.Next()>>48)
				case 1: // -32768 everywhere
					mic[i], far[i] = -32768, -32768
				case 2: // alternating full scale
					mic[i], far[i] = int16(32767-int(i%2)*65535), int16(-32768+int(i%2)*65535)
				case 3: // DC far end, silent mic
					mic[i], far[i] = 0, 32767
				case 4: // silent far end, full-scale mic
					mic[i], far[i] = int16(r.Next()>>48), 0
				default: // echo of the far end, clipped
					far[i] = int16(r.Next() >> 50)
					mic[i] = sat16(4 * int64(far[i]))
				}
			}
			if err := c.Cancel(mic, far, out); err != nil {
				t.Fatal(err)
			}
			for i := range c.st.power {
				if c.st.power[i] < 0 || c.st.Xf[i] < 0 {
					t.Fatalf("%+v frame %d: power[%d] = %d, Xf = %d", cfg, k, i, c.st.power[i], c.st.Xf[i])
				}
				if c.st.Rf[i] < 0 || c.st.Rf[i] > 1<<28 {
					t.Fatalf("%+v frame %d: Rf[%d] = %d", cfg, k, i, c.st.Rf[i])
				}
			}
		}
	}
}
