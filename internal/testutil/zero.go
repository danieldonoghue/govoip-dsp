package testutil

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"testing"
)

// ZeroCall is one call that CallZeroValue made.
type ZeroCall struct {
	Method  string
	Args    []reflect.Value
	Results []reflect.Value
	Panic   any  // the recovered panic value, nil if the call returned
	Changed bool // the receiver differs from a zero value after the call
	Wrote   bool // the call changed an element of a slice argument
}

// zeroSliceLens are the slice argument lengths: 160 and 161 are a 10 ms frame
// at 16 kHz and its residual spectrum.
var zeroSliceLens = []int{0, 1, 160, 161}

// zeroArgs returns the values CallZeroValue tries for parameter k of type typ,
// or nil if it has none. Slice elements differ by k and index to show writes.
func zeroArgs(k int, typ reflect.Type) []reflect.Value {
	var vs []reflect.Value
	switch typ.Kind() {
	case reflect.Bool:
		for _, b := range []bool{false, true} {
			vs = append(vs, reflect.ValueOf(b).Convert(typ))
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		top := int64(1)<<(typ.Bits()-1) - 1
		for _, n := range []int64{-top - 1, -1, 0, 1, 160, 16000, top} {
			v := reflect.New(typ).Elem()
			v.SetInt(min(n, top))
			vs = append(vs, v)
		}
	case reflect.Slice:
		switch typ.Elem().Kind() {
		case reflect.Int16, reflect.Int32, reflect.Float32:
		default:
			return nil
		}
		for _, n := range zeroSliceLens {
			if n == 0 {
				vs = append(vs, reflect.Zero(typ), reflect.MakeSlice(typ, 0, 0))
				continue
			}
			s := reflect.MakeSlice(typ, n, n)
			for i := range n {
				s.Index(i).Set(reflect.ValueOf(7 + 1000*k + i).Convert(typ.Elem()))
			}
			vs = append(vs, s)
		}
	}
	return vs
}

// CallZeroValue calls every exported method of *T on a fresh zero T with each
// combination of zeroArgs values, and passes each call to visit.
func CallZeroValue[T any](t testing.TB, visit func(ZeroCall)) {
	t.Helper()
	ptr := reflect.PointerTo(reflect.TypeFor[T]())
	calls, skipped := 0, false
	for i := range ptr.NumMethod() {
		m := ptr.Method(i)
		var sets [][]reflect.Value
		ok := true
		for j := 1; j < m.Type.NumIn(); j++ {
			vs := zeroArgs(j, m.Type.In(j))
			if vs == nil {
				t.Errorf("%v.%s: no zero-value arguments for parameter type %v", ptr, m.Name, m.Type.In(j))
				ok, skipped = false, true
				break
			}
			sets = append(sets, vs)
		}
		if !ok {
			continue
		}
		eachCombination(sets, func(args []reflect.Value) {
			calls++
			visit(callZero(m, args))
		})
	}
	if calls == 0 && !skipped {
		t.Errorf("%v has no exported methods to call", ptr)
	}
}

// eachCombination calls f with every tuple that takes one value from each
// set, reusing the slice it passes.
func eachCombination(sets [][]reflect.Value, f func([]reflect.Value)) {
	args := make([]reflect.Value, len(sets))
	var rec func(int)
	rec = func(k int) {
		if k == len(sets) {
			f(args)
			return
		}
		for _, v := range sets[k] {
			args[k] = v
			rec(k + 1)
		}
	}
	rec(0)
}

func callZero(m reflect.Method, args []reflect.Value) (c ZeroCall) {
	recv := reflect.New(m.Type.In(0).Elem())
	in := []reflect.Value{recv}
	before := make([]reflect.Value, len(args))
	for i, a := range args {
		if a.Kind() == reflect.Slice && !a.IsNil() {
			before[i] = reflect.MakeSlice(a.Type(), a.Len(), a.Len())
			reflect.Copy(before[i], a)
		}
		in = append(in, a)
	}
	c.Method = m.Name
	c.Args = append([]reflect.Value(nil), args...)
	defer func() {
		c.Panic = recover()
		c.Changed = !reflect.DeepEqual(recv.Interface(), reflect.New(recv.Type().Elem()).Interface())
		for i, a := range args {
			if before[i].IsValid() && !sameSlice(before[i], a) {
				c.Wrote = true
				// Restore the pattern so the next call sees the same argument.
				reflect.Copy(a, before[i])
			}
		}
	}()
	c.Results = m.Func.Call(in)
	return c
}

// sameSlice compares element bits, so that a NaN written over a NaN counts
// as unchanged and -0 over 0 as changed.
func sameSlice(a, b reflect.Value) bool {
	for i := range a.Len() {
		x, y := a.Index(i), b.Index(i)
		if x.Kind() == reflect.Float32 {
			if math.Float32bits(float32(x.Float())) != math.Float32bits(float32(y.Float())) {
				return false
			}
		} else if x.Int() != y.Int() {
			return false
		}
	}
	return true
}

// CheckZeroValue fails t for a method of a zero T that panics, writes an
// argument, returns other than zeros and errConfig, or changes T not in stores.
func CheckZeroValue[T any](t testing.TB, errConfig error, stores ...string) {
	t.Helper()
	errType := reflect.TypeFor[error]()
	failed := map[string]bool{}
	var msgs []string
	CallZeroValue[T](t, func(c ZeroCall) {
		if failed[c.Method] {
			return
		}
		var why string
		switch {
		case c.Panic != nil:
			why = fmt.Sprintf("panics: %v", c.Panic)
		case c.Wrote:
			why = "writes into an argument"
		case c.Changed && !slices.Contains(stores, c.Method):
			why = "changes the zero value"
		}
		for i, r := range c.Results {
			if why != "" {
				break
			}
			if r.Type() == errType {
				if err, _ := r.Interface().(error); err != errConfig {
					why = fmt.Sprintf("returns error %v, want %v", err, errConfig)
				}
			} else if !r.IsZero() {
				why = fmt.Sprintf("returns %v as result %d, want the zero value", r, i)
			}
		}
		if why != "" {
			failed[c.Method] = true
			msgs = append(msgs, fmt.Sprintf("zero %v.%s%s %s", reflect.TypeFor[T](), c.Method, formatArgs(c.Args), why))
		}
	})
	for _, m := range msgs {
		t.Errorf("%s", m)
	}
}

// formatArgs prints slices by length, so a failure names the call briefly.
func formatArgs(args []reflect.Value) string {
	s := "("
	for i, a := range args {
		if i > 0 {
			s += ", "
		}
		if a.Kind() == reflect.Slice && a.IsNil() {
			s += "nil"
		} else if a.Kind() == reflect.Slice {
			s += fmt.Sprintf("len %d", a.Len())
		} else {
			s += fmt.Sprint(a)
		}
	}
	return s + ")"
}
