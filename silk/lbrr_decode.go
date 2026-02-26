// Package silk implements LBRR (Low Bitrate Redundancy) decoding for FEC.
// LBRR provides forward error correction by including redundant data
// for the previous frame at a lower quality in the current packet.
//
// Reference: libopus silk/decode_frame.c, silk/dec_API.c

package silk

import (
	"github.com/thesyncim/gopus/plc"
	"github.com/thesyncim/gopus/rangecoding"
)

// DecodeFEC decodes LBRR (Low Bitrate Redundancy) frames for Forward Error Correction.
// This function decodes the FEC data from a packet to recover a lost frame.
//
// Parameters:
//   - data: The Opus packet data containing LBRR
//   - bandwidth: Audio bandwidth (NB/MB/WB)
//   - frameSizeSamples: Expected frame size in samples at 48kHz
//   - stereo: Whether the packet contains stereo data
//   - outputChannels: Number of output channels (1 or 2)
//
// Returns decoded samples at 48kHz, or an error if no LBRR data available.
//
// Reference: libopus silk/dec_API.c silk_Decode with lostFlag=FLAG_DECODE_LBRR
func (d *Decoder) DecodeFEC(
	data []byte,
	bandwidth Bandwidth,
	frameSizeSamples int,
	stereo bool,
	outputChannels int,
) ([]float32, error) {
	if data == nil || len(data) == 0 {
		return nil, ErrDecodeFailed
	}

	// Keep SILK bandwidth/resampler transition cadence aligned with normal decode.
	d.NotifyBandwidthChange(bandwidth)

	// Initialize range decoder
	var rd rangecoding.Decoder
	rd.Init(data)

	// Get frame parameters
	duration := FrameDurationFromTOC(frameSizeSamples)
	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return nil, err
	}

	config := GetBandwidthConfig(bandwidth)
	fsKHz := config.SampleRate / 1000

	// Set up decoder state for FEC decoding
	stMid := &d.state[0]
	stMid.nFramesDecoded = 0
	stMid.nFramesPerPacket = framesPerPacket
	stMid.nbSubfr = nbSubfr
	silkDecoderSetFs(stMid, fsKHz)

	if stereo && outputChannels == 2 {
		stSide := &d.state[1]
		stSide.nFramesDecoded = 0
		stSide.nFramesPerPacket = framesPerPacket
		stSide.nbSubfr = nbSubfr
		silkDecoderSetFs(stSide, fsKHz)
	}

	// Decode VAD and LBRR flags
	decodeVADAndLBRRFlags(&rd, stMid, framesPerPacket)

	if stereo && outputChannels == 2 {
		decodeVADAndLBRRFlags(&rd, &d.state[1], framesPerPacket)
	}

	// Decode FEC/LBRR frames. Match libopus decode_fec cadence:
	// if a packet frame has no LBRR, decode that frame as loss concealment.
	frameLength := stMid.frameLength
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
	lastFrameLost := false

	// Decode each frame using LBRR when present, otherwise run PLC.
	for i := 0; i < framesPerPacket; i++ {
		if stMid.LBRRFlags[i] == 0 {
			frameOut := outInt16[i*frameLength : (i+1)*frameLength]
			d.decodeFECLostMonoFrameInto(stMid, frameOut)
			d.syncLegacyPLCState(stMid, frameOut)
			stMid.nFramesDecoded++
			lastFrameLost = true
			continue
		}

		// Decode LBRR indices and pulses
		condCoding := codeIndependently
		if i > 0 && stMid.LBRRFlags[i-1] != 0 {
			condCoding = codeConditionally
		}

		// Stereo: decode stereo predictor for LBRR
		if stereo && outputChannels == 2 {
			predQ13 := make([]int32, 2)
			silkStereoDecodePred(&rd, predQ13)
			if d.state[1].LBRRFlags[i] == 0 {
				_ = silkStereoDecodeMidOnly(&rd)
			}
		}

		// Decode LBRR indices (same as regular indices but with decode_LBRR=true)
		vadFlag := true // LBRR always uses VAD=true for signal type
		silkDecodeIndices(stMid, &rd, vadFlag, condCoding)

		// Decode LBRR pulses
		pulsesLen := roundUpShellFrame(stMid.frameLength)
		var pulses []int16
		if d.scratchPulses != nil && len(d.scratchPulses) >= pulsesLen {
			pulses = d.scratchPulses[:pulsesLen]
			for j := range pulses {
				pulses[j] = 0
			}
		} else {
			pulses = make([]int16, pulsesLen)
		}
		silkDecodePulsesWithScratch(&rd, pulses, int(stMid.indices.signalType), int(stMid.indices.quantOffsetType), stMid.frameLength, stMid.scratchSumPulses, stMid.scratchNLshifts)

		// Decode frame
		var ctrl decoderControl
		silkDecodeParameters(stMid, &ctrl, condCoding)

		frameOut := outInt16[i*frameLength : (i+1)*frameLength]
		silkDecodeCore(stMid, &ctrl, frameOut, pulses)
		d.updateHistoryInt16(frameOut)
		silkUpdateOutBuf(stMid, frameOut)
		d.updateSILKPLCStateFromCtrl(0, stMid, &ctrl)

		// Apply PLC glue frames for smooth transition
		stMid.lossCnt = 0
		stMid.prevSignalType = int(stMid.indices.signalType)
		stMid.firstFrameAfterReset = false
		d.applyCNG(0, stMid, &ctrl, frameOut)
		silkPLCGlueFrames(stMid, frameOut, frameLength)
		stMid.lagPrev = ctrl.pitchL[stMid.nbSubfr-1]
		d.syncLegacyPLCState(stMid, frameOut)
		stMid.nFramesDecoded++
		lastFrameLost = false
	}

	// Resample from native rate to 48kHz using the same int16 path as normal decode.
	resampler := d.GetResampler(bandwidth)
	output := make([]float32, frameSizeSamples*outputChannels)
	outputOffset := 0

	for f := 0; f < framesPerPacket; f++ {
		start := f * frameLength
		end := start + frameLength
		if end > len(outInt16) {
			end = len(outInt16)
		}
		frameNative := outInt16[start:end]

		// Apply sMid buffering before resampling
		resamplerInput := d.BuildMonoResamplerInputInt16(frameNative)
		n := resampler.ProcessInt16Into(resamplerInput, output[outputOffset:])
		outputOffset += n
	}
	output = output[:outputOffset]

	// Handle channel expansion/reduction
	if outputChannels == 2 && !stereo {
		// Mono to stereo: duplicate samples
		stereoOutput := make([]float32, len(output)*2)
		for i, s := range output {
			stereoOutput[i*2] = s
			stereoOutput[i*2+1] = s
		}
		output = stereoOutput
	}

	// Match libopus decode_fec cadence:
	// - if the recovered frame decoded from LBRR, clear PLC loss accumulator
	// - if no LBRR was present, keep loss cadence from concealment path
	if d.plcState != nil {
		if !lastFrameLost {
			d.plcState.Reset()
			d.plcState.SetLastFrameParams(plc.ModeSILK, frameSizeSamples, outputChannels)
		}
	}

	d.haveDecoded = true
	return output, nil
}

func (d *Decoder) decodeFECLostMonoFrameInto(st *decoderState, frameOut []int16) {
	if st == nil || len(frameOut) == 0 {
		return
	}

	frameLength := len(frameOut)
	fadeFactor := 1.0
	if d.plcState != nil {
		fadeFactor = d.plcState.RecordLoss()
	}

	lossCnt := st.lossCnt
	var concealed []float32
	if d.scratchOutput != nil && len(d.scratchOutput) >= frameLength {
		concealed = d.scratchOutput[:frameLength]
		clear(concealed)
	} else {
		concealed = make([]float32, frameLength)
	}

	if state := d.ensureSILKPLCState(0); state != nil && st.nbSubfr > 0 {
		concealedQ0 := plc.ConcealSILKWithLTP(d, state, lossCnt, frameLength)
		const scale = float32(1.0 / 32768.0)
		n := len(concealedQ0)
		if n > frameLength {
			n = frameLength
		}
		for i := 0; i < n; i++ {
			concealed[i] = float32(concealedQ0[i]) * scale
		}
		if lag := int((state.PitchLQ8 + 128) >> 8); lag > 0 {
			st.lagPrev = lag
		}
	} else {
		plcOut := plc.ConcealSILK(d, frameLength, fadeFactor)
		copy(concealed, plcOut)
		if len(plcOut) < frameLength {
			clear(concealed[len(plcOut):])
		}
	}

	d.recordPLCLossForState(st, concealed)
	for i := range frameOut {
		frameOut[i] = float32ToInt16(concealed[i])
	}
}

// HasLBRR checks if the given packet contains LBRR (FEC) data.
// This can be used to check if FEC recovery is possible before attempting it.
func (d *Decoder) HasLBRR(data []byte, bandwidth Bandwidth, frameSizeSamples int) bool {
	if data == nil || len(data) == 0 {
		return false
	}

	// Initialize range decoder
	var rd rangecoding.Decoder
	rd.Init(data)

	// Get frame parameters
	duration := FrameDurationFromTOC(frameSizeSamples)
	framesPerPacket, _, err := frameParams(duration)
	if err != nil {
		return false
	}

	// Decode VAD and LBRR flags
	st := &decoderState{}
	decodeVADAndLBRRFlags(&rd, st, framesPerPacket)

	return st.LBRRFlag != 0
}
