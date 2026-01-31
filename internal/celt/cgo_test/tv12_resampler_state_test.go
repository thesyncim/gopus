// Package cgo investigates resampler state at packet 826 transition.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12ResamplerStateAtTransition checks if resampler preserves state.
func TestTV12ResamplerStateAtTransition(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Opus decoder
	opusDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
	silkDec := opusDec.GetSILKDecoder()

	t.Log("=== Tracing resampler state at MB→NB transition (pkt 825→826) ===")

	// Process up to packet 824
	for i := 0; i <= 824; i++ {
		decodeFloat32(opusDec, packets[i])
	}

	// Get resampler state BEFORE packet 825 (MB)
	mbResBefore := silkDec.GetResampler(silk.BandwidthMediumband)
	if mbResBefore != nil {
		sIIR := mbResBefore.GetSIIR()
		t.Logf("MB resampler BEFORE pkt 825: sIIR[0]=%d", sIIR[0])
	}

	// Decode packet 825 (MB)
	decodeFloat32(opusDec, packets[825])

	// Get resampler state AFTER packet 825
	mbResAfter := silkDec.GetResampler(silk.BandwidthMediumband)
	if mbResAfter != nil {
		sIIR := mbResAfter.GetSIIR()
		t.Logf("MB resampler AFTER pkt 825: sIIR[0]=%d", sIIR[0])
	}

	// Get NB resampler state BEFORE packet 826
	nbResBefore := silkDec.GetResampler(silk.BandwidthNarrowband)
	if nbResBefore != nil {
		sIIR := nbResBefore.GetSIIR()
		t.Logf("\nNB resampler BEFORE pkt 826: sIIR=%v", sIIR)
	} else {
		t.Log("\nNB resampler not created yet")
	}

	// Decode packet 826 (NB)
	// Check debug states
	silkDec.DebugClearResetFlag()
	output826, _ := decodeFloat32(opusDec, packets[826])
	resetCalled := silkDec.DebugResetCalled()
	pre, post := silkDec.DebugGetResetStates()

	t.Logf("\nPacket 826 decode:")
	t.Logf("  Reset called: %v", resetCalled)
	t.Logf("  Pre-reset sIIR[0]: %d", pre[0])
	t.Logf("  Post-reset sIIR[0]: %d", post[0])

	// Get NB resampler state AFTER packet 826
	nbResAfter := silkDec.GetResampler(silk.BandwidthNarrowband)
	if nbResAfter != nil {
		sIIR := nbResAfter.GetSIIR()
		t.Logf("  NB resampler AFTER pkt 826: sIIR=%v", sIIR)
	}

	t.Logf("\nOutput first 5 samples: %.6f, %.6f, %.6f, %.6f, %.6f",
		output826[0], output826[1], output826[2], output826[3], output826[4])

	// Key insight: if NB resampler had state from packet 136 (before MB transition)
	// and that state wasn't reset, it would affect the first samples
	t.Log("\n=== Analysis ===")
	t.Log("In gopus: separate resamplers per bandwidth, NB was reset on MB→NB transition")
	t.Log("In libopus: single resampler reconfigured, history might carry over?")

	// Let's also check what happens if we DON'T reset the resampler
	t.Log("\n=== Testing without resampler reset ===")

	opusDec2, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
	silkDec2 := opusDec2.GetSILKDecoder()
	silkDec2.SetDisableResamplerReset(true) // Disable reset

	for i := 0; i <= 825; i++ {
		decodeFloat32(opusDec2, packets[i])
	}

	nbRes2Before := silkDec2.GetResampler(silk.BandwidthNarrowband)
	if nbRes2Before != nil {
		sIIR := nbRes2Before.GetSIIR()
		t.Logf("NB resampler (no reset) BEFORE pkt 826: sIIR=%v", sIIR)
	}

	output826_2, _ := decodeFloat32(opusDec2, packets[826])

	nbRes2After := silkDec2.GetResampler(silk.BandwidthNarrowband)
	if nbRes2After != nil {
		sIIR := nbRes2After.GetSIIR()
		t.Logf("NB resampler (no reset) AFTER pkt 826: sIIR=%v", sIIR)
	}

	t.Logf("\nOutput (no reset) first 5 samples: %.6f, %.6f, %.6f, %.6f, %.6f",
		output826_2[0], output826_2[1], output826_2[2], output826_2[3], output826_2[4])
}
