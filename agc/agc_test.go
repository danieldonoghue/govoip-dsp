package agc

import (
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

func mustNew(t testing.TB, cfg Config) *AGC {
	t.Helper()
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestNewInvalidConfig(t *testing.T) {
	mod := func(f func(*Config)) Config {
		c := DefaultConfig(16000)
		f(&c)
		return c
	}
	bad := map[string]Config{
		"zero":            {},
		"rate low":        mod(func(c *Config) { c.SampleRate = 7999 }),
		"rate high":       mod(func(c *Config) { c.SampleRate = 48001 }),
		"target low":      mod(func(c *Config) { c.TargetLevel = -41 }),
		"target high":     mod(func(c *Config) { c.TargetLevel = -2 }),
		"limit low":       mod(func(c *Config) { c.LimitLevel = -21; c.TargetLevel = -30 }),
		"limit high":      mod(func(c *Config) { c.LimitLevel = 1 }),
		"limit at target": mod(func(c *Config) { c.LimitLevel = -10; c.TargetLevel = -10 }),
		"gate low":        mod(func(c *Config) { c.GateLevel = -81 }),
		"gate at target":  mod(func(c *Config) { c.GateLevel = c.TargetLevel }),
		"max gain low":    mod(func(c *Config) { c.MaxGain = -1 }),
		"max gain high":   mod(func(c *Config) { c.MaxGain = 41 }),
		"min gain low":    mod(func(c *Config) { c.MinGain = -41 }),
		"min gain high":   mod(func(c *Config) { c.MinGain = 1 }),
		"rise low":        mod(func(c *Config) { c.RiseRate = 0 }),
		"rise high":       mod(func(c *Config) { c.RiseRate = 101 }),
		"fall low":        mod(func(c *Config) { c.FallRate = 0 }),
		"fall high":       mod(func(c *Config) { c.FallRate = 401 }),
		"level short":     mod(func(c *Config) { c.LevelTime = 100*time.Millisecond - 1 }),
		"level long":      mod(func(c *Config) { c.LevelTime = 10*time.Second + 1 }),
		"level negative":  mod(func(c *Config) { c.LevelTime = -time.Second }),
	}
	for name, cfg := range bad {
		if a, err := New(cfg); !errors.Is(err, ErrConfig) || a != nil {
			t.Errorf("%s: New = %v, %v; want nil, ErrConfig", name, a, err)
		}
	}
}

func TestNewBoundaryConfig(t *testing.T) {
	mod := func(f func(*Config)) Config {
		c := DefaultConfig(16000)
		f(&c)
		return c
	}
	good := map[string]Config{
		"rate low":     DefaultConfig(8000),
		"rate high":    DefaultConfig(48000),
		"rate 11025":   DefaultConfig(11025),
		"rate 22050":   DefaultConfig(22050),
		"rate 44100":   DefaultConfig(44100),
		"target low":   mod(func(c *Config) { c.TargetLevel = -40 }),
		"target high":  mod(func(c *Config) { c.TargetLevel = -3; c.LimitLevel = -2 }),
		"limit low":    mod(func(c *Config) { c.LimitLevel = -20; c.TargetLevel = -21 }),
		"limit high":   mod(func(c *Config) { c.LimitLevel = 0 }),
		"gate low":     mod(func(c *Config) { c.GateLevel = -80 }),
		"gate high":    mod(func(c *Config) { c.GateLevel = c.TargetLevel - 1 }),
		"gains zero":   mod(func(c *Config) { c.MaxGain, c.MinGain = 0, 0 }),
		"gains wide":   mod(func(c *Config) { c.MaxGain, c.MinGain = 40, -40 }),
		"fast":         mod(func(c *Config) { c.RiseRate, c.FallRate, c.LevelTime = 100, 400, 100*time.Millisecond }),
		"slow":         mod(func(c *Config) { c.RiseRate, c.FallRate, c.LevelTime = 1, 1, 10*time.Second }),
		"fullscale 8k": mod(func(c *Config) { c.SampleRate, c.LimitLevel, c.MaxGain = 8000, 0, 40 }),
	}
	for name, cfg := range good {
		a, err := New(cfg)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		// The extreme configuration must still respect the ceiling.
		x := slices.Concat(testutil.WhiteNoise(cfg.SampleRate, 100, 1), testutil.WhiteNoise(cfg.SampleRate, 32767, 2))
		y := make([]int16, len(x))
		if err := a.Process(x, y, true); err != nil {
			t.Fatal(err)
		}
		for n, v := range y {
			if max(int32(v), -int32(v)) > a.ceiling {
				t.Fatalf("%s: sample %d is %d, above ceiling %d", name, n, v, a.ceiling)
			}
		}
	}
}

// TestConstants pins the integer constants New derives for DefaultConfig at
// 16 kHz, values computed independently from the formulas in the package.
func TestConstants(t *testing.T) {
	a := mustNew(t, DefaultConfig(16000))
	got := []int64{int64(a.blockLen), a.frameLen, int64(a.levCoef), int64(a.up), int64(a.down), int64(a.limCoef),
		int64(a.warm), int64(a.dropRun), int64(a.target), int64(a.gate), int64(a.ceiling), int64(a.maxGain),
		int64(a.minGain), int64(a.Latency())}
	want := []int64{16, 160, 109, 456, 31293, 1598, 300, 100, 8468566, 424434, 29205, 519338, 8231, 32}
	if !slices.Equal(got, want) {
		t.Errorf("constants %v, want %v", got, want)
	}
}

func TestProcessBufferSize(t *testing.T) {
	a := mustNew(t, DefaultConfig(16000))
	in := testutil.WhiteNoise(100, 1000, 1)
	out := make([]int16, 99)
	for i := range out {
		out[i] = 7
	}
	if err := a.Process(in, out, true); err != ErrBufferSize {
		t.Fatalf("Process = %v, want ErrBufferSize", err)
	}
	for i, v := range out {
		if v != 7 {
			t.Fatalf("out[%d] = %d written on error", i, v)
		}
	}
	// The failed call must not have changed the state.
	b := mustNew(t, DefaultConfig(16000))
	ya, yb := make([]int16, 100), make([]int16, 100)
	if err := a.Process(in, ya, true); err != nil {
		t.Fatal(err)
	}
	if err := b.Process(in, yb, true); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ya, yb) {
		t.Error("state changed by a failed call")
	}
	// A longer output is allowed; only len(in) samples are written.
	long := make([]int16, 200)
	long[150] = 9
	if err := a.Process(in, long, true); err != nil || long[150] != 9 {
		t.Errorf("long output: err %v, long[150] = %d", err, long[150])
	}
}

func TestProcessEmpty(t *testing.T) {
	cfg := DefaultConfig(16000)
	a, b := mustNew(t, cfg), mustNew(t, cfg)
	x := testutil.WhiteNoise(1000, 3000, 3)
	ya, yb := make([]int16, len(x)), make([]int16, len(x))
	for i := range 5 {
		if err := a.Process(nil, nil, i%2 == 0); err != nil {
			t.Fatal(err)
		}
		if err := a.Process(x[:0], ya[:0], false); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Process(x, ya, true); err != nil {
		t.Fatal(err)
	}
	if err := b.Process(x, yb, true); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ya, yb) {
		t.Error("empty calls changed the output")
	}
}

func TestLatency(t *testing.T) {
	for _, rate := range []int{8000, 11025, 16000, 44100, 48000} {
		a := mustNew(t, DefaultConfig(rate))
		d := 2 * (rate / 1000)
		if a.Latency() != d {
			t.Errorf("%d Hz: Latency %d, want %d", rate, a.Latency(), d)
		}
		x := testutil.Impulse(10*d, 3, 1000)
		y := make([]int16, len(x))
		if err := a.Process(x, y, true); err != nil {
			t.Fatal(err)
		}
		want := testutil.Impulse(len(x), 3+d, 1000)
		if !slices.Equal(y, want) {
			t.Errorf("%d Hz: impulse response %v", rate, y[:4*d])
		}
	}
}

// chunkInput is a stream with level changes, silence, full scale and speech
// flags, used by the chunking, reset and aliasing tests.
func chunkInput(rate int) ([]int16, []bool) {
	n := rate / 2
	x := slices.Concat(
		testutil.SpeechLike(2*n, float64(rate), 3000, 11),
		testutil.WhiteNoise(n, 20000, 12),
		make([]int16, n),
		testutil.SpeechLike(n, float64(rate), 300, 13),
		testutil.WhiteNoise(n/10, 32767, 14),
		testutil.SpeechLike(2*n, float64(rate), 8000, 15),
	)
	speech := make([]bool, len(x))
	for i := range speech {
		speech[i] = (i/1777)%3 != 1
	}
	return x, speech
}

// processChunks runs x through a in chunks whose lengths come from sizes
// (cycled), splitting further wherever the speech flag changes.
func processChunks(t testing.TB, a *AGC, x []int16, speech []bool, sizes []int) []int16 {
	t.Helper()
	y := make([]int16, len(x))
	for i, k := 0, 0; i < len(x); k++ {
		n := min(sizes[k%len(sizes)], len(x)-i)
		for j := 1; j < n; j++ {
			if speech[i+j] != speech[i] {
				n = j
				break
			}
		}
		if err := a.Process(x[i:i+n], y[i:i+n], speech[i]); err != nil {
			t.Fatal(err)
		}
		i += n
	}
	return y
}

func TestChunkInvarianceProcess(t *testing.T) {
	for _, rate := range []int{8000, 44100, 48000} {
		cfg := DefaultConfig(rate)
		x, speech := chunkInput(rate)
		want := processChunks(t, mustNew(t, cfg), x, speech, []int{len(x)})
		r := testutil.SplitMix64{State: testutil.Seed("agc-chunks")}
		partitions := [][]int{{1}, {2, 3}, {rate / 100}, {rate / 50}, {37}, {479, 1, 0, 481}}
		for range 4 {
			p := make([]int, 20)
			for i := range p {
				p[i] = r.Intn(2000)
			}
			partitions = append(partitions, p)
		}
		for _, p := range partitions {
			got := processChunks(t, mustNew(t, cfg), x, speech, p)
			if i := firstDiff(got, want); i >= 0 {
				t.Fatalf("%d Hz, chunks %v: first difference at sample %d: %d, want %d", rate, p, i, got[i], want[i])
			}
		}
	}
}

func firstDiff(a, b []int16) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return min(len(a), len(b))
	}
	return -1
}

func TestResetProcess(t *testing.T) {
	cfg := DefaultConfig(16000)
	x, speech := chunkInput(16000)
	want := processChunks(t, mustNew(t, cfg), x, speech, []int{160})
	a := mustNew(t, cfg)
	delay := a.delay
	processChunks(t, a, slices.Concat(testutil.WhiteNoise(30000, 32767, 21), x[:12345]), speech, []int{333})
	a.Reset()
	if &a.delay[0] != &delay[0] {
		t.Error("Reset moved the delay line")
	}
	if got := processChunks(t, a, x, speech, []int{160}); firstDiff(got, want) >= 0 {
		t.Errorf("after Reset: first difference at sample %d", firstDiff(got, want))
	}
	testutil.AssertNoAllocs(t, 100, a.Reset)
}

func TestAliasingProcess(t *testing.T) {
	cfg := DefaultConfig(16000)
	x, speech := chunkInput(16000)
	a := mustNew(t, cfg)
	buf := slices.Clone(x)
	for i := 0; i < len(buf); i += 160 {
		n := min(160, len(buf)-i)
		if err := a.Process(buf[i:i+n], buf[i:i+n], speech[i]); err != nil {
			t.Fatal(err)
		}
	}
	// The reference uses separate slices and the same per-chunk flags.
	b := mustNew(t, cfg)
	ref := make([]int16, len(x))
	for i := 0; i < len(x); i += 160 {
		n := min(160, len(x)-i)
		if err := b.Process(x[i:i+n], ref[i:i+n], speech[i]); err != nil {
			t.Fatal(err)
		}
	}
	if i := firstDiff(buf, ref); i >= 0 {
		t.Fatalf("in-place output differs at sample %d", i)
	}
}

// TestSpeechFlag checks that a single sample passed with speech false keeps
// its frame from changing the level, and that frames resume afterwards.
func TestSpeechFlag(t *testing.T) {
	cfg := DefaultConfig(16000)
	a := mustNew(t, cfg)
	x := testutil.Sine(1600, 16000, 1000, 328, 0)
	y := make([]int16, len(x))
	if err := a.Process(x[:100], y[:100], true); err != nil {
		t.Fatal(err)
	}
	if err := a.Process(x[100:101], y[100:101], false); err != nil {
		t.Fatal(err)
	}
	if err := a.Process(x[101:160], y[101:160], true); err != nil {
		t.Fatal(err)
	}
	if a.level != a.target || a.gain != unity {
		t.Fatalf("frame with one non-speech sample changed level %d, gain %d", a.level, a.gain)
	}
	if err := a.Process(x[160:320], y[160:320], true); err != nil {
		t.Fatal(err)
	}
	if a.gain <= unity {
		t.Fatalf("next speech frame did not raise the gain: %d", a.gain)
	}
}

func TestNoAllocsProcess(t *testing.T) {
	for _, rate := range []int{8000, 16000, 48000} {
		a := mustNew(t, DefaultConfig(rate))
		x := testutil.SpeechLike(rate, float64(rate), 20000, 31)
		y := make([]int16, len(x))
		// Warm up through loud input so the limiter and both smoothing paths run.
		if err := a.Process(x, y, true); err != nil {
			t.Fatal(err)
		}
		frame := rate / 100
		i := 0
		testutil.AssertNoAllocs(t, 200, func() {
			j := i % (len(x) - frame)
			_ = a.Process(x[j:j+frame], y[j:j+frame], i%2 == 0)
			i += frame + 7
		})
		testutil.AssertNoAllocs(t, 200, func() { _ = a.Process(x[:10], y[:9], true) })
	}
}

// TestLongRunProcess runs 10 minutes at 48 kHz and checks every state
// variable against its documented range after each frame.
func TestLongRunProcess(t *testing.T) {
	const rate = 48000
	cfg := DefaultConfig(rate)
	a := mustNew(t, cfg)
	seg := slices.Concat(
		testutil.SpeechLike(5*rate, rate, 25000, 41),
		testutil.WhiteNoise(rate, 300, 42),
		make([]int16, rate),
		testutil.SpeechLike(3*rate, rate, 500, 43),
	)
	y := make([]int16, len(seg))
	frame := rate / 100
	for loop := range 60 {
		for i := 0; i < len(seg); i += frame {
			n := min(frame, len(seg)-i)
			if err := a.Process(seg[i:i+n], y[i:i+n], loop%5 != 4); err != nil {
				t.Fatal(err)
			}
			if a.level < min(a.gate, a.target) || a.level > 1<<28 ||
				a.gain < a.minGain || a.gain > a.maxGain ||
				a.env <= 0 || a.env > a.gain || a.bound <= 0 || a.bound > a.maxGain ||
				a.pos < 0 || a.pos >= len(a.delay) || a.n < 0 || a.n >= a.blockLen ||
				a.energy < 0 || a.energy > a.frameLen<<30 {
				t.Fatalf("loop %d sample %d: state out of range: %+v", loop, i, *a)
			}
		}
		for i, v := range y {
			if max(int32(v), -int32(v)) > a.ceiling {
				t.Fatalf("loop %d sample %d: %d above ceiling", loop, i, v)
			}
		}
	}
}

func FuzzChunksProcess(f *testing.F) {
	f.Add([]byte{0, 0, 255, 127, 0, 128}, uint64(1), uint16(16000), false)
	f.Add(make([]byte, 4000), uint64(7), uint16(8000), true)
	f.Add([]byte("a mixture of bytes that becomes noise"), uint64(99), uint16(48000), true)
	f.Fuzz(func(t *testing.T, data []byte, seed uint64, rate uint16, quiet bool) {
		cfg := DefaultConfig(8000 + int(rate)%40001)
		x := testutil.DecodeLE[int16](data[:len(data)&^1])
		if quiet {
			for i := range x {
				x[i] >>= 6
			}
		}
		speech := make([]bool, len(x))
		r := testutil.SplitMix64{State: seed}
		for i := range speech {
			speech[i] = (i/97)%2 == 0 || seed&1 == 0
		}
		sizes := []int{1 + r.Intn(500), r.Intn(50), 1 + r.Intn(3000)}
		want := processChunks(t, mustNew(t, cfg), x, speech, []int{max(len(x), 1)})
		a := mustNew(t, cfg)
		got := processChunks(t, a, x, speech, sizes)
		if i := firstDiff(got, want); i >= 0 {
			t.Fatalf("chunks %v: first difference at sample %d", sizes, i)
		}
		d := a.Latency()
		for n, v := range got {
			y := int32(v)
			if max(y, -y) > a.ceiling {
				t.Fatalf("sample %d is %d, above ceiling %d", n, v, a.ceiling)
			}
			if n >= d {
				if in := x[n-d]; (y > 0 && in <= 0) || (y < 0 && in >= 0) {
					t.Fatalf("sample %d is %d for input %d", n, v, in)
				}
			} else if v != 0 {
				t.Fatalf("sample %d is %d before the delay line filled", n, v)
			}
		}
	})
}

func FuzzConfig(f *testing.F) {
	f.Add(16000, -24, -1, -50, 24, -12, 12, 40, int64(3000), []byte{1, 2, 3, 4})
	f.Add(8000, -40, 0, -80, 40, -40, 100, 1, int64(100), []byte{255, 127, 0, 128})
	f.Add(0, 0, 0, 0, 0, 0, 0, 0, int64(0), []byte{})
	f.Fuzz(func(t *testing.T, rate, target, limit, gate, maxG, minG, rise, fall int, level int64, data []byte) {
		cfg := Config{rate, target, limit, gate, maxG, minG, rise, fall, time.Duration(level) * time.Millisecond}
		a, err := New(cfg)
		if err != nil {
			if !errors.Is(err, ErrConfig) || a != nil {
				t.Fatalf("New = %v, %v", a, err)
			}
			return
		}
		x := testutil.DecodeLE[int16](data[:len(data)&^1])
		x = slices.Concat(x, x, x, x, x, x, x, x)
		y := make([]int16, len(x))
		if err := a.Process(x, y, true); err != nil {
			t.Fatal(err)
		}
		for n, v := range y {
			if max(int32(v), -int32(v)) > a.ceiling {
				t.Fatalf("sample %d is %d, above ceiling %d", n, v, a.ceiling)
			}
		}
	})
}

// TestConfigEdgeAccuracy runs the configurations at the edges of the valid
// range where a floored one-pole step could stall short of its target.
func TestConfigEdgeAccuracy(t *testing.T) {
	low := Config{16000, -40, -20, -80, 0, -40, 12, 40, 3 * time.Second}
	slow := Config{16000, -40, -20, -80, 40, -40, 1, 1, 10 * time.Second}
	for _, c := range []struct {
		name     string
		cfg      Config
		from, to float64
		sec      int
	}{
		{"min gain, limiter released", low, -3, -6, 60},
		{"10 s time constants near the gate", slow, -40, -79.5, 240},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := mustNew(t, c.cfg)
			rate := c.cfg.SampleRate
			x := slices.Concat(sine(10*rate, rate, c.from), sine(c.sec*rate, rate, c.to))
			y := make([]int16, len(x))
			minLevel := a.level
			for i := 0; i < len(x); i += 160 {
				if err := a.Process(x[i:i+160], y[i:i+160], true); err != nil {
					t.Fatal(err)
				}
				minLevel = min(minLevel, a.level)
			}
			got := testutil.DBFS(testutil.RMS(y[len(y)-rate:]))
			t.Logf("output %.3f dBFS, AGC gain %d, limiter gain %d, lowest level %d (gate %d)",
				got, a.gain, a.env, minLevel, a.gate)
			if d := got - float64(c.cfg.TargetLevel); d > 0.25 || d < -0.25 {
				t.Errorf("output %.3f dBFS, want %d ±0.25", got, c.cfg.TargetLevel)
			}
			// The closest the level divisor comes to zero is the gate.
			if minLevel < a.gate {
				t.Errorf("level %d fell below the gate %d", minLevel, a.gate)
			}
		})
	}
}

// TestRMS compares rms with the exact root mean square from 1/4 LSB RMS
// (-102 dBFS, below any gate) to full scale.
func TestRMS(t *testing.T) {
	worst := 0.0
	for _, n := range []int64{80, 160, 440, 480} {
		for e := n / 16; e <= n<<30; e += 1 + e/1000 {
			want := math.Sqrt(float64(e)/float64(n)) * 4096
			worst = max(worst, math.Abs(float64(rms(e, n))/want-1))
		}
	}
	t.Logf("worst relative error %.5f", worst)
	if worst > 0.003 || rms(0, 160) != 0 {
		t.Errorf("worst relative error %.5f, rms(0) = %d", worst, rms(0, 160))
	}
}
