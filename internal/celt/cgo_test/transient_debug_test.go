package cgo

import (
	
	"math"
	"testing"
	"github.com/thesyncim/gopus/internal/celt"
)

func TestTransientDebug(t *testing.T) {
	frameSize := 960
	channels := 1
	sampleRate := 48000

	// Generate 440Hz sine wave (just like the test)
	pcm64 := make([]float64, frameSize)
	for i := range pcm64 {
		pcm64[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate))
	}

	enc := celt.NewEncoder(channels)
	enc.Reset()

	overlap := celt.Overlap
	delayComp := 192

	// First frame scenario - fresh encoder
	t.Log("=== Frame 0 Transient Analysis Debug ===")
	t.Logf("Frame size: %d, Overlap: %d, DelayComp: %d", frameSize, overlap, delayComp)
	
	// Step 1: DC rejection
	dcRejected := enc.ApplyDCReject(pcm64)
	t.Logf("\nDC Rejected samples [0:5]: %.6f, %.6f, %.6f, %.6f, %.6f",
		dcRejected[0], dcRejected[1], dcRejected[2], dcRejected[3], dcRejected[4])
	
	// Step 2: Delay buffer (initially zeros)
	delayBuf := make([]float64, delayComp)
	combinedBuf := make([]float64, delayComp+len(dcRejected))
	copy(combinedBuf[:delayComp], delayBuf) // zeros
	copy(combinedBuf[delayComp:], dcRejected)
	samplesForFrame := combinedBuf[:frameSize]
	
	t.Logf("\nSamples for frame [0:5]: %.6f, %.6f, %.6f, %.6f, %.6f",
		samplesForFrame[0], samplesForFrame[1], samplesForFrame[2], samplesForFrame[3], samplesForFrame[4])
	t.Logf("Samples for frame [192:197]: %.6f, %.6f, %.6f, %.6f, %.6f",
		samplesForFrame[192], samplesForFrame[193], samplesForFrame[194], samplesForFrame[195], samplesForFrame[196])
	
	// Step 3: Pre-emphasis with scaling
	preemph := enc.ApplyPreemphasisWithScaling(samplesForFrame)
	
	t.Logf("\nPre-emphasized [0:5]: %.1f, %.1f, %.1f, %.1f, %.1f",
		preemph[0], preemph[1], preemph[2], preemph[3], preemph[4])
	t.Logf("Pre-emphasized [192:197]: %.1f, %.1f, %.1f, %.1f, %.1f",
		preemph[192], preemph[193], preemph[194], preemph[195], preemph[196])
		
	// Compute energy of pre-emphasized signal
	var preemphEnergy float64
	for _, v := range preemph {
		preemphEnergy += v * v
	}
	t.Logf("Pre-emphasis total energy: %.2f", preemphEnergy)
	
	// Step 4: Build transient input
	// preemphBuffer is initialized to zeros for frame 0
	preemphBufSize := overlap * channels
	preemphBuffer := make([]float64, preemphBufSize) // all zeros
	
	transientInput := make([]float64, (overlap+frameSize)*channels)
	copy(transientInput[:preemphBufSize], preemphBuffer) // zeros for frame 0
	copy(transientInput[preemphBufSize:], preemph)
	
	t.Logf("\nTransient input length: %d (overlap=%d + frame=%d)", 
		len(transientInput), overlap, frameSize)
	t.Logf("Transient input [0:5] (overlap - should be zeros): %.1f, %.1f, %.1f, %.1f, %.1f",
		transientInput[0], transientInput[1], transientInput[2], transientInput[3], transientInput[4])
	t.Logf("Transient input [115:125] (around overlap/frame boundary):")
	for i := 115; i < 130; i++ {
		t.Logf("  [%d]: %.1f", i, transientInput[i])
	}
	
	// Step 5: Run transient analysis
	result := enc.TransientAnalysis(transientInput, frameSize+overlap, false)
	
	t.Log("\n=== Transient Analysis Result ===")
	t.Logf("IsTransient: %v", result.IsTransient)
	t.Logf("MaskMetric: %.4f (threshold=200)", result.MaskMetric)
	t.Logf("TfEstimate: %.4f", result.TfEstimate)
	t.Logf("ToneFreq: %.6f rad/sample (%.1f Hz)", result.ToneFreq, result.ToneFreq*float64(sampleRate)/(2*math.Pi))
	t.Logf("Toneishness: %.6f", result.Toneishness)
	
	// NOW test what libopus does
	t.Log("\n=== Comparing with libopus ===")
	libEnc, err := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("libopus encoder creation failed: %v", err)
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(64000)
	libEnc.SetComplexity(5)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	
	pcm32 := make([]float32, frameSize)
	for i := range pcm32 {
		pcm32[i] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate)))
	}
	
	libPacket, libLen := libEnc.EncodeFloat(pcm32, frameSize)
	if libLen <= 0 {
		t.Fatalf("libopus encode failed")
	}
	
	// Parse libopus first payload byte to see transient flag
	// The payload is after TOC byte
	if len(libPacket) > 1 {
		payload := libPacket[1:]
		t.Logf("Libopus first payload byte: 0x%02X (%08b)", payload[0], payload[0])
		t.Logf("  Bits 7-6: %d%d (silence flag area)", (payload[0]>>7)&1, (payload[0]>>6)&1)
	}
}
