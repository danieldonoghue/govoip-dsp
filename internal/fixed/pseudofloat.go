// Derived from SpeexDSP 1.2.1 (https://github.com/xiph/speexdsp, commit
// 1b28a0f61bc31162979e1f26f3981fc3637095c8): libspeexdsp/pseudofloat.h.
//
// Copyright (C) 2005 Jean-Marc Valin
//
// Use of this source is governed by the BSD-3-Clause licence in
// LICENSES/SpeexDSP-COPYING. Changes: ported to Go; FLOAT_DIVU does not
// print its warning.

package fixed

// Float is spx_float_t, the pseudo-float of the fixed-point build: the value
// M * 2^E. Operations normalise |M| into [16384, 32767] where upstream does.
type Float struct {
	M, E int16
}

// FloatZero and FloatOne are FLOAT_ZERO and FLOAT_ONE. Never assign to them.
var (
	FloatZero = Float{0, 0}
	FloatOne  = Float{16384, -14}
)

// normalize is the mantissa fix-up shared by FLOAT_ADD, FLOAT_SUB and
// FLOAT_MULT.
func normalize(r Float) Float {
	if r.M > 0 {
		if r.M < 16384 {
			r.M <<= 1
			r.E--
		}
	} else if r.M > -16384 {
		r.M <<= 1
		r.E--
	}
	return r
}

// PseudoFloat is PSEUDOFLOAT: conversion from int32.
func PseudoFloat(x int32) Float {
	if x == 0 {
		return Float{}
	}
	neg := x < 0
	if neg {
		x = -x
	}
	e := int(ILog2(uint32(Abs32(x)))) - 14
	x = VShr32(x, e)
	if neg {
		return Float{M: int16(-x), E: int16(e)}
	}
	return Float{M: int16(x), E: int16(e)}
}

// alignShift is the MIN(15, ea-eb+1) shift of the smaller operand.
func alignShift(ea, eb int16) uint {
	return uint(min(15, int(ea)-int(eb)+1))
}

// FloatAdd is FLOAT_ADD.
func FloatAdd(a, b Float) Float {
	if a.M == 0 {
		return b
	}
	if b.M == 0 {
		return a
	}
	if a.E > b.E {
		return normalize(Float{M: a.M>>1 + b.M>>alignShift(a.E, b.E), E: a.E + 1})
	}
	return normalize(Float{M: b.M>>1 + a.M>>alignShift(b.E, a.E), E: b.E + 1})
}

// FloatSub is FLOAT_SUB. As upstream, a zero a returns b, not -b.
func FloatSub(a, b Float) Float {
	if a.M == 0 {
		return b
	}
	if b.M == 0 {
		return a
	}
	if a.E > b.E {
		return normalize(Float{M: a.M>>1 - b.M>>alignShift(a.E, b.E), E: a.E + 1})
	}
	return normalize(Float{M: a.M>>alignShift(b.E, a.E) - b.M>>1, E: b.E + 1})
}

// FloatLT is FLOAT_LT: a < b.
func FloatLT(a, b Float) bool {
	if a.M == 0 {
		return b.M > 0
	}
	if b.M == 0 {
		return a.M < 0
	}
	if a.E > b.E {
		return a.M>>1 < b.M>>alignShift(a.E, b.E)
	}
	return b.M>>1 > a.M>>alignShift(b.E, a.E)
}

// FloatGT is FLOAT_GT: a > b.
func FloatGT(a, b Float) bool { return FloatLT(b, a) }

// FloatMult is FLOAT_MULT.
func FloatMult(a, b Float) Float {
	return normalize(FloatAMult(a, b))
}

// FloatAMult is FLOAT_AMULT: FloatMult without normalisation.
func FloatAMult(a, b Float) Float {
	return Float{M: int16((int32(a.M) * int32(b.M)) >> 15), E: a.E + b.E + 15}
}

// FloatShl is FLOAT_SHL: multiplication by 2^b.
func FloatShl(a Float, b int) Float {
	return Float{M: a.M, E: int16(int(a.E) + b)}
}

// FloatExtract16 is FLOAT_EXTRACT16: rounding conversion to int16. Defined
// for E in [-31, 31].
func FloatExtract16(a Float) int16 {
	return int16(FloatExtract32(a))
}

// FloatExtract32 is FLOAT_EXTRACT32: rounding conversion to int32. Defined
// for E in [-31, 31].
func FloatExtract32(a Float) int32 {
	if a.E < 0 {
		s := uint(-int(a.E))
		return (int32(a.M) + int32(1)<<(s-1)) >> s
	}
	return int32(a.M) << uint(a.E)
}

// FloatMul32 is FLOAT_MUL32: a*b as an int32. Defined for E in [-46, 16].
func FloatMul32(a Float, b int32) int32 {
	return VShr32(Mult16x32Q15(a.M, b), -int(a.E)-15)
}

// FloatMul32U is FLOAT_MUL32U: the product of two int32 as a Float.
func FloatMul32U(a, b int32) Float {
	if a == 0 || b == 0 {
		return FloatZero
	}
	e1 := int(ILog2(uint32(Abs32(a))))
	a = VShr32(a, e1-14)
	e2 := int(ILog2(uint32(Abs32(b))))
	b = VShr32(b, e2-14)
	return Float{M: int16(Mult16x16Q15(int16(a), int16(b))), E: int16(e1 + e2 - 13)}
}

// FloatDiv32Float is FLOAT_DIV32_FLOAT: a/b. b.M must be positive; it
// panics for b.M = 0, and upstream forbids a negative b.
func FloatDiv32Float(a int32, b Float) Float {
	if a == 0 {
		return FloatZero
	}
	e := int(ILog2(uint32(Abs32(a)))) - int(ILog2(uint32(int32(b.M)-1))) - 15
	a = VShr32(a, e)
	if Abs32(a) >= Shl32(int32(b.M)-1, 15) {
		a >>= 1
		e++
	}
	return Float{M: Div32x16(a, b.M), E: int16(e - int(b.E))}
}

// FloatDiv32 is FLOAT_DIV32: a/b. b must be positive; it panics for b = 0,
// and upstream forbids a negative b.
func FloatDiv32(a, b int32) Float {
	if a == 0 {
		return FloatZero
	}
	e0 := 0
	if b > 32767 {
		e0 = int(ILog2(uint32(b))) - 14
		b = VShr32(b, e0)
		e0 = -e0
	}
	e := int(ILog2(uint32(Abs32(a)))) - int(ILog2(uint32(b-1))) - 15
	a = VShr32(a, e)
	if Abs32(a) >= Shl32(b-1, 15) {
		a >>= 1
		e++
	}
	e += e0
	return Float{M: Div32x16(a, int16(b)), E: int16(e)}
}

// FloatDivU is FLOAT_DIVU: a/b for positive b.M. For b.M <= 0 it returns
// FloatOne, as upstream does, without upstream's warning on stderr.
func FloatDivU(a, b Float) Float {
	if b.M <= 0 {
		return FloatOne
	}
	e := 0
	num := int32(a.M)
	a.M = int16(Abs16(a.M))
	// At most 15 iterations: a.M <= 32767 and b.M >= 1.
	for a.M >= b.M {
		e++
		a.M >>= 1
	}
	num <<= uint(15 - e)
	return Float{M: Div32x16(num, b.M), E: int16(int(a.E) - int(b.E) - 15 + e)}
}

// FloatSqrt is FLOAT_SQRT.
func FloatSqrt(a Float) Float {
	m := Shl32(int32(a.M), 14)
	e := a.E - 14
	if e&1 != 0 {
		e--
		m <<= 1
	}
	return Float{M: Sqrt(m), E: e >> 1}
}
