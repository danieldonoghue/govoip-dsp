package testutil

import "testing"

// ReportFrameMetrics, called after b.Loop, reports "samples/s" and
// "rt-fraction" (time over audio duration) for samples per iteration at rate.
func ReportFrameMetrics(b *testing.B, samples int, rate float64) {
	if b.N == 0 || samples <= 0 || rate <= 0 {
		return
	}
	perOp := b.Elapsed().Seconds() / float64(b.N)
	if perOp <= 0 {
		return
	}
	b.ReportMetric(float64(samples)/perOp, "samples/s")
	b.ReportMetric(perOp/(float64(samples)/rate), "rt-fraction")
}
