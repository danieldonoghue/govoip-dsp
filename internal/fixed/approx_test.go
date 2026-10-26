package fixed

import (
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"regexp"
	"strconv"
	"testing"
)

// worst is the largest error seen and the input that gave it.
type worst struct {
	err float64
	x   int64
}

func (w *worst) add(x int64, err float64) {
	if err > w.err {
		*w = worst{err, x}
	}
}

// unitErr is the error of got in output units, against the correctly rounded
// true value.
func unitErr(got int64, want float64) float64 {
	return math.Abs(float64(got) - math.Round(want))
}

func sqrtError() worst {
	var w worst
	add := func(x int64) {
		w.add(x, unitErr(int64(Sqrt(int32(x))), math.Sqrt(float64(x))))
	}
	for x := int64(0); x < 1<<16; x++ {
		add(x)
	}
	// From x = 4^8, Sqrt uses only the top 14 bits of x, so its result is
	// constant on each run of x sharing them, whose ends bound its error.
	for j := uint(8); j <= 14; j++ {
		s := 2 * (j - 6)
		for q := int64(4096); q < 16384; q++ {
			add(q << s)
			add((q+1)<<s - 1)
		}
	}
	return w
}

func cosError() worst {
	var w worst
	for x := int64(0); x <= 25736; x++ {
		w.add(x, unitErr(int64(Cos(int16(x))), 8192*math.Cos(float64(x)/8192)))
	}
	return w
}

// cosNormError covers every int32: CosNorm and the true value both have
// period 2^17.
func cosNormError() worst {
	var w worst
	for x := int64(0); x < 1<<17; x++ {
		want := 32768 * math.Cos(math.Pi/2*float64(x)/32768)
		w.add(x, unitErr(int64(CosNorm(int32(x))), want))
	}
	return w
}

// expError returns the error in units for x < 0, and the relative error
// against the rounded true value for x in [0, 21290].
func expError() (neg, pos worst) {
	for x := int64(math.MinInt16); x <= 21290; x++ {
		got, want := int64(Exp(int16(x))), 65536*math.Exp(float64(x)/2048)
		if x < 0 {
			neg.add(x, unitErr(got, want))
			continue
		}
		pos.add(x, math.Abs(float64(got)-math.Round(want))/math.Round(want))
	}
	return neg, pos
}

func TestExpSaturation(t *testing.T) {
	for x := 21291; x <= math.MaxInt16; x++ {
		if got := Exp(int16(x)); got != math.MaxInt32 {
			t.Fatalf("Exp(%d) = %d, want 2^31-1", x, got)
		}
	}
	// The doc says Exp saturates three steps early.
	if 65536*math.Exp(21293.0/2048) >= math.MaxInt32 ||
		65536*math.Exp(21294.0/2048) < math.MaxInt32 {
		t.Error("the true value does not first exceed 2^31-1 at x = 21294")
	}
}

func atanError() worst {
	var w worst
	add := func(x int64) {
		w.add(x, unitErr(int64(Atan(int32(x))), 16384*math.Atan(float64(x)/32768)))
	}
	for x := int64(0); x <= 32767; x++ {
		add(x)
	}
	// Above 32767, Atan uses only the top 15 bits of x, and from 2^29 it is
	// constant, so the ends of each run of x sharing them bound its error.
	for e := uint(15); e <= 28; e++ {
		for q := int64(16384); q <= 32767; q++ {
			add(q << (e - 14))
			add((q+1)<<(e-14) - 1)
		}
	}
	add(1 << 29)
	add(math.MaxInt32)
	return w
}

// docFigures returns, for each function in approx.go, the numbers in its doc
// comment that match re.
func docFigures(t *testing.T, re *regexp.Regexp) map[string][]float64 {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "approx.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	figs := map[string][]float64{}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Doc == nil {
			continue
		}
		for _, m := range re.FindAllStringSubmatch(fd.Doc.Text(), -1) {
			v, err := strconv.ParseFloat(m[1], 64)
			if err != nil {
				t.Fatal(err)
			}
			figs[fd.Name.Name] = append(figs[fd.Name.Name], v)
		}
	}
	return figs
}

// TestApproximationError checks that the maximum error stated in each
// approximation's doc is the measured maximum (acceptance criterion 7).
func TestApproximationError(t *testing.T) {
	units := docFigures(t, regexp.MustCompile(`[Ww]ithin (\d+) units`))
	percent := docFigures(t, regexp.MustCompile(`(\d+\.\d+)%`))
	expNeg, expPos := expError()
	for _, c := range []struct {
		name   string
		stated []float64
		got    worst
		step   float64 // resolution of the stated figure
	}{
		{"Sqrt", units["Sqrt"], sqrtError(), 1},
		{"Cos", units["Cos"], cosError(), 1},
		{"CosNorm", units["CosNorm"], cosNormError(), 1},
		{"Exp, x < 0", units["Exp"], expNeg, 1},
		{"Exp, x in [0, 21290]", percent["Exp"], worst{expPos.err * 100, expPos.x}, 0.001},
		{"Atan", units["Atan"], atanError(), 1},
	} {
		if len(c.stated) != 1 {
			t.Errorf("%s: doc states %v, want one figure", c.name, c.stated)
			continue
		}
		s := c.stated[0]
		if c.got.err > s || c.got.err <= s-c.step {
			t.Errorf("%s: doc states %g, measured maximum %g at x = %d",
				c.name, s, c.got.err, c.got.x)
		}
	}
}
