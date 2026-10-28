// Package buffer provides fixed-capacity FIFO ring buffers of int16 samples
// for streaming audio: Ring for a single owner, and SPSC, a wait-free
// single-producer, single-consumer buffer for the hand-off between the audio
// path and a device or network goroutine.
//
// Parity: own design; contains no SpeexDSP code. SpeexDSP's [buffer.c] was
// design input only, and docs/algorithms.md says where this package differs.
//
// Terms such as view, producer, consumer, wait-free and backlog are defined
// in the [glossary]. The developer guide has diagrams of the
// [SPSC hand-off] and of [view lifetimes].
//
// # Use
//
// Make a buffer once, before the stream starts: New for a Ring, NewSPSC for
// an SPSC. Both return an error wrapping ErrConfig, and nothing else, for
// a capacity outside 1 to MaxCapacity or, for a Ring, an Overflow other
// than Reject or Overwrite. Then write and read any number of samples per
// call. Reset empties a buffer for a new stream.
//
// Choose Ring when one goroutine both writes and reads, for example a queue
// that collects resampler output until a whole frame is there. Choose SPSC
// when one goroutine writes and another reads, for example a network
// reader handing received frames to the audio goroutine. Size an SPSC for
// the longest stall of its consumer that must lose nothing: a capacity of
// rate/5 samples holds 200 ms.
//
// To keep frame boundaries when frames go through a buffer, write a whole
// frame or nothing (check Free first) and read only when Len holds a whole
// frame. A short Write stores a prefix and would shift every later frame.
//
// # Samples, rates and chunking
//
// Samples are int16 on any rate; the buffers do not interpret them. Every
// method takes any length, including zero, and there is no frame size: a
// stream written and read in any partition of chunk sizes comes out the same,
// as long as no write overflows.
//
// # Capacity and overflow
//
// The capacity and the overflow policy are fixed at construction; to change
// either, make a new buffer. The capacity is from 1 to MaxCapacity samples, and
// every slot is usable: Len reaches Cap. Ring's Config chooses the overflow
// policy explicitly. Under Reject, Write stores as much of the input as fits
// (p[:n]) and returns n; samples already stored are untouched. Under
// Overwrite, Write stores the last min(len(p), Cap()) samples of p, discards
// as many of the oldest unread samples as needed, and returns the number
// stored; the caller can compute the number discarded from Len before and
// after. SPSC always rejects. Read returns fewer samples than asked for when
// fewer are stored, and never pads. A zero Ring or SPSC (not made by New or
// NewSPSC) is an empty buffer of capacity 0: every method returns 0 or empty
// views and changes nothing.
//
// # Views
//
// ReadSlices returns the readable samples and WriteSlices the free space, each
// as two segments (the second is empty unless the region wraps). Consume and
// Commit finish a zero-copy read or write. Consume clips its count to Len.
// Commit publishes the next samples of the write view and clips its count to
// what remains of that view, so it never publishes a slot the caller was not
// given, even when reads have freed more space since; without a live write
// view it returns 0. Each segment's capacity equals its length, so append on a
// view cannot write into the buffer. A view expires as follows,
// and the buffer never writes into a live read view or reads a live write
// view:
//
//   - Ring read view: valid until the next Read, Consume or Reset, or a Write
//     under Overwrite. Writes and commits under Reject leave it intact (it
//     does not grow to include their samples).
//   - Ring write view: valid until the next Write, WriteSlices or Reset. Reads
//     and consumes leave it intact. After Commit(n), only the part after the
//     first n samples is still a write view.
//   - SPSC read view (consumer): valid until the consumer's next Read, Consume
//     or Reset. The producer never writes into it.
//   - SPSC write view (producer): valid until the producer's next Write,
//     WriteSlices or Reset, with Commit as for Ring. The consumer never reads
//     a sample of it before Commit publishes that sample.
//
// Write copies from its argument and Read into its argument; neither retains
// a caller slice. A view must not be passed to Write or Read of the same
// buffer.
//
// # Reset
//
// Reset empties the buffer, keeps its storage and does not allocate. Afterwards
// the buffer behaves as a fresh one with the same configuration. It expires
// every view, because the positions restart at the beginning of the storage.
//
// # Concurrency
//
// A Ring is not safe for concurrent use. Use one per stream.
//
// An SPSC is safe for exactly one producer goroutine and one consumer
// goroutine at the same time. Producer methods: Write, WriteSlices, Commit,
// Free. Consumer methods: Read, ReadSlices, Consume, Len. Cap may be called
// from either side. Each side must be used from one goroutine at a time (or
// handed over with a synchronising operation). Called from the other side, Len
// and Free are snapshots that may already be stale. Reset needs both sides
// quiescent and synchronised with the caller, for example by joining their
// goroutines.
//
// Every SPSC method is wait-free: at most two atomic loads, at most one atomic
// store of the caller's own index (Reset, which needs quiescence, stores both),
// and one copy. There is no lock, no compare-and-swap and no retry, so neither
// side can block or delay the other. The memory-ordering argument and the
// interleaving trace are in docs/algorithms.md.
//
// # Dropping a backlog
//
// The SPSC never overwrites unread samples, so after a stall the consumer may
// find a backlog of old audio. Consume drops samples without reading them,
// with no read view needed first. To skip to recent audio and keep only the
// newest k samples, for 0 <= k:
//
//	s.Consume(s.Len() - k)
//
// This drops the oldest Len()-k samples (nothing if Len() <= k) and returns how
// many it dropped. Samples the producer publishes in between are kept, so at
// least the newest min(k, Len()) remain. Like every Consume, it is wait-free,
// makes the dropped slots free for the producer, and expires any open read
// view.
//
// # Allocation and latency
//
// New and NewSPSC allocate; no other method allocates. Every method's cost is
// proportional to the number of samples it copies, at most min(len(p), Cap()).
// The buffers add exactly the latency of the samples stored in them.
//
// # Errors
//
// Only New and NewSPSC return errors, wrapping ErrConfig; test with
// errors.Is. No other method returns an error or panics, for any
// argument: full and empty show in the counts. Write returns how many
// samples it stored, Read how many it read, and Consume and Commit how
// many they dropped or published. Compare the count with what was asked
// for to detect a full or empty buffer.
//
// [buffer.c]: https://gitlab.xiph.org/xiph/speexdsp/-/blob/1b28a0f61bc31162979e1f26f3981fc3637095c8/libspeexdsp/buffer.c
// [glossary]: https://github.com/danieldonoghue/govoip-dsp/blob/master/docs/glossary.md
// [SPSC hand-off]: https://github.com/danieldonoghue/govoip-dsp/blob/master/docs/guide.md#handing-audio-between-goroutines
// [view lifetimes]: https://github.com/danieldonoghue/govoip-dsp/blob/master/docs/guide.md#buffer-ownership-and-views
package buffer
