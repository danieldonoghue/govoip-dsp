// Package buffer provides fixed-capacity FIFO ring buffers of int16 samples
// for streaming audio: Ring for a single owner, and SPSC, a wait-free
// single-producer, single-consumer buffer for the hand-off between the audio
// path and a device or network goroutine.
//
// Parity: own design; contains no SpeexDSP code. SpeexDSP's buffer.c was
// design input only, and docs/algorithms.md says where this package differs.
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
// The capacity is fixed at construction, from 1 to MaxCapacity samples, and
// every slot is usable: Len reaches Cap. Ring's Config chooses the overflow
// policy explicitly. Under Reject, Write stores as much of the input as fits
// (p[:n]) and returns n; samples already stored are untouched. Under
// Overwrite, Write stores the last min(len(p), Cap()) samples of p, discards
// as many of the oldest unread samples as needed, and returns the number
// stored; the caller can compute the number discarded from Len before and
// after. SPSC always rejects. Read returns fewer samples than asked for when
// fewer are stored, and never pads.
//
// # Views
//
// ReadSlices returns the readable samples and WriteSlices the free space, each
// as two segments (the second is empty unless the region wraps). Consume and
// Commit finish a zero-copy read or write; both clip their count to what is
// available and return it. Each segment's capacity equals its length, so
// append on a view cannot write into the buffer. A view expires as follows,
// and the buffer never writes into a live read view or reads a live write
// view:
//
//   - Ring read view: valid until the next Read, Consume or Reset, or a Write
//     under Overwrite. Writes and commits under Reject leave it intact (it
//     does not grow to include their samples).
//   - Ring write view: valid until the next Write, Commit or Reset. Reads and
//     consumes leave it intact.
//   - SPSC read view (consumer): valid until the consumer's next Read, Consume
//     or Reset. The producer never writes into it.
//   - SPSC write view (producer): valid until the producer's next Write, Commit
//     or Reset. The consumer never reads it before Commit.
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
// store of the caller's own index, and one copy. There is no lock, no
// compare-and-swap and no retry, so neither side can block or delay the other.
// The memory-ordering argument and the interleaving trace are in
// docs/algorithms.md.
//
// # Allocation and latency
//
// New and NewSPSC allocate; no other method allocates. Every method's cost is
// proportional to the number of samples it copies, at most min(len(p), Cap()).
// The buffers add exactly the latency of the samples stored in them.
package buffer
