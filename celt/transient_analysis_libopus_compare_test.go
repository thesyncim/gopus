//go:build cgo_libopus
// +build cgo_libopus

package celt

import (
	"math"
	"math/rand"
	"testing"
)

func TestTransientAnalysisParityAgainstLibopus(t *testing.T) {
	const (
		channels   = 2
		sampleRate = 48000
		n          = 1080
		iters      = 300
	)

	enc := NewEncoder(channels)
	rng := rand.New(rand.NewSource(20260207))
	pcm := make([]float64, n*channels)
	toneBuf := make([]float32, n)

	var (
		transientMismatch int
		tfChanMismatch    int
		tfEstimateDrift   int
		maskMetricDrift   int
		maxTfDiff         float64
		maxMaskDiff       float64
	)

	for iter := 0; iter < iters; iter++ {
		for i := range pcm {
			pcm[i] = (rng.Float64()*2.0 - 1.0) * CELTSigScale
		}

		// Use matching tone detect values for the libopus transient path.
		toneFreq, toneishness := libopusToneDetectRef(pcm, channels, sampleRate)

		goResult := enc.TransientAnalysis(pcm, n, false)
		libResult := libopusTransientAnalysisRef(pcm, channels, false, toneFreq, toneishness)

		if goResult.IsTransient != libResult.isTransient {
			transientMismatch++
		}
		if goResult.TfChannel != libResult.tfChan {
			tfChanMismatch++
		}

		tfDiff := math.Abs(goResult.TfEstimate - libResult.tfEstimate)
		if tfDiff > 1e-4 {
			tfEstimateDrift++
			if tfDiff > maxTfDiff {
				maxTfDiff = tfDiff
			}
		}

		maskDiff := math.Abs(goResult.MaskMetric - libResult.maskMetric)
		if maskDiff > 1.0 {
			maskMetricDrift++
			if maskDiff > maxMaskDiff {
				maxMaskDiff = maskDiff
			}
		}

		// Keep scratch warm to match hot-path usage.
		_, _ = toneDetectScratch(pcm, channels, sampleRate, toneBuf)
	}

	t.Logf("iters=%d transientMismatch=%d tfChanMismatch=%d tfEstimateDrift=%d maskMetricDrift=%d maxTfDiff=%.6f maxMaskDiff=%.2f",
		iters, transientMismatch, tfChanMismatch, tfEstimateDrift, maskMetricDrift, maxTfDiff, maxMaskDiff)
	if transientMismatch != 0 || tfChanMismatch != 0 || tfEstimateDrift != 0 || maskMetricDrift != 0 {
		t.Fatalf("transient parity mismatch: transient=%d tfChan=%d tfEstimate=%d maskMetric=%d maxTfDiff=%.6f maxMaskDiff=%.2f",
			transientMismatch, tfChanMismatch, tfEstimateDrift, maskMetricDrift, maxTfDiff, maxMaskDiff)
	}
}
