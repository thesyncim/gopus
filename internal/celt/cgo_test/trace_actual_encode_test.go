package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestCompareMDCTCoeffs compares MDCT coefficients between gopus and libopus
func TestCompareMDCTCoeffs(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	shortBlocks := 8 // transient mode

	// Generate 440Hz sine wave
	pcm64 := make([]float64, frameSize)
	pcm32 := make([]float32, frameSize)
	for i := range pcm64 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm64[i] = val
		pcm32[i] = float32(val)
	}

	// Create gopus encoder and apply preprocessing
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()

	// Apply preprocessing (without delay compensation for direct comparison)
	preemph64 := goEnc.ApplyPreemphasisWithScaling(pcm64)

	// Convert to float32 for both MDCT implementations
	preemph32 := make([]float32, len(preemph64))
	for i, v := range preemph64 {
		preemph32[i] = float32(v)
	}

	// Create input buffer with overlap (zeros for first frame)
	overlap := celt.Overlap
	input32 := make([]float32, frameSize+overlap)
	copy(input32[overlap:], preemph32)

	// Gopus MDCT - short blocks
	historyBuf := make([]float64, overlap)
	goMDCT := celt.ComputeMDCTWithHistory(preemph64, historyBuf, shortBlocks)

	// Libopus MDCT - short blocks (shift=3 for 8 short blocks in 960-sample frame)
	libMode := GetCELTMode48000_960()

	// For short blocks, we need to process each sub-block separately
	// shift=3 means 1920 >> 3 = 240 MDCT size (120 coeffs per block, 8 blocks = 960 coeffs)
	subBlockSize := frameSize / shortBlocks // 120
	libMDCT := make([]float32, frameSize)

	for b := 0; b < shortBlocks; b++ {
		// Input for this sub-block (with overlap from previous sub-block)
		subStart := b * subBlockSize
		subInput := make([]float32, subBlockSize+overlap)

		// For first sub-block of first frame, use zeros for overlap
		if b == 0 {
			// Overlap is zeros
			copy(subInput[overlap:], input32[overlap:overlap+subBlockSize])
		} else {
			// Use samples from previous sub-block for overlap
			copy(subInput[:overlap], input32[subStart:subStart+overlap])
			copy(subInput[overlap:], input32[subStart+overlap:subStart+overlap+subBlockSize])
		}

		// Run libopus MDCT with shift=3 (for 120-sample blocks)
		subOutput := libMode.MDCTForward(subInput, 3)

		// Interleave into output (matching CELT interleaved format)
		for i := 0; i < len(subOutput) && i < subBlockSize; i++ {
			outIdx := b + i*shortBlocks
			if outIdx < frameSize {
				libMDCT[outIdx] = subOutput[i]
			}
		}
	}

	// Compare coefficients for bands 17-20
	t.Log("=== MDCT Coefficient Comparison (Bands 17-20) ===")
	for band := 17; band <= 20; band++ {
		start := celt.ScaledBandStart(band, frameSize)
		end := celt.ScaledBandEnd(band, frameSize)
		if end > frameSize {
			end = frameSize
		}

		var goSum, libSum float64
		maxDiff := 0.0
		for i := start; i < end; i++ {
			goSum += goMDCT[i] * goMDCT[i]
			libSum += float64(libMDCT[i]) * float64(libMDCT[i])
			diff := math.Abs(goMDCT[i] - float64(libMDCT[i]))
			if diff > maxDiff {
				maxDiff = diff
			}
		}

		goRMS := math.Sqrt(goSum)
		libRMS := math.Sqrt(libSum)
		ratio := goRMS / libRMS
		if math.IsNaN(ratio) || math.IsInf(ratio, 0) {
			ratio = 0
		}

		t.Logf("Band %d [%d:%d]: goRMS=%.6f libRMS=%.6f ratio=%.4f maxDiff=%.6f",
			band, start, end, goRMS, libRMS, ratio, maxDiff)
	}
}

// TestCompareBandEnergiesWithPreprocessing compares the band energy computation between gopus and libopus
func TestCompareBandEnergiesWithPreprocessing(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	shortBlocks := 8 // transient mode

	// Generate 440Hz sine wave
	pcm64 := make([]float64, frameSize)
	for i := range pcm64 {
		ti := float64(i) / float64(sampleRate)
		pcm64[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands

	// Create TWO encoders - one for manual computation, one for actual encoding
	// This ensures we don't corrupt internal state between the two operations

	// Encoder 1: for manual energy computation
	goEnc1 := celt.NewEncoder(1)
	goEnc1.Reset()
	goEnc1.SetBitrate(64000)
	goEnc1.SetComplexity(10)
	goEnc1.SetVBR(false)

	// Apply DC rejection
	dcRejected := goEnc1.ApplyDCReject(pcm64)

	// Delay buffer (zeros for first frame)
	delayComp := celt.DelayCompensation
	combinedBuf := make([]float64, delayComp+len(dcRejected))
	copy(combinedBuf[delayComp:], dcRejected)
	samplesForFrame := combinedBuf[:frameSize]

	// Pre-emphasis
	preemph := goEnc1.ApplyPreemphasisWithScaling(samplesForFrame)

	// MDCT with short blocks
	overlap := celt.Overlap
	historyBuf := make([]float64, overlap)
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, historyBuf, shortBlocks)

	// Compute band energies
	goEnergies := goEnc1.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	// Round to float32 like the encoder does
	for i := range goEnergies {
		goEnergies[i] = float64(float32(goEnergies[i]))
	}

	// Libopus: encode and decode the first packet to get the coarse energies
	libEnc, err := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("libopus encoder creation failed: %v", err)
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(64000)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(false)

	pcm32 := make([]float32, frameSize)
	for i, v := range pcm64 {
		pcm32[i] = float32(v)
	}

	libPacket, _ := libEnc.EncodeFloat(pcm32, frameSize)
	libPayload := libPacket[1:] // Skip TOC

	// Decode the libopus packet to get its coarse energies
	rd := &rangecoding.Decoder{}
	rd.Init(libPayload)

	// Skip header flags
	rd.DecodeBit(15) // silence
	rd.DecodeBit(1)  // postfilter
	rd.DecodeBit(3)  // transient
	intra := rd.DecodeBit(3)

	// Decode coarse energies
	lm := mode.LM
	goDec := celt.NewDecoder(1)
	libCoarse := goDec.DecodeCoarseEnergyWithDecoder(rd, nbBands, intra == 1, lm)

	// Encoder 2: for actual gopus encoding (fresh state)
	goEnc2 := celt.NewEncoder(1)
	goEnc2.Reset()
	goEnc2.SetBitrate(64000)
	goEnc2.SetComplexity(10)
	goEnc2.SetVBR(false)

	goPacket, _ := goEnc2.EncodeFrame(pcm64, frameSize)

	rd2 := &rangecoding.Decoder{}
	rd2.Init(goPacket)
	rd2.DecodeBit(15)
	rd2.DecodeBit(1)
	rd2.DecodeBit(3)
	intra2 := rd2.DecodeBit(3)
	goDec2 := celt.NewDecoder(1)
	goCoarse := goDec2.DecodeCoarseEnergyWithDecoder(rd2, nbBands, intra2 == 1, lm)

	t.Log("=== Band Energy Comparison ===")
	t.Logf("Bands 0-20 | Computed | Lib Coarse | Go Coarse | Raw-Lib | Raw-Go")
	for band := 0; band < nbBands; band++ {
		diffLib := goEnergies[band] - libCoarse[band]
		diffGo := goEnergies[band] - goCoarse[band]
		t.Logf("Band %2d: computed=%+.4f lib_coarse=%+.4f go_coarse=%+.4f diff_lib=%+.4f diff_go=%+.4f",
			band, goEnergies[band], libCoarse[band], goCoarse[band], diffLib, diffGo)
	}

	// Check if libopus and gopus coarse energies match
	t.Log("")
	t.Log("=== Coarse Energy Match ===")
	coarseMatch := true
	for band := 0; band < nbBands; band++ {
		if math.Abs(libCoarse[band]-goCoarse[band]) > 0.001 {
			t.Logf("MISMATCH Band %d: lib=%+.4f go=%+.4f", band, libCoarse[band], goCoarse[band])
			coarseMatch = false
		}
	}
	if coarseMatch {
		t.Log("All coarse energies match!")
	}

	// The key question: does gopus raw energy match what libopus is using?
	// We can infer this from the fine quant values
	t.Log("")
	t.Log("=== Fine Energy Residual Analysis ===")
	t.Log("If gopus raw = libopus raw, both should produce same fine quant")
	t.Log("Band 17 residual for gopus: computed - go_coarse = fine")
	t.Logf("Band 17: computed=%+.4f - go_coarse=%+.4f = %+.4f",
		goEnergies[17], goCoarse[17], goEnergies[17]-goCoarse[17])
}

// TestDebugActualEncoderEnergies uses the debug field to see actual encoder energies
func TestDebugActualEncoderEnergies(t *testing.T) {
	t.Skip("Debug test disabled - DebugLastEnergies method removed")
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

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	// Gopus: encode and get the debug energies
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(64000)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	goPacket, err := goEnc.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Fatalf("gopus EncodeFrame failed: %v", err)
	}

	// Debug energy logging removed - DebugLastEnergies method no longer exists
	t.Log("=== Encoder energies debug removed ===")

	// Now compute what manual test gets
	goEnc2 := celt.NewEncoder(1)
	goEnc2.Reset()
	goEnc2.SetBitrate(64000)
	goEnc2.SetComplexity(10)
	goEnc2.SetVBR(false)

	dcRejected := goEnc2.ApplyDCReject(pcm64)
	delayComp := celt.DelayCompensation
	combinedBuf := make([]float64, delayComp+len(dcRejected))
	copy(combinedBuf[delayComp:], dcRejected)
	samplesForFrame := combinedBuf[:frameSize]
	preemph := goEnc2.ApplyPreemphasisWithScaling(samplesForFrame)

	overlap := celt.Overlap
	shortBlocks := mode.ShortBlocks // = 8 for transient mode
	historyBuf := make([]float64, overlap)
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, historyBuf, shortBlocks)

	manualEnergies := goEnc2.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)
	for i := range manualEnergies {
		manualEnergies[i] = float64(float32(manualEnergies[i]))
	}

	t.Log("")
	t.Log("=== Manual Computation Energies ===")
	for band := 14; band <= 20 && band < len(manualEnergies); band++ {
		t.Logf("Band %d: manual_energy=%+.6f", band, manualEnergies[band])
	}

	// Compare section removed - actualEnergies no longer available
	_ = goPacket
	_ = manualEnergies
	_ = lm
	_ = nbBands
}

// TestTraceActualEncodeEnergies traces the exact energy values used during encoding
func TestTraceActualEncodeEnergies(t *testing.T) {
	frameSize := 960
	sampleRate := 48000

	// Generate 440Hz sine wave
	pcm64 := make([]float64, frameSize)
	for i := range pcm64 {
		ti := float64(i) / float64(sampleRate)
		pcm64[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM
	shortBlocks := mode.ShortBlocks

	// Create encoder and exactly replicate what EncodeFrame does
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(64000)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	// Step 3a: DC rejection
	dcRejected := goEnc.ApplyDCReject(pcm64)

	// Step 3b: Delay buffer (for first frame, this is zeros + dcRejected)
	delayComp := celt.DelayCompensation
	combinedBuf := make([]float64, delayComp+len(dcRejected))
	// First delayComp samples are zeros (from empty delay buffer)
	copy(combinedBuf[delayComp:], dcRejected)
	samplesForFrame := combinedBuf[:frameSize]

	// Step 3c: Pre-emphasis
	preemph := goEnc.ApplyPreemphasisWithScaling(samplesForFrame)

	t.Logf("Sample 0 after preemph: %f", preemph[0])
	t.Logf("Sample 100 after preemph: %f", preemph[100])

	// Step 5: MDCT (using encoder's overlap buffer which is zeros for first frame)
	overlap := celt.Overlap
	historyBuf := make([]float64, overlap) // zeros
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, historyBuf, shortBlocks)

	// Step 6: Compute band energies
	energies := goEnc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)
	// Round to float32 like the encoder does
	for i := range energies {
		energies[i] = float64(float32(energies[i]))
	}

	t.Log("")
	t.Log("=== Computed energies (after float32 rounding) ===")
	for band := 14; band <= 20; band++ {
		t.Logf("Band %d: energy=%+.6f", band, energies[band])
	}

	// Now encode coarse energy to get quantizedEnergies
	targetBytes := 159
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)
	re.Shrink(uint32(targetBytes))
	goEnc.SetRangeEncoder(re)

	// Encode header flags - match actual encoder behavior
	re.EncodeBit(0, 15) // silence=0
	re.EncodeBit(0, 1)  // postfilter=0
	re.EncodeBit(1, 3)  // transient=1
	re.EncodeBit(0, 3)  // intra=0 (gopus uses intra=false always)

	// Encode coarse energy with intra=false (matching actual encoder)
	quantizedEnergies := goEnc.EncodeCoarseEnergy(energies, nbBands, false, lm)

	t.Log("")
	t.Log("=== Quantized coarse energies ===")
	for band := 14; band <= 20; band++ {
		t.Logf("Band %d: coarse=%+.6f", band, quantizedEnergies[band])
	}

	t.Log("")
	t.Log("=== Fine energy residuals ===")
	for band := 14; band <= 20; band++ {
		residual := energies[band] - quantizedEnergies[band]
		t.Logf("Band %d: energy=%+.6f - coarse=%+.6f = residual=%+.6f",
			band, energies[band], quantizedEnergies[band], residual)
	}

	// Now compute what fine_q would be for band 17
	t.Log("")
	t.Log("=== Fine quant calculation for band 17 ===")
	band := 17
	fineBits := 3 // from earlier trace
	scale := float64(int(1) << fineBits)
	fine := energies[band] - quantizedEnergies[band]
	DB6 := 1.0
	q := int(math.Floor((fine/DB6+0.5)*scale + 1e-9))
	if q < 0 {
		q = 0
	}
	if q >= int(scale) {
		q = int(scale) - 1
	}
	t.Logf("fine=%+.6f, (fine + 0.5)*scale = %+.6f, q=%d", fine, (fine+0.5)*scale, q)
}
