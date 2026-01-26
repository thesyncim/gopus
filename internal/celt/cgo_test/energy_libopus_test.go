// Package cgo provides CGO-based tests to validate gopus against libopus.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestLaplaceDecodeVsLibopus validates Laplace decoding against libopus
func TestLaplaceDecodeVsLibopus(t *testing.T) {
	testCases := []struct {
		name  string
		data  []byte
		fs    int
		decay int
	}{
		{"typical_1", []byte{0xCF, 0xC5, 0x88, 0x30, 0x00, 0x00, 0x00, 0x00}, 72 << 7, 127 << 6},
		{"typical_2", []byte{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0}, 72 << 7, 127 << 6},
		{"zeros", []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, 72 << 7, 127 << 6},
		{"ones", []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, 72 << 7, 127 << 6},
		{"lm0_inter", []byte{0xAB, 0xCD, 0xEF, 0x01, 0x23, 0x45, 0x67, 0x89}, 72 << 7, 127 << 6},
		{"lm3_intra", []byte{0x55, 0xAA, 0x55, 0xAA, 0x55, 0xAA, 0x55, 0xAA}, 22 << 7, 178 << 6},
		// Additional test cases with different probability parameters
		{"high_decay", []byte{0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xAA}, 100 << 7, 180 << 6},
		{"low_fs", []byte{0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE, 0xBA, 0xBE}, 20 << 7, 80 << 6},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Decode with libopus
			libopusVal := DecodeLaplace(tc.data, tc.fs, tc.decay)

			// Decode with gopus
			d := celt.NewDecoder(1)
			rd := &rangecoding.Decoder{}
			rd.Init(tc.data)
			d.SetRangeDecoder(rd)
			gopusVal := d.TestDecodeLaplace(tc.fs, tc.decay)

			if gopusVal != libopusVal {
				t.Errorf("Laplace mismatch: gopus=%d, libopus=%d (fs=%d, decay=%d)",
					gopusVal, libopusVal, tc.fs, tc.decay)
			} else {
				t.Logf("Laplace match: val=%d (fs=%d, decay=%d)", gopusVal, tc.fs, tc.decay)
			}
		})
	}
}

// TestRangeStateVsLibopus validates range coder initialization
func TestRangeStateVsLibopus(t *testing.T) {
	testCases := [][]byte{
		{0xCF, 0xC5, 0x88, 0x30, 0x00, 0x00, 0x00, 0x00},
		{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF},
		{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0},
	}

	for i, testData := range testCases {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			// Get libopus state
			libRng, libVal := GetRangeState(testData)

			// Get gopus state
			rd := &rangecoding.Decoder{}
			rd.Init(testData)
			goRng := rd.Range()
			goVal := rd.Val()

			t.Logf("Data: %x", testData)
			t.Logf("Range state: libopus(rng=0x%08X, val=0x%08X), gopus(rng=0x%08X, val=0x%08X)",
				libRng, libVal, goRng, goVal)

			if goRng != libRng {
				t.Errorf("Range mismatch: gopus=0x%08X, libopus=0x%08X", goRng, libRng)
			}
			if goVal != libVal {
				t.Errorf("Val mismatch: gopus=0x%08X, libopus=0x%08X", goVal, libVal)
			}
		})
	}
}

// TestDecodeBitLogpVsLibopus validates bit decoding with logp probability
func TestDecodeBitLogpVsLibopus(t *testing.T) {
	testCases := []struct {
		name string
		data []byte
		logp int
	}{
		{"logp1", []byte{0xCF, 0xC5, 0x88, 0x30, 0x00, 0x00, 0x00, 0x00}, 1},
		{"logp3", []byte{0xCF, 0xC5, 0x88, 0x30, 0x00, 0x00, 0x00, 0x00}, 3},
		{"logp15_silence", []byte{0xCF, 0xC5, 0x88, 0x30, 0x00, 0x00, 0x00, 0x00}, 15},
		{"logp15_zeros", []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, 15},
		{"logp15_ones", []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, 15},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Decode with libopus
			libopusBit := DecodeBitLogp(tc.data, tc.logp)

			// Decode with gopus
			rd := &rangecoding.Decoder{}
			rd.Init(tc.data)
			gopusBit := rd.DecodeBit(uint(tc.logp))

			if gopusBit != libopusBit {
				t.Errorf("DecodeBit(%d) mismatch: gopus=%d, libopus=%d", tc.logp, gopusBit, libopusBit)
			} else {
				t.Logf("DecodeBit(%d) match: bit=%d", tc.logp, gopusBit)
			}
		})
	}
}

// TestDecodeICDFVsLibopus validates ICDF decoding
func TestDecodeICDFVsLibopus(t *testing.T) {
	// small_energy_icdf = {2, 1, 0}
	smallEnergyICDF := []byte{2, 1, 0}

	testCases := []struct {
		name string
		data []byte
		icdf []byte
		ftb  int
	}{
		{"small_energy_1", []byte{0xCF, 0xC5, 0x88, 0x30, 0x00, 0x00, 0x00, 0x00}, smallEnergyICDF, 2},
		{"small_energy_2", []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, smallEnergyICDF, 2},
		{"small_energy_3", []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, smallEnergyICDF, 2},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Decode with libopus
			libopusSym := DecodeICDF(tc.data, tc.icdf, tc.ftb)

			// Decode with gopus
			rd := &rangecoding.Decoder{}
			rd.Init(tc.data)
			gopusSym := rd.DecodeICDF(tc.icdf, uint(tc.ftb))

			if gopusSym != libopusSym {
				t.Errorf("DecodeICDF mismatch: gopus=%d, libopus=%d", gopusSym, libopusSym)
			} else {
				t.Logf("DecodeICDF match: sym=%d", gopusSym)
			}
		})
	}
}

// TestCoarseEnergySequenceVsLibopus tests a sequence of coarse energy decode operations
func TestCoarseEnergySequenceVsLibopus(t *testing.T) {
	// Test data that produces specific Laplace values
	testData := make([]byte, 64)
	for i := range testData {
		testData[i] = byte((i*17 + 0xCF) % 256)
	}

	// Decode multiple Laplace values with gopus
	d := celt.NewDecoder(1)
	rd := &rangecoding.Decoder{}
	rd.Init(testData)
	d.SetRangeDecoder(rd)

	// For LM=3 inter mode, band 0
	fs := int(celt.EProbModel()[3][0][0]) << 7
	decay := int(celt.EProbModel()[3][0][1]) << 6

	// Decode first value with both implementations
	libVal := DecodeLaplace(testData, fs, decay)
	goVal := d.TestDecodeLaplace(fs, decay)

	if goVal != libVal {
		t.Errorf("First Laplace value mismatch: gopus=%d, libopus=%d", goVal, libVal)
	} else {
		t.Logf("First Laplace value match: %d", goVal)
	}
}
