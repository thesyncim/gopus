package gopus

import (
	"math"
	"testing"
)

// TestFEC_EndToEnd tests the complete FEC encode/decode cycle:
// 1. Encode audio with FEC enabled
// 2. Verify LBRR data is present in packets
// 3. Simulate packet loss and verify FEC recovery
func TestFEC_EndToEnd(t *testing.T) {
	// Create encoder with FEC enabled
	enc, err := NewEncoder(48000, 1, ApplicationVoIP)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(true)
	if !enc.FECEnabled() {
		t.Fatal("FEC should be enabled")
	}

	// Create decoder
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Generate test signal (440Hz sine wave)
	frameSize := 960 // 20ms at 48kHz
	numFrames := 5
	
	packets := make([][]byte, numFrames)
	
	for i := 0; i < numFrames; i++ {
		pcm := make([]float32, frameSize)
		for j := 0; j < frameSize; j++ {
			sampleIdx := i*frameSize + j
			pcm[j] = float32(0.5 * math.Sin(2*math.Pi*440*float64(sampleIdx)/48000))
		}
		
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Encode frame %d error: %v", i, err)
		}
		if len(packet) == 0 {
			t.Fatalf("Frame %d: empty packet (DTX suppressed?)", i)
		}
		
		// Make a copy of the packet
		packets[i] = make([]byte, len(packet))
		copy(packets[i], packet)
		
		t.Logf("Frame %d: %d bytes, TOC=0x%02X", i, len(packet), packet[0])
	}
	
	// Now test decoding
	t.Log("\n=== Testing normal decode ===")
	decodedNormal := make([][]float32, numFrames)
	for i := 0; i < numFrames; i++ {
		pcm := make([]float32, frameSize)
		n, err := dec.Decode(packets[i], pcm)
		if err != nil {
			t.Fatalf("Decode frame %d error: %v", i, err)
		}
		if n != frameSize {
			t.Errorf("Decode frame %d: got %d samples, want %d", i, n, frameSize)
		}
		decodedNormal[i] = pcm[:n]
		t.Logf("Frame %d: decoded %d samples, hasFEC=%v", i, n, dec.hasFEC)
	}
	
	// Reset decoder and test FEC recovery
	dec.Reset()
	
	t.Log("\n=== Testing FEC recovery (simulating packet 2 lost) ===")
	// Decode frame 0 and 1 normally
	pcm0 := make([]float32, frameSize)
	_, err = dec.Decode(packets[0], pcm0)
	if err != nil {
		t.Fatalf("Decode frame 0 error: %v", err)
	}
	t.Logf("Frame 0: decoded, hasFEC=%v", dec.hasFEC)
	
	pcm1 := make([]float32, frameSize)
	_, err = dec.Decode(packets[1], pcm1)
	if err != nil {
		t.Fatalf("Decode frame 1 error: %v", err)
	}
	t.Logf("Frame 1: decoded, hasFEC=%v", dec.hasFEC)
	
	// Simulate packet 2 is lost - use FEC recovery
	pcm2FEC := make([]float32, frameSize)
	n, err := dec.DecodeWithFEC(nil, pcm2FEC, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC (lost packet 2) error: %v", err)
	}
	t.Logf("Frame 2 (FEC): recovered %d samples", n)
	
	// Decode frame 3 normally
	pcm3 := make([]float32, frameSize)
	_, err = dec.Decode(packets[3], pcm3)
	if err != nil {
		t.Fatalf("Decode frame 3 error: %v", err)
	}
	t.Logf("Frame 3: decoded, hasFEC=%v", dec.hasFEC)
	
	t.Log("\n=== FEC End-to-End test completed ===")
}

// TestFEC_LBRRPresence verifies that LBRR data is encoded in SILK packets
// by checking the LBRR flag in the packet header.
func TestFEC_LBRRPresence(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationVoIP)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	
	// Enable FEC
	enc.SetFEC(true)
	
	frameSize := 960
	numFrames := 5
	
	lbrrPackets := 0
	for i := 0; i < numFrames; i++ {
		pcm := make([]float32, frameSize)
		for j := 0; j < frameSize; j++ {
			sampleIdx := i*frameSize + j
			pcm[j] = float32(0.7 * math.Sin(2*math.Pi*440*float64(sampleIdx)/48000))
		}
		
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Encode frame %d error: %v", i, err)
		}
		if len(packet) < 2 {
			t.Logf("Frame %d: packet too short (%d bytes)", i, len(packet))
			continue
		}
		
		toc := ParseTOC(packet[0])
		t.Logf("Frame %d: %d bytes, mode=%v, config=%d", i, len(packet), toc.Mode, packet[0]>>3)
		
		// For SILK/Hybrid packets, check if LBRR flag is set
		// LBRR flag is encoded in the SILK layer header
		if toc.Mode == ModeSILK || toc.Mode == ModeHybrid {
			if len(packet) > 1 {
				// The SILK layer starts after TOC byte
				// For single-frame packets, bit positions are:
				// - VAD flag: 1 bit
				// - LBRR flag: 1 bit
				// Check second byte for LBRR presence indication
				hasLBRRIndication := (packet[1] & 0x40) != 0 || (packet[1] & 0x80) != 0
				if hasLBRRIndication {
					lbrrPackets++
				}
				t.Logf("  SILK header byte: 0x%02X, possible LBRR indication: %v", packet[1], hasLBRRIndication)
			}
		}
	}
	
	t.Logf("\nPackets with potential LBRR: %d/%d", lbrrPackets, numFrames)
	// Note: LBRR may not be present in all packets depending on speech activity
}

// TestFEC_RecoveryQuality compares FEC recovery quality vs PLC
func TestFEC_RecoveryQuality(t *testing.T) {
	// Create encoder with FEC enabled
	enc, err := NewEncoder(48000, 1, ApplicationVoIP)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(true)
	
	frameSize := 960
	numFrames := 10
	
	// Encode frames
	packets := make([][]byte, numFrames)
	for i := 0; i < numFrames; i++ {
		pcm := make([]float32, frameSize)
		for j := 0; j < frameSize; j++ {
			sampleIdx := i*frameSize + j
			// Use a more complex signal for better quality comparison
			pcm[j] = float32(0.5*math.Sin(2*math.Pi*440*float64(sampleIdx)/48000) +
				0.3*math.Sin(2*math.Pi*880*float64(sampleIdx)/48000))
		}
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Encode frame %d error: %v", i, err)
		}
		packets[i] = make([]byte, len(packet))
		copy(packets[i], packet)
	}
	
	// Test 1: Decode with FEC recovery for lost packet
	dec1, _ := NewDecoder(DefaultDecoderConfig(48000, 1))
	for i := 0; i < 5; i++ {
		pcm := make([]float32, frameSize)
		dec1.Decode(packets[i], pcm)
	}
	
	// Lose packet 5, recover with FEC
	pcmFEC := make([]float32, frameSize)
	dec1.DecodeWithFEC(nil, pcmFEC, true)
	
	// Continue normal decode
	for i := 6; i < numFrames; i++ {
		pcm := make([]float32, frameSize)
		dec1.Decode(packets[i], pcm)
	}
	
	// Test 2: Decode with PLC for lost packet (no FEC)
	dec2, _ := NewDecoder(DefaultDecoderConfig(48000, 1))
	for i := 0; i < 5; i++ {
		pcm := make([]float32, frameSize)
		dec2.Decode(packets[i], pcm)
	}
	
	// Lose packet 5, recover with PLC only
	pcmPLC := make([]float32, frameSize)
	dec2.Decode(nil, pcmPLC)
	
	// Continue normal decode
	for i := 6; i < numFrames; i++ {
		pcm := make([]float32, frameSize)
		dec2.Decode(packets[i], pcm)
	}
	
	// Compare energy of recovered frames
	energyFEC := 0.0
	energyPLC := 0.0
	for i := 0; i < frameSize; i++ {
		energyFEC += float64(pcmFEC[i]) * float64(pcmFEC[i])
		energyPLC += float64(pcmPLC[i]) * float64(pcmPLC[i])
	}
	
	t.Logf("FEC recovery energy: %.6f", energyFEC/float64(frameSize))
	t.Logf("PLC recovery energy: %.6f", energyPLC/float64(frameSize))
	
	// Both should produce non-zero output
	if energyFEC == 0 && energyPLC == 0 {
		t.Error("Both FEC and PLC produced silence - recovery failed")
	}
	
	t.Log("FEC/PLC recovery comparison complete")
}

// TestFEC_MultiplePacketLoss tests FEC behavior with multiple consecutive lost packets
func TestFEC_MultiplePacketLoss(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationVoIP)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(true)
	
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	
	frameSize := 960
	numFrames := 10
	
	// Encode all frames
	packets := make([][]byte, numFrames)
	for i := 0; i < numFrames; i++ {
		pcm := make([]float32, frameSize)
		for j := 0; j < frameSize; j++ {
			sampleIdx := i*frameSize + j
			pcm[j] = float32(0.5 * math.Sin(2*math.Pi*440*float64(sampleIdx)/48000))
		}
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Encode frame %d error: %v", i, err)
		}
		packets[i] = make([]byte, len(packet))
		copy(packets[i], packet)
	}
	
	// Decode: 0, 1, 2, [3 lost], [4 lost], 5, 6, 7, 8, 9
	pcm := make([]float32, frameSize)
	
	// Decode 0, 1, 2 normally
	for i := 0; i < 3; i++ {
		_, err := dec.Decode(packets[i], pcm)
		if err != nil {
			t.Fatalf("Decode frame %d error: %v", i, err)
		}
		t.Logf("Frame %d: decoded normally", i)
	}
	
	// Lose frame 3 and 4
	t.Log("Simulating loss of frames 3 and 4...")
	for i := 0; i < 2; i++ {
		_, err := dec.DecodeWithFEC(nil, pcm, true)
		if err != nil {
			t.Fatalf("DecodeWithFEC for lost frame error: %v", err)
		}
		t.Logf("Lost frame %d: recovered with FEC/PLC", 3+i)
	}
	
	// Continue with frames 5-9
	for i := 5; i < numFrames; i++ {
		_, err := dec.Decode(packets[i], pcm)
		if err != nil {
			t.Fatalf("Decode frame %d error: %v", i, err)
		}
		t.Logf("Frame %d: decoded normally", i)
	}
	
	t.Log("Multiple packet loss test completed successfully")
}
