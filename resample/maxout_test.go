package resample

import (
	"fmt"
	"math"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

// maxOutSignal is the input every MaxOut test draws from.
var maxOutSignal = testutil.WhiteNoise(256*1024, 32767, testutil.Seed("maxout"))

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

// maxOutChecker runs process calls sized by MaxOut and checks the
// properties of docs/acceptance/resample.md, change 9.
type maxOutChecker struct {
	t         testing.TB
	r         *Resampler
	pos       int
	slack     int // worst MaxOut minus frames written, where tightness applies
	zeroSlack int // worst slack in the zero-output state
}

// call offers n frames per channel through method via (see processWith)
// with an output of exactly MaxOut(n) frames per channel.
func (c *maxOutChecker) call(n, via int) {
	t, r := c.t, c.r
	t.Helper()
	nch := r.Channels()
	if (c.pos+n)*nch > len(maxOutSignal) {
		c.pos = 0
	}
	in := maxOutSignal[c.pos*nch : (c.pos+n)*nch]
	m := r.MaxOut(n)
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
		t.Fatalf("method %d: offered %d frames and MaxOut %d output frames, consumed %d", via, n, m, read)
	}
	if wrote > m {
		t.Fatalf("method %d: wrote %d frames, MaxOut %d", via, wrote, m)
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
		t.Fatalf("method %d: offered %d frames, MaxOut %d, wrote %d: slack %d, want %d", via, n, m, wrote, m-wrote, want)
	}
	if tight {
		c.slack = max(c.slack, m-wrote)
	} else if zero {
		c.zeroSlack = max(c.zeroSlack, m-wrote)
	}
	c.pos += n
}

var maxOutSizes = []int{0, 1, 2, 3, 7, 80, 160, 161, 331, 441, 480, 960, 1160}

// TestMaxOutMatrix checks soundness and tightness over every quality and
// rate pair of the fixture matrix, the fractional ratios, and stereo.
func TestMaxOutMatrix(t *testing.T) {
	rates := []int{8000, 16000, 24000, 32000, 44100, 48000, 96000}
	var cfgs []Config
	for q := 0; q <= 10; q++ {
		for _, a := range rates {
			for _, b := range rates {
				cfgs = append(cfgs, Config{Channels: 1, InRate: a, OutRate: b, Quality: q})
			}
		}
		for _, f := range [][2]int{{48000, 47999}, {47999, 48000}, {44101, 44100}, {1000003, 999983}, {7, 5},
			{5, 7}, {1, 100}, {100, 1}, {2147483646, 2147483647}, {1000000007, 3000000019}} {
			if f[1] > math.MaxInt32 && math.MaxInt == math.MaxInt32 {
				continue
			}
			cfgs = append(cfgs, Config{Channels: 1, RatioNum: f[0], RatioDen: f[1], InRate: 1, OutRate: 1, Quality: q})
		}
		cfgs = append(cfgs, Config{Channels: 2, InRate: 44100, OutRate: 48000, Quality: q},
			Config{Channels: 3, InRate: 48000, OutRate: 16000, Quality: q})
	}
	worst := 0
	for i, cfg := range cfgs {
		c := &maxOutChecker{t: t, r: mustNew(t, cfg)}
		rng := testutil.SplitMix64{State: uint64(i)}
		for k := range 40 {
			c.call(maxOutSizes[rng.Intn(len(maxOutSizes))], k%viaCount)
		}
		worst = max(worst, c.slack)
	}
	t.Logf("%d configurations, worst slack %d", len(cfgs), worst)
}

// TestMaxOutEvents covers magic samples after filter changes, Reset,
// SkipZeros and the zero-output state after rejected changes.
func TestMaxOutEvents(t *testing.T) {
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
			c := &maxOutChecker{t: t, r: r}
			for range 60 {
				if rng.Intn(4) == 0 {
					e := events[rng.Intn(len(events))]
					e.f(r)
				}
				c.call(maxOutSizes[rng.Intn(len(maxOutSizes))], rng.Intn(viaCount))
			}
			worstSlack = max(worstSlack, c.slack)
			worstZero = max(worstZero, c.zeroSlack)
		}
	}
	t.Logf("worst slack %d; in the zero-output state %d", worstSlack, worstZero)
}

// TestMaxOutMagicPending checks a case where pending magic samples alone
// produce output: with no input offered, MaxOut is still positive.
func TestMaxOutMagicPending(t *testing.T) {
	r := mustNew(t, Config{Channels: 1, InRate: 8000, OutRate: 48000, Quality: 10})
	c := &maxOutChecker{t: t, r: r}
	c.call(800, viaProcess)
	r.SetQuality(0)
	if r.magicSamples[0] == 0 {
		t.Fatal("no magic samples pending")
	}
	if m := r.MaxOut(0); m == 0 {
		t.Fatalf("MaxOut(0) = 0 with %d magic samples pending", r.magicSamples[0])
	}
	c.call(0, viaProcess)
	c.call(160, viaProcessFloat)
}

func TestMaxOutEdges(t *testing.T) {
	r := mustNew(t, Config{Channels: 2, InRate: 8000, OutRate: 16000, Quality: 3})
	for _, n := range []int{-1, -1000, math.MinInt} {
		if m := r.MaxOut(n); m != 0 {
			t.Errorf("MaxOut(%d) = %d, want 0", n, m)
		}
	}
	if m := r.MaxOut(0); m != 0 {
		t.Errorf("MaxOut(0) = %d, want 0", m)
	}
	if m := r.MaxOut(math.MaxInt); m != maxFrames {
		t.Errorf("MaxOut(MaxInt) = %d, want %d (clamped)", m, maxFrames)
	}
	if math.MaxInt > math.MaxUint32 {
		big := int(uint64(4294967294))
		w := mustNew(t, Config{Channels: 1, RatioNum: big, RatioDen: big + 1, Quality: 0})
		if m := w.MaxOut(1); m != maxFrames {
			t.Errorf("wrapping ratio: MaxOut(1) = %d, want %d", m, maxFrames)
		}
	}
	before := fmt.Sprint(r.lastSample, r.sampFracNum, r.magicSamples)
	r.MaxOut(500)
	if after := fmt.Sprint(r.lastSample, r.sampFracNum, r.magicSamples); after != before {
		t.Errorf("MaxOut changed the state: %s -> %s", before, after)
	}
}

func TestNoAllocsMaxOut(t *testing.T) {
	r := mustNew(t, Config{Channels: 3, InRate: 44100, OutRate: 48000, Quality: 10})
	testutil.AssertNoAllocs(t, 100, func() { _ = r.MaxOut(441) })
}

// FuzzMaxOut draws configurations, events and chunk sequences and checks
// soundness on every call, and tightness for mono streams outside the
// zero-output state.
func FuzzMaxOut(f *testing.F) {
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
		c := &maxOutChecker{t: t, r: r}
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

// TestMaxOutReadme runs the README example.
func TestMaxOutReadme(t *testing.T) {
	const inRate, outRate, frameMs = 8000, 16000, 20

	r, err := New(Config{
		Channels: 1, InRate: inRate, OutRate: outRate, Quality: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	in := make([]int16, inRate*frameMs/1000) // one 20 ms frame
	out := make([]int16, r.MaxOut(len(in)))  // always large enough for one call
	read, written, err := r.Process(in, out)
	if err != nil || read != len(in) || written != len(out)-1 {
		t.Errorf("read %d of %d, wrote %d into %d, %v", read, len(in), written, len(out), err)
	}
}

// TestMaxOutDivergedChannels checks the bound when the channels' states
// differ, after per-channel calls of different lengths: MaxOut must cover
// the channel that can write most.
func TestMaxOutDivergedChannels(t *testing.T) {
	for _, cfg := range []Config{
		{Channels: 2, InRate: 48000, OutRate: 44100, Quality: 5},
		{Channels: 3, InRate: 44100, OutRate: 48000, Quality: 10},
	} {
		for _, ahead := range []int{0, cfg.Channels - 1} {
			for _, k := range []int{1, 3, 5} {
				for via := range viaCount {
					r := mustNew(t, cfg)
					// Advance one channel by k input frames only.
					r.ProcessChannel(ahead, maxOutSignal[:k], 1, make([]int16, 100), 1)
					if channelsInSync(r) {
						t.Fatal("channels still in sync")
					}
					c := &maxOutChecker{t: t, r: r}
					for _, n := range []int{1, 2, 7, 160, 441} {
						c.call(n, via)
					}
				}
			}
		}
	}
}
