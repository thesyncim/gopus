package silk

import (
	"errors"
	"math"

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

	// Apply sMid buffering to match libopus timing exactly
	// libopus prepends sMid[0:2] to decoded samples, then resamples from index 1
	// This effectively delays the output by 1 sample and provides filter continuity
	n := len(nativeSamples)
	if n > 0 {
		// Build resampler input: sMid[1] + decoded[0:n-1]
		// (last decoded sample goes to sMid for next frame)
		resamplerInput := make([]float32, n)
		resamplerInput[0] = d.sMid[1]
		copy(resamplerInput[1:], nativeSamples[:n-1])

		// Save last 2 samples to sMid for next frame
		if n >= 2 {
			d.sMid[0] = nativeSamples[n-2]
			d.sMid[1] = nativeSamples[n-1]
		} else {
			d.sMid[0] = d.sMid[1]
			d.sMid[1] = nativeSamples[n-1]
		}

		nativeSamples = resamplerInput
	}

	// Upsample to 48kHz using libopus-compatible resampler
	resampler := d.GetResampler(bandwidth)
	output := resampler.Process(nativeSamples)

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

	// Upsample to 48kHz using libopus-compatible resampler
	resampler := d.GetResampler(bandwidth)
	left := resampler.Process(leftNative)
	// Note: For true stereo we'd need separate resamplers per channel
	// but the current implementation processes channels identically
	right := resampler.Process(rightNative)

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
		scaled := float64(s) * 32768.0
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
		scaled := float64(s) * 32768.0
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

	// Upsample to 48kHz using libopus-compatible resampler
	resampler := d.GetResampler(bandwidth)
	output := resampler.Process(concealed)

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

	// Upsample to 48kHz using libopus-compatible resampler
	resampler := d.GetResampler(bandwidth)
	leftUp := resampler.Process(left)
	rightUp := resampler.Process(right)

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
