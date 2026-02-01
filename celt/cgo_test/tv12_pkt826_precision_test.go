//go:build cgo_libopus
// +build cgo_libopus

// Package cgo checks if packet 826 output is exactly zero or very small.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/silk"
)

// TestTV12Packet826Precision checks if output is exactly zero or very small.
func TestTV12Packet826Precision(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create SILK decoder
	silkDec := silk.NewDecoder()

	// Process packets 0-825 to build state
	for i := 0; i <= 825; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}

		silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
	}

	// Decode packet 826
	pkt := packets[826]
	toc := gopus.ParseTOC(pkt[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))

	output, err := silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	// Check if outputs are exactly zero
	exactZeroCount := 0
	nonZeroCount := 0
	var firstNonZeroIdx int
	var firstNonZeroVal float32

	for i, v := range output {
		if v == 0.0 {
			exactZeroCount++
		} else {
			if nonZeroCount == 0 {
				firstNonZeroIdx = i
				firstNonZeroVal = v
			}
			nonZeroCount++
		}
	}

	t.Logf("Output length: %d", len(output))
	t.Logf("Exact zeros: %d", exactZeroCount)
	t.Logf("Non-zeros: %d", nonZeroCount)
	if nonZeroCount > 0 {
		t.Logf("First non-zero at index %d: %e (%.12f)", firstNonZeroIdx, firstNonZeroVal, firstNonZeroVal)
	}

	// Print first 20 samples with more precision
	t.Logf("\nFirst 20 samples with high precision:")
	for i := 0; i < 20 && i < len(output); i++ {
		t.Logf("  [%3d] %.12e", i, output[i])
	}

	// Compare with libopus
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Process with libopus to same point
	for i := 0; i <= 826; i++ {
		pkt := packets[i]
		libDec.DecodeFloat(pkt, 1920)
	}

	// Get packet 826 output from fresh libopus decoder chain
	libDec2, _ := NewLibopusDecoder(48000, 1)
	defer libDec2.Destroy()
	for i := 0; i <= 825; i++ {
		libDec2.DecodeFloat(packets[i], 1920)
	}
	libOut, _ := libDec2.DecodeFloat(packets[826], 1920)

	t.Logf("\nLibopus first 20 samples:")
	for i := 0; i < 20 && i < len(libOut); i++ {
		t.Logf("  [%3d] %.12e", i, libOut[i])
	}

	// Count libopus zeros
	libZeroCount := 0
	for _, v := range libOut[:960] {
		if v == 0.0 {
			libZeroCount++
		}
	}
	t.Logf("\nLibopus exact zeros: %d", libZeroCount)
}
