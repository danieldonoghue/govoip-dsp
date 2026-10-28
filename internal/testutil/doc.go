// Package testutil holds shared test support: deterministic signal
// generators, signal metrics, an allocation assertion, the SplitMix64
// generator shared with the C reference harness, the fixture loader, and a
// check of every method of a zero value (no panic, ErrConfig, no writes).
//
// Every generator is bit-identical on every platform. Generators use integer
// arithmetic, or IEEE basic operations with explicit float64 conversions so
// the compiler cannot fuse a multiply-add; they call no math library function
// that is implemented in assembly on some architectures (math.Sin, math.Exp
// and math.Log can differ between amd64 and arm64). Inputs generated from a
// seed at test time therefore match the hashes recorded by the harness on any
// machine. Metrics use the math package freely, since they are reported, not
// compared bit for bit.
//
// The package is append-only during parallel work: add functions with tests,
// never change existing behaviour.
package testutil
