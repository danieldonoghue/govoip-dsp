package testutil

import (
	"testing"
	"time"
)

func BenchmarkReportFrameMetrics(b *testing.B) {
	// A 10 ms frame at 16 kHz that takes 1 ms reports rt-fraction 0.1.
	for b.Loop() {
		time.Sleep(time.Millisecond)
	}
	ReportFrameMetrics(b, 160, 16000)
}

func TestReportFrameMetricsReports(t *testing.T) {
	r := testing.Benchmark(func(b *testing.B) {
		for b.Loop() {
		}
		ReportFrameMetrics(b, 160, 16000)
	})
	if _, ok := r.Extra["rt-fraction"]; !ok {
		t.Errorf("rt-fraction not reported: %v", r.Extra)
	}
	if v := r.Extra["samples/s"]; v <= 0 {
		t.Errorf("samples/s = %v", v)
	}
}

func TestSpeechFilesLoadWhenPresent(t *testing.T) {
	paths := SpeechFiles(t)
	x := LoadSpeech(t, paths[0])
	if len(x) < SpeechRate || RMS(x) < 100 {
		t.Errorf("%s: %d samples, RMS %g: not plausible 16 kHz speech", paths[0], len(x), RMS(x))
	}
}
