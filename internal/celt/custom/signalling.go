//go:build gopus_custom_modes

package custom

import "math/bits"

// celt/celt.h assigns this ID to the QEXT packet extension. The packet stores
// the ID shifted left once, as celt_decoder.c checks data[len] against
// QEXT_EXTENSION_ID << 1.
const customSignallingQEXTExtensionID = 124

type customSignalledFrame struct {
	frameSize     int
	channels      int
	endBand       int
	payloadOffset int
	payloadLength int
	qextOffset    int
	qextLength    int
}

func (f customSignalledFrame) payload(packet []byte) []byte {
	return packet[f.payloadOffset : f.payloadOffset+f.payloadLength]
}

func (f customSignalledFrame) qextPayload(packet []byte) []byte {
	if f.qextLength == 0 {
		return nil
	}
	return packet[f.qextOffset : f.qextOffset+f.qextLength]
}

func customSignallingHeader(mode *CustomMode, channels, frameSize int) (byte, error) {
	if mode == nil || channels < 1 || channels > 2 || frameSize <= 0 || mode.ShortMdctSize <= 0 || frameSize%mode.ShortMdctSize != 0 {
		return 0, ErrInvalidFrameSize
	}
	nbShort := frameSize / mode.ShortMdctSize
	if nbShort&(nbShort-1) != 0 {
		return 0, ErrInvalidFrameSize
	}
	lm := bits.Len(uint(nbShort)) - 1
	if lm > mode.MaxLM {
		return 0, ErrInvalidFrameSize
	}
	// celt_encode_with_ec writes tmp in the top bits, LM in bits 3–4,
	// and the coded-channel flag in bit 2. The wrapper keeps the full mode
	// bandwidth, so tmp is zero. See libopus celt/celt_encoder.c.
	header := byte((lm << 3) | ((channels - 1) << 2))
	if !customSignallingUsesOpusTOC(mode) {
		return header, nil
	}
	return customTOCFromCELTHeader(header)
}

func parseCustomSignallingPacket(mode *CustomMode, packet []byte, outputCapacity int) (customSignalledFrame, error) {
	if len(packet) == 0 {
		return customSignalledFrame{}, ErrInvalidPacket
	}
	header := packet[0]
	if customSignallingUsesOpusTOC(mode) {
		var err error
		header, err = customCELTHeaderFromTOC(header)
		if err != nil {
			return customSignalledFrame{}, err
		}
	}
	// libopus commits the end band immediately after converting a valid TOC,
	// before it validates LM, padding, or the output capacity.
	frame := customSignalledFrame{
		endBand: min(max(1, mode.EffEBands-2*int(header>>5)), mode.EffEBands),
	}
	// Match celt_decode_with_ec's custom header parsing in
	// libopus celt/celt_decoder.c: the header selects LM, coded channels,
	// and the end band while the API frame size is an output capacity.
	lm := int(header>>3) & 3
	if lm > mode.MaxLM {
		return frame, ErrInvalidPacket
	}
	frameSize := mode.ShortMdctSize << lm
	frame.frameSize = frameSize
	frame.channels = 1 + int((header>>2)&1)
	frame.payloadOffset = 1
	frame.payloadLength = len(packet) - frame.payloadOffset
	if packet[0]&3 == 3 {
		if frame.payloadLength <= 0 {
			return frame, ErrInvalidPacket
		}
		if packet[frame.payloadOffset]&0x40 != 0 {
			frame.payloadOffset++ // CELT packet framing byte.
			// celt_decode_with_ec subtracts these bytes from the main range-coded
			// payload length, then treats the same region as packet padding.
			paddingTotal := 0
			for {
				if frame.payloadOffset >= len(packet) {
					return frame, ErrInvalidPacket
				}
				count := packet[frame.payloadOffset]
				frame.payloadOffset++
				countBytes := int(count)
				if count == 255 {
					countBytes = 254
				}
				paddingTotal += countBytes
				if count != 255 {
					break
				}
			}
			padding := paddingTotal - 1
			frame.payloadLength = len(packet) - frame.payloadOffset - paddingTotal
			if frame.payloadLength <= 0 || padding < 0 {
				return frame, ErrInvalidPacket
			}
			if frame.payloadOffset+frame.payloadLength < len(packet) &&
				packet[frame.payloadOffset+frame.payloadLength] == customSignallingQEXTExtensionID<<1 {
				frame.qextOffset = frame.payloadOffset + frame.payloadLength + 1
				frame.qextLength = padding
			}
		}
	}
	if frame.payloadLength > 1275 {
		return frame, ErrInvalidPacket
	}
	if frameSize <= 0 || outputCapacity < frameSize {
		return frame, ErrInvalidFrameSize
	}
	return frame, nil
}

func customTOCFromCELTHeader(header byte) (byte, error) {
	if header >= 0xa0 {
		return 0, ErrInvalidPacket
	}
	converted := customTOCFromCELT[header>>3]
	if converted == 0 {
		return 0, ErrInvalidPacket
	}
	return converted | (header & 7), nil
}

func customCELTHeaderFromTOC(header byte) (byte, error) {
	if header < 0x80 {
		return 0, ErrInvalidPacket
	}
	index := int(header>>3) - 16
	if index < 0 || index >= len(customCELTFromTOC) {
		return 0, ErrInvalidPacket
	}
	return customCELTFromTOC[index] | (header & 7), nil
}

var customTOCFromCELT = [20]byte{
	0xe0, 0xe8, 0xf0, 0xf8,
	0xc0, 0xc8, 0xd0, 0xd8,
	0xa0, 0xa8, 0xb0, 0xb8,
	0x00, 0x00, 0x00, 0x00,
	0x80, 0x88, 0x90, 0x98,
}

var customCELTFromTOC = [16]byte{
	0x80, 0x88, 0x90, 0x98,
	0x40, 0x48, 0x50, 0x58,
	0x20, 0x28, 0x30, 0x38,
	0x00, 0x08, 0x10, 0x18,
}
