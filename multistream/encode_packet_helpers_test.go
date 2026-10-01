package multistream

// encodePacket encodes one frame into a fresh buffer of the recommended
// 4000 bytes per stream and returns the packet.
func encodePacket(enc *Encoder, pcm []float32, frameSize int) ([]byte, error) {
	return encodePacketMax(enc, pcm, frameSize, pcm, 4000*enc.Streams())
}

// encodePacketMax encodes one frame with an explicit caller budget
// (max_data_bytes) and analysis frame, returning the packet.
func encodePacketMax(enc *Encoder, pcm []float32, frameSize int, analysisPCM []float32, maxDataBytes int) ([]byte, error) {
	out := make([]byte, maxDataBytes)
	n, err := enc.EncodeWithAnalysis(pcm, frameSize, analysisPCM, out)
	if err != nil {
		return nil, err
	}
	return out[:n], nil
}

// encodePacketInt16Max encodes one 16-bit frame (opus_multistream_encode /
// opus_projection_encode) with an explicit caller budget, returning the packet.
func encodePacketInt16Max(enc *Encoder, pcm []int16, frameSize, maxDataBytes int) ([]byte, error) {
	out := make([]byte, maxDataBytes)
	n, err := enc.EncodeInt16(pcm, frameSize, out)
	if err != nil {
		return nil, err
	}
	return out[:n], nil
}

// encodeFormatFrame encodes frame i of a float (sampleFormat 0) or 16-bit
// (sampleFormat 1) caller buffer through the matching entry point.
func encodeFormatFrame(enc *Encoder, sampleFormat int, pcm32 []float32, pcm16 []int16, i, frameSize, maxDataBytes int) ([]byte, error) {
	n := frameSize * enc.Channels()
	if sampleFormat == 1 {
		return encodePacketInt16Max(enc, pcm16[i*n:(i+1)*n], frameSize, maxDataBytes)
	}
	frame := pcm32[i*n : (i+1)*n]
	return encodePacketMax(enc, frame, frameSize, frame, maxDataBytes)
}
