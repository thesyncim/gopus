package silk

import "github.com/thesyncim/gopus/rangecoding"

func initFrameDecodeState(st *decoderState, fsKHz, framesPerPacket, nbSubfr int) {
	st.nFramesDecoded = 0
	st.nFramesPerPacket = framesPerPacket
	st.nbSubfr = nbSubfr
	silkDecoderSetFs(st, fsKHz)
}

func frameCondCoding(frameIndex int) int {
	if frameIndex > 0 {
		return codeConditionally
	}
	return codeIndependently
}

func lbrrCondCoding(st *decoderState, frameIndex int) int {
	if frameIndex > 0 && st.LBRRFlags[frameIndex-1] != 0 {
		return codeConditionally
	}
	return codeIndependently
}

func sideFrameCondCoding(frameIndex, prevDecodeOnlyMiddle int) int {
	if frameIndex == 0 {
		return codeIndependently
	}
	if prevDecodeOnlyMiddle == 1 {
		return codeIndependentlyNoLtpScaling
	}
	return codeConditionally
}

func (d *Decoder) pulseBuffer(frameLength int) []int16 {
	pulsesLen := roundUpShellFrame(frameLength)
	if d.scratchPulses != nil && len(d.scratchPulses) >= pulsesLen {
		return d.scratchPulses[:pulsesLen]
	}
	return make([]int16, pulsesLen)
}

func (d *Decoder) int16OutputBuffer(length int) []int16 {
	if d.scratchOutInt16 != nil && len(d.scratchOutInt16) >= length {
		return d.scratchOutInt16[:length]
	}
	return make([]int16, length)
}

func (d *Decoder) float32OutputBuffer(length int) []float32 {
	if d.scratchOutput != nil && len(d.scratchOutput) >= length {
		return d.scratchOutput[:length]
	}
	return make([]float32, length)
}

func (d *Decoder) prepareMonoFramePacket(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	duration FrameDuration,
) (st *decoderState, framesPerPacket, fsKHz int, err error) {
	if rd == nil {
		return nil, 0, 0, ErrDecodeFailed
	}
	d.SetRangeDecoder(rd)
	config := GetBandwidthConfig(bandwidth)
	fsKHz = config.SampleRate / 1000

	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return nil, 0, 0, err
	}

	st = &d.state[0]
	initFrameDecodeState(st, fsKHz, framesPerPacket, nbSubfr)
	decodeVADAndLBRRFlags(rd, st, framesPerPacket)
	d.skipMonoLBRRFrames(rd, st, framesPerPacket)
	return st, framesPerPacket, fsKHz, nil
}

func (d *Decoder) prepareStereoFramePacket(
	rd *rangecoding.Decoder,
	bandwidth Bandwidth,
	duration FrameDuration,
) (stMid, stSide *decoderState, framesPerPacket, frameLength, fsKHz int, err error) {
	if rd == nil {
		return nil, nil, 0, 0, 0, ErrDecodeFailed
	}
	d.SetRangeDecoder(rd)
	config := GetBandwidthConfig(bandwidth)
	fsKHz = config.SampleRate / 1000

	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return nil, nil, 0, 0, 0, err
	}

	stMid = &d.state[0]
	stSide = &d.state[1]
	initFrameDecodeState(stMid, fsKHz, framesPerPacket, nbSubfr)
	initFrameDecodeState(stSide, fsKHz, framesPerPacket, nbSubfr)
	decodeVADAndLBRRFlags(rd, stMid, framesPerPacket)
	decodeVADAndLBRRFlags(rd, stSide, framesPerPacket)
	d.skipStereoLBRRFrames(rd, stMid, stSide, framesPerPacket)
	return stMid, stSide, framesPerPacket, stMid.frameLength, fsKHz, nil
}

func (d *Decoder) skipMonoLBRRFrames(rd *rangecoding.Decoder, st *decoderState, framesPerPacket int) {
	if st == nil || rd == nil || st.LBRRFlag == 0 {
		return
	}
	for i := 0; i < framesPerPacket; i++ {
		if st.LBRRFlags[i] == 0 {
			continue
		}
		silkDecodeIndices(st, rd, true, lbrrCondCoding(st, i))
		pulses := d.pulseBuffer(st.frameLength)
		silkDecodePulsesWithScratch(rd, pulses, int(st.indices.signalType), int(st.indices.quantOffsetType), st.frameLength, st.scratchSumPulses, st.scratchNLshifts)
	}
}

func (d *Decoder) skipStereoLBRRFrames(rd *rangecoding.Decoder, stMid, stSide *decoderState, framesPerPacket int) {
	if rd == nil || stMid == nil || stSide == nil {
		return
	}
	if stMid.LBRRFlag == 0 && stSide.LBRRFlag == 0 {
		return
	}

	var predQ13 [2]int32
	for i := 0; i < framesPerPacket; i++ {
		for ch := 0; ch < 2; ch++ {
			st := &d.state[ch]
			if st.LBRRFlags[i] == 0 {
				continue
			}
			if ch == 0 {
				silkStereoDecodePred(rd, predQ13[:])
				if stSide.LBRRFlags[i] == 0 {
					_ = silkStereoDecodeMidOnly(rd)
				}
			}
			silkDecodeIndices(st, rd, true, lbrrCondCoding(st, i))
			pulses := d.pulseBuffer(st.frameLength)
			silkDecodePulsesWithScratch(rd, pulses, int(st.indices.signalType), int(st.indices.quantOffsetType), st.frameLength, st.scratchSumPulses, st.scratchNLshifts)
		}
	}
}

func (d *Decoder) decodeFrameCoreInto(
	st *decoderState,
	rd *rangecoding.Decoder,
	frameOut []int16,
	condCoding int,
	vad bool,
	frameIndex int,
	trace TraceCallback,
) decoderControl {
	silkDecodeIndices(st, rd, vad, condCoding)
	pulses := d.pulseBuffer(st.frameLength)
	silkDecodePulsesWithScratch(rd, pulses, int(st.indices.signalType), int(st.indices.quantOffsetType), st.frameLength, st.scratchSumPulses, st.scratchNLshifts)

	var ctrl decoderControl
	silkDecodeParameters(st, &ctrl, condCoding)
	if trace != nil {
		silkDecodeCoreWithTrace(st, &ctrl, frameOut, pulses, frameIndex, trace)
	} else {
		silkDecodeCore(st, &ctrl, frameOut, pulses)
	}
	return ctrl
}

func (d *Decoder) finalizeDecodedChannelFrame(channel int, st *decoderState, ctrl *decoderControl, frameOut []int16, updateHistory bool) {
	if updateHistory {
		d.updateHistoryInt16(frameOut)
	}
	silkUpdateOutBuf(st, frameOut)
	d.updateSILKPLCStateFromCtrl(channel, st, ctrl)

	st.lossCnt = 0
	st.prevSignalType = int(st.indices.signalType)
	st.firstFrameAfterReset = false
	d.applyCNG(channel, st, ctrl, frameOut)
	silkPLCGlueFrames(st, frameOut, len(frameOut))
	if st.nbSubfr > 0 {
		st.lagPrev = ctrl.pitchL[st.nbSubfr-1]
	}
	st.nFramesDecoded++
}

func (d *Decoder) decodeLBRRFrameInto(channel int, st *decoderState, rd *rangecoding.Decoder, frameIndex int, frameOut []int16, updateHistory bool) {
	ctrl := d.decodeFrameCoreInto(st, rd, frameOut, lbrrCondCoding(st, frameIndex), true, frameIndex, nil)
	d.finalizeDecodedChannelFrame(channel, st, &ctrl, frameOut, updateHistory)
}

func (d *Decoder) maybeResetStereoSideChannel(decodeOnlyMiddle int, stSide *decoderState) {
	if decodeOnlyMiddle == 0 && d.prevDecodeOnlyMiddle == 1 {
		resetSideChannelState(stSide)
	}
}
