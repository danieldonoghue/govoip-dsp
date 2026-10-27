// Package integration holds tests only. They run the MVP packages together
// as one VoIP call uses them: resample, echo, preprocess with the residual
// echo of echo, and agc, with buffer.SPSC between a network goroutine and
// the processing goroutine.
//
// The tests check the echo and preprocess pairing bit for bit against the
// same chain in C (testdata/speexdsp/integration), measure the chain on
// synthetic calls, LibriSpeech speech and stationary noise, measure the
// real-time budget (go test ./integration -run TestRealTimeBudget -budget),
// and hold the recipe of docs/usage.md. Criteria are in
// docs/acceptance/integration.md.
package integration
