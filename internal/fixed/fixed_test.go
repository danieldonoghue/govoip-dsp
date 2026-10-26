package fixed

import (
	"math"
	"os/exec"
	"regexp"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

// Go-only policies, plus sanity checks that catch a driver and a port that
// agree on the wrong macro; the harness tests are in reference_test.go.

func TestWord2IntFloatNonFinite(t *testing.T) {
	nan32, nan64 := float32(math.NaN()), math.NaN()
	if got := Word2IntFloat32(nan32); got != 0 {
		t.Errorf("Word2IntFloat32(NaN) = %d, want 0", got)
	}
	if got := Word2IntFloat64(nan64); got != 0 {
		t.Errorf("Word2IntFloat64(NaN) = %d, want 0", got)
	}
	for _, tc := range []struct {
		x    float64
		want int16
	}{
		{math.Inf(1), 32767}, {math.Inf(-1), -32768},
		{32766.9, 32767}, {32766, 32766}, {-32767, -32767}, {-32767.1, -32768},
		{1.9, 1}, {-1.9, -1},
	} {
		if got := Word2IntFloat32(float32(tc.x)); got != tc.want {
			t.Errorf("Word2IntFloat32(%g) = %d, want %d", tc.x, got, tc.want)
		}
		if got := Word2IntFloat64(tc.x); got != tc.want {
			t.Errorf("Word2IntFloat64(%g) = %d, want %d", tc.x, got, tc.want)
		}
	}
}

func TestApproximationsTrackMath(t *testing.T) {
	for x := int32(1 << 10); x > 0 && x < 1<<30; x += x/97 + 1 {
		got, want := float64(Sqrt(x)), math.Sqrt(float64(x))
		if math.Abs(got-want) > 0.002*want+1 {
			t.Fatalf("Sqrt(%d) = %g, want about %g", x, got, want)
		}
	}
	for x := int16(0); x < 25736; x += 7 {
		got, want := float64(Cos(x))/8192, math.Cos(float64(x)/8192)
		if math.Abs(got-want) > 0.002 {
			t.Fatalf("Cos(%d) = %g, want about %g", x, got, want)
		}
	}
	for x := int32(-1 << 17); x < 1<<17; x += 13 {
		got, want := float64(CosNorm(x))/32767, math.Cos(math.Pi/2*float64(x)/32768)
		if math.Abs(got-want) > 0.001 {
			t.Fatalf("CosNorm(%d) = %g, want about %g", x, got, want)
		}
	}
	for x := int16(-20000); x < 20000; x += 11 {
		got, want := float64(Exp(x))/65536, math.Exp(float64(x)/2048)
		if math.Abs(got-want) > 0.005*want+2.0/65536 {
			t.Fatalf("Exp(%d) = %g, want about %g", x, got, want)
		}
	}
	for x := int32(0); x < 1<<24; x += x/50 + 1 {
		got, want := float64(Atan(x))/16384, math.Atan(float64(x)/32768)
		if math.Abs(got-want) > 0.002 {
			t.Fatalf("Atan(%d) = %g, want about %g", x, got, want)
		}
	}
}

func value(f Float) float64 { return float64(f.M) * math.Pow(2, float64(f.E)) }

func TestPseudoFloatTracksMath(t *testing.T) {
	r := testutil.SplitMix64{State: 3}
	for i := 0; i < 100000; i++ {
		a := int32(r.Next()) >> uint(r.Intn(31))
		b := int32(r.Next()) >> uint(r.Intn(31))
		if a == 0 || b == 0 {
			continue
		}
		fa, fb := PseudoFloat(a), PseudoFloat(b)
		if d := math.Abs(value(fa)-float64(a)) / math.Abs(float64(a)); d > 1.0/16384 {
			t.Fatalf("PseudoFloat(%d) = %v, relative error %g", a, fa, d)
		}
		prod := float64(a) * float64(b)
		if d := math.Abs(value(FloatMult(fa, fb))-prod) / math.Abs(prod); d > 4.0/16384 {
			t.Fatalf("FloatMult(%d, %d) relative error %g", a, b, d)
		}
		if d := math.Abs(value(FloatMul32U(a, b))-prod) / math.Abs(prod); d > 4.0/16384 {
			t.Fatalf("FloatMul32U(%d, %d) relative error %g", a, b, d)
		}
		sum := float64(a) + float64(b)
		if d := math.Abs(value(FloatAdd(fa, fb)) - sum); d > math.Max(math.Abs(float64(a)), math.Abs(float64(b)))*4.0/16384 {
			t.Fatalf("FloatAdd(%d, %d) error %g", a, b, d)
		}
		if FloatLT(fa, fb) != (value(fa) < value(fb)) && value(fa) != value(fb) {
			t.Fatalf("FloatLT(%v, %v) = %v", fa, fb, FloatLT(fa, fb))
		}
		if b > 0 {
			q := float64(a) / float64(b)
			if d := math.Abs(value(FloatDiv32(a, b))-q) / math.Abs(q); d > 4.0/16384 {
				t.Fatalf("FloatDiv32(%d, %d) relative error %g", a, b, d)
			}
		}
	}
	if FloatExtract32(FloatOne) != 1 || FloatExtract16(FloatShl(FloatOne, 3)) != 8 {
		t.Error("FloatOne does not extract to 1")
	}
	if got := FloatDivU(FloatOne, Float{M: 0, E: 0}); got != FloatOne {
		t.Errorf("FloatDivU by zero mantissa = %v, want FloatOne", got)
	}
}

func TestDivisionByZeroPanics(t *testing.T) {
	for name, f := range map[string]func(){
		"Div32":     func() { sink32 = Div32(1, sink32) },
		"Div32x16":  func() { sink32 = int32(Div32x16(1, int16(sink32))) },
		"PDiv32x16": func() { sink32 = int32(PDiv32x16(1, int16(sink32))) },
		"PDiv32":    func() { sink32 = PDiv32(1, sink32) },
	} {
		sink32 = 0
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s(1, 0) did not panic", name)
				}
			}()
			f()
		}()
	}
}

var sink32 int32

func TestNoAllocs(t *testing.T) {
	testutil.AssertNoAllocs(t, 100, func() {
		f := FloatAdd(PseudoFloat(sink32+12345), FloatDiv32(77, 3))
		sink32 = int32(Sqrt(sink32+1000)) + Exp(100) + int32(Atan(5000)) + FloatExtract32(FloatSqrt(f))
	})
}

// mustInline lists the functions criterion 5 of docs/acceptance/fixed.md
// requires to inline.
var mustInline = []string{
	"Abs16", "Abs32", "Min16", "Max16", "Min32", "Max32", "Word2Int",
	"Word2IntFloat32", "Word2IntFloat64", "QConst16", "QConst32", "Neg16",
	"Neg32", "Extract16", "Extend32", "shr16", "Shl16", "Shr32", "Shl32",
	"PShr16", "PShr32", "VShr32", "Saturate32", "Saturate32PShr", "Add16",
	"Sub16", "Add32", "Sub32", "Mult16x16", "Mac16x16", "Mult16x32Q15",
	"Mult16x32P15", "Mac16x32Q15", "Mult16x16Q14", "Mult16x16Q15",
	"mult16x16P13", "mult16x16P14", "Mult16x16P15", "Div32x16", "PDiv32x16",
	"Div32", "PDiv32",
	"FloatLT", "FloatMult", "FloatAMult", "FloatShl", "FloatExtract16",
	"FloatExtract32", "FloatMul32",
}

func TestInlining(t *testing.T) {
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
	for _, name := range mustInline {
		re := regexp.MustCompile(`(?m): can inline ` + name + `( |$)`)
		if !re.Match(out) {
			t.Errorf("%s does not inline", name)
		}
	}
}
