package testutil

import "testing"

// AssertNoAllocs fails t if f allocates. Reach steady state (delay lines,
// adapted filters) before calling it; AllocsPerRun only warms up once.
func AssertNoAllocs(t testing.TB, runs int, f func()) {
	t.Helper()
	if avg := testing.AllocsPerRun(runs, f); avg != 0 {
		t.Errorf("%v allocations per run, want 0", avg)
	}
}
