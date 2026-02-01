//go:build cgo_libopus
// +build cgo_libopus

// Package cgo compares band energies between gopus and libopus.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/rangecoding"
)

// TestCompareBandEnergies compares the band energies computed by gopus vs what libopus would compute.
func TestCompareBandEnergies(t *testing.T) {
	frameSize := 960
	sampleRate := 48000

	// Generate 440Hz sine wave
	pcm64 := make([]float64, frameSize)
	pcm32 := make([]float32, frameSize)
	for i := range pcm64 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm64[i] = val
		pcm32[i] = float32(val)
	}

	t.Log("=== Band Energy Comparison ===")
	t.Log("")

	// Compute gopus band energies
	enc := celt.NewEncoder(1)
	enc.SetBitrate(64000)

	preemph := enc.ApplyPreemphasisWithScaling(pcm64)
	history := make([]float64, celt.Overlap)
	mdct := celt.ComputeMDCTWithHistory(preemph, history, 1)

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands

	gopusEnergies := enc.ComputeBandEnergies(mdct, nbBands, frameSize)

	t.Logf("Gopus band energies (first 10 bands):")
	for i := 0; i < 10 && i < len(gopusEnergies); i++ {
		t.Logf("  Band %2d: %.4f", i, gopusEnergies[i])
	}
	t.Log("")

	// Compute qi values that gopus would encode
	t.Log("Computing qi values gopus would encode (intra=false, lm=3):")

	// For inter mode with intra=false, the prediction is:
	// f = x - coef*oldE - prev
	// qi = round(f/DB6)

	// Alpha coefficient for LM=3, inter mode
	alphaCoef := []float64{0.75, 0.822727, 0.857143, 0.875}
	betaCoefInter := []float64{0.039062, 0.070313, 0.101563, 0.132813}
	lm := 3
	coef := alphaCoef[lm]
	beta := betaCoefInter[lm]
	DB6 := 1.0

	prevBandEnergy := 0.0
	prevEnergy := make([]float64, nbBands)

	for band := 0; band < 10 && band < nbBands; band++ {
		x := gopusEnergies[band]
		oldE := prevEnergy[band]
		minEnergy := -9.0 * DB6
		if oldE < minEnergy {
			oldE = minEnergy
		}

		f := x - coef*oldE - prevBandEnergy
		qi := int(math.Floor(f/DB6 + 0.5))

		t.Logf("  Band %2d: x=%.4f, oldE=%.4f, prev=%.4f, f=%.4f, qi=%d",
			band, x, oldE, prevBandEnergy, f, qi)

		// Update predictor
		q := float64(qi) * DB6
		prevBandEnergy = prevBandEnergy + q - beta*q
	}

	t.Log("")
	t.Log("Now computing for intra=true mode:")

	// For intra mode:
	// coef = 0 (no prediction from previous frame)
	// beta = 0.149902
	coef = 0.0
	beta = 0.149902
	prevBandEnergy = 0.0

	for band := 0; band < 10 && band < nbBands; band++ {
		x := gopusEnergies[band]
		oldE := 0.0 // Not used in intra mode since coef=0
		minEnergy := -9.0 * DB6
		if oldE < minEnergy {
			oldE = minEnergy
		}

		f := x - coef*oldE - prevBandEnergy
		qi := int(math.Floor(f/DB6 + 0.5))

		t.Logf("  Band %2d: x=%.4f, prev=%.4f, f=%.4f, qi=%d",
			band, x, prevBandEnergy, f, qi)

		// Update predictor
		q := float64(qi) * DB6
		prevBandEnergy = prevBandEnergy + q - beta*q
	}

	// Compare with actual encoded packet
	t.Log("")
	t.Log("=== Actual Encoded qi Values ===")

	packet, err := enc.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	// Decode header
	rd := &rangecoding.Decoder{}
	rd.Init(packet)
	silence := rd.DecodeBit(15)
	if silence == 1 {
		t.Log("Silence frame")
		return
	}
	rd.DecodeBit(1) // postfilter
	transient := 0
	if mode.LM > 0 {
		transient = rd.DecodeBit(3)
	}
	intra := rd.DecodeBit(3)
	t.Logf("Encoded: transient=%d, intra=%d", transient, intra)

	// Decode coarse energy
	dec := celt.NewDecoder(1)
	dec.SetRangeDecoder(rd)
	coarseEnergies := dec.DecodeCoarseEnergy(nbBands, intra == 1, mode.LM)

	t.Logf("Decoded coarse energies (first 10 bands):")
	for i := 0; i < 10 && i < len(coarseEnergies); i++ {
		diff := gopusEnergies[i] - coarseEnergies[i]
		t.Logf("  Band %2d: decoded=%.4f, original=%.4f, diff=%.4f (qi≈%.0f)",
			i, coarseEnergies[i], gopusEnergies[i], diff, diff/DB6)
	}
}
