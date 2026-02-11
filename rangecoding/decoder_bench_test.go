package rangecoding

import "testing"

func benchmarkDecodeICDFBinary(b *testing.B, fast bool) {
	buf := make([]byte, 256)
	for i := range buf {
		buf[i] = byte(i*37 + 11)
	}
	icdf := [2]uint8{128, 0}

	var d Decoder
	d.Init(buf)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Refresh state periodically to keep symbol distribution realistic.
		if i&255 == 0 {
			d.Init(buf)
		}
		if fast {
			_ = d.DecodeICDF2(icdf[0], 8)
		} else {
			_ = d.DecodeICDF(icdf[:], 8)
		}
	}
}

func BenchmarkDecodeICDFBinary(b *testing.B) {
	benchmarkDecodeICDFBinary(b, false)
}

func BenchmarkDecodeICDF2Binary(b *testing.B) {
	benchmarkDecodeICDFBinary(b, true)
}
