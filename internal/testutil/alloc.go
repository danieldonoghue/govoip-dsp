package testutil

import "testing"

// AssertNoAllocs fails t if f allocates. testing.AllocsPerRun calls f once
// before measuring, but component warm-up (filling delay lines, adapting
// filters, reaching steady state) is the caller's job: run it before calling
// this, so the measured calls take the steady-state path.
func AssertNoAllocs(t testing.TB, runs int, f func()) {
	t.Helper()
	if avg := testing.AllocsPerRun(runs, f); avg != 0 {
		t.Errorf("%v allocations per run, want 0", avg)
	}
}
