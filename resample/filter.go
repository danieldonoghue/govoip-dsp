// Derived from SpeexDSP 1.2.1 (https://github.com/xiph/speexdsp, commit
// 1b28a0f61bc31162979e1f26f3981fc3637095c8): libspeexdsp/resample.c.
//
// Copyright (C) 2007-2008 Jean-Marc Valin
// Copyright (C) 2008      Thorvald Natvig
//
// Use of this source is governed by the BSD-3-Clause licence in
// LICENSES/SpeexDSP-COPYING. Changes: ported to Go.

package resample

import (
	"math"

	"github.com/danieldonoghue/govoip-dsp/internal/fixed"
)

// intMaxHalf is INT_MAX/sizeof(spx_word16_t), the bound of the size checks in
// update_filter (resample.c:643-713).
const intMaxHalf = math.MaxInt32 / 2

var kaiser12Table = [68]float64{
	0.99859849, 1.00000000, 0.99859849, 0.99440475, 0.98745105, 0.97779076,
	0.96549770, 0.95066529, 0.93340547, 0.91384741, 0.89213598, 0.86843014,
	0.84290116, 0.81573067, 0.78710866, 0.75723148, 0.72629970, 0.69451601,
	0.66208321, 0.62920216, 0.59606986, 0.56287762, 0.52980938, 0.49704014,
	0.46473455, 0.43304576, 0.40211431, 0.37206735, 0.34301800, 0.31506490,
	0.28829195, 0.26276832, 0.23854851, 0.21567274, 0.19416736, 0.17404546,
	0.15530766, 0.13794294, 0.12192957, 0.10723616, 0.09382272, 0.08164178,
	0.07063950, 0.06075685, 0.05193064, 0.04409466, 0.03718069, 0.03111947,
	0.02584161, 0.02127838, 0.01736250, 0.01402878, 0.01121463, 0.00886058,
	0.00691064, 0.00531256, 0.00401805, 0.00298291, 0.00216702, 0.00153438,
	0.00105297, 0.00069463, 0.00043489, 0.00025272, 0.00013031, 0.0000527734,
	0.00001000, 0.00000000}

var kaiser10Table = [36]float64{
	0.99537781, 1.00000000, 0.99537781, 0.98162644, 0.95908712, 0.92831446,
	0.89005583, 0.84522401, 0.79486424, 0.74011713, 0.68217934, 0.62226347,
	0.56155915, 0.50119680, 0.44221549, 0.38553619, 0.33194107, 0.28205962,
	0.23636152, 0.19515633, 0.15859932, 0.12670280, 0.09935205, 0.07632451,
	0.05731132, 0.04193980, 0.02979584, 0.02044510, 0.01345224, 0.00839739,
	0.00488951, 0.00257636, 0.00115101, 0.00035515, 0.00000000, 0.00000000}

var kaiser8Table = [36]float64{
	0.99635258, 1.00000000, 0.99635258, 0.98548012, 0.96759014, 0.94302200,
	0.91223751, 0.87580811, 0.83439927, 0.78875245, 0.73966538, 0.68797126,
	0.63451750, 0.58014482, 0.52566725, 0.47185369, 0.41941150, 0.36897272,
	0.32108304, 0.27619388, 0.23465776, 0.19672670, 0.16255380, 0.13219758,
	0.10562887, 0.08273982, 0.06335451, 0.04724088, 0.03412321, 0.02369490,
	0.01563093, 0.00959968, 0.00527363, 0.00233883, 0.00050000, 0.00000000}

var kaiser6Table = [36]float64{
	0.99733006, 1.00000000, 0.99733006, 0.98935595, 0.97618418, 0.95799003,
	0.93501423, 0.90755855, 0.87598009, 0.84068475, 0.80211977, 0.76076565,
	0.71712752, 0.67172623, 0.62508937, 0.57774224, 0.53019925, 0.48295561,
	0.43647969, 0.39120616, 0.34752997, 0.30580127, 0.26632152, 0.22934058,
	0.19505503, 0.16360756, 0.13508755, 0.10953262, 0.08693120, 0.06722600,
	0.05031820, 0.03607231, 0.02432151, 0.01487334, 0.00752000, 0.00000000}

type funcDef struct {
	table      []float64
	oversample int32
}

var (
	kaiser12 = &funcDef{kaiser12Table[:], 64}
	kaiser10 = &funcDef{kaiser10Table[:], 32}
	kaiser8  = &funcDef{kaiser8Table[:], 32}
	kaiser6  = &funcDef{kaiser6Table[:], 32}
)

type qualityMapping struct {
	baseLength          uint32
	oversample          uint32
	downsampleBandwidth float32
	upsampleBandwidth   float32
	windowFunc          *funcDef
}

var qualityMap = [11]qualityMapping{
	{8, 4, 0.830, 0.860, kaiser6},
	{16, 4, 0.850, 0.880, kaiser6},
	{32, 4, 0.882, 0.910, kaiser6},
	{48, 8, 0.895, 0.917, kaiser8},
	{64, 8, 0.921, 0.940, kaiser8},
	{80, 16, 0.922, 0.940, kaiser10},
	{96, 16, 0.940, 0.945, kaiser10},
	{128, 16, 0.950, 0.950, kaiser10},
	{160, 16, 0.960, 0.960, kaiser10},
	{192, 32, 0.968, 0.968, kaiser12},
	{256, 32, 0.975, 0.975, kaiser12},
}

// computeFunc is compute_func (resample.c:240). Every product is converted
// explicitly so that Go cannot fuse it into a multiply-add.
func computeFunc(x float32, f *funcDef) float64 {
	y := float32(x * float32(f.oversample))
	ind := int(math.Floor(float64(y)))
	frac := float32(y - float32(ind))
	f2 := float32(frac * frac)
	f3 := float32(f2 * frac)
	var interp [4]float64
	interp[3] = float64(-0.1666666667*float64(frac)) + float64(0.1666666667*float64(f3))
	interp[2] = float64(float64(frac)+float64(0.5*float64(f2))) - float64(0.5*float64(f3))
	interp[0] = float64(float64(-0.3333333333*float64(frac))+float64(0.5*float64(f2))) - float64(0.1666666667*float64(f3))
	interp[1] = 1.0 - interp[3] - interp[2] - interp[0]
	t := f.table
	return float64(float64(float64(interp[0]*t[ind])+float64(interp[1]*t[ind+1]))+float64(interp[2]*t[ind+2])) +
		float64(interp[3]*t[ind+3])
}

// sinc is the FIXED_POINT sinc (resample.c:275). math.Sin stands in for libm
// sin; TestReferenceTables compares every table entry with the harness.
func sinc(cutoff, x float32, n int32, w *funcDef) int16 {
	xx := float32(x * cutoff)
	ax := math.Abs(float64(x))
	if ax < float64(float32(1e-6)) {
		return fixed.Word2IntFloat64(float64(32768. * float64(cutoff)))
	}
	if ax > float64(float32(0.5*float32(n))) {
		return 0
	}
	pxx := float64(math.Pi * float64(xx))
	v := float64(float64(32768.*float64(cutoff))*math.Sin(pxx)) / pxx
	return fixed.Word2IntFloat64(float64(v * computeFunc(float32(math.Abs(float64(2.*float64(x))/float64(n))), w)))
}

var (
	qM16667 = fixed.QConst16(float64(float32(-0.16667)), 15)
	q16667  = fixed.QConst16(float64(float32(0.16667)), 15)
	qM33333 = fixed.QConst16(float64(float32(-0.33333)), 15)
	qHalf   = fixed.QConst16(float64(float32(0.5)), 15)
)

// cubicCoef is the FIXED_POINT cubic_coef (resample.c:302).
func cubicCoef(x int16) (i0, i1, i2, i3 int16) {
	x2 := int16(fixed.Mult16x16P15(x, x))
	x3 := int16(fixed.Mult16x16P15(x, x2))
	i0 = int16(fixed.PShr32(fixed.Mult16x16(qM16667, x)+fixed.Mult16x16(q16667, x3), 15))
	i1 = fixed.Extract16(fixed.Extend32(x) + fixed.Shr32(fixed.Sub32(fixed.Extend32(x2), fixed.Extend32(x3)), 1))
	i3 = int16(fixed.PShr32(fixed.Mult16x16(qM33333, x)+fixed.Mult16x16(qHalf, x2)-fixed.Mult16x16(q16667, x3), 15))
	i2 = int16(fixed.Q15One - int32(i0) - int32(i1) - int32(i3))
	if i2 < 32767 {
		i2++
	}
	return
}

// multiplyFrac is multiply_frac (resample.c:593): value*num/den in uint32
// without overflow, or false where C returns RESAMPLER_ERR_OVERFLOW.
func multiplyFrac(value, num, den uint32) (uint32, bool) {
	major := value / den
	remain := value % den
	if remain > math.MaxUint32/num || major > math.MaxUint32/num ||
		major*num > math.MaxUint32-remain*num/den {
		return 0, false
	}
	return remain*num/den + major*num, true
}

// filterPlan is the filter that update_filter builds for a quality and a
// reduced ratio.
type filterPlan struct {
	filtLen, oversample, tableLen uint32
	cutoff                        float32
	direct                        bool
}

// planFilter is the sizing part of update_filter (resample.c:613-658); false
// is C's fail label, and the plan then holds C's cutoff and oversampling.
func planFilter(quality int, num, den uint32) (filterPlan, bool) {
	qm := &qualityMap[quality]
	p := filterPlan{filtLen: qm.baseLength, oversample: qm.oversample, cutoff: qm.upsampleBandwidth}
	if num > den {
		p.cutoff = float32(float32(qm.downsampleBandwidth*float32(den)) / float32(num))
		fl, ok := multiplyFrac(p.filtLen, num, den)
		if !ok {
			return p, false
		}
		p.filtLen = ((fl - 1) &^ 7) + 8
		// The products wrap in uint32 as in C.
		if 2*den < num {
			p.oversample >>= 1
		}
		if 4*den < num {
			p.oversample >>= 1
		}
		if 8*den < num {
			p.oversample >>= 1
		}
		if 16*den < num {
			p.oversample >>= 1
		}
		if p.oversample < 1 {
			p.oversample = 1
		}
	}
	p.direct = p.filtLen*den <= p.filtLen*p.oversample+8 &&
		uint64(intMaxHalf)/uint64(den) >= uint64(p.filtLen)
	if p.direct {
		p.tableLen = p.filtLen * den
	} else {
		if (uint64(intMaxHalf)-8)/uint64(p.oversample) < uint64(p.filtLen) {
			return p, false
		}
		p.tableLen = p.filtLen*p.oversample + 8
	}
	return p, true
}

// maxEntries and maxChannels are the Go-only resource cap
// (docs/acceptance/resample.md, change 5).
const (
	maxEntries  = 16 * 1024 * 1024
	maxChannels = 256
)

// checkCap returns ErrAllocFailed above maxEntries entries or when the length
// wraps to 0, and nil where C's own limits reject, so C's failure path runs.
func (r *Resampler) checkCap(quality int, num, den uint32) error {
	p, ok := planFilter(quality, num, den)
	if !ok {
		return nil
	}
	if p.filtLen == 0 {
		return ErrAllocFailed
	}
	need := uint64(p.tableLen) + uint64(r.nbChannels)*uint64(p.filtLen-1+r.bufferSize)
	if need > maxEntries {
		return ErrAllocFailed
	}
	return nil
}

// updateFilter is update_filter (resample.c:605). It allocates when the
// sinc table or the memory must grow.
func (r *Resampler) updateFilter() error {
	oldLength := r.filtLen
	oldAllocSize := r.memAllocSize

	r.intAdvance = int32(r.numRate / r.denRate)
	r.fracAdvance = int32(r.numRate % r.denRate)
	p, ok := planFilter(r.quality, r.numRate, r.denRate)
	r.oversample, r.cutoff = p.oversample, p.cutoff
	if !ok {
		return r.filterFailed(oldLength)
	}
	r.filtLen = p.filtLen
	useDirect := p.direct
	minSincTableLength := p.tableLen
	if uint32(len(r.sincTable)) < minSincTableLength {
		t := make([]int16, minSincTableLength)
		copy(t, r.sincTable)
		r.sincTable = t
	}
	w := qualityMap[r.quality].windowFunc
	n := int32(r.filtLen)
	if useDirect {
		for i := uint32(0); i < r.denRate; i++ {
			fi := float32(float32(i) / float32(r.denRate))
			for j := int32(0); j < n; j++ {
				x := float32(j-n/2+1) - fi
				r.sincTable[i*r.filtLen+uint32(j)] = sinc(r.cutoff, x, n, w)
			}
		}
		r.kernel = kernelDirect
	} else {
		end := int32(r.oversample*r.filtLen + 4)
		half := float32(r.filtLen / 2)
		for i := int32(-4); i < end; i++ {
			x := float32(float32(i)/float32(r.oversample)) - half
			r.sincTable[i+4] = sinc(r.cutoff, x, n, w)
		}
		r.kernel = kernelInterpolate
	}

	minAllocSize := r.filtLen - 1 + r.bufferSize
	if minAllocSize > r.memAllocSize {
		if uint64(intMaxHalf)/uint64(r.nbChannels) < uint64(minAllocSize) {
			return r.filterFailed(oldLength)
		}
		m := make([]int16, int(r.nbChannels)*int(minAllocSize))
		copy(m, r.mem)
		r.mem = m
		r.memAllocSize = minAllocSize
	}
	r.adjustMemory(oldLength, oldAllocSize)
	return nil
}

// adjustMemory is the memory update at the end of update_filter
// (resample.c:721-782), which keeps the history across a filter change.
func (r *Resampler) adjustMemory(oldLength, oldAllocSize uint32) {
	mem := r.mem
	alloc := int(r.memAllocSize)
	filtLen := r.filtLen
	switch {
	case !r.started:
		clear(mem)
	case filtLen > oldLength:
		for i := int(r.nbChannels) - 1; i >= 0; i-- {
			base, oldBase := i*alloc, i*int(oldAllocSize)
			magic := r.magicSamples[i]
			olen := oldLength + 2*magic
			for j := oldLength - 1 + magic; j != 0; {
				j--
				mem[base+int(j+magic)] = mem[oldBase+int(j)]
			}
			for j := uint32(0); j < magic; j++ {
				mem[base+int(j)] = 0
			}
			r.magicSamples[i] = 0
			if filtLen > olen {
				var j uint32
				for j = 0; j < olen-1; j++ {
					mem[base+int(filtLen-2-j)] = mem[base+int(olen-2-j)]
				}
				for ; j < filtLen-1; j++ {
					mem[base+int(filtLen-2-j)] = 0
				}
				// Saturates where C would overflow, only after a failed change.
				r.lastSample[i] = int32(min(int64(r.lastSample[i])+int64((filtLen-olen)/2), math.MaxInt32))
			} else {
				m := (olen - filtLen) / 2
				r.magicSamples[i] = m
				for j := uint32(0); j < filtLen-1+m; j++ {
					mem[base+int(j)] = mem[base+int(j+m)]
				}
			}
		}
	case filtLen < oldLength:
		for i := 0; i < int(r.nbChannels); i++ {
			base := i * alloc
			oldMagic := r.magicSamples[i]
			m := (oldLength - filtLen) / 2
			r.magicSamples[i] = m
			for j := uint32(0); j < filtLen-1+m+oldMagic; j++ {
				mem[base+int(j)] = mem[base+int(j+m)]
			}
			r.magicSamples[i] += oldMagic
		}
	}
}

// filterFailed is the fail label of update_filter (resample.c:785).
func (r *Resampler) filterFailed(oldLength uint32) error {
	r.kernel = kernelZero
	r.filtLen = oldLength
	return ErrAllocFailed
}
