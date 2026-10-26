// Derived from SpeexDSP 1.2.1 (https://github.com/xiph/speexdsp, commit
// 1b28a0f61bc31162979e1f26f3981fc3637095c8): libspeexdsp/math_approx.h.
//
// Copyright (C) 2002 Jean-Marc Valin
//
// Use of this source is governed by the BSD-3-Clause licence in
// LICENSES/SpeexDSP-COPYING. Changes: ported to Go.

package fixed

import "math/bits"

// ILog2 is spx_ilog2: floor(log2(x)), and 0 for x = 0.
func ILog2(x uint32) int16 {
	if x == 0 {
		return 0
	}
	return int16(bits.Len32(x) - 1)
}

// ilog4 is spx_ilog4: floor(log4(x)), and 0 for x = 0.
func ilog4(x uint32) int16 {
	if x == 0 {
		return 0
	}
	return int16((bits.Len32(x) - 1) / 2)
}

// Sqrt is spx_sqrt (math_approx.h:142): the square root of a Q(2n) value in
// Q(n), by a cubic polynomial after normalisation.
// Within 40 units of the true root for x in [0, 1073709056]; larger x overflow.
func Sqrt(x int32) int16 {
	const c0, c1, c2, c3 = 3634, 21173, -12627, 4204
	k := int(ilog4(uint32(x))) - 6
	x = VShr32(x, k<<1)
	t := Mult16x16Q14(int16(x), c3)
	t = Mult16x16Q14(int16(x), Add16(c2, int16(t)))
	t = Mult16x16Q14(int16(x), Add16(c1, int16(t)))
	rt := int32(Add16(c0, int16(t)))
	return int16(VShr32(rt, 7-k))
}

// Cos is spx_cos (math_approx.h:188): cosine of a Q13 angle in radians,
// in Q13, for angles in [0, pi].
// Within 9 units of the true value on [0, pi]; not a substitute for math.Cos.
func Cos(x int16) int16 {
	const k1, k2, k3, k4 = 8192, -4096, 340, -10
	if x < 12868 {
		x2 := int16(mult16x16P13(x, x))
		t := Add32(k3, mult16x16P13(k4, x2))
		t = Add32(k2, mult16x16P13(x2, int16(t)))
		return int16(Add32(k1, mult16x16P13(x2, int16(t))))
	}
	x = int16(Sub16(25736, x))
	x2 := int16(mult16x16P13(x, x))
	t := Add32(k3, mult16x16P13(k4, x2))
	t = Add32(k2, mult16x16P13(x2, int16(t)))
	return int16(Sub32(-k1, mult16x16P13(x2, int16(t))))
}

// cosPi2 is _spx_cos_pi_2 (math_approx.h:208).
func cosPi2(x int16) int16 {
	const l1, l2, l3, l4 = 32767, -7651, 8277, -626
	x2 := int16(Mult16x16P15(x, x))
	t := Add32(l3, Mult16x16P15(l4, x2))
	t = Add32(l2, Mult16x16P15(x2, int16(t)))
	t = Add32(Sub16(l1, x2), Mult16x16P15(x2, int16(t)))
	// MIN16 compares the untruncated int; ADD16 then truncates it.
	return Add16(1, int16(min(32766, t)))
}

// CosNorm is spx_cos_norm (math_approx.h:216): cos(pi/2 * x/2^15) in Q15 for
// any x, the period being 2^17.
// Within 2 units of the true value for every x; not a substitute for math.Cos.
func CosNorm(x int32) int16 {
	x &= 0x0001ffff
	if x > 1<<16 {
		x = 1<<17 - x
	}
	if x&0x00007fff != 0 {
		if x < 1<<15 {
			return cosPi2(int16(x))
		}
		return int16(Neg32(int32(cosPi2(int16(65536 - x)))))
	}
	if x&0x0000ffff != 0 {
		return 0
	}
	if x&0x0001ffff != 0 {
		return -32767
	}
	return 32767
}

// exp2 is spx_exp2 (math_approx.h:250): 2^x for x in Q11, in Q16.
func exp2(x int16) int32 {
	const d0, d1, d2, d3 = 16384, 11356, 3726, 1301
	integer := int(shr16(x, 11))
	if integer > 14 {
		return 0x7fffffff
	}
	if integer < -15 {
		return 0
	}
	frac := int16((int32(x) - int32(integer)<<11) << 3)
	t := Mult16x16Q14(d3, frac)
	t = Mult16x16Q14(frac, Add16(d2, int16(t)))
	t = Mult16x16Q14(frac, Add16(d1, int16(t)))
	frac = Add16(d0, int16(t))
	return VShr32(Extend32(frac), -integer-2)
}

// Exp is spx_exp (math_approx.h:265): e^x for x in Q11, in Q16, saturating
// to 0x7fffffff.
// Within 39 units for x < 0, and 0.122% of the true value for x >= 0.
func Exp(x int16) int32 {
	if x > 21290 {
		return 0x7fffffff
	}
	if x < -21290 {
		return 0
	}
	return exp2(int16(mult16x16P14(23637, x)))
}

// atan01 is spx_atan01 (math_approx.h:279).
func atan01(x int16) int16 {
	const m1, m2, m3, m4 = 32767, -21, -11943, 4936
	t := Add32(m3, Mult16x16P15(m4, x))
	t = Add32(m2, Mult16x16P15(x, int16(t)))
	t = Add32(m1, Mult16x16P15(x, int16(t)))
	return int16(Mult16x16P15(x, int16(t)))
}

// Atan is spx_atan (math_approx.h:290): arctangent of a non-negative Q15
// value, in Q14. Negative inputs give what upstream gives, not atan.
// Within 5 units of the true value for x >= 0; not a substitute for math.Atan.
func Atan(x int32) int16 {
	if x <= 32767 {
		return shr16(atan01(int16(x)), 1)
	}
	e := int(ILog2(uint32(x)))
	if e >= 29 {
		return 25736
	}
	x = int32(Div32x16(Shl32(32767, uint(29-e)), Extract16(Shr32(x, uint(e-14)))))
	return int16(Sub16(25736, shr16(atan01(int16(x)), 1)))
}
