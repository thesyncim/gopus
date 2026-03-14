package gopus

import "math"

func packetFrameCount(data []byte) (TOC, int, error) {
	if len(data) < 1 {
		return TOC{}, 0, ErrPacketTooShort
	}
	toc := ParseTOC(data[0])
	switch toc.FrameCode {
	case 0:
		return toc, 1, nil
	case 1, 2:
		return toc, 2, nil
	case 3:
		if len(data) < 2 {
			return TOC{}, 0, ErrPacketTooShort
		}
		m := int(data[1] & 0x3F)
		if m == 0 || m > 48 {
			return TOC{}, 0, ErrInvalidFrameCount
		}
		return toc, m, nil
	default:
		return TOC{}, 0, ErrInvalidPacket
	}
}

func decodeGainLinear(gainQ8 int) float32 {
	return float32(math.Pow(10.0, float64(gainQ8)/(20.0*256.0)))
}

func (d *Decoder) applyOutputGain(samples []float32) {
	if d.decodeGainQ8 == 0 || len(samples) == 0 {
		return
	}
	g := decodeGainLinear(d.decodeGainQ8)
	for i := range samples {
		samples[i] *= g
	}
}

// DebugPrevMode returns the previous decode mode (SILK/Hybrid/CELT).
func (d *Decoder) DebugPrevMode() Mode {
	return d.prevMode
}

// DebugPrevRedundancy reports whether the previous frame used CELT redundancy.
func (d *Decoder) DebugPrevRedundancy() bool {
	return d.prevRedundancy
}

// DebugPrevPacketStereo returns the last packet's stereo flag.
func (d *Decoder) DebugPrevPacketStereo() bool {
	return d.prevPacketStereo
}
