package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

func TestSampleLevelDebug(t *testing.T) {
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

	t.Log("=== Sample Level Debug ===")

	// Original signal stats
	origRMS := computeRMSF64(pcm)
	origMax := computeMaxF64(pcm)
	t.Logf("Original: RMS=%.6f, max=%.6f", origRMS, origMax)
	t.Logf("Original samples [400:410]: ")
	for i := 400; i < 410; i++ {
		t.Logf("  [%d] = %.6f", i, pcm[i])
	}

	// Gopus encode
	celtEnc := celt.NewEncoder(1)
	celtEnc.SetBitrate(64000)
	gopusPacket, _ := celtEnc.EncodeFrame(pcm, frameSize)
	t.Logf("\nGopus packet: %d bytes", len(gopusPacket))

	// Libopus encode
	libEnc, _ := NewLibopusEncoder(48000, 1, OpusApplicationAudio)
	defer libEnc.Destroy()
	libEnc.SetBitrate(64000)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(true)
	libPacket, _ := libEnc.EncodeFloat(pcm32, frameSize)
	t.Logf("Libopus packet: %d bytes", len(libPacket))

	// Decode gopus packet with libopus decoder
	libDec1, _ := NewLibopusDecoder(48000, 1)
	defer libDec1.Destroy()

	tocGopus := byte(0xF8) // CELT FB 20ms
	gopusWithTOC := append([]byte{tocGopus}, gopusPacket...)
	gopusDecoded, gopusSamples := libDec1.DecodeFloat(gopusWithTOC, frameSize)

	if gopusSamples <= 0 {
		t.Fatalf("Gopus decode failed: %d", gopusSamples)
	}

	gopusRMS := computeRMS32F(gopusDecoded[:frameSize])
	gopusMax := computeMax32F(gopusDecoded[:frameSize])
	t.Logf("\nGopus decoded: %d samples, RMS=%.6f, max=%.6f", gopusSamples, gopusRMS, gopusMax)
	t.Logf("Gopus decoded samples [400:410]:")
	for i := 400; i < 410; i++ {
		t.Logf("  [%d] = %.6f", i, gopusDecoded[i])
	}

	// Decode libopus packet
	libDec2, _ := NewLibopusDecoder(48000, 1)
	defer libDec2.Destroy()

	libDecoded, libSamples := libDec2.DecodeFloat(libPacket, frameSize)

	libRMS := computeRMS32F(libDecoded[:frameSize])
	libMax := computeMax32F(libDecoded[:frameSize])
	t.Logf("\nLibopus decoded: %d samples, RMS=%.6f, max=%.6f", libSamples, libRMS, libMax)
	t.Logf("Libopus decoded samples [400:410]:")
	for i := 400; i < 410; i++ {
		t.Logf("  [%d] = %.6f", i, libDecoded[i])
	}

	// Ratios
	t.Log("\n=== Amplitude Analysis ===")
	t.Logf("Gopus/Original RMS ratio: %.4f (%.1f dB)", gopusRMS/origRMS, 20*math.Log10(gopusRMS/origRMS))
	t.Logf("Libopus/Original RMS ratio: %.4f (%.1f dB)", libRMS/origRMS, 20*math.Log10(libRMS/origRMS))
	t.Logf("Gopus/Libopus RMS ratio: %.4f (%.1f dB)", gopusRMS/libRMS, 20*math.Log10(gopusRMS/libRMS))

	// Correlation analysis
	t.Log("\n=== Correlation Analysis ===")
	gopusCorr := computeCorr64vs32(pcm, gopusDecoded[:frameSize])
	libCorr := computeCorr64vs32(pcm, libDecoded[:frameSize])
	t.Logf("Gopus vs Original correlation: %.6f", gopusCorr)
	t.Logf("Libopus vs Original correlation: %.6f", libCorr)

	// Find best delay alignment
	t.Log("\n=== Delay Search ===")
	bestGopusDelay := 0
	bestGopusCorr := -2.0
	bestLibDelay := 0
	bestLibCorr := -2.0

	for delay := -500; delay <= 500; delay++ {
		corr := computeCorrWithDelay64vs32(pcm, gopusDecoded[:frameSize], delay)
		if corr > bestGopusCorr {
			bestGopusCorr = corr
			bestGopusDelay = delay
		}

		corr = computeCorrWithDelay64vs32(pcm, libDecoded[:frameSize], delay)
		if corr > bestLibCorr {
			bestLibCorr = corr
			bestLibDelay = delay
		}
	}
	t.Logf("Gopus best delay: %d, correlation: %.6f", bestGopusDelay, bestGopusCorr)
	t.Logf("Libopus best delay: %d, correlation: %.6f", bestLibDelay, bestLibCorr)
}

func computeRMSF64(x []float64) float64 {
	var sum float64
	for _, v := range x {
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(x)))
}

func computeMaxF64(x []float64) float64 {
	max := 0.0
	for _, v := range x {
		if math.Abs(v) > max {
			max = math.Abs(v)
		}
	}
	return max
}

func computeRMS32F(x []float32) float64 {
	var sum float64
	for _, v := range x {
		sum += float64(v * v)
	}
	return math.Sqrt(sum / float64(len(x)))
}

func computeMax32F(x []float32) float64 {
	max := 0.0
	for _, v := range x {
		if math.Abs(float64(v)) > max {
			max = math.Abs(float64(v))
		}
	}
	return max
}

func computeCorr64vs32(x []float64, y []float32) float64 {
	n := len(x)
	if len(y) < n {
		n = len(y)
	}
	var sumXY, sumX2, sumY2 float64
	for i := 0; i < n; i++ {
		sumXY += x[i] * float64(y[i])
		sumX2 += x[i] * x[i]
		sumY2 += float64(y[i]) * float64(y[i])
	}
	if sumX2 == 0 || sumY2 == 0 {
		return 0
	}
	return sumXY / (math.Sqrt(sumX2) * math.Sqrt(sumY2))
}

func computeCorrWithDelay64vs32(x []float64, y []float32, delay int) float64 {
	n := len(x)
	if len(y) < n {
		n = len(y)
	}
	var sumXY, sumX2, sumY2 float64
	count := 0
	for i := 100; i < n-100; i++ {
		yIdx := i + delay
		if yIdx >= 0 && yIdx < len(y) {
			sumXY += x[i] * float64(y[yIdx])
			sumX2 += x[i] * x[i]
			sumY2 += float64(y[yIdx]) * float64(y[yIdx])
			count++
		}
	}
	if sumX2 == 0 || sumY2 == 0 || count == 0 {
		return 0
	}
	return sumXY / (math.Sqrt(sumX2) * math.Sqrt(sumY2))
}
