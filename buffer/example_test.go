package buffer_test

import (
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
