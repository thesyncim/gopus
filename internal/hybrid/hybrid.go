package hybrid

import (
	"gopus/internal/rangecoding"
)

// Decode decodes a Hybrid mono frame and returns 48kHz PCM samples.
//
// Parameters:
//   - data: raw Opus frame data (without TOC byte)
//   - frameSize: frame size in samples at 48kHz (480 for 10ms, 960 for 20ms)
//
// Returns float64 samples at 48kHz.
//
// Hybrid mode combines SILK (0-8kHz) and CELT (8-20kHz) for high-quality
// wideband speech at medium bitrates. Only 10ms and 20ms frames are supported.
func (d *Decoder) Decode(data []byte, frameSize int) ([]float64, error) {
	if len(data) == 0 {
		return nil, ErrDecodeFailed
	}

	if !ValidHybridFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}

	// Initialize range decoder
	var rd rangecoding.Decoder
	rd.Init(data)

	// Decode frame using shared range decoder
	return d.decodeFrame(&rd, frameSize, false)
}

// DecodeStereo decodes a Hybrid stereo frame and returns 48kHz PCM samples.
// Returns interleaved stereo samples [L0, R0, L1, R1, ...] at 48kHz.
//
// Parameters:
//   - data: raw Opus frame data (without TOC byte)
//   - frameSize: frame size in samples at 48kHz (480 for 10ms, 960 for 20ms)
//
// Returns interleaved float64 samples at 48kHz.
func (d *Decoder) DecodeStereo(data []byte, frameSize int) ([]float64, error) {
	if len(data) == 0 {
		return nil, ErrDecodeFailed
	}

	if !ValidHybridFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}

	if d.channels != 2 {
		// Stereo decoding requires a 2-channel decoder
		return nil, ErrDecodeFailed
	}

	// Initialize range decoder
	var rd rangecoding.Decoder
	rd.Init(data)

	// Decode stereo frame using shared range decoder
	return d.decodeFrame(&rd, frameSize, true)
}

// DecodeToInt16 decodes and converts to int16 PCM.
// This is a convenience wrapper for common audio output formats.
//
// Parameters:
//   - data: raw Opus frame data (without TOC byte)
//   - frameSize: frame size in samples at 48kHz (480 for 10ms, 960 for 20ms)
//
// Returns int16 samples at 48kHz in range [-32768, 32767].
func (d *Decoder) DecodeToInt16(data []byte, frameSize int) ([]int16, error) {
	samples, err := d.Decode(data, frameSize)
	if err != nil {
		return nil, err
	}

	return float64ToInt16(samples), nil
}

// DecodeStereoToInt16 decodes stereo and converts to int16 PCM.
// Returns interleaved stereo samples [L0, R0, L1, R1, ...] as int16.
func (d *Decoder) DecodeStereoToInt16(data []byte, frameSize int) ([]int16, error) {
	samples, err := d.DecodeStereo(data, frameSize)
	if err != nil {
		return nil, err
	}

	return float64ToInt16(samples), nil
}

// DecodeToFloat32 decodes and converts to float32 PCM.
// This is a convenience wrapper for audio APIs expecting float32.
//
// Parameters:
//   - data: raw Opus frame data (without TOC byte)
//   - frameSize: frame size in samples at 48kHz (480 for 10ms, 960 for 20ms)
//
// Returns float32 samples at 48kHz in approximate range [-1, 1].
func (d *Decoder) DecodeToFloat32(data []byte, frameSize int) ([]float32, error) {
	samples, err := d.Decode(data, frameSize)
	if err != nil {
		return nil, err
	}

	return float64ToFloat32(samples), nil
}

// DecodeStereoToFloat32 decodes stereo and converts to float32 PCM.
// Returns interleaved stereo samples [L0, R0, L1, R1, ...] as float32.
func (d *Decoder) DecodeStereoToFloat32(data []byte, frameSize int) ([]float32, error) {
	samples, err := d.DecodeStereo(data, frameSize)
	if err != nil {
		return nil, err
	}

	return float64ToFloat32(samples), nil
}

// DecodeWithDecoder decodes using a pre-initialized range decoder.
// This is useful when the range decoder state needs to be preserved or
// when decoding multiple frames from a single buffer.
//
// Parameters:
//   - rd: Pre-initialized range decoder
//   - frameSize: frame size in samples at 48kHz (480 for 10ms, 960 for 20ms)
//
// Returns float64 samples at 48kHz.
func (d *Decoder) DecodeWithDecoder(rd *rangecoding.Decoder, frameSize int) ([]float64, error) {
	return d.decodeFrame(rd, frameSize, false)
}

// DecodeStereoWithDecoder decodes stereo using a pre-initialized range decoder.
func (d *Decoder) DecodeStereoWithDecoder(rd *rangecoding.Decoder, frameSize int) ([]float64, error) {
	if d.channels != 2 {
		return nil, ErrDecodeFailed
	}
	return d.decodeFrame(rd, frameSize, true)
}

// float64ToInt16 converts float64 samples to int16.
// Clamps values to [-32768, 32767].
func float64ToInt16(samples []float64) []int16 {
	output := make([]int16, len(samples))
	for i, s := range samples {
		// Scale from float64 to int16 range
		// Assuming input is in roughly [-1, 1] but may exceed
		scaled := s * 32767.0
		if scaled > 32767 {
			scaled = 32767
		} else if scaled < -32768 {
			scaled = -32768
		}
		output[i] = int16(scaled)
	}
	return output
}

// float64ToFloat32 converts float64 samples to float32.
func float64ToFloat32(samples []float64) []float32 {
	output := make([]float32, len(samples))
	for i, s := range samples {
		output[i] = float32(s)
	}
	return output
}
