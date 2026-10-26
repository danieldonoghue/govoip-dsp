package testutil

import "testing"

// The expected values are the published SplitMix64 outputs for seed 1234567
// (from the reference implementation by Sebastiano Vigna).
func TestSplitMix64KnownValues(t *testing.T) {
	r := SplitMix64{State: 1234567}
	want := []uint64{
		6457827717110365317,
		3203168211198807973,
		9817491932198370423,
		4593380528125082431,
		16408922859458223821,
	}
	for i, w := range want {
		if got := r.Next(); got != w {
			t.Fatalf("value %d: got %d, want %d", i, got, w)
		}
	}
}

func TestSeedFNV1a(t *testing.T) {
	// FNV-1a 64 of "" is the offset basis; of "a" it is 0xaf63dc4c8601ec8c.
	if got := Seed(""); got != 0xcbf29ce484222325 {
		t.Errorf("Seed(\"\") = %#x", got)
	}
	if got := Seed("a"); got != 0xaf63dc4c8601ec8c {
		t.Errorf("Seed(\"a\") = %#x", got)
	}
}
