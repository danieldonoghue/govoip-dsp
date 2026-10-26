// Derived from SpeexDSP 1.2.1 (https://github.com/xiph/speexdsp, commit
// 1b28a0f61bc31162979e1f26f3981fc3637095c8): libspeexdsp/fixed_generic.h.
//
// Copyright (C) 2003 Jean-Marc Valin
//
// Use of this source is governed by the BSD-3-Clause licence in
// LICENSES/SpeexDSP-COPYING. Changes: ported from C macros to Go functions,
// with a defined result for a zero divisor.

package fixed

// Div32x16OrZero is Div32x16, except that b = 0 gives 0 instead of a panic:
// what AArch64 SDIV, on the canonical reference, yields where C is undefined.
func Div32x16OrZero(a int32, b int16) int16 {
	if b == 0 {
		return 0
	}
	return int16(a / int32(b))
}

// PDiv32x16OrZero is PDiv32x16, except that b = 0 gives 0, as
// Div32x16OrZero does.
func PDiv32x16OrZero(a int32, b int16) int16 {
	if b == 0 {
		return 0
	}
	return int16((a + int32(b>>1)) / int32(b))
}
