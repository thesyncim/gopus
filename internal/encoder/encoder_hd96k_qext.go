//go:build gopus_qext

package encoder

import "github.com/thesyncim/gopus/internal/celt"

const hd96kQEXTPacketSizeCap = 3825

func configureCELTEncoderForSampleRate(enc *celt.Encoder, sampleRate int32) {
	if sampleRate == 96000 {
		enc.EnableHD96kMode()
		enc.SetTopLevelDelayCompensatedInput(true)
	}
}

// EncodeNativeHD96k sends native-rate PCM through the shared Opus frame driver.
// libopus selects SILK, Hybrid or CELT at the requested bitrate and carries one
// continuous history across those modes.
func (e *Encoder) EncodeNativeHD96k(pcm []float32, frameSize int, dst []byte) (int, error) {
	if e.sampleRate != 96000 {
		return 0, ErrInvalidConfig
	}
	if frameSize <= 0 || len(pcm) != frameSize*int(e.channels) {
		return 0, ErrInvalidFrameSize
	}
	if len(dst) == 0 {
		return 0, ErrInvalidConfig
	}
	packet, err := e.EncodeFloat32WithAnalysisMaxBytes(pcm, frameSize, pcm, len(dst))
	if err != nil {
		return 0, err
	}
	return copy(dst, packet), nil
}
