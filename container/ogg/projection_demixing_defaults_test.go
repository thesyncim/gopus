package ogg

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
)

func TestDefaultProjectionDemixingMatrixLibopusParity(t *testing.T) {
	tests := []struct {
		channels uint8
		streams  uint8
		coupled  uint8
		gain     int16
		sha256   string
	}{
		{channels: 4, streams: 2, coupled: 2, gain: 0, sha256: "05f3ed8da003073ae3bd66bc9d5d5e8ad6bbaedca71884bf8c8b38fe763fb350"},
		{channels: 6, streams: 3, coupled: 3, gain: 0, sha256: "59377bceaf6285bce5d285a26558b3dfe0d3aebc9f4374578b77d796752fcfde"},
		{channels: 9, streams: 5, coupled: 4, gain: 3050, sha256: "e983595fa270241b4ac42f01f457d225f3e5f9721708aff12fbfbe873fe570a7"},
		{channels: 11, streams: 6, coupled: 5, gain: 3050, sha256: "c31679115ae64473729481a7f1ceea0bf114f64ea80e6f21fcc865fdf3102e74"},
		{channels: 16, streams: 8, coupled: 8, gain: 0, sha256: "5ce1e8e350f477a3bd58816e665b80ae8349369b3ed44b28f879acf8496819a6"},
		{channels: 18, streams: 9, coupled: 9, gain: 0, sha256: "bcda5e2c93d1cd7252eb84fae6795537aa4e24ed5308f84de3b3b6d729baabc6"},
		{channels: 25, streams: 13, coupled: 12, gain: 0, sha256: "a1f892a93f728289b37af3c73994cdfbd53fe3c0d4462693c6384dd294ee5c15"},
		{channels: 27, streams: 14, coupled: 13, gain: 0, sha256: "9fb0e5d89262aef3891b9f7fc27eb1ff61e30a8826d39f31cfdfc5aec09a0f28"},
		{channels: 36, streams: 18, coupled: 18, gain: 0, sha256: "421b2fe2ea4d61b7508931d729bf77208163fdaa2e2764fa379a12838dae5c81"},
		{channels: 38, streams: 19, coupled: 19, gain: 0, sha256: "9dd428cb3a2f63295f029b7a3b7d49298a7d2fdecfb4cfd768d5b06e183ccbcf"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(fmt.Sprintf("ch%d", tc.channels), func(t *testing.T) {
			matrix, gain, ok := defaultProjectionDemixingMatrix(tc.channels, tc.streams, tc.coupled)
			if !ok {
				t.Fatalf("defaultProjectionDemixingMatrix(%d,%d,%d) not found", tc.channels, tc.streams, tc.coupled)
			}
			if gain != tc.gain {
				t.Fatalf("gain = %d, want %d", gain, tc.gain)
			}
			if got, want := len(matrix), expectedDemixingMatrixSize(tc.channels, tc.streams, tc.coupled); got != want {
				t.Fatalf("matrix len = %d, want %d", got, want)
			}
			sum := sha256.Sum256(matrix)
			if got := hex.EncodeToString(sum[:]); got != tc.sha256 {
				t.Fatalf("matrix sha256 = %s, want %s", got, tc.sha256)
			}

			head := DefaultOpusHeadMultistreamWithFamily(48000, tc.channels, MappingFamilyProjection, tc.streams, tc.coupled, nil)
			if head.OutputGain != tc.gain {
				t.Fatalf("default head OutputGain = %d, want %d", head.OutputGain, tc.gain)
			}
			if got, want := len(head.DemixingMatrix), len(matrix); got != want {
				t.Fatalf("default head demixing len = %d, want %d", got, want)
			}
		})
	}
}

func TestDefaultProjectionDemixingMatrixFallbackIdentity(t *testing.T) {
	head := DefaultOpusHeadMultistreamWithFamily(48000, 9, MappingFamilyProjection, 6, 4, nil)
	if head.OutputGain != 0 {
		t.Fatalf("OutputGain = %d, want 0 fallback", head.OutputGain)
	}
	wantLen := expectedDemixingMatrixSize(9, 6, 4)
	if got := len(head.DemixingMatrix); got != wantLen {
		t.Fatalf("DemixingMatrix len = %d, want %d", got, wantLen)
	}
}
