package silk

import (
	"errors"
	"math"

	"github.com/thesyncim/gopus/plc"
	"github.com/thesyncim/gopus/rangecoding"
)

// Errors for SILK decoding
var (
	ErrInvalidBandwidth = errors.New("silk: invalid bandwidth for SILK mode")
	ErrDecodeFailed     = errors.New("silk: frame decode failed")
)

func (d *Decoder) finalizeSuccessfulDecode(frameSizeSamples, channels int) {
	d.plcState.Reset()
	d.plcState.SetLastFrameParams(plc.ModeSILK, frameSizeSamples, channels)
}

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

	// Handle bandwidth changes - reset sMid state when sample rate changes
	d.handleBandwidthChange(bandwidth)

	// Handle PLC for nil data (lost packet)
	if data == nil {
		return d.decodePLC(bandwidth, frameSizeSamples)
	}

	// Convert TOC frame size to duration
	duration := FrameDurationFromTOC(frameSizeSamples)

	// Initialize range decoder
	var rd rangecoding.Decoder
	rd.Init(data)

	// Decode frame at native rate (without delay compensation, since we'll handle
	// sMid buffering in BuildMonoResamplerInput before resampling to 48kHz)
	nativeSamples, err := d.DecodeFrameRaw(&rd, bandwidth, duration, vadFlag)
	if err != nil {
		return nil, err
	}

	// Check for bandwidth change and reset sMid state if needed.
	// This is necessary because sMid contains samples at the previous bandwidth's rate.
	d.HandleBandwidthChange(bandwidth)

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

		resamplerInput := d.BuildMonoResamplerInput(frame)
		output = append(output, resampler.Process(resamplerInput)...)
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 1)

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

	// Handle bandwidth changes - reset sMid state when sample rate changes
	d.handleBandwidthChange(bandwidth)

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
	leftResampler := d.GetResamplerForChannel(bandwidth, 0)
	rightResampler := d.GetResamplerForChannel(bandwidth, 1)
	left := leftResampler.Process(leftNative)
	right := rightResampler.Process(rightNative)

	// Interleave samples [L0, R0, L1, R1, ...]
	output := make([]float32, len(left)*2)
	for i := range left {
		output[i*2] = left[i]
		output[i*2+1] = right[i]
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 2)

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

	// Handle bandwidth changes - reset sMid state when sample rate changes
	d.handleBandwidthChange(bandwidth)

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
	resampler := d.GetResamplerForChannel(bandwidth, 0)
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
		}
		d.updateMonoHistoryFromInt16(frame)

		output = append(output, resampler.Process(resamplerInput)...)
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 1)

	return output, nil
}

// DecodeMonoToStereo decodes a mono SILK frame and returns stereo 48kHz PCM samples.
// When stereoToMono is true (stereo -> mono transition), the right channel is
// resampled using its own resampler state instead of simple duplication to
// match libopus behavior.
func (d *Decoder) DecodeMonoToStereo(
	data []byte,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
	stereoToMono bool,
) ([]float32, error) {
	if bandwidth > BandwidthWideband {
		return nil, ErrInvalidBandwidth
	}
	useStereoHistory := d.ShouldUseStereoToMonoHistory(bandwidth, stereoToMono)

	// Handle bandwidth changes - reset sMid state when sample rate changes
	d.handleBandwidthChange(bandwidth)

	if data == nil {
		return d.decodePLCStereo(bandwidth, frameSizeSamples)
	}

	duration := FrameDurationFromTOC(frameSizeSamples)

	var rd rangecoding.Decoder
	rd.Init(data)

	// Decode at native rate without delay compensation (sMid buffering happens before resampler)
	nativeSamples, err := d.DecodeFrameRaw(&rd, bandwidth, duration, vadFlag)
	if err != nil {
		return nil, err
	}

	// Check for bandwidth change and reset sMid state if needed.
	d.HandleBandwidthChange(bandwidth)

	config := GetBandwidthConfig(bandwidth)
	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return nil, err
	}
	fsKHz := config.SampleRate / 1000
	frameLength := nbSubfr * subFrameLengthMs * fsKHz
	if framesPerPacket > 0 && frameLength*framesPerPacket != len(nativeSamples) {
		frameLength = len(nativeSamples) / framesPerPacket
	}

	leftResampler := d.GetResamplerForChannel(bandwidth, 0)
	rightResampler := d.GetResamplerForChannel(bandwidth, 1)

	leftOut := make([]float32, 0, frameSizeSamples)
	var rightOut []float32
	if useStereoHistory {
		rightOut = make([]float32, 0, frameSizeSamples)
	}

	for f := 0; f < framesPerPacket; f++ {
		start := f * frameLength
		end := start + frameLength
		if start < 0 || end > len(nativeSamples) || frameLength == 0 {
			break
		}
		frame := nativeSamples[start:end]
		resamplerInput := d.BuildMonoResamplerInput(frame)
		left := leftResampler.Process(resamplerInput)
		leftOut = append(leftOut, left...)
		if useStereoHistory {
			right := rightResampler.Process(resamplerInput)
			rightOut = append(rightOut, right...)
		}
	}

	out := make([]float32, len(leftOut)*2)
	for i := range leftOut {
		out[i*2] = leftOut[i]
		if useStereoHistory {
			if i < len(rightOut) {
				out[i*2+1] = rightOut[i]
			} else {
				out[i*2+1] = leftOut[i]
			}
		} else {
			out[i*2+1] = leftOut[i]
		}
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 2)

	return out, nil
}

// DecodeWithDecoder decodes a SILK mono frame using a pre-initialized range decoder.
// This mirrors Decode() but avoids re-initializing the range decoder.
func (d *Decoder) DecodeWithDecoder(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
) ([]float32, error) {
	if bandwidth > BandwidthWideband {
		return nil, ErrInvalidBandwidth
	}
	if rd == nil {
		return nil, ErrDecodeFailed
	}

	// Handle bandwidth changes - reset sMid state when sample rate changes
	d.handleBandwidthChange(bandwidth)

	duration := FrameDurationFromTOC(frameSizeSamples)

	// Decode at native rate without delay compensation (sMid buffering happens before resampler)
	nativeSamples, err := d.DecodeFrameRaw(rd, bandwidth, duration, vadFlag)
	if err != nil {
		return nil, err
	}

	// Check for bandwidth change and reset sMid state if needed.
	d.HandleBandwidthChange(bandwidth)

	config := GetBandwidthConfig(bandwidth)
	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return nil, err
	}
	fsKHz := config.SampleRate / 1000
	frameLength := nbSubfr * subFrameLengthMs * fsKHz
	if framesPerPacket > 0 && frameLength*framesPerPacket != len(nativeSamples) {
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
		resamplerInput := d.BuildMonoResamplerInput(frame)
		processOutput := resampler.Process(resamplerInput)
		output = append(output, processOutput...)
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 1)

	return output, nil
}

// DecodeWithDecoderInto decodes a SILK mono frame into a caller-provided buffer.
// This is the zero-allocation version of DecodeWithDecoder.
// Returns the number of samples written to the output buffer.
func (d *Decoder) DecodeWithDecoderInto(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
	output []float32,
) (int, error) {
	if bandwidth > BandwidthWideband {
		return 0, ErrInvalidBandwidth
	}
	if rd == nil {
		return 0, ErrDecodeFailed
	}

	// Handle bandwidth changes - reset sMid state when sample rate changes
	d.handleBandwidthChange(bandwidth)

	duration := FrameDurationFromTOC(frameSizeSamples)

	// Decode at native rate without delay compensation (sMid buffering happens before resampler).
	// Use int16-native path to avoid float32->int16 reconversion before resampling.
	nativeSamples, err := d.decodeFrameRawInt16(rd, bandwidth, duration, vadFlag)
	if err != nil {
		return 0, err
	}

	// Check for bandwidth change and reset sMid state if needed.
	d.HandleBandwidthChange(bandwidth)

	config := GetBandwidthConfig(bandwidth)
	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return 0, err
	}
	fsKHz := config.SampleRate / 1000
	frameLength := nbSubfr * subFrameLengthMs * fsKHz
	if framesPerPacket > 0 && frameLength*framesPerPacket != len(nativeSamples) {
		frameLength = len(nativeSamples) / framesPerPacket
	}

	resampler := d.GetResampler(bandwidth)
	outputOffset := 0
	for f := 0; f < framesPerPacket; f++ {
		start := f * frameLength
		end := start + frameLength
		if start < 0 || end > len(nativeSamples) || frameLength == 0 {
			break
		}
		frame := nativeSamples[start:end]
		resamplerInput := d.BuildMonoResamplerInputInt16(frame)
		n := resampler.ProcessInt16Into(resamplerInput, output[outputOffset:])
		outputOffset += n
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 1)

	return outputOffset, nil
}

// DecodeStereoWithDecoderInto decodes a SILK stereo frame into a caller-owned
// interleaved stereo buffer.
func (d *Decoder) DecodeStereoWithDecoderInto(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
	output []float32,
) (int, error) {
	if bandwidth > BandwidthWideband {
		return 0, ErrInvalidBandwidth
	}
	if rd == nil {
		return 0, ErrDecodeFailed
	}
	if len(output) < frameSizeSamples*2 {
		return 0, ErrDecodeFailed
	}

	d.handleBandwidthChange(bandwidth)

	duration := FrameDurationFromTOC(frameSizeSamples)
	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return 0, err
	}
	config := GetBandwidthConfig(bandwidth)
	nativeSamples := framesPerPacket * nbSubfr * subFrameLengthMs * config.SampleRate / 1000
	leftNative, rightNative, ok := d.GetStereoInt16Scratch(nativeSamples)
	if !ok {
		return 0, ErrDecodeFailed
	}

	nativeSamples, err = d.DecodeStereoFrameInt16Into(rd, bandwidth, duration, vadFlag, leftNative, rightNative)
	if err != nil {
		return 0, err
	}
	leftResampler := d.GetResamplerForChannel(bandwidth, 0)
	rightResampler := d.GetResamplerForChannel(bandwidth, 1)
	leftScratch, rightScratch, ok := d.stereoFloat32Scratch(frameSizeSamples)
	if !ok {
		return 0, ErrDecodeFailed
	}

	nLeft := leftResampler.ProcessInt16Into(leftNative[:nativeSamples], leftScratch)
	nRight := rightResampler.ProcessInt16Into(rightNative[:nativeSamples], rightScratch)
	n := nLeft
	if nRight < n {
		n = nRight
	}
	if n < 0 || n*2 > len(output) {
		return 0, ErrDecodeFailed
	}
	for i := 0; i < n; i++ {
		output[i*2] = leftScratch[i]
		output[i*2+1] = rightScratch[i]
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 2)
	return n, nil
}

// DecodeStereoWithDecoder decodes a SILK stereo frame using a pre-initialized range decoder.
func (d *Decoder) DecodeStereoWithDecoder(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
) ([]float32, error) {
	if bandwidth > BandwidthWideband {
		return nil, ErrInvalidBandwidth
	}
	if rd == nil {
		return nil, ErrDecodeFailed
	}

	// Handle bandwidth changes - reset sMid state when sample rate changes
	d.handleBandwidthChange(bandwidth)

	duration := FrameDurationFromTOC(frameSizeSamples)

	leftNative, rightNative, err := d.DecodeStereoFrame(rd, bandwidth, duration, vadFlag)
	if err != nil {
		return nil, err
	}

	leftResampler := d.GetResamplerForChannel(bandwidth, 0)
	rightResampler := d.GetResamplerForChannel(bandwidth, 1)
	left := leftResampler.Process(leftNative)
	right := rightResampler.Process(rightNative)

	output := make([]float32, len(left)*2)
	for i := range left {
		output[i*2] = left[i]
		output[i*2+1] = right[i]
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 2)

	return output, nil
}

// DecodeStereoToMonoWithDecoder decodes a SILK stereo frame to mono using a pre-initialized range decoder.
func (d *Decoder) DecodeStereoToMonoWithDecoder(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
) ([]float32, error) {
	// Handle bandwidth changes - reset sMid state when sample rate changes
	d.handleBandwidthChange(bandwidth)

	if bandwidth > BandwidthWideband {
		return nil, ErrInvalidBandwidth
	}
	if rd == nil {
		return nil, ErrDecodeFailed
	}

	duration := FrameDurationFromTOC(frameSizeSamples)

	midNative, frameLength, err := d.decodeStereoMidNative(rd, bandwidth, duration, vadFlag)
	if err != nil {
		return nil, err
	}

	framesPerPacket := 0
	if frameLength > 0 {
		framesPerPacket = len(midNative) / frameLength
	}
	resampler := d.GetResamplerForChannel(bandwidth, 0)
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
		}
		d.updateMonoHistoryFromInt16(frame)

		output = append(output, resampler.Process(resamplerInput)...)
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 1)

	return output, nil
}

// DecodeMonoToStereoWithDecoder decodes a mono SILK frame to stereo using a pre-initialized range decoder.
// stereoToMono mirrors libopus behavior for stereo->mono transitions.
func (d *Decoder) DecodeMonoToStereoWithDecoder(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
	stereoToMono bool,
) ([]float32, error) {
	if bandwidth > BandwidthWideband {
		return nil, ErrInvalidBandwidth
	}
	if rd == nil {
		return nil, ErrDecodeFailed
	}
	useStereoHistory := d.ShouldUseStereoToMonoHistory(bandwidth, stereoToMono)

	// Handle bandwidth changes - reset sMid state when sample rate changes
	d.handleBandwidthChange(bandwidth)

	duration := FrameDurationFromTOC(frameSizeSamples)

	// Decode at native rate without delay compensation (sMid buffering happens before resampler)
	nativeSamples, err := d.DecodeFrameRaw(rd, bandwidth, duration, vadFlag)
	if err != nil {
		return nil, err
	}

	// Check for bandwidth change and reset sMid state if needed.
	d.HandleBandwidthChange(bandwidth)

	config := GetBandwidthConfig(bandwidth)
	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return nil, err
	}
	fsKHz := config.SampleRate / 1000
	frameLength := nbSubfr * subFrameLengthMs * fsKHz
	if framesPerPacket > 0 && frameLength*framesPerPacket != len(nativeSamples) {
		frameLength = len(nativeSamples) / framesPerPacket
	}

	leftResampler := d.GetResamplerForChannel(bandwidth, 0)
	rightResampler := d.GetResamplerForChannel(bandwidth, 1)

	leftOut := make([]float32, 0, frameSizeSamples)
	var rightOut []float32
	if useStereoHistory {
		rightOut = make([]float32, 0, frameSizeSamples)
	}

	for f := 0; f < framesPerPacket; f++ {
		start := f * frameLength
		end := start + frameLength
		if start < 0 || end > len(nativeSamples) || frameLength == 0 {
			break
		}
		frame := nativeSamples[start:end]
		resamplerInput := d.BuildMonoResamplerInput(frame)
		left := leftResampler.Process(resamplerInput)
		leftOut = append(leftOut, left...)
		if useStereoHistory {
			right := rightResampler.Process(resamplerInput)
			rightOut = append(rightOut, right...)
		}
	}

	out := make([]float32, len(leftOut)*2)
	for i := range leftOut {
		out[i*2] = leftOut[i]
		if useStereoHistory {
			if i < len(rightOut) {
				out[i*2+1] = rightOut[i]
			} else {
				out[i*2+1] = leftOut[i]
			}
		} else {
			out[i*2+1] = leftOut[i]
		}
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 2)

	return out, nil
}

// DecodeMonoToStereoWithDecoderInto decodes a mono SILK frame into a
// caller-owned interleaved stereo buffer.
func (d *Decoder) DecodeMonoToStereoWithDecoderInto(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
	stereoToMono bool,
	output []float32,
) (int, error) {
	if bandwidth > BandwidthWideband {
		return 0, ErrInvalidBandwidth
	}
	if rd == nil {
		return 0, ErrDecodeFailed
	}
	if len(output) < frameSizeSamples*2 {
		return 0, ErrDecodeFailed
	}
	useStereoHistory := d.ShouldUseStereoToMonoHistory(bandwidth, stereoToMono)

	d.handleBandwidthChange(bandwidth)

	duration := FrameDurationFromTOC(frameSizeSamples)
	nativeSamples, err := d.decodeFrameRawInt16(rd, bandwidth, duration, vadFlag)
	if err != nil {
		return 0, err
	}

	d.HandleBandwidthChange(bandwidth)

	config := GetBandwidthConfig(bandwidth)
	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return 0, err
	}
	fsKHz := config.SampleRate / 1000
	frameLength := nbSubfr * subFrameLengthMs * fsKHz
	if framesPerPacket > 0 && frameLength*framesPerPacket != len(nativeSamples) {
		frameLength = len(nativeSamples) / framesPerPacket
	}
	if frameLength <= 0 {
		return 0, ErrDecodeFailed
	}

	leftResampler := d.GetResamplerForChannel(bandwidth, 0)
	rightResampler := d.GetResamplerForChannel(bandwidth, 1)
	leftScratch, rightScratch, ok := d.stereoFloat32Scratch(frameSizeSamples)
	if !ok {
		return 0, ErrDecodeFailed
	}

	outputOffset := 0
	for f := 0; f < framesPerPacket; f++ {
		start := f * frameLength
		end := start + frameLength
		if start < 0 || end > len(nativeSamples) {
			return 0, ErrDecodeFailed
		}
		resamplerInput := d.BuildMonoResamplerInputInt16(nativeSamples[start:end])
		nLeft := leftResampler.ProcessInt16Into(resamplerInput, leftScratch)
		n := nLeft
		if useStereoHistory {
			nRight := rightResampler.ProcessInt16Into(resamplerInput, rightScratch)
			if nRight < n {
				n = nRight
			}
		}
		if n < 0 || (outputOffset+n)*2 > len(output) {
			return 0, ErrDecodeFailed
		}
		if useStereoHistory {
			for i := 0; i < n; i++ {
				left := leftScratch[i]
				output[(outputOffset+i)*2] = left
				output[(outputOffset+i)*2+1] = rightScratch[i]
			}
		} else {
			duplicateMonoFloat32ToStereo(output[outputOffset*2:], leftScratch, n)
		}
		outputOffset += n
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 2)
	return outputOffset, nil
}

func duplicateMonoFloat32ToStereo(dst, src []float32, n int) {
	if n <= 0 {
		return
	}
	dst = dst[: n*2 : n*2]
	src = src[:n:n]
	i := 0
	j := 0
	for ; i+3 < n; i += 4 {
		v0 := src[i]
		v1 := src[i+1]
		v2 := src[i+2]
		v3 := src[i+3]
		dst[j] = v0
		dst[j+1] = v0
		dst[j+2] = v1
		dst[j+3] = v1
		dst[j+4] = v2
		dst[j+5] = v2
		dst[j+6] = v3
		dst[j+7] = v3
		j += 8
	}
	for ; i < n; i++ {
		v := src[i]
		dst[j] = v
		dst[j+1] = v
		j += 2
	}
}

func (d *Decoder) stereoFloat32Scratch(frameSizeSamples int) (left, right []float32, ok bool) {
	if frameSizeSamples <= 0 {
		return nil, nil, false
	}
	needed := frameSizeSamples * 2
	if cap(d.upsampleScratch) < needed {
		return nil, nil, false
	}
	scratch := d.upsampleScratch[:needed]
	return scratch[:frameSizeSamples], scratch[frameSizeSamples:needed], true
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
	fadeFactor := d.plcState.RecordLoss()
	// Match libopus silk_PLC_conceal() input cadence: use decoder-state lossCnt.
	lossCnt := d.state[0].lossCnt

	// Get native sample count from 48kHz frame size
	config := GetBandwidthConfig(bandwidth)
	nativeSamples := frameSizeSamples * config.SampleRate / 48000

	// Generate concealment at native rate.
	// Use LTP-aware concealment whenever per-channel SILK PLC state is valid.
	// Fall back to legacy concealment only when required state is unavailable.
	var concealed []float32
	hookLagPrev := 0
	usedDeepPLCHook := false
	if d.deepPLCLossMonoHook != nil {
		if d.scratchOutput != nil && len(d.scratchOutput) >= nativeSamples {
			concealed = d.scratchOutput[:nativeSamples]
		} else {
			concealed = make([]float32, nativeSamples)
		}
		ok, lagPrev := d.deepPLCLossMonoHook(concealed)
		if ok {
			usedDeepPLCHook = true
			hookLagPrev = lagPrev
			if state := d.ensureSILKPLCState(0); state != nil && d.state[0].nbSubfr > 0 {
				_ = plc.ConcealSILKWithLTP(d, state, lossCnt, nativeSamples)
				if lag := int((state.PitchLQ8 + 128) >> 8); lag > 0 {
					hookLagPrev = lag
				}
			}
		} else {
			concealed = nil
		}
	}
	if concealed == nil {
		if state := d.ensureSILKPLCState(0); state != nil && d.state[0].nbSubfr > 0 {
			concealedQ0 := plc.ConcealSILKWithLTP(d, state, lossCnt, nativeSamples)
			if d.scratchOutput != nil && len(d.scratchOutput) >= nativeSamples {
				concealed = d.scratchOutput[:nativeSamples]
			} else {
				concealed = make([]float32, nativeSamples)
			}
			// ConcealSILKWithLTP already applies libopus PLC attenuation cadence.
			// Keep only Q0 -> float scaling here (no extra external fade).
			scale := float32(1.0 / 32768.0)
			for i := 0; i < nativeSamples && i < len(concealedQ0); i++ {
				concealed[i] = float32(concealedQ0[i]) * scale
			}
			if lag := int((state.PitchLQ8 + 128) >> 8); lag > 0 {
				d.state[0].lagPrev = lag
			}
		} else {
			concealed = plc.ConcealSILK(d, nativeSamples, fadeFactor)
		}
	} else if hookLagPrev > 0 {
		d.state[0].lagPrev = hookLagPrev
	} else if state := d.ensureSILKPLCState(0); state != nil {
		if lag := int((state.PitchLQ8 + 128) >> 8); lag > 0 {
			d.state[0].lagPrev = lag
		}
	}

	// Update decoder state for PLC gluing and outBuf cadence.
	if usedDeepPLCHook {
		d.applyDeepPLCHistoryMono(&d.state[0], concealed)
	}
	d.recordPLCLossForState(&d.state[0], concealed)
	if usedDeepPLCHook {
		d.state[0].plcSkipRecoveryGlue = true
	}
	// Match libopus dec_API.c: on FLAG_PACKET_LOST, reset gain index
	// to avoid gain-bounce on subsequent good frames.
	d.state[0].lastGainIndex = 10

	// Upsample to 48kHz using the same mono sMid buffering cadence as good frames.
	duration := FrameDurationFromTOC(frameSizeSamples)
	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil || framesPerPacket <= 0 {
		resampler := d.GetResampler(bandwidth)
		return resampler.Process(d.BuildMonoResamplerInput(concealed)), nil
	}
	frameLength := nbSubfr * subFrameLengthMs * config.SampleRate / 1000
	if frameLength <= 0 || frameLength*framesPerPacket != len(concealed) {
		frameLength = len(concealed) / framesPerPacket
	}

	resampler := d.GetResampler(bandwidth)
	output := make([]float32, frameSizeSamples)
	outputOffset := 0
	for f := 0; f < framesPerPacket; f++ {
		start := f * frameLength
		end := start + frameLength
		if start < 0 || end > len(concealed) || frameLength == 0 {
			break
		}
		if d.deepPLCLossMonoHook != nil && len(d.scratchOutInt16) >= end {
			frameQ0 := d.scratchOutInt16[start:end]
			resamplerInput := d.BuildMonoResamplerInputInt16(frameQ0)
			outputOffset += resampler.ProcessInt16Into(resamplerInput, output[outputOffset:])
			continue
		}
		frame := concealed[start:end]
		resamplerInput := d.BuildMonoResamplerInput(frame)
		outputOffset += resampler.ProcessInto(resamplerInput, output[outputOffset:])
	}

	return output[:outputOffset], nil
}

// RecordPLCLossMono records a mono SILK PLC loss event for glue-frame tracking.
// This mirrors the state bookkeeping performed by decodePLC.
func (d *Decoder) RecordPLCLossMono(concealed []float32) {
	d.recordPLCLossForState(&d.state[0], concealed)
}

// RecordPLCLossStereo records stereo SILK PLC loss events for glue-frame tracking.
// This mirrors the state bookkeeping performed by decodePLCStereo.
func (d *Decoder) RecordPLCLossStereo(left, right []float32) {
	d.recordPLCLossForState(&d.state[0], left)
	d.recordPLCLossForState(&d.state[1], right)
}

func (d *Decoder) recordPLCLossForState(st *decoderState, concealed []float32) {
	if st == nil {
		return
	}
	channel := 0
	if st == &d.state[1] {
		channel = 1
	}
	st.lossCnt++
	if len(concealed) == 0 {
		st.plcConcEnergy = 0
		st.plcConcEnergyShift = 0
		st.plcLastFrameLost = true
		return
	}

	if cap(d.scratchOutInt16) < len(concealed) {
		d.scratchOutInt16 = make([]int16, len(concealed))
	}
	tmp := d.scratchOutInt16[:len(concealed)]
	for i, v := range concealed {
		tmp[i] = float32ToInt16(v)
	}

	d.updateHistoryInt16(tmp)
	// Keep decoder outBuf cadence aligned with normal decode path so
	// subsequent PLC rewhitening uses the most recent concealed output.
	silkUpdateOutBuf(st, tmp)

	// Match libopus decode_frame.c cadence on lost frames:
	// CNG is applied after outBuf update, then PLC glue captures concealed energy.
	d.applyCNG(channel, st, nil, tmp)
	silkPLCGlueFrames(st, tmp, len(tmp))

	const scale = float32(1.0 / 32768.0)
	for i := range tmp {
		concealed[i] = float32(tmp[i]) * scale
	}
}

func (d *Decoder) applyDeepPLCHistoryMono(st *decoderState, concealed []float32) {
	if st == nil || len(concealed) == 0 {
		return
	}
	order := st.lpcOrder
	if order <= 0 {
		return
	}
	if order > maxLPCOrder {
		order = maxLPCOrder
	}
	prevGainQ16 := st.prevGainQ16
	if plcState := d.ensureSILKPLCState(0); plcState != nil && plcState.PrevGainQ16[1] > 0 {
		prevGainQ16 = plcState.PrevGainQ16[1]
	}
	prevGainQ10 := prevGainQ16 >> 6
	if prevGainQ10 <= 0 {
		return
	}
	var history [maxLPCOrder]int32
	start := len(concealed) - order
	if start < 0 {
		start = 0
	}
	historyIdx := 0
	for i := start; i < len(concealed) && historyIdx < order; i++ {
		sampleQ0 := int32(float32ToInt16(concealed[i]))
		scaled := (float64(sampleQ0) * (1 << 24)) / float64(prevGainQ10)
		history[historyIdx] = int32(math.Floor(0.5 + scaled))
		historyIdx++
	}
	if historyIdx == 0 {
		return
	}
	setSLPCQ14HistoryQ14(st, history[:historyIdx])
}

func (d *Decoder) ApplyDeepPLCLossMono(concealed, rendered []float32, lagPrev int) int {
	if d == nil || len(concealed) == 0 || len(rendered) < len(concealed) {
		return 0
	}
	st := &d.state[0]
	var plcLagPrev int
	if plcState := d.ensureSILKPLCState(0); plcState != nil && st.nbSubfr > 0 {
		if view := d.plcDecoderView(0); view != nil {
			_ = plc.ConcealSILKWithLTP(view, plcState, max(0, st.lossCnt), len(concealed))
			plcLagPrev = int((plcState.PitchLQ8 + 128) >> 8)
		}
	}
	tmp := rendered[:len(concealed)]
	copy(tmp, concealed)
	d.recordPLCLossForState(st, tmp)
	switch {
	case plcLagPrev > 0:
		st.lagPrev = plcLagPrev
	case lagPrev > 0:
		st.lagPrev = lagPrev
	}
	st.lastGainIndex = 10
	d.applyDeepPLCHistoryMono(st, concealed)
	return len(tmp)
}

// syncLegacyPLCState aligns legacy PLC helper fields from libopus-style decoder state.
// ConcealSILK() still reads these legacy fields, so keep them synchronized after
// successful frame decodes (including LBRR/FEC decodes).
func (d *Decoder) syncLegacyPLCState(st *decoderState, recent []int16) {
	if st == nil {
		return
	}

	if st.lpcOrder > 0 {
		d.lpcOrder = st.lpcOrder
	}
	d.isPreviousFrameVoiced = int(st.indices.signalType) == typeVoiced

	order := d.lpcOrder
	if order <= 0 {
		return
	}
	if order > len(d.prevLPCValues) {
		order = len(d.prevLPCValues)
	}

	scale := float32(1.0 / 32768.0)
	if len(recent) >= order {
		base := len(recent) - order
		for i := 0; i < order; i++ {
			d.prevLPCValues[i] = float32(recent[base+i]) * scale
		}
		return
	}

	historyLen := len(d.outputHistory)
	if historyLen == 0 {
		return
	}
	start := d.historyIndex - order
	for i := 0; i < order; i++ {
		idx := start + i
		for idx < 0 {
			idx += historyLen
		}
		idx %= historyLen
		d.prevLPCValues[i] = d.outputHistory[idx]
	}
}

// decodePLCStereo generates concealment audio for a lost stereo packet.
func (d *Decoder) decodePLCStereo(bandwidth Bandwidth, frameSizeSamples int) ([]float32, error) {
	// Get fade factor for this loss
	fadeFactor := d.plcState.RecordLoss()
	// Match libopus silk_PLC_conceal() input cadence: use decoder-state lossCnt.
	lossCnt := d.state[0].lossCnt

	// Get native sample count from 48kHz frame size
	config := GetBandwidthConfig(bandwidth)
	nativeSamples := frameSizeSamples * config.SampleRate / 48000

	// libopus stereo PLC keeps operating in mid/side space and only converts
	// back to left/right through silk_stereo_MS_to_LR before resampling.
	// Our decoder states 0/1 track mid/side, not left/right.
	hasSide := d.prevDecodeOnlyMiddle == 0
	mid := make([]float32, nativeSamples)
	side := make([]float32, nativeSamples)

	midState := d.ensureSILKPLCState(0)
	sideState := d.ensureSILKPLCState(1)
	midView := d.plcDecoderView(0)
	sideView := d.plcDecoderView(1)
	if midState != nil && midView != nil && d.state[0].nbSubfr > 0 {
		midQ0 := plc.ConcealSILKWithLTP(midView, midState, lossCnt, nativeSamples)
		scale := float32(1.0 / 32768.0)
		for i := 0; i < nativeSamples && i < len(midQ0); i++ {
			mid[i] = float32(midQ0[i]) * scale
		}
		if lag := int((midState.PitchLQ8 + 128) >> 8); lag > 0 {
			d.state[0].lagPrev = lag
		}
		if hasSide && sideState != nil && sideView != nil && d.state[1].nbSubfr > 0 {
			sideQ0 := plc.ConcealSILKWithLTP(sideView, sideState, lossCnt, nativeSamples)
			for i := 0; i < nativeSamples && i < len(sideQ0); i++ {
				side[i] = float32(sideQ0[i]) * scale
			}
			if lag := int((sideState.PitchLQ8 + 128) >> 8); lag > 0 {
				d.state[1].lagPrev = lag
			}
		}
	} else {
		// Legacy fallback when richer PLC state is unavailable.
		left, right := plc.ConcealSILKStereo(d, nativeSamples, fadeFactor)
		copy(mid, left)
		if hasSide {
			copy(side, right)
		}
	}

	// Update decoder state for the concealed internal channels before MS->LR.
	d.recordPLCLossForState(&d.state[0], mid)
	d.state[0].lastGainIndex = 10
	if hasSide {
		d.recordPLCLossForState(&d.state[1], side)
		d.state[1].lastGainIndex = 10
	}

	// Convert concealed mid/side to left/right using the saved stereo predictor.
	midFrame := make([]int16, nativeSamples+2)
	sideFrame := make([]int16, nativeSamples+2)
	for i := 0; i < nativeSamples; i++ {
		midFrame[i+2] = float32ToInt16(mid[i])
		if hasSide {
			sideFrame[i+2] = float32ToInt16(side[i])
		}
	}
	predQ13 := []int32{d.stereo.predPrevQ13[0], d.stereo.predPrevQ13[1]}
	silkStereoMSToLR(&d.stereo, midFrame, sideFrame, predQ13, config.SampleRate/1000, nativeSamples)

	// Resample left/right channels to API rate.
	leftResampler := d.GetResamplerForChannel(bandwidth, 0)
	rightResampler := d.GetResamplerForChannel(bandwidth, 1)
	leftUp := make([]float32, frameSizeSamples)
	rightUp := make([]float32, frameSizeSamples)
	nLeft := leftResampler.ProcessInt16Into(midFrame[1:nativeSamples+1], leftUp)
	nRight := rightResampler.ProcessInt16Into(sideFrame[1:nativeSamples+1], rightUp)
	if nRight < nLeft {
		nLeft = nRight
	}
	if nLeft < 0 {
		nLeft = 0
	}

	output := make([]float32, nLeft*2)
	for i := 0; i < nLeft; i++ {
		output[i*2] = leftUp[i]
		output[i*2+1] = rightUp[i]
	}

	return output, nil
}

func float32ToInt16(v float32) int16 {
	// Match libopus FLOAT2INT16 path: keep scaling/clamping in float32,
	// then round-to-nearest-even at conversion.
	scaled := v * 32768.0
	if scaled > 32767.0 {
		return 32767
	}
	if scaled < -32768.0 {
		return -32768
	}
	return int16(math.RoundToEven(float64(scaled)))
}
