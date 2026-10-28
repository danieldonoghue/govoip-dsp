package testutil

import (
	"errors"
	"strings"
	"testing"
)

var errZeroConfig = errors.New("zero: invalid configuration")

// zeroGood is a type whose zero value follows the rules.
type zeroGood struct {
	n   int
	buf []int16
}

func (z *zeroGood) Len() int { return z.n }
func (z *zeroGood) Process(in, out []int16, flag bool) error {
	if z.buf == nil {
		return errZeroConfig
	}
	return nil
}
func (z *zeroGood) SetN(n int)          { z.n = n }
func (z *zeroGood) Floats(x []float32)  {}
func (z *zeroGood) Narrow(a int8) int32 { return 0 }

type zeroPanics struct{ buf []int16 }

func (z *zeroPanics) Process(in []int16) error { _ = z.buf[len(in)]; return errZeroConfig }

type zeroWrongError struct{}

func (z *zeroWrongError) Process(in []int16) error { return nil }

type zeroNonzero struct{}

func (z *zeroNonzero) Len() int { return 1 }

type zeroWrites struct{}

func (z *zeroWrites) Read(p []int32) int {
	if len(p) > 0 {
		p[0] = 1
	}
	return 0
}

// zeroCopies copies one argument into another, as Capture does without echo.
type zeroCopies struct{}

func (z *zeroCopies) Process(in, out []int16) error {
	copy(out, in)
	return errZeroConfig
}

type zeroChanges struct{ v bool }

func (z *zeroChanges) Reset() { z.v = true }

// zeroNoMethods is a type with no methods, such as a Config passed by mistake.
type zeroNoMethods struct{}

type zeroUnsupported struct{}

func (z *zeroUnsupported) Name(s string) {}

func TestCheckZeroValuePasses(t *testing.T) {
	if r := run(t, func(tb testing.TB) { CheckZeroValue[zeroGood](tb, errZeroConfig, "SetN") }); len(r.errs) > 0 {
		t.Errorf("unexpected failures: %s", r.text())
	}
}

func TestCheckZeroValueFails(t *testing.T) {
	for _, c := range []struct {
		name  string
		check func(testing.TB)
		want  string
	}{
		{"panic", func(tb testing.TB) { CheckZeroValue[zeroPanics](tb, errZeroConfig) }, "zeroPanics.Process(nil) panics"},
		{"error", func(tb testing.TB) { CheckZeroValue[zeroWrongError](tb, errZeroConfig) }, "returns error <nil>"},
		{"result", func(tb testing.TB) { CheckZeroValue[zeroNonzero](tb, errZeroConfig) }, "returns 1 as result 0"},
		{"write", func(tb testing.TB) { CheckZeroValue[zeroWrites](tb, errZeroConfig) }, "Read(len 1) writes into an argument"},
		{"copy", func(tb testing.TB) { CheckZeroValue[zeroCopies](tb, errZeroConfig) }, "Process(len 1, len 1) writes into an argument"},
		{"change", func(tb testing.TB) { CheckZeroValue[zeroChanges](tb, errZeroConfig) }, "Reset() changes the zero value"},
		{"store", func(tb testing.TB) { CheckZeroValue[zeroGood](tb, errZeroConfig) }, "SetN(-9223372036854775808) changes the zero value"},
		{"none", func(tb testing.TB) { CheckZeroValue[zeroNoMethods](tb, errZeroConfig) }, "zeroNoMethods has no exported methods to call"},
		{"type", func(tb testing.TB) { CheckZeroValue[zeroUnsupported](tb, errZeroConfig) }, "no zero-value arguments for parameter type string"},
	} {
		r := run(t, c.check)
		if len(r.errs) != 1 || !strings.Contains(r.text(), c.want) {
			t.Errorf("%s: got %q, want one failure containing %q", c.name, r.text(), c.want)
		}
	}
}

// The arguments cover each length and sign, and every combination of them.
func TestCallZeroValueArguments(t *testing.T) {
	seen := map[string]int{}
	CallZeroValue[zeroGood](t, func(c ZeroCall) {
		seen[c.Method]++
		if c.Method == "Narrow" && c.Args[0].Int() == 127 {
			seen["Narrow max"]++
		}
	})
	want := map[string]int{"Len": 1, "Process": 5 * 5 * 2, "SetN": 7, "Floats": 5, "Narrow": 7, "Narrow max": 3}
	for k, n := range want {
		if seen[k] != n {
			t.Errorf("%s called %d times, want %d", k, seen[k], n)
		}
	}
	if len(seen) != len(want) {
		t.Errorf("calls %v, want %v", seen, want)
	}
}
