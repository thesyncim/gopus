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

func preparePacketRangeDecoder(data []byte, frameSizeSamples int) (rangecoding.Decoder, int, int, error) {
	var rd rangecoding.Decoder
	rd.Init(data)

	duration := FrameDurationFromTOC(frameSizeSamples)
	framesPerPacket, nbSubfr, err := frameParams(duration)
	if err != nil {
		return rangecoding.Decoder{}, 0, 0, err
	}

	return rd, framesPerPacket, nbSubfr, nil
}

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

	rd, framesPerPacket, nbSubfr, err := preparePacketRangeDecoder(data, frameSizeSamples)
	if err != nil {
		return nil, err
	}
	d.SetRangeDecoder(&rd)

	config := GetBandwidthConfig(bandwidth)
	fsKHz := config.SampleRate / 1000

	// Set up decoder state for FEC decoding
	stMid := &d.state[0]
	initFrameDecodeState(stMid, fsKHz, framesPerPacket, nbSubfr)

	if stereo && outputChannels == 2 {
		stSide := &d.state[1]
		initFrameDecodeState(stSide, fsKHz, framesPerPacket, nbSubfr)
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

	outInt16 := d.int16OutputBuffer(totalLen)
	clear(outInt16)
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

		// Stereo: decode stereo predictor for LBRR
		if stereo && outputChannels == 2 {
			var predQ13 [2]int32
			silkStereoDecodePred(&rd, predQ13[:])
			if d.state[1].LBRRFlags[i] == 0 {
				_ = silkStereoDecodeMidOnly(&rd)
			}
		}

		frameOut := outInt16[i*frameLength : (i+1)*frameLength]
		d.decodeLBRRFrameInto(0, stMid, &rd, i, frameOut, true)
		d.syncLegacyPLCState(stMid, frameOut)
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

	rd, framesPerPacket, _, err := preparePacketRangeDecoder(data, frameSizeSamples)
	if err != nil {
		return false
	}

	// Decode VAD and LBRR flags
	st := &decoderState{}
	decodeVADAndLBRRFlags(&rd, st, framesPerPacket)

	return st.LBRRFlag != 0
}
