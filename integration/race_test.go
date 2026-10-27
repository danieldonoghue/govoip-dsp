//go:build race

package integration

// The race detector slows the chain tenfold or more, so the reference test
// then compares the committed prefixes only.
const raceEnabled = true
