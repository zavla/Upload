package uploadserver

import (
	"os"
	"testing"
)

func producer(chI1 chan []byte, n int) {
	for i := 0; i < n; i++ {
		b := make([]byte, 65535)
		chI1 <- b
	}
	close(chI1)
}
func producer2(chI1 chan [65535]byte, b []byte, n int) {
	//u := unsafe.Pointer(&b[0])
	for i := 0; i < n; i++ {
		var a [65535]byte
		//chI1 <- *(*[65535]byte)(unsafe.Pointer(&b[0]))
		//chI1 <- *(*[65535]byte)(u)
		copy(a[:], b)
		chI1 <- a
	}
	close(chI1)
}
func consumer(chI1 chan []byte) {
	for {
		select {
		case b, ok := <-chI1:
			if !ok {
				// closed
				return
			}
			fnull.Write(b)
		}
	}
}
func consumer2(chI1 chan [65535]byte) {
	for {
		select {
		case b, ok := <-chI1:
			if !ok {
				// closed
				return
			}
			fnull.Write(b[:])
		}
	}
}

var fnull *os.File

func BenchmarkSliceIntoChannel(b *testing.B) {
	fnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		b.Errorf("%s", err)
		return
	}
	defer fnull.Close()
	fnull.Sync()
	for i := 0; i < b.N; i++ {
		chI1 := make(chan []byte, 10)
		go producer(chI1, 20)
		consumer(chI1)
	}
}

func BenchmarkArrayIntoChannel(b *testing.B) {
	fnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		b.Errorf("%s", err)
		return
	}
	defer fnull.Close()
	fnull.Sync()
	bb := make([]byte, 65535)
	for i := 0; i < b.N; i++ {
		chI2 := make(chan [65535]byte)
		go producer2(chI2, bb, 20)
		consumer2(chI2)
	}
}
