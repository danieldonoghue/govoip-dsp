// Derived from SpeexDSP 1.2.1 (https://github.com/xiph/speexdsp, commit
// 1b28a0f61bc31162979e1f26f3981fc3637095c8): libspeexdsp/kiss_fft.c and
// libspeexdsp/_kiss_fft_guts.h.
//
// Copyright (c) 2003-2004, Mark Borgerding
//
// Use of this source is governed by the BSD-3-Clause licence in
// LICENSES/kissfft-COPYING. Changes: ported to Go, FIXED_POINT branch only,
// indices instead of pointers.

package fft

import "github.com/danieldonoghue/govoip-dsp/internal/fixed"

// maxFactors is MAXFACTORS (_kiss_fft_guts.h:25).
const maxFactors = 32

// sampMax is SAMP_MAX (_kiss_fft_guts.h:51).
const sampMax = 32767

// maxRadix is the largest radix kf_bfly_generic accepts (kiss_fft.c:293).
const maxRadix = 17

// cpx is kiss_fft_cpx with kiss_fft_scalar = spx_int16_t.
type cpx struct {
	r, i int16
}

// kissState is struct kiss_fft_state (_kiss_fft_guts.h:31).
type kissState struct {
	nfft     int
	inverse  bool
	factors  [2 * maxFactors]int
	twiddles []cpx
}

// sround is sround (_kiss_fft_guts.h:63), truncated to kiss_fft_scalar.
func sround(x int32) int16 { return int16(fixed.PShr32(x, 15)) }

// sMul is S_MUL (_kiss_fft_guts.h:65).
func sMul(a, b int16) int16 { return sround(fixed.Mult16x16(a, b)) }

// cMul is C_MUL (_kiss_fft_guts.h:67).
func cMul(a, b cpx) cpx {
	return cpx{
		r: sround(fixed.Mult16x16(a.r, b.r) - fixed.Mult16x16(a.i, b.i)),
		i: sround(fixed.Mult16x16(a.r, b.i) + fixed.Mult16x16(a.i, b.r)),
	}
}

// cMul4 is C_MUL4 (_kiss_fft_guts.h:71).
func cMul4(a, b cpx) cpx {
	return cpx{
		r: int16(fixed.PShr32(fixed.Mult16x16(a.r, b.r)-fixed.Mult16x16(a.i, b.i), 17)),
		i: int16(fixed.PShr32(fixed.Mult16x16(a.r, b.i)+fixed.Mult16x16(a.i, b.r), 17)),
	}
}

// cFixDiv is C_FIXDIV (_kiss_fft_guts.h:78). div is a radix, at least 1.
func cFixDiv(c *cpx, div int) {
	k := int16(sampMax / div)
	c.r = sMul(c.r, k)
	c.i = sMul(c.i, k)
}

func cAdd(a, b cpx) cpx { return cpx{a.r + b.r, a.i + b.i} }
func cSub(a, b cpx) cpx { return cpx{a.r - b.r, a.i - b.i} }

// kfCexp2 is kf_cexp2 (_kiss_fft_guts.h:151).
func kfCexp2(phase int32) cpx {
	return cpx{fixed.CosNorm(phase), fixed.CosNorm(phase - 32768)}
}

// init is kiss_fft_alloc (kiss_fft.c:464) for FIXED_POINT, writing into
// preallocated memory. nfft must be at least 1.
func (st *kissState) init(nfft int, inverse bool) {
	st.nfft = nfft
	st.inverse = inverse
	st.twiddles = make([]cpx, nfft)
	for i := range st.twiddles {
		phase := int32(i)
		if !inverse {
			phase = -phase
		}
		st.twiddles[i] = kfCexp2(fixed.Div32(fixed.Shl32(phase, 17), int32(nfft)))
	}
	kfFactor(nfft, st.factors[:])
}

// kfFactor is kf_factor (kiss_fft.c:437): radix and remaining length pairs,
// powers of 4 first, then 2, then odd factors.
func kfFactor(n int, facbuf []int) {
	p := 4
	j := 0
	for {
		for n%p != 0 {
			switch p {
			case 4:
				p = 2
			case 2:
				p = 3
			default:
				p += 2
			}
			if p > 32000 || int32(p)*int32(p) > int32(n) {
				p = n
			}
		}
		n /= p
		facbuf[j] = p
		facbuf[j+1] = n
		j += 2
		if n <= 1 {
			return
		}
	}
}

// transform is kiss_fft (kiss_fft.c:519): fout must not overlap fin.
func (st *kissState) transform(fin, fout []cpx) {
	st.shuffle(fout, 0, fin, 0, 1, 0)
	st.work(fout, 1, 0, 1, 1)
}

// shuffle is kf_shuffle (kiss_fft.c:322) with in_stride 1.
func (st *kissState) shuffle(fout []cpx, fo int, f []cpx, fi, fstride, fac int) {
	p := st.factors[fac]
	m := st.factors[fac+1]
	if m == 1 {
		for j := 0; j < p; j++ {
			fout[fo+j] = f[fi]
			fi += fstride
		}
		return
	}
	for j := 0; j < p; j++ {
		st.shuffle(fout, fo, f, fi, fstride*p, fac+2)
		fi += fstride
		fo += m
	}
}

// work is the active (#else) branch of kf_work (kiss_fft.c:400): it runs
// the later stages first, then this stage's butterflies on n blocks of m2.
func (st *kissState) work(fout []cpx, fstride, fac, n, m2 int) {
	p := st.factors[fac]
	m := st.factors[fac+1]
	if m != 1 {
		st.work(fout, fstride*p, fac+2, n*p, m)
	}
	switch p {
	case 2:
		st.bfly2(fout, fstride, m, n, m2)
	case 3:
		for i := 0; i < n; i++ {
			st.bfly3(fout[i*m2:], fstride, m)
		}
	case 4:
		st.bfly4(fout, fstride, m, n, m2)
	case 5:
		for i := 0; i < n; i++ {
			st.bfly5(fout[i*m2:], fstride, m)
		}
	default:
		for i := 0; i < n; i++ {
			st.bflyGeneric(fout[i*m2:], fstride, m, p)
		}
	}
}

// bfly2 is kf_bfly2 (kiss_fft.c:29).
func (st *kissState) bfly2(fout []cpx, fstride, m, n, mm int) {
	tw := st.twiddles
	for i := 0; i < n; i++ {
		f := fout[i*mm:]
		t1 := 0
		for j := 0; j < m; j++ {
			a, b := &f[j], &f[j+m]
			w := tw[t1]
			t1 += fstride
			if !st.inverse {
				// Divides by two while keeping the best accuracy possible.
				tr := fixed.Shr32(fixed.Sub32(fixed.Mult16x16(b.r, w.r), fixed.Mult16x16(b.i, w.i)), 1)
				ti := fixed.Shr32(fixed.Add32(fixed.Mult16x16(b.i, w.r), fixed.Mult16x16(b.r, w.i)), 1)
				b.r = int16(fixed.PShr32(fixed.Sub32(fixed.Shl32(fixed.Extend32(a.r), 14), tr), 15))
				b.i = int16(fixed.PShr32(fixed.Sub32(fixed.Shl32(fixed.Extend32(a.i), 14), ti), 15))
				a.r = int16(fixed.PShr32(fixed.Add32(fixed.Shl32(fixed.Extend32(a.r), 14), tr), 15))
				a.i = int16(fixed.PShr32(fixed.Add32(fixed.Shl32(fixed.Extend32(a.i), 14), ti), 15))
			} else {
				t := cMul(*b, w)
				*b = cSub(*a, t)
				*a = cAdd(*a, t)
			}
		}
	}
}

// bfly4 is kf_bfly4 (kiss_fft.c:86).
func (st *kissState) bfly4(fout []cpx, fstride, m, n, mm int) {
	tw := st.twiddles
	m2, m3 := 2*m, 3*m
	for i := 0; i < n; i++ {
		f := fout[i*mm:]
		t1, t2, t3 := 0, 0, 0
		for j := 0; j < m; j++ {
			var s0, s1, s2 cpx
			if st.inverse {
				s0 = cMul(f[j+m], tw[t1])
				s1 = cMul(f[j+m2], tw[t2])
				s2 = cMul(f[j+m3], tw[t3])
			} else {
				s0 = cMul4(f[j+m], tw[t1])
				s1 = cMul4(f[j+m2], tw[t2])
				s2 = cMul4(f[j+m3], tw[t3])
				f[j].r = fixed.PShr16(f[j].r, 2)
				f[j].i = fixed.PShr16(f[j].i, 2)
			}
			s5 := cSub(f[j], s1)
			f[j] = cAdd(f[j], s1)
			s3 := cAdd(s0, s2)
			s4 := cSub(s0, s2)
			// kiss_fft.c:150 also scales f[j+m2] here; the next line overwrites it.
			f[j+m2] = cSub(f[j], s3)
			t1 += fstride
			t2 += fstride * 2
			t3 += fstride * 3
			f[j] = cAdd(f[j], s3)
			if st.inverse {
				f[j+m] = cpx{s5.r - s4.i, s5.i + s4.r}
				f[j+m3] = cpx{s5.r + s4.i, s5.i - s4.r}
			} else {
				f[j+m] = cpx{s5.r + s4.i, s5.i - s4.r}
				f[j+m3] = cpx{s5.r - s4.i, s5.i + s4.r}
			}
		}
	}
}

// bfly3 is kf_bfly3 (kiss_fft.c:168).
func (st *kissState) bfly3(f []cpx, fstride, m int) {
	tw := st.twiddles
	m2 := 2 * m
	epi3 := tw[fstride*m]
	t1, t2 := 0, 0
	for j := 0; j < m; j++ {
		if !st.inverse {
			cFixDiv(&f[j], 3)
			cFixDiv(&f[j+m], 3)
			cFixDiv(&f[j+m2], 3)
		}
		s1 := cMul(f[j+m], tw[t1])
		s2 := cMul(f[j+m2], tw[t2])
		s3 := cAdd(s1, s2)
		s0 := cSub(s1, s2)
		t1 += fstride
		t2 += fstride * 2

		// HALF_OF is >>1 on the int16 operand.
		f[j+m].r = f[j].r - s3.r>>1
		f[j+m].i = f[j].i - s3.i>>1

		s0.r = sMul(s0.r, epi3.i)
		s0.i = sMul(s0.i, epi3.i)

		f[j] = cAdd(f[j], s3)

		f[j+m2].r = f[j+m].r + s0.i
		f[j+m2].i = f[j+m].i - s0.r

		f[j+m].r -= s0.i
		f[j+m].i += s0.r
	}
}

// bfly5 is kf_bfly5 (kiss_fft.c:214).
func (st *kissState) bfly5(f []cpx, fstride, m int) {
	tw := st.twiddles
	ya := tw[fstride*m]
	yb := tw[fstride*2*m]
	for u := 0; u < m; u++ {
		f0, f1, f2, f3, f4 := &f[u], &f[u+m], &f[u+2*m], &f[u+3*m], &f[u+4*m]
		if !st.inverse {
			cFixDiv(f0, 5)
			cFixDiv(f1, 5)
			cFixDiv(f2, 5)
			cFixDiv(f3, 5)
			cFixDiv(f4, 5)
		}
		s0 := *f0

		s1 := cMul(*f1, tw[u*fstride])
		s2 := cMul(*f2, tw[2*u*fstride])
		s3 := cMul(*f3, tw[3*u*fstride])
		s4 := cMul(*f4, tw[4*u*fstride])

		s7 := cAdd(s1, s4)
		s10 := cSub(s1, s4)
		s8 := cAdd(s2, s3)
		s9 := cSub(s2, s3)

		f0.r += s7.r + s8.r
		f0.i += s7.i + s8.i

		s5 := cpx{
			s0.r + sMul(s7.r, ya.r) + sMul(s8.r, yb.r),
			s0.i + sMul(s7.i, ya.r) + sMul(s8.i, yb.r),
		}
		s6 := cpx{
			sMul(s10.i, ya.i) + sMul(s9.i, yb.i),
			-sMul(s10.r, ya.i) - sMul(s9.r, yb.i),
		}
		*f1 = cSub(s5, s6)
		*f4 = cAdd(s5, s6)

		s11 := cpx{
			s0.r + sMul(s7.r, yb.r) + sMul(s8.r, ya.r),
			s0.i + sMul(s7.i, yb.r) + sMul(s8.i, ya.r),
		}
		s12 := cpx{
			-sMul(s10.i, yb.i) + sMul(s9.i, ya.i),
			sMul(s10.r, yb.i) - sMul(s9.r, ya.i),
		}
		*f2 = cAdd(s11, s12)
		*f3 = cSub(s11, s12)
	}
}

// bflyGeneric is kf_bfly_generic (kiss_fft.c:278). p is at most maxRadix,
// which New guarantees.
func (st *kissState) bflyGeneric(f []cpx, fstride, m, p int) {
	tw := st.twiddles
	var scratch [maxRadix]cpx
	for u := 0; u < m; u++ {
		k := u
		for q1 := 0; q1 < p; q1++ {
			scratch[q1] = f[k]
			if !st.inverse {
				cFixDiv(&scratch[q1], p)
			}
			k += m
		}
		k = u
		for q1 := 0; q1 < p; q1++ {
			twidx := 0
			f[k] = scratch[0]
			for q := 1; q < p; q++ {
				twidx += fstride * k
				if twidx >= st.nfft {
					twidx -= st.nfft
				}
				f[k] = cAdd(f[k], cMul(scratch[q], tw[twidx]))
			}
			k += m
		}
	}
}
