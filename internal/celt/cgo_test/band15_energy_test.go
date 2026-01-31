// Package cgo investigates band 15 energy divergence.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestBand15EnergyComparison investigates why band 15 fine energy diverges.
func TestBand15EnergyComparison(t *testing.T) {
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

	libPacket, _ := libEnc.EncodeFloat(pcm32, frameSize)
	libPayload := libPacket[1:]
	t.Logf("libopus packet: %d bytes", len(libPayload))

	// Setup gopus encoder
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	// Compute energies
	dcRejected := goEnc.ApplyDCReject(pcm64)
	delayComp := celt.DelayCompensation
	combinedBuf := make([]float64, delayComp+len(dcRejected))
	copy(combinedBuf[delayComp:], dcRejected)
	samplesForFrame := combinedBuf[:frameSize]
	preemph := goEnc.ApplyPreemphasisWithScaling(samplesForFrame)

	overlap := celt.Overlap
	historyBuf := make([]float64, overlap)
	shortBlocks := mode.ShortBlocks
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, historyBuf, shortBlocks)

	energies := goEnc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	// Store raw energies before float32 truncation
	rawEnergies := make([]float64, len(energies))
	copy(rawEnergies, energies)

	// Apply float32 truncation (like libopus does)
	for i := range energies {
		energies[i] = float64(float32(energies[i]))
	}

	t.Log("=== Band 15 Energy Analysis ===")
	t.Logf("Band 15 raw energy (float64): %.15f", rawEnergies[15])
	t.Logf("Band 15 truncated energy (float32): %.15f", energies[15])

	// Initialize encoder for coarse energy
	targetBytes := 159
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)
	re.Shrink(uint32(targetBytes))
	goEnc.SetRangeEncoder(re)

	// Header flags
	re.EncodeBit(0, 15)
	re.EncodeBit(0, 1)
	re.EncodeBit(1, 3)
	re.EncodeBit(1, 3)

	// Get quantized energies from coarse encoding
	quantizedEnergies := goEnc.EncodeCoarseEnergy(energies, nbBands, true, lm)

	t.Logf("Band 15 quantized energy: %.15f", quantizedEnergies[15])

	error15 := energies[15] - quantizedEnergies[15]
	t.Logf("Band 15 error (energy - quantized): %.15f", error15)

	// Fine quant calculation
	fineBits := 3 // From the allocation test
	scale := 1 << uint(fineBits)
	q := int(math.Floor((error15 + 0.5) * float64(scale)))
	if q < 0 {
		q = 0
	}
	if q >= scale {
		q = scale - 1
	}

	t.Logf("Fine quant calculation for band 15:")
	t.Logf("  fineBits = %d", fineBits)
	t.Logf("  scale = %d", scale)
	t.Logf("  error + 0.5 = %.15f", error15+0.5)
	t.Logf("  (error + 0.5) * scale = %.15f", (error15+0.5)*float64(scale))
	t.Logf("  floor(...) = %d", int(math.Floor((error15+0.5)*float64(scale))))
	t.Logf("  final q = %d", q)

	// For q=1, we need (error + 0.5) * 8 >= 1, so error >= -0.375
	threshold := -0.5 + 1.0/float64(scale)
	t.Logf("")
	t.Logf("Threshold for q=1: error >= %.15f", threshold)
	t.Logf("Difference from threshold: %.15f", error15-threshold)

	// Also check if float32 precision matters
	error15_f32 := float64(float32(error15))
	q_f32 := int(math.Floor((error15_f32 + 0.5) * float64(scale)))
	if q_f32 < 0 {
		q_f32 = 0
	}
	if q_f32 >= scale {
		q_f32 = scale - 1
	}
	t.Logf("")
	t.Logf("With float32 error: %.15f -> q=%d", error15_f32, q_f32)

	// What if energies were computed slightly differently?
	t.Log("")
	t.Log("=== Sensitivity Analysis ===")
	for delta := -0.2; delta <= 0.2; delta += 0.05 {
		testError := error15 + delta
		testQ := int(math.Floor((testError + 0.5) * float64(scale)))
		if testQ < 0 {
			testQ = 0
		}
		if testQ >= scale {
			testQ = scale - 1
		}
		marker := ""
		if testQ != q {
			marker = " <-- different q!"
		}
		t.Logf("  error=%.6f: q=%d%s", testError, testQ, marker)
	}
}

// TestDecodeLibopusFineEnergy decodes libopus packet to get its fine energy values.
func TestDecodeLibopusFineEnergy(t *testing.T) {
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

	libPacket, _ := libEnc.EncodeFloat(pcm32, frameSize)
	libPayload := libPacket[1:]

	// Initialize range decoder
	rd := &rangecoding.Decoder{}
	rd.Init(libPayload)

	t.Log("=== Decoding libopus fine energy ===")

	// Decode header flags
	silence := rd.DecodeBit(15)
	postfilter := rd.DecodeBit(1)
	transient := rd.DecodeBit(3)
	intra := rd.DecodeBit(3)
	t.Logf("Header: silence=%d postfilter=%d transient=%d intra=%d",
		silence, postfilter, transient, intra)

	tellAfterHeader := rd.Tell()
	t.Logf("Tell after header: %d bits", tellAfterHeader)

	// Now we need to decode coarse energy to get the right position
	// This is complex because we need the full decoding path
	// For now, let's just trace bytes around byte 16

	t.Log("")
	t.Log("=== Bytes around divergence ===")
	t.Log("Byte | Value")
	for i := 14; i <= 18 && i < len(libPayload); i++ {
		t.Logf("  %2d | 0x%02x (%08b)", i, libPayload[i], libPayload[i])
	}
}

// TestCompareActualFineQuantValues compares actual fine quant indices.
func TestCompareActualFineQuantValues(t *testing.T) {
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

	libPacket, _ := libEnc.EncodeFloat(pcm32, frameSize)
	libPayload := libPacket[1:]

	// Encode with gopus
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	goPacket, _ := goEnc.EncodeFrame(pcm64, frameSize)

	// Compare bytes around divergence
	t.Log("=== Byte Comparison Around Band 15 ===")
	t.Log("(Fine energy for band 15 is at bits ~127-129)")
	t.Log("")
	t.Log("Byte | gopus   | libopus | Match")
	t.Log("-----+---------+---------+------")

	minLen := len(goPacket)
	if len(libPayload) < minLen {
		minLen = len(libPayload)
	}

	for i := 14; i < 20 && i < minLen; i++ {
		g := goPacket[i]
		l := libPayload[i]
		match := "OK"
		if g != l {
			match = "DIFF"
		}
		t.Logf("  %2d | %08b | %08b | %s (xor=%08b)", i, g, l, match, g^l)
	}

	// The divergence at byte 16 with XOR=0x01 means the LSB differs
	// In range coding, LSB differences are typically from the final carry propagation
	// or from the very last bits encoded before that point

	if goPacket[16] != libPayload[16] {
		xor := goPacket[16] ^ libPayload[16]
		t.Logf("")
		t.Logf("Byte 16 XOR: 0x%02x", xor)
		t.Logf("gopus byte 16:  0x%02x = %08b", goPacket[16], goPacket[16])
		t.Logf("libopus byte 16: 0x%02x = %08b", libPayload[16], libPayload[16])

		// Find the exact bit position
		for b := 0; b < 8; b++ {
			if (xor>>b)&1 == 1 {
				t.Logf("First differing bit: bit %d of byte 16 = bit %d overall", b, 16*8+b)
			}
		}
	}
}
