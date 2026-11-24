package gopus

import "testing"

type badLenPacketSource struct {
	n int
}

func (s badLenPacketSource) ReadPacketInto(_ []byte) (int, uint64, error) {
	return s.n, 0, nil
}

func TestReaderRejectsInvalidPacketLength(t *testing.T) {
	for _, badN := range []int{-1, 1 << 20} {
		r, err := NewReader(DefaultDecoderConfig(48000, 1), badLenPacketSource{n: badN}, FormatFloat32LE)
		if err != nil {
			t.Fatalf("NewReader failed: %v", err)
		}
		buf := make([]byte, 4096)
		if _, err := r.Read(buf); err != ErrInvalidPacket {
			t.Fatalf("Reader.Read(nPacket=%d) err = %v, want ErrInvalidPacket", badN, err)
		}
	}
}
