# Acceptance criteria: `buffer`

Pre-registered before the implementation, as requirements §9 demands. Changes
after the first implementation commit need a written justification in the
"Changes" section at the end of this file.

## Parity class

**None.** Own design; the package contains no SpeexDSP code. SpeexDSP's
`buffer.c` is design input only, so there are no reference fixtures. The
criteria below are functional invariants, checked by unit tests, by fuzzing
against a trivially correct model and, for the SPSC type, by `-race` stress
tests.

## Types covered

1. `Ring`: single-owner ring buffer of `int16`, no synchronisation, overflow
   policy `Reject` or `Overwrite` chosen in its `Config`.
2. `SPSC`: lock-free single-producer, single-consumer ring buffer of
   `int16`. Every configuration of both types is covered: every capacity
   from 1 to `MaxCapacity` is valid, and the tests use capacities 1, 2, 3,
   7, 64, 160, 1000 and 4096 plus fuzzed capacities from 1 to 300.

## Model

The model is a plain Go slice used as a queue: `Write` appends and, under
`Overwrite`, drops from the front until the length is at most the capacity;
`Read` takes from the front. It has no wrap-around, so it is correct by
inspection. "Equals the model" below means: the same return value, the same
`Len`, and the readable contents (both view segments, concatenated) equal to
the model slice, checked after every operation.

## Invariants (both types, where the operation exists)

1. **Conservation.** Samples are neither created nor lost, except the oldest
   unread samples that `Overwrite` discards: after every operation,
   written − read − discarded = `Len`, and 0 ≤ `Len` ≤ `Cap`.
2. **Order.** Reads return samples in the order they were written, with no
   duplicates.
3. **Reject.** `Write(p)` stores `p[:n]` with n = min(len(p), `Free`) and
   returns n. Samples already in the buffer are untouched.
4. **Overwrite.** `Write(p)` stores the last n = min(len(p), `Cap`) samples
   of `p`, discarding the oldest unread samples to make room, and returns n.
   Afterwards the contents are the last min(`Cap`, old `Len` + len(p))
   samples of the old contents followed by `p`.
5. **Zero length.** `Write` and `Read` with an empty or nil slice, and
   `Commit` or `Consume` of zero or a negative count, return 0 and change
   nothing.
6. **Full and empty.** No slot is wasted: `Len` reaches `Cap` for every
   capacity, including 1, and a full buffer is distinguishable from an empty
   one.
7. **Views.** The read view (two segments) holds exactly the readable
   samples in order; the write view (two segments) has total length `Free`.
   Each segment's capacity equals its length, so `append` on a view cannot
   write into the ring. `Commit(n)` clips n to [0, length of the current
   write view] (0 when no write view is live), `Consume(n)` clips n to
   [0, `Len`], and both return the clipped count.
8. **View expiry.** Each view's documented lifetime holds: while a view is
   live, the buffer writes no sample of a read view and reads no sample of a
   write view. Tested for `Ring` by holding a read view across `Reject`
   writes that fill the buffer, and a write view across reads; tested for
   `SPSC` in the stress test (criterion S3).
9. **Reset.** `Reset` empties the buffer without allocating, and afterwards
   the buffer behaves exactly as a fresh one with the same configuration
   (same results for the same operation sequence).
10. **Chunk partition.** Writing and reading one stream in any partition of
    chunk sizes, without overflow, yields the same output stream
    (`TestChunkInvariance*`, `FuzzChunks*`).
11. **No panics.** No operation sequence panics, including negative counts
    and slices longer than the capacity (fuzzing).
12. **Configuration.** A capacity below 1 or above `MaxCapacity`, or an
    overflow policy other than `Reject` or `Overwrite` (including the zero
    value), returns an error matching `ErrConfig`.
13. **Allocation.** No method other than the constructors allocates
    (`TestNoAllocs*` after warm-up, covering every method and the wrap,
    overflow and error paths).

Fuzz targets: `FuzzRing` (both policies) and `FuzzSPSC` (single goroutine,
`Reject` semantics) decode an operation sequence and check criteria 1 to 7
and 11 against the model after every operation. Each runs at least 10
minutes on the Mac with no failure before the status says Validated, and its
seed corpus lives in `buffer/testdata/fuzz/`.

## SPSC criteria

The SPSC type's overflow policy is `Reject`: overwriting the oldest samples
would need the producer to move the consumer's index, which is a
compare-and-swap loop or a lock, both forbidden (requirements §16).

- **S1. Wait-free by construction.** Every method does a bounded amount of
  work: at most two atomic loads, at most one atomic store and one copy of
  at most len(p) or `Cap` samples. No mutex, no `sync` import, no
  compare-and-swap, swap or add, no loop that retries. A test parses the
  package source and fails on a `sync` import or any call to
  `CompareAndSwap`, `Swap` or `Add`.
- **S2. Stress test under `-race`.** A real producer goroutine and a real
  consumer goroutine move at least 10^6 samples (at least 2 · 10^5 under
  `-race` or `-short`) through each of the capacities 1, 2, 3, 7, 64 and
  1000, with randomly sized chunks (including 0 and sizes above the
  capacity), mixing `Write` with `WriteSlices`+`Commit` and `Read` with
  `ReadSlices`+`Consume`. Each sample is a hash of its sequence number. The
  consumer checks every sample it receives against the expected sequence,
  so a lost, duplicated, stale or torn sample fails the test with its index.
  `make race` passes.
- **S3. Live views.** In the stress test the consumer reads its view, yields
  the processor, and reads it again before `Consume`; the two reads must be
  equal. The producer fills its write view, yields, and then commits; a
  consumer that saw unpublished samples fails the sequence check.
- **S4. Broken orderings are caught.** Each of these deliberate mutations,
  applied to a scratch copy, makes the stress test (with `-race`) fail:
  1. the producer stores the write index before copying the samples;
  2. the consumer stores the read index before copying the samples out;
  3. both indices accessed with plain loads and stores instead of atomics;
  4. the producer's free count is one too large.
  The results are recorded in `docs/testing.md` as caught or not caught. The
  stress test is trusted only if all four are caught.
- **S5. Checker self-test.** A committed test feeds the sequence checker a
  stream with one lost, one duplicated and one stale sample, and checks that
  each is reported.
- **S6. Memory-ordering argument and trace.** `docs/algorithms.md` states
  which store publishes which samples to the other side, under the Go memory
  model, and traces the two goroutines interleaving at each index update,
  including the full, empty and wrap cases.
- **S7. Single-goroutine model check.** `FuzzSPSC` (above).

## Benchmarks

Write/read cycles at 80, 160, 480 and 4096 samples, wrap-heavy patterns (the
capacity is chosen so that most operations wrap) and odd sizes (97 and 331
samples), for `Ring` with `Write`/`Read` and with the views, and for `SPSC`
on one goroutine. One cross-goroutine `SPSC` benchmark reports throughput
with a real producer and consumer.

Targets:

- 0 allocs/op and 0 B/op in every benchmark.
- On the Mac, a `Ring` write/read cycle of 480 samples under 500 ns, and
  the same for `SPSC` on one goroutine. These are targets for a copy-bound
  operation, not measured yet; results go in `docs/performance.md`.

## Changes

1. **Criterion 7, `Commit` clipping** (changed after the first
   implementation commit, in a commit of its own). The original text clipped
   `Commit(n)` to [0, `Free`]. The independent review showed that `Free` can
   grow after `WriteSlices` returns (the consumer reads, or the owner calls
   `Read`), so `Commit(n)` with n above the view length published slots the
   caller never wrote: stale samples from an earlier lap, silently. The view
   is what the caller filled, so `Commit` now clips to it. No test passed or
   failed because of the old wording; the change makes the API safer and
   adds a requirement that the tests must check.
