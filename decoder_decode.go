package gopus

import "github.com/thesyncim/gopus/internal/extsupport"

// Decode decodes an Opus packet into float32 PCM samples.
//
// data: Opus packet data, or nil for Packet Loss Concealment (PLC).
// pcm: Output buffer for decoded samples. Must be large enough to hold
// frameSize * frameCount * channels samples, where frameSize and frameCount
// are determined from the packet TOC and frame code.
//
// Returns the number of samples per channel decoded, or an error.
//
// When data is nil, the decoder performs packet loss concealment using
// the last successfully decoded frame parameters. Before the first packet has
// been decoded, cold PLC returns zeroed audio and a nil error.
//
// Buffer sizing: For 60ms frames at 48kHz stereo, pcm must have at least
// 2880 * 2 = 5760 elements. For multi-frame packets (code 1/2/3), the buffer
// must be large enough for all frames combined.
//
// Multi-frame packets (RFC 6716 Section 3.2):
//   - Code 0: 1 frame (most common)
//   - Code 1: 2 equal-sized frames
//   - Code 2: 2 different-sized frames
//   - Code 3: Arbitrary number of frames (1-48)
func (d *Decoder) Decode(data []byte, pcm []float32) (int, error) {
	if data == nil || len(data) == 0 {
		frameSize := d.lastFrameSize
		if frameSize <= 0 {
			frameSize = 960
		}
		if frameSize > d.maxPacketSamples {
			return 0, ErrPacketTooLarge
		}
		needed := frameSize * d.channels
		if len(pcm) < needed {
			return 0, ErrBufferTooSmall
		}

		remaining := frameSize
		offset := 0
		for remaining > 0 {
			chunk := min(remaining, 48000/50)
			n, err := d.decodeOpusFrameInto(pcm[offset*d.channels:], nil, chunk, d.lastFrameSize, d.prevMode, d.lastBandwidth, d.prevPacketStereo)
			if err != nil {
				return 0, err
			}
			if n == 0 {
				break
			}
			offset += n
			remaining -= n
		}
		d.applyOutputGain(pcm[:frameSize*d.channels])

		d.lastFrameSize = frameSize
		d.lastPacketDuration = frameSize
		d.lastDataLen = 0
		return frameSize, nil
	}

	if len(data) > d.maxPacketBytes {
		return 0, ErrPacketTooLarge
	}

	toc, frameCount, err := packetFrameCount(data)
	if err != nil {
		return 0, err
	}
	frameSize := toc.FrameSize
	totalSamples := frameSize * frameCount
	if totalSamples > d.maxPacketSamples {
		return 0, ErrPacketTooLarge
	}

	needed := totalSamples * d.channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	offsetSamples := 0
	var qextPayloads [maxRepacketizerFrames][]byte
	decodeFrame := func(frameIndex int, frameData []byte) error {
		var qextPayload []byte
		if extsupport.QEXT && toc.Mode == ModeCELT && !d.ignoreExtensions && frameIndex >= 0 && frameIndex < len(qextPayloads) {
			qextPayload = qextPayloads[frameIndex]
		}
		n, err := d.decodeOpusFrameIntoWithQEXT(pcm[offsetSamples*d.channels:], frameData, frameSize, frameSize, toc.Mode, toc.Bandwidth, toc.Stereo, qextPayload)
		if err != nil {
			return err
		}
		offsetSamples += n
		d.prevPacketStereo = toc.Stereo
		return nil
	}

	switch toc.FrameCode {
	case 0:
		if err := decodeFrame(0, data[1:]); err != nil {
			return 0, err
		}
	case 1:
		frameDataLen := len(data) - 1
		if frameDataLen%2 != 0 {
			return 0, ErrInvalidPacket
		}
		frameLen := frameDataLen / 2
		offset := 1
		for i := 0; i < 2; i++ {
			if offset+frameLen > len(data) {
				return 0, ErrInvalidPacket
			}
			if err := decodeFrame(i, data[offset:offset+frameLen]); err != nil {
				return 0, err
			}
			offset += frameLen
		}
	case 2:
		if len(data) < 2 {
			return 0, ErrPacketTooShort
		}
		frame1Len, bytesRead, err := parseFrameLength(data, 1)
		if err != nil {
			return 0, err
		}
		headerLen := 1 + bytesRead
		frame2Len := len(data) - headerLen - frame1Len
		if frame2Len < 0 {
			return 0, ErrInvalidPacket
		}
		if headerLen+frame1Len > len(data) {
			return 0, ErrInvalidPacket
		}
		if err := decodeFrame(0, data[headerLen:headerLen+frame1Len]); err != nil {
			return 0, err
		}
		offset := headerLen + frame1Len
		if offset+frame2Len > len(data) {
			return 0, ErrInvalidPacket
		}
		if err := decodeFrame(1, data[offset:offset+frame2Len]); err != nil {
			return 0, err
		}
	case 3:
		if len(data) < 2 {
			return 0, ErrPacketTooShort
		}
		frameCountByte := data[1]
		vbr := (frameCountByte & 0x80) != 0
		hasPadding := (frameCountByte & 0x40) != 0
		m := int(frameCountByte & 0x3F)
		if m == 0 || m > 48 {
			return 0, ErrInvalidFrameCount
		}

		offset := 2
		padding := 0

		if hasPadding {
			for {
				if offset >= len(data) {
					return 0, ErrPacketTooShort
				}
				padByte := int(data[offset])
				offset++
				if padByte == 255 {
					padding += 254
				} else {
					padding += padByte
				}
				if padByte < 255 {
					break
				}
			}
			if extsupport.QEXT && !d.ignoreExtensions && toc.Mode == ModeCELT {
				if padding > len(data) {
					return 0, ErrInvalidPacket
				}
				if err := collectPacketExtensionPayloadsByFrame(data[len(data)-padding:], m, qextPacketExtensionID, &qextPayloads); err != nil {
					for i := range qextPayloads {
						qextPayloads[i] = nil
					}
				}
			}
		}

		if vbr {
			var frameLens [48]int
			for i := 0; i < m-1; i++ {
				frameLen, bytesRead, err := parseFrameLength(data, offset)
				if err != nil {
					return 0, err
				}
				offset += bytesRead
				frameLens[i] = frameLen
			}
			frameDataOffset := offset
			for i := 0; i < m-1; i++ {
				frameLen := frameLens[i]
				if frameDataOffset+frameLen > len(data)-padding {
					return 0, ErrInvalidPacket
				}
				if err := decodeFrame(i, data[frameDataOffset:frameDataOffset+frameLen]); err != nil {
					return 0, err
				}
				frameDataOffset += frameLen
			}
			lastFrameLen := len(data) - frameDataOffset - padding
			if lastFrameLen < 0 {
				return 0, ErrInvalidPacket
			}
			if frameDataOffset+lastFrameLen > len(data)-padding {
				return 0, ErrInvalidPacket
			}
			if err := decodeFrame(m-1, data[frameDataOffset:frameDataOffset+lastFrameLen]); err != nil {
				return 0, err
			}
		} else {
			frameDataLen := len(data) - offset - padding
			if frameDataLen < 0 {
				return 0, ErrInvalidPacket
			}
			if frameDataLen%m != 0 {
				return 0, ErrInvalidPacket
			}
			frameLen := frameDataLen / m
			for i := 0; i < m; i++ {
				if offset+frameLen > len(data)-padding {
					return 0, ErrInvalidPacket
				}
				if err := decodeFrame(i, data[offset:offset+frameLen]); err != nil {
					return 0, err
				}
				offset += frameLen
			}
		}
	}

	d.lastFrameSize = frameSize
	d.lastPacketDuration = totalSamples
	d.lastBandwidth = toc.Bandwidth
	d.lastPacketMode = toc.Mode
	d.lastDataLen = len(data)

	if toc.Mode == ModeSILK || toc.Mode == ModeHybrid {
		firstFrameData, err := extractFirstFramePayload(data, toc)
		if err != nil {
			return 0, err
		}
		d.storeFECData(firstFrameData, toc, frameCount, frameSize)
	} else {
		d.hasFEC = false
	}

	d.applyOutputGain(pcm[:totalSamples*d.channels])
	return totalSamples, nil
}

// DecodeWithFEC decodes an Opus packet, optionally recovering a lost frame using FEC.
//
// This mirrors libopus decode_fec semantics: when fec is true, the decoder
// uses in-band LBRR data if present and otherwise falls back to packet loss
// concealment instead of returning a missing-FEC error.
func (d *Decoder) DecodeWithFEC(data []byte, pcm []float32, fec bool) (int, error) {
	if !fec {
		return d.Decode(data, pcm)
	}

	if data != nil && len(data) > 0 {
		toc, frameCount, err := packetFrameCount(data)
		if err != nil {
			return 0, err
		}
		frameSize := toc.FrameSize
		if frameSize <= 0 {
			frameSize = d.lastFrameSize
		}
		if frameSize <= 0 {
			frameSize = 960
		}

		prevPacketMode := d.lastPacketMode
		if toc.Mode == ModeCELT || prevPacketMode == ModeCELT {
			d.clearFECState()
			return d.decodePLCForFEC(pcm, frameSize)
		}
		d.lastPacketMode = toc.Mode

		if toc.Mode == ModeSILK || toc.Mode == ModeHybrid {
			firstFrameData, err := extractFirstFramePayload(data, toc)
			if err != nil {
				return 0, err
			}
			if !packetHasLBRR(firstFrameData, toc) {
				d.clearFECState()
				plcFrameSize := d.lastPacketDuration
				if plcFrameSize <= 0 {
					plcFrameSize = d.lastFrameSize
				}
				if plcFrameSize <= 0 {
					plcFrameSize = frameSize
				}
				return d.decodePLCForFEC(pcm, plcFrameSize)
			}
			d.storeFECData(firstFrameData, toc, frameCount, frameSize)
			if n, err := d.decodeFECFrame(pcm); err == nil {
				return n, nil
			}
			d.clearFECState()
		}
		return d.decodePLCForFECWithState(pcm, frameSize, toc.Mode, toc.Bandwidth, toc.Stereo)
	}

	if d.hasFEC && len(d.fecData) > 0 {
		n, err := d.decodeFECFrame(pcm)
		if err == nil {
			return n, nil
		}
		d.clearFECState()
	}

	return d.decodePLCForFEC(pcm, d.lastFrameSize)
}

// DecodeInt16 decodes an Opus packet into int16 PCM samples.
func (d *Decoder) DecodeInt16(data []byte, pcm []int16) (int, error) {
	if data == nil || len(data) == 0 {
		frameSize := d.lastFrameSize
		if frameSize <= 0 {
			frameSize = 960
		}
		if frameSize > d.maxPacketSamples {
			return 0, ErrPacketTooLarge
		}
		needed := frameSize * d.channels
		if len(pcm) < needed {
			return 0, ErrBufferTooSmall
		}

		n, err := d.Decode(data, d.scratchPCM)
		if err != nil {
			return 0, err
		}
		opusPCMSoftClip(d.scratchPCM[:n*d.channels], n, d.channels, d.softClipMem[:])
		for i := 0; i < n*d.channels; i++ {
			pcm[i] = float32ToInt16(d.scratchPCM[i])
		}
		return n, nil
	}

	if len(data) > d.maxPacketBytes {
		return 0, ErrPacketTooLarge
	}

	toc, frameCount, err := packetFrameCount(data)
	if err != nil {
		return 0, err
	}
	totalSamples := toc.FrameSize * frameCount
	if totalSamples > d.maxPacketSamples {
		return 0, ErrPacketTooLarge
	}
	needed := totalSamples * d.channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	n, err := d.Decode(data, d.scratchPCM)
	if err != nil {
		return 0, err
	}
	opusPCMSoftClip(d.scratchPCM[:n*d.channels], n, d.channels, d.softClipMem[:])
	for i := 0; i < n*d.channels; i++ {
		pcm[i] = float32ToInt16(d.scratchPCM[i])
	}
	return n, nil
}
