// Package resample converts audio between sampling rates. It is a port of
// the SpeexDSP 1.2.1 resampler (resample.c, FIXED_POINT build, no SIMD, no
// RESAMPLE_FULL_SINC_TABLE).
//
// Parity: bit-exact with SpeexDSP 1.2.1 speex_resampler_* (FIXED_POINT) for
// the configurations in docs/acceptance/resample.md: qualities 0 to 10, every
// pairing of 8, 16, 24, 32, 44.1, 48 and 96 kHz, fractional ratios,
// multichannel, strides, float32 entry points, and rate, quality, reset and
// skip-zeros changes. The sinc tables are built with Go's math.Sin in place
// of libm; they equal the harness tables entry for entry in every tested
// configuration.
//
// Terms such as sample rate, channel, interleaved, stride, quality level,
// ratio and latency are defined in the [glossary]. In this package a frame
// is one sample of every channel (the glossary's resampler frame): a mono
// frame is one sample, an interleaved stereo frame two. The developer
// guide shows the [receive path]. The SpeexDSP documentation of the same
// functions is the Speex manual's [resampler section] and the header
// [speex_resampler.h].
//
// # Use
//
// Make one Resampler per stream with New, size the output with OutLen,
// and pass each chunk to Process:
//
//	r, err := resample.New(resample.Config{Channels: 1, InRate: 48000, OutRate: 16000, Quality: resample.QualityVoIP})
//	if err != nil {
//		return err // wraps ErrConfig, or is ErrAllocFailed
//	}
//	in := make([]int16, 480)               // 10 ms at 48 kHz
//	out := make([]int16, r.OutLen(len(in))) // large enough for one call
//	nIn, nOut, err := r.Process(in, out)
//	// out[:nOut] is the output; nIn == len(in), because out is OutLen long
//
// Reset restarts the stream with the same settings. A higher quality
// (Config.Quality, 0 to 10) gives less noise and distortion at more cost
// and latency. The Speex manual calls 3 (QualityVoIP) acceptable for most
// desktop uses and recommends 10 mostly for professional audio; the tested
// recipes of docs/usage.md use QualityVoIP. Resample each stream that must
// stay sample-aligned with its own Resampler of the same configuration,
// for example the microphone and the loudspeaker signals before an echo
// canceller.
//
// # Rates, ratios and frames
//
// Any ratio num/den with 1 <= num, den <= 2^32-1 is accepted where the
// reference accepts it (where int is 32 bits wide, values are limited to
// 2^31-1), within a Go-only resource cap: the sinc table plus the history
// of all channels may hold at most 16 * 1024 * 1024 entries (32 MiB), and
// there may be at most 256 channels. That is about 158 times the largest
// need of any pairing of 8 to 96 kHz at any quality with 32 channels.
// Above the cap, and where the filter length would wrap to 0 (undefined in
// C), New, SetRate, SetRateFrac and SetQuality return ErrAllocFailed and
// change nothing. Where the reference's ratio or table limits reject the
// filter, they return it and follow C (see ErrAllocFailed); the
// reference's memory limit is always above the cap. A filter is roughly 8
// to 256 taps times the downsampling factor, so the cap allows
// downsampling by about 21000 to 32000 (mono, quality 10). The cap bounds
// each configuration; since buffers only grow, a sequence of changes can
// hold somewhat more (measured: 1.14 times the cap). Frames may be any
// length; a call may consume and produce nothing.
//
// # Chunking and output exhaustion
//
// Each process call reads up to the offered input and writes up to the
// offered output, and returns how many frames it read and wrote. It stops
// when either runs out. When out is too short, the unread input must be
// offered again in a later call. Any partition of a stream into calls,
// with any output capacities, gives the same concatenated output, provided
// all input is eventually consumed. Roughly, a call produces
// in*OutRate/InRate frames; the first OutputLatency output frames
// correspond to the filter delay (see SkipZeros).
//
// OutLen(n), an extension with no SpeexDSP equivalent, gives the output
// frames per channel to offer a call of n input frames per channel so
// that it consumes all of them: the frames the call can write, from the
// current position and any magic samples pending after a filter change,
// plus 1 when n > 0. For Process and ProcessFloat the buffer is OutLen(n)
// times Channels() samples. The result is clamped to 2^31-1, where the
// guarantee no longer holds (see OutLen).
//
// Interleaved calls (Process, ProcessFloat) process every channel with the
// same offered lengths and report the counts of the last channel, as the
// reference does. ProcessChannel and ProcessChannelFloat process one channel
// through strides, and let channels advance independently.
//
// Offered lengths above 2^31-1 frames are clamped to 2^31-1. C would stop
// making progress there. C feeds zeros for a NULL input; here, pass zeros.
// After a rejected rate change, the input position saturates at 2^31-1
// where C's int position would overflow. With a ratio of 2^31 or more the
// output is zeros either way, but the number of zeros produced can differ
// from the -fwrapv reference build.
//
// # Buffers, aliasing and retention
//
// in and out are caller-provided. They must not overlap; overlap is not
// detected. No method retains a caller slice. Output elements outside the
// strided positions written are not touched. On a zero Resampler (not made
// by New) the methods with an error result return ErrConfig (and 0 frames),
// the others return 0, and SkipZeros and Reset do nothing.
//
// # Float32 samples
//
// float32 values are on the 16-bit scale, not [-1, 1]. Input converts with
// the reference's WORD2INT: below -32767 gives -32768, above 32766 gives
// 32767, anything else truncates toward zero. ±Inf saturates. NaN becomes 0,
// a deviation: C is undefined there. Output values are exact copies of the
// int16 results.
//
// # Settings and Reset
//
// SetRate, SetRateFrac and SetQuality change the filter on the fly with the
// reference semantics (update_filter): the stream position and history are
// kept. A setting equal to the current one does nothing. They may allocate.
// An ErrAllocFailed from the cap changes nothing; one from the reference's
// ratio or table limits leaves the resampler outputting zeros until a
// later change succeeds (see ErrAllocFailed). If SetRateFrac returns
// ErrOverflow, nothing changes; C changes part of its state there, after
// which its behaviour is undefined. The channel count needs a new Resampler.
//
// Reset is speex_resampler_reset_mem: it keeps the rates, ratio and quality
// and restarts the stream. For a mono stream, output after Reset equals
// that of a new Resampler fed the same input, unless the filter is changed
// before the next process call (the reference then keeps the history
// layout of a started stream). For more than one channel, Reset, like the
// reference, clears only the first Channels()*(filt_len-1) samples of the
// filter memory, so channels after the first may keep old history.
//
// # Latency
//
// InputLatency is half the filter length in input samples; OutputLatency is
// the same delay in output samples. Per-quality figures are in
// docs/algorithms.md.
//
// # Errors
//
// New returns an error wrapping ErrConfig for an invalid Config, or
// ErrAllocFailed where the filter exceeds the size limits; test them with
// errors.Is. The setters return the sentinels unwrapped: ErrConfig for an
// invalid argument, ErrAllocFailed and ErrOverflow as described above. The
// process methods return ErrBufferSize for an interleaved length that is
// not a multiple of Channels(), ErrChannel for a channel index out of
// range, ErrStride for a stride below 1, ErrAllocFailed while a rejected
// filter change is in force, and ErrConfig on a zero Resampler; they
// return 0 frames then. With buffers made as in "Use", none of these
// occurs on the audio path, so treat them as programming errors.
//
// # Limitations
//
// The ratio is fixed between setter calls: clock drift between two
// devices is not corrected (a drift controller is planned). There is no
// NULL-input form: to flush the filter at the end of a stream, pass
// InputLatency() zeros, which push the last input samples out.
//
// # Concurrency and allocation
//
// A Resampler is not safe for concurrent use. Use one per stream.
//
// New allocates. SetRate, SetRateFrac and SetQuality allocate when the new
// filter needs a larger table or memory than any earlier one. No other
// method allocates. The process methods are bounded: their work is
// proportional to the offered lengths times the filter length.
//
// [glossary]: https://github.com/danieldonoghue/govoip-dsp/blob/master/docs/glossary.md
// [receive path]: https://github.com/danieldonoghue/govoip-dsp/blob/master/docs/guide.md#the-receive-path
// [resampler section]: https://www.speex.org/docs/manual/speex-manual/node7.html#SECTION00760000000000000000
// [speex_resampler.h]: https://gitlab.xiph.org/xiph/speexdsp/-/blob/1b28a0f61bc31162979e1f26f3981fc3637095c8/include/speex/speex_resampler.h
package resample
