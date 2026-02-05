// Package encoder export_test.go exports unexported items for testing.
// This file is only compiled during test execution.
package encoder

// Export unexported functions for testing

// Downsample48to16Hybrid exports the hybrid downsampler for testing.
func (e *Encoder) Downsample48to16Hybrid(samples []float64, frameSize int) []float32 {
	if e.hybridState == nil {
		e.hybridState = &HybridState{
			prevHBGain:     1.0,
			stereoWidthQ14: 16384,
		}
	}
	return e.downsample48to16Hybrid(samples, frameSize)
}

// TargetBytesForBitrate exports targetBytesForBitrate for testing.
var TargetBytesForBitrate = targetBytesForBitrate

// ClassifySignal exports classifySignal for testing.
var ClassifySignal = classifySignal

// ComputeLBRRBitrate exports computeLBRRBitrate for testing.
var ComputeLBRRBitrate = computeLBRRBitrate

// ShouldUseFEC exports shouldUseFEC as a method for testing.
func (e *Encoder) ShouldUseFEC() bool {
	return e.shouldUseFEC()
}

// UpdateFECState exports updateFECState as a method for testing.
func (e *Encoder) UpdateFECState(pcm []float32, vadFlag bool) {
	e.updateFECState(pcm, vadFlag)
}

// WriteFrameLength exports writeFrameLength for testing.
var WriteFrameLength = writeFrameLength

// SilkInputBitrate exports the internal SILK bitrate reservation logic for tests.
func (e *Encoder) SilkInputBitrate(frameSize int) int {
	return e.silkInputBitrate(frameSize)
}

// DTXFrameThreshold is the number of 20ms frames before DTX activates.
// DTXFrameThresholdMs = 200ms, so at 20ms frames, this is 10 frames.
const DTXFrameThreshold = DTXFrameThresholdMs / 20 // = 10 frames (matching NB_SPEECH_FRAMES_BEFORE_DTX)

// Export VAD state for testing
var NewVADStateExport = NewVADState

// Export VAD constants
const (
	VADNBandsExport                  = VADNBands
	VADInternalSubframesExport       = VADInternalSubframes
	VADNoiseLevelSmoothCoefQ16Export = VADNoiseLevelSmoothCoefQ16
	VADNoiseLevelsBiasExport         = VADNoiseLevelsBias
)
