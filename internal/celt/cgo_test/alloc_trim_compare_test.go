// Package cgo compares allocation trim computation between gopus and libopus.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestAllocTrimCompareWithEnc compares allocation trim by encoding.
func TestAllocTrimCompareWithEnc(t *testing.T) {
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

	// Encode with libopus to see what trim it uses
	libEnc, err := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("libopus encoder creation failed: %v", err)
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(false)

	// Encode frame to get allocation trim
	libPacket, libLen := libEnc.EncodeFloat(pcm32, frameSize)
	if libLen <= 0 {
		t.Fatalf("libopus encode failed: length=%d", libLen)
	}

	// Get the trim from libopus packet by decoding
	libTrim := GetAllocTrimFromPacket(libPacket[1:], nbBands, lm)
	t.Logf("libopus allocTrim = %d", libTrim)

	// Encode with gopus using debug mode
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)

	goPacket, debugInfo, err := goEnc.EncodeFrameWithDebug(pcm64, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	// Log debug info
	if debugInfo != nil {
		t.Logf("gopus internal state:")
		t.Logf("  tfEstimate = %.6f", debugInfo.TfEstimate)
		t.Logf("  equivRate = %d", debugInfo.EquivRate)
		t.Logf("  effectiveBytes = %d", debugInfo.EffectiveBytes)
		t.Logf("  targetBits = %d", debugInfo.TargetBits)
		t.Logf("  allocTrim (computed) = %d", debugInfo.AllocTrim)
	}

	// Get the trim from gopus packet with debug
	t.Log("--- Decoding gopus packet ---")
	goTrim := GetAllocTrimFromPacketDebug(goPacket, nbBands, lm, t)
	t.Logf("gopus allocTrim (decoded from packet) = %d", goTrim)

	// Also decode libopus packet for comparison
	t.Log("--- Decoding libopus packet ---")
	libTrimCheck := GetAllocTrimFromPacketDebug(libPacket[1:], nbBands, lm, t)
	t.Logf("libopus allocTrim (decoded again) = %d", libTrimCheck)

	if goTrim != libTrim {
		t.Errorf("Allocation trim mismatch: gopus=%d, libopus=%d", goTrim, libTrim)
	}
}

// TestAllocTrimCompare compares allocation trim computation.
func TestAllocTrimCompare(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm32 := make([]float32, frameSize)
	for i := range pcm32 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm32[i] = float32(val)
	}

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	// Encode with libopus to see what trim it uses
	libEnc, err := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("libopus encoder creation failed: %v", err)
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(false)

	// Encode frame to get allocation trim
	libPacket, libLen := libEnc.EncodeFloat(pcm32, frameSize)
	if libLen <= 0 {
		t.Fatalf("libopus encode failed: length=%d", libLen)
	}

	// Get the trim from libopus packet by decoding
	libTrim := GetAllocTrimFromPacket(libPacket[1:], nbBands, lm)
	t.Logf("libopus allocTrim = %d", libTrim)

	// Now compute what gopus would produce
	// First we need to get the band energies and normalized coefficients
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)

	// Manually compute the steps to get the inputs for AllocTrimAnalysis
	pcm64 := make([]float64, frameSize)
	for i, v := range pcm32 {
		pcm64[i] = float64(v)
	}

	// Pre-emphasis
	preemph := goEnc.ApplyPreemphasisWithScaling(pcm64)

	// MDCT
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, make([]float64, 120), 1)

	// Band energies
	energies := goEnc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	// Normalize
	normL := goEnc.NormalizeBandsToArray(mdctCoeffs, energies, nbBands, frameSize)

	// Compute equiv rate
	effectiveBytes := bitrate * frameSize / sampleRate / 8
	equivRate := celt.ComputeEquivRate(effectiveBytes, 1, lm, bitrate)

	// We need tf_estimate from transient analysis
	// For now, use a default value of 0.2 (typical for non-transient)
	tfEstimate := 0.2

	// Compute allocation trim
	goTrim := celt.AllocTrimAnalysis(
		normL,
		energies,
		nbBands,
		lm,
		1, // mono
		nil,
		nbBands, // intensity (all bands)
		tfEstimate,
		equivRate,
		0.0, // surroundTrim
		0.0, // tonalitySlope
	)

	t.Logf("gopus allocTrim = %d", goTrim)
	t.Logf("equivRate = %d", equivRate)
	t.Logf("effectiveBytes = %d", effectiveBytes)
	t.Logf("tfEstimate = %.4f", tfEstimate)

	// Show band energies
	t.Log("Band energies (first 10):")
	for i := 0; i < 10 && i < nbBands; i++ {
		t.Logf("  band %d: %.4f", i, energies[i])
	}

	// Compute spectral tilt to debug
	var diff float64
	end := nbBands
	if end > len(energies) {
		end = len(energies)
	}
	for i := 0; i < end-1; i++ {
		weight := float64(2 + 2*i - end)
		diff += energies[i] * weight
	}
	diff /= float64(end - 1)
	t.Logf("Spectral tilt diff = %.4f", diff)

	tiltAdjust := (diff + 1.0) / 6.0
	if tiltAdjust < -2.0 {
		tiltAdjust = -2.0
	}
	if tiltAdjust > 2.0 {
		tiltAdjust = 2.0
	}
	t.Logf("tiltAdjust = %.4f", tiltAdjust)

	// What would the trim be without tilt adjustment?
	baseTrim := 5.0 // For 64kbps
	tfAdjust := 2.0 * tfEstimate
	t.Logf("Base trim = %.4f", baseTrim)
	t.Logf("TF adjust = %.4f", tfAdjust)
	t.Logf("Expected trim = %.4f = %d", baseTrim-tiltAdjust-tfAdjust, int(math.Floor(0.5+baseTrim-tiltAdjust-tfAdjust)))

	if goTrim != libTrim {
		t.Errorf("Allocation trim mismatch: gopus=%d, libopus=%d", goTrim, libTrim)
	}
}

// TestAllocTrimDebugEncoder traces the exact values used by the encoder
func TestAllocTrimDebugEncoder(t *testing.T) {
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

	// Encode with libopus to get baseline
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
	libTrim := GetAllocTrimFromPacket(libPacket[1:], nbBands, lm)
	t.Logf("libopus allocTrim = %d", libTrim)

	// Now trace through the encoder step by step
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)

	// Step 1: DC rejection (apply what encode_frame does)
	dcRejected := goEnc.ApplyDCReject(pcm64)

	// Step 2: Apply delay buffer
	delayComp := celt.DelayCompensation
	combinedLen := delayComp + len(dcRejected)
	combinedBuf := make([]float64, combinedLen)
	// First frame: delay buffer is zero-initialized
	copy(combinedBuf[delayComp:], dcRejected)
	samplesForFrame := combinedBuf[:frameSize]

	// Step 3: Pre-emphasis with scaling
	preemph := goEnc.ApplyPreemphasisWithScaling(samplesForFrame)

	// Step 4: Transient analysis
	overlap := celt.Overlap
	transientInput := make([]float64, (overlap + frameSize))
	// For first frame, preemphBuffer is zeros, so we just copy preemph
	copy(transientInput[overlap:], preemph)

	transientResult := goEnc.TransientAnalysis(transientInput, frameSize+overlap, false)
	t.Logf("TransientAnalysis: isTransient=%v, tfEstimate=%.6f", transientResult.IsTransient, transientResult.TfEstimate)

	// For frame 0, encoder forces transient=true and tfEstimate=0.2
	transient := transientResult.IsTransient
	tfEstimate := transientResult.TfEstimate
	if lm > 0 {
		transient = true
		tfEstimate = 0.2
		t.Logf("Frame 0 override: transient=true, tfEstimate=0.2")
	}

	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}
	t.Logf("shortBlocks=%d", shortBlocks)

	// Step 5: MDCT with overlap
	historyBuf := make([]float64, overlap)
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, historyBuf, shortBlocks)
	t.Logf("MDCT coeffs length=%d", len(mdctCoeffs))

	// Step 6: Compute band energies
	energies := goEnc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)
	t.Logf("Band energies (first 5):")
	for i := 0; i < 5 && i < len(energies); i++ {
		t.Logf("  band %d: %.6f", i, energies[i])
	}

	// Step 7: Normalize bands
	normL := goEnc.NormalizeBandsToArray(mdctCoeffs, energies, nbBands, frameSize)
	t.Logf("Normalized coeffs length=%d", len(normL))

	// Step 8: Compute equiv rate - this is where the encoder computes it
	// The encoder uses computeTargetBits which includes VBR logic, let's compute both
	targetBitsSimple := bitrate * frameSize / sampleRate // Simple calculation: 1280 bits
	effectiveBytesSimple := targetBitsSimple / 8         // 160 bytes

	// Also compute what VBR produces - we need to know what the encoder actually uses
	// VBR computation involves overhead subtraction and boost factors
	baseBits := bitrate * frameSize / 48000 // 1280 bits
	vbrRateQ3 := baseBits << 3              // 10240 in Q3
	overheadQ3 := (40*1 + 20) << 3          // 60 * 8 = 480 in Q3
	baseTargetQ3 := vbrRateQ3 - overheadQ3  // 10240 - 480 = 9760 in Q3

	// For first frame without VBR boost factors, targetQ3 ~ baseTargetQ3
	// But VBR computation adds boosts based on dynalloc, tf_estimate, tonality
	// Since the encoder uses VBR mode, let's see what it might produce

	// Convert Q3 to bits: (9760 + 4) >> 3 = 1220 bits
	targetBitsVBR := (baseTargetQ3 + 4) >> 3

	t.Logf("targetBitsSimple=%d, targetBitsVBR=%d (approx)", targetBitsSimple, targetBitsVBR)
	t.Logf("effectiveBytesSimple=%d, effectiveBytesVBR=%d", effectiveBytesSimple, targetBitsVBR/8)

	// Test both equivRate calculations
	equivRateSimple := celt.ComputeEquivRate(effectiveBytesSimple, 1, lm, bitrate)
	equivRateVBR := celt.ComputeEquivRate(targetBitsVBR/8, 1, lm, bitrate)
	t.Logf("equivRateSimple=%d, equivRateVBR=%d", equivRateSimple, equivRateVBR)

	// Use the simple calculation (matching encoder's effectiveBytes = targetBits/8)
	targetBits := targetBitsSimple
	effectiveBytes := targetBits / 8
	equivRate := celt.ComputeEquivRate(effectiveBytes, 1, lm, bitrate)

	// Now compute allocTrim with same inputs
	allocTrim := celt.AllocTrimAnalysis(
		normL,
		energies,
		nbBands,
		lm,
		1,       // mono
		nil,     // no right channel
		nbBands, // intensity
		tfEstimate,
		equivRate,
		0.0, // surroundTrim
		0.0, // tonalitySlope
	)
	t.Logf("Computed allocTrim = %d (with tfEstimate=%.4f)", allocTrim, tfEstimate)

	// Compare with tfEstimate=0.0 (what might be happening in encoder)
	allocTrimNoTf := celt.AllocTrimAnalysis(
		normL, energies, nbBands, lm, 1, nil, nbBands, 0.0, equivRate, 0.0, 0.0,
	)
	t.Logf("allocTrim with tfEstimate=0.0: %d", allocTrimNoTf)

	// Also test with different tfEstimate values
	for tf := 0.0; tf <= 0.5; tf += 0.1 {
		trim := celt.AllocTrimAnalysis(normL, energies, nbBands, lm, 1, nil, nbBands, tf, equivRate, 0.0, 0.0)
		t.Logf("  tfEstimate=%.1f -> trim=%d", tf, trim)
	}

	if allocTrim != libTrim {
		t.Errorf("Allocation trim mismatch: computed=%d, libopus=%d", allocTrim, libTrim)
	}
}

// TestTrimICDFEncodeDecode verifies ICDF encoding/decoding roundtrip
func TestTrimICDFEncodeDecode(t *testing.T) {
	trimICDF := []uint8{126, 124, 119, 109, 87, 41, 19, 9, 4, 2, 0}

	for sym := 0; sym <= 10; sym++ {
		buf := make([]byte, 256)
		enc := &rangecoding.Encoder{}
		enc.Init(buf)

		// Encode the symbol
		enc.EncodeICDF(sym, trimICDF, 7)
		data := enc.Done()

		// Decode and verify
		dec := &rangecoding.Decoder{}
		dec.Init(data)
		decoded := dec.DecodeICDF(trimICDF, 7)

		if decoded != sym {
			t.Errorf("Symbol %d: encoded/decoded mismatch, got %d", sym, decoded)
		} else {
			t.Logf("Symbol %d: OK", sym)
		}
	}
}

// GetAllocTrimFromPacket decodes a CELT packet to get the allocation trim value.
func GetAllocTrimFromPacket(payload []byte, nbBands, lm int) int {
	return GetAllocTrimFromPacketDebug(payload, nbBands, lm, nil)
}

// GetAllocTrimFromPacketDebug decodes trim with optional debug logging.
func GetAllocTrimFromPacketDebug(payload []byte, nbBands, lm int, t *testing.T) int {
	// Use the same decoding as TestPostTFEncoding but extract just the trim
	rd := &rangecoding.Decoder{}
	rd.Init(payload)

	// Decode header flags
	_ = rd.DecodeBit(15) // silence
	_ = rd.DecodeBit(1)  // postfilter

	transient := false
	if lm > 0 {
		transient = rd.DecodeBit(3) == 1
	}
	intra := rd.DecodeBit(3) == 1

	if t != nil {
		t.Logf("[decoder] transient=%v, intra=%v, tell=%d", transient, intra, rd.Tell())
	}

	// Decode coarse energy
	dec := celt.NewDecoder(1)
	dec.SetRangeDecoder(rd)
	_ = dec.DecodeCoarseEnergy(nbBands, intra, lm)

	if t != nil {
		t.Logf("[decoder] after coarse energy: tell=%d", rd.Tell())
	}

	// Decode TF
	tfRes := make([]int, nbBands)
	celt.TFDecodeForTest(0, nbBands, transient, tfRes, lm, rd)

	if t != nil {
		t.Logf("[decoder] after TF: tell=%d", rd.Tell())
	}

	// Decode spread - MUST use the correct ICDF table from tables.go
	spreadICDF := []byte{25, 23, 2, 0}
	spread := rd.DecodeICDF(spreadICDF, 5)

	if t != nil {
		t.Logf("[decoder] spread=%d, tell=%d", spread, rd.Tell())
	}

	// Decode dynalloc
	dynallocLogp := 6
	totalBoost := 0
	for band := 0; band < nbBands; band++ {
		loopLogp := dynallocLogp
		boost := 0
		for j := 0; j < 10; j++ {
			flag := rd.DecodeBit(uint(loopLogp))
			if flag == 0 {
				break
			}
			boost++
			loopLogp = 1
		}
		if boost > 0 {
			totalBoost += boost
			if dynallocLogp > 2 {
				dynallocLogp--
			}
		}
	}

	if t != nil {
		t.Logf("[decoder] after dynalloc: tell=%d, totalBoost=%d", rd.Tell(), totalBoost)
		t.Logf("[decoder] range state before trim: val=0x%08X, rng=0x%08X", rd.Val(), rd.Range())
	}

	// Decode allocation trim
	// IMPORTANT: Must use the full 11-element ICDF table, not truncated version
	trimICDF := []byte{126, 124, 119, 109, 87, 41, 19, 9, 4, 2, 0}
	allocTrim := rd.DecodeICDF(trimICDF, 7)

	if t != nil {
		t.Logf("[decoder] allocTrim=%d, tell=%d", allocTrim, rd.Tell())
	}

	return allocTrim
}
