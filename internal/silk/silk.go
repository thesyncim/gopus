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

	// Apply libopus-style sMid buffering per 20ms frame, then resample.
	config := GetBandwidthConfig(bandwidth)
	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return nil, err
	}
	fsKHz := config.SampleRate / 1000
	frameLength := nbSubfr * subFrameLengthMs * fsKHz
	if framesPerPacket > 0 && frameLength*framesPerPacket != len(nativeSamples) {
		// Fallback to slice-based length if something is off.
		frameLength = len(nativeSamples) / framesPerPacket
	}

	resampler := d.GetResampler(bandwidth)
	output := make([]float32, 0, frameSizeSamples)
	for f := 0; f < framesPerPacket; f++ {
		start := f * frameLength
		end := start + frameLength
		if start < 0 || end > len(nativeSamples) || frameLength == 0 {
			break
		}
		frame := nativeSamples[start:end]

		// Build resampler input: sMid[1] + frame[0:len-1]
		resamplerInput := make([]float32, len(frame))
		resamplerInput[0] = float32(d.stereo.sMid[1]) / 32768.0
		if len(frame) > 1 {
			copy(resamplerInput[1:], frame[:len(frame)-1])
			d.stereo.sMid[0] = float32ToInt16(frame[len(frame)-2])
			d.stereo.sMid[1] = float32ToInt16(frame[len(frame)-1])
		} else {
			d.stereo.sMid[0] = d.stereo.sMid[1]
			d.stereo.sMid[1] = float32ToInt16(frame[0])
		}

		output = append(output, resampler.Process(resamplerInput)...)
	}

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
	leftResampler := d.getResamplerForChannel(bandwidth, 0)
	rightResampler := d.getResamplerForChannel(bandwidth, 1)
	left := leftResampler.Process(leftNative)
	right := rightResampler.Process(rightNative)

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

// DecodeStereoToMono decodes a SILK stereo frame and returns mono 48kHz PCM samples.
// This matches libopus behavior when the decoder is configured for mono output.
func (d *Decoder) DecodeStereoToMono(
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
		return d.decodePLC(bandwidth, frameSizeSamples)
	}

	// Convert TOC frame size to duration
	duration := FrameDurationFromTOC(frameSizeSamples)

	// Initialize range decoder
	var rd rangecoding.Decoder
	rd.Init(data)

	// Decode mid channel at native rate (side channel decoded for state)
	midNative, frameLength, err := d.decodeStereoMidNative(&rd, bandwidth, duration, vadFlag)
	if err != nil {
		return nil, err
	}

	// Upsample to 48kHz using libopus-compatible resampler and sMid buffering
	framesPerPacket := 0
	if frameLength > 0 {
		framesPerPacket = len(midNative) / frameLength
	}
	resampler := d.getResamplerForChannel(bandwidth, 0)
	output := make([]float32, 0, frameSizeSamples)
	for f := 0; f < framesPerPacket; f++ {
		start := f * frameLength
		end := start + frameLength
		if start < 0 || end > len(midNative) || frameLength == 0 {
			break
		}
		frame := midNative[start:end]

		resamplerInput := make([]float32, frameLength)
		resamplerInput[0] = float32(d.stereo.sMid[1]) / 32768.0
		if frameLength > 1 {
			for i := 0; i < frameLength-1; i++ {
				resamplerInput[i+1] = float32(frame[i]) / 32768.0
			}
			d.stereo.sMid[0] = frame[frameLength-2]
			d.stereo.sMid[1] = frame[frameLength-1]
		} else {
			d.stereo.sMid[0] = d.stereo.sMid[1]
			d.stereo.sMid[1] = frame[0]
		}

		output = append(output, resampler.Process(resamplerInput)...)
	}

	// Reset PLC state after successful decode
	plcState.Reset()
	plcState.SetLastFrameParams(plc.ModeSILK, frameSizeSamples, 1)

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
	leftResampler := d.getResamplerForChannel(bandwidth, 0)
	rightResampler := d.getResamplerForChannel(bandwidth, 1)
	leftUp := leftResampler.Process(left)
	rightUp := rightResampler.Process(right)

	// Interleave [L0, R0, L1, R1, ...]
	output := make([]float32, len(leftUp)*2)
	for i := range leftUp {
		output[i*2] = leftUp[i]
		output[i*2+1] = rightUp[i]
	}

	return output, nil
}

func float32ToInt16(v float32) int16 {
	scaled := float64(v) * 32768.0
	if scaled > 32767.0 {
		return 32767
	}
	if scaled < -32768.0 {
		return -32768
	}
	return int16(math.RoundToEven(scaled))
}

// PLCState returns the PLC state for external access (e.g., hybrid mode).
func PLCState() *plc.State {
	return plcState
}
