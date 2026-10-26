package testutil

// SplitMix64 (Steele, Lea and Flood, 2014) is identical to hz_next in
// reference/common/harness.c, so Go and C draw the same inputs from a seed.
type SplitMix64 struct {
	State uint64
}

// Next returns the next 64-bit value.
func (r *SplitMix64) Next() uint64 {
	r.State += 0x9e3779b97f4a7c15
	z := r.State
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// Intn returns a value in [0, n) for n > 0, by reduction modulo n. The
// slight bias is irrelevant for test signals and keeps the C side trivial.
func (r *SplitMix64) Intn(n int) int {
	return int(r.Next() % uint64(n))
}

// Seed returns the FNV-1a 64-bit hash of name, which the harness drivers use
// as a per-case seed.
func Seed(name string) uint64 {
	h := uint64(0xcbf29ce484222325)
	for i := 0; i < len(name); i++ {
		h ^= uint64(name[i])
		h *= 0x100000001b3
	}
	return h
}
