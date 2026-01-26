package hybrid

import (
	"math"

	"github.com/thesyncim/gopus/internal/plc"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// hybridPLCState tracks packet loss concealment state for Hybrid decoding.
var hybridPLCState = plc.NewState()

// Decode decodes a Hybrid mono frame and returns 48kHz PCM samples.
// If data is nil, performs Packet Loss Concealment (PLC) instead of decoding.
//
// Parameters:
//   - data: raw Opus frame data (without TOC byte), or nil for PLC
//   - frameSize: frame size in samples at 48kHz (480 for 10ms, 960 for 20ms)
//
// Returns float64 samples at 48kHz.
//
// Hybrid mode combines SILK (0-8kHz) and CELT (8-20kHz) for high-quality
// wideband speech at medium bitrates. Only 10ms and 20ms frames are supported.
func (d *Decoder) Decode(data []byte, frameSize int) ([]float64, error) {
	// Handle PLC for nil data (lost packet)
	if data == nil {
		return d.decodePLC(frameSize, false)
	}

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
	samples, err := d.decodeFrame(&rd, frameSize, false)
	if err != nil {
		return nil, err
	}

	// Reset PLC state after successful decode
	hybridPLCState.Reset()
	hybridPLCState.SetLastFrameParams(plc.ModeHybrid, frameSize, 1)

	return samples, nil
}

// DecodeStereo decodes a Hybrid stereo frame and returns 48kHz PCM samples.
// If data is nil, performs Packet Loss Concealment (PLC) instead of decoding.
// Returns interleaved stereo samples [L0, R0, L1, R1, ...] at 48kHz.
//
// Parameters:
//   - data: raw Opus frame data (without TOC byte), or nil for PLC
//   - frameSize: frame size in samples at 48kHz (480 for 10ms, 960 for 20ms)
//
// Returns interleaved float64 samples at 48kHz.
func (d *Decoder) DecodeStereo(data []byte, frameSize int) ([]float64, error) {
	// Handle PLC for nil data (lost packet)
	if data == nil {
		return d.decodePLC(frameSize, true)
	}

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
	samples, err := d.decodeFrame(&rd, frameSize, true)
	if err != nil {
		return nil, err
	}

	// Reset PLC state after successful decode
	hybridPLCState.Reset()
	hybridPLCState.SetLastFrameParams(plc.ModeHybrid, frameSize, 2)

	return samples, nil
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
		scaled := s * 32768.0
		if scaled > 32767.0 {
			output[i] = 32767
			continue
		}
		if scaled < -32768.0 {
			output[i] = -32768
			continue
		}
		output[i] = int16(math.RoundToEven(scaled))
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

// decodePLC generates concealment audio for a lost Hybrid packet.
// Coordinates both SILK PLC and CELT PLC for the full hybrid output.
func (d *Decoder) decodePLC(frameSize int, stereo bool) ([]float64, error) {
	if !ValidHybridFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}

	// Get fade factor for this loss
	fadeFactor := hybridPLCState.RecordLoss()

	// Total samples for output
	totalSamples := frameSize * d.channels

	// If fade is exhausted, return silence
	if fadeFactor < 0.001 {
		return make([]float64, totalSamples), nil
	}

	// Generate SILK PLC at 16kHz (WB)
	silkSamples := frameSize / 3 // 48kHz -> 16kHz
	var silkConcealed []float32
	if stereo {
		left, right := plc.ConcealSILKStereo(d.silkDecoder, silkSamples, fadeFactor)
		// Interleave
		silkConcealed = make([]float32, silkSamples*2)
		for i := range left {
			silkConcealed[i*2] = left[i]
			silkConcealed[i*2+1] = right[i]
		}
	} else {
		silkConcealed = plc.ConcealSILK(d.silkDecoder, silkSamples, fadeFactor)
	}

	// Upsample SILK to 48kHz (3x)
	var silkUpsampled []float64
	if stereo {
		// Deinterleave, upsample, reinterleave
		silkL := make([]float32, silkSamples)
		silkR := make([]float32, silkSamples)
		for i := 0; i < silkSamples; i++ {
			silkL[i] = silkConcealed[i*2]
			silkR[i] = silkConcealed[i*2+1]
		}
		upL := upsample3x(silkL)
		upR := upsample3x(silkR)
		silkUpsampled = make([]float64, len(upL)*2)
		for i := range upL {
			silkUpsampled[i*2] = upL[i]
			silkUpsampled[i*2+1] = upR[i]
		}
	} else {
		silkUpsampled = upsample3x(silkConcealed)
	}

	// Apply delay compensation to SILK
	var silkDelayed []float64
	if stereo {
		silkDelayed = d.applyDelayStereo(silkUpsampled)
	} else {
		silkDelayed = d.applyDelayMono(silkUpsampled)
	}

	// Generate CELT PLC (bands 17-21 only for hybrid)
	// Pass celtDecoder as both state and synthesizer (implements both interfaces)
	celtConcealed := plc.ConcealCELTHybrid(d.celtDecoder, d.celtDecoder, frameSize, fadeFactor)

	// Combine SILK and CELT
	output := make([]float64, totalSamples)
	for i := 0; i < totalSamples; i++ {
		silkSample := float64(0)
		celtSample := float64(0)

		if i < len(silkDelayed) {
			silkSample = silkDelayed[i]
		}
		if i < len(celtConcealed) {
			celtSample = celtConcealed[i]
		}

		output[i] = silkSample + celtSample
	}

	return output, nil
}

// HybridPLCState returns the PLC state for external access.
func HybridPLCState() *plc.State {
	return hybridPLCState
}
