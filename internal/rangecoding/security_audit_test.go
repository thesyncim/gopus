package rangecoding

import "testing"

func TestRangeCodingDefensiveGuards(t *testing.T) {
	var zeroDec Decoder
	_ = zeroDec.TellFrac()
	_ = zeroDec.Decode(0)
	_ = zeroDec.Decode(1)
	_ = zeroDec.DecodeBin(24)
	zeroDec.Update(1, 1, 2)

	var dec Decoder
	dec.Init([]byte{0xAA, 0xBB, 0xCC, 0xDD})
	_ = dec.DecodeRawBits(3)
	dec.ShrinkStorage(4)
	if got := dec.DecodeRawBits(8); got != 0 {
		t.Fatalf("DecodeRawBits after shrinking to 0 = %d, want 0", got)
	}

	var enc Encoder
	buf := make([]byte, 16)
	enc.Init(buf)
	enc.Encode(0, 0, 0)
	enc.Encode(2, 1, 4)
	enc.EncodeBin(1, 1, 24)
	enc.EncodeICDF(-1, []uint8{128, 0}, 8)
	enc.EncodeICDF(2, []uint8{128, 0}, 8)
}
