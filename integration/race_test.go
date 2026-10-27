//go:build race

package integration

// Under the race detector, single-goroutine tests skip (skipUnderRace) and
// the pairing test compares prefixes plus three full cases.
const raceEnabled = true
