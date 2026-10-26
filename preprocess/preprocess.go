// Derived from SpeexDSP 1.2.1 (https://github.com/xiph/speexdsp, commit
// 1b28a0f61bc31162979e1f26f3981fc3637095c8): libspeexdsp/preprocess.c.
//
// Copyright (C) 2003 Epic Games (written by Jean-Marc Valin)
// Copyright (C) 2004-2006 Epic Games
//
// Use of this source is governed by the BSD-3-Clause licence in
// LICENSES/SpeexDSP-COPYING. Changes: ported to Go, FIXED_POINT only; ctl
// requests became methods, the echo state an explicit residual argument.

package preprocess

import (
	"errors"
	"fmt"

	"github.com/danieldonoghue/govoip-dsp/internal/fft"
	"github.com/danieldonoghue/govoip-dsp/internal/fixed"
)

// Sentinel errors. Run, RunEcho, EstimateUpdate, PSD and NoisePSD return
// only these, so they do not allocate.
var (
	ErrConfig     = errors.New("preprocess: invalid configuration")
	ErrBufferSize = errors.New("preprocess: buffer length does not match the frame size")
)

// Supported ranges (see the package doc).
const (
	MinSampleRate = 4
	MaxSampleRate = 65535
	MaxFrameSize  = fft.MaxSize / 2
)

const nbBands = 24 // NB_BANDS

// Fixed-point constants of preprocess.c, as the QCONST16 and QCONST32
// expressions evaluate; TestConstants checks each against fixed.QConst16.
const (
	probStartDefault    = 11469 // QCONST16(0.35f,15)
	probContinueDefault = 6554  // QCONST16(0.20f,15)
	noiseSuppressDef    = -15
	echoSuppressDef     = -40
	echoSuppressActDef  = -15

	snrShift   = 8 // SNR_SHIFT
	expinShift = 11
	noiseShift = 7

	q13One    = 8192    // QCONST16(1.f,13)
	q13Two    = 16384   // QCONST16(2.f,13)
	q13Three  = 24576   // QCONST16(3.f,13)
	winScale  = 20839   // QCONST16(1.271903f,14)
	q15Half   = 16384   // QCONST16(.5f,15)
	hgTail    = 1087164 // QCONST32(.1296,23)
	qc060     = 19661   // QCONST16(.60f,15)
	gfNoise   = 236     // QCONST16(0.11513,11)
	gfRatio   = 472     // QCONST16(.2302585f,11)
	betaMin   = 983     // QCONST16(.03,15)
	echoDecay = 19661   // QCONST16(.6f,15)
	c080      = 26214   // QCONST16(.8f,15)
	c005      = 1638    // QCONST16(.05f,15)
	c010      = 3277    // QCONST16(.1f,15)
	c020      = 6554    // QCONST16(.2f,15)
	c040      = 13107   // QCONST16(.4f,15)
	c089      = 29164   // QCONST16(.89f,15)
	c070      = 22938   // QCONST16(.7f,15)
	c030      = 9830    // QCONST16(.3f,15)
	c015      = 4915    // QCONST16(.15f,15)
	c0075     = 2458    // QCONST16(.075f,15)
	c0899     = 29458   // QCONST16(.899f,15)
	c0199     = 6521    // QCONST16(.199f,15)
	c0333     = 10912   // QCONST16(.333f,15)
	c095      = 31130   // QCONST16(.95f,15)
	snrOne    = 256     // QCONST16(1.f,SNR_SHIFT)
	snr100    = 25600   // QCONST16(100.f,SNR_SHIFT)
	snr3      = 768     // QCONST16(3.,SNR_SHIFT)
	expinOne  = 2048    // QCONST32(1.f,EXPIN_SHIFT)
	noiseOne  = 128     // QCONST32(1.f,NOISE_SHIFT)
)

// Config configures a Preprocessor. Both fields are required.
type Config struct {
	// SampleRate is the sampling rate in Hz, MinSampleRate to MaxSampleRate.
	SampleRate int
	// FrameSize is the number of samples per frame; SupportedFrameSize tells
	// which New accepts. 10 and 20 ms at 8, 16, 32 and 48 kHz (80, 160,
	// 320, 640, 480, 960) are.
	FrameSize int
}

// Preprocessor is SpeexPreprocessState for the FIXED_POINT build.
type Preprocessor struct {
	frameSize, sampleRate int
	bank                  *filterbank
	fft                   *fft.FFT

	// Settings, kept by Reset.
	denoise, vad                bool
	probStart, probContinue     int16
	noiseSuppress, echoSuppress int32
	echoSuppressActive          int32
	reverbDecay                 int16 // always 0: no dereverb setter upstream

	speechProb int16

	frame, ft, window               []int16
	ps, noise, echoNoise, reverbEst []int32
	oldPS                           []int32
	gain2, gainFloor, gain          []int16
	prior, post, zeta               []int16
	s, sMin, sTmp                   []int32
	updateProb                      []bool
	inbuf, outbuf                   []int16

	nbAdapt    int
	wasSpeech  bool
	minCount   int
	frameShift int
}

// SupportedFrameSize reports whether New accepts frame size n, at any
// supported rate: 1 to MaxFrameSize, with an FFT of 2*n samples available.
func SupportedFrameSize(n int) bool {
	return n >= 1 && n <= MaxFrameSize && fft.Supported(2*n)
}

// New is speex_preprocess_state_init (preprocess.c:391). It returns an
// error wrapping ErrConfig for an unsupported configuration.
func New(cfg Config) (*Preprocessor, error) {
	if !SupportedFrameSize(cfg.FrameSize) {
		return nil, fmt.Errorf("%w: frame size %d (want 1 to %d with no prime factor above 17)",
			ErrConfig, cfg.FrameSize, MaxFrameSize)
	}
	if cfg.SampleRate < MinSampleRate || cfg.SampleRate > MaxSampleRate {
		return nil, fmt.Errorf("%w: sample rate %d Hz (want %d to %d)",
			ErrConfig, cfg.SampleRate, MinSampleRate, MaxSampleRate)
	}
	n := cfg.FrameSize
	f, err := fft.New(fft.Config{Size: 2 * n})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConfig, err)
	}
	m := nbBands
	p := &Preprocessor{
		frameSize:  n,
		sampleRate: cfg.SampleRate,
		bank:       newFilterbank(m, int32(cfg.SampleRate), n),
		fft:        f,
		frame:      make([]int16, 2*n),
		ft:         make([]int16, 2*n),
		window:     make([]int16, 2*n),
		ps:         make([]int32, n+m),
		noise:      make([]int32, n+m),
		echoNoise:  make([]int32, n+m),
		reverbEst:  make([]int32, n+m),
		oldPS:      make([]int32, n+m),
		gain2:      make([]int16, n+m),
		gainFloor:  make([]int16, n+m),
		gain:       make([]int16, n+m),
		prior:      make([]int16, n+m),
		post:       make([]int16, n+m),
		zeta:       make([]int16, n+m),
		s:          make([]int32, n),
		sMin:       make([]int32, n),
		sTmp:       make([]int32, n),
		updateProb: make([]bool, n),
		inbuf:      make([]int16, n),
		outbuf:     make([]int16, n),
	}
	// ps_size equals frame_size (preprocess.c:418), so N3 = N and N4 = 0:
	// the window is conj_window over all 2N samples.
	conjWindow(p.window)
	p.denoise = true
	p.probStart = probStartDefault
	p.probContinue = probContinueDefault
	p.noiseSuppress = noiseSuppressDef
	p.echoSuppress = echoSuppressDef
	p.echoSuppressActive = echoSuppressActDef
	p.Reset()
	return p, nil
}

// Reset returns the adaptive state to what New leaves and keeps the settings
// (the reference has no reset). It does not allocate.
func (p *Preprocessor) Reset() {
	for _, s := range [][]int16{p.frame, p.ft, p.gain2, p.gainFloor, p.zeta, p.inbuf, p.outbuf} {
		clear(s)
	}
	for _, s := range [][]int32{p.ps, p.echoNoise, p.s, p.sMin, p.sTmp} {
		clear(s)
	}
	for i := range p.noise {
		p.noise[i] = noiseOne
		p.reverbEst[i] = 0
		p.oldPS[i] = 1
		p.gain[i] = fixed.Q15One
		p.post[i] = snrOne
		p.prior[i] = snrOne
	}
	for i := range p.updateProb {
		p.updateProb[i] = true
	}
	p.speechProb = 0
	p.nbAdapt = 0
	p.wasSpeech = false
	p.minCount = 0
	p.frameShift = 0
}

// FrameSize returns the number of samples per frame.
func (p *Preprocessor) FrameSize() int { return p.frameSize }

// SampleRate returns the sampling rate in Hz.
func (p *Preprocessor) SampleRate() int { return p.sampleRate }

// conjWindow is conj_window (preprocess.c:251), FIXED_POINT.
func conjWindow(w []int16) {
	n := len(w)
	for i := range w {
		x := fixed.Div32x16(fixed.Mult16x16(32767, int16(i)), int16(n))
		inv := false
		switch {
		case x < q13One:
		case x < q13Two:
			x = q13Two - x
			inv = true
		case x < q13Three:
			x = x - q13Two
			inv = true
		default:
			x = q13Two - x + q13Two
		}
		x = int16(fixed.Mult16x16Q14(winScale, x))
		c := fixed.Mult16x16P15(q15Half, fixed.CosNorm(fixed.Shl32(fixed.Extend32(x), 2)))
		d := int16(q15Half - c)
		tmp := int16(fixed.Mult16x16Q15(d, d))
		if inv {
			tmp = int16(fixed.Sub16(fixed.Q15One, tmp))
		}
		w[i] = fixed.Sqrt(fixed.Shl32(fixed.Extend32(tmp), 15))
	}
}
