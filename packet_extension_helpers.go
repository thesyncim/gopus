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
	tocBase, frames, padding, paddingFrameCount, consumed, err := parseSelfDelimitedPacketAndPadding(data)
	if err != nil {
		return nil, 0, err
	}
	extensions, err := parsePacketExtensionList(padding, paddingFrameCount)
	if err != nil {
		return nil, 0, err
	}

	dst := make([]byte, consumed)
	n, err := buildRepacketizedPacketWithOptions(tocBase, frames, dst, 0, false, extensions)
	if err != nil {
		return nil, 0, err
	}
	return dst[:n], consumed, nil
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

func (r *Repacketizer) collectExtensions(begin, end int) ([]packetExtensionData, error) {
	total := 0
	for i := begin; i < end; i++ {
		if len(r.paddings[i]) == 0 || r.padFrames[i] <= 0 {
			continue
		}
		n, err := countPacketExtensions(r.paddings[i], r.padFrames[i])
		if err != nil {
			return nil, err
		}
		total += n
	}
	if total == 0 {
		return nil, nil
	}

	extensions := make([]packetExtensionData, 0, total)
	for i := begin; i < end; i++ {
		if len(r.paddings[i]) == 0 || r.padFrames[i] <= 0 {
			continue
		}
		n, err := countPacketExtensions(r.paddings[i], r.padFrames[i])
		if err != nil {
			return nil, err
		}
		if n == 0 {
			continue
		}
		offset := len(extensions)
		extensions = append(extensions, make([]packetExtensionData, n)...)
		parsed, err := parsePacketExtensions(r.paddings[i], r.padFrames[i], extensions[offset:])
		if err != nil {
			return nil, err
		}
		extensions = extensions[:offset+parsed]
		for j := offset; j < offset+parsed; j++ {
			extensions[j].Frame += i - begin
		}
	}
	return extensions, nil
}
