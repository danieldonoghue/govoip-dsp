// Derived from SpeexDSP 1.2.1 (https://github.com/xiph/speexdsp, commit
// 1b28a0f61bc31162979e1f26f3981fc3637095c8): libspeexdsp/filterbank.c.
//
// Copyright (C) 2006 Jean-Marc Valin
//
// Use of this source is governed by the BSD-3-Clause licence in
// LICENSES/SpeexDSP-COPYING. Changes: ported to Go, FIXED_POINT only.

package preprocess

import "github.com/danieldonoghue/govoip-dsp/internal/fixed"

// filterbank is FilterBank: each linear bin i feeds band left[i] with
// weight filterLeft[i] and band right[i] with weight filterRight[i].
type filterbank struct {
	left, right             []int
	filterLeft, filterRight []int16
	banks                   int
}

// toBark is the FIXED_POINT toBARK (filterbank.c:45).
func toBark(n int16) int32 {
	return fixed.Mult16x16(26829, fixed.Atan(fixed.Shr32(fixed.Mult16x16(97, n), 2))) +
		fixed.Mult16x16(4588, fixed.Atan(fixed.Mult16x32Q15(20, fixed.Mult16x16(n, n)))) +
		fixed.Mult16x16(3355, n)
}

// bankDivisor is the divisor of filterbank.c:96 for a sampling rate, which
// must be nonzero for filterbank_new to be defined.
func bankDivisor(sampling int32, banks int) int16 {
	melInterval := fixed.PDiv32(toBark(fixed.Extract16(sampling/2)), int32(banks-1))
	return fixed.Extract16(fixed.PShr32(melInterval, 15))
}

// newFilterbank is filterbank_new (filterbank.c:54) with type 1, for len 1 to
// 2048 and sampling 4 to 65535, where bankDivisor is nonzero.
func newFilterbank(banks int, sampling int32, n int) *filterbank {
	b := &filterbank{
		left:        make([]int, n),
		right:       make([]int, n),
		filterLeft:  make([]int16, n),
		filterRight: make([]int16, n),
		banks:       banks,
	}
	df := fixed.Div32(fixed.Shl32(sampling, 15), fixed.Mult16x16(2, int16(n)))
	maxMel := toBark(fixed.Extract16(sampling / 2))
	melInterval := fixed.PDiv32(maxMel, int32(banks-1))
	div := bankDivisor(sampling, banks)
	for i := range n {
		currFreq := fixed.Extract16(fixed.Mult16x32P15(int16(i), df))
		mel := toBark(currFreq)
		if mel > maxMel {
			break
		}
		id1 := int(fixed.Div32(mel, melInterval))
		var val int16
		if id1 > banks-2 {
			id1 = banks - 2
			val = fixed.Q15One
		} else {
			val = fixed.Div32x16(mel-int32(id1)*melInterval, div)
		}
		b.left[i] = id1
		b.filterLeft[i] = int16(fixed.Sub16(fixed.Q15One, val))
		b.right[i] = id1 + 1
		b.filterRight[i] = val
	}
	return b
}

// computeBank32 is filterbank_compute_bank32: mel[:banks] from ps[:len].
func (b *filterbank) computeBank32(ps, mel []int32) {
	mel = mel[:b.banks]
	clear(mel)
	ps = ps[:len(b.left)]
	for i, v := range ps {
		mel[b.left[i]] += fixed.Mult16x32P15(b.filterLeft[i], v)
		mel[b.right[i]] += fixed.Mult16x32P15(b.filterRight[i], v)
	}
}

// computePSD16 is filterbank_compute_psd16: ps[:len] from mel[:banks].
func (b *filterbank) computePSD16(mel, ps []int16) {
	ps = ps[:len(b.left)]
	for i := range ps {
		tmp := fixed.Mult16x16(mel[b.left[i]], b.filterLeft[i]) +
			fixed.Mult16x16(mel[b.right[i]], b.filterRight[i])
		ps[i] = fixed.Extract16(fixed.PShr32(tmp, 15))
	}
}
