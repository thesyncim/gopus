//go:build gopus_custom_modes && gopus_fixed_point

package custom

import (
	"errors"
	"math"
	"testing"
)

func TestFixedCustomFloatToResExceptionalAndTies(t *testing.T) {
	// Values are from the pinned celt/float_cast.h FLOAT2INT24 compiled with
	// the selected fixed-point config on arm64, including both NaN signs.
	for _, tc := range []struct {
		bits uint32
		want int32
	}{
		{0x00000000, 0}, {0x80000000, 0},
		{0x33800000, 0}, {0x33c00000, 1}, {0x34400000, 2}, {0x34a00000, 2},
		{0xb3800000, 0}, {0xb3c00000, -1}, {0xb4400000, -2}, {0xb4a00000, -2},
		{0x3f800000, 8388608}, {0xbf800000, -8388608},
		{0x40000000, 16777216}, {0xc0000000, -16777216},
		{0x7f800000, 16777216}, {0xff800000, -16777216},
		{0x7fc01234, -16777216}, {0xffc01234, -16777216},
	} {
		if got := fixedCustomFloatToRes(math.Float32frombits(tc.bits)); got != tc.want {
			t.Errorf("input %08x -> %d, want %d", tc.bits, got, tc.want)
		}
	}
}

func TestFixedCustomEncodeRejectsInvalidBudget(t *testing.T) {
	mode, err := NewMode(48000, 960)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := NewEncoder(mode, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.EncodeFloat(make([]float32, 960), 0); !errors.Is(err, ErrMaxBytes) {
		t.Fatalf("EncodeFloat zero budget: %v", err)
	}
	if _, err := enc.Encode(make([]int16, 960), -1); !errors.Is(err, ErrMaxBytes) {
		t.Fatalf("Encode negative budget: %v", err)
	}
}
