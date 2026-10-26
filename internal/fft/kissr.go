// Derived from SpeexDSP 1.2.1 (https://github.com/xiph/speexdsp, commit
// 1b28a0f61bc31162979e1f26f3981fc3637095c8): libspeexdsp/kiss_fftr.c.
//
// Copyright (c) 2003-2004, Mark Borgerding
//
// Use of this source is governed by the BSD-3-Clause licence in
// LICENSES/kissfft-COPYING. Changes: ported to Go, FIXED_POINT branch only;
// only kiss_fftr_alloc, kiss_fftr2 and kiss_fftri2, which fftwrap.c calls.

package fft

import "github.com/danieldonoghue/govoip-dsp/internal/fixed"

// realState is struct kiss_fftr_state (kiss_fftr.c:23), plus scratch for the
// complex input or output that C reaches by casting the real buffer.
type realState struct {
	sub           kissState
	tmpbuf        []cpx
	superTwiddles []cpx
	work          []cpx
}

// init is kiss_fftr_alloc (kiss_fftr.c:32) for an even nfft of at least 2.
func (st *realState) init(nfft int, inverse bool) {
	nfft >>= 1
	st.sub.init(nfft, inverse)
	st.tmpbuf = make([]cpx, nfft)
	st.work = make([]cpx, nfft)
	st.superTwiddles = make([]cpx, nfft)
	for i := range st.superTwiddles {
		phase := int32(i + nfft>>1)
		if !inverse {
			phase = -phase
		}
		st.superTwiddles[i] = kfCexp2(fixed.Div32(fixed.Shl32(phase, 16), int32(nfft)))
	}
}

// forward is kiss_fftr2 (kiss_fftr.c:173): real input, half-complex output
// (DC, then real and imaginary parts of bins 1 to n/2-1, then Nyquist).
func (st *realState) forward(timedata, freqdata []int16) {
	ncfft := st.sub.nfft
	for k := range st.work {
		st.work[k] = cpx{timedata[2*k], timedata[2*k+1]}
	}
	st.sub.transform(st.work, st.tmpbuf)

	tdc := st.tmpbuf[0]
	cFixDiv(&tdc, 2)
	freqdata[0] = tdc.r + tdc.i
	freqdata[2*ncfft-1] = tdc.r - tdc.i

	for k := 1; k <= ncfft/2; k++ {
		a, b := st.tmpbuf[k], st.tmpbuf[ncfft-k]
		var f2k cpx
		f2k.r = int16(fixed.Shr32(fixed.Sub32(fixed.Extend32(a.r), fixed.Extend32(b.r)), 1))
		f2k.i = int16(fixed.PShr32(fixed.Add32(fixed.Extend32(a.i), fixed.Extend32(b.i)), 1))

		f1kr := fixed.Shl32(fixed.Add32(fixed.Extend32(a.r), fixed.Extend32(b.r)), 13)
		f1ki := fixed.Shl32(fixed.Sub32(fixed.Extend32(a.i), fixed.Extend32(b.i)), 13)

		w := st.superTwiddles[k]
		twr := fixed.Shr32(fixed.Sub32(fixed.Mult16x16(f2k.r, w.r), fixed.Mult16x16(f2k.i, w.i)), 1)
		twi := fixed.Shr32(fixed.Add32(fixed.Mult16x16(f2k.i, w.r), fixed.Mult16x16(f2k.r, w.i)), 1)

		// For k == ncfft-k the last two stores overwrite the first two, as in C.
		freqdata[2*k-1] = int16(fixed.PShr32(f1kr+twr, 15))
		freqdata[2*k] = int16(fixed.PShr32(f1ki+twi, 15))
		freqdata[2*(ncfft-k)-1] = int16(fixed.PShr32(f1kr-twr, 15))
		freqdata[2*(ncfft-k)] = int16(fixed.PShr32(twi-f1ki, 15))
	}
}

// inverse is kiss_fftri2 (kiss_fftr.c:261), the inverse of forward's layout.
func (st *realState) inverse(freqdata, timedata []int16) {
	ncfft := st.sub.nfft

	st.tmpbuf[0].r = freqdata[0] + freqdata[2*ncfft-1]
	st.tmpbuf[0].i = freqdata[0] - freqdata[2*ncfft-1]

	for k := 1; k <= ncfft/2; k++ {
		fk := cpx{freqdata[2*k-1], freqdata[2*k]}
		fnkc := cpx{freqdata[2*(ncfft-k)-1], -freqdata[2*(ncfft-k)]}

		fek := cAdd(fk, fnkc)
		tmp := cSub(fk, fnkc)
		fok := cMul(tmp, st.superTwiddles[k])
		st.tmpbuf[k] = cAdd(fek, fok)
		st.tmpbuf[ncfft-k] = cSub(fek, fok)
		st.tmpbuf[ncfft-k].i *= -1
	}
	st.sub.transform(st.tmpbuf, st.work)
	for k, c := range st.work {
		timedata[2*k] = c.r
		timedata[2*k+1] = c.i
	}
}
