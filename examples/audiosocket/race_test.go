//go:build race

package main

// Under the race detector, single-goroutine tests skip (skipUnderRace).
const raceEnabled = true
