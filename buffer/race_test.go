//go:build race

package buffer

// Under the race detector the stress test moves fewer samples per capacity.
const raceEnabled = true
