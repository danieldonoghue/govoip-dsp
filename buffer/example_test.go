package buffer_test

import (
	"errors"
	"fmt"

	"github.com/danieldonoghue/govoip-dsp/buffer"
)

func ExampleRing() {
	r, err := buffer.New(buffer.Config{Capacity: 4, Overflow: buffer.Reject})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("stored:", r.Write([]int16{1, 2, 3, 4, 5, 6})) // 5 and 6 do not fit
	p := make([]int16, 3)
	n := r.Read(p)
	fmt.Println("read:", p[:n])

	// The unread samples now wrap around the end of the storage, so the
	// zero-copy read view has two segments.
	r.Write([]int16{7, 8})
	first, second := r.ReadSlices()
	fmt.Println("view:", first, second)
	fmt.Println("consumed:", r.Consume(len(first)+len(second)))

	o, err := buffer.New(buffer.Config{Capacity: 4, Overflow: buffer.Overwrite})
	if err != nil {
		fmt.Println(err)
		return
	}
	o.Write([]int16{1, 2, 3, 4, 5, 6}) // keeps the newest 4
	first, second = o.ReadSlices()
	fmt.Println("overwrite keeps:", first, second)
	// Output:
	// stored: 4
	// read: [1 2 3]
	// view: [4] [7 8]
	// consumed: 3
	// overwrite keeps: [3 4 5 6] []
}

func ExampleSPSC() {
	const frame = 160 // 20 ms at 8 kHz
	s, err := buffer.NewSPSC(buffer.SPSCConfig{Capacity: 5 * frame})
	if err != nil {
		fmt.Println(err)
		return
	}

	// The producer writes frame f as 160 samples of value f. The channels only
	// make the output deterministic; the buffer calls never wait.
	request := make(chan int)
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]int16, frame)
		f := 0
		for frames := range request {
			for range frames {
				for i := range buf {
					buf[i] = int16(f)
				}
				s.Write(buf)
				f++
			}
			done <- struct{}{}
		}
	}()

	out := make([]int16, frame)
	request <- 1
	<-done
	n := s.Read(out)
	fmt.Println("read", n, "samples of frame", out[0])

	// The consumer stalls while four frames arrive, then skips the backlog
	// and keeps only the newest frame.
	request <- 4
	<-done
	fmt.Println("backlog:", s.Len())
	fmt.Println("dropped:", s.Consume(s.Len()-frame))
	n = s.Read(out)
	fmt.Println("read", n, "samples of frame", out[0])

	close(request)
	<-done
	// Output:
	// read 160 samples of frame 0
	// backlog: 640
	// dropped: 480
	// read 160 samples of frame 4
}

// A network goroutine hands 20 ms frames to the audio goroutine through an
// SPSC; the audio goroutine reads whole frames. Here both sides run in turn
// on one goroutine, which the SPSC allows; in a program each side runs on
// its own goroutine, as in the SPSC example.
func Example() {
	const frame = 160 // 20 ms at 8 kHz
	// Room for two and a half frames, 50 ms. Size a real SPSC for the
	// longest stall of the consumer that must lose nothing.
	s, err := buffer.NewSPSC(buffer.SPSCConfig{Capacity: 400})
	if err != nil {
		fmt.Println(err)
		return
	}
	packet := make([]int16, frame)
	// Producer: store a whole frame or none, so frame boundaries stay put.
	// The third frame does not fit whole and is skipped.
	for k := range 3 {
		for i := range packet {
			packet[i] = int16(100*k + i)
		}
		if s.Free() >= len(packet) {
			s.Write(packet)
		} else {
			fmt.Println("no room for frame", k)
		}
	}
	// Consumer: process only whole frames.
	in := make([]int16, frame)
	for s.Len() >= frame {
		s.Read(in)
		fmt.Println("frame of", len(in), "samples, first", in[0], "last", in[frame-1])
	}
	fmt.Println("left:", s.Len())
	// Output:
	// no room for frame 2
	// frame of 160 samples, first 0 last 159
	// frame of 160 samples, first 100 last 259
	// left: 0
}

// New checks the capacity and the overflow policy, and returns an error
// that wraps ErrConfig.
func ExampleNew() {
	_, err := buffer.New(buffer.Config{Capacity: 160}) // no Overflow policy
	fmt.Println(errors.Is(err, buffer.ErrConfig))
	fmt.Println(err)
	_, err = buffer.New(buffer.Config{Capacity: buffer.MaxCapacity + 1, Overflow: buffer.Reject})
	fmt.Println(err)
	// Output:
	// true
	// buffer: invalid configuration: overflow policy 0, want Reject or Overwrite
	// buffer: invalid configuration: capacity 536870913, want 1 to 536870912
}

// A Config for a Ring that holds 100 ms at 16 kHz and refuses what does
// not fit.
func ExampleConfig() {
	cfg := buffer.Config{Capacity: 16000 / 10, Overflow: buffer.Reject}
	r, err := buffer.New(cfg)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("capacity:", r.Cap(), "samples")
	// Output:
	// capacity: 1600 samples
}

// The two overflow policies on the same input: Reject keeps what is
// stored, Overwrite keeps the newest samples.
func ExampleOverflow() {
	for _, policy := range []buffer.Overflow{buffer.Reject, buffer.Overwrite} {
		r, err := buffer.New(buffer.Config{Capacity: 4, Overflow: policy})
		if err != nil {
			fmt.Println(err)
			return
		}
		r.Write([]int16{1, 2, 3})
		n := r.Write([]int16{4, 5, 6})
		out := make([]int16, 4)
		fmt.Println("stored", n, "of 3, buffer holds", out[:r.Read(out)])
	}
	// Output:
	// stored 1 of 3, buffer holds [1 2 3 4]
	// stored 3 of 3, buffer holds [3 4 5 6]
}

// Cap is the capacity from Config; every slot is usable.
func ExampleRing_Cap() {
	r, err := buffer.New(buffer.Config{Capacity: 8, Overflow: buffer.Reject})
	if err != nil {
		fmt.Println(err)
		return
	}
	r.Write([]int16{1, 2, 3})
	fmt.Println("cap", r.Cap(), "len", r.Len(), "free", r.Free())
	// Output:
	// cap 8 len 3 free 5
}

// Len and Free always add up to Cap.
func ExampleRing_Len() {
	r, err := buffer.New(buffer.Config{Capacity: 8, Overflow: buffer.Reject})
	if err != nil {
		fmt.Println(err)
		return
	}
	r.Write([]int16{1, 2, 3, 4, 5})
	r.Read(make([]int16, 2))
	fmt.Println("len", r.Len(), "free", r.Free(), "sum", r.Len()+r.Free())
	// Output:
	// len 3 free 5 sum 8
}

// Check Free before Write to store a whole frame or nothing.
func ExampleRing_Free() {
	r, err := buffer.New(buffer.Config{Capacity: 10, Overflow: buffer.Reject})
	if err != nil {
		fmt.Println(err)
		return
	}
	frame := []int16{1, 2, 3, 4}
	for i := range 3 {
		if r.Free() < len(frame) {
			fmt.Println("frame", i, "dropped, free", r.Free())
			continue
		}
		r.Write(frame)
		fmt.Println("frame", i, "stored, free", r.Free())
	}
	// Output:
	// frame 0 stored, free 6
	// frame 1 stored, free 2
	// frame 2 dropped, free 2
}

// Under Reject, Write stores what fits, p[:n], and returns n.
func ExampleRing_Write() {
	r, err := buffer.New(buffer.Config{Capacity: 4, Overflow: buffer.Reject})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(r.Write([]int16{1, 2, 3}))
	fmt.Println(r.Write([]int16{4, 5, 6})) // only 4 fits
	fmt.Println(r.Write(nil))              // zero length is fine
	// Output:
	// 3
	// 1
	// 0
}

// Read copies the oldest samples into p and never pads: it returns fewer
// when fewer are stored.
func ExampleRing_Read() {
	r, err := buffer.New(buffer.Config{Capacity: 8, Overflow: buffer.Reject})
	if err != nil {
		fmt.Println(err)
		return
	}
	r.Write([]int16{1, 2, 3})
	p := make([]int16, 5)
	n := r.Read(p)
	fmt.Println(n, p[:n], "len now", r.Len())
	// Output:
	// 3 [1 2 3] len now 0
}

// Zero-copy read: process the stored samples where they are, then Consume
// them. The second segment is non-empty when the samples wrap around the
// end of the storage.
func ExampleRing_ReadSlices() {
	r, err := buffer.New(buffer.Config{Capacity: 4, Overflow: buffer.Reject})
	if err != nil {
		fmt.Println(err)
		return
	}
	r.Write([]int16{1, 2, 3})
	r.Read(make([]int16, 2)) // the next write wraps
	r.Write([]int16{4, 5})
	first, second := r.ReadSlices()
	sum := 0
	for _, seg := range [][]int16{first, second} {
		for _, v := range seg {
			sum += int(v)
		}
	}
	fmt.Println(first, second, "sum", sum)
	r.Consume(len(first) + len(second)) // the view expires here
	fmt.Println("len", r.Len())
	// Output:
	// [3 4] [5] sum 12
	// len 0
}

// Consume drops the oldest samples without copying them; its count is
// clipped to Len.
func ExampleRing_Consume() {
	r, err := buffer.New(buffer.Config{Capacity: 8, Overflow: buffer.Reject})
	if err != nil {
		fmt.Println(err)
		return
	}
	r.Write([]int16{1, 2, 3, 4, 5})
	fmt.Println(r.Consume(2), r.Consume(10), r.Consume(-1))
	// Output:
	// 2 3 0
}

// Zero-copy write: fill a prefix of the free space in place, then Commit
// how much was filled.
func ExampleRing_WriteSlices() {
	r, err := buffer.New(buffer.Config{Capacity: 4, Overflow: buffer.Reject})
	if err != nil {
		fmt.Println(err)
		return
	}
	first, second := r.WriteSlices()
	fmt.Println("free space:", len(first), len(second))
	first[0], first[1] = 10, 20
	fmt.Println("committed:", r.Commit(2))
	p := make([]int16, 4)
	fmt.Println("read:", p[:r.Read(p)])
	// Output:
	// free space: 4 0
	// committed: 2
	// read: [10 20]
}

// Commit is clipped to what is left of the write view, and is 0 without
// one, so it never publishes a slot the caller was not given.
func ExampleRing_Commit() {
	r, err := buffer.New(buffer.Config{Capacity: 4, Overflow: buffer.Reject})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("no view:", r.Commit(2))
	first, _ := r.WriteSlices()
	copy(first, []int16{1, 2, 3, 4})
	fmt.Println("view of 4, commit 3:", r.Commit(3))
	fmt.Println("then commit 5:", r.Commit(5))
	// Output:
	// no view: 0
	// view of 4, commit 3: 3
	// then commit 5: 1
}

// Reset empties the buffer for a new stream and keeps its storage.
func ExampleRing_Reset() {
	r, err := buffer.New(buffer.Config{Capacity: 4, Overflow: buffer.Reject})
	if err != nil {
		fmt.Println(err)
		return
	}
	r.Write([]int16{1, 2, 3})
	r.Reset()
	fmt.Println("len", r.Len(), "cap", r.Cap())

	var zero buffer.Ring // not made by New: an empty buffer of capacity 0
	fmt.Println("zero ring: write", zero.Write([]int16{1}), "cap", zero.Cap())
	// Output:
	// len 0 cap 4
	// zero ring: write 0 cap 0
}

// NewSPSC checks the capacity; an SPSC has no overflow policy, because it
// always rejects.
func ExampleNewSPSC() {
	_, err := buffer.NewSPSC(buffer.SPSCConfig{Capacity: 0})
	fmt.Println(errors.Is(err, buffer.ErrConfig))
	s, err := buffer.NewSPSC(buffer.SPSCConfig{Capacity: 8000 / 5}) // 200 ms at 8 kHz
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("capacity:", s.Cap())
	// Output:
	// true
	// capacity: 1600
}

// Size an SPSC for the longest stall the consumer must survive: here
// 200 ms at 16 kHz, ten 20 ms frames.
func ExampleSPSCConfig() {
	const rate, stallMs = 16000, 200
	s, err := buffer.NewSPSC(buffer.SPSCConfig{Capacity: rate * stallMs / 1000})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(s.Cap(), "samples,", s.Cap()/(rate*20/1000), "frames of 20 ms")
	// Output:
	// 3200 samples, 10 frames of 20 ms
}

// Cap is fixed and may be called from either side.
func ExampleSPSC_Cap() {
	s, err := buffer.NewSPSC(buffer.SPSCConfig{Capacity: 320})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(s.Cap())
	// Output:
	// 320
}

// Len is the consumer's count of readable samples; for the producer it is
// a snapshot that the consumer may already have changed.
func ExampleSPSC_Len() {
	s, err := buffer.NewSPSC(buffer.SPSCConfig{Capacity: 320})
	if err != nil {
		fmt.Println(err)
		return
	}
	s.Write(make([]int16, 200))
	fmt.Println("whole frames of 160 ready:", s.Len()/160)
	// Output:
	// whole frames of 160 ready: 1
}

// Free is the producer's count of free samples; for the consumer it is a
// snapshot.
func ExampleSPSC_Free() {
	s, err := buffer.NewSPSC(buffer.SPSCConfig{Capacity: 320})
	if err != nil {
		fmt.Println(err)
		return
	}
	s.Write(make([]int16, 200))
	fmt.Println("free:", s.Free())
	// Output:
	// free: 120
}

// Write never overwrites unread samples: it stores what fits, p[:n].
func ExampleSPSC_Write() {
	s, err := buffer.NewSPSC(buffer.SPSCConfig{Capacity: 4})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(s.Write([]int16{1, 2, 3}), s.Write([]int16{4, 5, 6}))
	p := make([]int16, 4)
	fmt.Println(p[:s.Read(p)])
	// Output:
	// 3 1
	// [1 2 3 4]
}

// Read takes up to len(p) of the oldest samples and frees their slots for
// the producer.
func ExampleSPSC_Read() {
	s, err := buffer.NewSPSC(buffer.SPSCConfig{Capacity: 4})
	if err != nil {
		fmt.Println(err)
		return
	}
	s.Write([]int16{1, 2, 3, 4})
	p := make([]int16, 3)
	fmt.Println(p[:s.Read(p)], "free for the producer:", s.Free())
	// Output:
	// [1 2 3] free for the producer: 3
}

// The consumer's zero-copy read. The producer never writes into the view,
// which stays valid until the consumer's next Read, Consume or Reset.
func ExampleSPSC_ReadSlices() {
	s, err := buffer.NewSPSC(buffer.SPSCConfig{Capacity: 4})
	if err != nil {
		fmt.Println(err)
		return
	}
	s.Write([]int16{1, 2, 3, 4})
	s.Read(make([]int16, 3))
	s.Write([]int16{5, 6})
	first, second := s.ReadSlices()
	fmt.Println(first, second)
	fmt.Println("consumed:", s.Consume(len(first)+len(second)))
	// Output:
	// [4] [5 6]
	// consumed: 3
}

// Consume drops a backlog: after a stall, keep only the newest frame.
func ExampleSPSC_Consume() {
	const frame = 160
	s, err := buffer.NewSPSC(buffer.SPSCConfig{Capacity: 10 * frame})
	if err != nil {
		fmt.Println(err)
		return
	}
	for f := range 6 { // six frames arrived while the consumer was stalled
		p := make([]int16, frame)
		for i := range p {
			p[i] = int16(f)
		}
		s.Write(p)
	}
	fmt.Println("dropped:", s.Consume(s.Len()-frame))
	p := make([]int16, frame)
	s.Read(p)
	fmt.Println("now reading frame", p[0])
	// Output:
	// dropped: 800
	// now reading frame 5
}

// The producer's zero-copy write: fill the free space in place, then
// Commit publishes it to the consumer.
func ExampleSPSC_WriteSlices() {
	s, err := buffer.NewSPSC(buffer.SPSCConfig{Capacity: 4})
	if err != nil {
		fmt.Println(err)
		return
	}
	first, second := s.WriteSlices()
	fmt.Println("free space:", len(first), len(second))
	copy(first, []int16{7, 8, 9})
	fmt.Println("readable before Commit:", s.Len())
	s.Commit(3)
	fmt.Println("readable after Commit:", s.Len())
	// Output:
	// free space: 4 0
	// readable before Commit: 0
	// readable after Commit: 3
}

// Commit publishes at most what is left of the write view, even when the
// consumer has freed more space since.
func ExampleSPSC_Commit() {
	s, err := buffer.NewSPSC(buffer.SPSCConfig{Capacity: 4})
	if err != nil {
		fmt.Println(err)
		return
	}
	s.Write([]int16{1, 2})
	first, _ := s.WriteSlices() // a view of the 2 free slots
	copy(first, []int16{3, 4})
	s.Read(make([]int16, 2)) // the consumer frees 2 more slots
	fmt.Println("commit 4:", s.Commit(4))
	// Output:
	// commit 4: 2
}

// Reset needs both sides stopped and synchronised with the caller, for
// example after joining their goroutines.
func ExampleSPSC_Reset() {
	s, err := buffer.NewSPSC(buffer.SPSCConfig{Capacity: 4})
	if err != nil {
		fmt.Println(err)
		return
	}
	done := make(chan struct{})
	go func() { // the producer
		defer close(done)
		s.Write([]int16{1, 2, 3})
	}()
	<-done // joined: neither side runs now
	s.Reset()
	fmt.Println("len", s.Len(), "free", s.Free())
	// Output:
	// len 0 free 4
}
