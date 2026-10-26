package fixed

import (
	"os/exec"
	"regexp"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

// The OrZero variants must equal the harness-checked divisions wherever
// those are defined, and give 0 for a zero divisor.
func TestDivOrZero(t *testing.T) {
	r := testutil.SplitMix64{State: testutil.Seed("div_or_zero")}
	as := []int32{0, 1, -1, 32767, -32768, 1 << 30, -1 << 31, 1<<31 - 1}
	for range 2000 {
		as = append(as, int32(r.Next()))
	}
	for _, a := range as {
		for b := -32768; b <= 32767; b += 1 + b&7 {
			if b == 0 {
				continue
			}
			if got, want := Div32x16OrZero(a, int16(b)), Div32x16(a, int16(b)); got != want {
				t.Fatalf("Div32x16OrZero(%d, %d) = %d, want %d", a, b, got, want)
			}
			if got, want := PDiv32x16OrZero(a, int16(b)), PDiv32x16(a, int16(b)); got != want {
				t.Fatalf("PDiv32x16OrZero(%d, %d) = %d, want %d", a, b, got, want)
			}
		}
		if got := Div32x16OrZero(a, 0); got != 0 {
			t.Fatalf("Div32x16OrZero(%d, 0) = %d, want 0", a, got)
		}
		if got := PDiv32x16OrZero(a, 0); got != 0 {
			t.Fatalf("PDiv32x16OrZero(%d, 0) = %d, want 0", a, got)
		}
	}
}

func TestInliningDivOrZero(t *testing.T) {
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
	for _, name := range []string{"Div32x16OrZero", "PDiv32x16OrZero"} {
		if !regexp.MustCompile(`(?m): can inline ` + name + `( |$)`).Match(out) {
			t.Errorf("%s does not inline", name)
		}
	}
}
