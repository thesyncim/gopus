package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func TestStepByStepEncodeCompare(t *testing.T) {
	sampleRate := 48000
	frameSize := 960

	// Generate simple sine wave
	pcm := make([]float64, frameSize)
	pcm32 := make([]float32, frameSize)
	for i := 0; i < frameSize; i++ {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm[i] = val
		pcm32[i] = float32(val)
	}

	t.Log("=== Step-by-Step Encoding Comparison ===")
	t.Log("")

	// GOPUS ENCODING
	t.Log("--- GOPUS ---")
	enc := celt.NewEncoder(1)
	enc.SetBitrate(64000)

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	// Step 1: Pre-emphasis
	preemph := enc.ApplyPreemphasis(pcm)
	t.Logf("Pre-emphasis sum: %.6f", sumAbs(preemph))

	// Step 2: MDCT
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, make([]float64, 120), 1)
	t.Logf("MDCT coeffs sum: %.6f, first 5: [%.4f, %.4f, %.4f, %.4f, %.4f]",
		sumAbs(mdctCoeffs), mdctCoeffs[0], mdctCoeffs[1], mdctCoeffs[2], mdctCoeffs[3], mdctCoeffs[4])

	// Step 3: Band energies
	energies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)
	t.Logf("Energies first 5: [%.4f, %.4f, %.4f, %.4f, %.4f]",
		energies[0], energies[1], energies[2], energies[3], energies[4])

	// Step 4: Range encoder initialization
	targetBits := 1280 // 64kbps * 20ms
	buf := make([]byte, (targetBits+7)/8+256)
	re := &rangecoding.Encoder{}
	re.Init(buf)
	enc.SetRangeEncoder(re)

	// Step 5: Encode flags
	// Silence flag
	re.EncodeBit(0, 15) // Not silent
	t.Logf("After silence flag: tell=%d, range=0x%08X", re.Tell(), re.Range())

	// Postfilter flag
	re.EncodeBit(0, 1) // No postfilter
	t.Logf("After postfilter flag: tell=%d, range=0x%08X", re.Tell(), re.Range())

	// Transient flag
	if lm > 0 {
		re.EncodeBit(0, 3) // Not transient
		t.Logf("After transient flag: tell=%d, range=0x%08X", re.Tell(), re.Range())
	}

	// Intra flag
	re.EncodeBit(1, 3) // Intra (first frame)
	t.Logf("After intra flag: tell=%d, range=0x%08X", re.Tell(), re.Range())

	// Step 6: Encode coarse energy
	tellBefore := re.Tell()
	quantized := enc.EncodeCoarseEnergy(energies, nbBands, true, lm)
	tellAfter := re.Tell()
	t.Logf("Coarse energy: %d bits, first 5 quant: [%.4f, %.4f, %.4f, %.4f, %.4f]",
		tellAfter-tellBefore, quantized[0], quantized[1], quantized[2], quantized[3], quantized[4])

	// Get bytes so far
	partialBytes := re.Done()
	partialLen := len(partialBytes)
	if partialLen > 20 {
		partialLen = 20
	}
	t.Logf("Partial bytes (flags+energy): %02X", partialBytes[:partialLen])

	// LIBOPUS ENCODING
	t.Log("")
	t.Log("--- LIBOPUS ---")

	libEnc, err := NewLibopusEncoder(48000, 1, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("Failed to create libopus encoder: %v", err)
	}
	defer libEnc.Destroy()

	libEnc.SetBitrate(64000)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(true)

	libPacket, n := libEnc.EncodeFloat(pcm32, frameSize)
	if n < 0 {
		t.Fatalf("libopus encode failed: %d", n)
	}

	t.Logf("Libopus packet: %d bytes", len(libPacket))
	t.Logf("TOC: 0x%02X", libPacket[0])
	payloadLen := len(libPacket)
	if payloadLen > 21 {
		payloadLen = 21
	}
	t.Logf("Payload first 20: %02X", libPacket[1:payloadLen])

	// Compare bytes
	t.Log("")
	t.Log("--- BYTE COMPARISON ---")
	libPayload := libPacket[1:]
	maxCmp := len(partialBytes)
	if len(libPayload) < maxCmp {
		maxCmp = len(libPayload)
	}
	if maxCmp > 20 {
		maxCmp = 20
	}
	t.Logf("       gopus    libopus")
	for i := 0; i < maxCmp; i++ {
		match := ""
		if partialBytes[i] == libPayload[i] {
			match = " *"
		}
		t.Logf("  %2d:  0x%02X     0x%02X%s", i, partialBytes[i], libPayload[i], match)
	}

	// Decode both and compare
	t.Log("")
	t.Log("--- DECODE COMPARISON ---")

	// Full gopus encode
	gopusPacket, err := enc.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("gopus full encode failed: %v", err)
	}
	t.Logf("Gopus full packet: %d bytes", len(gopusPacket))

	// Decode both with libopus
	libDec1, _ := NewLibopusDecoder(48000, 1)
	defer libDec1.Destroy()
	libDec2, _ := NewLibopusDecoder(48000, 1)
	defer libDec2.Destroy()

	// Add TOC to gopus packet
	toc := byte((31 << 3) | 0) // CELT FB 20ms, code 0
	gopusWithTOC := append([]byte{toc}, gopusPacket...)

	gopusDecoded, gopusSamples := libDec1.DecodeFloat(gopusWithTOC, frameSize)
	libDecoded, libSamples := libDec2.DecodeFloat(libPacket, frameSize)

	t.Logf("Gopus decoded: %d samples", gopusSamples)
	t.Logf("Libopus decoded: %d samples", libSamples)

	// Compare decoded at middle
	t.Log("")
	t.Log("Middle samples (400-410):")
	t.Logf("  idx     original     gopus-dec    libopus-dec")
	for i := 400; i < 410; i++ {
		t.Logf("  [%d]  %10.5f  %10.5f  %10.5f",
			i, pcm[i], gopusDecoded[i], libDecoded[i])
	}

	// Compute correlations
	gopusCorr := computeCorrFloat(pcm, toFloat64(gopusDecoded[:frameSize]))
	libCorr := computeCorrFloat(pcm, toFloat64(libDecoded[:frameSize]))
	t.Logf("\nCorrelation: gopus=%.4f, libopus=%.4f", gopusCorr, libCorr)
}

func sumAbs(x []float64) float64 {
	sum := 0.0
	for _, v := range x {
		if v < 0 {
			sum -= v
		} else {
			sum += v
		}
	}
	return sum
}

func toFloat64(f32 []float32) []float64 {
	f64 := make([]float64, len(f32))
	for i, v := range f32 {
		f64[i] = float64(v)
	}
	return f64
}

func computeCorrFloat(x, y []float64) float64 {
	n := len(x)
	if len(y) < n {
		n = len(y)
	}

	var sumXY, sumX2, sumY2 float64
	for i := 0; i < n; i++ {
		sumXY += x[i] * y[i]
		sumX2 += x[i] * x[i]
		sumY2 += y[i] * y[i]
	}

	if sumX2 == 0 || sumY2 == 0 {
		return 0
	}
	return sumXY / (math.Sqrt(sumX2) * math.Sqrt(sumY2))
}
