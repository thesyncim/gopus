package gopus

import (
	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/silk"
)

func (d *Decoder) decodePLCForFEC(pcm []float32, frameSize int) (int, error) {
	return d.decodePLCForFECWithState(pcm, frameSize, d.prevMode, d.lastBandwidth, d.prevPacketStereo)
}

func (d *Decoder) decodePLCForFECWithState(
	pcm []float32,
	frameSize int,
	mode Mode,
	bandwidth Bandwidth,
	packetStereo bool,
) (int, error) {
	if frameSize <= 0 {
		frameSize = d.lastFrameSize
	}
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
		n, err := d.decodeOpusFrameIntoWithStatePolicy(
			pcm[offset*d.channels:],
			nil,
			chunk,
			frameSize,
			mode,
			bandwidth,
			packetStereo,
			false,
		)
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

// extractFirstFramePayload extracts the first Opus frame payload bytes from
// a packet. This excludes packet-level TOC and framing headers.
func extractFirstFramePayload(data []byte, toc TOC) ([]byte, error) {
	if len(data) <= 1 {
		return nil, ErrPacketTooShort
	}

	switch toc.FrameCode {
	case 0:
		return data[1:], nil
	case 1:
		frameDataLen := len(data) - 1
		if frameDataLen%2 != 0 {
			return nil, ErrInvalidPacket
		}
		frameLen := frameDataLen / 2
		if frameLen <= 0 || 1+frameLen > len(data) {
			return nil, ErrInvalidPacket
		}
		return data[1 : 1+frameLen], nil
	case 2:
		if len(data) < 2 {
			return nil, ErrPacketTooShort
		}
		frame1Len, bytesRead, err := parseFrameLength(data, 1)
		if err != nil {
			return nil, err
		}
		headerLen := 1 + bytesRead
		if frame1Len <= 0 || headerLen+frame1Len > len(data) {
			return nil, ErrInvalidPacket
		}
		return data[headerLen : headerLen+frame1Len], nil
	case 3:
		if len(data) < 2 {
			return nil, ErrPacketTooShort
		}
		frameCountByte := data[1]
		vbr := (frameCountByte & 0x80) != 0
		hasPadding := (frameCountByte & 0x40) != 0
		m := int(frameCountByte & 0x3F)
		if m == 0 || m > 48 {
			return nil, ErrInvalidFrameCount
		}

		offset := 2
		padding := 0
		if hasPadding {
			for {
				if offset >= len(data) {
					return nil, ErrPacketTooShort
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
		}

		if vbr {
			frameDataEnd := len(data) - padding
			if frameDataEnd < offset {
				return nil, ErrInvalidPacket
			}

			frameLen := frameDataEnd - offset
			if m > 1 {
				var bytesRead int
				parsedFrameLen, bytesRead, err := parseFrameLength(data, offset)
				if err != nil {
					return nil, err
				}
				frameLen = parsedFrameLen
				offset += bytesRead
				for i := 1; i < m-1; i++ {
					_, readN, err := parseFrameLength(data, offset)
					if err != nil {
						return nil, err
					}
					offset += readN
				}
			}
			if frameLen <= 0 || offset+frameLen > frameDataEnd {
				return nil, ErrInvalidPacket
			}
			return data[offset : offset+frameLen], nil
		}

		frameDataLen := len(data) - offset - padding
		if frameDataLen < 0 || frameDataLen%m != 0 {
			return nil, ErrInvalidPacket
		}
		frameLen := frameDataLen / m
		if frameLen <= 0 || offset+frameLen > len(data)-padding {
			return nil, ErrInvalidPacket
		}
		return data[offset : offset+frameLen], nil
	default:
		return nil, ErrInvalidPacket
	}
}

// packetHasLBRR mirrors libopus opus_packet_has_lbrr() semantics for Opus
// frame payload bytes (first frame only).
func packetHasLBRR(firstFrameData []byte, toc TOC) bool {
	if toc.Mode == ModeCELT || len(firstFrameData) == 0 {
		return false
	}

	nbFrames := 1
	if toc.FrameSize > 960 {
		nbFrames = toc.FrameSize / 960
	}

	monoBit := 7 - nbFrames
	if monoBit < 0 {
		return false
	}
	lbrr := (firstFrameData[0] >> uint(monoBit)) & 0x1

	if toc.Stereo {
		stereoBit := 6 - 2*nbFrames
		if stereoBit >= 0 {
			lbrr |= (firstFrameData[0] >> uint(stereoBit)) & 0x1
		}
	}

	return lbrr != 0
}

// storeFECData stores the current packet's information for FEC recovery.
// This is called after successfully decoding a SILK or Hybrid packet.
func (d *Decoder) storeFECData(data []byte, toc TOC, frameCount, frameSize int) {
	if cap(d.fecData) < len(data) {
		d.fecData = make([]byte, len(data))
	} else {
		d.fecData = d.fecData[:len(data)]
	}
	copy(d.fecData, data)

	d.fecMode = toc.Mode
	d.fecBandwidth = toc.Bandwidth
	d.fecStereo = toc.Stereo
	d.fecFrameSize = frameSize
	d.fecFrameCount = frameCount
	d.hasFEC = true
}

// decodeFECFrame decodes LBRR data from the stored FEC packet.
// This is used to recover a lost frame using forward error correction.
func (d *Decoder) decodeFECFrame(pcm []float32) (int, error) {
	if !d.hasFEC || len(d.fecData) == 0 {
		return 0, errNoFECData
	}

	frameSize := d.fecFrameSize
	if frameSize <= 0 {
		frameSize = d.lastFrameSize
	}
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

	n, err := d.decodeLBRRFrames(pcm, frameSize)
	if err != nil {
		return 0, err
	}
	d.applyOutputGain(pcm[:n*d.channels])

	d.prevMode = d.fecMode
	d.lastPacketMode = d.fecMode
	d.lastBandwidth = d.fecBandwidth
	d.prevPacketStereo = d.fecStereo
	d.lastFrameSize = frameSize
	d.lastPacketDuration = frameSize
	d.lastDataLen = len(d.fecData)
	d.prevRedundancy = false
	d.haveDecoded = true

	d.clearFECState()

	return n, nil
}

func (d *Decoder) clearFECState() {
	d.hasFEC = false
	d.fecFrameSize = 0
	d.fecFrameCount = 0
	d.fecData = d.fecData[:0]
	d.fecMode = ModeHybrid
	d.fecBandwidth = BandwidthFullband
	d.fecStereo = false
}

// decodeLBRRFrames decodes LBRR (FEC) data from the stored packet.
func (d *Decoder) decodeLBRRFrames(pcm []float32, frameSize int) (int, error) {
	switch d.fecMode {
	case ModeSILK:
		return d.decodeSILKFEC(pcm, frameSize)
	case ModeHybrid:
		return d.decodeHybridFEC(pcm, frameSize)
	default:
		return 0, errNoFECData
	}
}

func (d *Decoder) decodeFECViaSILK(pcm []float32, frameSize int) (int, error) {
	silkBW, ok := silk.BandwidthFromOpus(int(d.fecBandwidth))
	if !ok {
		silkBW = silk.BandwidthWideband
	}

	fecSamples, err := d.silkDecoder.DecodeFEC(d.fecData, silkBW, frameSize, d.fecStereo, d.channels)
	if err != nil {
		return 0, err
	}

	needed := len(fecSamples)
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}
	copy(pcm[:needed], fecSamples)

	return needed, nil
}

// decodeSILKFEC decodes SILK LBRR data for FEC recovery.
func (d *Decoder) decodeSILKFEC(pcm []float32, frameSize int) (int, error) {
	if _, err := d.decodeFECViaSILK(pcm, frameSize); err != nil {
		return 0, err
	}
	return frameSize, nil
}

// decodeHybridFEC decodes Hybrid mode LBRR data for FEC recovery.
func (d *Decoder) decodeHybridFEC(pcm []float32, frameSize int) (int, error) {
	needed, err := d.decodeFECViaSILK(pcm, frameSize)
	if err != nil {
		return 0, err
	}

	celtBW := celt.BandwidthFromOpusConfig(int(d.fecBandwidth))
	d.celtDecoder.SetBandwidth(celtBW)
	if d.haveDecoded && d.prevMode != ModeHybrid && !d.prevRedundancy {
		d.celtDecoder.Reset()
		d.celtDecoder.SetBandwidth(celtBW)
	}
	d.hybridDecoder.RecordPLCLoss()
	celtSamples, err := d.celtDecoder.DecodeHybridFECPLC(min(frameSize, 48000/50))
	if err != nil {
		return 0, err
	}
	n := min(needed, len(celtSamples))
	for i := 0; i < n; i++ {
		pcm[i] += float32(celtSamples[i])
	}
	d.mainDecodeRng = d.celtDecoder.FinalRange()

	return frameSize, nil
}
