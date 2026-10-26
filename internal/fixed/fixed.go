// Derived from SpeexDSP 1.2.1 (https://github.com/xiph/speexdsp, commit
// 1b28a0f61bc31162979e1f26f3981fc3637095c8): libspeexdsp/arch.h and
// libspeexdsp/fixed_generic.h.
//
// Copyright (C) 2003 Jean-Marc Valin
//
// Use of this source is governed by the BSD-3-Clause licence in
// LICENSES/SpeexDSP-COPYING. Changes: ported from C macros to Go functions.

package fixed

// Q15One is Q15ONE and Q15_ONE, 1.0 in Q15 (arch.h:90, arch.h:109).
const Q15One = 32767

// Abs16 is ABS16 on an int16. The C result is int, so -32768 gives 32768.
func Abs16(x int16) int32 {
	if x < 0 {
		return -int32(x)
	}
	return int32(x)
}

// Abs32 is ABS32, and ABS on int32 operands. -2^31 wraps to itself.
func Abs32(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}

// Min16 is MIN16 on two int16 operands.
func Min16(a, b int16) int16 { return min(a, b) }

// Max16 is MAX16 on two int16 operands.
func Max16(a, b int16) int16 { return max(a, b) }

// Min32 is MIN32, and MIN16 where an operand is wider than int16.
func Min32(a, b int32) int32 { return min(a, b) }

// Max32 is MAX32, and MAX16 where an operand is wider than int16.
func Max32(a, b int32) int32 { return max(a, b) }

// Word2Int is WORD2INT on an integer: saturation to int16.
func Word2Int(x int32) int16 {
	if x < -32767 {
		return -32768
	}
	if x > 32766 {
		return 32767
	}
	return int16(x)
}

// Word2IntFloat32 is WORD2INT on a float then the cast to spx_word16_t
// (resample.c:1003). NaN gives 0, where C is undefined.
func Word2IntFloat32(x float32) int16 {
	if x != x {
		return 0
	}
	if x < -32767 {
		return -32768
	}
	if x > 32766 {
		return 32767
	}
	return int16(x)
}

// Word2IntFloat64 is Word2IntFloat32 for a double argument (resample.c:280).
func Word2IntFloat64(x float64) int16 {
	if x != x {
		return 0
	}
	if x < -32767 {
		return -32768
	}
	if x > 32766 {
		return 32767
	}
	return int16(x)
}

// QConst16 is QCONST16; pass float64(float32(c)) for an f-suffixed literal.
// Out-of-range results, undefined in C, saturate (a Go-only choice).
func QConst16(x float64, bits uint) int16 {
	v := 0.5 + float64(x*float64(int32(1)<<bits))
	if v >= 32768 {
		return 32767
	}
	if v <= -32769 {
		return -32768
	}
	return int16(v)
}

// QConst32 is QCONST32, saturating like QConst16 where C is undefined.
func QConst32(x float64, bits uint) int32 {
	v := 0.5 + float64(x*float64(int32(1)<<bits))
	if v >= 2147483648 {
		return 2147483647
	}
	if v <= -2147483649 {
		return -2147483648
	}
	return int32(v)
}

// Neg16 is NEG16 on an int16. The C result is int, so -32768 gives 32768.
func Neg16(x int16) int32 { return -int32(x) }

// Neg32 is NEG32.
func Neg32(x int32) int32 { return -x }

// Extract16 is EXTRACT16: truncation to int16.
func Extract16(x int32) int16 { return int16(x) }

// Extend32 is EXTEND32 on an int16.
func Extend32(x int16) int32 { return int32(x) }

// shr16 is SHR16 on an int16; the int result always fits in int16.
func shr16(a int16, shift uint) int16 { return a >> shift }

// Shl16 is SHL16 on an int16. The C result is int and is not truncated.
func Shl16(a int16, shift uint) int32 { return int32(a) << shift }

// Shr32 is SHR32: arithmetic shift right.
func Shr32(a int32, shift uint) int32 { return a >> shift }

// Shl32 is SHL32.
func Shl32(a int32, shift uint) int32 { return a << shift }

// PShr16 is PSHR16, shift right with rounding, on an int16. The int result
// always fits in int16.
func PShr16(a int16, shift uint) int16 {
	return int16((int32(a) + ((int32(1) << shift) >> 1)) >> shift)
}

// PShr32 is PSHR32, shift right with rounding.
func PShr32(a int32, shift uint) int32 {
	return (a + ((int32(1) << shift) >> 1)) >> shift
}

// VShr32 is VSHR32: shift right for positive shift, left otherwise.
func VShr32(a int32, shift int) int32 {
	if shift > 0 {
		return a >> uint(shift)
	}
	return a << uint(-shift)
}

// Saturate32 is SATURATE32: clamp x to [-a, a].
func Saturate32(x, a int32) int32 {
	if x > a {
		return a
	}
	if x < -a {
		return -a
	}
	return x
}

// Saturate32PShr is SATURATE32PSHR: PShr32(x, shift) clamped to [-a, a],
// with the comparison made before the shift.
func Saturate32PShr(x int32, shift uint, a int32) int32 {
	if x >= a<<shift {
		return a
	}
	if x <= -(a << shift) {
		return -a
	}
	return PShr32(x, shift)
}

// Add16 is ADD16: the sum truncated to int16.
func Add16(a, b int16) int16 { return a + b }

// Sub16 is SUB16. The C result is int and is not truncated.
func Sub16(a, b int16) int32 { return int32(a) - int32(b) }

// Add32 is ADD32.
func Add32(a, b int32) int32 { return a + b }

// Sub32 is SUB32.
func Sub32(a, b int32) int32 { return a - b }

// Mult16x16 is MULT16_16.
func Mult16x16(a, b int16) int32 { return int32(a) * int32(b) }

// Mac16x16 is MAC16_16: c + a*b.
func Mac16x16(c int32, a, b int16) int32 { return c + int32(a)*int32(b) }

// Mult16x32Q15 is MULT16_32_Q15: (a*b) >> 15, computed in 32 bits from the
// high and low parts of b.
func Mult16x32Q15(a int16, b int32) int32 {
	return int32(a)*(b>>15) + (int32(a)*(b&0x7fff))>>15
}

// Mult16x32P15 is MULT16_32_P15: Mult16x32Q15 with the low part rounded.
func Mult16x32P15(a int16, b int32) int32 {
	return int32(a)*(b>>15) + (int32(a)*(b&0x7fff)+16384)>>15
}

// Mac16x32Q15 is MAC16_32_Q15: c + Mult16x32Q15(a, b).
func Mac16x32Q15(c int32, a int16, b int32) int32 { return c + Mult16x32Q15(a, b) }

// Mult16x16Q14 is MULT16_16_Q14.
func Mult16x16Q14(a, b int16) int32 { return (int32(a) * int32(b)) >> 14 }

// Mult16x16Q15 is MULT16_16_Q15.
func Mult16x16Q15(a, b int16) int32 { return (int32(a) * int32(b)) >> 15 }

// mult16x16P13 is MULT16_16_P13.
func mult16x16P13(a, b int16) int32 { return (4096 + int32(a)*int32(b)) >> 13 }

// mult16x16P14 is MULT16_16_P14.
func mult16x16P14(a, b int16) int32 { return (8192 + int32(a)*int32(b)) >> 14 }

// Mult16x16P15 is MULT16_16_P15.
func Mult16x16P15(a, b int16) int32 { return (16384 + int32(a)*int32(b)) >> 15 }

// Div32x16 is DIV32_16: a/b truncated toward zero, then to int16. It panics
// if b is 0; C is undefined there.
func Div32x16(a int32, b int16) int16 { return int16(a / int32(b)) }

// PDiv32x16 is PDIV32_16: (a + b/2)/b with b/2 an arithmetic shift, then
// truncated to int16. It panics if b is 0.
func PDiv32x16(a int32, b int16) int16 { return int16((a + int32(b>>1)) / int32(b)) }

// Div32 is DIV32. It panics if b is 0.
func Div32(a, b int32) int32 { return a / b }

// PDiv32 is PDIV32: (a + int16(b)>>1)/b. The rounding term uses b truncated
// to int16, as upstream does. It panics if b is 0.
func PDiv32(a, b int32) int32 { return (a + int32(int16(b)>>1)) / b }
