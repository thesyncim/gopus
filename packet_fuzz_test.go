package gopus

import "testing"

func FuzzParsePacket_NoPanic(f *testing.F) {
	f.Add([]byte{0xF8, 0x11, 0x22, 0x33})
	f.Add([]byte{0x00, 0x10})
	f.Add([]byte{0x03, 0x02, 0x10, 0x20})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			return
		}
		info, err := ParsePacket(data)
		_ = ParseTOC(data[0])
		if err != nil {
			return
		}
		if info.FrameCount < 1 || info.FrameCount > 48 {
			t.Fatalf("invalid frame count: %d", info.FrameCount)
		}
		if len(info.FrameSizes) != info.FrameCount {
			t.Fatalf("frame size metadata mismatch: count=%d sizes=%d", info.FrameCount, len(info.FrameSizes))
		}
		for i, n := range info.FrameSizes {
			if n <= 0 {
				t.Fatalf("invalid frame size[%d]=%d", i, n)
			}
		}
	})
}
