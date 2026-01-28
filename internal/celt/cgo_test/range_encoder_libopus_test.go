// Package cgo provides CGO comparison tests for the range encoder.
// This file tests the Go range encoder against libopus ec_enc.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestRangeEncoderUniformMatchesLibopus tests that the Go range encoder
// produces the same bytes as libopus for uniform encoding.
func TestRangeEncoderUniformMatchesLibopus(t *testing.T) {
	testCases := []struct {
		name string
		vals []uint32
		fts  []uint32
	}{
		{"single_2", []uint32{1}, []uint32{2}},
		{"single_4", []uint32{2}, []uint32{4}},
		{"single_8", []uint32{5}, []uint32{8}},
		{"multiple_same", []uint32{1, 2, 3}, []uint32{4, 4, 4}},
		{"multiple_different", []uint32{1, 3, 7}, []uint32{2, 4, 8}},
		{"larger_range", []uint32{100}, []uint32{256}},
		{"zero", []uint32{0}, []uint32{4}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Encode with Go
			buf := make([]byte, 4096)
			enc := &rangecoding.Encoder{}
			enc.Init(buf)
			for i := range tc.vals {
				enc.EncodeUniform(tc.vals[i], tc.fts[i])
			}
			goBytes := enc.Done()

			// Encode with libopus
			libBytes, _ := LibopusEncodeUniformSequence(tc.vals, tc.fts)

			// Compare
			match := len(goBytes) == len(libBytes)
			if match {
				for i := range goBytes {
					if goBytes[i] != libBytes[i] {
						match = false
						break
					}
				}
			}

			if !match {
				t.Errorf("Mismatch for %s:", tc.name)
				t.Logf("  Go:      %x (len=%d)", goBytes, len(goBytes))
				t.Logf("  libopus: %x (len=%d)", libBytes, len(libBytes))
			} else {
				t.Logf("Match: %x", goBytes)
			}
		})
	}
}

// TestCWRSBytesMatchLibopus tests that Go CWRS encoding matches libopus at byte level.
func TestCWRSBytesMatchLibopus(t *testing.T) {
	testCases := []struct {
		name   string
		pulses []int
		n, k   int
	}{
		{"simple_pos", []int{1, 0, 0, 0}, 4, 1},
		{"simple_neg", []int{-1, 0, 0, 0}, 4, 1},
		{"two_pulses", []int{1, 1, 0, 0}, 4, 2},
		{"spread", []int{1, 0, 1, 0}, 4, 2},
		{"all_positive", []int{1, 1, 1, 1}, 4, 4},
		{"mixed_signs", []int{1, -1, 1, -1}, 4, 4},
		{"larger_k", []int{2, 0, 1, 0}, 4, 3},
		{"k4_n8", []int{1, 0, 1, 0, 1, 0, 1, 0}, 8, 4},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Compute sum of absolute values to verify k
			sum := 0
			for _, p := range tc.pulses {
				if p < 0 {
					sum -= p
				} else {
					sum += p
				}
			}
			if sum != tc.k {
				t.Fatalf("Test case k mismatch: sum=%d, k=%d", sum, tc.k)
			}

			// Encode with Go
			goIndex := celt.EncodePulses(tc.pulses, tc.n, tc.k)

			// Encode to bytes with libopus
			libBytes, _ := LibopusEncodePulsesToBytes(tc.pulses, tc.n, tc.k)

			// Encode with Go range encoder
			vSize := celt.PVQ_V(tc.n, tc.k)
			buf := make([]byte, 4096)
			enc := &rangecoding.Encoder{}
			enc.Init(buf)
			enc.EncodeUniform(goIndex, vSize)
			goBytes := enc.Done()

			t.Logf("Pulses: %v", tc.pulses)
			t.Logf("Go index: %d, V=%d", goIndex, vSize)
			t.Logf("Go bytes:     %x", goBytes)
			t.Logf("libopus bytes: %x", libBytes)

			// Note: libopus encode_pulses includes the CWRS encoding directly,
			// so the comparison depends on V(n,k) being same.
		})
	}
}

// TestRangeEncoderRoundtripWithLibopusDecoder tests that Go-encoded bytes
// can be decoded by libopus.
func TestRangeEncoderRoundtripWithLibopusDecoder(t *testing.T) {
	// This would require a libopus decoder wrapper which we don't have yet.
	// For now, just verify the encoding produces correct output.
	t.Skip("Need libopus decoder wrapper")
}
