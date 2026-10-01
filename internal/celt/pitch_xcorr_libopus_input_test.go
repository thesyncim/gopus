package celt

import (
	"encoding/binary"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const libopusPitchXcorrInputMagic = "GXCI"

type libopusPitchXcorrCase struct {
	name     string
	x        []float32
	y        []float32
	maxPitch int
}

func buildLibopusPitchXcorrInput(cases []libopusPitchXcorrCase) ([]byte, error) {
	payload := libopustest.NewOraclePayload(libopusPitchXcorrInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		if tc.maxPitch <= 0 || len(tc.x) == 0 || len(tc.y) != len(tc.x)+tc.maxPitch-1 {
			return nil, fmt.Errorf("%s: invalid xcorr input lengths x=%d y=%d maxPitch=%d", tc.name, len(tc.x), len(tc.y), tc.maxPitch)
		}
		payload.U32(uint32(len(tc.x)))
		payload.U32(uint32(tc.maxPitch))
		payload.Float32s(tc.x...)
		payload.Float32s(tc.y...)
	}
	return payload.Bytes(), nil
}

func TestPitchXCorrOracleInputFraming(t *testing.T) {
	cases := []libopusPitchXcorrCase{
		{
			name:     "short",
			x:        []float32{1.25, math.Float32frombits(0x80000000)},
			y:        []float32{math.Float32frombits(0x7fc01234), -0.5},
			maxPitch: 1,
		},
		{
			name:     "longer",
			x:        []float32{math.Float32frombits(0xffc05678), 0.25, float32(math.Inf(1))},
			y:        []float32{math.SmallestNonzeroFloat32, -1, 2, math.Float32frombits(0x00000002), 0},
			maxPitch: 3,
		},
	}

	data, err := buildLibopusPitchXcorrInput(cases)
	if err != nil {
		t.Fatalf("build xcorr oracle input: %v", err)
	}
	if len(data) < 12 {
		t.Fatalf("xcorr oracle input length=%d, want header and two records", len(data))
	}
	if got := string(data[:4]); got != libopusPitchXcorrInputMagic {
		t.Fatalf("input magic=%q want %q", got, libopusPitchXcorrInputMagic)
	}
	if got := binary.LittleEndian.Uint32(data[4:8]); got != 1 {
		t.Fatalf("input version=%d want 1", got)
	}
	if got := binary.LittleEndian.Uint32(data[8:12]); got != uint32(len(cases)) {
		t.Fatalf("input record count=%d want %d", got, len(cases))
	}

	off := 12
	readU32 := func(label string) uint32 {
		t.Helper()
		if off+4 > len(data) {
			t.Fatalf("truncated %s at byte %d", label, off)
			return 0
		}
		got := binary.LittleEndian.Uint32(data[off : off+4])
		off += 4
		return got
	}
	readFloats := func(label string, want []float32) {
		t.Helper()
		for i, value := range want {
			got := readU32(fmt.Sprintf("%s[%d]", label, i))
			if expected := math.Float32bits(value); got != expected {
				t.Fatalf("%s[%d] bits=%08x want %08x", label, i, got, expected)
			}
		}
	}
	for _, tc := range cases {
		if got := readU32(tc.name + " x length"); got != uint32(len(tc.x)) {
			t.Fatalf("%s x length=%d want %d", tc.name, got, len(tc.x))
		}
		if got := readU32(tc.name + " max pitch"); got != uint32(tc.maxPitch) {
			t.Fatalf("%s max pitch=%d want %d", tc.name, got, tc.maxPitch)
		}
		readFloats(tc.name+" x", tc.x)
		readFloats(tc.name+" y", tc.y)
	}
	if off != len(data) {
		t.Fatalf("input framing consumed %d of %d bytes; %d trailing", off, len(data), len(data)-off)
	}
}
