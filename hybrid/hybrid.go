package hybrid

import (
	"math"

	"github.com/thesyncim/gopus/plc"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
)

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
	// Handle PLC for nil/empty data (lost packet)
	if data == nil || len(data) == 0 {
		return d.decodePLC(frameSize, false)
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
	d.plcState.Reset()
	d.plcState.SetLastFrameParams(plc.ModeHybrid, frameSize, 1)

	return samples, nil
}

// DecodeWithPacketStereo decodes a Hybrid frame and honors the packet stereo flag.
// This is used when the output channels (decoder configuration) differ from the packet channels.
func (d *Decoder) DecodeWithPacketStereo(data []byte, frameSize int, packetStereo bool) ([]float64, error) {
	// Handle PLC for nil/empty data (lost packet)
	if data == nil || len(data) == 0 {
		return d.decodePLC(frameSize, d.channels == 2)
	}

	if !ValidHybridFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}

	// Initialize range decoder
	var rd rangecoding.Decoder
	rd.Init(data)

	// Decode frame using shared range decoder
	samples, err := d.decodeFrame(&rd, frameSize, packetStereo)
	if err != nil {
		return nil, err
	}

	// Reset PLC state after successful decode
	d.plcState.Reset()
	d.plcState.SetLastFrameParams(plc.ModeHybrid, frameSize, d.channels)

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
	// Handle PLC for nil/empty data (lost packet)
	if data == nil || len(data) == 0 {
		return d.decodePLC(frameSize, true)
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
	d.plcState.Reset()
	d.plcState.SetLastFrameParams(plc.ModeHybrid, frameSize, 2)

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

// DecodeToFloat32WithPacketStereo decodes with packet stereo flag and converts to float32.
func (d *Decoder) DecodeToFloat32WithPacketStereo(data []byte, frameSize int, packetStereo bool) ([]float32, error) {
	samples, err := d.DecodeWithPacketStereo(data, frameSize, packetStereo)
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

// DecodeWithDecoderHook decodes using a pre-initialized range decoder and an optional hook.
// The hook runs after SILK decode and before CELT decode, allowing Opus-layer parsing.
func (d *Decoder) DecodeWithDecoderHook(rd *rangecoding.Decoder, frameSize int, packetStereo bool, afterSilk func(*rangecoding.Decoder) error) ([]float64, error) {
	if rd == nil {
		return nil, ErrNilDecoder
	}
	if !ValidHybridFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}
	samples, err := d.decodeFrameWithHook(rd, frameSize, packetStereo, afterSilk)
	if err != nil {
		return nil, err
	}

	d.plcState.Reset()
	d.plcState.SetLastFrameParams(plc.ModeHybrid, frameSize, d.channels)

	return samples, nil
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
	if !ValidHybridFrameSize(frameSize) && frameSize != 120 && frameSize != 240 {
		return nil, ErrInvalidFrameSize
	}

	// Get fade factor for this loss
	fadeFactor := d.plcState.RecordLoss()

	// Total samples for output
	totalSamples := frameSize * d.channels

	// SILK PLC cannot produce less than 10ms; use 10ms and trim if needed.
	plcSilkFrameSize := frameSize
	if plcSilkFrameSize < 480 {
		plcSilkFrameSize = 480
	}

	// If fade is exhausted, return silence
	if fadeFactor < 0.001 {
		return make([]float64, totalSamples), nil
	}

	// Generate SILK PLC through the SILK decoder's native nil-packet path.
	// This keeps concealment cadence/state aligned with SILK-mode PLC.
	var silkUpsampled []float64
	if stereo {
		silkPCM, err := d.silkDecoder.DecodeStereo(nil, silk.BandwidthWideband, plcSilkFrameSize, false)
		if err != nil {
			return nil, err
		}
		silkUpsampled = make([]float64, len(silkPCM))
		for i := range silkPCM {
			silkUpsampled[i] = float64(silkPCM[i])
		}
	} else {
		silkPCM, err := d.silkDecoder.Decode(nil, silk.BandwidthWideband, plcSilkFrameSize, false)
		if err != nil {
			return nil, err
		}
		if d.channels == 2 {
			silkUpsampled = make([]float64, len(silkPCM)*2)
			for i := range silkPCM {
				val := float64(silkPCM[i])
				silkUpsampled[i*2] = val
				silkUpsampled[i*2+1] = val
			}
		} else {
			silkUpsampled = make([]float64, len(silkPCM))
			for i := range silkPCM {
				silkUpsampled[i] = float64(silkPCM[i])
			}
		}
	}
	if len(silkUpsampled) > totalSamples {
		silkUpsampled = silkUpsampled[:totalSamples]
	}

	// Keep PLC alignment consistent with normal hybrid decode.
	// The SILK decoder/resampler path already provides API-rate alignment.
	silkAligned := silkUpsampled

	// Generate CELT PLC (bands 17-21 only for hybrid)
	// For native hybrid frame sizes, use decoder-owned hybrid PLC cadence.
	celtScale := 1.0 / 32768.0
	var celtConcealed []float64
	if frameSize == 480 || frameSize == 960 {
		var err error
		celtConcealed, err = d.celtDecoder.DecodeHybridFECPLC(frameSize)
		if err != nil {
			return nil, err
		}
		celtScale = 1.0
	} else {
		// Fallback for non-hybrid frame sizes used by internal cadence paths.
		// Pass celtDecoder as both state and synthesizer (implements both interfaces).
		celtConcealed = plc.ConcealCELTHybrid(d.celtDecoder, d.celtDecoder, frameSize, fadeFactor)
	}

	// Combine SILK and CELT
	output := make([]float64, totalSamples)
	for i := 0; i < totalSamples; i++ {
		silkSample := float64(0)
		celtSample := float64(0)

		if i < len(silkAligned) {
			silkSample = silkAligned[i]
		}
		if i < len(celtConcealed) {
			celtSample = celtConcealed[i] * celtScale
		}

		output[i] = silkSample + celtSample
	}

	return output, nil
}
