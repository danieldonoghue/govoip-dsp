// Derived from SpeexDSP 1.2.1 (https://github.com/xiph/speexdsp, commit
// 1b28a0f61bc31162979e1f26f3981fc3637095c8): libspeexdsp/preprocess.c.
//
// Copyright (C) 2003 Epic Games (written by Jean-Marc Valin)
// Copyright (C) 2004-2006 Epic Games
//
// Use of this source is governed by the BSD-3-Clause licence in
// LICENSES/SpeexDSP-COPYING. Changes: speex_preprocess_ctl requests of the
// FIXED_POINT build became methods.

package preprocess

import "github.com/danieldonoghue/govoip-dsp/internal/fixed"

// SetDenoise is SPEEX_PREPROCESS_SET_DENOISE. Denoise is on after New.
// With it off, frames still pass through the analysis and synthesis.
func (p *Preprocessor) SetDenoise(on bool) { p.denoise = on }

// Denoise is SPEEX_PREPROCESS_GET_DENOISE: whether noise suppression is
// on.
func (p *Preprocessor) Denoise() bool { return p.denoise }

// SetVAD is SPEEX_PREPROCESS_SET_VAD. VAD is off after New, and Run then
// always reports speech.
func (p *Preprocessor) SetVAD(on bool) { p.vad = on }

// VAD is SPEEX_PREPROCESS_GET_VAD: whether Run and RunEcho report the
// VAD decision.
func (p *Preprocessor) VAD() bool { return p.vad }

// probQ15 is the conversion of SET_PROB_START and SET_PROB_CONTINUE.
func probQ15(percent int) int16 {
	v := int16(min(100, max(0, percent)))
	return fixed.Div32x16(fixed.Mult16x16(fixed.Q15One, v), 100)
}

// SetProbStart is SPEEX_PREPROCESS_SET_PROB_START: the speech probability,
// in percent clamped to 0 to 100, above which a frame starts speech.
func (p *Preprocessor) SetProbStart(percent int) { p.probStart = probQ15(percent) }

// ProbStart is SPEEX_PREPROCESS_GET_PROB_START, 35 after New. It truncates
// as the reference does, so SetProbStart(35) reads back as 34.
func (p *Preprocessor) ProbStart() int { return int(fixed.Mult16x16Q15(p.probStart, 100)) }

// SetProbContinue is SPEEX_PREPROCESS_SET_PROB_CONTINUE: the threshold, in
// percent clamped to 0 to 100, that keeps speech going after a speech frame.
func (p *Preprocessor) SetProbContinue(percent int) { p.probContinue = probQ15(percent) }

// ProbContinue is SPEEX_PREPROCESS_GET_PROB_CONTINUE, 20 after New; it
// truncates as ProbStart does.
func (p *Preprocessor) ProbContinue() int { return int(fixed.Mult16x16Q15(p.probContinue, 100)) }

// SetNoiseSuppress is SPEEX_PREPROCESS_SET_NOISE_SUPPRESS: the maximum noise
// attenuation in dB, stored as -|dB| (default -15); large values wrap as in C.
func (p *Preprocessor) SetNoiseSuppress(dB int32) { p.noiseSuppress = -fixed.Abs32(dB) }

// NoiseSuppress is SPEEX_PREPROCESS_GET_NOISE_SUPPRESS, in dB (-15 after
// New).
func (p *Preprocessor) NoiseSuppress() int32 { return p.noiseSuppress }

// SetEchoSuppress is SPEEX_PREPROCESS_SET_ECHO_SUPPRESS: residual-echo
// attenuation in dB without near-end speech, stored as -|dB| (default -40).
func (p *Preprocessor) SetEchoSuppress(dB int32) { p.echoSuppress = -fixed.Abs32(dB) }

// EchoSuppress is SPEEX_PREPROCESS_GET_ECHO_SUPPRESS, in dB (-40 after
// New).
func (p *Preprocessor) EchoSuppress() int32 { return p.echoSuppress }

// SetEchoSuppressActive is SPEEX_PREPROCESS_SET_ECHO_SUPPRESS_ACTIVE: the
// same during near-end speech, stored as -|dB| (default -15).
func (p *Preprocessor) SetEchoSuppressActive(dB int32) { p.echoSuppressActive = -fixed.Abs32(dB) }

// EchoSuppressActive is SPEEX_PREPROCESS_GET_ECHO_SUPPRESS_ACTIVE, in dB
// (-15 after New).
func (p *Preprocessor) EchoSuppressActive() int32 { return p.echoSuppressActive }

// SpeechProb is SPEEX_PREPROCESS_GET_PROB: the last frame's speech score in
// percent, which drives the VAD; a heuristic, not a calibrated probability.
func (p *Preprocessor) SpeechProb() int { return int(fixed.Mult16x16Q15(p.speechProb, 100)) }

// PSDLen is SPEEX_PREPROCESS_GET_PSD_SIZE and GET_NOISE_PSD_SIZE: the frame
// size.
func (p *Preprocessor) PSDLen() int { return p.frameSize }

// PSD is SPEEX_PREPROCESS_GET_PSD: the last frame's power spectrum into
// dst[:PSDLen()], or ErrBufferSize if dst is shorter.
func (p *Preprocessor) PSD(dst []int32) error {
	if p.fft == nil {
		return ErrConfig
	}
	if len(dst) < p.frameSize {
		return ErrBufferSize
	}
	copy(dst, p.ps[:p.frameSize])
	return nil
}

// NoisePSD is SPEEX_PREPROCESS_GET_NOISE_PSD: it writes the noise estimate
// into dst[:PSDLen()], or returns ErrBufferSize if dst is shorter.
func (p *Preprocessor) NoisePSD(dst []int32) error {
	if p.fft == nil {
		return ErrConfig
	}
	if len(dst) < p.frameSize {
		return ErrBufferSize
	}
	for i := range p.frameSize {
		dst[i] = fixed.PShr32(p.noise[i], noiseShift)
	}
	return nil
}
