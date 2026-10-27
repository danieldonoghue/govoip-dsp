package preprocess

import (
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/fft"
	"github.com/danieldonoghue/govoip-dsp/internal/fixed"
	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

// f32 is a C float literal promoted to double, as QCONST16 sees x*2^bits
// for an f-suffixed literal.
func f32(x float32) float64 { return float64(x) }

func TestConstants(t *testing.T) {
	for _, tc := range []struct {
		name      string
		got, want int32
	}{
		{"SPEECH_PROB_START_DEFAULT", probStartDefault, int32(fixed.QConst16(f32(0.35), 15))},
		{"SPEECH_PROB_CONTINUE_DEFAULT", probContinueDefault, int32(fixed.QConst16(f32(0.20), 15))},
		{"1.f,13", q13One, int32(fixed.QConst16(1, 13))},
		{"2.f,13", q13Two, int32(fixed.QConst16(2, 13))},
		{"3.f,13", q13Three, int32(fixed.QConst16(3, 13))},
		{"1.271903f,14", winScale, int32(fixed.QConst16(f32(1.271903), 14))},
		{".5f,15", q15Half, int32(fixed.QConst16(f32(.5), 15))},
		{".1296,23", hgTail, fixed.QConst32(.1296, 23)},
		{".60f,15", qc060, int32(fixed.QConst16(f32(.60), 15))},
		{"0.11513,11", gfNoise, int32(fixed.QConst16(0.11513, 11))},
		{".2302585f,11", gfRatio, int32(fixed.QConst16(f32(.2302585), 11))},
		{".03,15", betaMin, int32(fixed.QConst16(.03, 15))},
		{".6f,15", echoDecay, int32(fixed.QConst16(f32(.6), 15))},
		{".8f,15", c080, int32(fixed.QConst16(f32(.8), 15))},
		{".05f,15", c005, int32(fixed.QConst16(f32(.05), 15))},
		{".1f,15", c010, int32(fixed.QConst16(f32(.1), 15))},
		{".2f,15", c020, int32(fixed.QConst16(f32(.2), 15))},
		{".4f,15", c040, int32(fixed.QConst16(f32(.4), 15))},
		{".89f,15", c089, int32(fixed.QConst16(f32(.89), 15))},
		{".7f,15", c070, int32(fixed.QConst16(f32(.7), 15))},
		{".3f,15", c030, int32(fixed.QConst16(f32(.3), 15))},
		{".15f,15", c015, int32(fixed.QConst16(f32(.15), 15))},
		{".075f,15", c0075, int32(fixed.QConst16(f32(.075), 15))},
		{".899f,15", c0899, int32(fixed.QConst16(f32(.899), 15))},
		{".199f,15", c0199, int32(fixed.QConst16(f32(.199), 15))},
		{".333f,15", c0333, int32(fixed.QConst16(f32(.333), 15))},
		{".95f,15", c095, int32(fixed.QConst16(f32(.95), 15))},
		{"1.f,SNR_SHIFT", snrOne, int32(fixed.QConst16(1, snrShift))},
		{"100.f,SNR_SHIFT", snr100, int32(fixed.QConst16(100, snrShift))},
		{"3.,SNR_SHIFT", snr3, int32(fixed.QConst16(3, snrShift))},
		{"1.f,EXPIN_SHIFT", expinOne, fixed.QConst32(1, expinShift)},
		{"1.f,NOISE_SHIFT", noiseOne, fixed.QConst32(1, noiseShift)},
	} {
		if tc.got != tc.want {
			t.Errorf("QCONST(%s) = %d in the port, want %d", tc.name, tc.got, tc.want)
		}
	}
}

func TestNewErrors(t *testing.T) {
	for n := -1; n <= 2100; n++ {
		_, err := New(Config{SampleRate: 16000, FrameSize: n})
		want := n >= 1 && n <= MaxFrameSize && fft.Supported(2*n)
		if (err == nil) != want {
			t.Fatalf("New(FrameSize %d): err %v, want supported=%v", n, err, want)
		}
		if err != nil && !errors.Is(err, ErrConfig) {
			t.Fatalf("New(FrameSize %d): %v does not wrap ErrConfig", n, err)
		}
	}
	for _, r := range []int{math.MinInt, -1, 0, 1, 2, 3, 65536, 96000, math.MaxInt} {
		if _, err := New(Config{SampleRate: r, FrameSize: 160}); !errors.Is(err, ErrConfig) {
			t.Errorf("New(SampleRate %d): err %v, want ErrConfig", r, err)
		}
	}
	for _, r := range []int{MinSampleRate, MaxSampleRate} {
		if _, err := New(Config{SampleRate: r, FrameSize: 160}); err != nil {
			t.Errorf("New(SampleRate %d): %v", r, err)
		}
	}
}

// The filterbank init divides by bankDivisor (filterbank.c:96) and by
// mel_interval; the rate range is exactly where both are nonzero.
func TestFilterbankDivisors(t *testing.T) {
	for r := int32(1); r < MinSampleRate; r++ {
		if d := bankDivisor(r, nbBands); d != 0 {
			t.Errorf("bankDivisor(%d) = %d, want 0 below MinSampleRate", r, d)
		}
	}
	for r := int32(MinSampleRate); r <= MaxSampleRate; r++ {
		if d := bankDivisor(r, nbBands); d <= 0 {
			t.Fatalf("bankDivisor(%d) = %d, want > 0", r, d)
		}
	}
	// Indices stay within the bands for every rate.
	for r := int32(MinSampleRate); r <= MaxSampleRate; r += 1 + r/64 {
		for _, n := range []int{1, 64, 2048} {
			b := newFilterbank(nbBands, r, n)
			for i := range b.left {
				if b.left[i] < 0 || b.right[i] >= nbBands || b.right[i] < b.left[i] {
					t.Fatalf("rate %d n %d bin %d: bands %d, %d", r, n, i, b.left[i], b.right[i])
				}
			}
		}
	}
}

// Divisors shown nonzero in docs/algorithms.md: Q15_ONE-q >= 882 from the
// qcurve range, and hypergeom_gain's for every reachable theta.
func TestRunDivisors(t *testing.T) {
	lo, hi := int16(math.MaxInt16), int16(math.MinInt16)
	for x := math.MinInt16; x <= math.MaxInt16; x++ {
		q := qcurve(int16(x))
		lo, hi = min(lo, q), max(hi, q)
	}
	if lo != 831 || hi != 32767 {
		t.Errorf("qcurve range [%d, %d], want [831, 32767]", lo, hi)
	}
	pFrameMin := int16(c010 + fixed.Mult16x16Q15(c0899, lo))
	p1Min := int16(c0199 + fixed.Mult16x16Q15(c080, lo))
	if d := fixed.Mult16x16Q15(pFrameMin, p1Min); d != 882 {
		t.Errorf("smallest Q15_ONE-q = %d, want 882", d)
	}
	// theta = MULT16_32_P15(prior_ratio, 2048 + post<<3): extreme at the
	// corners, as it is monotone in each argument.
	var maxTheta int32
	for _, pr := range []int16{math.MinInt16, math.MaxInt16} {
		for _, post := range []int16{math.MinInt16, snr100} {
			th := fixed.Mult16x32P15(pr, expinOne+fixed.Shl32(int32(post), expinShift-snrShift))
			maxTheta = max(maxTheta, fixed.Abs32(th))
		}
	}
	for th := -maxTheta; th <= maxTheta; th++ {
		hypergeomGain(th) // panics on a zero divisor
	}
}

// The sites that can see a zero divisor give 0, as on arm64.
func TestZeroDivisorSites(t *testing.T) {
	if got := div32x16Q8(-1000, 0); got != 0 {
		t.Errorf("div32x16Q8(-1000, 0) = %d, want 0", got)
	}
	if got := div32x16Q15(-1, 0); got != 0 {
		t.Errorf("div32x16Q15(-1, 0) = %d, want 0", got)
	}
	if got := div32x16Q8(-1<<30, -65536); got != 0 {
		t.Errorf("div32x16Q8 with a divisor truncating to 0 = %d, want 0", got)
	}
	if got := priorRatio(-256); got != 0 {
		t.Errorf("priorRatio(-256) = %d, want 0", got)
	}
}

func mustNew(t testing.TB, rate, n int) *Preprocessor {
	t.Helper()
	p, err := New(Config{SampleRate: rate, FrameSize: n})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func runAll(t testing.TB, p *Preprocessor, x []int16, residual []int32) ([]int16, []bool) {
	t.Helper()
	n := p.FrameSize()
	out := slices.Clone(x)
	var vad []bool
	for f := 0; f+n <= len(out); f += n {
		var v bool
		var err error
		if residual != nil {
			v, err = p.RunEcho(out[f:f+n], residual[f:f+n])
		} else {
			v, err = p.Run(out[f : f+n])
		}
		if err != nil {
			t.Fatal(err)
		}
		vad = append(vad, v)
	}
	return out, vad
}

func TestBufferSize(t *testing.T) {
	const rate, n = 16000, 160
	x := signal("speech_noise", rate, 50*n)
	p, ref := mustNew(t, rate, n), mustNew(t, rate, n)
	frame := slices.Clone(x[:n+1])
	for _, l := range []int{0, 1, n - 1, n + 1} {
		if _, err := p.Run(frame[:l]); err != ErrBufferSize {
			t.Errorf("Run(len %d): %v, want ErrBufferSize", l, err)
		}
		if _, err := p.RunEcho(frame[:l], make([]int32, n)); err != ErrBufferSize {
			t.Errorf("RunEcho(len %d): %v, want ErrBufferSize", l, err)
		}
		if err := p.EstimateUpdate(frame[:l]); err != ErrBufferSize {
			t.Errorf("EstimateUpdate(len %d): %v, want ErrBufferSize", l, err)
		}
	}
	if _, err := p.RunEcho(frame[:n], make([]int32, n-1)); err != ErrBufferSize {
		t.Errorf("RunEcho with %d residual values: %v, want ErrBufferSize", n-1, err)
	}
	if !slices.Equal(frame, x[:n+1]) {
		t.Error("a rejected call changed the frame")
	}
	for _, f := range []func([]int32) error{p.PSD, p.NoisePSD} {
		if err := f(make([]int32, n-1)); err != ErrBufferSize {
			t.Errorf("PSD with %d values: %v, want ErrBufferSize", n-1, err)
		}
	}
	// The rejected calls left no trace: output equals an untouched instance.
	got, _ := runAll(t, p, x, nil)
	want, _ := runAll(t, ref, x, nil)
	if !slices.Equal(got, want) {
		t.Error("output after rejected calls differs from a fresh instance")
	}
}

func TestZeroPreprocessor(t *testing.T) {
	var p Preprocessor
	frame := make([]int16, 160)
	if _, err := p.Run(frame); err != ErrConfig {
		t.Errorf("Run: %v, want ErrConfig", err)
	}
	if _, err := p.RunEcho(frame, make([]int32, 160)); err != ErrConfig {
		t.Errorf("RunEcho: %v, want ErrConfig", err)
	}
	if err := p.EstimateUpdate(frame); err != ErrConfig {
		t.Errorf("EstimateUpdate: %v, want ErrConfig", err)
	}
	p.Reset()
	if err := p.PSD(nil); err != nil {
		t.Errorf("PSD: %v", err)
	}
}

// RunEcho reads residual[:FrameSize()] and ignores the rest.
func TestRunEchoExtraResidual(t *testing.T) {
	const rate, n = 16000, 160
	x := signal("speech_noise", rate, 30*n)
	r := testutil.SplitMix64{State: 7}
	res := make([]int32, len(x))
	for i := range res {
		res[i] = int32(r.Next() >> 40)
	}
	a, b := mustNew(t, rate, n), mustNew(t, rate, n)
	want, _ := runAll(t, a, x, res)
	got := slices.Clone(x)
	long := make([]int32, 2*n)
	for f := 0; f < len(x); f += n {
		copy(long, res[f:f+n])
		for i := n; i < 2*n; i++ {
			long[i] = int32(r.Next())
		}
		if _, err := b.RunEcho(got[f:f+n], long); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(got, want) {
		t.Error("values past FrameSize changed the output")
	}
}

func configure(p *Preprocessor) {
	p.SetVAD(true)
	p.SetNoiseSuppress(25)
	p.SetEchoSuppress(30)
	p.SetEchoSuppressActive(10)
	p.SetProbStart(50)
	p.SetProbContinue(30)
}

func TestResetPreprocessor(t *testing.T) {
	const rate, n = 48000, 480
	x := signal("speech_noise", rate, 200*n)
	res := make([]int32, len(x))
	for i := range res {
		res[i] = int32(x[i]) * int32(x[i]) / 64
	}
	p := mustNew(t, rate, n)
	configure(p)
	runAll(t, p, x, res)
	if err := p.EstimateUpdate(x[:n]); err != nil {
		t.Fatal(err)
	}
	p.Reset()
	fresh := mustNew(t, rate, n)
	configure(fresh)
	for _, r := range [][]int32{nil, res} {
		got, gv := runAll(t, p, x, r)
		want, wv := runAll(t, fresh, x, r)
		if !slices.Equal(got, want) || !slices.Equal(gv, wv) {
			t.Errorf("after Reset (residual %v), output differs from a fresh instance", r != nil)
		}
		p.Reset()
		fresh.Reset()
	}
	if !p.VAD() || p.NoiseSuppress() != -25 || p.ProbStart() != 49 {
		t.Error("Reset changed the settings")
	}
}

func TestNoAllocs(t *testing.T) {
	const rate, n = 32000, 640
	p := mustNew(t, rate, n)
	p.SetVAD(true)
	x := signal("speech_noise", rate, 40*n)
	res := make([]int32, n)
	buf := make([]int32, n)
	frame := make([]int16, n)
	runAll(t, p, x, nil)
	f := 0
	testutil.AssertNoAllocs(t, 100, func() {
		copy(frame, x[f*n:(f+1)*n])
		f = (f + 1) % 40
		_, _ = p.Run(frame)
		_, _ = p.RunEcho(frame, res)
		_ = p.EstimateUpdate(frame)
		_ = p.PSD(buf)
		_ = p.NoisePSD(buf)
		_, _ = p.Run(frame[:1])
		p.SetNoiseSuppress(int32(f))
		p.SetProbStart(f)
		_ = p.SpeechProb()
	})
	testutil.AssertNoAllocs(t, 10, p.Reset)
}

// FuzzRun checks that no frame content, residual or setting panics, and
// that the VAD is always true while VAD is off.
func FuzzRun(f *testing.F) {
	f.Add([]byte{0, 0, 1, 2}, int32(-15), int32(-40), int32(-15), 35, 20, uint8(3))
	f.Add([]byte{0xff, 0x7f, 0, 0x80}, int32(math.MinInt32), int32(math.MaxInt32), int32(0), 0, 100, uint8(0))
	f.Add([]byte{0x00, 0x80}, int32(32768), int32(-32769), int32(1), -5, 1000, uint8(255))
	ps := make([]*Preprocessor, 0, 3)
	for _, c := range [][2]int{{8000, 1}, {16000, 160}, {48000, 480}} {
		ps = append(ps, mustNew(f, c[0], c[1]))
	}
	f.Fuzz(func(t *testing.T, data []byte, ns, es, esa int32, pStart, pCont int, mode uint8) {
		p := ps[int(mode)%len(ps)]
		p.Reset()
		p.SetNoiseSuppress(ns)
		p.SetEchoSuppress(es)
		p.SetEchoSuppressActive(esa)
		p.SetProbStart(pStart)
		p.SetProbContinue(pCont)
		p.SetDenoise(mode&4 == 0)
		p.SetVAD(mode&8 == 0)
		n := p.FrameSize()
		frame := make([]int16, n)
		res := make([]int32, n)
		r := testutil.SplitMix64{State: uint64(len(data))}
		for fr := 0; fr < 12; fr++ {
			for i := range frame {
				if len(data) > 1 {
					k := (fr*n + i) * 2 % (len(data) - 1)
					frame[i] = int16(uint16(data[k]) | uint16(data[k+1])<<8)
				}
				res[i] = int32(r.Next())
				if mode&16 != 0 {
					res[i] = int32(r.Next() >> 33) // nonnegative, as a power spectrum is
				}
			}
			var v bool
			var err error
			switch {
			case mode&32 != 0 && fr%3 == 0:
				err = p.EstimateUpdate(frame)
				v = true
			case mode&64 != 0:
				v, err = p.RunEcho(frame, res)
			default:
				v, err = p.Run(frame)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !p.VAD() && !v {
				t.Fatal("VAD off but Run reported no speech")
			}
		}
	})
}

// DIV32_16_Q8 truncates a quotient of 32768 to -32768 (docs/algorithms.md).
func TestDiv32x16Q8Wraps(t *testing.T) {
	for b := int32(1); b < 1<<15; b++ {
		got := div32x16Q8(128*b-1, b)
		if want := b >= 512; (got == -32768) != want {
			t.Fatalf("div32x16Q8(%d, %d) = %d", 128*b-1, b, got)
		}
	}
}
