package resample

import (
	"fmt"
	"math"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

// outLenSignal is the input every OutLen test draws from.
var outLenSignal = testutil.WhiteNoise(256*1024, 32767, testutil.Seed("maxout"))

// channelsInSync reports whether every channel has the same position state,
// so that the interleaved count of the last channel is every channel's.
func channelsInSync(r *Resampler) bool {
	for ch := 1; ch < len(r.lastSample); ch++ {
		if r.lastSample[ch] != r.lastSample[0] || r.sampFracNum[ch] != r.sampFracNum[0] ||
			r.magicSamples[ch] != r.magicSamples[0] {
			return false
		}
	}
	return true
}

// outLenChecker runs process calls sized by OutLen and checks the
// properties of docs/acceptance/resample.md, change 9.
type outLenChecker struct {
	t         testing.TB
	r         *Resampler
	pos       int
	slack     int // worst OutLen minus frames written, where tightness applies
	zeroSlack int // worst slack in the zero-output state
}

// call offers n frames per channel through method via (see processWith)
// with an output of exactly OutLen(n) frames per channel.
func (c *outLenChecker) call(n, via int) {
	t, r := c.t, c.r
	t.Helper()
	nch := r.Channels()
	if (c.pos+n)*nch > len(outLenSignal) {
		c.pos = 0
	}
	in := outLenSignal[c.pos*nch : (c.pos+n)*nch]
	m := r.OutLen(n)
	tight := (nch == 1 || channelsInSync(r)) && r.kernel != kernelZero
	zero := r.kernel == kernelZero
	var read, wrote int
	switch via {
	case viaProcess:
		out := make([]int16, m*nch)
		read, wrote, _ = r.Process(in, out)
	case viaProcessFloat:
		out := make([]float32, m*nch)
		read, wrote, _ = r.ProcessFloat(testutil.ToFloat32(in), out)
	default:
		// Per channel: each channel's call gets the bound taken before any
		// of them runs, as one call per channel would.
		read, wrote = n, 0
		fin := testutil.ToFloat32(in)
		for ch := range nch {
			var cin []int16
			var cfin []float32
			if n > 0 {
				cin, cfin = in[ch:ch+(n-1)*nch+1], fin[ch:ch+(n-1)*nch+1]
			}
			var cr, cw int
			if via == viaProcessChannel {
				cr, cw, _ = r.ProcessChannel(ch, cin, nch, make([]int16, m), 1)
			} else {
				cr, cw, _ = r.ProcessChannelFloat(ch, cfin, nch, make([]float32, m), 1)
			}
			read = min(read, cr)
			wrote = max(wrote, cw)
		}
	}
	if read != n {
		t.Fatalf("method %d: offered %d frames and OutLen %d output frames, consumed %d", via, n, m, read)
	}
	if wrote > m {
		t.Fatalf("method %d: wrote %d frames, OutLen %d", via, wrote, m)
	}
	want := 0
	if n > 0 {
		want = 1
	}
	// A float32 call offered no input writes nothing, as upstream
	// process_float does (docs/acceptance/resample.md, change 11).
	if n == 0 && (via == viaProcessFloat || via == viaProcessChannelFloat) {
		tight = false
	}
	if tight && m-wrote != want {
		t.Fatalf("method %d: offered %d frames, OutLen %d, wrote %d: slack %d, want %d", via, n, m, wrote, m-wrote, want)
	}
	if tight {
		c.slack = max(c.slack, m-wrote)
	} else if zero {
		c.zeroSlack = max(c.zeroSlack, m-wrote)
	}
	c.pos += n
}

var outLenSizes = []int{0, 1, 2, 3, 7, 80, 160, 161, 331, 441, 480, 960, 1160}

// TestOutLenMatrix checks soundness and tightness over every quality and
// rate pair of the fixture matrix, the fractional ratios, and stereo.
func TestOutLenMatrix(t *testing.T) {
	rates := []int{8000, 16000, 24000, 32000, 44100, 48000, 96000}
	var cfgs []Config
	for q := 0; q <= 10; q++ {
		for _, a := range rates {
			for _, b := range rates {
				cfgs = append(cfgs, Config{Channels: 1, InRate: a, OutRate: b, Quality: q})
			}
		}
		for _, f := range [][2]uint64{{48000, 47999}, {47999, 48000}, {44101, 44100}, {1000003, 999983}, {7, 5},
			{5, 7}, {1, 100}, {100, 1}, {2147483646, 2147483647}, {1000000007, 3000000019}} {
			if f[1] > math.MaxInt32 && math.MaxInt == math.MaxInt32 {
				continue
			}
			cfgs = append(cfgs, Config{Channels: 1, RatioNum: asInt(f[0]), RatioDen: asInt(f[1]), InRate: 1, OutRate: 1, Quality: q})
		}
		cfgs = append(cfgs, Config{Channels: 2, InRate: 44100, OutRate: 48000, Quality: q},
			Config{Channels: 3, InRate: 48000, OutRate: 16000, Quality: q})
	}
	worst := 0
	for i, cfg := range cfgs {
		c := &outLenChecker{t: t, r: mustNew(t, cfg)}
		rng := testutil.SplitMix64{State: uint64(i)}
		for k := range 40 {
			c.call(outLenSizes[rng.Intn(len(outLenSizes))], k%viaCount)
		}
		worst = max(worst, c.slack)
	}
	t.Logf("%d configurations, worst slack %d", len(cfgs), worst)
}

// TestOutLenEvents covers magic samples after filter changes, Reset,
// SkipZeros and the zero-output state after rejected changes.
func TestOutLenEvents(t *testing.T) {
	type event struct {
		name string
		f    func(r *Resampler)
	}
	events := []event{
		{"quality down", func(r *Resampler) { r.SetQuality(0) }},
		{"quality up", func(r *Resampler) { r.SetQuality(10) }},
		{"quality 5", func(r *Resampler) { r.SetQuality(5) }},
		{"rate 48k:8k", func(r *Resampler) { r.SetRate(48000, 8000) }},
		{"rate 8k:48k", func(r *Resampler) { r.SetRate(8000, 48000) }},
		{"rate 44.1k:48k", func(r *Resampler) { r.SetRate(44100, 48000) }},
		{"drift", func(r *Resampler) { r.SetRateFrac(48001, 48000, 1, 1) }},
		{"reset", func(r *Resampler) { r.Reset() }},
		{"skip zeros", func(r *Resampler) { r.SkipZeros() }},
		{"rejected by C", func(r *Resampler) { r.SetRateFrac(600000000, 1, 1, 1) }},
		{"rejected huge", func(r *Resampler) { r.SetRate(upTo(4000000000), 1) }},
		{"over the cap", func(r *Resampler) { r.SetRate(4000000, 1) }},
	}
	worstSlack, worstZero := 0, 0
	for _, ch := range []int{1, 2, 3} {
		for seed := range 30 {
			rng := testutil.SplitMix64{State: uint64(seed*10 + ch)}
			r := mustNew(t, Config{Channels: ch, InRate: 48000, OutRate: 16000, Quality: 10})
			c := &outLenChecker{t: t, r: r}
			for range 60 {
				if rng.Intn(4) == 0 {
					e := events[rng.Intn(len(events))]
					e.f(r)
				}
				c.call(outLenSizes[rng.Intn(len(outLenSizes))], rng.Intn(viaCount))
			}
			worstSlack = max(worstSlack, c.slack)
			worstZero = max(worstZero, c.zeroSlack)
		}
	}
	t.Logf("worst slack %d; in the zero-output state %d", worstSlack, worstZero)
}

// TestOutLenMagicPending checks a case where pending magic samples alone
// produce output: with no input offered, OutLen is still positive.
func TestOutLenMagicPending(t *testing.T) {
	r := mustNew(t, Config{Channels: 1, InRate: 8000, OutRate: 48000, Quality: 10})
	c := &outLenChecker{t: t, r: r}
	c.call(800, viaProcess)
	r.SetQuality(0)
	if r.magicSamples[0] == 0 {
		t.Fatal("no magic samples pending")
	}
	if m := r.OutLen(0); m == 0 {
		t.Fatalf("OutLen(0) = 0 with %d magic samples pending", r.magicSamples[0])
	}
	c.call(0, viaProcess)
	c.call(160, viaProcessFloat)
}

func TestOutLenEdges(t *testing.T) {
	r := mustNew(t, Config{Channels: 2, InRate: 8000, OutRate: 16000, Quality: 3})
	for _, n := range []int{-1, -1000, math.MinInt} {
		if m := r.OutLen(n); m != 0 {
			t.Errorf("OutLen(%d) = %d, want 0", n, m)
		}
	}
	if m := r.OutLen(0); m != 0 {
		t.Errorf("OutLen(0) = %d, want 0", m)
	}
	if m := r.OutLen(math.MaxInt); m != maxFrames {
		t.Errorf("OutLen(MaxInt) = %d, want %d (clamped)", m, maxFrames)
	}
	if math.MaxInt > math.MaxUint32 {
		big := asInt(4294967294)
		w := mustNew(t, Config{Channels: 1, RatioNum: big, RatioDen: big + 1, Quality: 0})
		if m := w.OutLen(1); m != maxFrames {
			t.Errorf("wrapping ratio: OutLen(1) = %d, want %d", m, maxFrames)
		}
	}
	before := fmt.Sprint(r.lastSample, r.sampFracNum, r.magicSamples)
	r.OutLen(500)
	if after := fmt.Sprint(r.lastSample, r.sampFracNum, r.magicSamples); after != before {
		t.Errorf("OutLen changed the state: %s -> %s", before, after)
	}
}

func TestNoAllocsOutLen(t *testing.T) {
	r := mustNew(t, Config{Channels: 3, InRate: 44100, OutRate: 48000, Quality: 10})
	testutil.AssertNoAllocs(t, 100, func() { _ = r.OutLen(441) })
}

// FuzzOutLen draws configurations, events and chunk sequences and checks
// soundness on every call, and tightness for mono streams outside the
// zero-output state.
func FuzzOutLen(f *testing.F) {
	f.Add([]byte{0, 5, 10, 200, 3, 1, 4, 9, 9, 0, 255})
	f.Add([]byte{2, 1, 3, 7, 7, 7, 100, 2, 50, 1})
	f.Add([]byte{1, 9, 0, 0, 0, 13, 13, 13, 6, 6})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 4 {
			return
		}
		rd := byteReader{data: data}
		ch := int(rd.byte()%3) + 1
		den := int(rd.byte())%200 + 1
		num := int(rd.byte())%(den*8) + 1
		r, err := New(Config{Channels: ch, RatioNum: num, RatioDen: den, Quality: int(rd.byte()) % 11})
		if err != nil {
			t.Fatal(err)
		}
		c := &outLenChecker{t: t, r: r}
		for !rd.done() {
			switch op := rd.byte(); op % 8 {
			case 0:
				r.SetQuality(int(rd.byte()) % 11)
			case 1:
				d := int(rd.byte())%200 + 1
				r.SetRateFrac(int(rd.byte())%(d*8)+1, d, 1, 1)
			case 2:
				r.Reset()
			case 3:
				r.SkipZeros()
			case 4:
				r.SetRateFrac(600000000, 1, 1, 1)
			default:
				c.call(int(rd.byte())*5, int(op/8)%viaCount)
			}
		}
	})
}

// TestOutLenReadme runs the README example.
func TestOutLenReadme(t *testing.T) {
	const inRate, outRate, frameMs = 8000, 16000, 20

	r, err := New(Config{
		Channels: 1, InRate: inRate, OutRate: outRate, Quality: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	in := make([]int16, inRate*frameMs/1000) // one 20 ms frame
	out := make([]int16, r.OutLen(len(in)))  // always large enough for one call
	read, written, err := r.Process(in, out)
	if err != nil || read != len(in) || written != len(out)-1 {
		t.Errorf("read %d of %d, wrote %d into %d, %v", read, len(in), written, len(out), err)
	}
}

// TestOutLenDivergedChannels checks the bound when the channels' states
// differ, after per-channel calls of different lengths: OutLen must cover
// the channel that can write most.
func TestOutLenDivergedChannels(t *testing.T) {
	for _, cfg := range []Config{
		{Channels: 2, InRate: 48000, OutRate: 44100, Quality: 5},
		{Channels: 3, InRate: 44100, OutRate: 48000, Quality: 10},
	} {
		for _, ahead := range []int{0, cfg.Channels - 1} {
			for _, k := range []int{1, 3, 5} {
				for via := range viaCount {
					r := mustNew(t, cfg)
					// Leave magic samples pending on every channel, then
					// let one channel process its own and k more frames.
					in := outLenSignal[:800*cfg.Channels]
					r.Process(in, make([]int16, r.OutLen(800)*cfg.Channels))
					r.SetQuality(0)
					r.ProcessChannel(ahead, outLenSignal[:k], 1, make([]int16, 1000), 1)
					if channelsInSync(r) {
						t.Fatal("channels still in sync")
					}
					c := &outLenChecker{t: t, r: r}
					for _, n := range []int{1, 2, 7, 160, 441} {
						c.call(n, via)
					}
				}
			}
		}
	}
}

// TestOutLenWrapBoundary checks the wrap condition at its edge: with
// den = 2^31+1, num = 2^31-1 gives den - 1 + num = 2^32 - 1, which cannot
// wrap, and num = 2^31 gives 2^32, which can.
func TestOutLenWrapBoundary(t *testing.T) {
	if math.MaxInt == math.MaxInt32 {
		t.Skip("needs ratios above 2^31-1")
	}
	den := asInt(1<<31 + 1)
	r := mustNew(t, Config{Channels: 1, RatioNum: asInt(1<<31 - 1), RatioDen: den, Quality: 0})
	if m := r.OutLen(1); m == maxFrames || m > 3 {
		t.Errorf("(2^31-1):(2^31+1) cannot wrap, but OutLen(1) = %d", m)
	}
	w := mustNew(t, Config{Channels: 1, RatioNum: asInt(1 << 31), RatioDen: den, Quality: 0})
	if m := w.OutLen(1); m != maxFrames {
		t.Errorf("2^31:(2^31+1) can wrap, OutLen(1) = %d, want %d", m, maxFrames)
	}
}

// asInt converts at run time, so that tests compile where int is 32 bits
// wide; callers skip there.
func asInt(v uint64) int { return int(v) }

// TestOutLenNoStateChange checks OutLen with magic samples pending.
func TestOutLenNoStateChange(t *testing.T) {
	r := mustNew(t, Config{Channels: 2, InRate: 44100, OutRate: 48000, Quality: 10})
	r.Process(outLenSignal[:2*500], make([]int16, 2*r.OutLen(500)))
	r.SetQuality(0)
	if r.magicSamples[0] == 0 {
		t.Fatal("no magic samples pending")
	}
	before := fmt.Sprint(r.lastSample, r.sampFracNum, r.magicSamples, r.filtLen, len(r.mem))
	for _, n := range []int{0, 1, 441, math.MaxInt} {
		r.OutLen(n)
	}
	if after := fmt.Sprint(r.lastSample, r.sampFracNum, r.magicSamples, r.filtLen, len(r.mem)); after != before {
		t.Errorf("OutLen changed the state: %s -> %s", before, after)
	}
}
