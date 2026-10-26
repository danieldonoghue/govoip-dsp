package fixed

import "testing"

// Inputs cycle through a table so the compiler cannot fold the calls, and
// results go to a package-level sink so it cannot drop them.

var benchIn = func() []int32 {
	v := make([]int32, 1024)
	x := uint32(12345)
	for i := range v {
		x = x*1664525 + 1013904223
		v[i] = int32(x >> 1)
	}
	return v
}()

var (
	sink16 int16
	sinkF  Float
)

func BenchmarkSqrt(b *testing.B) {
	for i := 0; b.Loop(); i++ {
		sink16 = Sqrt(benchIn[i&1023])
	}
}

func BenchmarkExp(b *testing.B) {
	for i := 0; b.Loop(); i++ {
		sink32 = Exp(int16(benchIn[i&1023]))
	}
}

func BenchmarkAtan(b *testing.B) {
	for i := 0; b.Loop(); i++ {
		sink16 = Atan(benchIn[i&1023] >> 12)
	}
}

func BenchmarkCos(b *testing.B) {
	for i := 0; b.Loop(); i++ {
		sink16 = Cos(int16(benchIn[i&1023] % 25736))
	}
}

func BenchmarkCosNorm(b *testing.B) {
	for i := 0; b.Loop(); i++ {
		sink16 = CosNorm(benchIn[i&1023])
	}
}

func BenchmarkMult16x32Q15(b *testing.B) {
	for i := 0; b.Loop(); i++ {
		sink32 = Mult16x32Q15(int16(benchIn[i&1023]), benchIn[(i+1)&1023])
	}
}

func BenchmarkPseudoFloat(b *testing.B) {
	for i := 0; b.Loop(); i++ {
		sinkF = PseudoFloat(benchIn[i&1023])
	}
}

func BenchmarkFloatAdd(b *testing.B) {
	for i := 0; b.Loop(); i++ {
		sinkF = FloatAdd(Float{int16(benchIn[i&1023]), 3}, Float{int16(benchIn[(i+1)&1023]), -2})
	}
}

func BenchmarkFloatMul32U(b *testing.B) {
	for i := 0; b.Loop(); i++ {
		sinkF = FloatMul32U(benchIn[i&1023], benchIn[(i+1)&1023])
	}
}

func BenchmarkFloatDiv32(b *testing.B) {
	for i := 0; b.Loop(); i++ {
		sinkF = FloatDiv32(benchIn[i&1023], benchIn[(i+1)&1023]|1)
	}
}

func BenchmarkFloatDiv32Float(b *testing.B) {
	for i := 0; b.Loop(); i++ {
		sinkF = FloatDiv32Float(benchIn[i&1023], Float{int16(benchIn[(i+1)&1023]&0x3fff) | 0x4000, -3})
	}
}

func BenchmarkFloatDivU(b *testing.B) {
	for i := 0; b.Loop(); i++ {
		sinkF = FloatDivU(Float{int16(benchIn[i&1023]), 2}, Float{int16(benchIn[(i+1)&1023]&0x3fff) | 0x4000, -3})
	}
}

func BenchmarkFloatSqrt(b *testing.B) {
	for i := 0; b.Loop(); i++ {
		sinkF = FloatSqrt(Float{int16(benchIn[i&1023]&0x3fff) | 0x4000, int16(i & 15)})
	}
}
