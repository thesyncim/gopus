package silk

import (
	"github.com/thesyncim/gopus/rangecoding"
)

// DecodeFrame decodes a single SILK mono frame from the bitstream.
// Returns decoded samples at native SILK sample rate (8/12/16kHz).
//
// The output includes libopus-compatible delay compensation:
// - 1 sample from sMid history buffer prepended before resampler
// - N samples of resampler input delay (varies by sample rate)
// This matches libopus's exact output timing for test vector compliance.
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
			// Use pre-allocated pulses buffer if available
			pulsesLen := roundUpShellFrame(st.frameLength)
			var pulses []int16
			if d.scratchPulses != nil && len(d.scratchPulses) >= pulsesLen {
				pulses = d.scratchPulses[:pulsesLen]
				for j := range pulses {
					pulses[j] = 0
				}
			} else {
				pulses = make([]int16, pulsesLen)
			}
			silkDecodePulsesWithScratch(rd, pulses, int(st.indices.signalType), int(st.indices.quantOffsetType), st.frameLength, st.scratchSumPulses, st.scratchNLshifts)
		}
	}

	frameLength := st.frameLength
	totalLen := framesPerPacket * frameLength

	// Use pre-allocated outInt16 buffer if available
	var outInt16 []int16
	if d.scratchOutInt16 != nil && len(d.scratchOutInt16) >= totalLen {
		outInt16 = d.scratchOutInt16[:totalLen]
		for j := range outInt16 {
			outInt16[j] = 0
		}
	} else {
		outInt16 = make([]int16, totalLen)
	}

	for i := 0; i < framesPerPacket; i++ {
		frameIndex := st.nFramesDecoded
		condCoding := codeIndependently
		if frameIndex > 0 {
			condCoding = codeConditionally
		}
		vad := st.VADFlags[frameIndex] != 0
		frameOut := outInt16[i*frameLength : (i+1)*frameLength]
		silkDecodeIndices(st, rd, vad, condCoding)
		// Use pre-allocated pulses buffer if available
		pulsesLen := roundUpShellFrame(st.frameLength)
		var pulses []int16
		if d.scratchPulses != nil && len(d.scratchPulses) >= pulsesLen {
			pulses = d.scratchPulses[:pulsesLen]
			for j := range pulses {
				pulses[j] = 0
			}
		} else {
			pulses = make([]int16, pulsesLen)
		}
		silkDecodePulsesWithScratch(rd, pulses, int(st.indices.signalType), int(st.indices.quantOffsetType), st.frameLength, st.scratchSumPulses, st.scratchNLshifts)
		var ctrl decoderControl
		silkDecodeParameters(st, &ctrl, condCoding)
		silkDecodeCore(st, &ctrl, frameOut, pulses)
		silkUpdateOutBuf(st, frameOut)

		// Apply PLC glue frames for smooth transition from concealed to real frames.
		// This must be called after updating outBuf and before resetting lossCnt.
		silkPLCGlueFrames(st, frameOut, frameLength)

		st.lossCnt = 0
		st.lagPrev = ctrl.pitchL[st.nbSubfr-1]
		st.prevSignalType = int(st.indices.signalType)
		st.firstFrameAfterReset = false
		st.nFramesDecoded++
	}

	// Apply libopus-compatible mono delay compensation.
	// This matches the delay introduced by:
	// 1. sMid[1] prepended before resampler input (1 sample)
	// 2. Resampler input delay from delay_matrix_dec (varies by rate)
	delayedInt16 := d.applyMonoDelay(outInt16, fsKHz)

	// Use pre-allocated output buffer if available
	var output []float32
	if d.scratchOutput != nil && len(d.scratchOutput) >= len(delayedInt16) {
		output = d.scratchOutput[:len(delayedInt16)]
	} else {
		output = make([]float32, len(delayedInt16))
	}
	for i, v := range delayedInt16 {
		output[i] = float32(v) / 32768.0
	}

	// Mono decode resets mid-only tracking (libopus sets decode_only_middle=0).
	d.prevDecodeOnlyMiddle = 0
	d.haveDecoded = true
	return output, nil
}

// DecodeFrameRaw decodes a single SILK mono frame from the bitstream.
// Returns decoded samples at native SILK sample rate (8/12/16kHz) as float32.
//
// Unlike DecodeFrame, this does NOT apply libopus-compatible delay compensation.
// The caller is responsible for handling sMid buffering before resampling.
// This is used by Decode, DecodeWithDecoder, and the Hybrid decoder.
//
// Parameters:
//   - rd: Range decoder initialized with the SILK bitstream
//   - bandwidth: Audio bandwidth (NB/MB/WB)
//   - duration: Frame duration (10/20/40/60ms)
//   - vadFlag: Voice Activity Detection flag from header
//
// For 40/60ms frames, the frame is decoded as multiple 20ms sub-blocks.
func (d *Decoder) DecodeFrameRaw(
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
			// Use pre-allocated pulses buffer if available
			pulsesLen := roundUpShellFrame(st.frameLength)
			var pulses []int16
			if d.scratchPulses != nil && len(d.scratchPulses) >= pulsesLen {
				pulses = d.scratchPulses[:pulsesLen]
				for j := range pulses {
					pulses[j] = 0
				}
			} else {
				pulses = make([]int16, pulsesLen)
			}
			silkDecodePulsesWithScratch(rd, pulses, int(st.indices.signalType), int(st.indices.quantOffsetType), st.frameLength, st.scratchSumPulses, st.scratchNLshifts)
		}
	}

	frameLength := st.frameLength
	totalLen := framesPerPacket * frameLength

	// Use pre-allocated outInt16 buffer if available
	var outInt16 []int16
	if d.scratchOutInt16 != nil && len(d.scratchOutInt16) >= totalLen {
		outInt16 = d.scratchOutInt16[:totalLen]
		for j := range outInt16 {
			outInt16[j] = 0
		}
	} else {
		outInt16 = make([]int16, totalLen)
	}

	for i := 0; i < framesPerPacket; i++ {
		frameIndex := st.nFramesDecoded
		condCoding := codeIndependently
		if frameIndex > 0 {
			condCoding = codeConditionally
		}
		vad := st.VADFlags[frameIndex] != 0
		frameOut := outInt16[i*frameLength : (i+1)*frameLength]
		silkDecodeIndices(st, rd, vad, condCoding)
		// Use pre-allocated pulses buffer if available
		pulsesLen := roundUpShellFrame(st.frameLength)
		var pulses []int16
		if d.scratchPulses != nil && len(d.scratchPulses) >= pulsesLen {
			pulses = d.scratchPulses[:pulsesLen]
			for j := range pulses {
				pulses[j] = 0
			}
		} else {
			pulses = make([]int16, pulsesLen)
		}
		silkDecodePulsesWithScratch(rd, pulses, int(st.indices.signalType), int(st.indices.quantOffsetType), st.frameLength, st.scratchSumPulses, st.scratchNLshifts)
		var ctrl decoderControl
		silkDecodeParameters(st, &ctrl, condCoding)
		silkDecodeCore(st, &ctrl, frameOut, pulses)
		silkUpdateOutBuf(st, frameOut)

		// Apply PLC glue frames for smooth transition from concealed to real frames.
		// This must be called after updating outBuf and before resetting lossCnt.
		silkPLCGlueFrames(st, frameOut, frameLength)

		st.lossCnt = 0
		st.lagPrev = ctrl.pitchL[st.nbSubfr-1]
		st.prevSignalType = int(st.indices.signalType)
		st.firstFrameAfterReset = false
		st.nFramesDecoded++
	}

	// Convert to float32 WITHOUT delay compensation.
	// The caller handles sMid buffering via BuildMonoResamplerInput.
	// Use pre-allocated output buffer if available
	var output []float32
	if d.scratchOutput != nil && len(d.scratchOutput) >= len(outInt16) {
		output = d.scratchOutput[:len(outInt16)]
	} else {
		output = make([]float32, len(outInt16))
	}
	for i, v := range outInt16 {
		output[i] = float32(v) / 32768.0
	}

	// Mono decode resets mid-only tracking (libopus sets decode_only_middle=0).
	d.prevDecodeOnlyMiddle = 0
	d.haveDecoded = true
	return output, nil
}

// DecodeStereoFrameToMono decodes a stereo SILK frame and returns the mid channel
// at native sample rate. The side channel is still decoded to keep the bitstream aligned.
func (d *Decoder) DecodeStereoFrameToMono(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	duration FrameDuration,
	vadFlag bool,
) ([]float32, error) {
	midNative, _, err := d.decodeStereoMidNative(rd, bandwidth, duration, vadFlag)
	if err != nil {
		return nil, err
	}
	mid := make([]float32, len(midNative))
	for i, v := range midNative {
		mid[i] = float32(v) / 32768.0
	}
	return mid, nil
}

// DecodeFrameWithTrace decodes a SILK frame with tracing callbacks.
// The callback is called for each subframe with LTP information.
func (d *Decoder) DecodeFrameWithTrace(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	duration FrameDuration,
	vadFlag bool,
	trace TraceCallback,
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
		// Use tracing version if callback provided
		silkDecodeCoreWithTrace(st, &ctrl, frameOut, pulses, i, trace)
		silkUpdateOutBuf(st, frameOut)

		// Apply PLC glue frames for smooth transition from concealed to real frames.
		silkPLCGlueFrames(st, frameOut, frameLength)

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
			// Transition from mono to stereo - reset side channel decoder state only.
			// Per libopus dec_API.c lines 307-314: only outBuf, sLPC_Q14_buf, etc. are reset.
			// NOTE: pred_prev_Q13 and sSide are NOT reset here - they keep continuity.
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

		// Apply PLC glue frames for smooth transition from concealed to real frames.
		silkPLCGlueFrames(stMid, midOut, frameLength)

		stMid.lossCnt = 0
		stMid.lagPrev = ctrlMid.pitchL[stMid.nbSubfr-1]
		stMid.prevSignalType = int(stMid.indices.signalType)
		stMid.firstFrameAfterReset = false
		stMid.nFramesDecoded++

		if hasSide {
			condSide := codeIndependently
			if frameIndex > 0 {
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

			// Apply PLC glue frames for side channel.
			silkPLCGlueFrames(stSide, sideOut, frameLength)

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

		// Track mid-only flag per frame (used for side-channel conditioning).
		d.prevDecodeOnlyMiddle = decodeOnlyMiddle
	}

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

// decodeStereoMidNative decodes a stereo SILK frame but returns only the mid channel
// at native sample rate. It still decodes the side channel to keep the bitstream aligned.
func (d *Decoder) decodeStereoMidNative(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	duration FrameDuration,
	vadFlag bool,
) (mid []int16, frameLength int, err error) {
	_ = vadFlag
	if rd == nil {
		return nil, 0, ErrDecodeFailed
	}
	d.SetRangeDecoder(rd)
	config := GetBandwidthConfig(bandwidth)
	fsKHz := config.SampleRate / 1000
	stMid := &d.state[0]
	stSide := &d.state[1]

	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return nil, 0, err
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

	frameLength = stMid.frameLength
	midNative := make([]int16, framesPerPacket*frameLength)
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
			// Transition from mono to stereo - reset side channel decoder state only.
			// Per libopus dec_API.c lines 307-314: only outBuf, sLPC_Q14_buf, etc. are reset.
			// NOTE: pred_prev_Q13 and sSide are NOT reset here - they keep continuity.
			resetSideChannelState(stSide)
		}

		hasSide := decodeOnlyMiddle == 0
		midOut := midNative[i*frameLength : (i+1)*frameLength]

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

		// Apply PLC glue frames for smooth transition from concealed to real frames.
		silkPLCGlueFrames(stMid, midOut, frameLength)

		stMid.lossCnt = 0
		stMid.lagPrev = ctrlMid.pitchL[stMid.nbSubfr-1]
		stMid.prevSignalType = int(stMid.indices.signalType)
		stMid.firstFrameAfterReset = false
		stMid.nFramesDecoded++

		if hasSide {
			condSide := codeIndependently
			if frameIndex > 0 {
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
			sideOut := make([]int16, frameLength)
			silkDecodeCore(stSide, &ctrlSide, sideOut, pulsesSide)
			silkUpdateOutBuf(stSide, sideOut)

			// Apply PLC glue frames for side channel.
			silkPLCGlueFrames(stSide, sideOut, frameLength)

			stSide.lossCnt = 0
			stSide.lagPrev = ctrlSide.pitchL[stSide.nbSubfr-1]
			stSide.prevSignalType = int(stSide.indices.signalType)
			stSide.firstFrameAfterReset = false
		}
		stSide.nFramesDecoded++

		// Track mid-only flag per frame (used for side-channel conditioning).
		d.prevDecodeOnlyMiddle = decodeOnlyMiddle
	}

	d.haveDecoded = true
	return midNative, frameLength, nil
}
