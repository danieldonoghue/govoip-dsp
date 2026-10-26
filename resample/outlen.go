package resample

import "math"

// OutLen returns the output frames per channel to offer one process call
// that is offered inFrames input frames per channel, in the current state:
// with that much output space (times Channels() for Process and
// ProcessFloat), the call consumes all its input. It is the number of
// frames the call can write, counting magic samples pending after a filter
// change, plus 1 when inFrames > 0, because a call stops as soon as its
// output is full, even if the input left would write nothing. Where the
// fractional position can wrap (den + (num mod den) - 1 at least 2^32), the
// output is not bounded by the input and OutLen returns 2^31-1, the largest
// length a call accepts. The result is clamped to 2^31-1, and then the
// guarantee does not hold: strong upsampling (for example 1:1000 with
// millions of input frames) can need more. Multiplying by Channels() can
// overflow int where int is 32 bits wide. Negative inFrames gives 0.
//
// OutLen is an extension; SpeexDSP has no equivalent. It does not allocate
// or change state.
func (r *Resampler) OutLen(inFrames int) int {
	if inFrames < 0 {
		inFrames = 0
	}
	num, den := uint64(r.numRate), uint64(r.denRate)
	if den-1+num%den > math.MaxUint32 {
		return maxFrames
	}
	in := uint64(min(inFrames, maxFrames))
	best := uint64(0)
	for ch := range r.lastSample {
		best = max(best, outputsBefore(uint64(r.magicSamples[ch])+in, r.lastSample[ch], r.sampFracNum[ch], num, den))
	}
	if in > 0 {
		best++
	}
	return int(min(best, maxFrames))
}

// outputsBefore counts the output positions L + floor((s + k*num)/den),
// k >= 0, that lie below t: the kernels produce one output for each, over
// the magic samples and the offered input taken together.
func outputsBefore(t uint64, lastSample int32, samp uint32, num, den uint64) uint64 {
	l := uint64(max(lastSample, 0))
	if t <= l {
		return 0
	}
	x := (t-l)*den - uint64(samp)
	return (x + num - 1) / num
}
