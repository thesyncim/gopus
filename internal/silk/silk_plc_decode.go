package silk

import (
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/plc"
)

// decodePLC generates concealment audio for a lost mono packet.
func (d *Decoder) decodePLC(bandwidth Bandwidth, frameSizeSamples int) ([]float32, error) {
	if frameSizeSamples <= 0 {
		return nil, ErrDecodeFailed
	}
	output := make([]float32, frameSizeSamples)
	n, err := d.DecodePLCInto(bandwidth, frameSizeSamples, output)
	if err != nil {
		return nil, err
	}
	return output[:n], nil
}

// DecodePLCInto generates mono SILK concealment audio for a lost packet and
// writes the resampled API-rate PCM into output. It is the zero-allocation
// counterpart of decodePLC: the caller owns the destination buffer, which must
// hold at least frameSizeSamples samples. Returns the number of samples written.
func (d *Decoder) DecodePLCInto(bandwidth Bandwidth, frameSizeSamples int, output []float32) (int, error) {
	if bandwidth > BandwidthWideband {
		return 0, ErrInvalidBandwidth
	}
	if frameSizeSamples <= 0 || len(output) < frameSizeSamples {
		return 0, ErrDecodeFailed
	}
	d.preparePLCFrameDecodeState(bandwidth, frameSizeSamples, 1)
	// Get fade factor for this loss
	fadeFactor := d.plcState.RecordLoss()

	// Get native sample count from the API-rate frame size.
	config := GetBandwidthConfig(bandwidth)
	nativeSamples := frameSizeSamples * config.SampleRate / d.outputSampleRate()

	// Generate concealment at native rate.
	// Use LTP-aware concealment whenever per-channel SILK PLC state is valid.
	// Fall back to legacy concealment only when required state is unavailable.
	var concealed []float32
	var concealedQ0 []int16
	hookLagPrev := 0
	usedDeepPLCHook := false
	if state := d.ensureSILKPLCState(0); state != nil && d.state[0].nbSubfr > 0 {
		// Use the per-channel decoder view so the PLC reads the SILK frame's
		// actual LPC order (10 for NB/MB, 16 for WB) and its exact integer
		// sLPC_Q14_buf history, matching libopus silk_PLC_conceal. The
		// Decoder-level accessor reports a stale order and would force the
		// float-derived LPC fallback, corrupting unvoiced concealment.
		concealedQ0 = d.concealSILKFrame(0, &d.state[0], nativeSamples)
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
			hookLagPrev = lag
		}
	} else {
		concealed = plc.ConcealSILK(d, nativeSamples, fadeFactor)
	}
	if dredHooksEnabled && d.hasDeepPLCLossMonoHook() {
		if len(concealed) < nativeSamples {
			concealed = make([]float32, nativeSamples)
		}
		ok, lagPrev := d.fireDeepPLCLossMonoHook(concealed)
		if ok {
			usedDeepPLCHook = true
			if lagPrev > 0 {
				hookLagPrev = lagPrev
			}
		}
	}
	if hookLagPrev > 0 {
		d.state[0].lagPrev = int32(hookLagPrev)
	} else if state := d.ensureSILKPLCState(0); state != nil {
		if lag := int((state.PitchLQ8 + 128) >> 8); lag > 0 {
			d.state[0].lagPrev = int32(lag)
		}
	}
	// libopus silk_PLC updates LPCNet with each classical concealed 10 ms
	// channel-0 block before CNG and PLC glue. Deep PLC instead calls
	// lpcnet_plc_conceal and must not feed its rendered samples back as updates.
	if !usedDeepPLCHook && len(concealedQ0) >= nativeSamples {
		d.fireRawMonoLossFrameHook(0, &d.state[0], concealedQ0[:nativeSamples])
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

	// Expose the native-rate concealed PCM via LatestNativeMono so the optional
	// OSCE BWE forward pass can read the pre-resample lowband during PLC.
	// libopus enables OSCE_MODE_SILK_BBWE on `data == NULL` whenever the
	// internal sample rate is 16 kHz; the gopus equivalent needs access to the
	// native-rate samples for the 16k -> 48k BWE input.
	if nativeLowbandCaptureEnabled {
		if cap(d.scratchOutInt16) >= len(concealed) {
			buf := d.scratchOutInt16[:len(concealed)]
			for i, v := range concealed {
				buf[i] = float32ToInt16(v)
			}
		}
		d.lastNativeMonoLen = int32(len(concealed))
		d.lastNativeMonoFsKHz = int32(config.SampleRate / 1000)
		d.lastNativeStereoLen = 0
		d.lastNativeStereoFsKHz = 0
		d.lastNativeMidLen = 0
		d.lastNativeMidFsKHz = 0
	}

	// Resample to the decoder API rate using the same mono sMid buffering cadence as good frames.
	duration := d.frameDurationFromAPISamples(frameSizeSamples)
	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil || framesPerPacket <= 0 {
		resampler := d.GetResampler(bandwidth)
		n := resampler.ProcessInto(d.BuildMonoResamplerInput(concealed), output)
		d.captureMonoPLCLowband(output[:n])
		return n, nil
	}
	frameLength := nbSubfr * subFrameLengthMs * config.SampleRate / 1000
	if frameLength <= 0 || frameLength*framesPerPacket != len(concealed) {
		frameLength = len(concealed) / framesPerPacket
	}

	resampler := d.GetResampler(bandwidth)
	outputOffset := 0
	captureI16 := d.plcLowbandCaptureArm && len(d.plcLowbandCapture) > 0
	for f := range framesPerPacket {
		start := f * frameLength
		end := start + frameLength
		if start < 0 || end > len(concealed) || frameLength == 0 {
			break
		}
		if dredHooksEnabled && usedDeepPLCHook && len(d.scratchOutInt16) >= end {
			frameQ0 := d.scratchOutInt16[start:end]
			resamplerInput := d.BuildMonoResamplerInputInt16(frameQ0)
			if captureI16 {
				outputOffset += d.resamplePLCFrameCaptureInt16(resampler, resamplerInput, output[outputOffset:], outputOffset)
			} else {
				outputOffset += resampler.ProcessInt16Into(resamplerInput, output[outputOffset:])
			}
			continue
		}
		if captureI16 {
			// Resample the int16-recovered concealment (float concealed = q/32768,
			// so float32ToInt16 recovers q exactly) so the captured lowband is the
			// integer-exact resampler output the FIXED_POINT path needs, while the
			// float output is bit-identical to the float-input resampler path.
			frameI16 := d.plcConcealInt16Scratch(end - start)
			for i := start; i < end; i++ {
				frameI16[i-start] = float32ToInt16(concealed[i])
			}
			resamplerInput := d.BuildMonoResamplerInputInt16(frameI16)
			outputOffset += d.resamplePLCFrameCaptureInt16(resampler, resamplerInput, output[outputOffset:], outputOffset)
			continue
		}
		frame := concealed[start:end]
		resamplerInput := d.BuildMonoResamplerInput(frame)
		outputOffset += resampler.ProcessInto(resamplerInput, output[outputOffset:])
	}

	if captureI16 {
		d.plcLowbandCaptured = min(outputOffset, len(d.plcLowbandCapture))
	}
	return outputOffset, nil
}

// plcConcealInt16Scratch returns a reusable int16 scratch buffer of length n for
// converting the mono PLC concealment to int16 before resampling.
func (d *Decoder) plcConcealInt16Scratch(n int) []int16 {
	if cap(d.plcConcealI16) < n {
		d.plcConcealI16 = make([]int16, n)
	}
	d.plcConcealI16 = d.plcConcealI16[:n]
	return d.plcConcealI16
}

func (d *Decoder) plcConcealQ0For(channel, n int) []int16 {
	buf := &d.plcConcealQ0[channel]
	if cap(*buf) < n {
		*buf = make([]int16, n)
	}
	return (*buf)[:n]
}

// resamplePLCFrameCaptureInt16 resamples one int16 PLC frame, writing the float
// output to out and the native int16 resampler output into the armed
// plcLowbandCapture buffer at outputOffset. It returns the sample count written.
func (d *Decoder) resamplePLCFrameCaptureInt16(resampler *LibopusResampler, resamplerInput []int16, out []float32, outputOffset int) int {
	if outputOffset < 0 || outputOffset >= len(d.plcLowbandCapture) {
		return resampler.ProcessInt16Into(resamplerInput, out)
	}
	dst := d.plcLowbandCapture[outputOffset:]
	return resampler.ProcessInt16IntoBoth(resamplerInput, out, dst)
}

// captureMonoPLCLowband captures a fallback mono PLC lowband (the degenerate
// frameParams<=0 path) by re-deriving the int16 from the float output (out =
// int16/32768, so float32ToInt16 recovers it). Only runs when capture is armed.
func (d *Decoder) captureMonoPLCLowband(out []float32) {
	if !d.plcLowbandCaptureArm || len(d.plcLowbandCapture) == 0 {
		return
	}
	n := min(len(out), len(d.plcLowbandCapture))
	for i := range n {
		d.plcLowbandCapture[i] = float32ToInt16(out[i])
	}
	d.plcLowbandCaptured = n
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

// recordPLCLossForState applies the per-channel state updates for a concealed
// frame: it bumps the loss counter, converts the concealment to int16, updates
// the LTP/output history and outBuf, runs comfort-noise generation and PLC
// frame-energy gluing, and writes the (possibly CNG-modified) result back into
// concealed. Mirrors the lost-frame bookkeeping of libopus silk/decode_frame.c
// (silk_CNG and silk_PLC_glue_frames on a concealed frame).
func (d *Decoder) recordPLCLossForState(st *decoderState, concealed []float32) {
	if st == nil {
		return
	}
	channel := 0
	if st == &d.state[1] {
		channel = 1
	}
	if len(concealed) == 0 {
		st.lossCnt++
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
	d.finishLostFrame(channel, st, tmp)

	const scale = float32(1.0 / 32768.0)
	for i := range tmp {
		concealed[i] = float32(tmp[i]) * scale
	}
}

// finishLostFrame applies the state updates silk_PLC and silk_decode_frame
// make after concealing a frame into pOut: the loss count, the output buffer,
// comfort noise generation (silk_CNG) and the energy capture of
// silk_PLC_glue_frames, in that order. frame is modified in place by CNG.
func (d *Decoder) finishLostFrame(channel int, st *decoderState, frame []int16) {
	d.fireNativeLossHook(channel)
	st.lossCnt++
	d.updateHistoryInt16(frame)
	silkUpdateOutBuf(st, frame)
	d.applyCNG(channel, st, nil, frame)
	silkPLCGlueFrames(st, frame, len(frame))
}

func (d *Decoder) applyDeepPLCHistoryMono(st *decoderState, concealed []float32) {
	if st == nil || len(concealed) == 0 {
		return
	}
	order := int(st.lpcOrder)
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
	start := max(len(concealed)-order, 0)
	historyIdx := 0
	for i := start; i < len(concealed) && historyIdx < order; i++ {
		sampleQ0 := int32(float32ToInt16(concealed[i]))
		scaled := float32(sampleQ0) * float32(1<<24) / float32(prevGainQ10)
		history[historyIdx] = floorHalfPlusF32ToInt32(scaled)
		historyIdx++
	}
	if historyIdx == 0 {
		return
	}
	setSLPCQ14HistoryQ14(st, history[:historyIdx])
}

// ApplyDeepPLCLossMono records a mono lost frame whose concealment was produced
// externally (the deep/neural PLC), writing concealed into rendered and updating
// the channel-0 SILK loss state, pitch lag and LPC history so a following good
// frame glues correctly. It still advances the standard SILK PLC state to keep
// the pitch lag consistent. Returns the number of samples written. Used by the
// hybrid DRED/DeepPLC path; the standard SILK concealment is decodePLC.
func (d *Decoder) ApplyDeepPLCLossMono(concealed, rendered []float32, lagPrev int) int {
	if d == nil || len(concealed) == 0 || len(rendered) < len(concealed) {
		return 0
	}
	st := &d.state[0]
	var plcLagPrev int
	if plcState := d.ensureSILKPLCState(0); plcState != nil && st.nbSubfr > 0 {
		if view := d.plcDecoderView(0); view != nil {
			_ = plc.ConcealSILKWithLTP(view, plcState, int(max(0, st.lossCnt)), len(concealed))
			plcLagPrev = int((plcState.PitchLQ8 + 128) >> 8)
		}
	}
	tmp := rendered[:len(concealed)]
	copy(tmp, concealed)
	d.recordPLCLossForState(st, tmp)
	switch {
	case plcLagPrev > 0:
		st.lagPrev = int32(plcLagPrev)
	case lagPrev > 0:
		st.lagPrev = int32(lagPrev)
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

	order := int(d.lpcOrder)
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

	d.syncOutputHistory()
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
	if frameSizeSamples <= 0 {
		return nil, ErrDecodeFailed
	}
	output := make([]float32, frameSizeSamples*2)
	n, err := d.DecodePLCStereoInto(bandwidth, frameSizeSamples, output)
	if err != nil {
		return nil, err
	}
	return output[:n], nil
}

func (d *Decoder) decodePLCStereoToMono(bandwidth Bandwidth, frameSizeSamples int) ([]float32, error) {
	if frameSizeSamples <= 0 {
		return nil, ErrDecodeFailed
	}
	output := make([]float32, frameSizeSamples)
	n, err := d.DecodePLCStereoToMonoInto(bandwidth, frameSizeSamples, output)
	if err != nil {
		return nil, err
	}
	return output[:n], nil
}

// DecodeMonoToStereoPLCInto preserves the mono-packet stereo-output routing
// used by DecodeMonoToStereo while writing into caller-owned PCM.
func (d *Decoder) DecodeMonoToStereoPLCInto(bandwidth Bandwidth, frameSizeSamples int, stereoToMono bool, output []float32) (int, error) {
	if bandwidth > BandwidthWideband {
		return 0, ErrInvalidBandwidth
	}
	if frameSizeSamples <= 0 || len(output) < frameSizeSamples*2 {
		return 0, ErrDecodeFailed
	}
	useStereoHistory := d.ShouldUseStereoToMonoHistory(bandwidth, stereoToMono)
	d.handleBandwidthChange(bandwidth)
	if useStereoHistory {
		return d.DecodePLCStereoInto(bandwidth, frameSizeSamples, output)
	}
	mono := d.plcStereoFloatScratch(&d.plcMonoDup, frameSizeSamples)
	n, err := d.DecodePLCInto(bandwidth, frameSizeSamples, mono)
	if err != nil {
		return 0, err
	}
	duplicateMonoFloat32ToStereo(output, mono, n)
	return n * 2, nil
}

// DecodePLCStereoInto generates stereo SILK concealment audio for a lost packet
// and writes the interleaved [L0,R0,L1,R1,...] API-rate PCM into output. It is
// the zero-allocation counterpart of decodePLCStereo: the caller owns the
// destination buffer, which must hold at least 2*frameSizeSamples samples.
// Returns the number of interleaved samples written.
func (d *Decoder) DecodePLCStereoInto(bandwidth Bandwidth, frameSizeSamples int, output []float32) (int, error) {
	return d.decodePLCStereoInto(bandwidth, frameSizeSamples, output, 2)
}

// DecodePLCStereoToMonoInto generates concealment for a stereo SILK stream and
// writes its mid channel at the decoder API rate. Both mid and side PLC states
// advance, while the side signal is not converted to left/right because
// libopus emits the mid channel when the API decoder has one output channel.
// The caller owns output, which must hold frameSizeSamples samples.
func (d *Decoder) DecodePLCStereoToMonoInto(bandwidth Bandwidth, frameSizeSamples int, output []float32) (int, error) {
	return d.decodePLCStereoInto(bandwidth, frameSizeSamples, output, 1)
}

func (d *Decoder) decodePLCStereoInto(bandwidth Bandwidth, frameSizeSamples int, output []float32, apiChannels int) (int, error) {
	if bandwidth > BandwidthWideband {
		return 0, ErrInvalidBandwidth
	}
	if apiChannels != 1 && apiChannels != 2 {
		return 0, ErrDecodeFailed
	}
	if frameSizeSamples <= 0 || len(output) < frameSizeSamples*apiChannels {
		return 0, ErrDecodeFailed
	}

	// Get native sample count from the API-rate frame size.
	config := GetBandwidthConfig(bandwidth)
	nativeSamples := frameSizeSamples * config.SampleRate / d.outputSampleRate()
	// A frame size shorter than the stereo MS->LR interpolation window
	// (8 ms = 64/96/128 native samples for NB/MB/WB) cannot be interpolated
	// safely. Every valid SILK frame size is >= 10 ms, so this never fires on
	// real input.
	minStereoSamples := stereoInterpLenMs * (config.SampleRate / 1000)
	if nativeSamples < minStereoSamples {
		return 0, nil
	}
	d.preparePLCFrameDecodeState(bandwidth, frameSizeSamples, 2)

	// Get fade factor for this loss
	fadeFactor := d.plcState.RecordLoss()

	// libopus stereo PLC keeps operating in mid/side space and only converts
	// back to left/right through silk_stereo_MS_to_LR before resampling.
	// Our decoder states 0/1 track mid/side, not left/right.
	hasSide := d.prevDecodeOnlyMiddle == 0
	mid := d.plcStereoFloatScratch(&d.plcMidNative, nativeSamples)
	side := d.plcStereoFloatScratch(&d.plcSideNative, nativeSamples)

	midState := d.ensureSILKPLCState(0)
	sideState := d.ensureSILKPLCState(1)
	midView := d.plcDecoderView(0)
	sideView := d.plcDecoderView(1)
	usedDeepPLCHook := false
	hookLagPrev := 0
	var midQ0 []int16
	if midState != nil && midView != nil && d.state[0].nbSubfr > 0 {
		midQ0 = d.concealSILKFrame(0, &d.state[0], nativeSamples)
		scale := float32(1.0 / 32768.0)
		for i := 0; i < nativeSamples && i < len(midQ0); i++ {
			mid[i] = float32(midQ0[i]) * scale
		}
		if lag := int((midState.PitchLQ8 + 128) >> 8); lag > 0 {
			hookLagPrev = lag
		}
	} else {
		// Legacy fallback when richer PLC state is unavailable.
		left, right := plc.ConcealSILKStereo(d, nativeSamples, fadeFactor)
		copy(mid, left)
		if hasSide {
			copy(side, right)
		}
	}
	if dredHooksEnabled && d.hasDeepPLCLossMonoHook() {
		ok, lagPrev := d.fireDeepPLCLossMonoHook(mid)
		if ok {
			usedDeepPLCHook = true
			if lagPrev > 0 {
				hookLagPrev = lagPrev
			}
		}
	}
	if usedDeepPLCHook && hookLagPrev > 0 {
		d.state[0].lagPrev = int32(hookLagPrev)
	} else if !usedDeepPLCHook && hookLagPrev > 0 {
		d.state[0].lagPrev = int32(hookLagPrev)
	}
	if !usedDeepPLCHook && len(midQ0) >= nativeSamples {
		// Stereo SILK keeps its LPCNet PLC state on the mid/channel-0 path.
		d.fireRawMonoLossFrameHook(0, &d.state[0], midQ0[:nativeSamples])
	}
	if hasSide && sideState != nil && sideView != nil && d.state[1].nbSubfr > 0 {
		sideQ0 := d.concealSILKFrame(1, &d.state[1], nativeSamples)
		scale := float32(1.0 / 32768.0)
		for i := 0; i < nativeSamples && i < len(sideQ0); i++ {
			side[i] = float32(sideQ0[i]) * scale
		}
		if lag := int((sideState.PitchLQ8 + 128) >> 8); lag > 0 {
			d.state[1].lagPrev = int32(lag)
		}
	}

	// Update decoder state for the concealed internal channels before MS->LR.
	if usedDeepPLCHook {
		d.applyDeepPLCHistoryMono(&d.state[0], mid)
	}
	d.recordPLCLossForState(&d.state[0], mid)
	if usedDeepPLCHook {
		d.state[0].plcSkipRecoveryGlue = true
	}
	d.state[0].lastGainIndex = 10
	if hasSide {
		d.recordPLCLossForState(&d.state[1], side)
		d.state[1].lastGainIndex = 10
	}

	// Prepare the concealed mid/side history. A mono API output receives the
	// mid channel directly; stereo output converts mid/side to left/right.
	midFrame, sideFrame, ok := d.stereoFrameScratch(nativeSamples)
	if !ok {
		midFrame = make([]int16, nativeSamples+2)
		sideFrame = make([]int16, nativeSamples+2)
	}
	clear(midFrame[:nativeSamples+2])
	clear(sideFrame[:nativeSamples+2])
	for i := range nativeSamples {
		midFrame[i+2] = float32ToInt16(mid[i])
		if hasSide {
			sideFrame[i+2] = float32ToInt16(side[i])
		}
	}
	if apiChannels == 1 {
		// silk_Decode buffers two samples of the mid channel and resamples from
		// sMid[1], followed by the current frame except for its final sample.
		// See silk/dec_API.c's non-stereo-output branch after SILK PLC.
		copy(midFrame[:2], d.stereo.sMid[:])
		copy(d.stereo.sMid[:], midFrame[nativeSamples:nativeSamples+2])
		resampler := d.GetResamplerForChannel(bandwidth, 0)
		captureI16 := d.plcLowbandCaptureArm && len(d.plcLowbandCapture) > 0
		if captureI16 {
			midI16 := d.plcStereoLeftI16Scratch(frameSizeSamples)
			n := resampler.ProcessInt16IntoBoth(midFrame[1:nativeSamples+1], output, midI16)
			n = min(n, len(midI16), len(d.plcLowbandCapture))
			copy(d.plcLowbandCapture, midI16[:n])
			d.plcLowbandCaptured = n
			return n, nil
		}
		n := resampler.ProcessInt16Into(midFrame[1:nativeSamples+1], output)
		return n, nil
	}
	// Convert concealed mid/side to left/right using the saved stereo predictor.
	d.plcPredQ13[0] = int32(d.stereo.predPrevQ13[0])
	d.plcPredQ13[1] = int32(d.stereo.predPrevQ13[1])
	silkStereoMSToLR(&d.stereo, midFrame, sideFrame, d.plcPredQ13[:], config.SampleRate/1000, nativeSamples)

	// Resample left/right channels to API rate.
	leftResampler := d.GetResamplerForChannel(bandwidth, 0)
	rightResampler := d.GetResamplerForChannel(bandwidth, 1)
	leftUp := d.plcStereoFloatScratch(&d.plcLeftUp, frameSizeSamples)
	rightUp := d.plcStereoFloatScratch(&d.plcRightUp, frameSizeSamples)
	captureI16 := d.plcLowbandCaptureArm && len(d.plcLowbandCapture) > 0
	var leftI16, rightI16 []int16
	var nLeft, nRight int
	if captureI16 {
		leftI16 = d.plcStereoLeftI16Scratch(frameSizeSamples)
		rightI16 = d.plcStereoRightI16Scratch(frameSizeSamples)
		nLeft = leftResampler.ProcessInt16IntoBoth(midFrame[1:nativeSamples+1], leftUp, leftI16)
		nRight = rightResampler.ProcessInt16IntoBoth(sideFrame[1:nativeSamples+1], rightUp, rightI16)
	} else {
		nLeft = leftResampler.ProcessInt16Into(midFrame[1:nativeSamples+1], leftUp)
		nRight = rightResampler.ProcessInt16Into(sideFrame[1:nativeSamples+1], rightUp)
	}
	if nRight < nLeft {
		nLeft = nRight
	}
	if nLeft < 0 {
		nLeft = 0
	}

	for i := 0; i < nLeft; i++ {
		output[i*2] = leftUp[i]
		output[i*2+1] = rightUp[i]
	}
	if captureI16 {
		filled := 0
		for i := 0; i < nLeft && 2*i+1 < len(d.plcLowbandCapture); i++ {
			d.plcLowbandCapture[2*i] = leftI16[i]
			d.plcLowbandCapture[2*i+1] = rightI16[i]
			filled = 2 * (i + 1)
		}
		d.plcLowbandCaptured = filled
	}

	return nLeft * 2, nil
}

// plcStereoFloatScratch returns a zeroed slice of length n backed by *buf,
// growing the backing buffer if necessary. Clearing the active slice preserves
// the libopus zero tail when concealment writes only part of the output.
func (d *Decoder) plcStereoFloatScratch(buf *[]float32, n int) []float32 {
	if n < 0 {
		n = 0
	}
	if cap(*buf) < n {
		*buf = make([]float32, n)
	}
	s := (*buf)[:n]
	clear(s)
	return s
}

func (d *Decoder) plcStereoLeftI16Scratch(n int) []int16 {
	if cap(d.plcStereoLI16) < n {
		d.plcStereoLI16 = make([]int16, n)
	}
	d.plcStereoLI16 = d.plcStereoLI16[:n]
	return d.plcStereoLI16
}

func (d *Decoder) plcStereoRightI16Scratch(n int) []int16 {
	if cap(d.plcStereoRI16) < n {
		d.plcStereoRI16 = make([]int16, n)
	}
	d.plcStereoRI16 = d.plcStereoRI16[:n]
	return d.plcStereoRI16
}

func float32ToInt16(v float32) int16 {
	return opusmath.Float32ToInt16(v)
}

// interleaveStereoFloat32 writes left[i] and right[i] to dst[2i] and dst[2i+1];
// dst holds 2*len(left) samples and right at least len(left).
func interleaveStereoFloat32(dst, left, right []float32) {
	right = right[:len(left)]
	for i, l := range left {
		pair := (*[2]float32)(dst[2*i : 2*i+2])
		pair[0] = l
		pair[1] = right[i]
	}
}
