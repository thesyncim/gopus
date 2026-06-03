package main

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestDemosRun exercises both loss-recovery demos end to end, the same way
// `go run .` does, so the example stays runnable.
func TestDemosRun(t *testing.T) {
	frames := speechFrames(40)
	if err := demoPLC(frames); err != nil {
		t.Fatalf("demoPLC: %v", err)
	}
	if err := demoFEC(frames); err != nil {
		t.Fatalf("demoFEC: %v", err)
	}
}

// TestFECRecoversRealAudio verifies the FEC path reconstructs real speech-band
// energy from LBRR rather than silently emitting concealment.
func TestFECRecoversRealAudio(t *testing.T) {
	frames := speechFrames(40)

	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: gopus.ApplicationVoIP,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetMode(gopus.EncoderModeSILK); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if err := enc.SetBandwidth(gopus.BandwidthWideband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetSignal(gopus.SignalVoice); err != nil {
		t.Fatalf("SetSignal: %v", err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(20); err != nil {
		t.Fatalf("SetPacketLoss: %v", err)
	}

	packets := make([][]byte, len(frames))
	for i, frame := range frames {
		pkt, err := enc.EncodeFloat32(frame)
		if err != nil {
			t.Fatalf("encode frame %d: %v", i, err)
		}
		packets[i] = pkt
	}

	const lostIndex = 20
	recovered, err := decodeStreamWithLoss(packets, lostIndex)
	if err != nil {
		t.Fatalf("decodeStreamWithLoss: %v", err)
	}
	if len(recovered) != frameSize*channels {
		t.Fatalf("recovered frame length = %d, want %d", len(recovered), frameSize*channels)
	}

	rms := math.Sqrt(frameEnergy(recovered))
	if rms < 0.01 {
		t.Fatalf("recovered frame RMS = %.5f, want > 0.01 (FEC should reconstruct real audio)", rms)
	}
}
