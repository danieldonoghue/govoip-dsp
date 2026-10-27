package integration

import (
	"flag"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
	"github.com/danieldonoghue/govoip-dsp/resample"
)

var budget = flag.Bool("budget", false, "run the 10-minute real-time budget measurement (docs/acceptance/integration.md, T)")

// budgetConfig is one row of the real-time budget table.
type budgetConfig struct {
	name             string
	netRate, frameMs int
	quality          int // resampler quality where the chain resamples
}

func budgetConfigs() []budgetConfig {
	return []budgetConfig{
		{"8k_10ms", 8000, 10, resample.QualityVoIP},
		{"8k_20ms", 8000, 20, resample.QualityVoIP},
		{"16k_10ms", 16000, 10, resample.QualityVoIP},
		{"16k_20ms", 16000, 20, resample.QualityVoIP},
		{"48k_10ms", 48000, 10, resample.QualityVoIP},
		{"48k_20ms", 48000, 20, resample.QualityVoIP},
		{"48k_10ms_q10", 48000, 10, resample.QualityMax},
	}
}

// chainRig is the whole per-frame work of a budget configuration: the
// send path of a call and, at 48 kHz, the receive path from 16 kHz back to
// 48 kHz.
type chainRig struct {
	c          *call
	rx         *resample.Resampler // nil unless the chain resamples
	s          callSignals
	decoded    []int16 // far end at the processing rate, the receive input
	out, rxOut []int16
	k, frames  int
}

func newChainRig(t testing.TB, bc budgetConfig) *chainRig {
	c, err := newCall(bc.netRate, bc.frameMs)
	if err != nil {
		t.Fatal(err)
	}
	r := &chainRig{c: c, s: callScenario(bc.netRate, 1), out: make([]int16, c.outLen())}
	r.frames = len(r.s.mic) / c.netFrame
	if c.micRS != nil {
		for _, rs := range []*resample.Resampler{c.micRS, c.farRS} {
			if err := rs.SetQuality(bc.quality); err != nil {
				t.Fatal(err)
			}
		}
		if len(c.rsOut) < c.micRS.OutLen(c.netFrame) {
			t.Fatal("resampler buffer too short after SetQuality")
		}
		r.rx, err = resample.New(resample.Config{Channels: 1, InRate: c.rate, OutRate: bc.netRate, Quality: bc.quality})
		if err != nil {
			t.Fatal(err)
		}
		r.rxOut = make([]int16, r.rx.OutLen(c.frame))
		_, r.decoded = atRate(t, r.s, c)
	}
	return r
}

// step runs one network frame through the chain.
func (r *chainRig) step(t testing.TB) {
	lo, hi := r.k*r.c.netFrame, (r.k+1)*r.c.netFrame
	if _, err := r.c.process(r.s.mic[lo:hi], r.s.far[lo:hi], r.out); err != nil {
		t.Fatal(err)
	}
	if r.rx != nil {
		f := r.decoded[r.k*r.c.frame : (r.k+1)*r.c.frame]
		if _, _, err := r.rx.Process(f, r.rxOut); err != nil {
			t.Fatal(err)
		}
	}
	r.k = (r.k + 1) % r.frames
}

// TestRealTimeBudget times every frame over 10 minutes of audio per
// configuration with -budget (criteria T1 to T3), or over 10 s without it,
// checking only that no garbage collection runs during the timed loop.
func TestRealTimeBudget(t *testing.T) {
	seconds := 10
	if *budget {
		seconds = 600
	}
	for _, bc := range budgetConfigs() {
		t.Run(bc.name, func(t *testing.T) {
			r := newChainRig(t, bc)
			perSecond := 1000 / bc.frameMs
			for range 2 * perSecond { // warm-up, not counted
				r.step(t)
			}
			d := make([]time.Duration, seconds*perSecond)
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			for i := range d {
				t0 := time.Now()
				r.step(t)
				d[i] = time.Since(t0)
			}
			runtime.ReadMemStats(&after)
			mallocs, gcs := after.Mallocs-before.Mallocs, after.NumGC-before.NumGC
			slices.Sort(d)
			frame := time.Duration(bc.frameMs) * time.Millisecond
			p50, p99, top := d[len(d)/2], d[len(d)*99/100], d[len(d)-1]
			pct := func(x time.Duration) float64 { return 100 * float64(x) / float64(frame) }
			t.Logf("%s: %d frames (%d s): p50 %v (%.2f%%), p99 %v (%.2f%%), max %v (%.2f%%); %d heap allocations, %d GCs",
				bc.name, len(d), seconds, p50, pct(p50), p99, pct(p99), top, pct(top), mallocs, gcs)
			// Mallocs counts every goroutine, including the runtime's
			// scavenger; TestNoAllocsChain checks the chain itself.
			if gcs != 0 {
				t.Errorf("T3: %d GCs during the timed loop, want 0", gcs)
			}
			if *budget {
				if pct(p99) > 10 {
					t.Errorf("T2: p99 %v is %.2f%% of the %v frame, target at most 10%%", p99, pct(p99), frame)
				}
				if top >= frame {
					t.Errorf("T2: max %v is not below the %v frame", top, frame)
				}
			}
		})
	}
}

// BenchmarkChain is the per-frame cost of each budget configuration.
func BenchmarkChain(b *testing.B) {
	for _, bc := range budgetConfigs() {
		b.Run(bc.name, func(b *testing.B) {
			r := newChainRig(b, bc)
			for range 12 * 1000 / bc.frameMs { // past adaptation
				r.step(b)
			}
			b.ReportAllocs()
			for b.Loop() {
				r.step(b)
			}
			testutil.ReportFrameMetrics(b, r.c.netFrame, float64(bc.netRate))
		})
	}
}

// TestNoAllocsChain checks the budget rig itself, so the benchmark and the
// budget measure allocation-free work.
func TestNoAllocsChain(t *testing.T) {
	for _, bc := range budgetConfigs() {
		t.Run(bc.name, func(t *testing.T) {
			r := newChainRig(t, bc)
			for range 12 * 1000 / bc.frameMs {
				r.step(t)
			}
			testutil.AssertNoAllocs(t, 100, func() { r.step(t) })
		})
	}
}
