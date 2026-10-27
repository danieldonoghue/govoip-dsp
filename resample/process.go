// Derived from SpeexDSP 1.2.1 (https://github.com/xiph/speexdsp, commit
// 1b28a0f61bc31162979e1f26f3981fc3637095c8): libspeexdsp/resample.c.
//
// Copyright (C) 2007-2008 Jean-Marc Valin
// Copyright (C) 2008      Thorvald Natvig
//
// Use of this source is governed by the BSD-3-Clause licence in
// LICENSES/SpeexDSP-COPYING. Changes: ported to Go, with explicit strides
// and slices in place of pointers.

package resample

import (
	"math"

	"github.com/danieldonoghue/govoip-dsp/internal/fixed"
)

// runKernel is the call through resampler_ptr: it reads in[:inLen] of one
// channel's memory and writes up to outLen samples to out[off+k*stride].
func (r *Resampler) runKernel(ch int, in []int16, inLen uint32, out []int16, off, stride int, outLen uint32) uint32 {
	switch r.kernel {
	case kernelDirect:
		return r.direct(ch, in, inLen, out, off, stride, outLen)
	case kernelInterpolate:
		return r.interpolate(ch, in, inLen, out, off, stride, outLen)
	}
	return r.zero(ch, inLen, out, off, stride, outLen)
}

// direct is resampler_basic_direct_single (resample.c:331).
func (r *Resampler) direct(ch int, in []int16, inLen uint32, out []int16, off, stride int, outLen uint32) uint32 {
	n := int(r.filtLen)
	outSample := int32(0)
	lastSample := r.lastSample[ch]
	samp := r.sampFracNum[ch]
	intAdvance, fracAdvance, den := r.intAdvance, uint32(r.fracAdvance), r.denRate
	for !(lastSample >= int32(inLen) || outSample >= int32(outLen)) {
		sinct := r.sincTable[int(samp*r.filtLen):][:n]
		iptr := in[lastSample:][:n]
		var sum int32
		for j, s := range sinct {
			sum += fixed.Mult16x16(s, iptr[j])
		}
		out[off+stride*int(outSample)] = int16(fixed.Saturate32PShr(sum, 15, 32767))
		outSample++
		lastSample += intAdvance
		samp += fracAdvance
		if samp >= den {
			samp -= den
			lastSample++
		}
	}
	r.lastSample[ch] = lastSample
	r.sampFracNum[ch] = samp
	return uint32(outSample)
}

// interpolate is resampler_basic_interpolate_single (resample.c:438).
func (r *Resampler) interpolate(ch int, in []int16, inLen uint32, out []int16, off, stride int, outLen uint32) uint32 {
	n := int(r.filtLen)
	outSample := int32(0)
	lastSample := r.lastSample[ch]
	samp := r.sampFracNum[ch]
	intAdvance, fracAdvance, den := r.intAdvance, uint32(r.fracAdvance), r.denRate
	ov := r.oversample
	step := int(ov)
	for !(lastSample >= int32(inLen) || outSample >= int32(outLen)) {
		iptr := in[lastSample:][:n]
		// The uint32 product wraps as in C; offset stays below oversample.
		offset := int(samp * ov / den)
		frac := int16(fixed.PDiv32(fixed.Shl32(int32((samp*ov)%den), 15), int32(den)))
		// Table index 4+(j+1)*oversample-offset-2 for j = 0.
		k := 2 + step - offset
		table := r.sincTable[k : k+(n-1)*step+4]
		var a0, a1, a2, a3 int32
		for j, cur := range iptr {
			t := table[j*step:][:4]
			a0 += fixed.Mult16x16(cur, t[0])
			a1 += fixed.Mult16x16(cur, t[1])
			a2 += fixed.Mult16x16(cur, t[2])
			a3 += fixed.Mult16x16(cur, t[3])
		}
		i0, i1, i2, i3 := cubicCoef(frac)
		sum := fixed.Mult16x32Q15(i0, a0) + fixed.Mult16x32Q15(i1, a1) + fixed.Mult16x32Q15(i2, a2) + fixed.Mult16x32Q15(i3, a3)
		out[off+stride*int(outSample)] = int16(fixed.Saturate32PShr(sum, 15, 32767))
		outSample++
		lastSample += intAdvance
		samp += fracAdvance
		if samp >= den {
			samp -= den
			lastSample++
		}
	}
	r.lastSample[ch] = lastSample
	r.sampFracNum[ch] = samp
	return uint32(outSample)
}

// zero is resampler_basic_zero (resample.c:565). Its ratio may reach 2^32-1,
// so the position is int64 and saturates at MaxInt32 where C would overflow.
func (r *Resampler) zero(ch int, inLen uint32, out []int16, off, stride int, outLen uint32) uint32 {
	outSample := int32(0)
	lastSample := int64(r.lastSample[ch])
	samp := r.sampFracNum[ch]
	intAdvance, fracAdvance, den := int64(r.numRate/r.denRate), uint32(r.fracAdvance), r.denRate
	for !(lastSample >= int64(inLen) || outSample >= int32(outLen)) {
		out[off+stride*int(outSample)] = 0
		outSample++
		lastSample += intAdvance
		samp += fracAdvance
		if samp >= den {
			samp -= den
			lastSample++
		}
		lastSample = min(lastSample, math.MaxInt32)
	}
	r.lastSample[ch] = int32(lastSample)
	r.sampFracNum[ch] = samp
	return uint32(outSample)
}

// processNative is speex_resampler_process_native (resample.c:878).
func (r *Resampler) processNative(ch int, inLen *uint32, out []int16, off, stride int, outLen *uint32) {
	n := int(r.filtLen)
	mem := r.mem[ch*int(r.memAllocSize):]
	r.started = true
	outSample := r.runKernel(ch, mem, *inLen, out, off, stride, *outLen)
	if r.lastSample[ch] < int32(*inLen) {
		*inLen = uint32(r.lastSample[ch])
	}
	*outLen = outSample
	r.lastSample[ch] -= int32(*inLen)
	ilen := int(*inLen)
	copy(mem[:n-1], mem[ilen:ilen+n-1])
}

// magic is speex_resampler_magic (resample.c:904). It advances *off past
// the samples it writes.
func (r *Resampler) magic(ch int, out []int16, off *int, stride int, outLen uint32) uint32 {
	tmpInLen := r.magicSamples[ch]
	mem := r.mem[ch*int(r.memAllocSize):]
	n := int(r.filtLen)
	r.processNative(ch, &tmpInLen, out, *off, stride, &outLen)
	r.magicSamples[ch] -= tmpInLen
	if m := int(r.magicSamples[ch]); m != 0 {
		t := int(tmpInLen)
		copy(mem[n-1:n-1+m], mem[n-1+t:n-1+t+m])
	}
	*off += int(outLen) * stride
	return outLen
}

// processInt is speex_resampler_process_int (resample.c:925) with the input
// stride and the output stride as arguments.
func (r *Resampler) processInt(ch int, in []int16, istride int, inLen *uint32, out []int16, ostride int, outLen *uint32) error {
	ilen, olen := *inLen, *outLen
	x := r.mem[ch*int(r.memAllocSize):]
	filtOffs := int(r.filtLen) - 1
	xlen := r.memAllocSize - uint32(filtOffs)
	inOff, outOff := 0, 0
	if r.magicSamples[ch] != 0 {
		olen -= r.magic(ch, out, &outOff, ostride, olen)
	}
	if r.magicSamples[ch] == 0 {
		for ilen != 0 && olen != 0 {
			ichunk := min(ilen, xlen)
			ochunk := olen
			dst := x[filtOffs : filtOffs+int(ichunk)]
			for j := range dst {
				dst[j] = in[inOff+j*istride]
			}
			r.processNative(ch, &ichunk, out, outOff, ostride, &ochunk)
			ilen -= ichunk
			olen -= ochunk
			outOff += int(ochunk) * ostride
			inOff += int(ichunk) * istride
		}
	}
	*inLen -= ilen
	*outLen -= olen
	if r.kernel == kernelZero {
		return ErrAllocFailed
	}
	return nil
}

// processFloat is speex_resampler_process_float, FIXED_POINT (resample.c:966):
// input saturates through WORD2INT; output goes in fixedStackAlloc chunks.
func (r *Resampler) processFloat(ch int, in []float32, istride int, inLen *uint32, out []float32, ostride int, outLen *uint32) error {
	ilen, olen := *inLen, *outLen
	x := r.mem[ch*int(r.memAllocSize):]
	xlen := r.memAllocSize - (r.filtLen - 1)
	ylen := min(olen, fixedStackAlloc)
	ystack := r.ystack[:]
	inOff, outOff := 0, 0
	for ilen != 0 && olen != 0 {
		y := 0
		ichunk := min(ilen, xlen)
		ochunk := min(olen, ylen)
		omagic := uint32(0)
		if r.magicSamples[ch] != 0 {
			omagic = r.magic(ch, ystack, &y, 1, ochunk)
			ochunk -= omagic
			olen -= omagic
		}
		if r.magicSamples[ch] == 0 {
			filtOffs := int(r.filtLen) - 1
			dst := x[filtOffs : filtOffs+int(ichunk)]
			for j := range dst {
				dst[j] = fixed.Word2IntFloat32(in[inOff+j*istride])
			}
			r.processNative(ch, &ichunk, ystack, y, 1, &ochunk)
		} else {
			ichunk = 0
			ochunk = 0
		}
		for j, v := range ystack[:ochunk+omagic] {
			out[outOff+j*ostride] = float32(v)
		}
		ilen -= ichunk
		olen -= ochunk
		outOff += int(ochunk+omagic) * ostride
		inOff += int(ichunk) * istride
	}
	*inLen -= ilen
	*outLen -= olen
	if r.kernel == kernelZero {
		return ErrAllocFailed
	}
	return nil
}

// frames returns how many strided elements a slice of length n holds,
// clamped to maxFrames.
func frames(n, stride int) uint32 {
	if n == 0 {
		return 0
	}
	return uint32(min(1+(n-1)/stride, maxFrames))
}

// from returns s[i:], or nil when s is too short, so that a call offering
// zero frames on a later channel never slices out of range.
func from[T int16 | float32](s []T, i int) []T {
	if i >= len(s) {
		return nil
	}
	return s[i:]
}

// Process resamples interleaved int16 frames of every channel
// (speex_resampler_process_interleaved_int). It returns the frames per
// channel read from in and written to out. len(in) and len(out) must be
// multiples of Channels(). A result shorter than offered means out was full
// or, for in, that the rest must be offered again.
func (r *Resampler) Process(in, out []int16) (inFrames, outFrames int, err error) {
	nch := int(r.nbChannels)
	if len(in)%nch != 0 || len(out)%nch != 0 {
		return 0, 0, ErrBufferSize
	}
	bakIn, bakOut := frames(len(in), nch), frames(len(out), nch)
	var il, ol uint32
	for i := 0; i < nch; i++ {
		il, ol = bakIn, bakOut
		err = r.processInt(i, from(in, i), nch, &il, from(out, i), nch, &ol)
	}
	return int(il), int(ol), err
}

// ProcessFloat is Process for float32 samples on the 16-bit scale
// (speex_resampler_process_interleaved_float). Input saturates to int16 and
// truncates toward zero; NaN becomes 0. Output values are exact copies of
// the int16 results.
func (r *Resampler) ProcessFloat(in, out []float32) (inFrames, outFrames int, err error) {
	nch := int(r.nbChannels)
	if len(in)%nch != 0 || len(out)%nch != 0 {
		return 0, 0, ErrBufferSize
	}
	bakIn, bakOut := frames(len(in), nch), frames(len(out), nch)
	var il, ol uint32
	for i := 0; i < nch; i++ {
		il, ol = bakIn, bakOut
		err = r.processFloat(i, from(in, i), nch, &il, from(out, i), nch, &ol)
	}
	return int(il), int(ol), err
}

// ProcessChannel resamples one channel (speex_resampler_process_int with
// the input and output strides set). It reads in[k*inStride] and writes
// out[k*outStride]; the number of frames offered is the number of such
// indices in range. Other elements of out are not written.
func (r *Resampler) ProcessChannel(ch int, in []int16, inStride int, out []int16, outStride int) (inFrames, outFrames int, err error) {
	if ch < 0 || ch >= int(r.nbChannels) {
		return 0, 0, ErrChannel
	}
	if inStride < 1 || outStride < 1 {
		return 0, 0, ErrStride
	}
	il, ol := frames(len(in), inStride), frames(len(out), outStride)
	err = r.processInt(ch, in, inStride, &il, out, outStride, &ol)
	return int(il), int(ol), err
}

// ProcessChannelFloat is ProcessChannel for float32 samples, with the
// conversions of ProcessFloat (speex_resampler_process_float).
func (r *Resampler) ProcessChannelFloat(ch int, in []float32, inStride int, out []float32, outStride int) (inFrames, outFrames int, err error) {
	if ch < 0 || ch >= int(r.nbChannels) {
		return 0, 0, ErrChannel
	}
	if inStride < 1 || outStride < 1 {
		return 0, 0, ErrStride
	}
	il, ol := frames(len(in), inStride), frames(len(out), outStride)
	err = r.processFloat(ch, in, inStride, &il, out, outStride, &ol)
	return int(il), int(ol), err
}
