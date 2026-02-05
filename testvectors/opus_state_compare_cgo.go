//go:build cgo_libopus

package testvectors

import cgowrap "github.com/thesyncim/gopus/celt/cgo_test"

type libopusOpusStateSnapshot = cgowrap.OpusSilkEncoderStateSnapshot

func captureLibopusOpusSilkState(samples []float32, sampleRate, channels, bitrate, frameSize, frameIndex int) (libopusOpusStateSnapshot, bool) {
	return cgowrap.CaptureOpusSilkEncoderStateAtFrame(samples, sampleRate, channels, bitrate, frameSize, frameIndex)
}

func captureLibopusOpusSilkStateBeforeFrame(samples []float32, sampleRate, channels, bitrate, frameSize, frameIndex int) (libopusOpusStateSnapshot, bool) {
	return cgowrap.CaptureOpusSilkEncoderStateBeforeFrame(samples, sampleRate, channels, bitrate, frameSize, frameIndex)
}

func captureLibopusOpusPitchXBufBeforeFrame(samples []float32, sampleRate, channels, bitrate, frameSize, frameIndex int) ([]float32, bool) {
	return cgowrap.CaptureOpusSilkPitchXBufBeforeFrame(samples, sampleRate, channels, bitrate, frameSize, frameIndex)
}
