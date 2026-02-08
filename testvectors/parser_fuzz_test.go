package testvectors

import "testing"

func FuzzParseOpusDemoBitstream(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{0, 0, 0, 1, 0, 0, 0, 1, 0xF8})

	f.Fuzz(func(t *testing.T, data []byte) {
		packets, err := ParseOpusDemoBitstream(data)
		if err != nil {
			return
		}
		for i, p := range packets {
			_ = p.FinalRange
			if len(p.Data) > len(data) {
				t.Fatalf("invalid packet size at index %d: %d > %d", i, len(p.Data), len(data))
			}
		}
	})
}
