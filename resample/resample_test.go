package resample

import (
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

func mustNew(t testing.TB, cfg Config) *Resampler {
	t.Helper()
	r, err := New(cfg)
	if err != nil {
		t.Fatalf("New(%+v): %v", cfg, err)
	}
	return r
}

func TestNewInvalid(t *testing.T) {
	for _, cfg := range []Config{
		{Channels: 0, InRate: 48000, OutRate: 16000},
		{Channels: -1, InRate: 48000, OutRate: 16000},
		{Channels: 1, InRate: 0, OutRate: 16000},
		{Channels: 1, InRate: 48000, OutRate: 0},
		{Channels: 1, InRate: -48000, OutRate: 16000},
		{Channels: 1, InRate: 48000, OutRate: 16000, Quality: -1},
		{Channels: 1, InRate: 48000, OutRate: 16000, Quality: 11},
		{Channels: 1, InRate: 48000, OutRate: 16000, RatioNum: 3},
		{Channels: 1, InRate: 48000, OutRate: 16000, RatioDen: 1},
		{Channels: 1, RatioNum: -3, RatioDen: 1},
		{Channels: 1, InRate: 1 << 33, OutRate: 16000},
		{Channels: 1, RatioNum: 1 << 33, RatioDen: 1},
		{Channels: 1 << 33, InRate: 48000, OutRate: 16000},
	} {
		if r, err := New(cfg); !errors.Is(err, ErrConfig) || r != nil {
			t.Errorf("New(%+v) = %v, %v; want nil, ErrConfig", cfg, r, err)
		}
	}
	if _, err := New(Config{Channels: 1, RatioNum: 600000000, RatioDen: 1}); !errors.Is(err, ErrAllocFailed) {
		t.Errorf("ratio 600000000:1: %v, want ErrAllocFailed", err)
	}
}

func TestSettersInvalid(t *testing.T) {
	r := mustNew(t, Config{Channels: 2, InRate: 48000, OutRate: 16000, Quality: 5})
	for _, err := range []error{
		r.SetQuality(-1), r.SetQuality(11), r.SetRate(0, 16000), r.SetRate(48000, -1),
		r.SetRateFrac(1, 0, 1, 1), r.SetRateFrac(1, 1, -1, 1), r.SetRateFrac(1<<33, 1, 1, 1),
	} {
		if !errors.Is(err, ErrConfig) {
			t.Errorf("got %v, want ErrConfig", err)
		}
	}
	if q := r.Quality(); q != 5 {
		t.Errorf("quality %d after invalid calls, want 5", q)
	}
	if in, out := r.Rate(); in != 48000 || out != 16000 {
		t.Errorf("rate %d:%d after invalid calls", in, out)
	}
	if n, d := r.Ratio(); n != 3 || d != 1 {
		t.Errorf("ratio %d:%d, want 3:1", n, d)
	}
	if r.Channels() != 2 {
		t.Errorf("channels %d", r.Channels())
	}
}

func TestProcessArgumentErrors(t *testing.T) {
	r := mustNew(t, Config{Channels: 2, InRate: 16000, OutRate: 8000, Quality: 3})
	in := make([]int16, 6)
	out := make([]int16, 6)
	if _, _, err := r.Process(in[:5], out); err != ErrBufferSize {
		t.Errorf("odd input: %v", err)
	}
	if _, _, err := r.Process(in, out[:5]); err != ErrBufferSize {
		t.Errorf("odd output: %v", err)
	}
	fin, fout := make([]float32, 6), make([]float32, 6)
	if _, _, err := r.ProcessFloat(fin[:3], fout); err != ErrBufferSize {
		t.Errorf("odd float input: %v", err)
	}
	for _, ch := range []int{-1, 2} {
		if _, _, err := r.ProcessChannel(ch, in, 1, out, 1); err != ErrChannel {
			t.Errorf("channel %d: %v", ch, err)
		}
		if _, _, err := r.ProcessChannelFloat(ch, fin, 1, fout, 1); err != ErrChannel {
			t.Errorf("float channel %d: %v", ch, err)
		}
	}
	for _, s := range [][2]int{{0, 1}, {1, 0}, {-2, 1}} {
		if _, _, err := r.ProcessChannel(0, in, s[0], out, s[1]); err != ErrStride {
			t.Errorf("strides %v: %v", s, err)
		}
		if _, _, err := r.ProcessChannelFloat(0, fin, s[0], fout, s[1]); err != ErrStride {
			t.Errorf("float strides %v: %v", s, err)
		}
	}
	// Empty calls are valid and do nothing.
	for _, f := range []func() (int, int, error){
		func() (int, int, error) { return r.Process(nil, nil) },
		func() (int, int, error) { return r.Process(in, nil) },
		func() (int, int, error) { return r.Process(nil, out) },
		func() (int, int, error) { return r.ProcessFloat(nil, nil) },
		func() (int, int, error) { return r.ProcessChannel(1, nil, 1, out, 1) },
		func() (int, int, error) { return r.ProcessChannelFloat(1, fin, 2, nil, 1) },
	} {
		if a, b, err := f(); a != 0 || b != 0 || err != nil {
			t.Errorf("empty call: %d, %d, %v", a, b, err)
		}
	}
}

func TestFramesClamp(t *testing.T) {
	type tc struct {
		n, stride int
		want      uint32
	}
	cases := []tc{{0, 1, 0}, {1, 1, 1}, {5, 2, 3}, {6, 2, 3}, {7, 3, 3}, {math.MaxInt32, 1, math.MaxInt32}}
	if math.MaxInt > math.MaxInt32 {
		cases = append(cases, tc{math.MaxInt32 + 1, 1, math.MaxInt32}, tc{1 << 40, 1, math.MaxInt32})
	}
	for _, c := range cases {
		if got := frames(c.n, c.stride); got != c.want {
			t.Errorf("frames(%d, %d) = %d, want %d", c.n, c.stride, got, c.want)
		}
	}
}

// TestFloatNonFinite checks the Go-only NaN policy: NaN input is processed
// as 0. ±Inf and the boundaries are compared with C in the fixtures.
func TestFloatNonFinite(t *testing.T) {
	nan := float32(math.NaN())
	in := testutil.ToFloat32(testutil.WhiteNoise(960, 20000, 7))
	zeroed := slices.Clone(in)
	for i := 0; i < len(in); i += 13 {
		in[i] = nan
		zeroed[i] = 0
	}
	a := mustNew(t, Config{Channels: 1, InRate: 48000, OutRate: 16000, Quality: 5})
	b := mustNew(t, Config{Channels: 1, InRate: 48000, OutRate: 16000, Quality: 5})
	outA, outB := make([]float32, 400), make([]float32, 400)
	_, na, err := a.ProcessFloat(in, outA)
	if err != nil {
		t.Fatal(err)
	}
	_, nb, _ := b.ProcessFloat(zeroed, outB)
	if !slices.Equal(outA[:na], outB[:nb]) {
		t.Error("NaN input differs from zero input")
	}
	for _, v := range outA[:na] {
		if v != v || v != float32(int16(v)) {
			t.Fatalf("output %v is not an int16 value", v)
		}
	}
}

// TestFloatMatchesInt checks that float32 output is the exact int16 result
// for integer-valued input.
func TestFloatMatchesInt(t *testing.T) {
	x := testutil.WhiteNoise(4410, 32767, 3)
	a := mustNew(t, Config{Channels: 1, InRate: 44100, OutRate: 48000, Quality: 8})
	b := mustNew(t, Config{Channels: 1, InRate: 44100, OutRate: 48000, Quality: 8})
	oi := make([]int16, 5000)
	of := make([]float32, 5000)
	_, ni, _ := a.Process(x, oi)
	_, nf, _ := b.ProcessFloat(testutil.ToFloat32(x), of)
	if ni != nf || !slices.Equal(testutil.ToFloat32(oi[:ni]), of[:nf]) {
		t.Errorf("float output differs from int16 output (%d vs %d samples)", nf, ni)
	}
}

func TestStridesLeaveOtherElements(t *testing.T) {
	r := mustNew(t, Config{Channels: 2, InRate: 8000, OutRate: 16000, Quality: 4})
	in := testutil.WhiteNoise(400, 30000, 11)
	const sentinel = int16(-12345)
	out := make([]int16, 3*500)
	for i := range out {
		out[i] = sentinel
	}
	_, n, err := r.ProcessChannel(1, in[1:], 2, out, 3)
	if err != nil || n == 0 {
		t.Fatalf("%d, %v", n, err)
	}
	for i, v := range out {
		if i%3 != 0 || i/3 >= n {
			if v != sentinel {
				t.Fatalf("out[%d] = %d was written", i, v)
			}
		}
	}
	fout := make([]float32, 3*500)
	for i := range fout {
		fout[i] = 0.25
	}
	_, n, _ = r.ProcessChannelFloat(0, testutil.ToFloat32(in), 2, fout, 3)
	for i, v := range fout {
		if (i%3 != 0 || i/3 >= n) && v != 0.25 {
			t.Fatalf("float out[%d] = %v was written", i, v)
		}
	}
}

// processAll feeds in through r in calls of the given sizes (frames) with
// the given output capacities, re-offering unconsumed input, and returns
// the concatenated output.
func processAll(t testing.TB, r *Resampler, in []int16, sizes, caps []int) []int16 {
	t.Helper()
	nch := r.Channels()
	var got []int16
	buf := make([]int16, 0)
	pos, k, stall := 0, 0, 0
	for pos < len(in)/nch {
		size, c := sizes[k%len(sizes)], caps[k%len(caps)]
		k++
		n := min(size, len(in)/nch-pos)
		if cap(buf) < c*nch {
			buf = make([]int16, c*nch)
		}
		il, ol, err := r.Process(in[pos*nch:(pos+n)*nch], buf[:c*nch])
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		got = append(got, buf[:ol*nch]...)
		pos += il
		if il == 0 && ol == 0 {
			if stall++; stall > 1000 {
				t.Fatalf("no progress at frame %d", pos)
			}
		} else {
			stall = 0
		}
	}
	return got
}

var invarianceConfigs = []Config{
	{Channels: 1, InRate: 48000, OutRate: 16000, Quality: 3},
	{Channels: 1, InRate: 16000, OutRate: 48000, Quality: 10},
	{Channels: 1, InRate: 44100, OutRate: 48000, Quality: 5},
	{Channels: 2, InRate: 48000, OutRate: 44100, Quality: 8},
	{Channels: 3, InRate: 8000, OutRate: 96000, Quality: 0},
	{Channels: 1, RatioNum: 1000003, RatioDen: 999983, InRate: 48000, OutRate: 48000, Quality: 4},
	{Channels: 1, InRate: 96000, OutRate: 8000, Quality: 10},
}

func TestChunkInvarianceProcess(t *testing.T) {
	partitions := []struct{ sizes, caps []int }{
		{[]int{1}, []int{1}},
		{[]int{7}, []int{3, 1000}},
		{[]int{160, 0, 13}, []int{1 << 16}},
		{[]int{1000, 1, 331}, []int{0, 5, 1 << 16}},
	}
	for _, cfg := range invarianceConfigs {
		in := testutil.SpeechLike(8000*cfg.Channels, 16000, 20000, testutil.Seed("inv"))
		want := processAll(t, mustNew(t, cfg), in, []int{len(in)}, []int{1 << 20})
		if len(want) == 0 {
			t.Fatalf("%+v: no output", cfg)
		}
		for _, p := range partitions {
			got := processAll(t, mustNew(t, cfg), in, p.sizes, p.caps)
			if !testutil.Compare(t, "partitioned output", want, got) {
				t.Errorf("config %+v, partition %v/%v", cfg, p.sizes, p.caps)
			}
		}
	}
}

// FuzzChunksResampler checks chunk-partition invariance and that no
// partition panics.
func FuzzChunksResampler(f *testing.F) {
	f.Add(uint8(0), []byte{1, 2, 3, 200, 0, 7})
	f.Add(uint8(3), []byte{255, 255, 0, 0, 1, 1})
	f.Add(uint8(6), []byte{17, 4, 99, 1, 0, 160})
	f.Fuzz(func(t *testing.T, which uint8, plan []byte) {
		if len(plan) < 2 {
			return
		}
		cfg := invarianceConfigs[int(which)%len(invarianceConfigs)]
		in := testutil.WhiteNoise(2000*cfg.Channels, 32767, uint64(which))
		want := processAll(t, mustNew(t, cfg), in, []int{len(in)}, []int{1 << 20})
		var sizes, caps []int
		for i := 0; i+1 < len(plan); i += 2 {
			sizes = append(sizes, int(plan[i])*3)
			caps = append(caps, int(plan[i+1]))
		}
		// One call that offers both input and output guarantees progress.
		sizes = append(sizes, 1)
		caps = append(caps, 1)
		got := processAll(t, mustNew(t, cfg), in, sizes, caps)
		testutil.Compare(t, "partitioned output", want, got)
	})
}

// FuzzConfigResampler drives arbitrary configurations and call sequences
// and checks that nothing panics and that invalid arguments are rejected.
// Downsampling factors are kept to at most 64 so that filters stay small.
func FuzzConfigResampler(f *testing.F) {
	f.Add([]byte{1, 0x80, 0xbb, 0x3e, 0x80, 5, 1, 2, 3, 4, 5, 6, 7, 8})
	f.Add([]byte{3, 0xff, 0xff, 0xff, 0xff, 10, 9, 9, 0, 1, 2, 250, 251})
	f.Add([]byte{2, 0x11, 0x00, 0x00, 0x03, 0, 4, 4, 4, 5, 5, 6, 6})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 6 {
			return
		}
		rd := byteReader{data: data}
		ch := int(rd.byte()%4) + 1
		den := int(rd.u32()%math.MaxUint32) + 1
		num := pickNum(den, rd.u32())
		q := int(rd.byte()) % 12
		r, err := New(Config{Channels: ch, RatioNum: num, RatioDen: den, Quality: q})
		if q > 10 {
			if !errors.Is(err, ErrConfig) {
				t.Fatalf("quality %d: %v", q, err)
			}
			return
		}
		if err != nil {
			if !errors.Is(err, ErrAllocFailed) {
				t.Fatalf("New: %v", err)
			}
			return
		}
		in := testutil.WhiteNoise(4096, 32767, 1)
		fin := testutil.ToFloat32(in)
		out := make([]int16, 4096)
		fout := make([]float32, 4096)
		for !rd.done() {
			switch op := rd.byte() % 9; op {
			case 0, 1:
				n := int(rd.byte()) * ch
				m := int(rd.byte()) * ch
				r.Process(in[:n], out[:m])
			case 2:
				n := int(rd.byte()) * ch
				m := int(rd.byte()) * ch
				r.ProcessFloat(fin[:n], fout[:m])
			case 3:
				c := int(rd.byte()) % (ch + 1)
				s := int(rd.byte())%4 + 1
				_, _, err := r.ProcessChannel(c, in[:int(rd.byte())*s], s, out[:int(rd.byte())*s], s)
				if c == ch && err != ErrChannel {
					t.Fatalf("channel %d: %v", c, err)
				}
			case 4:
				c := int(rd.byte()) % ch
				r.ProcessChannelFloat(c, fin[:int(rd.byte())], 1, fout[:int(rd.byte())], 2)
			case 5:
				r.SetQuality(int(rd.byte()) % 11)
			case 6:
				d := int(rd.u32()%math.MaxUint32) + 1
				r.SetRateFrac(pickNum(d, rd.u32()), d, 48000, 48000)
			case 7:
				r.Reset()
			case 8:
				r.SkipZeros()
			}
			_ = r.InputLatency()
			_ = r.OutputLatency()
		}
	})
}

// pickNum returns a numerator for den with num/den at most 64.
func pickNum(den int, v uint32) int {
	limit := min(uint64(den)*64, math.MaxUint32)
	return int(uint64(v)%limit) + 1
}

type byteReader struct {
	data []byte
	i    int
}

func (b *byteReader) done() bool { return b.i >= len(b.data) }

func (b *byteReader) byte() byte {
	if b.i >= len(b.data) {
		return 0
	}
	b.i++
	return b.data[b.i-1]
}

func (b *byteReader) u32() uint32 {
	return uint32(b.byte())<<24 | uint32(b.byte())<<16 | uint32(b.byte())<<8 | uint32(b.byte())
}

func TestResetMonoEqualsNew(t *testing.T) {
	for _, cfg := range invarianceConfigs {
		if cfg.Channels != 1 {
			continue
		}
		in := testutil.SpeechLike(6000, 16000, 20000, 5)
		r := mustNew(t, cfg)
		processAll(t, r, testutil.WhiteNoise(3001, 32767, 9), []int{97}, []int{50})
		r.Reset()
		got := processAll(t, r, in, []int{333}, []int{1 << 14})
		want := processAll(t, mustNew(t, cfg), in, []int{333}, []int{1 << 14})
		if !testutil.Compare(t, "after Reset", want, got) {
			t.Errorf("config %+v", cfg)
		}
	}
}

// TestResetMultichannelKeepsHistory documents the reference behaviour: for
// two channels, Reset leaves part of the second channel's history.
func TestResetMultichannelKeepsHistory(t *testing.T) {
	cfg := Config{Channels: 2, InRate: 48000, OutRate: 16000, Quality: 5}
	r := mustNew(t, cfg)
	processAll(t, r, testutil.WhiteNoise(6000, 32767, 9), []int{480}, []int{1 << 10})
	r.Reset()
	in := make([]int16, 960)
	got := processAll(t, r, in, []int{480}, []int{1 << 10})
	var ch0, ch1 bool
	for i, v := range got {
		if v != 0 {
			if i%2 == 0 {
				ch0 = true
			} else {
				ch1 = true
			}
		}
	}
	if ch0 || !ch1 {
		t.Errorf("after Reset with silent input: channel 0 non-zero %v, channel 1 non-zero %v; want false, true", ch0, ch1)
	}
}

func TestSetRateFracOverflowLeavesStateUnchanged(t *testing.T) {
	cfg := Config{Channels: 2, RatioNum: 1000003, RatioDen: 999983, InRate: 48000, OutRate: 48000, Quality: 5}
	a, b := mustNew(t, cfg), mustNew(t, cfg)
	warm := testutil.WhiteNoise(2*7001, 32767, 4)
	processAll(t, a, warm, []int{7001}, []int{1 << 14})
	processAll(t, b, warm, []int{7001}, []int{1 << 14})
	if a.sampFracNum[0] <= math.MaxUint32/1000003 && a.sampFracNum[1] <= math.MaxUint32/1000003 {
		t.Fatalf("fractional positions %v too small to overflow", a.sampFracNum)
	}
	if err := a.SetRateFrac(999983, 1000003, 1, 2); err != ErrOverflow {
		t.Fatalf("SetRateFrac: %v, want ErrOverflow", err)
	}
	if in, out := a.Rate(); in != 48000 || out != 48000 {
		t.Errorf("rates changed to %d:%d", in, out)
	}
	in := testutil.WhiteNoise(2*3000, 32767, 5)
	got := processAll(t, a, in, []int{301}, []int{1 << 14})
	want := processAll(t, b, in, []int{301}, []int{1 << 14})
	testutil.Compare(t, "after ErrOverflow", want, got)
}

func TestAllocFailedOutputsZerosAndRecovers(t *testing.T) {
	r := mustNew(t, Config{Channels: 1, InRate: 44100, OutRate: 48000, Quality: 10})
	in := testutil.WhiteNoise(4410, 32767, 2)
	out := make([]int16, 5000)
	r.Process(in, out)
	if err := r.SetRateFrac(600000000, 1, 44100, 48000); err != ErrAllocFailed {
		t.Fatalf("SetRateFrac: %v", err)
	}
	n := 0
	for k := 0; k < 4; k++ {
		_, o, err := r.Process(in, out)
		if err != ErrAllocFailed {
			t.Fatalf("Process: %v", err)
		}
		for _, v := range out[:o] {
			if v != 0 {
				t.Fatal("non-zero output in the failed state")
			}
		}
		n += o
	}
	if n == 0 {
		t.Fatal("failed state produced no output")
	}
	if err := r.SetRate(44100, 48000); err != nil {
		t.Fatalf("SetRate: %v", err)
	}
	if _, _, err := r.Process(in, out); err != nil {
		t.Errorf("Process after recovery: %v", err)
	}
}

// TestLongRunBounded checks that state does not grow over a long stream
// with rate and quality changes.
func TestLongRunBounded(t *testing.T) {
	r := mustNew(t, Config{Channels: 2, InRate: 48000, OutRate: 16000, Quality: 10})
	in := testutil.WhiteNoise(2*480, 32767, 8)
	out := make([]int16, 2*480)
	r.Process(in, out)
	r.SetRate(48000, 44100)
	r.SetRate(48000, 16000)
	memLen, tableLen := len(r.mem), len(r.sincTable)
	frames := 0
	for frames < 48000*60 {
		_, _, err := r.Process(in, out)
		if err != nil {
			t.Fatal(err)
		}
		frames += 480
	}
	if len(r.mem) != memLen || len(r.sincTable) != tableLen {
		t.Errorf("state grew: mem %d -> %d, table %d -> %d", memLen, len(r.mem), tableLen, len(r.sincTable))
	}
	for i := range r.lastSample {
		if r.lastSample[i] < 0 || r.lastSample[i] > int32(r.filtLen) || r.magicSamples[i] != 0 {
			t.Errorf("channel %d: last_sample %d, magic %d", i, r.lastSample[i], r.magicSamples[i])
		}
	}
}

func TestSilenceAndFullScale(t *testing.T) {
	for _, cfg := range invarianceConfigs {
		r := mustNew(t, cfg)
		got := processAll(t, r, make([]int16, 4000*cfg.Channels), []int{400}, []int{1 << 14})
		for _, v := range got {
			if v != 0 {
				t.Fatalf("%+v: silence gives %d", cfg, v)
			}
		}
		// A full-scale square wave overshoots, so the output saturates.
		sq := make([]int16, 8000*cfg.Channels)
		for i := range sq {
			if (i/cfg.Channels/800)%2 == 0 {
				sq[i] = 32767
			} else {
				sq[i] = -32768
			}
		}
		got = processAll(t, mustNew(t, cfg), sq, []int{8000}, []int{1 << 16})
		peak := int16(0)
		for _, v := range got {
			peak = max(peak, v)
		}
		if peak != 32767 {
			t.Errorf("%+v: square wave peak %d, want saturation at 32767", cfg, peak)
		}
	}
}

func TestLatencyQueries(t *testing.T) {
	r := mustNew(t, Config{Channels: 1, InRate: 48000, OutRate: 16000, Quality: 3})
	// q3: base length 48, times 3 for 3:1 downsampling.
	if got := r.InputLatency(); got != 72 {
		t.Errorf("InputLatency %d, want 72", got)
	}
	if got := r.OutputLatency(); got != 24 {
		t.Errorf("OutputLatency %d, want 24", got)
	}
	// SkipZeros: the first output corresponds to input sample 0.
	r.SkipZeros()
	in := testutil.Impulse(4800, 0, 30000)
	got := processAll(t, r, in, []int{480}, []int{1 << 12})
	peak := 0
	for i, v := range got {
		if v > got[peak] {
			peak = i
		}
	}
	if peak != 0 {
		t.Errorf("impulse peak at output %d after SkipZeros, want 0", peak)
	}
}

func TestNoAllocsProcess(t *testing.T) {
	r := mustNew(t, Config{Channels: 2, InRate: 44100, OutRate: 48000, Quality: 10})
	in := testutil.WhiteNoise(2*441, 30000, 1)
	out := make([]int16, 2*500)
	r.Process(in, out)
	testutil.AssertNoAllocs(t, 100, func() { r.Process(in, out) })
}

func TestNoAllocsProcessFloat(t *testing.T) {
	r := mustNew(t, Config{Channels: 1, InRate: 8000, OutRate: 96000, Quality: 5})
	in := testutil.ToFloat32(testutil.WhiteNoise(800, 30000, 1))
	out := make([]float32, 10000)
	r.ProcessFloat(in, out)
	testutil.AssertNoAllocs(t, 100, func() { r.ProcessFloat(in, out) })
}

func TestNoAllocsProcessChannel(t *testing.T) {
	r := mustNew(t, Config{Channels: 2, InRate: 48000, OutRate: 16000, Quality: 8})
	in := testutil.WhiteNoise(2*480, 30000, 1)
	out := make([]int16, 3*170)
	r.ProcessChannel(1, in[1:], 2, out, 3)
	testutil.AssertNoAllocs(t, 100, func() {
		r.ProcessChannel(0, in, 2, out, 3)
		r.ProcessChannel(1, in[1:], 2, out, 3)
	})
}

func TestNoAllocsProcessChannelFloat(t *testing.T) {
	r := mustNew(t, Config{Channels: 1, InRate: 16000, OutRate: 48000, Quality: 3})
	in := testutil.ToFloat32(testutil.WhiteNoise(160, 30000, 1))
	out := make([]float32, 500)
	r.ProcessChannelFloat(0, in, 1, out, 1)
	testutil.AssertNoAllocs(t, 100, func() { r.ProcessChannelFloat(0, in, 1, out, 1) })
}

// TestNoAllocsMagicAndErrors covers filter changes that need no larger
// buffers, the magic-sample path that follows them, and the error paths.
func TestNoAllocsMagicAndErrors(t *testing.T) {
	r := mustNew(t, Config{Channels: 1, InRate: 48000, OutRate: 16000, Quality: 10})
	in := testutil.WhiteNoise(480, 30000, 1)
	out := make([]int16, 200)
	r.Process(in, out)
	pending := 0
	testutil.AssertNoAllocs(t, 100, func() {
		r.SetQuality(10)
		r.SetQuality(0)
		if r.magicSamples[0] != 0 {
			pending++
		}
		r.Process(in, out)
	})
	if pending == 0 {
		t.Fatal("no magic samples were pending")
	}
	testutil.AssertNoAllocs(t, 100, func() {
		r.Process(in[:3], out)
		r.ProcessChannel(5, in, 1, out, 1)
		r.ProcessChannel(0, in, 0, out, 1)
	})
	r.SetRateFrac(600000000, 1, 1, 1)
	testutil.AssertNoAllocs(t, 100, func() { r.Process(in, out) })
}
