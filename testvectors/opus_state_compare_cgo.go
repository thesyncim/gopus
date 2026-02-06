//go:build cgo_libopus

package testvectors

import cgowrap "github.com/thesyncim/gopus/celt/cgo_test"

type libopusOpusStateSnapshot = cgowrap.OpusSilkEncoderStateSnapshot
type libopusOpusNSQStateSnapshot = cgowrap.OpusSilkNSQStateSnapshot
type libopusOpusNSQInputSnapshot = cgowrap.OpusNSQInputSnapshot

func captureLibopusOpusSilkState(samples []float32, sampleRate, channels, bitrate, frameSize, frameIndex int) (libopusOpusStateSnapshot, bool) {
	return cgowrap.CaptureOpusSilkEncoderStateAtFrame(samples, sampleRate, channels, bitrate, frameSize, frameIndex)
}

func captureLibopusOpusSilkStateBeforeFrame(samples []float32, sampleRate, channels, bitrate, frameSize, frameIndex int) (libopusOpusStateSnapshot, bool) {
	return cgowrap.CaptureOpusSilkEncoderStateBeforeFrame(samples, sampleRate, channels, bitrate, frameSize, frameIndex)
}

func captureLibopusOpusPitchXBufBeforeFrame(samples []float32, sampleRate, channels, bitrate, frameSize, frameIndex int) ([]float32, bool) {
	return cgowrap.CaptureOpusSilkPitchXBufBeforeFrame(samples, sampleRate, channels, bitrate, frameSize, frameIndex)
}

func captureLibopusOpusNSQStateBeforeFrame(samples []float32, sampleRate, channels, bitrate, frameSize, frameIndex int) (libopusOpusNSQStateSnapshot, bool) {
	return cgowrap.CaptureOpusSilkNSQStateBeforeFrame(samples, sampleRate, channels, bitrate, frameSize, frameIndex)
}

func captureLibopusOpusNSQInputsAtFrame(samples []float32, sampleRate, channels, bitrate, frameSize, frameIndex int) (libopusOpusNSQInputSnapshot, bool) {
	return cgowrap.CaptureOpusNSQInputsAtFrame(samples, sampleRate, channels, bitrate, frameSize, frameIndex)
}

func captureLibopusOpusNSQInputsAtFrameInt16(samplesInt16 []int16, sampleRate, channels, bitrate, frameSize, frameIndex int) (libopusOpusNSQInputSnapshot, bool) {
	return cgowrap.CaptureOpusNSQInputsAtFrameInt16(samplesInt16, sampleRate, channels, bitrate, frameSize, frameIndex)
}
