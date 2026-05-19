//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package gopus

import (
	"bytes"
	"testing"

	encpkg "github.com/thesyncim/gopus/encoder"
)

// encodeUntilMultistreamDREDPacket exercises the public multistream encoder
// with one coupled stream, matching the libopus multistream helper path.
func encodeUntilMultistreamDREDPacket(t *testing.T, mode encpkg.Mode, bandwidth Bandwidth, frameSize, channels, streams, coupledStreams int, mapping []byte) ([]byte, []byte, int) {
	t.Helper()

	enc, err := NewMultistreamEncoder(48000, channels, streams, coupledStreams, mapping, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoder error: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize error: %v", err)
	}
	if err := enc.SetBandwidth(bandwidth); err != nil {
		t.Fatalf("SetBandwidth error: %v", err)
	}
	if err := enc.SetBitrate(encoderDREDBitrateForFrameSize(frameSize)); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}
	if err := enc.SetSignal(SignalMusic); err != nil {
		t.Fatalf("SetSignal error: %v", err)
	}
	if err := enc.SetPacketLoss(20); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}
	if err := enc.SetDNNBlob(requireLibopusEncoderNeuralModelBlob(t)); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if err := enc.SetDREDDuration(80); err != nil {
		t.Fatalf("SetDREDDuration error: %v", err)
	}
	// Reach inside via the unexported multistream.Encoder field to force the
	// requested base mode on all stream encoders. This mirrors the
	// enc.enc.SetMode(mode) hop used by the single-stream parity tests.
	enc.enc.SetMode(mode)

	wantMode, err := encoderModeToPublic(mode)
	if err != nil {
		t.Fatal(err)
	}

	packet := make([]byte, maxPacketBytesPerStream*streams)
	for frameIdx := 0; frameIdx < 640; frameIdx++ {
		pcm := encoderDREDFrame(frameIdx, frameSize, enc.SampleRate(), channels)
		n, err := enc.Encode(pcm, packet)
		if err != nil {
			t.Fatalf("Encode(frame=%d) error: %v", frameIdx, err)
		}
		if n == 0 {
			// DTX-suppressed packet; keep searching.
			continue
		}
		gotPacket := append([]byte(nil), packet[:n]...)
		toc := ParseTOC(gotPacket[0])
		packetDuration, err := opusPacketDurationSamples(gotPacket)
		if err != nil {
			t.Fatalf("parse packet duration frame=%d: %v", frameIdx, err)
		}
		if toc.Mode != wantMode || toc.Bandwidth != bandwidth || packetDuration != frameSize {
			continue
		}
		payload, frameOffset, ok, err := findDREDPayload(gotPacket)
		if err != nil {
			t.Fatalf("findDREDPayload(frame=%d) error: %v", frameIdx, err)
		}
		if ok {
			return gotPacket, append([]byte(nil), payload...), frameOffset
		}
	}
	t.Fatalf("no DRED packet emitted via multistream encoder for mode=%v bandwidth=%v frameSize=%d", mode, bandwidth, frameSize)
	return nil, nil, 0
}

func TestMultistreamEncoderCarriedDREDPayloadMatchesLibopusSilkWideband20msStereo(t *testing.T) {
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:   960,
		ForceMode:   ModeSILK,
		Bandwidth:   BandwidthWideband,
		Channels:    2,
		Multistream: true,
	})
	if err != nil {
		t.Skipf("libopus stereo silk DRED packet helper unavailable: %v", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus stereo silk packet missing DRED payload")
	}

	// 2-channel stereo via one coupled stream: streams=1, coupledStreams=1,
	// mapping={0,1} routes input ch0 -> left, ch1 -> right of stream 0.
	mapping := []byte{0, 1}
	gotPacket, gotPayload, gotOffset := encodeUntilMultistreamDREDPacket(
		t, encpkg.ModeSILK, BandwidthWideband, 960, 2, 1, 1, mapping,
	)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeSILK || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want silk stereo", toc)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func TestMultistreamEncoderCarriedDREDPayloadMatchesLibopusCELTFullband20msStereo(t *testing.T) {
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:   960,
		ForceMode:   ModeCELT,
		Bandwidth:   BandwidthFullband,
		Channels:    2,
		Multistream: true,
	})
	if err != nil {
		t.Skipf("libopus stereo CELT DRED packet helper unavailable: %v", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus stereo CELT packet missing DRED payload")
	}

	mapping := []byte{0, 1}
	gotPacket, gotPayload, gotOffset := encodeUntilMultistreamDREDPacket(
		t, encpkg.ModeCELT, BandwidthFullband, 960, 2, 1, 1, mapping,
	)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeCELT || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want celt stereo", toc)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func TestMultistreamEncoderCarriedDREDPayloadMatchesLibopusHybridFullband20msStereo(t *testing.T) {
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:   960,
		ForceMode:   ModeHybrid,
		Bandwidth:   BandwidthFullband,
		Channels:    2,
		Multistream: true,
	})
	if err != nil {
		t.Skipf("libopus stereo hybrid DRED packet helper unavailable: %v", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus stereo hybrid packet missing DRED payload")
	}

	mapping := []byte{0, 1}
	gotPacket, gotPayload, gotOffset := encodeUntilMultistreamDREDPacket(
		t, encpkg.ModeHybrid, BandwidthFullband, 960, 2, 1, 1, mapping,
	)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeHybrid || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want hybrid stereo", toc)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}
