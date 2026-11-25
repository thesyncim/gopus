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

// finalizeSuccessfulDecode resets the shared PLC fade state after a good packet
// and records the frame size and channel count so a subsequent lost packet
// conceals at the right geometry. Called at the end of every successful public
// decode.
func (d *Decoder) finalizeSuccessfulDecode(frameSizeSamples, channels int) {
	d.plcState.Reset()
	d.plcState.SetLastFrameParams(plc.ModeSILK, frameSizeSamples, channels)
}

// Decode decodes one SILK mono packet and returns PCM at the decoder API rate.
// If data is nil it performs Packet Loss Concealment (PLC) for a lost packet
// instead of decoding. Mirrors the mono path of libopus silk/dec_API.c
// silk_Decode followed by the SILK resampler.
//
// The decoder bounds symbol-derived indices before they select state or
// buffers. Oracle tests record exact output for their tested valid inputs.
//
// Parameters:
//   - data: raw SILK frame data (without TOC byte), or nil for PLC
//   - bandwidth: NB, MB, or WB (from TOC)
//   - frameSizeSamples: frame size in samples at the decoder API rate
//   - vadFlag: ignored; VAD flags are decoded from the SILK bitstream
//
// Returns float32 samples in range [-1, 1] at the decoder API rate.
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
	duration := d.frameDurationFromAPISamples(frameSizeSamples)

	// Initialize range decoder
	var rd rangecoding.Decoder
	rd.Init(data)

	// Decode frame at native rate (without delay compensation, since we'll handle
	// sMid buffering in BuildMonoResamplerInput before resampling to the API rate)
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
	for f := range framesPerPacket {
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

// DecodeStereo decodes one SILK stereo packet and returns PCM at the decoder
// API rate. If data is nil it performs Packet Loss Concealment (PLC) for a lost
// packet instead of decoding. Mirrors the stereo path of libopus
// silk/dec_API.c silk_Decode (mid/side decode plus silk/stereo_MS_to_LR.c)
// followed by the SILK resampler. Oracle tests compare exact output for their
// tested valid inputs; malformed-input handling follows the public decoder's
// packet validation and bounded decode behavior.
//
// Returns interleaved stereo samples [L0, R0, L1, R1, ...] at the decoder API rate.
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
	duration := d.frameDurationFromAPISamples(frameSizeSamples)

	// Initialize range decoder
	var rd rangecoding.Decoder
	rd.Init(data)

	// Decode stereo at native rate
	leftNative, rightNative, err := d.DecodeStereoFrame(&rd, bandwidth, duration, vadFlag)
	if err != nil {
		return nil, err
	}

	// Mirror the int16-path bookkeeping so callers can read the native-rate
	// SILK lowband via LatestNativeStereo. Multistream stream decoders use
	// DecodeStereo (this function) and still need the pre-resample buffers
	// available for optional decoder-side post-processing (OSCE BWE /
	// LACE / NoLACE). The libopus-style int16 view is re-derived here from
	// the float32 native output rather than threading a second buffer
	// through DecodeStereoFrame.
	if nativeLowbandCaptureEnabled {
		d.recordNativeStereoFromFloat32(leftNative, rightNative, bandwidth)
	}

	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return nil, err
	}
	config := GetBandwidthConfig(bandwidth)
	frameLength := nbSubfr * subFrameLengthMs * config.SampleRate / 1000
	if framesPerPacket > 0 && frameLength*framesPerPacket != len(leftNative) {
		frameLength = len(leftNative) / framesPerPacket
	}

	// silk_Decode resamples once per decoded SILK frame. Keep that cadence so
	// the resampler delay buffer at a packet boundary matches libopus.
	leftResampler := d.GetResamplerForChannel(bandwidth, 0)
	rightResampler := d.GetResamplerForChannel(bandwidth, 1)
	left := make([]float32, 0, frameSizeSamples)
	right := make([]float32, 0, frameSizeSamples)
	for f := range framesPerPacket {
		start := f * frameLength
		end := start + frameLength
		if frameLength <= 0 || end > len(leftNative) || end > len(rightNative) {
			break
		}
		left = append(left, leftResampler.Process(leftNative[start:end])...)
		right = append(right, rightResampler.Process(rightNative[start:end])...)
	}

	// Interleave samples [L0, R0, L1, R1, ...]
	output := make([]float32, len(left)*2)
	for i := range left {
		output[i*2] = left[i]
		output[i*2+1] = right[i]
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 2)

	return output, nil
}

// recordNativeStereoFromFloat32 copies the float32 native SILK left/right
// channels into the int16 scratch slots that back LatestNativeStereo. Used by
// DecodeStereo so multistream stream decoders (which take this path) can
// expose the pre-resample SILK lowband to optional post-processing such as
// OSCE BWE / LACE.
func (d *Decoder) recordNativeStereoFromFloat32(leftNative, rightNative []float32, bandwidth Bandwidth) {
	if !nativeLowbandCaptureEnabled {
		return
	}
	n := min(min(len(leftNative), len(d.stereoLeftNative)), len(rightNative))
	if n > len(d.stereoRightNative) {
		n = len(d.stereoRightNative)
	}
	for i := 0; i < n; i++ {
		l := leftNative[i] * 32768.0
		if l > 32767.0 {
			l = 32767.0
		} else if l < -32768.0 {
			l = -32768.0
		}
		r := rightNative[i] * 32768.0
		if r > 32767.0 {
			r = 32767.0
		} else if r < -32768.0 {
			r = -32768.0
		}
		d.stereoLeftNative[i] = int16(l)
		d.stereoRightNative[i] = int16(r)
	}
	d.lastNativeStereoLen = int32(n)
	d.lastNativeStereoFsKHz = int32(GetBandwidthConfig(bandwidth).SampleRate / 1000)
	d.lastNativeMonoLen = 0
	d.lastNativeMonoFsKHz = 0
	d.lastNativeMidLen = 0
	d.lastNativeMidFsKHz = 0
}

// DecodeStereoToMono decodes a SILK stereo frame and returns mono PCM at the decoder API rate.
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
		return d.decodePLCStereoToMono(bandwidth, frameSizeSamples)
	}

	// Convert TOC frame size to duration
	duration := d.frameDurationFromAPISamples(frameSizeSamples)

	// Initialize range decoder
	var rd rangecoding.Decoder
	rd.Init(data)

	// Decode mid channel at native rate (side channel decoded for state)
	midNative, frameLength, err := d.decodeStereoMidNative(&rd, bandwidth, duration, vadFlag)
	if err != nil {
		return nil, err
	}

	// Resample to the decoder API rate using libopus-compatible resampler and sMid buffering.
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

// DecodeMonoToStereo decodes a mono SILK frame and returns stereo PCM at the decoder API rate.
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
	if data == nil {
		useStereoHistory := d.ShouldUseStereoToMonoHistory(bandwidth, stereoToMono)
		// Handle bandwidth changes - reset sMid state when sample rate changes.
		d.handleBandwidthChange(bandwidth)
		if !useStereoHistory {
			mono, err := d.decodePLC(bandwidth, frameSizeSamples)
			if err != nil {
				return nil, err
			}
			out := make([]float32, len(mono)*2)
			duplicateMonoFloat32ToStereo(out, mono, len(mono))
			return out, nil
		}
		return d.decodePLCStereo(bandwidth, frameSizeSamples)
	}
	var rd rangecoding.Decoder
	rd.Init(data)
	out := make([]float32, frameSizeSamples*2)
	n, err := d.DecodeMonoToStereoWithDecoderInto(
		&rd, bandwidth, frameSizeSamples, vadFlag, stereoToMono, out,
	)
	if err != nil {
		return nil, err
	}
	return out[:n], nil
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

	duration := d.frameDurationFromAPISamples(frameSizeSamples)

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
	for f := range framesPerPacket {
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

	duration := d.frameDurationFromAPISamples(frameSizeSamples)

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
	for f := range framesPerPacket {
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

	duration := d.frameDurationFromAPISamples(frameSizeSamples)
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
	// Record native-rate length / fs for optional decoder-side post-
	// processing (OSCE BWE). LatestNativeStereo reads back from
	// stereoLeftNative / stereoRightNative which are aliased by the
	// `leftNative` / `rightNative` slices returned by GetStereoInt16Scratch
	// above, so the pre-resample SILK lowband is available to the caller
	// without performing a second decode pass. Mirrors LatestNativeMono.
	if nativeLowbandCaptureEnabled {
		d.lastNativeStereoLen = int32(nativeSamples)
		d.lastNativeStereoFsKHz = int32(config.SampleRate / 1000)
	}
	leftResampler := d.GetResamplerForChannel(bandwidth, 0)
	rightResampler := d.GetResamplerForChannel(bandwidth, 1)
	leftScratch, rightScratch, ok := d.stereoFloat32Scratch(frameSizeSamples)
	if !ok {
		return 0, ErrDecodeFailed
	}

	frameLength := nbSubfr * subFrameLengthMs * config.SampleRate / 1000
	outputOffset := 0
	for f := range framesPerPacket {
		start := f * frameLength
		end := start + frameLength
		if frameLength <= 0 || end > nativeSamples {
			return 0, ErrDecodeFailed
		}
		if outL, outR, ok := ResampleStereoInt16(leftResampler, rightResampler, leftNative[start:end], rightNative[start:end]); ok {
			n := min(len(outL), len(outR), len(leftScratch)-outputOffset)
			if (outputOffset+n)*2 > len(output) {
				return 0, ErrDecodeFailed
			}
			InterleaveInt16AsFloat32(output[outputOffset*2:(outputOffset+n)*2], outL[:n], outR[:n])
			outputOffset += n
			continue
		}
		nLeft := leftResampler.ProcessInt16Into(leftNative[start:end], leftScratch[outputOffset:])
		nRight := rightResampler.ProcessInt16Into(rightNative[start:end], rightScratch[outputOffset:])
		n := min(nRight, nLeft)
		if n < 0 || (outputOffset+n)*2 > len(output) {
			return 0, ErrDecodeFailed
		}
		interleaveStereoFloat32(output[outputOffset*2:(outputOffset+n)*2], leftScratch[outputOffset:outputOffset+n], rightScratch[outputOffset:outputOffset+n])
		outputOffset += n
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 2)
	return outputOffset, nil
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

	duration := d.frameDurationFromAPISamples(frameSizeSamples)

	leftNative, rightNative, err := d.DecodeStereoFrame(rd, bandwidth, duration, vadFlag)
	if err != nil {
		return nil, err
	}

	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return nil, err
	}
	config := GetBandwidthConfig(bandwidth)
	frameLength := nbSubfr * subFrameLengthMs * config.SampleRate / 1000
	if framesPerPacket > 0 && frameLength*framesPerPacket != len(leftNative) {
		frameLength = len(leftNative) / framesPerPacket
	}
	leftResampler := d.GetResamplerForChannel(bandwidth, 0)
	rightResampler := d.GetResamplerForChannel(bandwidth, 1)
	left := make([]float32, 0, frameSizeSamples)
	right := make([]float32, 0, frameSizeSamples)
	for f := range framesPerPacket {
		start := f * frameLength
		end := start + frameLength
		if frameLength <= 0 || end > len(leftNative) || end > len(rightNative) {
			break
		}
		left = append(left, leftResampler.Process(leftNative[start:end])...)
		right = append(right, rightResampler.Process(rightNative[start:end])...)
	}

	output := make([]float32, len(left)*2)
	for i := range left {
		output[i*2] = left[i]
		output[i*2+1] = right[i]
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 2)

	return output, nil
}

// DecodeStereoToMonoWithDecoderInto decodes a SILK stereo frame to mono into a
// caller-owned buffer using a pre-initialized range decoder.
func (d *Decoder) DecodeStereoToMonoWithDecoderInto(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	frameSizeSamples int,
	vadFlag bool,
	output []float32,
) (int, error) {
	// Handle bandwidth changes - reset sMid state when sample rate changes
	d.handleBandwidthChange(bandwidth)

	if bandwidth > BandwidthWideband {
		return 0, ErrInvalidBandwidth
	}
	if rd == nil {
		return 0, ErrDecodeFailed
	}

	duration := d.frameDurationFromAPISamples(frameSizeSamples)

	midNative, frameLength, err := d.decodeStereoMidNative(rd, bandwidth, duration, vadFlag)
	if err != nil {
		return 0, err
	}

	framesPerPacket := 0
	if frameLength > 0 {
		framesPerPacket = len(midNative) / frameLength
	}
	resampler := d.GetResamplerForChannel(bandwidth, 0)
	outputOffset := 0
	config := GetBandwidthConfig(bandwidth)
	for f := 0; f < framesPerPacket; f++ {
		start := f * frameLength
		end := start + frameLength
		if start < 0 || end > len(midNative) || frameLength == 0 {
			break
		}
		resamplerInput := d.BuildMonoResamplerInputInt16(midNative[start:end])
		frameOutputLen := len(resamplerInput) * d.outputSampleRate() / config.SampleRate
		frameOutput := output[outputOffset:]
		copyToOutput := len(frameOutput) >= frameOutputLen
		if !copyToOutput {
			if frameOutputLen > cap(d.upsampleScratch) {
				return outputOffset, ErrDecodeFailed
			}
			frameOutput = d.upsampleScratch[:frameOutputLen]
		}
		written := resampler.ProcessInt16Into(resamplerInput, frameOutput)
		if !copyToOutput {
			written = copy(output[outputOffset:], frameOutput[:written])
		}
		outputOffset += written
	}

	d.finalizeSuccessfulDecode(frameSizeSamples, 1)

	return outputOffset, nil
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

	duration := d.frameDurationFromAPISamples(frameSizeSamples)
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
	for f := range framesPerPacket {
		start := f * frameLength
		end := start + frameLength
		if start < 0 || end > len(nativeSamples) {
			return 0, ErrDecodeFailed
		}
		// dec_API.c recomputes stereo_to_mono per silk_Decode call. A
		// multi-frame packet resamples the retained right history only on its
		// first frame, before nChannelsInternal becomes mono.
		useStereoHistoryThisFrame := useStereoHistory && f == 0
		resamplerInput := d.BuildMonoResamplerInputInt16(nativeSamples[start:end])
		nLeft := leftResampler.ProcessInt16Into(resamplerInput, leftScratch)
		n := nLeft
		if useStereoHistoryThisFrame {
			nRight := rightResampler.ProcessInt16Into(resamplerInput, rightScratch)
			if nRight < n {
				n = nRight
			}
		}
		if n < 0 || (outputOffset+n)*2 > len(output) {
			return 0, ErrDecodeFailed
		}
		if useStereoHistoryThisFrame {
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
	duplicateMonoFloat32ToStereoCore(dst, src, n)
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
		output[i] = float32ToInt16(s)
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
		output[i] = float32ToInt16(s)
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
