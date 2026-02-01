//go:build cgo_libopus
// +build cgo_libopus

package cgo

import (
	"math"
	"testing"
)

// TestTransientAlgorithmStepByStep traces through the transient algorithm manually
func TestTransientAlgorithmStepByStep(t *testing.T) {
	sampleRate := 48000
	frameSize := 960
	overlap := 120
	
	// Generate 440Hz sine wave
	pcm := make([]float64, frameSize)
	for i := range pcm {
		pcm[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate))
	}
	
	// Build input like gopus does for Frame 0:
	// 1. DC rejection (assume pass-through for simplicity)
	// 2. Delay buffer of 192 zeros prepended
	// 3. Take first 960 samples
	// 4. Pre-emphasis with scaling
	
	delayComp := 192
	combinedBuf := make([]float64, delayComp + frameSize)
	// First delayComp samples are zeros (fresh encoder)
	copy(combinedBuf[delayComp:], pcm)
	
	samplesForFrame := combinedBuf[:frameSize] // First 960 samples
	
	// Pre-emphasis: y[n] = x[n]*32768 - 0.85*state, state = x[n]*32768
	preemphCoef := 0.85000610
	preemph := make([]float64, frameSize)
	var state float64 = 0 // Fresh encoder
	for i := 0; i < frameSize; i++ {
		scaled := samplesForFrame[i] * 32768.0
		preemph[i] = scaled - preemphCoef*state
		state = scaled
	}
	
	t.Logf("Pre-emphasis input (samplesForFrame):")
	t.Logf("  [0:5]: %.6f, %.6f, %.6f, %.6f, %.6f", samplesForFrame[0], samplesForFrame[1], samplesForFrame[2], samplesForFrame[3], samplesForFrame[4])
	t.Logf("  [192:197]: %.6f, %.6f, %.6f, %.6f, %.6f", samplesForFrame[192], samplesForFrame[193], samplesForFrame[194], samplesForFrame[195], samplesForFrame[196])
	
	t.Logf("Pre-emphasis output:")
	t.Logf("  [0:5]: %.1f, %.1f, %.1f, %.1f, %.1f", preemph[0], preemph[1], preemph[2], preemph[3], preemph[4])
	t.Logf("  [192:197]: %.1f, %.1f, %.1f, %.1f, %.1f", preemph[192], preemph[193], preemph[194], preemph[195], preemph[196])
	
	// Build transient input: [overlap zeros] + [pre-emphasized frame]
	transientLen := overlap + frameSize
	transientInput := make([]float64, transientLen)
	// First overlap samples are zeros (fresh encoder's preemphBuffer)
	copy(transientInput[overlap:], preemph)
	
	t.Logf("\nTransient input:")
	t.Logf("  Length: %d", transientLen)
	t.Logf("  [0:5]: %.1f, %.1f, %.1f, %.1f, %.1f", transientInput[0], transientInput[1], transientInput[2], transientInput[3], transientInput[4])
	t.Logf("  [115:125]: ...")
	for i := 115; i <= 125; i++ {
		t.Logf("    [%d]: %.1f", i, transientInput[i])
	}
	t.Logf("  [312:317]: %.1f, %.1f, %.1f, %.1f, %.1f", transientInput[312], transientInput[313], transientInput[314], transientInput[315], transientInput[316])
	
	// Now run the transient analysis algorithm step by step
	// High-pass filter: (1 - 2*z^-1 + z^-2) / (1 - z^-1 + 0.5*z^-2)
	tmp := make([]float64, transientLen)
	var mem0, mem1 float64
	for i := 0; i < transientLen; i++ {
		x := transientInput[i]
		y := mem0 + x
		mem00 := mem0
		mem0 = mem0 - x + 0.5*mem1
		mem1 = x - mem00
		tmp[i] = y * 0.25
	}
	
	// Clear first 12 samples
	for i := 0; i < 12; i++ {
		tmp[i] = 0
	}
	
	t.Logf("\nHigh-pass filtered (tmp):")
	t.Logf("  [0:15]: %.4f, %.4f, ..., %.4f, %.4f", tmp[0], tmp[1], tmp[14], tmp[15])
	t.Logf("  [115:125]: ...")
	for i := 115; i <= 125; i++ {
		t.Logf("    [%d]: %.4f", i, tmp[i])
	}
	t.Logf("  [312:317]: %.4f, %.4f, %.4f, %.4f, %.4f", tmp[312], tmp[313], tmp[314], tmp[315], tmp[316])
	
	// Forward pass
	len2 := transientLen / 2
	energy := make([]float64, len2)
	var mean float64
	forwardDecay := 0.0625
	mem0 = 0
	for i := 0; i < len2; i++ {
		x2 := tmp[2*i]*tmp[2*i] + tmp[2*i+1]*tmp[2*i+1]
		mean += x2
		mem0 = x2 + (1.0-forwardDecay)*mem0
		energy[i] = forwardDecay * mem0
	}
	
	t.Logf("\nForward pass energy (len2=%d):", len2)
	t.Logf("  [0:5]: %.4f, %.4f, %.4f, %.4f, %.4f", energy[0], energy[1], energy[2], energy[3], energy[4])
	t.Logf("  [55:65]: ...")
	for i := 55; i <= 65; i++ {
		t.Logf("    [%d]: %.4f", i, energy[i])
	}
	t.Logf("  [150:160]: ...")
	for i := 150; i <= 160; i++ {
		t.Logf("    [%d]: %.4f", i, energy[i])
	}
	
	// Backward pass
	var maxE float64
	mem0 = 0
	for i := len2 - 1; i >= 0; i-- {
		mem0 = energy[i] + 0.875*mem0
		energy[i] = 0.125 * mem0
		if 0.125*mem0 > maxE {
			maxE = 0.125 * mem0
		}
	}
	
	t.Logf("\nBackward pass energy:")
	t.Logf("  maxE: %.4f", maxE)
	t.Logf("  [55:65]: ...")
	for i := 55; i <= 65; i++ {
		t.Logf("    [%d]: %.4f", i, energy[i])
	}
	
	// Geometric mean
	mean = math.Sqrt(mean * maxE * 0.5 * float64(len2))
	t.Logf("\nGeometric mean: %.4f", mean)
	
	// Norm (inverse of mean)
	epsilon := 1e-15
	norm := (float64(len2) * (1 << 20)) / (mean*0.5 + epsilon)
	t.Logf("Norm: %.4f", norm)
	
	// Inverse table
	invTable := [128]int{
		255,255,156,110, 86, 70, 59, 51, 45, 40, 37, 33, 31, 28, 26, 25,
		23, 22, 21, 20, 19, 18, 17, 16, 16, 15, 15, 14, 13, 13, 12, 12,
		12, 12, 11, 11, 11, 10, 10, 10,  9,  9,  9,  9,  9,  9,  8,  8,
		8,  8,  8,  7,  7,  7,  7,  7,  7,  6,  6,  6,  6,  6,  6,  6,
		6,  6,  6,  6,  6,  6,  6,  6,  6,  5,  5,  5,  5,  5,  5,  5,
		5,  5,  5,  5,  5,  4,  4,  4,  4,  4,  4,  4,  4,  4,  4,  4,
		4,  4,  4,  4,  4,  4,  4,  4,  4,  4,  4,  4,  4,  4,  3,  3,
		3,  3,  3,  3,  3,  3,  3,  3,  3,  3,  3,  3,  3,  3,  3,  2,
	}
	
	// Compute harmonic mean
	var unmask int
	for i := 12; i < len2-5; i += 4 {
		id := int(math.Floor(64 * norm * (energy[i] + epsilon)))
		if id < 0 {
			id = 0
		}
		if id > 127 {
			id = 127
		}
		unmask += invTable[id]
	}
	
	numSamples := (len2 - 17) / 4
	if numSamples < 1 {
		numSamples = 1
	}
	maskMetric := float64(64*unmask*4) / float64(6*numSamples*4)
	
	t.Logf("\nMask metric computation:")
	t.Logf("  unmask: %d", unmask)
	t.Logf("  numSamples: %d", numSamples)
	t.Logf("  maskMetric: %.4f", maskMetric)
	t.Logf("  threshold: 200")
	t.Logf("  isTransient: %v", maskMetric > 200)
	
	// The key issue: with zeros in first part, energy[0:60] are near zero
	// This creates very low energy in that region
	// The energy spike at ~155 (samples 312+) is spread out by the backward pass
	// Result: no sharp transient detected
	
	t.Log("\n=== DIAGNOSIS ===")
	t.Logf("The signal only starts being non-zero at sample %d of transient input", overlap+delayComp)
	t.Logf("That's index %d of the len2=%d energy array", (overlap+delayComp)/2, len2)
	t.Log("The backward pass spreads energy from non-zero region back to zero region")
	t.Log("Result: smooth energy profile instead of sharp transient")
}
