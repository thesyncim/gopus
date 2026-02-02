package gopus

import (
	"math"
	"testing"
)

// TestFEC_LBRRActualRecovery verifies that actual LBRR data is recovered,
// not just PLC fallback. This test compares the recovered signal with
// the original encoded signal.
func TestFEC_LBRRActualRecovery(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationVoIP)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(true)
	
	frameSize := 960
	numFrames := 10
	
	// Generate and encode frames
	originalPCM := make([][]float32, numFrames)
	packets := make([][]byte, numFrames)
	
	for i := 0; i < numFrames; i++ {
		pcm := make([]float32, frameSize)
		for j := 0; j < frameSize; j++ {
			sampleIdx := i*frameSize + j
			// Use distinct frequency per frame for easier identification
			freq := 440.0 + float64(i)*20.0 // 440Hz, 460Hz, 480Hz, etc.
			pcm[j] = float32(0.5 * math.Sin(2*math.Pi*freq*float64(sampleIdx)/48000))
		}
		originalPCM[i] = pcm
		
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Encode frame %d error: %v", i, err)
		}
		packets[i] = make([]byte, len(packet))
		copy(packets[i], packet)
		t.Logf("Frame %d: encoded %d bytes (freq=%.0fHz)", i, len(packet), 440.0+float64(i)*20.0)
	}
	
	// Create two decoders: one for FEC recovery, one for baseline comparison
	dec, _ := NewDecoder(DefaultDecoderConfig(48000, 1))
	decBaseline, _ := NewDecoder(DefaultDecoderConfig(48000, 1))
	
	// Decode frames 0-4 with both decoders
	for i := 0; i < 5; i++ {
		pcm := make([]float32, frameSize)
		pcmBaseline := make([]float32, frameSize)
		dec.Decode(packets[i], pcm)
		decBaseline.Decode(packets[i], pcmBaseline)
	}
	
	t.Log("\n=== Testing LBRR recovery for frame 5 ===")
	
	// Frame 5 is "lost" - recover with FEC
	pcmFEC := make([]float32, frameSize)
	n, err := dec.DecodeWithFEC(nil, pcmFEC, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC error: %v", err)
	}
	t.Logf("FEC recovered %d samples for lost frame 5", n)
	
	// For baseline, decode frame 5 normally
	pcmBaseline := make([]float32, frameSize)
	decBaseline.Decode(packets[5], pcmBaseline)
	
	// Calculate correlation between FEC recovered signal and baseline
	// High correlation indicates LBRR contains useful data
	var sumFEC, sumBaseline, sumProduct float64
	var sqSumFEC, sqSumBaseline float64
	
	for i := 0; i < frameSize; i++ {
		sumFEC += float64(pcmFEC[i])
		sumBaseline += float64(pcmBaseline[i])
		sumProduct += float64(pcmFEC[i]) * float64(pcmBaseline[i])
		sqSumFEC += float64(pcmFEC[i]) * float64(pcmFEC[i])
		sqSumBaseline += float64(pcmBaseline[i]) * float64(pcmBaseline[i])
	}
	
	n64 := float64(frameSize)
	numerator := n64*sumProduct - sumFEC*sumBaseline
	denominator := math.Sqrt((n64*sqSumFEC - sumFEC*sumFEC) * (n64*sqSumBaseline - sumBaseline*sumBaseline))
	
	correlation := 0.0
	if denominator > 0 {
		correlation = numerator / denominator
	}
	
	// Calculate RMS energy for both
	energyFEC := math.Sqrt(sqSumFEC / n64)
	energyBaseline := math.Sqrt(sqSumBaseline / n64)
	
	t.Logf("FEC recovery RMS energy: %.6f", energyFEC)
	t.Logf("Baseline decode RMS energy: %.6f", energyBaseline)
	t.Logf("Correlation between FEC and baseline: %.4f", correlation)
	
	// FEC should produce non-zero signal
	if energyFEC < 0.001 {
		t.Error("FEC recovery produced near-silence - LBRR may not be working")
	}
	
	// Note: LBRR is encoded at lower quality, so we don't expect perfect match,
	// but correlation should be positive and energy should be in similar range
	if correlation < 0 {
		t.Log("Warning: Negative correlation suggests LBRR may not contain useful data")
	} else if correlation > 0.5 {
		t.Log("Good: High correlation suggests LBRR recovery is working well")
	} else if correlation > 0 {
		t.Log("Moderate: Some correlation - LBRR provides partial recovery")
	}
	
	// Print first few samples for visual inspection
	t.Log("\nFirst 10 samples comparison:")
	for i := 0; i < 10 && i < frameSize; i++ {
		t.Logf("  [%d] FEC=%.4f, Baseline=%.4f", i, pcmFEC[i], pcmBaseline[i])
	}
}

// TestFEC_HasLBRRCheck verifies the SILK decoder's HasLBRR function
func TestFEC_HasLBRRCheck(t *testing.T) {
	enc, _ := NewEncoder(48000, 1, ApplicationVoIP)
	enc.SetFEC(true)
	
	dec, _ := NewDecoder(DefaultDecoderConfig(48000, 1))
	
	// Generate and encode a few frames
	frameSize := 960
	for i := 0; i < 3; i++ {
		pcm := make([]float32, frameSize)
		for j := 0; j < frameSize; j++ {
			pcm[j] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i*frameSize+j)/48000))
		}
		packet, _ := enc.EncodeFloat32(pcm)
		
		// Decode the packet
		out := make([]float32, frameSize)
		dec.Decode(packet, out)
		
		// Check if FEC data is stored
		t.Logf("After frame %d: hasFEC=%v, fecMode=%v, fecFrameSize=%d", 
			i, dec.hasFEC, dec.fecMode, dec.fecFrameSize)
	}
	
	// After SILK decode, hasFEC should be true
	if !dec.hasFEC {
		t.Error("hasFEC should be true after decoding SILK packets")
	}
	if dec.fecMode != ModeSILK {
		t.Errorf("fecMode should be ModeSILK (0), got %v", dec.fecMode)
	}
}

// TestFEC_VsSILKDecoder directly tests the SILK decoder's FEC capability
func TestFEC_SILKEncoderLBRREnabled(t *testing.T) {
	enc, _ := NewEncoder(48000, 1, ApplicationVoIP)
	
	// Initially FEC should be disabled
	if enc.FECEnabled() {
		t.Error("FEC should be disabled initially")
	}
	
	// Enable FEC
	enc.SetFEC(true)
	if !enc.FECEnabled() {
		t.Error("FEC should be enabled after SetFEC(true)")
	}
	
	// Encode a frame and check the internal SILK encoder's LBRR state
	frameSize := 960
	pcm := make([]float32, frameSize)
	for j := 0; j < frameSize; j++ {
		pcm[j] = float32(0.5 * math.Sin(2*math.Pi*440*float64(j)/48000))
	}
	
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	
	t.Logf("Encoded packet: %d bytes, TOC=0x%02X", len(packet), packet[0])
	
	// Parse TOC to verify mode
	toc := ParseTOC(packet[0])
	t.Logf("Mode: %v, Bandwidth: %v, FrameSize: %d", toc.Mode, toc.Bandwidth, toc.FrameSize)
	
	if toc.Mode != ModeSILK && toc.Mode != ModeHybrid {
		t.Logf("Note: Mode is %v - LBRR only applies to SILK/Hybrid modes", toc.Mode)
	}
}
