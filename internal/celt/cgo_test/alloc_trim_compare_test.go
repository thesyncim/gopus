// Package cgo compares allocation trim computation between gopus and libopus.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

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
	goEnc.SetVBR(false)

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

func TestAllocTrimSpectralTilt(t *testing.T) {
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

	// First, encode with libopus to get baseline
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

	// Decode libopus packet header to see transient flag
	t.Log("=== Decoding libopus packet ===")
	libTrim := GetAllocTrimFromPacketDebug(libPacket[1:], nbBands, lm, t)
	t.Logf("libopus packet allocTrim = %d", libTrim)

	// Now trace through gopus encoder to get band energies
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	// Apply delay buffer and pre-emphasis
	delayComp := celt.DelayCompensation
	combinedLen := delayComp + len(pcm64)
	combinedBuf := make([]float64, combinedLen)
	copy(combinedBuf[delayComp:], pcm64)
	samplesForFrame := combinedBuf[:frameSize]
	preemph := goEnc.ApplyPreemphasisWithScaling(samplesForFrame)

	// MDCT - use short blocks (transient)
	historyBuf := make([]float64, celt.Overlap)
	shortBlocks := mode.ShortBlocks
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, historyBuf, shortBlocks)

	// Get band energies
	energies := goEnc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	// Convert to float32 for libopus comparison
	energies32 := make([]float32, len(energies))
	for i, e := range energies {
		energies32[i] = float32(e)
	}

	// Compute spectral tilt using libopus-style logic
	libResult := ComputeLibopusAllocTrim(energies32, nbBands, 1, nbBands, 64000, 0.2, 0.0, 0.0)
	t.Logf("Libopus-style alloc_trim calculation:")
	t.Logf("  baseTrim = %.4f", libResult.BaseTrim)
	t.Logf("  diff (spectral tilt) = %.4f", libResult.Diff)
	t.Logf("  tiltAdjust = %.4f", libResult.TiltAdjust)
	t.Logf("  tfAdjust = %.4f", libResult.TfAdjust)
	t.Logf("  trimIndex = %d", libResult.TrimIndex)

	// Compute using gopus AllocTrimAnalysis
	normL := goEnc.NormalizeBandsToArray(mdctCoeffs, energies, nbBands, frameSize)
	effectiveBytes := 160
	equivRate := celt.ComputeEquivRate(effectiveBytes, 1, lm, bitrate)

	goTrim := celt.AllocTrimAnalysis(
		normL, energies, nbBands, lm, 1, nil, nbBands, 0.2, equivRate, 0.0, 0.0,
	)
	t.Logf("gopus AllocTrimAnalysis = %d", goTrim)

	// Compute gopus spectral tilt manually
	var gopusDiff float64
	end := nbBands
	for i := 0; i < end-1; i++ {
		weight := float64(2 + 2*i - end)
		gopusDiff += energies[i] * weight
	}
	gopusDiff /= float64(end - 1)
	t.Logf("gopus spectral tilt diff = %.4f", gopusDiff)

	gopusTiltAdjust := (gopusDiff + 1.0) / 6.0
	if gopusTiltAdjust < -2.0 {
		gopusTiltAdjust = -2.0
	}
	if gopusTiltAdjust > 2.0 {
		gopusTiltAdjust = 2.0
	}
	t.Logf("gopus tiltAdjust = %.4f", gopusTiltAdjust)

	// Show band energies for analysis
	t.Log("Band energies (first 10):")
	for i := 0; i < 10 && i < nbBands; i++ {
		weight := 2 + 2*i - nbBands
		t.Logf("  band %d: energy=%.4f, weight=%d, contribution=%.4f",
			i, energies[i], weight, energies[i]*float64(weight))
	}

	// The key insight: are the energies correct?
	// Let's compare with energies computed from preemph coeffs directly using libopus logic
	preemph32 := make([]float32, len(preemph))
	for i, v := range preemph {
		preemph32[i] = float32(v)
	}

	// Actually, the issue is that we're computing energies on different MDCT outputs
	// libopus uses short-block MDCT for transient frames
	// Let's check what the actual encoded packet tells us about the energy values

	if libResult.TrimIndex != libTrim {
		t.Logf("NOTE: libopus-style trim (%d) differs from actual packet trim (%d)", libResult.TrimIndex, libTrim)
		t.Log("This means libopus encoder uses different bandLogE values than we're computing!")
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
	energies := dec.DecodeCoarseEnergy(nbBands, intra, lm)

	if t != nil {
		t.Logf("[decoder] after coarse energy: tell=%d", rd.Tell())
		// Print first few energies
		t.Log("[decoder] Decoded energies (first 5):")
		for i := 0; i < 5 && i < len(energies); i++ {
			t.Logf("  band %d: %.4f", i, energies[i])
		}
	}

	// Decode TF
	tfRes := make([]int, nbBands)
	celt.TFDecodeForTest(0, nbBands, transient, tfRes, lm, rd)

	if t != nil {
		t.Logf("[decoder] after TF: tell=%d", rd.Tell())
		// Show TF values
		t.Log("[decoder] TF values (first 10):")
		for i := 0; i < 10 && i < len(tfRes); i++ {
			t.Logf("  band %d: tf=%d", i, tfRes[i])
		}
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
	boostPerBand := make([]int, nbBands)
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
		boostPerBand[band] = boost
		if boost > 0 {
			totalBoost += boost
			if dynallocLogp > 2 {
				dynallocLogp--
			}
		}
	}

	if t != nil {
		t.Logf("[decoder] after dynalloc: tell=%d, totalBoost=%d", rd.Tell(), totalBoost)
		// Print boosts per band (only non-zero)
		t.Log("[decoder] Dynalloc boosts:")
		for band, boost := range boostPerBand {
			if boost > 0 {
				t.Logf("  band %d: boost=%d", band, boost)
			}
		}
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
