// Package encoder export_test.go exports unexported items for testing.
// This file is only compiled during test execution.
package encoder

// Export unexported functions for testing

// Downsample48to16 exports downsample48to16 for testing.
var Downsample48to16 = downsample48to16

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
