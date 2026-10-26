package fixed

import (
	"os/exec"
	"regexp"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

// The masked variants are checked against the compiled reference over the
// undefined exponents by echo's TestReferencePseudoFloatMasked (fixtures in
// testdata/speexdsp/echo). Here: equality with the unmasked functions where
// those are defined, and an independent model of 32-bit register shifts.

var maskedM = []int16{-32768, -32767, -16385, -16384, -16383, -256, -2, -1, 0, 1, 2, 255, 256, 16383, 16384, 16385, 32766, 32767}

// hwShl and hwSar model 32-bit register shifts: the count modulo 32.
func hwShl(a int32, s int) int32 { return int32(uint32(a) << (uint(s) % 32)) }
func hwSar(a int32, s int) int32 { return a >> (uint(s) % 32) }

func TestFloatExtractMasked(t *testing.T) {
	for _, m := range maskedM {
		for e := -32768; e <= 32767; e++ {
			if e > -100 && e < 100 || e%997 == 0 || e == -32768 || e == 32767 {
				a := Float{m, int16(e)}
				var want int32
				if e < 0 {
					want = hwSar(int32(m)+hwShl(1, -e-1), -e)
				} else {
					want = hwShl(int32(m), e)
				}
				if got := FloatExtract32Masked(a); got != want {
					t.Fatalf("FloatExtract32Masked(%v) = %d, want %d", a, got, want)
				}
				if got := FloatExtract16Masked(a); got != int16(want) {
					t.Fatalf("FloatExtract16Masked(%v) = %d, want %d", a, got, int16(want))
				}
				if e >= -31 && e <= 31 {
					if FloatExtract32Masked(a) != FloatExtract32(a) || FloatExtract16Masked(a) != FloatExtract16(a) {
						t.Fatalf("masked and unmasked extract differ at %v", a)
					}
				}
			}
		}
	}
}

func TestFloatDiv32FloatMasked(t *testing.T) {
	r := testutil.SplitMix64{State: testutil.Seed("float_div32_float_masked")}
	for i := 0; i < 1000000; i++ {
		a := int32(r.Next())
		if i%2 == 1 {
			a >>= r.Next() % 32
		}
		b := Float{int16(r.Next()), int16(int(r.Next()%100) - 50)}
		if b.M == 0 {
			continue
		}
		got := FloatDiv32FloatMasked(a, b)
		if b.M > 0 {
			if want := FloatDiv32Float(a, b); got != want {
				t.Fatalf("FloatDiv32FloatMasked(%d, %v) = %v, unmasked %v", a, b, got, want)
			}
			continue
		}
		// Negative b.M: the normalising shift can exceed 31.
		if a == 0 {
			if got != FloatZero {
				t.Fatalf("FloatDiv32FloatMasked(0, %v) = %v", b, got)
			}
			continue
		}
		e := int(ILog2(uint32(Abs32(a)))) - int(ILog2(uint32(int32(b.M)-1))) - 15
		x := hwSar(a, e)
		if e <= 0 {
			x = hwShl(a, -e)
		}
		if Abs32(x) >= (int32(b.M)-1)<<15 {
			x >>= 1
			e++
		}
		want := Float{int16(x / int32(b.M)), int16(e - int(b.E))}
		if got != want {
			t.Fatalf("FloatDiv32FloatMasked(%d, %v) = %v, want %v", a, b, got, want)
		}
	}
}

func TestMaskedInlining(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the package with -gcflags=-m")
	}
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go command not found")
	}
	out, err := exec.Command(gobin, "build", "-gcflags=-m", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go build -gcflags=-m: %v\n%s", err, out)
	}
	for _, name := range []string{"VShr32Masked", "FloatExtract16Masked", "FloatExtract32Masked"} {
		if !regexp.MustCompile(`(?m): can inline ` + name + `( |$)`).Match(out) {
			t.Errorf("%s does not inline", name)
		}
	}
}
