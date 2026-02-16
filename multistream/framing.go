package multistream

type parsedOpusPacket struct {
	tocBase  byte
	frames   [][]byte
	consumed int
}

// parseOpusPacket parses one Opus packet from data.
// If selfDelimited is true, data may contain trailing bytes for subsequent
// packets and this function consumes exactly one self-delimited packet.
func parseOpusPacket(data []byte, selfDelimited bool) (parsedOpusPacket, error) {
	if len(data) < 1 {
		return parsedOpusPacket{}, ErrPacketTooShort
	}

	toc := data[0]
	code := toc & 0x03
	offset := 1
	padding := 0
	frameCount := 1
	frameSizes := make([]int, 0, 2)

	switch code {
	case 0:
		frameCount = 1
		if selfDelimited {
			length, consumed, err := parseSelfDelimitedLength(data[offset:])
			if err != nil {
				return parsedOpusPacket{}, err
			}
			offset += consumed
			frameSizes = append(frameSizes, length)
		} else {
			frameSizes = append(frameSizes, len(data)-offset)
		}

	case 1:
		frameCount = 2
		if selfDelimited {
			length, consumed, err := parseSelfDelimitedLength(data[offset:])
			if err != nil {
				return parsedOpusPacket{}, err
			}
			offset += consumed
			frameSizes = append(frameSizes, length, length)
		} else {
			frameDataLen := len(data) - offset
			if frameDataLen < 0 || frameDataLen%2 != 0 {
				return parsedOpusPacket{}, ErrInvalidPacket
			}
			frameLen := frameDataLen / 2
			frameSizes = append(frameSizes, frameLen, frameLen)
		}

	case 2:
		length0, consumed, err := parseSelfDelimitedLength(data[offset:])
		if err != nil {
			return parsedOpusPacket{}, err
		}
		offset += consumed

		var length1 int
		if selfDelimited {
			length1, consumed, err = parseSelfDelimitedLength(data[offset:])
			if err != nil {
				return parsedOpusPacket{}, err
			}
			offset += consumed
		} else {
			length1 = len(data) - offset - length0
		}

		if length0 < 0 || length1 < 0 {
			return parsedOpusPacket{}, ErrInvalidPacket
		}
		frameCount = 2
		frameSizes = append(frameSizes, length0, length1)

	case 3:
		if offset >= len(data) {
			return parsedOpusPacket{}, ErrPacketTooShort
		}

		frameCountByte := data[offset]
		offset++
		vbr := (frameCountByte & 0x80) != 0
		hasPadding := (frameCountByte & 0x40) != 0
		frameCount = int(frameCountByte & 0x3F)
		if frameCount == 0 || frameCount > 48 {
			return parsedOpusPacket{}, ErrInvalidPacket
		}

		frameSizes = make([]int, frameCount)
		if hasPadding {
			for {
				if offset >= len(data) {
					return parsedOpusPacket{}, ErrPacketTooShort
				}
				padByte := int(data[offset])
				offset++
				if padByte == 255 {
					padding += 254
				} else {
					padding += padByte
					break
				}
			}
		}

		if vbr {
			totalKnown := 0
			for i := 0; i < frameCount-1; i++ {
				frameLen, consumed, err := parseSelfDelimitedLength(data[offset:])
				if err != nil {
					return parsedOpusPacket{}, err
				}
				offset += consumed
				frameSizes[i] = frameLen
				totalKnown += frameLen
			}

			if selfDelimited {
				lastLen, consumed, err := parseSelfDelimitedLength(data[offset:])
				if err != nil {
					return parsedOpusPacket{}, err
				}
				offset += consumed
				frameSizes[frameCount-1] = lastLen
			} else {
				lastLen := len(data) - offset - padding - totalKnown
				if lastLen < 0 {
					return parsedOpusPacket{}, ErrInvalidPacket
				}
				frameSizes[frameCount-1] = lastLen
			}
		} else {
			if selfDelimited {
				frameLen, consumed, err := parseSelfDelimitedLength(data[offset:])
				if err != nil {
					return parsedOpusPacket{}, err
				}
				offset += consumed
				for i := 0; i < frameCount; i++ {
					frameSizes[i] = frameLen
				}
			} else {
				frameDataLen := len(data) - offset - padding
				if frameDataLen < 0 || frameDataLen%frameCount != 0 {
					return parsedOpusPacket{}, ErrInvalidPacket
				}
				frameLen := frameDataLen / frameCount
				for i := 0; i < frameCount; i++ {
					frameSizes[i] = frameLen
				}
			}
		}

	default:
		return parsedOpusPacket{}, ErrInvalidPacket
	}

	frameBytes := 0
	for _, sz := range frameSizes {
		if sz < 0 {
			return parsedOpusPacket{}, ErrInvalidPacket
		}
		frameBytes += sz
	}

	consumed := offset + frameBytes + padding
	if consumed > len(data) {
		return parsedOpusPacket{}, ErrPacketTooShort
	}
	if !selfDelimited && consumed != len(data) {
		return parsedOpusPacket{}, ErrInvalidPacket
	}

	frames := make([][]byte, frameCount)
	frameOffset := offset
	frameEnd := offset + frameBytes
	for i := 0; i < frameCount; i++ {
		next := frameOffset + frameSizes[i]
		if next > frameEnd {
			return parsedOpusPacket{}, ErrInvalidPacket
		}
		frames[i] = data[frameOffset:next]
		frameOffset = next
	}

	return parsedOpusPacket{
		tocBase:  toc & 0xFC,
		frames:   frames,
		consumed: consumed,
	}, nil
}

func frameLengthBytes(length int) int {
	if length < 252 {
		return 1
	}
	return 2
}

func writeFrameLength(dst []byte, length int) int {
	if length < 252 {
		dst[0] = byte(length)
		return 1
	}
	firstByte := 252 + (length % 4)
	secondByte := (length - firstByte) / 4
	dst[0] = byte(firstByte)
	dst[1] = byte(secondByte)
	return 2
}

// buildOpusPacketFromFrames assembles an Opus packet from frames.
// When selfDelimited is true, it writes RFC 6716 Appendix B self-delimited framing.
func buildOpusPacketFromFrames(tocBase byte, frames [][]byte, selfDelimited bool, dst []byte) (int, error) {
	count := len(frames)
	if count < 1 || count > 48 {
		return 0, ErrInvalidPacket
	}

	lengths := make([]int, count)
	totalFrameBytes := 0
	for i := 0; i < count; i++ {
		lengths[i] = len(frames[i])
		totalFrameBytes += lengths[i]
	}

	sdBytes := 0
	if selfDelimited {
		sdBytes = frameLengthBytes(lengths[count-1])
	}

	offset := 0
	switch count {
	case 1:
		need := 1 + sdBytes + lengths[0]
		if len(dst) < need {
			return 0, ErrPacketTooShort
		}
		dst[offset] = tocBase
		offset++
		if selfDelimited {
			offset += writeFrameLength(dst[offset:], lengths[0])
		}
		copy(dst[offset:], frames[0])
		offset += lengths[0]
		return offset, nil

	case 2:
		if lengths[0] == lengths[1] {
			need := 1 + sdBytes + lengths[0] + lengths[1]
			if len(dst) < need {
				return 0, ErrPacketTooShort
			}
			dst[offset] = tocBase | 0x01
			offset++
			if selfDelimited {
				offset += writeFrameLength(dst[offset:], lengths[1])
			}
			copy(dst[offset:], frames[0])
			offset += lengths[0]
			copy(dst[offset:], frames[1])
			offset += lengths[1]
			return offset, nil
		}

		need := 1 + frameLengthBytes(lengths[0]) + sdBytes + lengths[0] + lengths[1]
		if len(dst) < need {
			return 0, ErrPacketTooShort
		}
		dst[offset] = tocBase | 0x02
		offset++
		offset += writeFrameLength(dst[offset:], lengths[0])
		if selfDelimited {
			offset += writeFrameLength(dst[offset:], lengths[1])
		}
		copy(dst[offset:], frames[0])
		offset += lengths[0]
		copy(dst[offset:], frames[1])
		offset += lengths[1]
		return offset, nil
	}

	vbr := false
	for i := 1; i < count; i++ {
		if lengths[i] != lengths[0] {
			vbr = true
			break
		}
	}

	need := 2 + sdBytes + totalFrameBytes
	if vbr {
		for i := 0; i < count-1; i++ {
			need += frameLengthBytes(lengths[i])
		}
	}
	if len(dst) < need {
		return 0, ErrPacketTooShort
	}

	dst[offset] = tocBase | 0x03
	offset++
	if vbr {
		dst[offset] = byte(count) | 0x80
		offset++
		for i := 0; i < count-1; i++ {
			offset += writeFrameLength(dst[offset:], lengths[i])
		}
	} else {
		dst[offset] = byte(count)
		offset++
	}

	if selfDelimited {
		offset += writeFrameLength(dst[offset:], lengths[count-1])
	}

	for i := 0; i < count; i++ {
		copy(dst[offset:], frames[i])
		offset += lengths[i]
	}

	return offset, nil
}

func makeSelfDelimitedPacket(packet []byte) ([]byte, error) {
	parsed, err := parseOpusPacket(packet, false)
	if err != nil {
		return nil, err
	}

	// Self-delimiting framing adds at most 2 bytes.
	dst := make([]byte, len(packet)+2)
	n, err := buildOpusPacketFromFrames(parsed.tocBase, parsed.frames, true, dst)
	if err != nil {
		return nil, err
	}
	return dst[:n], nil
}

func decodeSelfDelimitedPacket(data []byte) ([]byte, int, error) {
	parsed, err := parseOpusPacket(data, true)
	if err != nil {
		return nil, 0, err
	}

	dst := make([]byte, parsed.consumed)
	n, err := buildOpusPacketFromFrames(parsed.tocBase, parsed.frames, false, dst)
	if err != nil {
		return nil, 0, err
	}
	return dst[:n], parsed.consumed, nil
}
