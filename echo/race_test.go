//go:build race

package echo

// The race detector slows the reference cases tenfold or more, so they then
// compare a leading part of each case.
const raceEnabled = true
