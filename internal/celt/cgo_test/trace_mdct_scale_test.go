// Package cgo compares MDCT scale between gopus and libopus
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTraceMDCTScale compares MDCT coefficient scale.
func TestTraceMDCTScale(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm32 := make([]float32, frameSize)
	pcm64 := make([]float64, frameSize)
	for i := range pcm32 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm32[i] = float32(val)
		pcm64[i] = val
	}

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM
	shortBlocks := mode.ShortBlocks

	// Gopus pipeline
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	// Apply pre-emphasis with scaling
	gopusPreemph := goEnc.ApplyPreemphasisWithScaling(pcm64)

	// Compute MDCT
	gopusMDCT := celt.ComputeMDCTWithHistory(gopusPreemph, goEnc.OverlapBuffer(), shortBlocks)

	// Compute band energies
	gopusLinearE := celt.ComputeLinearBandAmplitudes(gopusMDCT, nbBands, frameSize)

	t.Log("=== Gopus MDCT ===")
	t.Logf("Pre-emphasis sample 0: %.10f", gopusPreemph[0])
	t.Logf("Pre-emphasis sample 100: %.10f", gopusPreemph[100])
	t.Logf("MDCT coeff 0: %.10f", gopusMDCT[0])
	t.Logf("MDCT coeff 1: %.10f", gopusMDCT[1])
	t.Logf("Band 0 linear amplitude: %.10f", gopusLinearE[0])
	t.Logf("Band 1 linear amplitude: %.10f", gopusLinearE[1])

	// Now get libopus values
	// Apply libopus pre-emphasis
	libPreemph := ApplyLibopusPreemphasis(pcm32, 0.8500061035)

	// Convert to float64 for MDCT
	libPreemphF64 := make([]float64, frameSize)
	for i, v := range libPreemph {
		libPreemphF64[i] = float64(v)
	}

	// Compute MDCT using gopus MDCT function on libopus pre-emphasized data
	libMDCT := celt.ComputeMDCTWithHistory(libPreemphF64, make([]float64, celt.Overlap), shortBlocks)

	// Compute linear band energies using libopus method
	libMDCTF32 := make([]float32, len(libMDCT))
	for i, v := range libMDCT {
		libMDCTF32[i] = float32(v)
	}
	libLinearE := ComputeLibopusBandEnergyLinear(libMDCTF32, nbBands, frameSize, lm)

	t.Log("")
	t.Log("=== Libopus Pre-emphasis (gopus MDCT) ===")
	t.Logf("Pre-emphasis sample 0: %.10f", libPreemph[0])
	t.Logf("Pre-emphasis sample 100: %.10f", libPreemph[100])
	t.Logf("MDCT coeff 0: %.10f", libMDCT[0])
	t.Logf("MDCT coeff 1: %.10f", libMDCT[1])
	t.Logf("Band 0 linear amplitude: %.10f", libLinearE[0])
	t.Logf("Band 1 linear amplitude: %.10f", libLinearE[1])

	// Compare
	t.Log("")
	t.Log("=== Comparison ===")
	t.Logf("Pre-emphasis ratio (gopus/lib): %.6f", gopusPreemph[100]/libPreemphF64[100])
	if len(gopusMDCT) > 0 && len(libMDCT) > 0 && libMDCT[0] != 0 {
		t.Logf("MDCT coeff 0 ratio: %.6f", gopusMDCT[0]/libMDCT[0])
	}
	if gopusLinearE[0] > 0 && libLinearE[0] > 0 {
		t.Logf("Band 0 linear amp ratio: %.6f", gopusLinearE[0]/float64(libLinearE[0]))
	}

	// The key insight: what does libopus encode as coarse energy vs what gopus computes?
	t.Log("")
	t.Log("=== Energy Scale Analysis ===")
	// Gopus mean-relative log2 energy
	gopusEnergies := goEnc.ComputeBandEnergies(gopusMDCT, nbBands, frameSize)
	t.Logf("Gopus band 0 energy (log2, mean-relative): %.6f", gopusEnergies[0])

	// Libopus mean-relative energy from decoded packet
	libEnc, err := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("libopus encoder creation failed: %v", err)
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(false)

	libPacket, _ := libEnc.EncodeFloat(pcm32, frameSize)
	libPayload := libPacket[1:]

	// Decode libopus packet to get coarse energy
	rdLib := &rangecoding.Decoder{}
	rdLib.Init(libPayload)
	rdLib.DecodeBit(15) // silence
	rdLib.DecodeBit(1)  // postfilter
	rdLib.DecodeBit(3)  // transient
	intra := rdLib.DecodeBit(3)

	goDecLib := celt.NewDecoder(1)
	coarseLib := goDecLib.DecodeCoarseEnergyWithDecoder(rdLib, nbBands, intra == 1, lm)
	t.Logf("Libopus band 0 coarse energy (decoded): %.6f", coarseLib[0])

	// The difference should tell us the scale factor
	energyDiff := gopusEnergies[0] - coarseLib[0]
	scaleFactor := math.Exp2(energyDiff)
	t.Logf("Energy difference (gopus - lib): %.6f dB*6", energyDiff)
	t.Logf("Implied scale factor: %.6f (should be ~1.0 if MDCT scale matches)", scaleFactor)

	// Check eMeans to ensure they're the same
	eMeans := celt.GetEMeans()
	libEMean := GetLibopusEMeans(0)
	t.Logf("Gopus eMeans[0]: %.6f", eMeans[0])
	t.Logf("Libopus eMeans[0]: %.6f", libEMean)
}
