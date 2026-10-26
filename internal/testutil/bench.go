package testutil

import "testing"

// ReportFrameMetrics adds two metrics to a benchmark whose every iteration
// processes samples samples (per channel) of audio at rate Hz: "samples/s",
// the throughput, and "rt-fraction", the processing time divided by the
// duration of that audio. Call it after the b.Loop loop.
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
