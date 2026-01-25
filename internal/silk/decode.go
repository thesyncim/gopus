package silk

import (
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// DecodeFrame decodes a single SILK mono frame from the bitstream.
// Returns decoded samples at native SILK sample rate (8/12/16kHz).
//
// Parameters:
//   - rd: Range decoder initialized with the SILK bitstream
//   - bandwidth: Audio bandwidth (NB/MB/WB)
//   - duration: Frame duration (10/20/40/60ms)
//   - vadFlag: Voice Activity Detection flag from header
//
// For 40/60ms frames, the frame is decoded as multiple 20ms sub-blocks.
func (d *Decoder) DecodeFrame(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	duration FrameDuration,
	vadFlag bool,
) ([]float32, error) {
	_ = vadFlag
	if rd == nil {
		return nil, ErrDecodeFailed
	}
	d.SetRangeDecoder(rd)
	config := GetBandwidthConfig(bandwidth)
	fsKHz := config.SampleRate / 1000
	st := &d.state[0]

	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return nil, err
	}

	st.nFramesDecoded = 0
	st.nFramesPerPacket = framesPerPacket
	st.nbSubfr = nbSubfr
	silkDecoderSetFs(st, fsKHz)

	decodeVADAndLBRRFlags(rd, st, framesPerPacket)
	if st.LBRRFlag != 0 {
		for i := 0; i < framesPerPacket; i++ {
			if st.LBRRFlags[i] == 0 {
				continue
			}
			condCoding := codeIndependently
			if i > 0 && st.LBRRFlags[i-1] != 0 {
				condCoding = codeConditionally
			}
			silkDecodeIndices(st, rd, true, condCoding)
			pulses := make([]int16, roundUpShellFrame(st.frameLength))
			silkDecodePulses(rd, pulses, int(st.indices.signalType), int(st.indices.quantOffsetType), st.frameLength)
		}
	}

	frameLength := st.frameLength
	outInt16 := make([]int16, framesPerPacket*frameLength)
	for i := 0; i < framesPerPacket; i++ {
		frameIndex := st.nFramesDecoded
		condCoding := codeIndependently
		if frameIndex > 0 {
			condCoding = codeConditionally
		}
		vad := st.VADFlags[frameIndex] != 0
		frameOut := outInt16[i*frameLength : (i+1)*frameLength]
		silkDecodeIndices(st, rd, vad, condCoding)
		pulses := make([]int16, roundUpShellFrame(st.frameLength))
		silkDecodePulses(rd, pulses, int(st.indices.signalType), int(st.indices.quantOffsetType), st.frameLength)
		var ctrl decoderControl
		silkDecodeParameters(st, &ctrl, condCoding)
		silkDecodeCore(st, &ctrl, frameOut, pulses)
		silkUpdateOutBuf(st, frameOut)
		st.lossCnt = 0
		st.lagPrev = ctrl.pitchL[st.nbSubfr-1]
		st.prevSignalType = int(st.indices.signalType)
		st.firstFrameAfterReset = false
		st.nFramesDecoded++
	}

	output := make([]float32, len(outInt16))
	for i, v := range outInt16 {
		output[i] = float32(v) / 32768.0
	}

	d.haveDecoded = true
	return output, nil
}

// decode20msBlock decodes one 20ms sub-block.
func (d *Decoder) decode20msBlock(
	bandwidth Bandwidth,
	vadFlag bool,
	output []float32,
) error {
	return d.decodeBlock(bandwidth, vadFlag, Frame20ms, output)
}

// decodeBlock decodes a 10ms or 20ms block.
func (d *Decoder) decodeBlock(
	bandwidth Bandwidth,
	vadFlag bool,
	duration FrameDuration,
	output []float32,
) error {
	config := GetBandwidthConfig(bandwidth)
	numSubframes := len(output) / config.SubframeSamples

	// 1. Decode frame type
	signalType, quantOffset := d.DecodeFrameType(vadFlag)

	// 2. Decode subframe gains
	gains := d.decodeSubframeGains(signalType, numSubframes)

	// 3. Decode LSF -> LPC coefficients
	lsfQ15 := d.decodeLSFCoefficients(bandwidth, signalType)
	lpcQ12 := lsfToLPC(lsfQ15)
	limitLPCFilterGain(lpcQ12)

	// 4. Decode pitch/LTP (voiced only)
	var pitchLags []int
	var ltpCoeffs [][]int8
	var ltpScale int
	if signalType == 2 { // Voiced
		pitchLags = d.decodePitchLag(bandwidth, numSubframes)
		ltpCoeffs, ltpScale = d.decodeLTPCoefficients(bandwidth, numSubframes)
	}

	// 5. Decode and synthesize each subframe
	for sf := 0; sf < numSubframes; sf++ {
		sfStart := sf * config.SubframeSamples
		sfEnd := sfStart + config.SubframeSamples
		sfOutput := output[sfStart:sfEnd]

		// Decode excitation
		excitation := d.decodeExcitation(config.SubframeSamples, signalType, quantOffset)

		// Scale excitation by gain
		scaleExcitation(excitation, gains[sf])

		// Apply LTP synthesis (voiced only)
		if signalType == 2 && pitchLags != nil {
			d.ltpSynthesis(excitation, pitchLags[sf], ltpCoeffs[sf], ltpScale)
		}

		// Apply LPC synthesis
		d.lpcSynthesis(excitation, lpcQ12, gains[sf], sfOutput)

		// Update output history for LTP lookback
		d.updateHistory(sfOutput)
	}

	// Update voiced flag for next frame
	d.isPreviousFrameVoiced = (signalType == 2)

	return nil
}

// DecodeStereoFrame decodes a SILK stereo frame from the bitstream.
// Returns left and right channel samples at native sample rate.
//
// Stereo SILK uses mid-side coding with prediction.
// The mid channel is decoded first, then the side channel,
// and finally they are unmixed to left and right.
func (d *Decoder) DecodeStereoFrame(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	duration FrameDuration,
	vadFlag bool,
) (left, right []float32, err error) {
	_ = vadFlag
	if rd == nil {
		return nil, nil, ErrDecodeFailed
	}
	d.SetRangeDecoder(rd)
	config := GetBandwidthConfig(bandwidth)
	fsKHz := config.SampleRate / 1000
	stMid := &d.state[0]
	stSide := &d.state[1]

	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return nil, nil, err
	}

	stMid.nFramesDecoded = 0
	stSide.nFramesDecoded = 0
	stMid.nFramesPerPacket = framesPerPacket
	stSide.nFramesPerPacket = framesPerPacket
	stMid.nbSubfr = nbSubfr
	stSide.nbSubfr = nbSubfr
	silkDecoderSetFs(stMid, fsKHz)
	silkDecoderSetFs(stSide, fsKHz)

	decodeVADAndLBRRFlags(rd, stMid, framesPerPacket)
	decodeVADAndLBRRFlags(rd, stSide, framesPerPacket)
	if stMid.LBRRFlag != 0 || stSide.LBRRFlag != 0 {
		predQ13 := make([]int32, 2)
		for i := 0; i < framesPerPacket; i++ {
			for ch := 0; ch < 2; ch++ {
				st := &d.state[ch]
				if st.LBRRFlags[i] == 0 {
					continue
				}
				if ch == 0 {
					silkStereoDecodePred(rd, predQ13)
					if stSide.LBRRFlags[i] == 0 {
						_ = silkStereoDecodeMidOnly(rd)
					}
				}
				condCoding := codeIndependently
				if i > 0 && st.LBRRFlags[i-1] != 0 {
					condCoding = codeConditionally
				}
				silkDecodeIndices(st, rd, true, condCoding)
				pulses := make([]int16, roundUpShellFrame(st.frameLength))
				silkDecodePulses(rd, pulses, int(st.indices.signalType), int(st.indices.quantOffsetType), st.frameLength)
			}
		}
	}

	frameLength := stMid.frameLength
	leftNative := make([]int16, framesPerPacket*frameLength)
	rightNative := make([]int16, framesPerPacket*frameLength)
	predQ13 := make([]int32, 2)
	decodeOnlyMiddle := 0

	for i := 0; i < framesPerPacket; i++ {
		frameIndex := stMid.nFramesDecoded
		silkStereoDecodePred(rd, predQ13)
		if stSide.VADFlags[frameIndex] == 0 {
			decodeOnlyMiddle = silkStereoDecodeMidOnly(rd)
		} else {
			decodeOnlyMiddle = 0
		}

		if decodeOnlyMiddle == 0 && d.prevDecodeOnlyMiddle == 1 {
			resetSideChannelState(stSide)
		}

		hasSide := decodeOnlyMiddle == 0
		midFrame := make([]int16, frameLength+2)
		sideFrame := make([]int16, frameLength+2)
		midOut := midFrame[2:]
		sideOut := sideFrame[2:]

		condMid := codeIndependently
		if frameIndex > 0 {
			condMid = codeConditionally
		}
		vadMid := stMid.VADFlags[frameIndex] != 0
		silkDecodeIndices(stMid, rd, vadMid, condMid)
		pulsesMid := make([]int16, roundUpShellFrame(stMid.frameLength))
		silkDecodePulses(rd, pulsesMid, int(stMid.indices.signalType), int(stMid.indices.quantOffsetType), stMid.frameLength)
		var ctrlMid decoderControl
		silkDecodeParameters(stMid, &ctrlMid, condMid)
		silkDecodeCore(stMid, &ctrlMid, midOut, pulsesMid)
		silkUpdateOutBuf(stMid, midOut)
		stMid.lossCnt = 0
		stMid.lagPrev = ctrlMid.pitchL[stMid.nbSubfr-1]
		stMid.prevSignalType = int(stMid.indices.signalType)
		stMid.firstFrameAfterReset = false
		stMid.nFramesDecoded++

		if hasSide {
			frameIndexSide := frameIndex - 1
			condSide := codeIndependently
			if frameIndexSide > 0 {
				if d.prevDecodeOnlyMiddle == 1 {
					condSide = codeIndependentlyNoLtpScaling
				} else {
					condSide = codeConditionally
				}
			}
			vadSide := stSide.VADFlags[stSide.nFramesDecoded] != 0
			silkDecodeIndices(stSide, rd, vadSide, condSide)
			pulsesSide := make([]int16, roundUpShellFrame(stSide.frameLength))
			silkDecodePulses(rd, pulsesSide, int(stSide.indices.signalType), int(stSide.indices.quantOffsetType), stSide.frameLength)
			var ctrlSide decoderControl
			silkDecodeParameters(stSide, &ctrlSide, condSide)
			silkDecodeCore(stSide, &ctrlSide, sideOut, pulsesSide)
			silkUpdateOutBuf(stSide, sideOut)
			stSide.lossCnt = 0
			stSide.lagPrev = ctrlSide.pitchL[stSide.nbSubfr-1]
			stSide.prevSignalType = int(stSide.indices.signalType)
			stSide.firstFrameAfterReset = false
		} else {
			for j := range sideOut {
				sideOut[j] = 0
			}
		}
		stSide.nFramesDecoded++

		silkStereoMSToLR(&d.stereo, midFrame, sideFrame, predQ13, fsKHz, frameLength)
		copy(leftNative[i*frameLength:(i+1)*frameLength], midFrame[1:frameLength+1])
		copy(rightNative[i*frameLength:(i+1)*frameLength], sideFrame[1:frameLength+1])
	}

	d.prevDecodeOnlyMiddle = decodeOnlyMiddle
	left = make([]float32, len(leftNative))
	right = make([]float32, len(rightNative))
	for i, v := range leftNative {
		left[i] = float32(v) / 32768.0
	}
	for i, v := range rightNative {
		right[i] = float32(v) / 32768.0
	}

	d.haveDecoded = true
	return left, right, nil
}

// decodeChannel decodes a single channel (used for stereo).
func (d *Decoder) decodeChannel(
	bandwidth Bandwidth,
	duration FrameDuration,
	vadFlag bool,
	output []float32,
) error {
	if is40or60ms(duration) {
		subBlocks := getSubBlockCount(duration)
		config := GetBandwidthConfig(bandwidth)
		subBlockSamples := 4 * config.SubframeSamples

		for block := 0; block < subBlocks; block++ {
			blockOutput := output[block*subBlockSamples : (block+1)*subBlockSamples]
			if err := d.decode20msBlock(bandwidth, vadFlag, blockOutput); err != nil {
				return err
			}
		}
	} else {
		return d.decodeBlock(bandwidth, vadFlag, duration, output)
	}
	return nil
}
