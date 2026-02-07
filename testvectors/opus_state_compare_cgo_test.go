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

func captureLibopusOpusSilkStateBeforeFrameWithBandwidth(samples []float32, sampleRate, channels, bitrate, bandwidth, frameSize, frameIndex int) (libopusOpusStateSnapshot, bool) {
	return cgowrap.CaptureOpusSilkEncoderStateBeforeFrameWithBandwidth(samples, sampleRate, channels, bitrate, bandwidth, frameSize, frameIndex)
}

func captureLibopusOpusPitchXBufBeforeFrame(samples []float32, sampleRate, channels, bitrate, frameSize, frameIndex int) ([]float32, bool) {
	return cgowrap.CaptureOpusSilkPitchXBufBeforeFrame(samples, sampleRate, channels, bitrate, frameSize, frameIndex)
}

func captureLibopusOpusPitchXBufBeforeFrameWithBandwidth(samples []float32, sampleRate, channels, bitrate, bandwidth, frameSize, frameIndex int) ([]float32, bool) {
	return cgowrap.CaptureOpusSilkPitchXBufBeforeFrameWithBandwidth(samples, sampleRate, channels, bitrate, bandwidth, frameSize, frameIndex)
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

// encodeWithLibopusFloat encodes the given float32 samples using libopus opus_encode_float
// and returns the encoded packets. Uses the given application type.
func encodeWithLibopusFloat(samples []float32, sampleRate, channels, bitrate, frameSize, application int) []libopusPacket {
	enc, err := cgowrap.NewLibopusEncoder(sampleRate, channels, application)
	if err != nil || enc == nil {
		return nil
	}
	defer enc.Destroy()
	enc.SetForceMode(cgowrap.ModeSilkOnly)
	enc.SetBitrate(bitrate)
	enc.SetBandwidth(cgowrap.OpusBandwidthWideband)

	samplesPerFrame := frameSize * channels
	numFrames := len(samples) / samplesPerFrame
	packets := make([]libopusPacket, 0, numFrames)
	for i := 0; i < numFrames; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		data, n := enc.EncodeFloat(samples[start:end], frameSize)
		if n <= 0 {
			continue
		}
		pkt := libopusPacket{
			data:       make([]byte, len(data)),
			finalRange: enc.GetFinalRange(),
		}
		copy(pkt.data, data)
		packets = append(packets, pkt)
	}
	return packets
}
