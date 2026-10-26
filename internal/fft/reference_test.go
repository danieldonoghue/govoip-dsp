package fft

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

// Mirrors reference/drivers/fft.c: the size and signal tables, the sweep
// generator and the supported-size rule must stay identical, which the input
// streams check.

const fixtureDir = "../../testdata/speexdsp/fft"

var harnessInputs = flag.String("harness-inputs", "", "directory to write harness input files into (used by reference/fixtures.sh)")

var mvpSizes = []int{160, 320, 640, 960, 1280, 1920}

var signals = []string{"silence", "impulse_first", "impulse_mid_neg", "sine_16000", "sine_3", "noise", "square", "min", "dc", "impulse_16000", "impulse_16001", "noise_min_last"}

// signal returns the named input of n samples (docs/acceptance/fft.md).
func signal(name string, n int) []int16 {
	switch name {
	case "silence":
		return testutil.Silence(n)
	case "impulse_first":
		return testutil.Impulse(n, 0, 32767)
	case "impulse_mid_neg":
		return testutil.Impulse(n, n/2-1, -32768)
	case "impulse_16000":
		return testutil.Impulse(n, 1, 16000)
	case "impulse_16001":
		return testutil.Impulse(n, 1, 16001)
	case "sine_16000":
		return testutil.Sine(n, 16000, 1000, 16000, 0.25)
	case "sine_3":
		return testutil.Sine(n, 16000, 1000, 3, 0.25)
	case "noise", "noise_min_last":
		r := testutil.SplitMix64{State: testutil.Seed(fmt.Sprintf("%d_noise", n))}
		x := make([]int16, n)
		for i := range x {
			x[i] = int16(r.Next() >> 48)
		}
		if name == "noise_min_last" {
			x[n-1] = -32768
		}
		return x
	case "square":
		x := make([]int16, n)
		for i := range x {
			x[i] = 32767
			if i/8%2 == 1 {
				x[i] = -32768
			}
		}
		return x
	case "min", "dc":
		v := int16(-32768)
		if name == "dc" {
			v = 32767
		}
		x := make([]int16, n)
		for i := range x {
			x[i] = v
		}
		return x
	}
	panic("unknown signal " + name)
}

// TestHarnessInputs writes the Go-generated driver inputs. It runs only when
// reference/fixtures.sh passes -harness-inputs.
func TestHarnessInputs(t *testing.T) {
	if *harnessInputs == "" {
		t.Skip("writes harness inputs only when -harness-inputs is set (reference/fixtures.sh)")
	}
	for _, n := range mvpSizes {
		for _, s := range signals {
			name := fmt.Sprintf("%d_%s.in.raw", n, s)
			if err := os.WriteFile(filepath.Join(*harnessInputs, name), testutil.EncodeLE(nil, signal(s, n)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// twiddles returns the tables in the driver's order: forward and inverse
// complex twiddles, then forward and inverse super twiddles.
func twiddles(f *FFT) []int16 {
	var out []int16
	for _, tw := range [][]cpx{f.forward.sub.twiddles, f.backward.sub.twiddles, f.forward.superTwiddles, f.backward.superTwiddles} {
		for _, c := range tw {
			out = append(out, c.r, c.i)
		}
	}
	return out
}

func checkStream[T testutil.Elem](t *testing.T, m *testutil.Manifest, c *testutil.Case, name string, got []T) {
	t.Helper()
	k := testutil.NewStreamChecker[T](t, m, c, c.Stream(t, name), false)
	for _, v := range got {
		k.Put(v)
	}
	k.Done()
}

func TestReferenceMVP(t *testing.T) {
	m := testutil.LoadManifest(t, fixtureDir)
	for _, n := range mvpSizes {
		// One FFT per size serves every signal, as in the driver.
		f, err := New(Config{Size: n})
		if err != nil {
			t.Fatal(err)
		}
		t.Run(fmt.Sprintf("twiddles_%d", n), func(t *testing.T) {
			checkStream(t, m, m.Case(t, fmt.Sprintf("twiddles_%d", n)), "twiddles", twiddles(f))
		})
		for _, s := range signals {
			name := fmt.Sprintf("%d_%s", n, s)
			t.Run(name, func(t *testing.T) {
				c := m.Case(t, name)
				x := signal(s, n)
				checkStream(t, m, c, "in", x)

				in := append([]int16(nil), x...)
				spec := make([]int16, n)
				out := make([]int16, n)
				if err := f.Forward(in, spec); err != nil {
					t.Fatal(err)
				}
				checkStream(t, m, c, "fwd", spec)
				checkStream(t, m, c, "in_after", in)

				copy(in, x)
				if err := f.Inverse(in, out); err != nil {
					t.Fatal(err)
				}
				checkStream(t, m, c, "inv", out)

				if err := f.Inverse(spec, out); err != nil {
					t.Fatal(err)
				}
				checkStream(t, m, c, "roundtrip", out)
			})
		}
	}
}

// TestReferenceSweep checks every supported size against the driver's sweep.
func TestReferenceSweep(t *testing.T) {
	m := testutil.LoadManifest(t, fixtureDir)
	c := m.Case(t, "sweep")
	sizes := testutil.NewStreamChecker[int32](t, m, c, c.Stream(t, "sizes"), false)
	tw := testutil.NewStreamChecker[int16](t, m, c, c.Stream(t, "twiddles"), false)
	fwdIn := testutil.NewStreamChecker[int16](t, m, c, c.Stream(t, "fwd_in"), false)
	fwd := testutil.NewStreamChecker[int16](t, m, c, c.Stream(t, "fwd"), false)
	after := testutil.NewStreamChecker[int16](t, m, c, c.Stream(t, "in_after"), false)
	invIn := testutil.NewStreamChecker[int16](t, m, c, c.Stream(t, "inv_in"), false)
	inv := testutil.NewStreamChecker[int16](t, m, c, c.Stream(t, "inv"), false)
	put := func(k *testutil.StreamChecker[int16], x []int16) {
		for _, v := range x {
			k.Put(v)
		}
	}

	r := testutil.SplitMix64{State: testutil.Seed("sweep")}
	in := make([]int16, MaxSize)
	out := make([]int16, MaxSize)
	for n := 2; n <= MaxSize; n += 2 {
		if !Supported(n) {
			continue
		}
		f, err := New(Config{Size: n})
		if err != nil {
			t.Fatal(err)
		}
		sizes.Put(int32(n))
		put(tw, twiddles(f))

		x, y := in[:n], out[:n]
		for i := range x {
			x[i] = int16(r.Next() >> 48)
		}
		put(fwdIn, x)
		if err := f.Forward(x, y); err != nil {
			t.Fatal(err)
		}
		put(fwd, y)
		put(after, x)

		for i := range x {
			x[i] = int16(r.Next() >> 48)
		}
		put(invIn, x)
		if err := f.Inverse(x, y); err != nil {
			t.Fatal(err)
		}
		put(inv, y)
	}
	sizes.Done()
	tw.Done()
	fwdIn.Done()
	fwd.Done()
	after.Done()
	invIn.Done()
	inv.Done()
}
