package silk

import (
	"bytes"
	"math"
	"testing"
)

func makeMonoPacketSignal(sampleRate, samples int) []float32 {
	pcm := make([]float32, samples)
	for i := 0; i < samples; i++ {
		tm := float64(i) / float64(sampleRate)
		pcm[i] = 0.35*float32(math.Sin(2*math.Pi*420.0*tm)) + 0.2*float32(math.Sin(2*math.Pi*910.0*tm))
	}
	return pcm
}

func primeWidebandEncoder(enc *Encoder) {
	prime := makeMonoPacketSignal(16000, 320)
	_ = enc.EncodeFrame(prime, nil, true)
}

func TestEncodePacketWithFECWithVADStatesUsesPerFrameState(t *testing.T) {
	frame := 320 // 20ms at 16kHz (WB)
	pcm := makeMonoPacketSignal(16000, frame*2)
	vadFlags := []bool{true, true}

	low := VADFrameState{
		SpeechActivityQ8: 0,
		InputTiltQ15:     -20000,
		InputQualityBandsQ15: [4]int{
			0, 0, 0, 0,
		},
		Valid: true,
	}
	high := VADFrameState{
		SpeechActivityQ8: 255,
		InputTiltQ15:     20000,
		InputQualityBandsQ15: [4]int{
			32767, 32767, 32767, 32767,
		},
		Valid: true,
	}

	encA := NewEncoder(BandwidthWideband)
	encA.SetBitrate(22000)
	encA.SetVBR(true)
	primeWidebandEncoder(encA)
	outFirstLow := encA.EncodePacketWithFECWithVADStates(pcm, nil, vadFlags, []VADFrameState{low, high})

	encB := NewEncoder(BandwidthWideband)
	encB.SetBitrate(22000)
	encB.SetVBR(true)
	primeWidebandEncoder(encB)
	outFirstHigh := encB.EncodePacketWithFECWithVADStates(pcm, nil, vadFlags, []VADFrameState{high, high})

	if len(outFirstLow) == 0 || len(outFirstHigh) == 0 {
		t.Fatal("expected non-empty SILK packets")
	}
	if bytes.Equal(outFirstLow, outFirstHigh) {
		t.Fatal("expected packet bitstream to change when first-frame VAD state changes")
	}
}
