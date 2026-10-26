// Derived from SpeexDSP 1.2.1 (https://github.com/xiph/speexdsp, commit
// 1b28a0f61bc31162979e1f26f3981fc3637095c8): libspeexdsp/mdf.c.
//
// Copyright (C) 2003-2008 Jean-Marc Valin
//
// Use of this source is governed by the BSD-3-Clause licence in
// LICENSES/SpeexDSP-COPYING. Changes: ported to Go, FIXED_POINT with
// TWO_PATH, one microphone and one loudspeaker; warnings are not printed.

package echo

import (
	"github.com/danieldonoghue/govoip-dsp/internal/fft"
	"github.com/danieldonoghue/govoip-dsp/internal/fixed"
)

// FLOAT_EXTRACT16, FLOAT_EXTRACT32 and FLOAT_DIV32_FLOAT use the masked
// variants: their shift counts can leave [0, 31] here (docs/algorithms.md).

// mdf.c:84-86.
const (
	weightShift        = 11
	normalizeScaledown = 5
	normalizeScaleup   = 3
)

// playbackDelay is PLAYBACK_DELAY (mdf.c:120), in frames.
const playbackDelay = 2

// Pseudo-float constants of mdf.c:96-103.
var (
	minLeak      = fixed.Float{M: 20972, E: -22}
	var1Smooth   = fixed.Float{M: 23593, E: -16}
	var2Smooth   = fixed.Float{M: 23675, E: -15}
	var1Update   = fixed.Float{M: 16384, E: -15}
	var2Update   = fixed.Float{M: 16384, E: -16}
	varBacktrack = fixed.Float{M: 16384, E: -12}
)

// Q15 constants. A float literal (".6f") is passed as float32, a double
// literal (".7") as float64, matching QCONST16's evaluation in C.
var (
	q07    = fixed.QConst16(0.7, 15)
	q09    = fixed.QConst16(0.9, 15)
	q03    = fixed.QConst16(0.3, 15)
	q0982  = fixed.QConst16(0.982, 15)
	q0992  = fixed.QConst16(0.992, 15)
	q24Q11 = fixed.QConst16(2.4, 11)
	q01f   = fixed.QConst16(float64(float32(0.1)), 15)
	q099f  = fixed.QConst16(float64(float32(0.99)), 15)
	q08f   = fixed.QConst16(float64(float32(0.8)), 15)
	q06f   = fixed.QConst16(float64(float32(0.6)), 15)
	q04f   = fixed.QConst16(float64(float32(0.4)), 15)
	q085f  = fixed.QConst16(float64(float32(0.85)), 15)
	q015f  = fixed.QConst16(float64(float32(0.15)), 15)
	q003f  = fixed.QConst16(float64(float32(0.03)), 15)
	q025f  = fixed.QConst16(float64(float32(0.25)), 15)
	ssNum  = int16(11469) // mdf.c:709
)

// state is SpeexEchoState (mdf.c:126) for one microphone
// and one loudspeaker. Field names follow the C struct.
type state struct {
	frameSize   int
	windowSize  int // N
	m           int // M, the number of filter blocks
	cancelCount int32
	adapted     bool
	saturated   int
	screwedUp   int

	samplingRate int
	specAverage  int16
	beta0        int16
	betaMax      int16
	sumAdapt     int32
	leakEstimate int16

	e          []int16 // scratch, N
	x          []int16 // far-end input buffer, N
	X          []int16 // far-end spectra, (M+1)*N
	input      []int16 // scratch, frameSize
	y          []int16 // scratch, N
	lastY      []int16 // N
	Y          []int16 // scratch, N
	E          []int16 // N
	PHI        []int32 // scratch, N
	W          []int32 // background filter, M*N
	foreground []int16 // M*N
	davg1      int32
	davg2      int32
	dvar1      fixed.Float
	dvar2      fixed.Float
	power      []int32       // frameSize+1
	power1     []fixed.Float // frameSize+1
	wtmp       []int16       // scratch, N
	wtmp2      []int16       // scratch, N
	Rf         []int32       // frameSize+1
	Yf         []int32       // frameSize+1
	Xf         []int32       // frameSize+1
	Eh         []int32       // frameSize+1
	Yh         []int32       // frameSize+1
	pey        fixed.Float
	pyy        fixed.Float
	window     []int16 // N
	prop       []int16 // M
	fft        *fft.FFT
	memX       int16
	memD       int16
	memE       int16
	preemph    int16
	notchRad   int16
	notchMem   [2]int32

	playBuf        []int16 // (playbackDelay+1)*frameSize
	playBufPos     int
	playBufStarted bool
}

// newState is speex_echo_state_init (mdf.c:396) followed by
// SPEEX_ECHO_SET_SAMPLING_RATE. Arguments are validated by New.
func newState(frameSize, filterLength, rate int) (*state, error) {
	n := 2 * frameSize
	m := (filterLength + frameSize - 1) / frameSize
	f, err := fft.New(fft.Config{Size: n})
	if err != nil {
		return nil, err
	}
	c := &state{
		frameSize:  frameSize,
		windowSize: n,
		m:          m,
		fft:        f,
		e:          make([]int16, n),
		x:          make([]int16, n),
		input:      make([]int16, frameSize),
		y:          make([]int16, n),
		lastY:      make([]int16, n),
		Yf:         make([]int32, frameSize+1),
		Rf:         make([]int32, frameSize+1),
		Xf:         make([]int32, frameSize+1),
		Yh:         make([]int32, frameSize+1),
		Eh:         make([]int32, frameSize+1),
		X:          make([]int16, (m+1)*n),
		Y:          make([]int16, n),
		E:          make([]int16, n),
		W:          make([]int32, m*n),
		foreground: make([]int16, m*n),
		PHI:        make([]int32, n),
		power:      make([]int32, frameSize+1),
		power1:     make([]fixed.Float, frameSize+1),
		window:     make([]int16, n),
		prop:       make([]int16, m),
		wtmp:       make([]int16, n),
		wtmp2:      make([]int16, n),
		playBuf:    make([]int16, (playbackDelay+1)*frameSize),
		preemph:    q09,
		pey:        fixed.FloatOne,
		pyy:        fixed.FloatOne,
	}
	for i := 0; i < n>>1; i++ {
		// mdf.c:468: MULT16_16 casts i<<1 to spx_word16_t, DIV32_16 casts N.
		a := fixed.Div32x16(fixed.Mult16x16(25736, int16(i<<1)), int16(n))
		c.window[i] = int16(16383 - fixed.Shl16(fixed.Cos(a), 1))
		c.window[n-i-1] = c.window[i]
	}
	for i := range c.power1 {
		c.power1[i] = fixed.FloatOne
	}
	decay := int16(fixed.Shr32(fixed.Exp(int16(fixed.Neg16(fixed.Div32x16(int32(q24Q11), int16(m))))), 1))
	c.prop[0] = q07
	sum := fixed.Extend32(c.prop[0])
	for i := 1; i < m; i++ {
		c.prop[i] = int16(fixed.Mult16x16Q15(c.prop[i-1], decay))
		sum = fixed.Add32(sum, fixed.Extend32(c.prop[i]))
	}
	for i := m - 1; i >= 0; i-- {
		c.prop[i] = int16(fixed.Div32(fixed.Mult16x16(q08f, c.prop[i]), sum))
	}
	c.playBufPos = playbackDelay * frameSize
	c.setRate(rate)
	return c, nil
}

// setRate is SPEEX_ECHO_SET_SAMPLING_RATE (mdf.c:1231). DIV32_16 truncates
// the rate to spx_word16_t, so rates above 32767 give negative divisors.
func (c *state) setRate(rate int) {
	c.samplingRate = rate
	fs := fixed.Extend32(int16(c.frameSize))
	r := int16(rate)
	c.specAverage = fixed.Div32x16(fixed.Shl32(fs, 15), r)
	c.beta0 = fixed.Div32x16(fixed.Shl32(fs, 16), r)
	c.betaMax = fixed.Div32x16(fixed.Shl32(fs, 14), r)
	switch {
	case rate < 12000:
		c.notchRad = q09
	case rate < 24000:
		c.notchRad = q0982
	default:
		c.notchRad = q0992
	}
}

// reset is speex_echo_state_reset (mdf.c:524). Like the C code it keeps
// prop, leakEstimate, the second half of lastY and the rate settings.
func (c *state) reset() {
	c.cancelCount = 0
	c.screwedUp = 0
	clear(c.W)
	clear(c.foreground)
	clear(c.X)
	clear(c.power)
	for i := range c.power1 {
		c.power1[i] = fixed.FloatOne
	}
	clear(c.Eh)
	clear(c.Yh)
	clear(c.lastY[:c.frameSize])
	clear(c.E)
	clear(c.x)
	c.notchMem = [2]int32{}
	c.memD, c.memE, c.memX = 0, 0, 0
	c.saturated = 0
	c.adapted = false
	c.sumAdapt = 0
	c.pey, c.pyy = fixed.FloatOne, fixed.FloatOne
	c.davg1, c.davg2 = 0, 0
	c.dvar1, c.dvar2 = fixed.FloatZero, fixed.FloatZero
	clear(c.playBuf)
	c.playBufPos = playbackDelay * c.frameSize
	c.playBufStarted = false
}

// forward and inverse call the FFT on buffers sized at construction, so the
// length error the FFT can return cannot occur.
func (c *state) forward(in, out []int16) { _ = c.fft.Forward(in, out) }
func (c *state) inverse(in, out []int16) { _ = c.fft.Inverse(in, out) }

// filterDCNotch16 is filter_dc_notch16 (mdf.c:187) with stride 1.
func filterDCNotch16(in []int16, radius int16, out []int16, mem *[2]int32) {
	r1 := int16(32767 - int32(radius))
	den2 := int16(fixed.Mult16x16Q15(radius, radius) + fixed.Mult16x16Q15(q07, int16(fixed.Mult16x16Q15(r1, r1))))
	out = out[:len(in)]
	for i, vin := range in {
		vout := mem[0] + fixed.Shl32(fixed.Extend32(vin), 15)
		mem[0] = mem[1] + fixed.Shl32(fixed.Shl32(-fixed.Extend32(vin), 15)+fixed.Mult16x32Q15(radius, vout), 1)
		mem[1] = fixed.Shl32(fixed.Extend32(vin), 15) - fixed.Mult16x32Q15(den2, vout)
		out[i] = int16(fixed.Saturate32(fixed.PShr32(fixed.Mult16x32Q15(radius, vout), 15), 32767))
	}
}

// mdfInnerProd is mdf_inner_prod (mdf.c:212). An odd last sample is ignored.
func mdfInnerProd(x, y []int16, n int) int32 {
	var sum int32
	x, y = x[:n&^1], y[:n&^1]
	for i := 0; i+1 < len(x); i += 2 {
		part := fixed.Mult16x16(x[i], y[i])
		part = fixed.Mac16x16(part, x[i+1], y[i+1])
		sum = fixed.Add32(sum, fixed.Shr32(part, 6))
	}
	return sum
}

// powerSpectrum is power_spectrum (mdf.c:228): n/2+1 bins of a half-complex
// vector of n values.
func powerSpectrum(X []int16, ps []int32, n int) {
	ps[0] = fixed.Mult16x16(X[0], X[0])
	i, j := 1, 1
	for ; i < n-1; i, j = i+2, j+1 {
		ps[j] = fixed.Mult16x16(X[i], X[i]) + fixed.Mult16x16(X[i+1], X[i+1])
	}
	ps[j] = fixed.Mult16x16(X[i], X[i])
}

// powerSpectrumAccum is power_spectrum_accum (mdf.c:240).
func powerSpectrumAccum(X []int16, ps []int32, n int) {
	ps[0] += fixed.Mult16x16(X[0], X[0])
	i, j := 1, 1
	for ; i < n-1; i, j = i+2, j+1 {
		ps[j] += fixed.Mult16x16(X[i], X[i]) + fixed.Mult16x16(X[i+1], X[i+1])
	}
	ps[j] += fixed.Mult16x16(X[i], X[i])
}

// top16 is TOP16 (mdf.c:104).
func top16(x int32) int16 { return int16(x >> 16) }

// spectralMulAccum is spectral_mul_accum (mdf.c:253): the filter output
// spectrum of m blocks of 32-bit weights.
func spectralMulAccum(X []int16, Y []int32, acc []int16, n, m int) {
	var tmp1, tmp2 int32
	for j := 0; j < m; j++ {
		tmp1 = fixed.Mac16x16(tmp1, X[j*n], top16(Y[j*n]))
	}
	acc[0] = int16(fixed.PShr32(tmp1, weightShift))
	for i := 1; i < n-1; i += 2 {
		tmp1, tmp2 = 0, 0
		for j := 0; j < m; j++ {
			k := j*n + i
			tmp1 = fixed.Sub32(fixed.Mac16x16(tmp1, X[k], top16(Y[k])), fixed.Mult16x16(X[k+1], top16(Y[k+1])))
			tmp2 = fixed.Mac16x16(fixed.Mac16x16(tmp2, X[k+1], top16(Y[k])), X[k], top16(Y[k+1]))
		}
		acc[i] = int16(fixed.PShr32(tmp1, weightShift))
		acc[i+1] = int16(fixed.PShr32(tmp2, weightShift))
	}
	tmp1 = 0
	for j := 0; j < m; j++ {
		tmp1 = fixed.Mac16x16(tmp1, X[(j+1)*n-1], top16(Y[(j+1)*n-1]))
	}
	acc[n-1] = int16(fixed.PShr32(tmp1, weightShift))
}

// spectralMulAccum16 is spectral_mul_accum16 (mdf.c:280): as
// spectralMulAccum with 16-bit weights.
func spectralMulAccum16(X, Y, acc []int16, n, m int) {
	var tmp1, tmp2 int32
	for j := 0; j < m; j++ {
		tmp1 = fixed.Mac16x16(tmp1, X[j*n], Y[j*n])
	}
	acc[0] = int16(fixed.PShr32(tmp1, weightShift))
	for i := 1; i < n-1; i += 2 {
		tmp1, tmp2 = 0, 0
		for j := 0; j < m; j++ {
			k := j*n + i
			tmp1 = fixed.Sub32(fixed.Mac16x16(tmp1, X[k], Y[k]), fixed.Mult16x16(X[k+1], Y[k+1]))
			tmp2 = fixed.Mac16x16(fixed.Mac16x16(tmp2, X[k+1], Y[k]), X[k], Y[k+1])
		}
		acc[i] = int16(fixed.PShr32(tmp1, weightShift))
		acc[i+1] = int16(fixed.PShr32(tmp2, weightShift))
	}
	tmp1 = 0
	for j := 0; j < m; j++ {
		tmp1 = fixed.Mac16x16(tmp1, X[(j+1)*n-1], Y[(j+1)*n-1])
	}
	acc[n-1] = int16(fixed.PShr32(tmp1, weightShift))
}

// weightedSpectralMulConj is weighted_spectral_mul_conj (mdf.c:331).
func weightedSpectralMulConj(w []fixed.Float, p fixed.Float, X, Y []int16, prod []int32, n int) {
	W := fixed.FloatAMult(p, w[0])
	prod[0] = fixed.FloatMul32(W, fixed.Mult16x16(X[0], Y[0]))
	i, j := 1, 1
	for ; i < n-1; i, j = i+2, j+1 {
		W = fixed.FloatAMult(p, w[j])
		prod[i] = fixed.FloatMul32(W, fixed.Mac16x16(fixed.Mult16x16(X[i], Y[i]), X[i+1], Y[i+1]))
		// MULT16_16 casts -X[i+1] back to spx_word16_t, so -(-32768) wraps.
		prod[i+1] = fixed.FloatMul32(W, fixed.Mac16x16(fixed.Mult16x16(int16(fixed.Neg16(X[i+1])), Y[i]), X[i], Y[i+1]))
	}
	W = fixed.FloatAMult(p, w[j])
	prod[i] = fixed.FloatMul32(W, fixed.Mult16x16(X[i], Y[i]))
}

// mdfAdjustProp is mdf_adjust_prop (mdf.c:347) for one channel pair.
func mdfAdjustProp(W []int32, n, m int, prop []int16) {
	maxSum := int16(1)
	propSum := int32(1)
	for i := 0; i < m; i++ {
		tmp := int32(1)
		for _, w := range W[i*n : (i+1)*n] {
			t := fixed.Extract16(fixed.Shr32(w, 18))
			tmp += fixed.Mult16x16(t, t)
		}
		tmp = fixed.Min32(fixed.Abs32(tmp), 536870912)
		prop[i] = fixed.Sqrt(tmp)
		if prop[i] > maxSum {
			maxSum = prop[i]
		}
	}
	for i := 0; i < m; i++ {
		prop[i] = int16(int32(prop[i]) + fixed.Mult16x16Q15(q01f, maxSum))
		propSum += fixed.Extend32(prop[i])
	}
	for i := 0; i < m; i++ {
		prop[i] = int16(fixed.Div32(fixed.Mult16x16(q099f, prop[i]), propSum))
	}
}

// cancel is speex_echo_cancellation (mdf.c:686). in, far and out hold
// exactly frameSize samples; out must not overlap in.
func (c *state) cancel(in, far, out []int16) {
	fs := c.frameSize
	n := c.windowSize
	m := c.m
	in, far, out = in[:fs], far[:fs], out[:fs]

	c.cancelCount++
	ss := fixed.Div32x16(int32(ssNum), int16(m))
	ss1 := int16(fixed.Sub16(32767, ss))

	filterDCNotch16(in, c.notchRad, c.input, &c.notchMem)
	for i := 0; i < fs; i++ {
		tmp32 := fixed.Sub32(fixed.Extend32(c.input[i]), fixed.Mult16x16P15(c.preemph, c.memD))
		if tmp32 > 32767 {
			tmp32 = 32767
			if c.saturated == 0 {
				c.saturated = 1
			}
		}
		if tmp32 < -32767 {
			tmp32 = -32767
			if c.saturated == 0 {
				c.saturated = 1
			}
		}
		c.memD = c.input[i]
		c.input[i] = fixed.Extract16(tmp32)
	}

	for i := 0; i < fs; i++ {
		c.x[i] = c.x[i+fs]
		tmp32 := fixed.Sub32(fixed.Extend32(far[i]), fixed.Mult16x16P15(c.preemph, c.memX))
		if tmp32 > 32767 {
			tmp32 = 32767
			c.saturated = m + 1
		}
		if tmp32 < -32767 {
			tmp32 = -32767
			c.saturated = m + 1
		}
		c.x[i+fs] = fixed.Extract16(tmp32)
		c.memX = far[i]
	}

	// Shift the far-end spectra one block older (mdf.c:774); copy is a
	// memmove, as the descending block loop is.
	copy(c.X[n:], c.X[:m*n])
	// spx_fft rewrites x, which is read again below (contracts section 13).
	c.forward(c.x, c.X[:n])

	sxx := mdfInnerProd(c.x[fs:], c.x[fs:], fs)
	powerSpectrumAccum(c.X[:n], c.Xf, n)

	// Foreground filter.
	spectralMulAccum16(c.X, c.foreground, c.Y, n, m)
	c.inverse(c.Y, c.e)
	for i := 0; i < fs; i++ {
		c.e[i] = int16(fixed.Sub16(c.input[i], c.e[i+fs]))
	}
	sff := mdfInnerProd(c.e, c.e, fs)

	if c.adapted {
		mdfAdjustProp(c.W, n, m, c.prop)
	}
	if c.saturated == 0 {
		for j := m - 1; j >= 0; j-- {
			p := fixed.FloatShl(fixed.PseudoFloat(int32(c.prop[j])), -15)
			weightedSpectralMulConj(c.power1, p, c.X[(j+1)*n:(j+2)*n], c.E, c.PHI, n)
			w := c.W[j*n : (j+1)*n]
			for i := range w {
				w[i] += c.PHI[i]
			}
		}
	} else {
		c.saturated--
	}

	// AUMDF constraint (mdf.c:828). For m = 1 only j = 0 runs, so the
	// modulo by m-1 = 0 is never evaluated, as in C.
	for j := 0; j < m; j++ {
		if j == 0 || c.cancelCount%int32(m-1) == int32(j-1) {
			w := c.W[j*n : (j+1)*n]
			for i := range w {
				c.wtmp2[i] = fixed.Extract16(fixed.PShr32(w[i], normalizeScaledown+16))
			}
			c.inverse(c.wtmp2, c.wtmp)
			clear(c.wtmp[:fs])
			for i := fs; i < n; i++ {
				c.wtmp[i] = int16(fixed.Shl16(c.wtmp[i], normalizeScaleup))
			}
			c.forward(c.wtmp, c.wtmp2)
			for i := range w {
				w[i] -= fixed.Shl32(fixed.Extend32(c.wtmp2[i]), 16+normalizeScaledown-normalizeScaleup-1)
			}
		}
	}

	clear(c.Rf)
	clear(c.Yf)
	clear(c.Xf)

	// Background filter and the difference between the two (mdf.c:875).
	spectralMulAccum(c.X, c.W, c.Y, n, m)
	c.inverse(c.Y, c.y)
	for i := 0; i < fs; i++ {
		c.e[i] = int16(fixed.Sub16(c.e[i+fs], c.y[i+fs]))
	}
	dbf := 10 + mdfInnerProd(c.e, c.e, fs)
	for i := 0; i < fs; i++ {
		c.e[i] = int16(fixed.Sub16(c.input[i], c.y[i+fs]))
	}
	see := mdfInnerProd(c.e, c.e, fs)

	// Foreground update logic (mdf.c:896).
	c.davg1 = fixed.Add32(fixed.Mult16x32Q15(q06f, c.davg1), fixed.Mult16x32Q15(q04f, fixed.Sub32(sff, see)))
	c.davg2 = fixed.Add32(fixed.Mult16x32Q15(q085f, c.davg2), fixed.Mult16x32Q15(q015f, fixed.Sub32(sff, see)))
	c.dvar1 = fixed.FloatAdd(fixed.FloatMult(var1Smooth, c.dvar1),
		fixed.FloatMul32U(fixed.Mult16x32Q15(q04f, sff), fixed.Mult16x32Q15(q04f, dbf)))
	c.dvar2 = fixed.FloatAdd(fixed.FloatMult(var2Smooth, c.dvar2),
		fixed.FloatMul32U(fixed.Mult16x32Q15(q015f, sff), fixed.Mult16x32Q15(q015f, dbf)))

	diff := fixed.Sub32(sff, see)
	updateForeground := false
	switch {
	case fixed.FloatGT(fixed.FloatMul32U(diff, fixed.Abs32(diff)), fixed.FloatMul32U(sff, dbf)):
		updateForeground = true
	case fixed.FloatGT(fixed.FloatMul32U(c.davg1, fixed.Abs32(c.davg1)), fixed.FloatMult(var1Update, c.dvar1)):
		updateForeground = true
	case fixed.FloatGT(fixed.FloatMul32U(c.davg2, fixed.Abs32(c.davg2)), fixed.FloatMult(var2Update, c.dvar2)):
		updateForeground = true
	}

	if updateForeground {
		c.davg1, c.davg2 = 0, 0
		c.dvar1, c.dvar2 = fixed.FloatZero, fixed.FloatZero
		for i, w := range c.W {
			c.foreground[i] = fixed.Extract16(fixed.PShr32(w, 16))
		}
		// Smooth transition (mdf.c:929).
		for i := 0; i < fs; i++ {
			c.e[i+fs] = int16(fixed.Mult16x16Q15(c.window[i+fs], c.e[i+fs]) + fixed.Mult16x16Q15(c.window[i], c.y[i+fs]))
		}
	} else {
		resetBackground := false
		if fixed.FloatGT(fixed.FloatMul32U(fixed.Neg32(diff), fixed.Abs32(diff)), fixed.FloatMult(varBacktrack, fixed.FloatMul32U(sff, dbf))) {
			resetBackground = true
		}
		if fixed.FloatGT(fixed.FloatMul32U(fixed.Neg32(c.davg1), fixed.Abs32(c.davg1)), fixed.FloatMult(varBacktrack, c.dvar1)) {
			resetBackground = true
		}
		if fixed.FloatGT(fixed.FloatMul32U(fixed.Neg32(c.davg2), fixed.Abs32(c.davg2)), fixed.FloatMult(varBacktrack, c.dvar2)) {
			resetBackground = true
		}
		if resetBackground {
			for i, f := range c.foreground {
				c.W[i] = fixed.Shl32(fixed.Extend32(f), 16)
			}
			for i := 0; i < fs; i++ {
				c.y[i+fs] = c.e[i+fs]
			}
			for i := 0; i < fs; i++ {
				c.e[i] = int16(fixed.Sub16(c.input[i], c.y[i+fs]))
			}
			see = sff
			c.davg1, c.davg2 = 0, 0
			c.dvar1, c.dvar2 = fixed.FloatZero, fixed.FloatZero
		}
	}

	// Output with de-emphasis (mdf.c:963).
	for i := 0; i < fs; i++ {
		tmpOut := fixed.Sub32(fixed.Extend32(c.input[i]), fixed.Extend32(c.e[i+fs]))
		tmpOut = fixed.Add32(tmpOut, fixed.Mult16x16P15(c.preemph, c.memE))
		if in[i] <= -32000 || in[i] >= 32000 {
			if c.saturated == 0 {
				c.saturated = 1
			}
		}
		out[i] = fixed.Word2Int(tmpOut)
		c.memE = int16(tmpOut)
	}

	for i := 0; i < fs; i++ {
		c.e[i+fs] = c.e[i]
		c.e[i] = 0
	}

	sey := mdfInnerProd(c.e[fs:], c.y[fs:], fs)
	syy := mdfInnerProd(c.y[fs:], c.y[fs:], fs)
	sdd := mdfInnerProd(c.input, c.input, fs)

	c.forward(c.e, c.E)
	clear(c.y[:fs])
	c.forward(c.y, c.Y)

	powerSpectrumAccum(c.E, c.Rf, n)
	powerSpectrumAccum(c.Y, c.Yf, n)

	// Sanity check (mdf.c:1014).
	switch {
	case !(syy >= 0 && sxx >= 0 && see >= 0):
		c.screwedUp += 50
		clear(out)
	case fixed.Shr32(sff, 2) > fixed.Add32(sdd, fixed.Shr32(fixed.Mult16x16(int16(n), 10000), 6)):
		c.screwedUp++
	default:
		c.screwedUp = 0
	}
	if c.screwedUp >= 50 {
		c.reset()
		return
	}

	see = fixed.Max32(see, fixed.Shr32(fixed.Mult16x16(int16(n), 100), 6))

	// mdf.c:1044 adds the far-end energy a second time; Xf was cleared.
	sxx += mdfInnerProd(c.x[fs:], c.x[fs:], fs)
	powerSpectrumAccum(c.X[:n], c.Xf, n)

	for j := 0; j <= fs; j++ {
		c.power[j] = fixed.Mult16x32Q15(ss1, c.power[j]) + 1 + fixed.Mult16x32Q15(ss, c.Xf[j])
	}

	pey, pyy := fixed.FloatOne, fixed.FloatOne
	sa1 := int16(fixed.Sub16(32767, c.specAverage))
	for j := fs; j >= 0; j-- {
		eh := fixed.PseudoFloat(c.Rf[j] - c.Eh[j])
		yh := fixed.PseudoFloat(c.Yf[j] - c.Yh[j])
		pey = fixed.FloatAdd(pey, fixed.FloatMult(eh, yh))
		pyy = fixed.FloatAdd(pyy, fixed.FloatMult(yh, yh))
		c.Eh[j] = fixed.Mac16x32Q15(fixed.Mult16x32Q15(sa1, c.Eh[j]), c.specAverage, c.Rf[j])
		c.Yh[j] = fixed.Mac16x32Q15(fixed.Mult16x32Q15(sa1, c.Yh[j]), c.specAverage, c.Yf[j])
	}
	pyy = fixed.FloatSqrt(pyy)
	pey = fixed.FloatDivU(pey, pyy)

	// Correlation update rate (mdf.c:1074). see > 0 after the floor above.
	tmp32 := fixed.Mult16x32Q15(c.beta0, syy)
	if lim := fixed.Mult16x32Q15(c.betaMax, see); tmp32 > lim {
		tmp32 = lim
	}
	alpha := fixed.FloatDiv32(tmp32, see)
	alpha1 := fixed.FloatSub(fixed.FloatOne, alpha)
	c.pey = fixed.FloatAdd(fixed.FloatMult(alpha1, c.pey), fixed.FloatMult(alpha, pey))
	c.pyy = fixed.FloatAdd(fixed.FloatMult(alpha1, c.pyy), fixed.FloatMult(alpha, pyy))
	if fixed.FloatLT(c.pyy, fixed.FloatOne) {
		c.pyy = fixed.FloatOne
	}
	if fixed.FloatLT(c.pey, fixed.FloatMult(minLeak, c.pyy)) {
		c.pey = fixed.FloatMult(minLeak, c.pyy)
	}
	if fixed.FloatGT(c.pey, c.pyy) {
		c.pey = c.pyy
	}
	c.leakEstimate = fixed.FloatExtract16Masked(fixed.FloatShl(fixed.FloatDivU(c.pey, c.pyy), 14))
	if c.leakEstimate > 16383 {
		c.leakEstimate = 32767
	} else {
		c.leakEstimate = int16(fixed.Shl16(c.leakEstimate, 1))
	}

	// Residual to error ratio (mdf.c:1100).
	tmp32 = fixed.Mult16x32Q15(c.leakEstimate, syy)
	tmp32 = fixed.Add32(fixed.Shr32(sxx, 13), fixed.Add32(tmp32, fixed.Shl32(tmp32, 1)))
	bound := fixed.PseudoFloat(sey)
	bound = fixed.FloatDivU(fixed.FloatMult(bound, bound), fixed.PseudoFloat(fixed.Add32(1, syy)))
	if fixed.FloatGT(bound, fixed.PseudoFloat(see)) {
		tmp32 = see
	} else if b := fixed.FloatExtract32Masked(bound); tmp32 < b {
		tmp32 = b
	}
	if tmp32 > fixed.Shr32(see, 1) {
		tmp32 = fixed.Shr32(see, 1)
	}
	rer := fixed.FloatExtract16Masked(fixed.FloatShl(fixed.FloatDiv32(tmp32, see), 15))

	if !c.adapted && c.sumAdapt > fixed.Shl32(int32(m), 15) &&
		fixed.Mult16x32Q15(c.leakEstimate, syy) > fixed.Mult16x32Q15(q003f, syy) {
		c.adapted = true
	}

	if c.adapted {
		for i := 0; i <= fs; i++ {
			r := fixed.Mult16x32Q15(c.leakEstimate, fixed.Shl32(c.Yf[i], 3))
			e := fixed.Shl32(c.Rf[i], 3) + 1
			if r > fixed.Shr32(e, 1) {
				r = fixed.Shr32(e, 1)
			}
			r = fixed.Mult16x32Q15(q07, r) + fixed.Mult16x32Q15(q03, fixed.Mult16x32Q15(rer, e))
			// e is odd, so nonzero; docs/algorithms.md shows power > 0.
			c.power1[i] = fixed.FloatShl(fixed.FloatDiv32FloatMasked(r, fixed.FloatMul32U(e, c.power[i]+10)), weightShift+16)
		}
	} else {
		adaptRate := int16(0)
		if sxx > fixed.Shr32(fixed.Mult16x16(int16(n), 1000), 6) {
			tmp32 = fixed.Mult16x32Q15(q025f, sxx)
			if tmp32 > fixed.Shr32(see, 2) {
				tmp32 = fixed.Shr32(see, 2)
			}
			adaptRate = fixed.FloatExtract16Masked(fixed.FloatShl(fixed.FloatDiv32(tmp32, see), 15))
		}
		for i := 0; i <= fs; i++ {
			c.power1[i] = fixed.FloatShl(fixed.FloatDiv32(fixed.Extend32(adaptRate), fixed.Add32(c.power[i], 10)), weightShift+1)
		}
		c.sumAdapt = fixed.Add32(c.sumAdapt, int32(adaptRate))
	}

	copy(c.lastY[:fs], c.lastY[fs:])
	if c.adapted {
		for i := 0; i < fs; i++ {
			c.lastY[fs+i] = int16(int32(in[i]) - int32(out[i]))
		}
	}
}

// residual is speex_echo_get_residual (mdf.c:1190). It writes the scratch
// buffers y and Y, which cancel overwrites before reading.
func (c *state) residual(dst []int32) {
	n := c.windowSize
	for i := 0; i < n; i++ {
		c.y[i] = int16(fixed.Mult16x16Q15(c.window[i], c.lastY[i]))
	}
	c.forward(c.y, c.Y)
	powerSpectrum(c.Y, dst, n)
	leak2 := int16(32767)
	if c.leakEstimate <= 16383 {
		leak2 = int16(fixed.Shl16(c.leakEstimate, 1))
	}
	for i := 0; i <= c.frameSize; i++ {
		dst[i] = fixed.Mult16x32Q15(leak2, dst[i])
	}
}

// impulseResponse is SPEEX_ECHO_GET_IMPULSE_RESPONSE (mdf.c:1255), writing
// m*frameSize values. It uses only the scratch buffers wtmp and wtmp2.
func (c *state) impulseResponse(dst []int32) {
	n, fs := c.windowSize, c.frameSize
	for j := 0; j < c.m; j++ {
		for i := 0; i < n; i++ {
			c.wtmp2[i] = fixed.Extract16(fixed.PShr32(c.W[j*n+i], 16+normalizeScaledown))
		}
		c.inverse(c.wtmp2, c.wtmp)
		for i := 0; i < fs; i++ {
			dst[j*fs+i] = fixed.PShr32(fixed.Mult16x16(32767, c.wtmp[i]), weightShift-normalizeScaledown)
		}
	}
}

// capture is speex_echo_capture (mdf.c:630).
func (c *state) capture(rec, out []int16) {
	fs := c.frameSize
	c.playBufStarted = true
	if c.playBufPos >= fs {
		c.cancel(rec, c.playBuf[:fs], out)
		c.playBufPos -= fs
		copy(c.playBuf[:c.playBufPos], c.playBuf[fs:fs+c.playBufPos])
		return
	}
	// "No playback frame available": pass the microphone through.
	if c.playBufPos != 0 {
		c.playBufPos = 0
	}
	copy(out[:fs], rec[:fs])
}

// playback is speex_echo_playback (mdf.c:653).
func (c *state) playback(play []int16) {
	fs := c.frameSize
	if !c.playBufStarted {
		return // "discarded first playback frame"
	}
	if c.playBufPos > playbackDelay*fs {
		return // "Had to discard a playback frame"
	}
	copy(c.playBuf[c.playBufPos:c.playBufPos+fs], play[:fs])
	c.playBufPos += fs
	if c.playBufPos <= (playbackDelay-1)*fs {
		// "Auto-filling the buffer"
		copy(c.playBuf[c.playBufPos:c.playBufPos+fs], play[:fs])
		c.playBufPos += fs
	}
}
