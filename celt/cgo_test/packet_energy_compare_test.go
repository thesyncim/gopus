//go:build cgo_libopus
// +build cgo_libopus

// Package cgo compares the actual band energies encoded in packets.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/rangecoding"
)

// TestPacketEnergiesComparison encodes the same signal with both gopus and libopus,
// then decodes the coarse energy QI values from both packets to compare.
func TestPacketEnergiesComparison(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm64 := make([]float64, frameSize)
	pcm32 := make([]float32, frameSize)
	for i := range pcm64 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm64[i] = val
		pcm32[i] = float32(val)
	}

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	t.Log("=== Packet Energy Comparison ===")
	t.Logf("Frame: %d samples, LM=%d, nbBands=%d", frameSize, lm, nbBands)
	t.Log("")

	// Encode with libopus
	libEnc, err := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("libopus encoder creation failed: %v", err)
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(false)

	libPacket, libLen := libEnc.EncodeFloat(pcm32, frameSize)
	if libLen <= 0 {
		t.Fatalf("libopus encode failed: length=%d", libLen)
	}
	t.Logf("Libopus packet: %d bytes", len(libPacket))

	// Encode with gopus
	gopusEnc := celt.NewEncoder(1)
	gopusEnc.Reset()
	gopusEnc.SetBitrate(bitrate)
	gopusEnc.SetVBR(false)

	gopusPacket, err := gopusEnc.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}
	t.Logf("Gopus packet: %d bytes", len(gopusPacket))

	// Decode libopus header and QI values
	libPayload := libPacket[1:] // Skip TOC
	libRd := &rangecoding.Decoder{}
	libRd.Init(libPayload)

	libSilence := libRd.DecodeBit(15)
	libPostfilter := libRd.DecodeBit(1)
	var libTransient int
	if lm > 0 {
		libTransient = libRd.DecodeBit(3)
	}
	libIntra := libRd.DecodeBit(3)

	t.Logf("Libopus flags: silence=%d, postfilter=%d, transient=%d, intra=%d",
		libSilence, libPostfilter, libTransient, libIntra)

	// Decode gopus header and QI values
	gopusRd := &rangecoding.Decoder{}
	gopusRd.Init(gopusPacket)

	gopusSilence := gopusRd.DecodeBit(15)
	gopusPostfilter := gopusRd.DecodeBit(1)
	var gopusTransient int
	if lm > 0 {
		gopusTransient = gopusRd.DecodeBit(3)
	}
	gopusIntra := gopusRd.DecodeBit(3)

	t.Logf("Gopus flags: silence=%d, postfilter=%d, transient=%d, intra=%d",
		gopusSilence, gopusPostfilter, gopusTransient, gopusIntra)

	// Get probability model
	probModel := celt.GetEProbModel()
	libProb := probModel[lm][0]
	if libIntra == 1 {
		libProb = probModel[lm][1]
	}
	gopusProb := probModel[lm][0]
	if gopusIntra == 1 {
		gopusProb = probModel[lm][1]
	}

	// Decode QI values
	libDec := celt.NewDecoder(1)
	libDec.SetRangeDecoder(libRd)

	gopusDec := celt.NewDecoder(1)
	gopusDec.SetRangeDecoder(gopusRd)

	t.Log("")
	t.Log("=== QI Value Comparison ===")
	t.Log("Band | Libopus QI | Gopus QI | Diff | Match")
	t.Log("-----+------------+----------+------+------")

	libQIs := make([]int, nbBands)
	gopusQIs := make([]int, nbBands)
	totalDiff := 0
	mismatchCount := 0

	for band := 0; band < nbBands; band++ {
		pi := 2 * band
		if pi > 40 {
			pi = 40
		}
		fs := int(libProb[pi]) << 7
		decay := int(libProb[pi+1]) << 6

		libQIs[band] = libDec.DecodeLaplaceTest(fs, decay)

		fsG := int(gopusProb[pi]) << 7
		decayG := int(gopusProb[pi+1]) << 6
		gopusQIs[band] = gopusDec.DecodeLaplaceTest(fsG, decayG)

		diff := gopusQIs[band] - libQIs[band]
		absDiff := diff
		if absDiff < 0 {
			absDiff = -absDiff
		}
		totalDiff += absDiff

		match := "YES"
		if diff != 0 {
			match = "NO"
			mismatchCount++
		}

		t.Logf("%4d | %10d | %8d | %4d | %s", band, libQIs[band], gopusQIs[band], diff, match)
	}

	t.Log("")
	t.Logf("Total QI mismatches: %d/%d bands", mismatchCount, nbBands)
	t.Logf("Total absolute QI difference: %d", totalDiff)

	// Reconstruct energies from QI values
	t.Log("")
	t.Log("=== Reconstructed Band Energies ===")

	coef := celt.AlphaCoef[lm]
	betaLib := celt.BetaCoefInter[lm]
	betaGopus := celt.BetaCoefInter[lm]
	if libIntra == 1 {
		betaLib = celt.BetaIntra
	}
	if gopusIntra == 1 {
		betaGopus = celt.BetaIntra
	}

	t.Log("Band | Lib Energy | Gopus Energy | Diff")
	t.Log("-----+------------+--------------+------")

	prevLib := 0.0
	prevGopus := 0.0
	eMeans := celt.GetEMeans()

	for band := 0; band < nbBands && band < 12; band++ {
		// Reconstruct libopus energy
		oldE := 0.0 // First frame
		if oldE < -9.0 {
			oldE = -9.0
		}
		qLib := float64(libQIs[band])
		libEnergy := coef*oldE + prevLib + qLib
		prevLib = prevLib + qLib - betaLib*qLib

		// Reconstruct gopus energy
		qGopus := float64(gopusQIs[band])
		gopusEnergy := coef*oldE + prevGopus + qGopus
		prevGopus = prevGopus + qGopus - betaGopus*qGopus

		diff := gopusEnergy - libEnergy

		t.Logf("%4d | %10.4f | %12.4f | %5.2f", band, libEnergy, gopusEnergy, diff)
	}

	t.Log("")
	t.Log("=== What energies would produce these QIs? ===")
	t.Log("(Reverse engineering the input band energies)")

	prevLib = 0.0
	prevGopus = 0.0

	t.Log("Band | Lib Implied | Gopus Implied | Diff (6dB units)")
	t.Log("-----+-------------+---------------+-----------------")

	for band := 0; band < nbBands && band < 12; band++ {
		oldE := 0.0
		if oldE < -9.0 {
			oldE = -9.0
		}

		// Implied energy: qi = round((x - coef*oldE - prev) / DB6)
		// So x ≈ qi*DB6 + coef*oldE + prev
		libImplied := float64(libQIs[band]) + coef*oldE + prevLib + eMeans[band]
		gopusImplied := float64(gopusQIs[band]) + coef*oldE + prevGopus + eMeans[band]

		diff := gopusImplied - libImplied

		t.Logf("%4d | %11.4f | %13.4f | %f", band, libImplied, gopusImplied, diff)

		// Update prev
		qLib := float64(libQIs[band])
		qGopus := float64(gopusQIs[band])
		prevLib = prevLib + qLib - betaLib*qLib
		prevGopus = prevGopus + qGopus - betaGopus*qGopus
	}

	// Now let's compute what gopus would compute for band energies
	t.Log("")
	t.Log("=== Gopus Internal Band Energies ===")

	enc2 := celt.NewEncoder(1)
	enc2.Reset()
	enc2.SetBitrate(bitrate)

	preemph := enc2.ApplyPreemphasisWithScaling(pcm64)
	transientResult := enc2.TransientAnalysis(append(make([]float64, celt.Overlap), preemph...), frameSize+celt.Overlap, false)
	shortBlocks := 1
	if transientResult.IsTransient {
		shortBlocks = mode.ShortBlocks
	}
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, enc2.OverlapBuffer(), shortBlocks)
	gopusEnergies := enc2.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	t.Logf("Transient: %v, shortBlocks: %d", transientResult.IsTransient, shortBlocks)
	t.Log("")
	t.Log("Band | Gopus Comp | Gopus Enc | Diff")
	t.Log("-----+------------+-----------+------")

	prevGopus = 0.0
	for band := 0; band < nbBands && band < 12; band++ {
		oldE := 0.0
		if oldE < -9.0 {
			oldE = -9.0
		}

		// What energy was encoded
		gopusEncE := float64(gopusQIs[band]) + coef*oldE + prevGopus

		t.Logf("%4d | %10.4f | %9.4f | %5.2f",
			band, gopusEnergies[band], gopusEncE, gopusEnergies[band]-gopusEncE)

		qGopus := float64(gopusQIs[band])
		prevGopus = prevGopus + qGopus - betaGopus*qGopus
	}

	if mismatchCount > 0 {
		t.Log("")
		t.Log("=== Analysis ===")
		t.Logf("The QI values differ between gopus and libopus.")
		t.Logf("Gopus computed energies: first 3 bands = %.4f, %.4f, %.4f",
			gopusEnergies[0], gopusEnergies[1], gopusEnergies[2])
		t.Logf("These should produce QIs matching libopus, but they don't.")
		t.Logf("This suggests libopus computes different band energies during encoding.")
	}
}
