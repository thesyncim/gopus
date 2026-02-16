package multistream

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"
)

func hashInt16LE(vals []int16) string {
	buf := make([]byte, 2*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint16(buf[2*i:2*i+2], uint16(v))
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

func TestProjectionMixingDefaultsLibopusParity(t *testing.T) {
	tests := []struct {
		channels int
		streams  int
		coupled  int
		hash     string
	}{
		{channels: 4, streams: 2, coupled: 2, hash: "1143a56642be1320085afae0940651974008a4fa8a69856d0f2ed1dc7b386610"},
		{channels: 6, streams: 3, coupled: 3, hash: "cfa5a4b0fc3229589e8dd9b0b1538ba1e0b2e0ee3376036c2d08ff5be1158840"},
		{channels: 9, streams: 5, coupled: 4, hash: "bddd96d5de3e2da7097f23bfa6938a268aec9142a9961a3086ed3c652d1f6e7a"},
		{channels: 11, streams: 6, coupled: 5, hash: "5a6d53e62b7e04eaf52854688df25ce5d8c7df6933c5af04f174cd576804be30"},
		{channels: 16, streams: 8, coupled: 8, hash: "42b3b8555ebb469b536f0543c63356ef162d6f6c74bf70a8279550618f6529bf"},
		{channels: 18, streams: 9, coupled: 9, hash: "b5d46a15688b23ace9d42238aa5da3fee36d76a4d0b6321c6e26eaf8247dd501"},
		{channels: 25, streams: 13, coupled: 12, hash: "76695ab08fd5c2610670806b76e1c340f6865e3a65a82c2ec4da4a0ba3523c61"},
		{channels: 27, streams: 14, coupled: 13, hash: "ce65b5051c615ed0c7b9906a598de8fa6d0abeb77d5b5c1edbf8cd98add1d7ab"},
		{channels: 36, streams: 18, coupled: 18, hash: "7bf0876f8d7763bf8e45080f150b692dafc40e4a6e41433f2286b0c84287e26d"},
		{channels: 38, streams: 19, coupled: 19, hash: "f5b446b8490ac448097149d42421f46035740f0168bd21fa294c5a2d11f0aa37"},
	}

	for _, tc := range tests {
		matrix, ok := defaultProjectionMixingMatrix(tc.channels, tc.streams, tc.coupled)
		if !ok {
			t.Fatalf("defaultProjectionMixingMatrix(%d,%d,%d) not found", tc.channels, tc.streams, tc.coupled)
		}
		if got, want := len(matrix), tc.channels*tc.channels; got != want {
			t.Fatalf("matrix len = %d, want %d", got, want)
		}
		if got := hashInt16LE(matrix); got != tc.hash {
			t.Fatalf("matrix hash = %s, want %s", got, tc.hash)
		}
	}
}

func TestNewEncoderAmbisonicsFamily3InitializesProjectionMixing(t *testing.T) {
	enc, err := NewEncoderAmbisonics(48000, 9, 3)
	if err != nil {
		t.Fatalf("NewEncoderAmbisonics failed: %v", err)
	}

	if got, want := len(enc.projectionMixing), 81; got != want {
		t.Fatalf("projectionMixing len = %d, want %d", got, want)
	}
	if enc.projectionRows != 9 || enc.projectionCols != 9 {
		t.Fatalf("projection dims = %dx%d, want 9x9", enc.projectionRows, enc.projectionCols)
	}
}

func TestApplyProjectionMixingSwapsChannels(t *testing.T) {
	enc := &Encoder{
		projectionMixing: []float64{0, 1, 1, 0},
		projectionRows:   2,
		projectionCols:   2,
	}

	in := []float64{1, 2, 3, 4}
	out := enc.applyProjectionMixing(in, 2)
	want := []float64{2, 1, 4, 3}
	for i := range want {
		if math.Abs(out[i]-want[i]) > 1e-9 {
			t.Fatalf("out[%d] = %f, want %f", i, out[i], want[i])
		}
	}
}
