// Derived from SpeexDSP 1.2.1 (https://github.com/xiph/speexdsp, commit
// 1b28a0f61bc31162979e1f26f3981fc3637095c8): libspeexdsp/pseudofloat.h and
// libspeexdsp/fixed_generic.h.
//
// Copyright (C) 2005 Jean-Marc Valin
// Copyright (C) 2003 Jean-Marc Valin
//
// Use of this source is governed by the BSD-3-Clause licence in
// LICENSES/SpeexDSP-COPYING. Changes: ported to Go with shift counts taken
// modulo 32.

package fixed

// Masked variants take shift counts modulo 32, as the scalar LSLV and ASRV
// the reference build uses in mdf.c do; elsewhere they equal the originals.

// VShr32Masked is VSHR32 with the count taken modulo 32.
func VShr32Masked(a int32, shift int) int32 {
	if shift > 0 {
		return a >> (uint(shift) & 31)
	}
	return a << (uint(-shift) & 31)
}

// FloatExtract16Masked is FloatExtract16 for every exponent.
func FloatExtract16Masked(a Float) int16 {
	return int16(FloatExtract32Masked(a))
}

// FloatExtract32Masked is FloatExtract32 for every exponent.
func FloatExtract32Masked(a Float) int32 {
	if a.E < 0 {
		s := uint(-int(a.E))
		return (int32(a.M) + int32(1)<<((s-1)&31)) >> (s & 31)
	}
	return int32(a.M) << (uint(a.E) & 31)
}

// FloatDiv32FloatMasked is FloatDiv32Float for every b.M other than 0,
// including the negative b.M that upstream forbids. It panics for b.M = 0.
func FloatDiv32FloatMasked(a int32, b Float) Float {
	if a == 0 {
		return FloatZero
	}
	e := int(ILog2(uint32(Abs32(a)))) - int(ILog2(uint32(int32(b.M)-1))) - 15
	a = VShr32Masked(a, e)
	if Abs32(a) >= Shl32(int32(b.M)-1, 15) {
		a >>= 1
		e++
	}
	return Float{M: Div32x16(a, b.M), E: int16(e - int(b.E))}
}
