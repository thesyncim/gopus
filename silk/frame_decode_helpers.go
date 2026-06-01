package silk

import "github.com/thesyncim/gopus/rangecoding"

func initFrameDecodeState(st *decoderState, fsKHz, framesPerPacket, nbSubfr int) {
	st.nFramesDecoded = 0
	st.nFramesPerPacket = int32(framesPerPacket)
	st.nbSubfr = int32(nbSubfr)
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

func sideFrameCondCoding(frameIndex int, prevDecodeOnlyMiddle int32) int {
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

// fecOutputBuffer returns the multi-frame output accumulator for DecodeFEC. It
// is intentionally distinct from int16OutputBuffer (scratchOutInt16) so that a
// concealed sub-frame's recordPLCLossForState (which writes through
// scratchOutInt16) cannot clobber earlier sub-frames' decoded output.
func (d *Decoder) fecOutputBuffer(length int) []int16 {
	if d.scratchFECOut != nil && len(d.scratchFECOut) >= length {
		return d.scratchFECOut[:length]
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
	// libopus dec_API.c decodes VAD + LBRR-present flags for BOTH channels
	// first, then the per-frame LBRR flags symbol for both channels. The two
	// phases must not be interleaved per channel or the range decoder desyncs.
	decodeVADFlagsAndLBRRFlag(rd, stMid, framesPerPacket)
	decodeVADFlagsAndLBRRFlag(rd, stSide, framesPerPacket)
	decodeLBRRFlagsSymbol(rd, stMid, framesPerPacket)
	decodeLBRRFlagsSymbol(rd, stSide, framesPerPacket)
	d.skipStereoLBRRFrames(rd, stMid, stSide, framesPerPacket)
	return stMid, stSide, framesPerPacket, int(stMid.frameLength), fsKHz, nil
}

func (d *Decoder) skipMonoLBRRFrames(rd *rangecoding.Decoder, st *decoderState, framesPerPacket int) {
	if st == nil || rd == nil || st.LBRRFlag == 0 {
		return
	}
	frameLength := int(st.frameLength)
	for i := 0; i < framesPerPacket; i++ {
		if st.LBRRFlags[i] == 0 {
			continue
		}
		silkDecodeIndices(st, rd, true, lbrrCondCoding(st, i))
		pulses := d.pulseBuffer(frameLength)
		silkDecodePulsesWithScratch(rd, pulses, int(st.indices.signalType), int(st.indices.quantOffsetType), frameLength, st.scratchSumPulses, st.scratchNLshifts)
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
			frameLength := int(st.frameLength)
			pulses := d.pulseBuffer(frameLength)
			silkDecodePulsesWithScratch(rd, pulses, int(st.indices.signalType), int(st.indices.quantOffsetType), frameLength, st.scratchSumPulses, st.scratchNLshifts)
		}
	}
}

func (d *Decoder) decodeFrameCoreInto(
	st *decoderState,
	rd *rangecoding.Decoder,
	frameOut []int16,
	condCoding int,
	vad bool,
) decoderControl {
	ecStart := 0
	if rd != nil {
		ecStart = rd.Tell()
	}
	silkDecodeIndices(st, rd, vad, condCoding)
	frameLength := int(st.frameLength)
	pulses := d.pulseBuffer(frameLength)
	silkDecodePulsesWithScratch(rd, pulses, int(st.indices.signalType), int(st.indices.quantOffsetType), frameLength, st.scratchSumPulses, st.scratchNLshifts)

	var ctrl decoderControl
	silkDecodeParameters(st, &ctrl, condCoding)
	silkDecodeCore(st, &ctrl, frameOut, pulses)
	if rd != nil {
		ctrl.NumBits = int32(rd.Tell() - ecStart)
	}
	return ctrl
}

func (d *Decoder) finalizeDecodedChannelFrame(channel int, st *decoderState, ctrl *decoderControl, frameOut []int16, updateHistory bool) {
	if updateHistory {
		d.updateHistoryInt16(frameOut)
	}
	silkUpdateOutBuf(st, frameOut)
	if nativePostfilterEnabled {
		d.fireNativePostfilterHook(channel, st, ctrl, frameOut)
	}
	d.updateSILKPLCStateFromCtrl(channel, st, ctrl)
	if dredHooksEnabled {
		d.fireRawMonoFrameHook(channel, st, frameOut)
	}

	st.lossCnt = 0
	st.prevSignalType = int32(st.indices.signalType)
	st.firstFrameAfterReset = false
	d.applyCNG(channel, st, ctrl, frameOut)
	silkPLCGlueFrames(st, frameOut, len(frameOut))
	if st.nbSubfr > 0 {
		st.lagPrev = ctrl.pitchL[int(st.nbSubfr)-1]
	}
	// Cache the decoder control + signal type so optional decoder-side
	// post-processing (OSCE LACE / NoLACE) can read libopus' per-frame
	// PredCoef_Q12 / LTPCoef_Q14 / Gains_Q16 / pitchL out of the SILK
	// decoder after the frame finishes. Multi-frame packets retain only
	// the last 20 ms frame's ctrl, which matches the LACE/NoLACE per-frame
	// invocation cadence (libopus runs osce_enhance_frame at the bottom of
	// each silk_decode_frame call).
	if channel >= 0 && channel < len(d.lastFrameCtrl) {
		d.lastFrameCtrl[channel] = *ctrl
		d.lastFrameCtrlSignal[channel] = int32(st.indices.signalType)
		d.lastFrameCtrlValid[channel] = true
	}
	st.nFramesDecoded++
}

func (d *Decoder) decodeLBRRFrameInto(channel int, st *decoderState, rd *rangecoding.Decoder, frameIndex int, frameOut []int16, updateHistory bool) {
	ctrl := d.decodeFrameCoreInto(st, rd, frameOut, lbrrCondCoding(st, frameIndex), true)
	d.finalizeDecodedChannelFrame(channel, st, &ctrl, frameOut, updateHistory)
}

func (d *Decoder) maybeResetStereoSideChannel(decodeOnlyMiddle int, stSide *decoderState) {
	if decodeOnlyMiddle == 0 && d.prevDecodeOnlyMiddle == 1 {
		resetSideChannelState(stSide)
	}
}
