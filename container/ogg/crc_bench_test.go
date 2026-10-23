package ogg

import (
	"bytes"
	"fmt"
	"io"
	"testing"
)

func checksumBenchmarkPackets(size int) [][]byte {
	packets := make([][]byte, 16)
	for i := range packets {
		packets[i] = make([]byte, size)
		for j := range packets[i] {
			packets[i][j] = byte(i*17 + j*29)
		}
		packets[i][0] = 0xf8 // One 20 ms CELT frame.
	}
	return packets
}

func BenchmarkWriterPacketSizes(b *testing.B) {
	for _, size := range []int{80, 400, 1200, 4096} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			packets := checksumBenchmarkPackets(size)
			w, err := NewWriter(io.Discard, 48000, 2)
			if err != nil {
				b.Fatal(err)
			}
			if err := w.WritePacket(packets[0], 960); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := w.WritePacket(packets[i%len(packets)], 960); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkReaderPacketSizes(b *testing.B) {
	const packetCount = 64
	for _, size := range []int{80, 400, 1200, 4096} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			packets := checksumBenchmarkPackets(size)
			var stream bytes.Buffer
			w, err := NewWriter(&stream, 48000, 2)
			if err != nil {
				b.Fatal(err)
			}
			for i := range packetCount {
				if err := w.WritePacket(packets[i%len(packets)], 960); err != nil {
					b.Fatal(err)
				}
			}
			r, err := NewReader(bytes.NewReader(stream.Bytes()))
			if err != nil {
				b.Fatal(err)
			}
			dst := make([]byte, size)
			read := func() {
				if err := r.SeekGranule(0); err != nil {
					b.Fatal(err)
				}
				for i := range packetCount {
					n, gp, err := r.ReadPacketInto(dst)
					if err != nil || n != size || gp != uint64((i+1)*960) {
						b.Fatalf("packet %d = (%d, %d, %v)", i, n, gp, err)
					}
				}
			}
			read() // Warm packet scratch and pushback outside the timer.
			b.ReportAllocs()
			b.SetBytes(int64(size * packetCount))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				read()
			}
		})
	}
}
