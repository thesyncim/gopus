package gopus

import (
	"math"
	"testing"
)

func TestDecodeWithFECRejectsOver120msBeforeCELTPLCFallback(t *testing.T) {
	cfg := DefaultDecoderConfig(48000, 1)
	cfg.MaxPacketSamples = 7000
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm := make([]float32, cfg.MaxPacketSamples)
	_, err = dec.DecodeWithFEC([]byte{GenerateTOC(31, false, 3), 0x07}, pcm, true)
	if err != ErrInvalidPacket {
		t.Fatalf("DecodeWithFEC error=%v want %v", err, ErrInvalidPacket)
	}
}

// TestDecodeWithFEC_FallbackToPLC verifies that DecodeWithFEC falls back to PLC
// when no FEC data is available (e.g., when no previous packet was decoded,
// or the previous packet was CELT-only mode).
func TestDecodeWithFEC_FallbackToPLC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Attempt FEC decode without any previous packet - should fall back to PLC
	frameSize := 960
	pcm := make([]float32, frameSize)

	// First FEC decode with no prior data - should use PLC and produce silence/zeros
	n, err := dec.DecodeWithFEC(nil, pcm, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC error (expected PLC fallback): %v", err)
	}
	if n != frameSize {
		t.Errorf("DecodeWithFEC returned %d samples, want %d", n, frameSize)
	}

	t.Logf("DecodeWithFEC fell back to PLC successfully, produced %d samples", n)
}

// TestDecodeWithFEC_CELTNoFEC verifies that CELT-only packets don't have FEC data.
// DecodeWithFEC should fall back to PLC after decoding a CELT packet.
func TestDecodeWithFEC_CELTNoFEC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Create a CELT packet (config 31 = CELT FB 20ms)
	celtPacket := make([]byte, 100)
	celtPacket[0] = GenerateTOC(31, false, 0) // CELT FB 20ms
	for i := 1; i < len(celtPacket); i++ {
		celtPacket[i] = byte(i)
	}

	frameSize := 960
	pcm := make([]float32, frameSize)

	// Decode the CELT packet normally
	_, err = dec.Decode(celtPacket, pcm)
	if err != nil {
		t.Fatalf("Decode CELT packet error: %v", err)
	}

	// Check that no FEC data was stored (CELT doesn't have LBRR)
	if dec.hasFEC {
		t.Error("hasFEC should be false after CELT packet decode")
	}

	// Attempt FEC decode - should fall back to PLC since no FEC available
	n, err := dec.DecodeWithFEC(nil, pcm, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC error (expected PLC fallback): %v", err)
	}
	if n != frameSize {
		t.Errorf("DecodeWithFEC returned %d samples, want %d", n, frameSize)
	}

	t.Logf("DecodeWithFEC correctly fell back to PLC for CELT mode")
}

// TestDecodeWithFEC_SILKNoLBRRDoesNotStoreFEC verifies that SILK packets only
// arm FEC state when their LBRR flag is present.
func TestDecodeWithFEC_SILKNoLBRRDoesNotStoreFEC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Create a SILK packet (config 9 = SILK WB 20ms)
	silkPacket := make([]byte, 100)
	silkPacket[0] = GenerateTOC(9, false, 0) // SILK WB 20ms
	for i := 1; i < len(silkPacket); i++ {
		silkPacket[i] = byte(i)
	}

	frameSize := 960
	pcm := make([]float32, frameSize)

	// Decode the SILK packet normally
	_, err = dec.Decode(silkPacket, pcm)
	if err != nil {
		t.Fatalf("Decode SILK packet error: %v", err)
	}

	if dec.hasFEC {
		t.Error("hasFEC should be false after SILK packet without LBRR")
	}

	t.Log("DecodeWithFEC correctly left FEC state clear for SILK without LBRR")
}

func TestStoreFECData_ReusesBackingBuffer(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	initialCap := cap(dec.fecData)
	if initialCap == 0 {
		t.Fatal("expected preallocated fecData backing buffer")
	}

	toc := TOC{
		Mode:      ModeSILK,
		Bandwidth: BandwidthWideband,
		Stereo:    false,
	}

	packetSmall := make([]byte, 32)
	packetLarge := make([]byte, 512)
	for i := range packetSmall {
		packetSmall[i] = byte(i)
	}
	for i := range packetLarge {
		packetLarge[i] = byte(255 - (i % 255))
	}
	packetSmall[0] |= 0x40
	packetLarge[0] |= 0x40

	dec.storeFECData(packetSmall, toc, 1, 960)
	if cap(dec.fecData) != initialCap {
		t.Fatalf("fecData cap changed after small packet: got %d want %d", cap(dec.fecData), initialCap)
	}

	dec.storeFECData(packetLarge, toc, 1, 960)
	if cap(dec.fecData) != initialCap {
		t.Fatalf("fecData cap changed after large packet: got %d want %d", cap(dec.fecData), initialCap)
	}
	if len(dec.fecData) != len(packetLarge) {
		t.Fatalf("fecData len = %d, want %d", len(dec.fecData), len(packetLarge))
	}
	if dec.fecData[0] != packetLarge[0] || dec.fecData[len(dec.fecData)-1] != packetLarge[len(packetLarge)-1] {
		t.Fatal("fecData content mismatch after copy")
	}
}

func TestStoreFECData_NoLBRRClearsState(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	dec.hasFEC = true
	dec.fecData = append(dec.fecData[:0], 0x40, 0x01)
	dec.fecMode = ModeSILK

	toc := TOC{
		Mode:      ModeSILK,
		Bandwidth: BandwidthWideband,
		FrameSize: 960,
	}
	dec.storeFECData([]byte{0x00, 0x01}, toc, 1, 960)
	if dec.hasFEC {
		t.Fatal("storeFECData kept FEC armed for packet without LBRR")
	}
}

func TestDecodeFECFrame_BufferSizingUsesSingleFrame(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Simulate stored FEC metadata from a 2-frame packet. decode_fec should still
	// only require one frame of output buffer.
	dec.hasFEC = true
	dec.fecData = []byte{0x01, 0x02, 0x03, 0x04}
	dec.fecMode = ModeSILK
	dec.fecBandwidth = BandwidthWideband
	dec.fecStereo = false
	dec.fecFrameSize = 960
	dec.fecFrameCount = 2

	pcm := make([]float32, 960)
	_, err = dec.decodeFECFrame(pcm, 0)
	if err == ErrBufferTooSmall {
		t.Fatal("decodeFECFrame rejected single-frame output buffer for multi-frame packet metadata")
	}
}

// TestDecodeWithFEC_HybridLBRRNormalDecodeDoesNotCacheFEC verifies that normal
// decode skips LBRR instead of caching it for a later nil decode_fec call.
func TestDecodeWithFEC_HybridLBRRNormalDecodeDoesNotCacheFEC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Use the minimal hybrid test packet
	packet := minimalHybridTestPacket20ms()
	frameSize := 960
	pcm := make([]float32, frameSize)

	// Decode the Hybrid packet normally
	_, err = dec.Decode(packet, pcm)
	if err != nil {
		t.Fatalf("Decode Hybrid packet error: %v", err)
	}

	if dec.hasFEC {
		t.Error("hasFEC should be false after normal Hybrid decode")
	}
}

// TestDecodeWithFEC_Recovery tests FEC recovery flow.
// This test simulates packet loss and FEC recovery.
func TestDecodeWithFEC_Recovery(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Decode a series of SILK packets to build up state
	silkPacket := make([]byte, 100)
	silkPacket[0] = GenerateTOC(9, false, 0) // SILK WB 20ms
	for i := 1; i < len(silkPacket); i++ {
		silkPacket[i] = byte(i * 3)
	}

	frameSize := 960
	pcm1 := make([]float32, frameSize)
	pcm2 := make([]float32, frameSize)

	// Decode packet 1
	_, err = dec.Decode(silkPacket, pcm1)
	if err != nil {
		t.Fatalf("Decode packet 1 error: %v", err)
	}

	// Simulate packet 2 is lost - use FEC to recover
	// In real usage, we'd use the NEXT packet's LBRR to recover the lost one.
	// For this test, we just verify DecodeWithFEC works without crashing.
	n, err := dec.DecodeWithFEC(nil, pcm2, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC error: %v", err)
	}
	if n != frameSize {
		t.Errorf("DecodeWithFEC returned %d samples, want %d", n, frameSize)
	}

	t.Logf("FEC recovery produced %d samples", n)
}

func TestDecodeWithFEC_UsesProvidedPacketAndPreservesNormalDecode(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(15); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}

	frameSize := 960
	makeFrame := func(phase float64) []float32 {
		pcm := make([]float32, frameSize)
		for i := range pcm {
			tm := (float64(i) + phase) / 48000.0
			pcm[i] = float32(0.5*math.Sin(2*math.Pi*440*tm) + 0.2*math.Sin(2*math.Pi*880*tm))
		}
		return pcm
	}

	pktBuf := make([]byte, 4000)
	encodeFrame := func(pcm []float32) []byte {
		n, err := enc.Encode(pcm, pktBuf)
		if err != nil {
			t.Fatalf("Encode error: %v", err)
		}
		if n == 0 {
			t.Fatal("unexpected DTX suppression while generating FEC test packet")
		}
		packet := make([]byte, n)
		copy(packet, pktBuf[:n])
		return packet
	}

	packet0 := encodeFrame(makeFrame(0))
	_ = encodeFrame(makeFrame(960)) // packet 1 intentionally "lost" in decode sequence
	packet2 := encodeFrame(makeFrame(1920))

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm0 := make([]float32, frameSize)
	if _, err := dec.Decode(packet0, pcm0); err != nil {
		t.Fatalf("Decode packet0 error: %v", err)
	}

	// Recover the missing packet using packet2's LBRR.
	pcmFEC := make([]float32, frameSize)
	nFEC, err := dec.DecodeWithFEC(packet2, pcmFEC, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) error: %v", err)
	}
	if nFEC != frameSize {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) samples=%d want=%d", nFEC, frameSize)
	}

	// The same packet must still decode normally after FEC recovery.
	pcm2 := make([]float32, frameSize)
	n2, err := dec.Decode(packet2, pcm2)
	if err != nil {
		t.Fatalf("Decode packet2 after FEC recovery error: %v", err)
	}
	if n2 != frameSize {
		t.Fatalf("Decode packet2 after FEC recovery samples=%d want=%d", n2, frameSize)
	}
}

func TestDecodeWithFEC_FrameSizeTransitionUsesProvidedPacketGranularity(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(15); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}

	makeFrame := func(frameSize int, phase float64) []float32 {
		pcm := make([]float32, frameSize)
		for i := range pcm {
			tm := (float64(i) + phase) / 48000.0
			pcm[i] = float32(0.5*math.Sin(2*math.Pi*330*tm) + 0.2*math.Sin(2*math.Pi*660*tm))
		}
		return pcm
	}

	pktBuf := make([]byte, 4000)
	encodeFrame := func(frameSize int, phase float64) []byte {
		if err := enc.SetFrameSize(frameSize); err != nil {
			t.Fatalf("SetFrameSize(%d) error: %v", frameSize, err)
		}
		n, err := enc.Encode(makeFrame(frameSize, phase), pktBuf)
		if err != nil {
			t.Fatalf("Encode(%d) error: %v", frameSize, err)
		}
		if n == 0 {
			t.Fatal("unexpected DTX suppression while generating frame-size transition packet")
		}
		packet := make([]byte, n)
		copy(packet, pktBuf[:n])
		return packet
	}

	packet0 := encodeFrame(1920, 0)
	_ = encodeFrame(960, 1920) // packet 1 intentionally "lost"
	packet2 := encodeFrame(960, 2880)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm0 := make([]float32, 1920)
	if _, err := dec.Decode(packet0, pcm0); err != nil {
		t.Fatalf("Decode packet0 error: %v", err)
	}

	// Recover the missing 20ms packet from packet2 LBRR while previous decode
	// state still reflects a 40ms frame. The output buffer length is the
	// caller's requested decode_fec frame size, matching libopus' frame_size.
	pcmFEC := make([]float32, 960)
	nFEC, err := dec.DecodeWithFEC(packet2, pcmFEC, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) error: %v", err)
	}
	if nFEC != 960 {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) samples=%d want=960", nFEC)
	}

	pcm2 := make([]float32, 960)
	n2, err := dec.Decode(packet2, pcm2)
	if err != nil {
		t.Fatalf("Decode packet2 after FEC recovery error: %v", err)
	}
	if n2 != 960 {
		t.Fatalf("Decode packet2 after FEC recovery samples=%d want=960", n2)
	}
}

func TestDecodeWithFEC_ProvidedCELTPacketFallsBackToPLC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Seed last frame size/state with a normal decode.
	celtPacket := make([]byte, 100)
	celtPacket[0] = GenerateTOC(31, false, 0) // CELT FB 20ms
	for i := 1; i < len(celtPacket); i++ {
		celtPacket[i] = byte(i)
	}

	pcm := make([]float32, 960)
	if _, err := dec.Decode(celtPacket, pcm); err != nil {
		t.Fatalf("Decode CELT packet error: %v", err)
	}

	// CELT has no LBRR, so this should transparently fall back to PLC.
	n, err := dec.DecodeWithFEC(celtPacket, pcm, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC(CELT packet, fec=true) error: %v", err)
	}
	if n != 960 {
		t.Fatalf("DecodeWithFEC(CELT packet, fec=true) samples=%d want=%d", n, 960)
	}
}

func TestDecodeWithFEC_ProvidedCELTPacketClearsStoredFECState(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	dec.hasFEC = true
	dec.fecData = append(dec.fecData[:0], 0x40, 0x01)
	dec.fecMode = ModeSILK
	dec.fecFrameSize = 960
	pcm := make([]float32, 960)

	celtPacket := make([]byte, 100)
	celtPacket[0] = GenerateTOC(31, false, 0)
	for i := 1; i < len(celtPacket); i++ {
		celtPacket[i] = byte(i)
	}
	if _, err := dec.DecodeWithFEC(celtPacket, pcm, true); err != nil {
		t.Fatalf("DecodeWithFEC(CELT packet, fec=true) error: %v", err)
	}
	if dec.hasFEC {
		t.Fatalf("hasFEC should be false after CELT-based decode_fec fallback")
	}
}

func TestDecodeWithFEC_ProvidedPacketUsesPacketModeForCELTGate(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(15); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}

	frameSize := 960
	makeFrame := func(phase float64) []float32 {
		pcm := make([]float32, frameSize)
		for i := range pcm {
			tm := (float64(i) + phase) / 48000.0
			pcm[i] = float32(0.5*math.Sin(2*math.Pi*440*tm) + 0.2*math.Sin(2*math.Pi*880*tm))
		}
		return pcm
	}

	pktBuf := make([]byte, 4000)
	encodeFrame := func(pcm []float32) []byte {
		n, err := enc.Encode(pcm, pktBuf)
		if err != nil {
			t.Fatalf("Encode error: %v", err)
		}
		if n == 0 {
			t.Fatal("unexpected DTX suppression while generating FEC test packet")
		}
		packet := make([]byte, n)
		copy(packet, pktBuf[:n])
		return packet
	}

	packet0 := encodeFrame(makeFrame(0))
	_ = encodeFrame(makeFrame(960)) // packet 1 intentionally "lost"
	packet2 := encodeFrame(makeFrame(1920))
	toc, _, err := packetFrameCount(packet2)
	if err != nil {
		t.Fatalf("packetFrameCount(packet2) error: %v", err)
	}
	if toc.Mode == ModeCELT {
		t.Fatalf("test setup invalid: packet2 mode resolved to CELT")
	}

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm0 := make([]float32, frameSize)
	if _, err := dec.Decode(packet0, pcm0); err != nil {
		t.Fatalf("Decode packet0 error: %v", err)
	}
	if dec.lastPacketMode == ModeCELT {
		t.Fatalf("test setup invalid: first packet mode resolved to CELT")
	}

	// Simulate transient prevMode=CELLT (e.g., after redundancy/PLC path).
	// decode_fec gating should still use packet-mode state (lastPacketMode).
	dec.prevMode = ModeCELT

	pcmFEC := make([]float32, frameSize)
	nFEC, err := dec.DecodeWithFEC(packet2, pcmFEC, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) error: %v", err)
	}
	if nFEC != frameSize {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) samples=%d want=%d", nFEC, frameSize)
	}
	if dec.prevMode != toc.Mode {
		t.Fatalf("prevMode=%v want %v (provided packet mode must override transient PLC mode)", dec.prevMode, toc.Mode)
	}
}

func TestDecodeWithFEC_ProvidedPacketWithoutLBRRUsesDirectPLCFallback(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(false)
	if err := enc.SetBitrate(12000); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}
	if err := enc.SetSignal(SignalVoice); err != nil {
		t.Fatalf("SetSignal error: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthWideband); err != nil {
		t.Fatalf("SetBandwidth error: %v", err)
	}

	const frameSize = 960
	makeFrame := func(phase float64) []float32 {
		pcm := make([]float32, frameSize)
		for i := range pcm {
			tm := (float64(i) + phase) / 48000.0
			pcm[i] = float32(0.4*math.Sin(2*math.Pi*220*tm) + 0.1*math.Sin(2*math.Pi*440*tm))
		}
		return pcm
	}

	pktBuf := make([]byte, 4000)
	encodeFrame := func(pcm []float32) []byte {
		n, err := enc.Encode(pcm, pktBuf)
		if err != nil {
			t.Fatalf("Encode error: %v", err)
		}
		if n == 0 {
			t.Fatal("unexpected DTX suppression while generating no-LBRR test packet")
		}
		packet := make([]byte, n)
		copy(packet, pktBuf[:n])
		return packet
	}

	packet0 := encodeFrame(makeFrame(0))
	_ = encodeFrame(makeFrame(960)) // packet 1 intentionally "lost"
	packet2 := encodeFrame(makeFrame(1920))

	toc, _, err := packetFrameCount(packet2)
	if err != nil {
		t.Fatalf("packetFrameCount(packet2) error: %v", err)
	}
	if toc.Mode == ModeCELT {
		t.Fatalf("test setup invalid: packet2 mode resolved to CELT")
	}
	firstFrameData, err := extractFirstFramePayload(packet2, toc)
	if err != nil {
		t.Fatalf("extractFirstFramePayload(packet2) error: %v", err)
	}
	if packetHasLBRR(firstFrameData, toc) {
		t.Fatalf("test setup invalid: packet2 unexpectedly has LBRR")
	}

	decExpected, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder(expected) error: %v", err)
	}
	decActual, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder(actual) error: %v", err)
	}

	pcm0 := make([]float32, frameSize)
	if _, err := decExpected.Decode(packet0, pcm0); err != nil {
		t.Fatalf("Decode packet0 (expected) error: %v", err)
	}
	if _, err := decActual.Decode(packet0, pcm0); err != nil {
		t.Fatalf("Decode packet0 (actual) error: %v", err)
	}

	pcmExpected := make([]float32, frameSize)
	nExpected, err := decExpected.decodePLCForFECWithState(pcmExpected, frameSize, frameSize, toc.Mode, toc.Bandwidth, toc.Stereo)
	if err != nil {
		t.Fatalf("decodePLCForFECWithState(expected) error: %v", err)
	}
	if nExpected != frameSize {
		t.Fatalf("decodePLCForFECWithState(expected) samples=%d want=%d", nExpected, frameSize)
	}

	pcmActual := make([]float32, frameSize)
	nActual, err := decActual.DecodeWithFEC(packet2, pcmActual, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) error: %v", err)
	}
	if nActual != frameSize {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) samples=%d want=%d", nActual, frameSize)
	}

	const tol = 1e-7
	for i := 0; i < frameSize; i++ {
		if math.Abs(float64(pcmExpected[i]-pcmActual[i])) > tol {
			t.Fatalf("sample %d mismatch: expected=%v actual=%v", i, pcmExpected[i], pcmActual[i])
		}
	}
}

func TestDecodeWithFEC_PLCWithProvidedStateUsesProvidedMode(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	frameSize := 960
	packet := minimalHybridTestPacket20ms()
	pcmPrime := make([]float32, frameSize)
	if _, err := dec.Decode(packet, pcmPrime); err != nil {
		t.Fatalf("Decode prime packet error: %v", err)
	}

	// Force decoder transient PLC mode to CELT and verify provided state wins.
	dec.prevMode = ModeCELT
	dec.prevRedundancy = false
	dec.haveDecoded = true

	pcmPLC := make([]float32, frameSize)
	n, err := dec.decodePLCForFECWithState(pcmPLC, frameSize, frameSize, ModeHybrid, BandwidthFullband, false)
	if err != nil {
		t.Fatalf("decodePLCForFECWithState error: %v", err)
	}
	if n != frameSize {
		t.Fatalf("decodePLCForFECWithState samples=%d want=%d", n, frameSize)
	}
	if dec.prevMode != ModeHybrid {
		t.Fatalf("prevMode=%v want=%v (provided PLC mode must be honored)", dec.prevMode, ModeHybrid)
	}
}

// TestDecodeWithFEC_NoFECRequested verifies that when fec=false, DecodeWithFEC
// behaves identically to Decode.
func TestDecodeWithFEC_NoFECRequested(t *testing.T) {
	dec1, _ := NewDecoder(DefaultDecoderConfig(48000, 1))
	dec2, _ := NewDecoder(DefaultDecoderConfig(48000, 1))

	packet := minimalHybridTestPacket20ms()
	frameSize := 960

	pcm1 := make([]float32, frameSize)
	pcm2 := make([]float32, frameSize)

	// Decode with Decode
	n1, err1 := dec1.Decode(packet, pcm1)
	// Decode with DecodeWithFEC(fec=false)
	n2, err2 := dec2.DecodeWithFEC(packet, pcm2, false)

	if err1 != err2 {
		t.Errorf("Errors differ: Decode=%v, DecodeWithFEC=%v", err1, err2)
	}
	if n1 != n2 {
		t.Errorf("Sample counts differ: Decode=%d, DecodeWithFEC=%d", n1, n2)
	}

	// Verify samples are identical
	for i := 0; i < n1*1; i++ {
		if pcm1[i] != pcm2[i] {
			t.Errorf("Sample %d differs: Decode=%v, DecodeWithFEC=%v", i, pcm1[i], pcm2[i])
			break
		}
	}

	t.Log("DecodeWithFEC(fec=false) behaves identically to Decode")
}

// TestDecodeWithFEC_ResetClearsFEC verifies that Reset clears FEC state.
func TestDecodeWithFEC_ResetClearsFEC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	dec.hasFEC = true
	dec.fecData = append(dec.fecData[:0], 0x40, 0x01)
	dec.fecMode = ModeSILK
	dec.fecFrameSize = 960

	// Reset the decoder
	dec.Reset()

	// FEC state should be cleared
	if dec.hasFEC {
		t.Error("hasFEC should be false after Reset")
	}
	if dec.fecFrameSize != 0 {
		t.Error("fecFrameSize should be 0 after Reset")
	}

	t.Log("Reset correctly clears FEC state")
}
