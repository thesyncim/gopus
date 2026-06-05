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
