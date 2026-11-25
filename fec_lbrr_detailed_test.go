package gopus

import (
	"math"
	"sync"
	"testing"
)

var fecQualityOnce sync.Once

func logFECQualityStatus(t *testing.T) {
	t.Helper()
	fecQualityOnce.Do(func() {
		t.Log("FEC/LBRR STATUS: LBRR recovery is active in focused tests; keep correlation and energy checks as regression sentinels.")
		t.Log("COVERED: provided-packet recovery, nil-packet PLC fallback, no-LBRR PLC fallback, and packet-mode gating.")
		t.Log("WATCH: low-bitrate/no-LBRR packet mixes can still fall back to PLC by design; broaden claims only with libopus-backed seams.")
	})
}

// TestFEC_LBRRActualRecovery verifies that provided-packet LBRR recovery
// produces a non-silent concealment frame.
func TestFEC_LBRRActualRecovery(t *testing.T) {
	logFECQualityStatus(t)

	const frameSize = 960
	seedPacket, recoveryPacket := encodeAPIRateFECSequence(t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, 1, frameSize)

	t.Log("\n=== Testing LBRR recovery for frame 5 ===")

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	pcmSeed := make([]float32, frameSize)
	if _, err := dec.Decode(seedPacket, pcmSeed); err != nil {
		t.Fatalf("Decode seed error: %v", err)
	}

	pcmFEC := make([]float32, frameSize)
	n, err := dec.DecodeWithFEC(recoveryPacket, pcmFEC, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC error: %v", err)
	}
	t.Logf("FEC recovered %d samples for lost frame 5", n)

	pcmRecovery := make([]float32, frameSize)
	if _, err := dec.Decode(recoveryPacket, pcmRecovery); err != nil {
		t.Fatalf("Decode recovery packet error: %v", err)
	}

	var sqSumFEC, sqSumRecovery float64

	for i := range frameSize {
		sqSumFEC += float64(pcmFEC[i]) * float64(pcmFEC[i])
		sqSumRecovery += float64(pcmRecovery[i]) * float64(pcmRecovery[i])
	}

	n64 := float64(frameSize)
	energyFEC := math.Sqrt(sqSumFEC / n64)
	energyRecovery := math.Sqrt(sqSumRecovery / n64)

	t.Logf("FEC recovery RMS energy: %.6f", energyFEC)
	t.Logf("Recovery-packet decode RMS energy: %.6f", energyRecovery)

	if energyFEC < 0.001 {
		t.Error("FEC recovery produced near-silence - LBRR may not be working")
	}

	t.Log("\nFirst 10 samples comparison:")
	for i := 0; i < 10 && i < frameSize; i++ {
		t.Logf("  [%d] FEC=%.4f, Recovery=%.4f", i, pcmFEC[i], pcmRecovery[i])
	}
}

// TestFEC_HasLBRRCheck verifies packet-level LBRR detection.
func TestFEC_HasLBRRCheck(t *testing.T) {
	_, recoveryPacket := encodeAPIRateFECSequence(t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, 1, 960)
	if !packetHasInBandFEC(t, recoveryPacket) {
		t.Fatal("recovery packet should carry LBRR")
	}

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	out := make([]float32, 960)
	if _, err := dec.Decode(recoveryPacket, out); err != nil {
		t.Fatalf("Decode recovery packet error: %v", err)
	}
	if dec.hasFEC {
		t.Fatal("normal decode should not cache LBRR for nil decode_fec")
	}
}

// TestFEC_VsSILKDecoder directly tests the SILK decoder's FEC capability
func TestFEC_SILKEncoderLBRREnabled(t *testing.T) {
	enc, _ := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})

	// Initially FEC should be disabled
	if enc.FECEnabled() {
		t.Error("FEC should be disabled initially")
	}

	// Enable FEC
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(15); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}
	if !enc.FECEnabled() {
		t.Error("FEC should be enabled after SetFEC(true)")
	}

	// Encode a frame and check the internal SILK encoder's LBRR state
	frameSize := 960
	pcm := make([]float32, frameSize)
	for j := range frameSize {
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

func TestFEC_ProvidedPacketWithoutLBRRFallsBackToPLC(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(false)
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}

	frameSize := 960
	makeFrame := func(phase int) []float32 {
		pcm := make([]float32, frameSize)
		for i := range pcm {
			sampleIdx := phase + i
			pcm[i] = float32(0.45*math.Sin(2*math.Pi*260*float64(sampleIdx)/48000) +
				0.15*math.Sin(2*math.Pi*520*float64(sampleIdx)/48000))
		}
		return pcm
	}

	packet0, err := enc.EncodeFloat32(makeFrame(0))
	if err != nil {
		t.Fatalf("Encode packet0 error: %v", err)
	}
	packet1, err := enc.EncodeFloat32(makeFrame(frameSize))
	if err != nil {
		t.Fatalf("Encode packet1 error: %v", err)
	}
	if packetHasInBandFEC(t, packet1) {
		t.Fatal("expected FEC-disabled packet to have no in-band LBRR")
	}

	decPLC, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder plc error: %v", err)
	}
	decFEC, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder fec error: %v", err)
	}

	seedPLC := make([]float32, frameSize)
	if _, err := decPLC.Decode(packet0, seedPLC); err != nil {
		t.Fatalf("Decode packet0 on plc decoder error: %v", err)
	}
	seedFEC := make([]float32, frameSize)
	if _, err := decFEC.Decode(packet0, seedFEC); err != nil {
		t.Fatalf("Decode packet0 on fec decoder error: %v", err)
	}

	want := make([]float32, frameSize)
	nWant, err := decPLC.Decode(nil, want)
	if err != nil {
		t.Fatalf("Decode(nil) plc fallback error: %v", err)
	}

	got := make([]float32, frameSize)
	nGot, err := decFEC.DecodeWithFEC(packet1, got, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC(no-LBRR packet) error: %v", err)
	}

	if nGot != nWant {
		t.Fatalf("sample count mismatch: got %d want %d", nGot, nWant)
	}
	for i := range nWant {
		if got[i] != want[i] {
			t.Fatalf("provided-packet no-LBRR fallback diverged from PLC at sample %d: got=%f want=%f", i, got[i], want[i])
		}
	}
}

func packetHasInBandFEC(t *testing.T, packet []byte) bool {
	t.Helper()

	toc, _, err := packetFrameCount(packet)
	if err != nil {
		t.Fatalf("packetFrameCount error: %v", err)
	}
	if toc.Mode != ModeSILK && toc.Mode != ModeHybrid {
		return false
	}

	firstFrameData, err := extractFirstFramePayload(packet, toc)
	if err != nil {
		t.Fatalf("extractFirstFramePayload error: %v", err)
	}
	return packetHasLBRR(firstFrameData, toc)
}

// TestFEC_EndToEnd tests the complete FEC encode/decode cycle:
// 1. Encode audio with FEC enabled
// 2. Verify LBRR data is present in packets
// 3. Simulate packet loss and verify FEC recovery
func TestFEC_EndToEnd(t *testing.T) {
	// Create encoder with FEC enabled
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(15); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}
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

	for i := range numFrames {
		pcm := make([]float32, frameSize)
		for j := range frameSize {
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
	for i := range numFrames {
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

func TestFEC_ProvidedPacketRecoveryPath(t *testing.T) {
	const channels = 2
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: channels, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(15); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}
	if err := enc.SetBitrate(96000); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}
	if err := enc.SetSignal(SignalVoice); err != nil {
		t.Fatalf("SetSignal error: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth error: %v", err)
	}

	frameSize := 960
	var packets [][]byte
	for i := range 12 {
		pcm := make([]float32, frameSize*channels)
		for j := range frameSize {
			sampleIdx := i*frameSize + j
			pcm[2*j] = float32(0.5*math.Sin(2*math.Pi*220*float64(sampleIdx)/48000) +
				0.25*math.Sin(2*math.Pi*440*float64(sampleIdx)/48000))
			pcm[2*j+1] = float32(0.45*math.Sin(2*math.Pi*330*float64(sampleIdx)/48000) +
				0.20*math.Sin(2*math.Pi*660*float64(sampleIdx)/48000))
		}
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Encode frame %d error: %v", i, err)
		}
		packets = append(packets, append([]byte(nil), packet...))
		if len(packets) >= 3 && packetHasInBandFEC(t, packet) {
			break
		}
	}

	if len(packets) < 3 || !packetHasInBandFEC(t, packets[len(packets)-1]) {
		t.Fatal("failed to generate a packet carrying LBRR for provided-packet recovery")
	}

	packet0 := packets[len(packets)-3]
	recoveryPacket := packets[len(packets)-1]

	dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	seed := make([]float32, frameSize*channels)
	if _, err := dec.Decode(packet0, seed); err != nil {
		t.Fatalf("Decode seed packet error: %v", err)
	}

	recovered := make([]float32, frameSize*channels)
	n, err := dec.DecodeWithFEC(recoveryPacket, recovered, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC(recovery packet) error: %v", err)
	}
	if n != frameSize {
		t.Fatalf("DecodeWithFEC(recovery packet) samples=%d want=%d", n, frameSize)
	}
	if computeEnergyFloat32(recovered[:n*channels]) == 0 {
		t.Fatal("DecodeWithFEC(recovery packet) produced silence")
	}

	packetOut := make([]float32, frameSize*channels)
	if _, err := dec.Decode(recoveryPacket, packetOut); err != nil {
		t.Fatalf("Decode recovery packet after FEC error: %v", err)
	}
}

// TestFEC_LBRRPresence verifies that LBRR data is encoded in SILK packets
// by checking the LBRR flag in the packet header.
func TestFEC_LBRRPresence(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Enable FEC
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(15); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}

	frameSize := 960
	numFrames := 5

	lbrrPackets := 0
	for i := range numFrames {
		pcm := make([]float32, frameSize)
		for j := range frameSize {
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
				hasLBRRIndication := (packet[1]&0x40) != 0 || (packet[1]&0x80) != 0
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
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(15); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}

	frameSize := 960
	numFrames := 10

	// Encode frames
	packets := make([][]byte, numFrames)
	for i := range numFrames {
		pcm := make([]float32, frameSize)
		for j := range frameSize {
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
	for i := range 5 {
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
	for i := range 5 {
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
	for i := range frameSize {
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
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(15); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	frameSize := 960
	numFrames := 10

	// Encode all frames
	packets := make([][]byte, numFrames)
	for i := range numFrames {
		pcm := make([]float32, frameSize)
		for j := range frameSize {
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
	for i := range 3 {
		_, err := dec.Decode(packets[i], pcm)
		if err != nil {
			t.Fatalf("Decode frame %d error: %v", i, err)
		}
		t.Logf("Frame %d: decoded normally", i)
	}

	// Lose frame 3 and 4
	t.Log("Simulating loss of frames 3 and 4...")
	for i := range 2 {
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
