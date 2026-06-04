package rangecoding

import "testing"

// benchEncSink prevents the compiler from eliminating encode work.
var benchEncSink uint32

// encodeBatch encodes n symbols of the given kind into a fresh encoder so the
// per-symbol cost (including occasional renormalization) dominates, rather than
// the Init/reset bookkeeping. Returns the final range to keep work observable.
func benchEncodeRun(b *testing.B, perReset int, encode func(e *Encoder, i int)) {
	buf := make([]byte, perReset+64)
	var e Encoder
	e.Init(buf)
	b.ReportAllocs()
	b.ResetTimer()
	j := 0
	for i := 0; i < b.N; i++ {
		if j >= perReset {
			benchEncSink += e.rng
			e.Init(buf)
			j = 0
		}
		encode(&e, i)
		j++
	}
	benchEncSink += e.rng
}

func BenchmarkEncodeICDF8(b *testing.B) {
	icdf := [4]uint8{192, 128, 64, 0}
	benchEncodeRun(b, 2048, func(e *Encoder, i int) {
		e.EncodeICDF(i&3, icdf[:], 8)
	})
}

func BenchmarkEncodeBit(b *testing.B) {
	benchEncodeRun(b, 2048, func(e *Encoder, i int) {
		e.EncodeBit(i&1, 3)
	})
}

func BenchmarkEncodeSymbol(b *testing.B) {
	benchEncodeRun(b, 2048, func(e *Encoder, i int) {
		fl := uint32(i & 7)
		e.Encode(fl, fl+1, 8)
	})
}
