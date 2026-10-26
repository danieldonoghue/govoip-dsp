// Derived from SpeexDSP 1.2.1 (https://github.com/xiph/speexdsp, commit
// 1b28a0f61bc31162979e1f26f3981fc3637095c8): libspeexdsp/preprocess.c.
//
// Copyright (C) 2003 Epic Games (written by Jean-Marc Valin)
// Copyright (C) 2004-2006 Epic Games
//
// Use of this source is governed by the BSD-3-Clause licence in
// LICENSES/SpeexDSP-COPYING. Changes: ported to Go, FIXED_POINT only; a zero
// divisor at the sites docs/algorithms.md lists gives 0, as on arm64.

package preprocess

import "github.com/danieldonoghue/govoip-dsp/internal/fixed"

// Run processes one frame in place without echo residual (no SET_ECHO_STATE)
// and returns the VAD decision, always true while VAD is off.
func (p *Preprocessor) Run(frame []int16) (bool, error) {
	if p.fft == nil {
		return false, ErrConfig
	}
	if len(frame) != p.frameSize {
		return false, ErrBufferSize
	}
	return p.run(frame, nil), nil
}

// RunEcho is Run with SET_ECHO_STATE, using residual[:FrameSize()], the
// spectrum echo.Canceller.Residual gives for the same frame.
func (p *Preprocessor) RunEcho(frame []int16, residual []int32) (bool, error) {
	if p.fft == nil {
		return false, ErrConfig
	}
	if len(frame) != p.frameSize || len(residual) < p.frameSize {
		return false, ErrBufferSize
	}
	return p.run(frame, residual[:p.frameSize]), nil
}

// EstimateUpdate is speex_preprocess_estimate_update (preprocess.c:1009):
// it updates the noise estimate from frame, which it only reads.
func (p *Preprocessor) EstimateUpdate(frame []int16) error {
	if p.fft == nil {
		return ErrConfig
	}
	if len(frame) != p.frameSize {
		return ErrBufferSize
	}
	n := p.frameSize
	p.minCount++
	p.analysis(frame)
	p.updateNoiseProb()
	for i := 1; i < n-1; i++ {
		if !p.updateProb[i] || p.ps[i] < fixed.PShr32(p.noise[i], noiseShift) {
			p.noise[i] = fixed.Mult16x32Q15(c095, p.noise[i]) +
				fixed.Mult16x32Q15(c005, fixed.Shl32(p.ps[i], noiseShift))
		}
	}
	for i := range n {
		p.outbuf[i] = int16(fixed.Mult16x16Q15(frame[i], p.window[n+i]))
	}
	copy(p.oldPS, p.ps)
	for i := range n {
		p.reverbEst[i] = fixed.Mult16x32Q15(p.reverbDecay, p.reverbEst[i])
	}
	return nil
}

// analysis is preprocess_analysis (preprocess.c:614) with N3 = N, N4 = 0.
func (p *Preprocessor) analysis(x []int16) {
	n := p.frameSize
	copy(p.frame[:n], p.inbuf)
	copy(p.frame[n:], x)
	copy(p.inbuf, x)
	for i, v := range p.frame {
		p.frame[i] = int16(fixed.Mult16x16Q15(v, p.window[i]))
	}
	var maxVal int16
	for _, v := range p.frame {
		maxVal = int16(fixed.Max32(int32(maxVal), fixed.Abs16(v)))
	}
	p.frameShift = 14 - int(fixed.ILog2(uint32(fixed.Extend32(maxVal))))
	for i, v := range p.frame {
		p.frame[i] = int16(fixed.Shl16(v, uint(p.frameShift)))
	}
	// spx_fft rewrites st->frame; it is overwritten before its next read.
	_ = p.fft.Forward(p.frame, p.ft)

	ps, ft := p.ps, p.ft
	ps[0] = fixed.Mult16x16(ft[0], ft[0])
	for i := 1; i < n; i++ {
		ps[i] = fixed.Mult16x16(ft[2*i-1], ft[2*i-1]) + fixed.Mult16x16(ft[2*i], ft[2*i])
	}
	for i := range n {
		ps[i] = fixed.PShr32(ps[i], uint(2*p.frameShift))
	}
	p.bank.computeBank32(ps[:n], ps[n:])
}

// updateNoiseProb is update_noise_prob (preprocess.c:660).
func (p *Preprocessor) updateNoiseProb() {
	n := p.frameSize
	s, ps := p.s, p.ps
	for i := 1; i < n-1; i++ {
		s[i] = fixed.Mult16x32Q15(c080, s[i]) + fixed.Mult16x32Q15(c005, ps[i-1]) +
			fixed.Mult16x32Q15(c010, ps[i]) + fixed.Mult16x32Q15(c005, ps[i+1])
	}
	s[0] = fixed.Mult16x32Q15(c080, s[0]) + fixed.Mult16x32Q15(c020, ps[0])
	s[n-1] = fixed.Mult16x32Q15(c080, s[n-1]) + fixed.Mult16x32Q15(c020, ps[n-1])

	if p.nbAdapt == 1 {
		clear(p.sMin)
		clear(p.sTmp)
	}
	var minRange int
	switch {
	case p.nbAdapt < 100:
		minRange = 15
	case p.nbAdapt < 1000:
		minRange = 50
	case p.nbAdapt < 10000:
		minRange = 150
	default:
		minRange = 300
	}
	if p.minCount > minRange {
		p.minCount = 0
		for i := range n {
			p.sMin[i] = fixed.Min32(p.sTmp[i], s[i])
			p.sTmp[i] = s[i]
		}
	} else {
		for i := range n {
			p.sMin[i] = fixed.Min32(p.sMin[i], s[i])
			p.sTmp[i] = fixed.Min32(p.sTmp[i], s[i])
		}
	}
	for i := range n {
		p.updateProb[i] = fixed.Mult16x32Q15(c040, s[i]) > p.sMin[i]
	}
}

// div32x16Q8 is DIV32_16_Q8 (preprocess.c:92).
func div32x16Q8(a, b int32) int16 {
	if fixed.Shr32(a, 7) >= b {
		return 32767
	}
	a, b = scaleDivisor(a, b)
	return fixed.PDiv32x16OrZero(fixed.Shl32(a, 8), int16(b))
}

// div32x16Q15 is DIV32_16_Q15 (preprocess.c:118).
func div32x16Q15(a, b int32) int16 {
	if fixed.Shr32(a, 15) >= b {
		return 32767
	}
	a, b = scaleDivisor(a, b)
	return fixed.Div32x16OrZero(fixed.Shl32(a, 15)-a, int16(b))
}

// scaleDivisor is the shared range reduction of DIV32_16_Q8 and Q15.
func scaleDivisor(a, b int32) (int32, int32) {
	if b >= 1<<23 {
		a, b = fixed.Shr32(a, 8), fixed.Shr32(b, 8)
	}
	if b >= 1<<19 {
		a, b = fixed.Shr32(a, 4), fixed.Shr32(b, 4)
	}
	if b >= 1<<15 {
		a, b = fixed.Shr32(a, 4), fixed.Shr32(b, 4)
	}
	return a, b
}

// hgTable is the Q13 table of hypergeom_gain (preprocess.c:297).
var hgTable = [21]int16{
	6730, 8357, 9868, 11267, 12563, 13770, 14898,
	15959, 16961, 17911, 18816, 19682, 20512, 21311,
	22082, 22827, 23549, 24250, 24931, 25594, 26241}

// hypergeomGain is hypergeom_gain (preprocess.c:292), FIXED_POINT.
func hypergeomGain(xx int32) int32 {
	ind := int(fixed.Shr32(xx, 10))
	if ind < 0 {
		return fixed.Q15One
	}
	if ind > 19 {
		return fixed.Add32(fixed.Q15One,
			fixed.Extend32(fixed.Div32x16(hgTail, int16(fixed.Shr32(xx, expinShift-snrShift)))))
	}
	frac := int16(fixed.Shl32(xx-fixed.Shl32(int32(ind), 10), 5))
	num := fixed.PShr32(fixed.Mult16x16(int16(fixed.Q15One-int32(frac)), hgTable[ind])+
		fixed.Mult16x16(frac, hgTable[ind+1]), 7)
	return fixed.Shl32(fixed.Extend32(fixed.Div32x16(num, fixed.Sqrt(fixed.Shl32(xx, 15)+6711))), 7)
}

// qcurve is qcurve (preprocess.c:310), FIXED_POINT.
func qcurve(x int16) int16 {
	x = fixed.Max16(x, 1)
	d := fixed.Add16(512, int16(fixed.Mult16x16Q15(qc060, fixed.Div32x16(32767, x))))
	return fixed.Div32x16(fixed.Shl32(32767, 9), d)
}

// gainFromSuppress is EXTRACT16(MIN32(Q15_ONE,SHR32(spx_exp(MULT16_16(c,v)),1))).
func gainFromSuppress(c int16, v int32) int16 {
	e := fixed.Exp(int16(fixed.Mult16x16(c, int16(v))))
	return fixed.Extract16(fixed.Min32(fixed.Q15One, fixed.Shr32(e, 1)))
}

// computeGainFloor is compute_gain_floor (preprocess.c:317), FIXED_POINT,
// over the bands.
func (p *Preprocessor) computeGainFloor(noiseSuppress, effEcho int32, noise, echo []int32, gainFloor []int16) {
	if noiseSuppress > effEcho {
		noiseGain := gainFromSuppress(gfNoise, noiseSuppress)
		gainRatio := gainFromSuppress(gfRatio, effEcho-noiseSuppress)
		for i := range gainFloor {
			nz := fixed.PShr32(noise[i], noiseShift)
			d := div32x16Q15(nz+fixed.Mult16x32Q15(gainRatio, echo[i]), 1+nz+echo[i])
			gainFloor[i] = int16(fixed.Mult16x16Q15(noiseGain, fixed.Sqrt(fixed.Shl32(fixed.Extend32(d), 15))))
		}
		return
	}
	echoGain := gainFromSuppress(gfNoise, effEcho)
	gainRatio := gainFromSuppress(gfRatio, noiseSuppress-effEcho)
	for i := range gainFloor {
		nz := fixed.PShr32(noise[i], noiseShift)
		d := div32x16Q15(fixed.Mult16x32Q15(gainRatio, nz)+echo[i], 1+nz+echo[i])
		gainFloor[i] = int16(fixed.Mult16x16Q15(echoGain, fixed.Sqrt(fixed.Shl32(fixed.Extend32(d), 15))))
	}
}

// priorRatio is the Wiener gain prior/(1+prior) of preprocess.c:846 and :887.
func priorRatio(prior int16) int16 {
	return fixed.PDiv32x16OrZero(fixed.Shl32(fixed.Extend32(prior), 15), fixed.Add16(prior, 1<<snrShift))
}

// run is speex_preprocess_run (preprocess.c:722); residual is nil without
// an echo state.
func (p *Preprocessor) run(x []int16, residual []int32) bool {
	n := p.frameSize
	m := nbBands
	ps := p.ps

	p.nbAdapt++
	if p.nbAdapt > 20000 {
		p.nbAdapt = 20000
	}
	p.minCount++

	beta := fixed.Max16(betaMin, fixed.Div32x16(fixed.Q15One, int16(p.nbAdapt)))
	beta1 := int16(fixed.Q15One - int32(beta))

	if residual != nil {
		for i := range n {
			p.echoNoise[i] = fixed.Max32(fixed.Mult16x32Q15(echoDecay, p.echoNoise[i]), residual[i])
		}
		p.bank.computeBank32(p.echoNoise[:n], p.echoNoise[n:])
	} else {
		clear(p.echoNoise)
	}
	p.analysis(x)
	p.updateNoiseProb()

	for i := range n {
		if !p.updateProb[i] || ps[i] < fixed.PShr32(p.noise[i], noiseShift) {
			p.noise[i] = fixed.Max32(0, fixed.Mult16x32Q15(beta1, p.noise[i])+
				fixed.Mult16x32Q15(beta, fixed.Shl32(ps[i], noiseShift)))
		}
	}
	p.bank.computeBank32(p.noise[:n], p.noise[n:])

	if p.nbAdapt == 1 {
		copy(p.oldPS, ps)
	}

	for i := range n + m {
		// reverb_estimate is always zero; the term is kept as upstream adds it.
		totNoise := fixed.Add32(fixed.Add32(fixed.Add32(1, fixed.PShr32(p.noise[i], noiseShift)),
			p.echoNoise[i]), p.reverbEst[i])

		post := int16(fixed.Sub16(div32x16Q8(ps[i], totNoise), snrOne))
		post = fixed.Min16(post, snr100)
		p.post[i] = post

		d := div32x16Q15(p.oldPS[i], fixed.Add32(p.oldPS[i], totNoise))
		gamma := int16(c010 + fixed.Mult16x16Q15(c089, int16(fixed.Mult16x16Q15(d, d))))

		prior := fixed.Extract16(fixed.PShr32(fixed.Add32(
			fixed.Mult16x16(gamma, fixed.Max16(0, post)),
			fixed.Mult16x16(int16(fixed.Q15One-int32(gamma)), div32x16Q8(p.oldPS[i], totNoise))), 15))
		p.prior[i] = fixed.Min16(prior, snr100)
	}

	z := p.zeta
	z[0] = int16(fixed.PShr32(fixed.Add32(fixed.Mult16x16(c070, z[0]), fixed.Mult16x16(c030, p.prior[0])), 15))
	for i := 1; i < n-1; i++ {
		z[i] = int16(fixed.PShr32(fixed.Add32(fixed.Add32(fixed.Add32(
			fixed.Mult16x16(c070, z[i]), fixed.Mult16x16(c015, p.prior[i])),
			fixed.Mult16x16(c0075, p.prior[i-1])), fixed.Mult16x16(c0075, p.prior[i+1])), 15))
	}
	for i := n - 1; i < n+m; i++ {
		z[i] = int16(fixed.PShr32(fixed.Add32(fixed.Mult16x16(c070, z[i]), fixed.Mult16x16(c030, p.prior[i])), 15))
	}

	var zFrame int32
	for i := n; i < n+m; i++ {
		zFrame = fixed.Add32(zFrame, fixed.Extend32(z[i]))
	}
	pFrame := int16(c010 + fixed.Mult16x16Q15(c0899, qcurve(fixed.Div32x16(zFrame, int16(m)))))

	effEcho := fixed.Extract16(fixed.PShr32(fixed.Add32(
		fixed.Mult16x16(int16(fixed.Sub16(fixed.Q15One, pFrame)), int16(p.echoSuppress)),
		fixed.Mult16x16(pFrame, int16(p.echoSuppressActive))), 15))

	p.computeGainFloor(p.noiseSuppress, int32(effEcho), p.noise[n:], p.echoNoise[n:], p.gainFloor[n:])

	for i := n; i < n+m; i++ {
		pr := priorRatio(p.prior[i])
		theta := fixed.Mult16x32P15(pr, expinOne+fixed.Shl32(fixed.Extend32(p.post[i]), expinShift-snrShift))
		mm := hypergeomGain(theta)
		p.gain[i] = fixed.Extract16(fixed.Min32(fixed.Q15One, fixed.Mult16x32Q15(pr, mm)))
		g := p.gain[i]
		p.oldPS[i] = fixed.Mult16x32P15(c020, p.oldPS[i]) +
			fixed.Mult16x32P15(int16(fixed.Mult16x16P15(c080, int16(fixed.Mult16x16Q15(g, g)))), ps[i])

		p1 := int16(c0199 + fixed.Mult16x16Q15(c080, qcurve(z[i])))
		q := int16(fixed.Q15One - fixed.Mult16x16Q15(pFrame, p1))
		theta = fixed.Min32(theta, 32767)
		e := fixed.Exp(int16(fixed.Neg16(fixed.Extract16(theta))))
		tmp := int16(fixed.Mult16x16Q15(int16(1<<snrShift+int32(p.prior[i])),
			fixed.Extract16(fixed.Min32(fixed.Q15One, fixed.Shr32(e, 1)))))
		tmp = fixed.Min16(snr3, tmp)
		// Q15_ONE-q is MULT16_16_Q15(Pframe,P1) >= 882, so it is never zero.
		r := fixed.PDiv32x16(fixed.Shl32(fixed.Extend32(q), 8), int16(fixed.Q15One-int32(q)))
		tmp = fixed.Extract16(fixed.PShr32(fixed.Mult16x16(r, tmp), 8))
		p.gain2[i] = fixed.Div32x16OrZero(fixed.Shl32(32767, snrShift), fixed.Add16(256, tmp))
	}
	p.bank.computePSD16(p.gain2[n:], p.gain2[:n])
	p.bank.computePSD16(p.gain[n:], p.gain[:n])
	p.bank.computePSD16(p.gainFloor[n:], p.gainFloor[:n])

	for i := range n {
		pr := priorRatio(p.prior[i])
		theta := fixed.Mult16x32P15(pr, expinOne+fixed.Shl32(fixed.Extend32(p.post[i]), expinShift-snrShift))
		mm := hypergeomGain(theta)
		g := fixed.Extract16(fixed.Min32(fixed.Q15One, fixed.Mult16x32Q15(pr, mm)))
		pp := p.gain2[i]
		if fixed.Mult16x16Q15(c0333, g) > int32(p.gain[i]) {
			g = int16(fixed.Mult16x16(3, p.gain[i]))
		}
		p.gain[i] = g
		p.oldPS[i] = fixed.Mult16x32P15(c020, p.oldPS[i]) +
			fixed.Mult16x32P15(int16(fixed.Mult16x16P15(c080, int16(fixed.Mult16x16Q15(g, g)))), ps[i])
		if p.gain[i] < p.gainFloor[i] {
			p.gain[i] = p.gainFloor[i]
		}
		t := int16(fixed.Mult16x16P15(pp, fixed.Sqrt(fixed.Shl32(fixed.Extend32(p.gain[i]), 15))) +
			fixed.Mult16x16P15(int16(fixed.Sub16(fixed.Q15One, pp)), fixed.Sqrt(fixed.Shl32(fixed.Extend32(p.gainFloor[i]), 15))))
		p.gain2[i] = int16(fixed.Mult16x16Q15(t, t))
	}

	if !p.denoise {
		for i := range p.gain2 {
			p.gain2[i] = fixed.Q15One
		}
	}

	ft := p.ft
	for i := 1; i < n; i++ {
		ft[2*i-1] = int16(fixed.Mult16x16P15(p.gain2[i], ft[2*i-1]))
		ft[2*i] = int16(fixed.Mult16x16P15(p.gain2[i], ft[2*i]))
	}
	ft[0] = int16(fixed.Mult16x16P15(p.gain2[0], ft[0]))
	ft[2*n-1] = int16(fixed.Mult16x16P15(p.gain2[n-1], ft[2*n-1]))

	_ = p.fft.Inverse(ft, p.frame)
	for i, v := range p.frame {
		p.frame[i] = int16(fixed.Mult16x16Q15(fixed.PShr16(v, uint(p.frameShift)), p.window[i]))
	}
	for i := range n {
		x[i] = fixed.Word2Int(fixed.Add32(fixed.Extend32(p.outbuf[i]), fixed.Extend32(p.frame[i])))
	}
	copy(p.outbuf, p.frame[n:])

	p.speechProb = pFrame
	if !p.vad {
		return true
	}
	if p.speechProb > p.probStart || (p.wasSpeech && p.speechProb > p.probContinue) {
		p.wasSpeech = true
		return true
	}
	p.wasSpeech = false
	return false
}
