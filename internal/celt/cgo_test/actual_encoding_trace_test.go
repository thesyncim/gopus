// Package cgo traces actual encoding to find divergence between gopus and libopus.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestActualEncodingDivergence traces actual encoding to identify divergence.
func TestActualEncodingDivergence(t *testing.T) {
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

	t.Log("=== Actual Encoding Divergence Trace ===")
	t.Log("")

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM
	shortBlocks := mode.ShortBlocks

	t.Logf("Frame: %d samples, LM=%d, shortBlocks=%d", frameSize, lm, shortBlocks)
	t.Log("")

	// Show correct prediction coefficients
	t.Log("=== Correct Prediction Coefficients ===")
	t.Logf("AlphaCoef[%d] = %.6f (from celt.AlphaCoef)", lm, celt.AlphaCoef[lm])
	t.Logf("BetaCoefInter[%d] = %.6f (from celt.BetaCoefInter)", lm, celt.BetaCoefInter[lm])
	t.Logf("BetaIntra = %.6f (from celt.BetaIntra)", celt.BetaIntra)
	t.Log("")

	// Create gopus encoder
	enc := celt.NewEncoder(1)
	enc.Reset()
	enc.SetBitrate(bitrate)

	// Step 1: Pre-emphasis
	preemph := enc.ApplyPreemphasisWithScaling(pcm64)
	t.Logf("Pre-emphasis first 5 samples: %.2f, %.2f, %.2f, %.2f, %.2f",
		preemph[0], preemph[1], preemph[2], preemph[3], preemph[4])

	// Step 2: MDCT with short blocks (transient mode)
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, enc.OverlapBuffer(), shortBlocks)
	t.Logf("MDCT: %d coefficients", len(mdctCoeffs))

	// Step 3: Band energies
	energies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	t.Log("")
	t.Log("=== Gopus Band Energies ===")
	for i := 0; i < 10; i++ {
		t.Logf("  Band %d: %.4f", i, energies[i])
	}

	// Step 4: Now encode coarse energy using actual EncodeCoarseEnergy
	t.Log("")
	t.Log("=== QI Values from Actual EncodeCoarseEnergy ===")

	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	// Need to set the range encoder on the encoder
	enc.SetRangeEncoder(re)

	// Encode with intra=false (inter mode) - first frame should use inter mode
	intra := false
	quantized := enc.EncodeCoarseEnergy(energies, nbBands, intra, lm)

	// Compute what QI values were encoded
	t.Log("Reconstructed QI values:")
	t.Log("Band | Energy  | Quantized | QI (approx)")
	t.Log("-----+---------+-----------+------------")

	// The quantized energy is: q = round(f) where f = x - coef*oldE - prev
	// And the reconstructed energy is: oldE*coef + prev + qi*DB6
	// For first frame with intra=false: oldE=0, so qi ≈ round((x - prev) / DB6)

	coef := celt.AlphaCoef[lm]
	beta := celt.BetaCoefInter[lm]
	DB6 := 1.0
	prev := 0.0

	for band := 0; band < 10; band++ {
		x := energies[band]
		oldE := 0.0 // First frame
		if oldE < -9.0*DB6 {
			oldE = -9.0 * DB6
		}

		f := x - coef*oldE - prev
		qi := int(math.Floor(f/DB6 + 0.5))

		t.Logf("%4d | %7.4f | %9.4f | %d",
			band, x, quantized[band], qi)

		// Update prev for next band
		q := float64(qi) * DB6
		prev = prev + q - beta*q
	}

	// Now let's encode with libopus and compare
	t.Log("")
	t.Log("=== Libopus Encoding ===")

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

	libPayload := libPacket[1:] // Skip TOC
	t.Logf("Libopus packet: %d bytes", len(libPacket))

	// Decode libopus header
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

	// Decode libopus qi values
	probModel := celt.GetEProbModel()
	prob := probModel[lm][0]
	if libIntra == 1 {
		prob = probModel[lm][1]
	}

	libDec := celt.NewDecoder(1)
	libDec.SetRangeDecoder(libRd)

	t.Log("")
	t.Log("=== Libopus QI Values (decoded from packet) ===")
	t.Log("Band | Libopus QI")
	t.Log("-----+-----------")

	libQIs := make([]int, nbBands)
	for band := 0; band < nbBands; band++ {
		pi := 2 * band
		if pi > 40 {
			pi = 40
		}
		fs := int(prob[pi]) << 7
		decay := int(prob[pi+1]) << 6
		libQIs[band] = libDec.DecodeLaplaceTest(fs, decay)
	}

	for band := 0; band < 10; band++ {
		t.Logf("%4d | %10d", band, libQIs[band])
	}

	// Now encode a full gopus packet and decode its QI values
	t.Log("")
	t.Log("=== Gopus Full Encode ===")

	enc2 := celt.NewEncoder(1)
	enc2.Reset()
	enc2.SetBitrate(bitrate)

	gopusPacket, err := enc2.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	t.Logf("Gopus packet: %d bytes", len(gopusPacket))

	// Decode gopus header
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

	// Decode gopus qi values
	gopusProb := probModel[lm][0]
	if gopusIntra == 1 {
		gopusProb = probModel[lm][1]
	}

	gopusDec := celt.NewDecoder(1)
	gopusDec.SetRangeDecoder(gopusRd)

	t.Log("")
	t.Log("=== QI Comparison ===")
	t.Log("Band | Gopus QI | Libopus QI | Diff | Match")
	t.Log("-----+----------+------------+------+------")

	gopusQIs := make([]int, nbBands)
	totalDiff := 0
	mismatch := 0
	for band := 0; band < nbBands; band++ {
		pi := 2 * band
		if pi > 40 {
			pi = 40
		}
		fs := int(gopusProb[pi]) << 7
		decay := int(gopusProb[pi+1]) << 6
		gopusQIs[band] = gopusDec.DecodeLaplaceTest(fs, decay)
	}

	for band := 0; band < nbBands; band++ {
		diff := gopusQIs[band] - libQIs[band]
		absDiff := diff
		if absDiff < 0 {
			absDiff = -absDiff
		}
		totalDiff += absDiff

		match := "YES"
		if diff != 0 {
			match = "NO"
			mismatch++
		}

		if band < 12 {
			t.Logf("%4d | %8d | %10d | %4d | %s", band, gopusQIs[band], libQIs[band], diff, match)
		}
	}

	t.Log("")
	t.Logf("Mismatch count: %d/%d bands", mismatch, nbBands)
	t.Logf("Total absolute QI difference: %d", totalDiff)
}
