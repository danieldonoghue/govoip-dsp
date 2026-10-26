package preprocess

import (
	"math"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/fixed"
	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

// A reachable zero divisor (reference/drivers/preprocess_zero.c): after
// silence, a band echo sum wrapped to -1 makes the gain floor divide by 0.
const (
	zeroRate, zeroN = 16000, 160
	zeroEchoFrame   = 300
	zeroBand        = 18
	zeroFrames      = zeroEchoFrame + 51
)

func zeroInput() []int16 {
	x := make([]int16, zeroFrames*zeroN)
	copy(x[(zeroEchoFrame+1)*zeroN:], signal("speech_noise", zeroRate, (zeroFrames-zeroEchoFrame-1)*zeroN))
	return x
}

// zeroResidual sets some bins feeding zeroBand to MaxInt32 and solves one
// more so that the band's filterbank sum wraps to exactly -1.
func zeroResidual(t testing.TB) []int32 {
	b := newFilterbank(nbBands, zeroRate, zeroN)
	weight := func(i int) int16 {
		switch zeroBand {
		case b.left[i]:
			return b.filterLeft[i]
		case b.right[i]:
			return b.filterRight[i]
		}
		return 0
	}
	var bins []int
	for i := range zeroN {
		if weight(i) > 0 {
			bins = append(bins, i)
		}
	}
	for k := 1; k < len(bins); k++ {
		var sum int32
		for _, i := range bins[:k] {
			sum += fixed.Mult16x32P15(weight(i), math.MaxInt32)
		}
		target := -1 - sum // what one more bin must add, as int32 arithmetic wraps
		for _, i := range bins[k:] {
			w := weight(i)
			if target < 0 || fixed.Mult16x32P15(w, math.MaxInt32) < target {
				continue
			}
			lo, hi := int32(0), int32(math.MaxInt32)
			for lo < hi { // nondecreasing in v for w > 0
				mid := lo + (hi-lo)/2
				if fixed.Mult16x32P15(w, mid) < target {
					lo = mid + 1
				} else {
					hi = mid
				}
			}
			if fixed.Mult16x32P15(w, lo) != target {
				continue
			}
			r := make([]int32, zeroN)
			for _, j := range bins[:k] {
				r[j] = math.MaxInt32
			}
			r[i] = lo
			return r
		}
	}
	t.Fatalf("no residual wraps band %d to -1", zeroBand)
	return nil
}

func TestReferenceZeroDivisor(t *testing.T) {
	m := testutil.LoadManifest(t, "../testdata/speexdsp/preprocess_zero")
	c := m.Case(t, "zero_divisor_16000_160")
	res := zeroResidual(t)
	kres := stream[int32](t, m, c, "residual", false)
	putAll(kres, res)
	kres.Done()
	in, out := stream[int16](t, m, c, "in", false), stream[int16](t, m, c, "out", false)
	vad, prob := stream[int16](t, m, c, "vad", false), stream[int16](t, m, c, "prob", false)
	band := stream[int32](t, m, c, "band", false)
	x := zeroInput()
	p := mustNew(t, zeroRate, zeroN)
	p.SetVAD(true)
	for f := range zeroFrames {
		frame := x[f*zeroN : (f+1)*zeroN]
		putAll(in, frame)
		var v bool
		var err error
		if f == zeroEchoFrame {
			v, err = p.RunEcho(frame, res)
			nz, en := p.noise[zeroN+zeroBand], p.echoNoise[zeroN+zeroBand]
			den := 1 + fixed.PShr32(nz, noiseShift) + en
			if den != 0 {
				t.Errorf("gain-floor divisor of band %d is %d, want 0 (noise %d, echo %d)", zeroBand, den, nz, en)
			}
			putAll(band, []int32{nz, en, den, int32(p.gainFloor[zeroN+zeroBand])})
		} else {
			v, err = p.Run(frame)
		}
		if err != nil {
			t.Fatal(err)
		}
		putAll(out, frame)
		vad.Put(int16(b2i(v)))
		prob.Put(p.speechProb)
	}
	for _, k := range []checker[int16]{in, out, vad, prob} {
		k.Done()
	}
	band.Done()
}
