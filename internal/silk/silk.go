package silk

import (
	"errors"

	"github.com/thesyncim/gopus/internal/plc"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// Errors for SILK decoding
var (
	ErrInvalidBandwidth = errors.New("silk: invalid bandwidth for SILK mode")
	ErrDecodeFailed     = errors.New("silk: frame decode failed")
)

// plcState tracks packet loss concealment state for the decoder.
// Managed internally to provide seamless PLC when data is nil.
var plcState = plc.NewState()

// Decode decodes a SILK mono frame and returns 48kHz PCM samples.
// If data is nil, performs Packet Loss Concealment (PLC) instead of decoding.
//
// Parameters:
//   - data: raw SILK frame data (without TOC byte), or nil for PLC
//   - bandwidth: NB, MB, or WB (from TOC)
//   - frameSizeSamples: frame size in samples at 48kHz (from TOC)
//   - vadFlag: voice activity flag (from bitstream header)
//
// Returns float32 samples in range [-1, 1] at 48kHz.
func (d *Decoder) Decode(
	data []byte,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
) ([]float32, error) {
	// Validate bandwidth is SILK-compatible (NB, MB, WB only)
	if bandwidth > BandwidthWideband {
		return nil, ErrInvalidBandwidth
	}

	// Handle PLC for nil data (lost packet)
	if data == nil {
		return d.decodePLC(bandwidth, frameSizeSamples)
	}

	// Convert TOC frame size to duration
	duration := FrameDurationFromTOC(frameSizeSamples)

	// Initialize range decoder
	var rd rangecoding.Decoder
	rd.Init(data)

	// Decode frame at native rate
	nativeSamples, err := d.DecodeFrame(&rd, bandwidth, duration, vadFlag)
	if err != nil {
		return nil, err
	}

	// Upsample to 48kHz
	config := GetBandwidthConfig(bandwidth)
	output := upsampleTo48k(nativeSamples, config.SampleRate)

	// Reset PLC state after successful decode
	plcState.Reset()
	plcState.SetLastFrameParams(plc.ModeSILK, frameSizeSamples, 1)

	return output, nil
}

// DecodeStereo decodes a SILK stereo frame and returns 48kHz PCM samples.
// If data is nil, performs Packet Loss Concealment (PLC) instead of decoding.
//
// Returns interleaved stereo samples [L0, R0, L1, R1, ...] at 48kHz.
func (d *Decoder) DecodeStereo(
	data []byte,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
) ([]float32, error) {
	// Validate bandwidth is SILK-compatible
	if bandwidth > BandwidthWideband {
		return nil, ErrInvalidBandwidth
	}

	// Handle PLC for nil data (lost packet)
	if data == nil {
		return d.decodePLCStereo(bandwidth, frameSizeSamples)
	}

	// Convert TOC frame size to duration
	duration := FrameDurationFromTOC(frameSizeSamples)

	// Initialize range decoder
	var rd rangecoding.Decoder
	rd.Init(data)

	// Decode stereo at native rate
	leftNative, rightNative, err := d.DecodeStereoFrame(&rd, bandwidth, duration, vadFlag)
	if err != nil {
		return nil, err
	}

	// Upsample to 48kHz
	config := GetBandwidthConfig(bandwidth)
	left := upsampleTo48k(leftNative, config.SampleRate)
	right := upsampleTo48k(rightNative, config.SampleRate)

	// Interleave samples [L0, R0, L1, R1, ...]
	output := make([]float32, len(left)*2)
	for i := range left {
		output[i*2] = left[i]
		output[i*2+1] = right[i]
	}

	// Reset PLC state after successful decode
	plcState.Reset()
	plcState.SetLastFrameParams(plc.ModeSILK, frameSizeSamples, 2)

	return output, nil
}

// DecodeToInt16 decodes and converts to int16 PCM.
// This is a convenience wrapper for common audio output formats.
func (d *Decoder) DecodeToInt16(
	data []byte,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
) ([]int16, error) {
	samples, err := d.Decode(data, bandwidth, frameSizeSamples, vadFlag)
	if err != nil {
		return nil, err
	}

	output := make([]int16, len(samples))
	for i, s := range samples {
		// Convert float32 [-1, 1] to int16 [-32768, 32767]
		scaled := s * 32767.0
		if scaled > 32767 {
			scaled = 32767
		} else if scaled < -32768 {
			scaled = -32768
		}
		output[i] = int16(scaled)
	}

	return output, nil
}

// DecodeStereoToInt16 decodes stereo and converts to int16 PCM.
// Returns interleaved stereo samples [L0, R0, L1, R1, ...] as int16.
func (d *Decoder) DecodeStereoToInt16(
	data []byte,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
) ([]int16, error) {
	samples, err := d.DecodeStereo(data, bandwidth, frameSizeSamples, vadFlag)
	if err != nil {
		return nil, err
	}

	output := make([]int16, len(samples))
	for i, s := range samples {
		scaled := s * 32767.0
		if scaled > 32767 {
			scaled = 32767
		} else if scaled < -32768 {
			scaled = -32768
		}
		output[i] = int16(scaled)
	}

	return output, nil
}

// BandwidthFromOpus converts Opus bandwidth to SILK bandwidth.
// SILK only supports NB, MB, WB. SWB and FB use Hybrid mode.
//
// Returns the SILK bandwidth and true if valid, or (0, false) for SWB/FB.
func BandwidthFromOpus(opusBandwidth int) (Bandwidth, bool) {
	switch opusBandwidth {
	case 0: // Narrowband
		return BandwidthNarrowband, true
	case 1: // Mediumband
		return BandwidthMediumband, true
	case 2: // Wideband
		return BandwidthWideband, true
	default:
		return 0, false // SWB/FB not supported in SILK-only mode
	}
}

// decodePLC generates concealment audio for a lost mono packet.
func (d *Decoder) decodePLC(bandwidth Bandwidth, frameSizeSamples int) ([]float32, error) {
	// Get fade factor for this loss
	fadeFactor := plcState.RecordLoss()

	// Get native sample count from 48kHz frame size
	config := GetBandwidthConfig(bandwidth)
	nativeSamples := frameSizeSamples * config.SampleRate / 48000

	// Generate concealment at native rate
	concealed := plc.ConcealSILK(d, nativeSamples, fadeFactor)

	// Upsample to 48kHz
	output := upsampleTo48k(concealed, config.SampleRate)

	return output, nil
}

// decodePLCStereo generates concealment audio for a lost stereo packet.
func (d *Decoder) decodePLCStereo(bandwidth Bandwidth, frameSizeSamples int) ([]float32, error) {
	// Get fade factor for this loss
	fadeFactor := plcState.RecordLoss()

	// Get native sample count from 48kHz frame size
	config := GetBandwidthConfig(bandwidth)
	nativeSamples := frameSizeSamples * config.SampleRate / 48000

	// Generate concealment at native rate for both channels
	left, right := plc.ConcealSILKStereo(d, nativeSamples, fadeFactor)

	// Upsample to 48kHz
	leftUp := upsampleTo48k(left, config.SampleRate)
	rightUp := upsampleTo48k(right, config.SampleRate)

	// Interleave [L0, R0, L1, R1, ...]
	output := make([]float32, len(leftUp)*2)
	for i := range leftUp {
		output[i*2] = leftUp[i]
		output[i*2+1] = rightUp[i]
	}

	return output, nil
}

// PLCState returns the PLC state for external access (e.g., hybrid mode).
func PLCState() *plc.State {
	return plcState
}
