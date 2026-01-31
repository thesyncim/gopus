package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

func TestGopusRoundTrip(t *testing.T) {
	frameSize := 960

	// Generate simple sine wave
	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		ti := float64(i) / 48000.0
		pcm[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	t.Log("=== Gopus Encode -> Gopus Decode Round-trip ===")
	t.Logf("Original samples [400:405]: %.4f, %.4f, %.4f, %.4f, %.4f",
		pcm[400], pcm[401], pcm[402], pcm[403], pcm[404])

	// Encode with gopus
	enc := celt.NewEncoder(1)
	enc.SetBitrate(64000)
	packet, err := enc.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("Gopus encode failed: %v", err)
	}
	t.Logf("Encoded packet: %d bytes", len(packet))

	// Decode with gopus decoder
	dec := celt.NewDecoder(1)
	gopusDecoded, err := dec.DecodeFrame(packet, frameSize)
	if err != nil {
		t.Fatalf("Gopus decode failed: %v", err)
	}
	t.Logf("Gopus decoded samples [400:405]: %.4f, %.4f, %.4f, %.4f, %.4f",
		gopusDecoded[400], gopusDecoded[401], gopusDecoded[402], gopusDecoded[403], gopusDecoded[404])

	// Compute correlation and SNR with gopus decoder
	gopusRMS := computeRMSF64(gopusDecoded[:frameSize])
	origRMS := computeRMSF64(pcm)
	gopusCorr := computeCorrF64(pcm, gopusDecoded[:frameSize])

	t.Logf("\nGopus round-trip:")
	t.Logf("  RMS: original=%.6f, decoded=%.6f", origRMS, gopusRMS)
	t.Logf("  Correlation: %.6f", gopusCorr)
	t.Logf("  Sign at [400]: original=%s, decoded=%s",
		signStrF64(pcm[400]), signStrF64(gopusDecoded[400]))

	// Compare with libopus decoder
	t.Log("\n=== Gopus Encode -> Libopus Decode ===")
	libDec, _ := NewLibopusDecoder(48000, 1)
	defer libDec.Destroy()

	toc := byte(0xF8) // CELT FB 20ms
	packetWithTOC := append([]byte{toc}, packet...)
	libDecoded, _ := libDec.DecodeFloat(packetWithTOC, frameSize)

	t.Logf("Libopus decoded samples [400:405]: %.4f, %.4f, %.4f, %.4f, %.4f",
		libDecoded[400], libDecoded[401], libDecoded[402], libDecoded[403], libDecoded[404])

	libRMS := computeRMS32F(libDecoded[:frameSize])
	libCorr := computeCorrF64vs32(pcm, libDecoded[:frameSize])

	t.Logf("\nLibopus decode of gopus packet:")
	t.Logf("  RMS: original=%.6f, decoded=%.6f", origRMS, libRMS)
	t.Logf("  Correlation: %.6f", libCorr)
	t.Logf("  Sign at [400]: original=%s, decoded=%s",
		signStrF64(pcm[400]), signStrF32(libDecoded[400]))

	// Find best delay for both
	t.Log("\n=== Delay Analysis ===")

	bestGopusDelay := 0
	bestGopusCorr := -2.0
	for delay := -500; delay <= 500; delay++ {
		corr := computeCorrWithDelayF64(pcm, gopusDecoded[:frameSize], delay)
		if corr > bestGopusCorr {
			bestGopusCorr = corr
			bestGopusDelay = delay
		}
	}

	bestLibDelay := 0
	bestLibCorr := -2.0
	for delay := -500; delay <= 500; delay++ {
		corr := computeCorrWithDelayF64vs32(pcm, libDecoded[:frameSize], delay)
		if corr > bestLibCorr {
			bestLibCorr = corr
			bestLibDelay = delay
		}
	}

	t.Logf("Gopus decoder: best delay=%d, correlation=%.6f", bestGopusDelay, bestGopusCorr)
	t.Logf("Libopus decoder: best delay=%d, correlation=%.6f", bestLibDelay, bestLibCorr)
}

func computeCorrF64(x, y []float64) float64 {
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

func computeCorrF64vs32(x []float64, y []float32) float64 {
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

func computeCorrWithDelayF64(x, y []float64, delay int) float64 {
	n := len(x)
	if len(y) < n {
		n = len(y)
	}
	var sumXY, sumX2, sumY2 float64
	for i := 100; i < n-100; i++ {
		yIdx := i + delay
		if yIdx >= 0 && yIdx < len(y) {
			sumXY += x[i] * y[yIdx]
			sumX2 += x[i] * x[i]
			sumY2 += y[yIdx] * y[yIdx]
		}
	}
	if sumX2 == 0 || sumY2 == 0 {
		return 0
	}
	return sumXY / (math.Sqrt(sumX2) * math.Sqrt(sumY2))
}

func computeCorrWithDelayF64vs32(x []float64, y []float32, delay int) float64 {
	n := len(x)
	if len(y) < n {
		n = len(y)
	}
	var sumXY, sumX2, sumY2 float64
	for i := 100; i < n-100; i++ {
		yIdx := i + delay
		if yIdx >= 0 && yIdx < len(y) {
			sumXY += x[i] * float64(y[yIdx])
			sumX2 += x[i] * x[i]
			sumY2 += float64(y[yIdx]) * float64(y[yIdx])
		}
	}
	if sumX2 == 0 || sumY2 == 0 {
		return 0
	}
	return sumXY / (math.Sqrt(sumX2) * math.Sqrt(sumY2))
}

func signStrF64(v float64) string {
	if v >= 0 {
		return "+"
	}
	return "-"
}

func signStrF32(v float32) string {
	if v >= 0 {
		return "+"
	}
	return "-"
}
