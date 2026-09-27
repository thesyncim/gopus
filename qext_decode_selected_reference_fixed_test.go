//go:build gopus_qext && gopus_fixed_point

package gopus

import "github.com/thesyncim/gopus/internal/libopustest"

func selectedQEXTDecodeSequenceReference(outputChannels, frameSize int, packets [][]byte) ([]float32, []uint32, error) {
	result, err := libopustest.ProbeQEXTDecodeFixed(libopustest.QEXTDecode96kParams{
		SampleFormat: libopustest.QEXTDecode96kFormatFloat32,
		Channels:     outputChannels,
		SampleRate:   48000,
		MaxFrameSize: frameSize,
		Packets:      packets,
	})
	return result.PCM, result.FinalRanges, err
}
