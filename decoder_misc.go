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
