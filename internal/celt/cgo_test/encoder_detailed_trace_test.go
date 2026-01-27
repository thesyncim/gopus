// Package cgo provides detailed tracing for encoder debugging.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestDetailedEncoderPath traces the complete encoding path with exact values.
func TestDetailedEncoderPath(t *testing.T) {
	frameSize := 960
	channels := 1
	sampleRate := 48000

	// Generate a simple 440 Hz sine wave
	samples := make([]float64, frameSize)
	amplitude := 0.5
	freq := 440.0
	for i := range samples {
		samples[i] = amplitude * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate))
	}

	t.Log("=== STEP 1: INPUT ===")
	var inputSum, inputSumSq float64
	for _, s := range samples {
		inputSum += s
		inputSumSq += s * s
	}
	t.Logf("  Input samples: %d", len(samples))
	t.Logf("  Mean: %.6f", inputSum/float64(len(samples)))
	t.Logf("  RMS: %.6f", math.Sqrt(inputSumSq/float64(len(samples))))
	t.Logf("  Peak: %.6f", amplitude)

	// Create encoder
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)

	t.Log("\n=== STEP 2: PRE-EMPHASIS ===")
	preemph := encoder.ApplyPreemphasisWithScaling(samples)
	var preSum, preSumSq float64
	var preMax float64
	for _, s := range preemph {
		preSum += s
		preSumSq += s * s
		if math.Abs(s) > preMax {
			preMax = math.Abs(s)
		}
	}
	t.Logf("  Pre-emphasis output: %d samples", len(preemph))
	t.Logf("  Mean: %.6f", preSum/float64(len(preemph)))
	t.Logf("  RMS: %.6f", math.Sqrt(preSumSq/float64(len(preemph))))
	t.Logf("  Peak: %.6f", preMax)
	t.Logf("  Expected peak (after 32768x scale): %.2f", amplitude*32768)
	t.Logf("  Actual vs expected ratio: %.2f", preMax/(amplitude*32768))

	t.Log("\n=== STEP 3: MDCT ===")
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, encoder.OverlapBuffer(), 1)
	var mdctSum, mdctSumSq float64
	var mdctMax float64
	mdctMaxIdx := 0
	for i, c := range mdctCoeffs {
		mdctSum += c
		mdctSumSq += c * c
		if math.Abs(c) > mdctMax {
			mdctMax = math.Abs(c)
			mdctMaxIdx = i
		}
	}
	t.Logf("  MDCT coefficients: %d", len(mdctCoeffs))
	t.Logf("  Mean: %.6f", mdctSum/float64(len(mdctCoeffs)))
	t.Logf("  RMS: %.6f", math.Sqrt(mdctSumSq/float64(len(mdctCoeffs))))
	t.Logf("  Peak: %.6f at index %d", mdctMax, mdctMaxIdx)

	// Show bins around 440 Hz
	// 440 Hz at 48k = bin 440*2*960/48000 = 17.6
	expectedBin := int(freq * 2 * float64(frameSize) / float64(sampleRate))
	t.Logf("\n  440 Hz bins (around index %d):", expectedBin)
	for i := max5(0, expectedBin-3); i <= min5(len(mdctCoeffs)-1, expectedBin+3); i++ {
		t.Logf("    [%d] = %.6f", i, mdctCoeffs[i])
	}

	t.Log("\n=== STEP 4: BAND ENERGIES ===")
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	bandEnergies := encoder.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	// eMeans table (local copy for comparison)
	eMeans := [25]float64{
		6.437500, 6.250000, 5.750000, 5.312500, 5.062500,
		4.812500, 4.500000, 4.375000, 4.875000, 4.687500,
		4.562500, 4.437500, 4.875000, 4.625000, 4.312500,
		4.500000, 4.375000, 4.625000, 4.750000, 4.437500,
		3.750000, 3.750000, 3.750000, 3.750000, 3.750000,
	}

	t.Logf("  Number of bands: %d", nbBands)
	t.Log("\n  Band details:")
	offset := 0
	for band := 0; band < nbBands; band++ {
		width := celt.ScaledBandWidth(band, frameSize)
		end := offset + width
		if end > len(mdctCoeffs) {
			end = len(mdctCoeffs)
		}

		// Compute raw linear energy
		var sumSq float64
		for i := offset; i < end; i++ {
			sumSq += mdctCoeffs[i] * mdctCoeffs[i]
		}
		linearAmp := math.Sqrt(sumSq)

		// What encoder computed
		encoderEnergy := bandEnergies[band]

		// Decode what this means
		rawLogE := 0.5 * math.Log2(sumSq+1e-27)

		// What the encoder should have computed (subtracting eMeans)
		expectedEncoderE := rawLogE
		if band < len(eMeans) {
			expectedEncoderE -= eMeans[band]
		}

		// What the decoder will interpret this as (adding eMeans back)
		decoderTotalE := encoderEnergy
		if band < len(eMeans) {
			decoderTotalE += eMeans[band]
		}
		decoderGain := math.Exp2(decoderTotalE)

		t.Logf("  Band %d (bins %d-%d, width %d):", band, offset, end-1, width)
		t.Logf("    Linear amplitude: %.6f", linearAmp)
		t.Logf("    Raw log2(amp): %.6f", rawLogE)
		t.Logf("    eMeans[%d]: %.6f", band, eMeans[band])
		t.Logf("    Encoder output (rawLogE - eMeans): %.6f", encoderEnergy)
		t.Logf("    Expected encoder output: %.6f", expectedEncoderE)
		t.Logf("    Decoder interpreted e: %.6f", decoderTotalE)
		t.Logf("    Decoder gain: %.6f", decoderGain)
		t.Logf("    Roundtrip (should match linear amp): %.6f (ratio: %.4f)",
			decoderGain, decoderGain/linearAmp)

		offset = end
		if band >= 5 {
			break // Only show first 6 bands for readability
		}
	}

	t.Log("\n=== STEP 5: ENCODE AND DECODE ===")
	// Encode
	encoded, err := encoder.EncodeFrame(samples, frameSize)
	if err != nil {
		t.Fatalf("EncodeFrame failed: %v", err)
	}
	t.Logf("  Encoded: %d bytes", len(encoded))

	// Decode with gopus decoder
	decoder := celt.NewDecoder(channels)
	decoded, err := decoder.DecodeFrame(encoded, frameSize)
	if err != nil {
		t.Fatalf("DecodeFrame failed: %v", err)
	}

	// Compare
	var decSum, decSumSq float64
	var decMax float64
	for _, s := range decoded {
		decSum += s
		decSumSq += s * s
		if math.Abs(s) > decMax {
			decMax = math.Abs(s)
		}
	}
	t.Logf("  Decoded samples: %d", len(decoded))
	t.Logf("  Mean: %.6f", decSum/float64(len(decoded)))
	t.Logf("  RMS: %.6f", math.Sqrt(decSumSq/float64(len(decoded))))
	t.Logf("  Peak: %.6f", decMax)

	// Compute correlation and SNR
	compareLen := min5(len(samples), len(decoded))
	var sumXY, sumXX, sumYY float64
	var sigPow, noisePow float64
	for i := 0; i < compareLen; i++ {
		x := samples[i]
		y := decoded[i]
		sumXY += x * y
		sumXX += x * x
		sumYY += y * y
		sigPow += x * x
		noisePow += (y - x) * (y - x)
	}
	corr := sumXY / math.Sqrt(sumXX*sumYY)
	snr := 10 * math.Log10(sigPow/noisePow)

	t.Logf("\n  Input vs Decoded comparison:")
	t.Logf("    Correlation: %.6f", corr)
	t.Logf("    SNR: %.2f dB", snr)
	t.Logf("    Energy ratio (decoded/input): %.6f", decSumSq/inputSumSq)

	// Decode with libopus for comparison
	t.Log("\n=== STEP 6: DECODE WITH LIBOPUS ===")
	toc := byte(0x78) // CELT fullband 20ms mono
	packet := append([]byte{toc}, encoded...)

	libDec, err := NewLibopusDecoder(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer libDec.Destroy()

	libDecoded, libSamples := libDec.DecodeFloat(packet, frameSize)
	if libSamples <= 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	var libSum, libSumSq float64
	var libMax float64
	for i := 0; i < libSamples; i++ {
		s := libDecoded[i]
		libSum += float64(s)
		libSumSq += float64(s) * float64(s)
		if math.Abs(float64(s)) > libMax {
			libMax = math.Abs(float64(s))
		}
	}
	t.Logf("  libopus decoded: %d samples", libSamples)
	t.Logf("  Mean: %.6f", libSum/float64(libSamples))
	t.Logf("  RMS: %.6f", math.Sqrt(libSumSq/float64(libSamples)))
	t.Logf("  Peak: %.6f", libMax)

	// Compare libopus output vs input
	var libSigPow, libNoisePow float64
	for i := 0; i < min5(frameSize, libSamples); i++ {
		x := samples[i]
		y := float64(libDecoded[i])
		libSigPow += x * x
		libNoisePow += (y - x) * (y - x)
	}
	libSNR := 10 * math.Log10(libSigPow/libNoisePow)
	t.Logf("  libopus vs input SNR: %.2f dB", libSNR)

	// The key question: is libopus producing near-zero output?
	if libMax < 0.01 {
		t.Log("\n  WARNING: libopus output is essentially zero!")
		t.Log("  This confirms the encoded energy values are wrong.")
	}
}

// TestTraceEnergyMismatch compares encoder band energies with what libopus expects.
func TestTraceEnergyMismatch(t *testing.T) {
	frameSize := 960
	channels := 1

	// Generate test signal
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/48000)
	}

	// Encode
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)

	preemph := encoder.ApplyPreemphasisWithScaling(samples)
	mdct := celt.ComputeMDCTWithHistory(preemph, encoder.OverlapBuffer(), 1)

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	bandEnergies := encoder.ComputeBandEnergies(mdct, nbBands, frameSize)

	// What are reasonable band energies?
	// After 32768x scaling, a 0.5 amplitude signal becomes amplitude ~16384
	// log2(16384) ~ 14
	// After subtracting eMeans[0] ~ 6.4, we should have ~7.6

	t.Log("Band energies analysis:")
	t.Log("(After 32768x scaling, 0.5 amplitude -> ~16384)")
	t.Log("(Expected log2(16384) - eMeans[0] ~ 14 - 6.4 ~ 7.6)")

	for band := 0; band < min5(10, nbBands); band++ {
		t.Logf("  Band %d: energy = %.4f", band, bandEnergies[band])
	}
}

func min5(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max5(a, b int) int {
	if a > b {
		return a
	}
	return b
}
