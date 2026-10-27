package integration

import (
	"fmt"
	"math"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/echo"
	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
	"github.com/danieldonoghue/govoip-dsp/resample"
)

// ceiling is the AGC output ceiling of agc.DefaultConfig (LimitLevel -1
// dBFS): round(32768 * 10^(-1/20)).
const ceiling = 29205

// atRate returns the microphone and far end at the processing rate, as
// resamplers of the given quality produce them, one call per network frame.
func atRate(t testing.TB, s callSignals, c *call, quality int) (mic, far []int16) {
	if c.netRate == c.rate {
		return s.mic, s.far
	}
	conv := func(x []int16) []int16 {
		r, err := resample.New(resample.Config{Channels: 1, InRate: c.netRate, OutRate: c.rate, Quality: quality})
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]int16, r.OutLen(c.netFrame))
		var y []int16
		for k := 0; (k+1)*c.netFrame <= len(x); k++ {
			_, n, err := r.Process(x[k*c.netFrame:(k+1)*c.netFrame], buf)
			if err != nil {
				t.Fatal(err)
			}
			y = append(y, buf[:n]...)
		}
		return y
	}
	return conv(s.mic), conv(s.far)
}

// echoOnly runs a lone canceller, configured as the call's, over mic and
// far: the echo-stage output of the chain.
func echoOnly(t testing.TB, c *call, mic, far []int16) []int16 {
	ec, err := echo.New(echo.Config{FrameSize: c.frame, FilterLength: c.rate / 5, SampleRate: c.rate})
	if err != nil {
		t.Fatal(err)
	}
	e := make([]int16, len(mic)/c.frame*c.frame)
	for i := 0; i+c.frame <= len(e); i += c.frame {
		if err := ec.Cancel(mic[i:i+c.frame], far[i:i+c.frame], e[i:i+c.frame]); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

// figures are the measurements of docs/acceptance/integration.md, B1 to B5.
type figures struct {
	echoERLE           float64 // B1, 8 to 14 s
	chainERLE, after   float64 // B2, 8 to 14 s and 36 to 40 s
	nearLevel          float64 // B3, active frames 18 to 24 s
	dtLevel            float64 // B4, active frames 28 to 34 s
	nearFrames, dtFrms int
	peak               int // B5
}

func (f figures) String() string {
	return fmt.Sprintf("echo stage %.1f dB; chain %.1f dB, after double talk %.1f dB; near end %.1f dBFS (%d frames), double talk %.1f dBFS (%d frames, %+.1f dB); peak %d",
		f.echoERLE, f.chainERLE, f.after, f.nearLevel, f.nearFrames, f.dtLevel, f.dtFrms, f.dtLevel-f.nearLevel, f.peak)
}

// measureCall runs s through a new call and measures it.
func measureCall(t testing.TB, s callSignals, netRate, frameMs int) figures {
	c, err := newCall(netRate, frameMs)
	if err != nil {
		t.Fatal(err)
	}
	mic, far := atRate(t, s, c, resample.QualityVoIP)
	e := echoOnly(t, c, mic, far)
	out := runDirect(t, c, s)
	r := c.rate
	ratio := func(y []int16, from, to float64) float64 {
		return dB(energy(window(mic, r, from, to)), energy(window(y, r, from, to)))
	}
	var f figures
	f.echoERLE = ratio(e, 8, 14)
	f.chainERLE = ratio(out, 8, 14)
	f.after = ratio(out, 36, 40)
	f.nearLevel, f.nearFrames = activeDBFS(window(out, r, 18, 24), r)
	f.dtLevel, f.dtFrms = activeDBFS(window(out, r, 28, 34), r)
	for _, v := range out {
		f.peak = max(f.peak, int(math.Abs(float64(v))))
	}
	return f
}

// failures applies the thresholds of B1 to B5 and returns the criteria
// that fail, with the reason.
func (f figures) failures() map[string]string {
	fail := map[string]string{}
	if f.echoERLE < 20 {
		fail["B1"] = fmt.Sprintf("echo stage %.1f dB, want at least 20", f.echoERLE)
	}
	if f.chainERLE < 20 || f.after < 20 {
		fail["B2"] = fmt.Sprintf("echo through the chain %.1f and %.1f dB, want at least 20", f.chainERLE, f.after)
	}
	if f.nearLevel < -30 || f.nearLevel > -18 {
		fail["B3"] = fmt.Sprintf("near-end level %.1f dBFS, want -24 ± 6", f.nearLevel)
	}
	if f.dtLevel < f.nearLevel-10 {
		fail["B4"] = fmt.Sprintf("double-talk level %.1f dBFS is more than 10 dB below the near-end level %.1f", f.dtLevel, f.nearLevel)
	}
	if f.peak > ceiling {
		fail["B5"] = fmt.Sprintf("peak %d above the ceiling %d", f.peak, ceiling)
	}
	return fail
}

// check fails t for a failing criterion not in findings, and for one in
// findings that passes, so a change in either direction shows.
func (f figures) check(t testing.TB, findings ...string) {
	t.Helper()
	fail := f.failures()
	for _, id := range findings {
		if why, ok := fail[id]; ok {
			t.Logf("%s fails, a reported finding (docs/testing.md): %s", id, why)
			delete(fail, id)
		} else {
			t.Errorf("%s was a reported finding but now passes; update docs/testing.md", id)
		}
	}
	for _, id := range []string{"B1", "B2", "B3", "B4", "B5"} {
		if why, ok := fail[id]; ok {
			t.Errorf("%s: %s", id, why)
		}
	}
}

// TestPipelineCall measures the send path on the synthetic call (B1 to B5).
// B1 and B3 fail at 48 kHz, a finding reported in docs/testing.md.
func TestPipelineCall(t *testing.T) {
	type cfg struct {
		netRate, frameMs int
		findings         []string
	}
	cfgs := []cfg{{8000, 10, nil}, {8000, 20, nil}, {16000, 10, nil}, {16000, 20, nil}, {48000, 20, []string{"B1", "B3"}}}
	for _, k := range cfgs {
		t.Run(fmt.Sprintf("%dk_%dms", k.netRate/1000, k.frameMs), func(t *testing.T) {
			f := measureCall(t, callScenario(k.netRate, 1), k.netRate, k.frameMs)
			t.Log(f)
			f.check(t, k.findings...)
		})
	}
}

// TestPipelineSpeech is TestPipelineCall on LibriSpeech (criterion B6). It
// skips when the speech files are absent.
func TestPipelineSpeech(t *testing.T) {
	for _, rate := range []int{8000, 16000} {
		t.Run(fmt.Sprintf("%dk_20ms", rate/1000), func(t *testing.T) {
			s := speechScenario(t, rate)
			f := measureCall(t, s, rate, 20)
			t.Logf("far %.1f dBFS, near %.1f dBFS active", must(activeDBFS(s.far, rate)), must(activeDBFS(s.near, rate)))
			t.Log(f)
			f.check(t)
		})
	}
}

func must(v float64, _ int) float64 { return v }

// TestReceivePath resamples decoded 20 ms frames one call per frame with
// an output buffer of OutLen samples (criterion B7).
func TestReceivePath(t *testing.T) {
	for _, p := range [][2]int{{8000, 48000}, {16000, 48000}, {48000, 16000}} {
		in, out := p[0], p[1]
		t.Run(fmt.Sprintf("%dk_to_%dk", in/1000, out/1000), func(t *testing.T) {
			cfg := resample.Config{Channels: 1, InRate: in, OutRate: out, Quality: resample.QualityVoIP}
			r, err := resample.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			frame := in / 50
			x := speech(in, 60*in, 12000, "integration/receive")
			buf := make([]int16, r.OutLen(frame))
			var got []int16
			for k := 0; (k+1)*frame <= len(x); k++ {
				read, n, err := r.Process(x[k*frame:(k+1)*frame], buf)
				if err != nil || read != frame {
					t.Fatalf("frame %d: read %d of %d, %v", k, read, frame, err)
				}
				got = append(got, buf[:n]...)
			}
			whole, err := resample.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			want := make([]int16, whole.OutLen(len(x)))
			read, n, err := whole.Process(x, want)
			if err != nil || read != len(x) {
				t.Fatalf("one call read %d of %d, %v", read, len(x), err)
			}
			if len(got) != n {
				t.Fatalf("per-frame calls wrote %d samples, one call %d", len(got), n)
			}
			testutil.Compare(t, "out", want[:n], got)
			t.Logf("%d frames of %d samples, buffer %d, wrote %d samples", len(x)/frame, frame, len(buf), len(got))
		})
	}
}

// TestFinding48k reports the measurements behind the 48 kHz finding of
// docs/testing.md. It asserts nothing; TestPipelineCall pins the finding.
func TestFinding48k(t *testing.T) {
	s48 := callScenario(48000, 1)
	for _, q := range []int{resample.QualityVoIP, resample.QualityDesktop, resample.QualityMax} {
		c, err := newCall(48000, 20)
		if err != nil {
			t.Fatal(err)
		}
		mic, far := atRate(t, s48, c, q)
		e := echoOnly(t, c, mic, far)
		t.Logf("48 kHz, resampler quality %d: echo stage %.1f dB", q, dB(energy(window(mic, 16000, 8, 14)), energy(window(e, 16000, 8, 14))))
	}
	c, err := newCall(48000, 20)
	if err != nil {
		t.Fatal(err)
	}
	_, far := atRate(t, s48, c, resample.QualityVoIP)
	near, _ := atRate(t, callSignals{mic: s48.near, far: s48.near}, c, resample.QualityVoIP)
	mic := mix(convolve(far, echoPath(16000)), near, testutil.WhiteNoise(len(far), 30, testutil.Seed("integration/noise")))
	e := echoOnly(t, c, mic, far)
	t.Logf("48 kHz signals, echo built at 16 kHz from the resampled far end: echo stage %.1f dB",
		dB(energy(window(mic, 16000, 8, 14)), energy(window(e, 16000, 8, 14))))

	for _, netRate := range []int{16000, 48000} {
		c, err := newCall(netRate, 20)
		if err != nil {
			t.Fatal(err)
		}
		mic, far := atRate(t, callScenario(netRate, 1), c, resample.QualityVoIP)
		var pre, out []int16
		o := make([]int16, c.frame)
		speech := 0
		for i := 0; i+c.frame <= len(mic); i += c.frame {
			if err := c.ec.Cancel(mic[i:i+c.frame], far[i:i+c.frame], c.clean); err != nil {
				t.Fatal(err)
			}
			if err := c.ec.Residual(c.residual); err != nil {
				t.Fatal(err)
			}
			v, err := c.pre.RunEcho(c.clean, c.residual)
			if err != nil {
				t.Fatal(err)
			}
			if v && i >= 2*c.rate && i < 14*c.rate {
				speech++
			}
			pre = append(pre, c.clean...)
			if err := c.gc.Process(c.clean, o, v); err != nil {
				t.Fatal(err)
			}
			out = append(out, o...)
		}
		r := c.rate
		res, _ := activeDBFS(window(pre, r, 2, 14), r)
		gain := func(from, to float64) float64 {
			return dB(energy(window(out, r, from, to)), energy(window(pre, r, from, to)))
		}
		t.Logf("network %d Hz: residual echo after preprocess %.1f dBFS (active frames, 2 to 14 s), VAD speech on %d of %d frames there; AGC gain %+.1f dB over 8 to 14 s, %+.1f dB over 18 to 24 s",
			netRate, res, speech, 12*r/c.frame, gain(8, 14), gain(18, 24))
	}
}
