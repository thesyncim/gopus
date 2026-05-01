//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package gopus

import (
	"bytes"
	"fmt"
	"math"
	"sync"
	"testing"

	encpkg "github.com/thesyncim/gopus/encoder"
)

var (
	libopusDREDEncoderModelBlobHelperOnce sync.Once
	libopusDREDEncoderModelBlobHelperPath string
	libopusDREDEncoderModelBlobHelperErr  error
)

func getLibopusDREDEncoderModelBlobHelperPath() (string, error) {
	libopusDREDEncoderModelBlobHelperOnce.Do(func() {
		libopusDREDEncoderModelBlobHelperPath, libopusDREDEncoderModelBlobHelperErr = buildLibopusDREDHelper("libopus_dred_encoder_model_blob.c", "gopus_libopus_dred_encoder_model_blob", true)
	})
	if libopusDREDEncoderModelBlobHelperErr != nil {
		return "", libopusDREDEncoderModelBlobHelperErr
	}
	return libopusDREDEncoderModelBlobHelperPath, nil
}

func probeLibopusDREDEncoderModelBlob() ([]byte, error) {
	binPath, err := getLibopusDREDEncoderModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	return runModelBlobHelper(binPath)
}

func probeLibopusPitchDNNModelBlob() ([]byte, error) {
	binPath, err := getLibopusPitchDNNModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	return runModelBlobHelper(binPath)
}

func probeLibopusEncoderNeuralModelBlob() ([]byte, error) {
	pitchBlob, err := probeLibopusPitchDNNModelBlob()
	if err != nil {
		return nil, err
	}
	dredBlob, err := probeLibopusDREDEncoderModelBlob()
	if err != nil {
		return nil, err
	}
	blob := make([]byte, 0, len(pitchBlob)+len(dredBlob))
	blob = append(blob, pitchBlob...)
	blob = append(blob, dredBlob...)
	return blob, nil
}

func requireLibopusEncoderNeuralModelBlob(t *testing.T) []byte {
	t.Helper()
	blob, err := probeLibopusEncoderNeuralModelBlob()
	if err != nil {
		t.Skipf("libopus encoder neural model helper unavailable: %v", err)
	}
	return blob
}

func encoderDREDBitrateForFrameSize(frameSize int) int {
	bitrate := 40000
	if frameSize > 0 && frameSize < 960 {
		bitrate = (40000 * 960) / frameSize
	}
	if bitrate > 320000 {
		bitrate = 320000
	}
	return bitrate
}

func encoderDREDVoicedSample(frameIdx, sampleIdx, frameSize, sampleRate int) float32 {
	n := frameIdx*frameSize + sampleIdx
	t := float64(n) / float64(sampleRate)
	env := 0.82 + 0.18*math.Sin(2*math.Pi*1.3*t)
	s := 0.0
	s += 0.28 * math.Sin(2*math.Pi*110*t)
	s += 0.17 * math.Sin(2*math.Pi*220*t+0.11)
	s += 0.09 * math.Sin(2*math.Pi*330*t+0.23)
	s += 0.05 * math.Sin(2*math.Pi*440*t+0.37)
	return float32(env * s)
}

func encoderDREDFrame(frameIdx, frameSize, sampleRate, channels int) []float32 {
	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		s := encoderDREDVoicedSample(frameIdx, i, frameSize, sampleRate)
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = s
		}
	}
	return pcm
}

func encoderModeToPublic(mode encpkg.Mode) (Mode, error) {
	switch mode {
	case encpkg.ModeSILK:
		return ModeSILK, nil
	case encpkg.ModeHybrid:
		return ModeHybrid, nil
	case encpkg.ModeCELT:
		return ModeCELT, nil
	default:
		return 0, fmt.Errorf("unsupported encoder mode %v", mode)
	}
}

func encodeUntilDREDPacket(t *testing.T, mode encpkg.Mode, bandwidth Bandwidth, frameSize, channels int) ([]byte, []byte, int) {
	t.Helper()

	cfg := EncoderConfig{
		SampleRate:  48000,
		Channels:    channels,
		Application: ApplicationAudio,
	}
	enc, err := NewEncoder(cfg)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
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
	enc.enc.SetMode(mode)

	wantMode, err := encoderModeToPublic(mode)
	if err != nil {
		t.Fatal(err)
	}

	packet := make([]byte, maxPacketBytesPerStream)
	for frameIdx := 0; frameIdx < 640; frameIdx++ {
		pcm := encoderDREDFrame(frameIdx, frameSize, cfg.SampleRate, cfg.Channels)
		n, err := enc.Encode(pcm, packet)
		if err != nil {
			t.Fatalf("Encode(frame=%d) error: %v", frameIdx, err)
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
	t.Fatalf("no DRED packet emitted for mode=%v bandwidth=%v frameSize=%d", mode, bandwidth, frameSize)
	return nil, nil, 0
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWideband20ms(t *testing.T) {
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
	})
	if err != nil {
		t.Skipf("libopus DRED packet helper unavailable: %v", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus silk packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, 960, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeSILK {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeSILK)
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
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWideband40ms(t *testing.T) {
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 1920,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
	})
	if err != nil {
		t.Skipf("libopus 40 ms DRED packet helper unavailable: %v", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus 40 ms silk packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, 1920, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeSILK {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeSILK)
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
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWideband60ms(t *testing.T) {
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 2880,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
	})
	if err != nil {
		t.Skipf("libopus 60 ms DRED packet helper unavailable: %v", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus 60 ms silk packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, 2880, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeSILK {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeSILK)
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
}

func TestEncoderCarriedDREDPrimaryBudgetMatchesLibopusSilkWideband20ms(t *testing.T) {
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
	})
	if err != nil {
		t.Skipf("libopus DRED packet helper unavailable: %v", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus silk packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, 960, 1)
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if len(gotPacket) <= len(gotPayload)+4 || len(packetInfo.packet) <= len(wantPayload)+4 {
		t.Fatalf("packet too short for primary budget check: got=%d/%d want=%d/%d", len(gotPacket), len(gotPayload), len(packetInfo.packet), len(wantPayload))
	}
	if gotPacket[0] != packetInfo.packet[0] || gotPacket[1] != packetInfo.packet[1] {
		t.Fatalf("packet header=%x want %x", gotPacket[:2], packetInfo.packet[:2])
	}
	gotExtID := gotPacket[len(gotPacket)-len(gotPayload)-1]
	wantExtID := packetInfo.packet[len(packetInfo.packet)-len(wantPayload)-1]
	if gotExtID != wantExtID {
		t.Fatalf("extension id=%02x want %02x", gotExtID, wantExtID)
	}
	gotPrimary := gotPacket[3 : len(gotPacket)-len(gotPayload)-1]
	wantPrimary := packetInfo.packet[3 : len(packetInfo.packet)-len(wantPayload)-1]
	if !bytes.Equal(gotPrimary, wantPrimary) {
		t.Fatalf("primary packet bytes mismatch\n got=%x\nwant=%x", gotPrimary, wantPrimary)
	}
}
