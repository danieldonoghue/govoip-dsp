//go:build race

package fixed

// The race detector slows the exhaustive and random cases tenfold or more,
// so reference tests then compare only the first chunk of each stream.
const raceEnabled = true
