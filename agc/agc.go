package agc

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
	"time"

	"github.com/danieldonoghue/govoip-dsp/internal/fixed"
)

// Sentinel errors. Process returns them unwrapped; New wraps ErrConfig.
var (
	// ErrConfig reports an invalid Config from New, or a zero AGC.
	ErrConfig = errors.New("agc: invalid configuration")
	// ErrBufferSize reports a Process call whose out is shorter than in.
	ErrBufferSize = errors.New("agc: output buffer shorter than input")
)

// Config configures an AGC; the package doc details each field. Every field
// must be set (see DefaultConfig); the zero Config is invalid.
type Config struct {
	// SampleRate is in Hz, 8000 to 48000. Blocks are SampleRate/1000
	// samples.
	SampleRate int

	// TargetLevel, -40 to -3 dBFS (default -24), is the output RMS that a
	// steady input above the gate converges to while its peaks fit LimitLevel.
	TargetLevel int

	// LimitLevel, -20 to 0 dBFS above TargetLevel (default -1), caps output
	// magnitudes at min(32767, round(32768·10^(LimitLevel/20))).
	LimitLevel int

	// GateLevel, -80 dBFS to below TargetLevel (default -50), is the frame
	// RMS below which frames cannot raise gain or move the level estimate.
	GateLevel int

	// MaxGain, 0 to 40 dB (default 24), is the highest AGC gain.
	MaxGain int
	// MinGain, -40 to 0 dB (default -12), is the lowest AGC gain. The
	// limiter may reduce the applied gain below it.
	MinGain int

	// RiseRate, 1 to 100 dB/s (default 12), limits how fast the gain rises,
	// which it does on active frames only.
	RiseRate int
	// FallRate, 1 to 400 dB/s (default 40), limits how fast the gain falls.
	FallRate int

	// LevelTime, 100 ms to 10 s (default 3 s), is the averaging time of the
	// level estimate.
	LevelTime time.Duration
}

// DefaultConfig returns the recommended configuration for sampleRate. It
// does not check sampleRate; New does.
func DefaultConfig(sampleRate int) Config {
	return Config{
		SampleRate:  sampleRate,
		TargetLevel: -24,
		LimitLevel:  -1,
		GateLevel:   -50,
		MaxGain:     24,
		MinGain:     -12,
		RiseRate:    12,
		FallRate:    40,
		LevelTime:   3 * time.Second,
	}
}

const (
	blocksPerFrame = 10
	unity          = 32768 // gain 1.0 in Q15
	limiterRelease = 20 * time.Millisecond
	cutRatio       = 20 // energy ratio over L² of a loud frame (+13 dB)
)

// AGC is an automatic gain control for one mono stream. Make it with New;
// the zero value is not usable (see the package doc).
type AGC struct {
	cfg      Config
	blockLen int32 // K: samples per block
	frameLen int64 // F = 10K: samples per frame
	target   int32 // Q12
	gate     int32 // Q12
	ceiling  int32 // amplitude
	minGain  int32
	maxGain  int32
	limCoef  int16   // Q15 limiter release coefficient
	levCoef  int16   // Q15 level-estimate coefficient
	riseRun  int     // loud frames in a row that trigger a cut: more than a 10 ms click can touch
	up       int16   // Q15 gain increase per frame, minus 1
	down     int16   // Q15 gain factor per frame
	warm     int     // active frames until the level estimate is warmed up
	dropRun  int     // quiet active frames that reset the level estimate
	delay    []int16 // the last 2K input samples

	pos     int         // next slot of delay
	n       int32       // sample index in the current block
	block   int         // block index in the current frame
	energy  int64       // sum of squares of the current frame
	peak    int32       // peak magnitude of the current block
	voiced  bool        // every sample of the current frame had speech = true
	level   int32       // level estimate L = e4^(1/4), Q12
	e4      fixed.Float // mean of the 4th power of frame RMS
	seen    int         // active frames seen, up to warm
	louder  int         // consecutive active frames above +13 dB
	quieter int         // consecutive active frames below -6 dB
	runMin  int32       // smallest RMS of the current loud run
	runMax  int32       // largest RMS of the current quiet run
	fine    int32       // AGC gain in Q22, so that small rate steps are exact
	gain    int32       // AGC gain, fine >> 7
	env     int32       // limiter gain of the last completed block
	bound   int32       // gain at the start of the block being output
	ramp    int32       // gain at sample 0 of the output block
	step    int32       // gain increment per output sample
}

// New validates cfg and allocates everything the AGC will use. It returns
// an error wrapping ErrConfig, naming the field, for an invalid cfg.
func New(cfg Config) (*AGC, error) {
	if err := validate(cfg); err != nil {
		return nil, err
	}
	k := cfg.SampleRate / 1000
	frame := blocksPerFrame * k
	sec := float64(frame) / float64(cfg.SampleRate) // frame duration
	lev := coefficient(frame, cfg.SampleRate, cfg.LevelTime)
	warm := int(math.Round(cfg.LevelTime.Seconds() / sec))
	a := &AGC{
		cfg:      cfg,
		blockLen: int32(k),
		frameLen: int64(frame),
		target:   int32(math.Round(amplitude(cfg.TargetLevel) * 4096)),
		gate:     int32(math.Round(amplitude(cfg.GateLevel) * 4096)),
		ceiling:  int32(min(32767, math.Round(amplitude(cfg.LimitLevel)))),
		minGain:  int32(math.Round(unity * dbToRatio(cfg.MinGain))),
		maxGain:  int32(math.Round(unity * dbToRatio(cfg.MaxGain))),
		limCoef:  coefficient(k, cfg.SampleRate, limiterRelease),
		levCoef:  lev,
		riseRun:  clickFrames(frame, cfg.SampleRate) + 1,
		up:       int16(math.Round(unity * (math.Pow(10, float64(cfg.RiseRate)*sec/20) - 1))),
		down:     int16(math.Round(unity * math.Pow(10, -float64(cfg.FallRate)*sec/20))),
		warm:     warm,
		dropRun:  max(1, warm/3),
		delay:    make([]int16, 2*k),
	}
	a.Reset()
	return a, nil
}

func validate(c Config) error {
	switch {
	case c.SampleRate < 8000 || c.SampleRate > 48000:
		return fmt.Errorf("%w: sample rate %d Hz", ErrConfig, c.SampleRate)
	case c.TargetLevel < -40 || c.TargetLevel > -3:
		return fmt.Errorf("%w: target level %d dBFS", ErrConfig, c.TargetLevel)
	case c.LimitLevel < -20 || c.LimitLevel > 0 || c.LimitLevel <= c.TargetLevel:
		return fmt.Errorf("%w: limit level %d dBFS", ErrConfig, c.LimitLevel)
	case c.GateLevel < -80 || c.GateLevel >= c.TargetLevel:
		return fmt.Errorf("%w: gate level %d dBFS", ErrConfig, c.GateLevel)
	case c.MaxGain < 0 || c.MaxGain > 40:
		return fmt.Errorf("%w: max gain %d dB", ErrConfig, c.MaxGain)
	case c.MinGain < -40 || c.MinGain > 0:
		return fmt.Errorf("%w: min gain %d dB", ErrConfig, c.MinGain)
	case c.RiseRate < 1 || c.RiseRate > 100:
		return fmt.Errorf("%w: rise rate %d dB/s", ErrConfig, c.RiseRate)
	case c.FallRate < 1 || c.FallRate > 400:
		return fmt.Errorf("%w: fall rate %d dB/s", ErrConfig, c.FallRate)
	case c.LevelTime < 100*time.Millisecond || c.LevelTime > 10*time.Second:
		return fmt.Errorf("%w: level time %v", ErrConfig, c.LevelTime)
	}
	return nil
}

// clickFrames returns the most frames of length frame that a 10 ms click
// can touch.
func clickFrames(frame, rate int) int {
	click := (rate + 99) / 100
	return (frame+click-2)/frame + 1
}

// q15Float returns c/32768 as a pseudo-float.
func q15Float(c int32) fixed.Float { return fixed.FloatShl(fixed.PseudoFloat(c), -15) }

func dbToRatio(db int) float64 { return math.Pow(10, float64(db)/20) }

func amplitude(dbfs int) float64 { return 32768 * dbToRatio(dbfs) }

// coefficient returns 1 - exp(-n/(rate·tau)) in Q15: the one-pole step for
// time constant tau, updated every n samples.
func coefficient(n, rate int, tau time.Duration) int16 {
	x := float64(n) / (float64(rate) * tau.Seconds())
	c := math.Round(unity * -math.Expm1(-x))
	return int16(min(max(c, 1), 32767))
}

// Latency returns the delay from input to output in samples: 2 ms worth
// (2·(SampleRate/1000) samples).
func (a *AGC) Latency() int { return len(a.delay) }

// Reset returns the AGC to the state New produced, without allocating.
func (a *AGC) Reset() {
	if a.delay == nil {
		return
	}
	clear(a.delay)
	a.pos, a.n, a.block = 0, 0, 0
	a.energy, a.peak, a.voiced = 0, 0, true
	a.level, a.e4 = a.target, pow4(a.target)
	a.seen, a.louder, a.quieter, a.runMin, a.runMax = 0, 0, 0, 0, 0
	a.fine = unity << 7
	a.gain, a.env, a.bound, a.ramp, a.step = unity, unity, unity, unity, 0
}

// Process writes the delayed, gain-controlled in to out[:len(in)], or returns
// ErrBufferSize. speech is the VAD decision for all of in (true if no VAD).
func (a *AGC) Process(in, out []int16, speech bool) error {
	if a.delay == nil {
		return ErrConfig
	}
	if len(out) < len(in) {
		return ErrBufferSize
	}
	out = out[:len(in)]
	for i, x := range in {
		// x is read before out[i] is written, so in and out may be the same slice.
		y := fixed.Mult16x32Q15(a.delay[a.pos], a.ramp+a.step*a.n)
		out[i] = int16(fixed.Saturate32(y, 32767))
		a.delay[a.pos] = x
		a.pos++
		if a.pos == len(a.delay) {
			a.pos = 0
		}
		a.energy += int64(fixed.Mult16x16(x, x))
		a.peak = max(a.peak, fixed.Abs16(x))
		if !speech {
			a.voiced = false
		}
		a.n++
		if a.n == a.blockLen {
			a.endBlock()
		}
	}
	return nil
}

// endBlock runs after the last input sample of a block. It sets the gain
// ramp for the block that is output next, which is the block before it.
func (a *AGC) endBlock() {
	a.n = 0
	a.block++
	if a.block == blocksPerFrame {
		a.block = 0
		a.endFrame()
	}
	allowed := a.maxGain
	if a.peak > 0 {
		allowed = fixed.Div32(a.ceiling<<15, a.peak)
	}
	e := a.env + towards(a.limCoef, a.gain-a.env)
	e = min(e, allowed, a.gain)
	// Both ends of the ramp are at most the previous block's limiter gain,
	// so no sample of that block exceeds the ceiling.
	b := min(a.env, e)
	a.ramp = a.bound
	a.step = fixed.Div32(b-a.bound, a.blockLen)
	a.bound = b
	a.env = e
	a.peak = 0
}

// endFrame updates the level estimate from an active frame and moves the
// AGC gain towards its target.
func (a *AGC) endFrame() {
	r := rms(a.energy, a.frameLen)
	active := a.voiced && r >= a.gate
	cut := active && a.track(r)
	g := (int64(a.target) << 15) / int64(a.level) // level >= gate > 0
	want := int32(min(max(g, int64(a.minGain)), int64(a.maxGain)))
	if !active {
		a.louder = 0 // the loud run must be consecutive in time
	}
	want <<= 7
	switch {
	case cut:
		a.fine = min(a.fine, want)
	case want > a.fine && active:
		a.fine = min(want, a.fine+max(1, fixed.Mult16x32Q15(a.up, a.fine)))
	case want < a.fine:
		a.fine = max(want, fixed.Mult16x32Q15(a.down, a.fine))
	}
	a.gain = a.fine >> 7
	a.energy = 0
	a.voiced = true
}

// track updates the level estimate with the RMS r of an active frame. It
// reports whether a sustained rise calls for an immediate gain cut.
func (a *AGC) track(r int32) bool {
	l := int64(a.level)
	if int64(r)*int64(r) > cutRatio*l*l {
		if a.louder == 0 {
			a.runMin = r
		}
		a.louder++
		a.runMin = min(a.runMin, r)
	} else {
		a.louder = 0
	}
	if 2*int64(r) < l {
		if a.quieter == 0 {
			a.runMax = r
		}
		a.quieter++
		a.runMax = max(a.runMax, r)
	} else {
		a.quieter = 0
	}
	cut := false
	switch {
	case a.louder == a.riseRun:
		a.e4, a.louder, a.quieter, cut = pow4(a.runMin), 0, 0, true
	case a.quieter == a.dropRun:
		a.e4, a.louder, a.quieter = pow4(a.runMax), 0, 0
	default:
		// Until warm, e4 is the plain mean of the frames seen so far; the
		// first frame is clamped against the initial level, the target.
		c := int32(a.levCoef)
		if a.seen < a.warm {
			c = max(c, unity/int32(a.seen+1))
		}
		// e4 - c·e4 + c·x keeps FloatMult's truncation in the small terms,
		// so the mean does not drift low.
		x := pow4(min(r, 2*a.level))
		if c >= unity {
			a.e4 = x
			break
		}
		cf := q15Float(c)
		a.e4 = fixed.FloatAdd(fixed.FloatSub(a.e4, fixed.FloatMult(cf, a.e4)), fixed.FloatMult(cf, x))
	}
	a.seen = min(a.seen+1, a.warm)
	a.level = max(a.gate, fixed.FloatExtract32(fixed.FloatSqrt(fixed.FloatSqrt(a.e4))))
	return cut
}

// pow4 returns x^4 as a pseudo-float.
func pow4(x int32) fixed.Float {
	p := fixed.PseudoFloat(x)
	p = fixed.FloatMult(p, p)
	return fixed.FloatMult(p, p)
}

// towards returns c·d in Q15 rounded towards d, so that a one-pole update
// always moves when d != 0 and never overshoots, since c < 1.
func towards(c int16, d int32) int32 {
	if d > 0 {
		return -fixed.Mult16x32Q15(c, -d)
	}
	return fixed.Mult16x32Q15(c, d)
}

// rms returns the RMS of n samples with the given sum of squares, in Q12.
// The mean square is normalised to [2^26, 2^28), where fixed.Sqrt is best.
func rms(energy, n int64) int32 {
	if energy == 0 {
		return 0
	}
	m := (energy << 20) / n // Q20; energy < 2^39
	s := (28 - bits.Len64(uint64(m))) &^ 1
	if s >= 0 {
		m <<= uint(s)
	} else {
		m >>= uint(-s)
	}
	return fixed.VShr32(int32(fixed.Sqrt(int32(m))), s/2-2)
}
