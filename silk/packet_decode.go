package silk

import "github.com/thesyncim/gopus/rangecoding"

func frameParams(duration FrameDuration) (framesPerPacket int, nbSubfr int, err error) {
	switch duration {
	case Frame10ms:
		framesPerPacket = 1
		nbSubfr = maxNbSubfr / 2
	case Frame20ms:
		framesPerPacket = 1
		nbSubfr = maxNbSubfr
	case Frame40ms:
		framesPerPacket = 2
		nbSubfr = maxNbSubfr
	case Frame60ms:
		framesPerPacket = 3
		nbSubfr = maxNbSubfr
	default:
		return 0, 0, ErrDecodeFailed
	}
	if framesPerPacket > maxFramesPerPacket {
		return 0, 0, ErrDecodeFailed
	}
	return framesPerPacket, nbSubfr, nil
}

func roundUpShellFrame(length int) int {
	return (length + shellCodecFrameLength - 1) & ^(shellCodecFrameLength - 1)
}

func decodeVADAndLBRRFlags(rd *rangecoding.Decoder, st *decoderState, framesPerPacket int) {
	for i := range st.VADFlags {
		st.VADFlags[i] = 0
	}
	for i := range st.LBRRFlags {
		st.LBRRFlags[i] = 0
	}
	for i := 0; i < framesPerPacket; i++ {
		st.VADFlags[i] = rd.DecodeBit(1)
	}
	st.LBRRFlag = rd.DecodeBit(1)
	if st.LBRRFlag != 0 {
		if framesPerPacket == 1 {
			st.LBRRFlags[0] = 1
		} else {
			symbol := rd.DecodeICDF(silk_LBRR_flags_iCDF_ptr[framesPerPacket-2], 8) + 1
			for i := 0; i < framesPerPacket; i++ {
				st.LBRRFlags[i] = (symbol >> i) & 1
			}
		}
	}
}

func resetSideChannelState(st *decoderState) {
	for i := range st.outBuf {
		st.outBuf[i] = 0
	}
	for i := range st.sLPCQ14Buf {
		st.sLPCQ14Buf[i] = 0
	}
	st.lagPrev = 100
	st.lastGainIndex = 10
	st.prevSignalType = typeNoVoiceActivity
	st.firstFrameAfterReset = true
}
