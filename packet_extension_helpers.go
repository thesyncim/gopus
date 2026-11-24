package gopus

func makeSelfDelimitedPacket(packet []byte) ([]byte, error) {
	_, frames, padding, paddingFrameCount, err := parsePacketFramesAndPadding(packet)
	if err != nil {
		return nil, err
	}
	extensions, err := parsePacketExtensionList(padding, paddingFrameCount)
	if err != nil {
		return nil, err
	}

	// Self-delimited adds at most 2 bytes versus standard framing.
	dst := make([]byte, len(packet)+2)
	n, err := buildSelfDelimitedPacketFromFramesAndOptions(packet[0]&0xFC, frames, dst, 0, false, extensions)
	if err != nil {
		return nil, err
	}
	return dst[:n], nil
}

func decodeSelfDelimitedPacket(data []byte) ([]byte, int, error) {
	tocBase, frames, padding, _, consumed, err := parseSelfDelimitedPacketAndPadding(data)
	if err != nil {
		return nil, 0, err
	}

	dst := make([]byte, consumed)
	n, err := buildPacketFromFramesAndPadding(tocBase, frames, padding, dst, false)
	if err != nil {
		return nil, 0, err
	}
	return dst[:n], consumed, nil
}

func buildPacketFromFramesAndPadding(tocBase byte, frames [][]byte, padding []byte, data []byte, selfDelimited bool) (int, error) {
	if len(padding) == 0 {
		return buildPacketWithOptions(tocBase, frames, data, 0, false, nil, selfDelimited)
	}

	count := len(frames)
	if count < 1 || count > maxRepacketizerFrames {
		return 0, ErrInvalidArgument
	}

	var lengthsBuf [maxRepacketizerFrames]int
	lengths := lengthsBuf[:count]
	totalFrameBytes := 0
	vbr := false
	for i, frame := range frames {
		lengths[i] = len(frame)
		totalFrameBytes += lengths[i]
		if i > 0 && lengths[i] != lengths[0] {
			vbr = true
		}
	}

	lengthBytes := 0
	if vbr {
		for i := 0; i < count-1; i++ {
			lengthBytes += frameLengthBytes(lengths[i])
		}
	}
	if selfDelimited {
		lengthBytes += frameLengthBytes(lengths[count-1])
	}

	paddingAmount := len(padding) + (len(padding)+253)/254
	paddingBytes := paddingLengthBytes(paddingAmount)
	need := 2 + paddingBytes + lengthBytes + totalFrameBytes + len(padding)
	if len(data) < need {
		return 0, ErrBufferTooSmall
	}

	offset := 0
	data[offset] = tocBase | 0x03
	offset++
	countByte := byte(count & 0x3F)
	if vbr {
		countByte |= 0x80
	}
	countByte |= 0x40
	data[offset] = countByte
	offset++
	offset += writePaddingLength(data[offset:], paddingAmount)

	if vbr {
		for i := 0; i < count-1; i++ {
			offset += encodeFrameLength(data[offset:], lengths[i])
		}
	}
	if selfDelimited {
		offset += encodeFrameLength(data[offset:], lengths[count-1])
	}
	for i, frame := range frames {
		copy(data[offset:], frame)
		offset += lengths[i]
	}
	copy(data[offset:], padding)
	offset += len(padding)

	return offset, nil
}

func parsePacketExtensionList(padding []byte, nbFrames int) ([]packetExtensionData, error) {
	if len(padding) == 0 || nbFrames <= 0 {
		return nil, nil
	}
	count, err := countPacketExtensions(padding, nbFrames)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, nil
	}
	extensions := make([]packetExtensionData, count)
	parsed, err := parsePacketExtensions(padding, nbFrames, extensions)
	if err != nil {
		return nil, err
	}
	return extensions[:parsed], nil
}

func (r *Repacketizer) collectExtensionsInto(begin, end int, dst []packetExtensionData) ([]packetExtensionData, error) {
	total := 0
	for i := begin; i < end; i++ {
		pad := r.paddings[i]
		if pad.len == 0 || r.padFrames[i] == 0 {
			continue
		}
		n, err := countPacketExtensions(r.padBuf[pad.off:pad.off+pad.len], int(r.padFrames[i]))
		if err != nil {
			return nil, err
		}
		total += n
	}
	if total == 0 {
		return nil, nil
	}

	var extensions []packetExtensionData
	if total <= cap(dst) {
		extensions = dst[:total]
	} else {
		extensions = make([]packetExtensionData, total)
	}

	offset := 0
	for i := begin; i < end; i++ {
		pad := r.paddings[i]
		if pad.len == 0 || r.padFrames[i] == 0 {
			continue
		}
		parsed, err := parsePacketExtensions(r.padBuf[pad.off:pad.off+pad.len], int(r.padFrames[i]), extensions[offset:])
		if err != nil {
			return nil, err
		}
		for j := offset; j < offset+parsed; j++ {
			extensions[j].Frame += i - begin
		}
		offset += parsed
	}
	return extensions[:offset], nil
}
