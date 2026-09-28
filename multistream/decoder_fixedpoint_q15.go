//go:build gopus_fixed_point && !gopus_qext

package multistream

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

type streamFixedQEXTFields struct{}

func (d *streamState) prepareFixedQEXTCELTFrame(_ parsedOpusPacket, _ streamTOC) error { return nil }

func (d *streamState) prepareFixedHybridStream(toc streamTOC) (bool, error) {
	if int(d.sampleRate) < 16000 {
		return false, nil
	}
	if d.fixedCELT == nil {
		d.fixedCELT = fixedpoint.NewCELTDecoderRate(int(d.channels), int(d.sampleRate))
	}
	if d.fixedHybridHook == nil {
		d.fixedHybridHook = &streamFixedHybridHook{st: d}
	}
	d.fixedHybridEnd = celt.BandwidthFromOpusConfig(toc.bandwidth).EffectiveBands()
	d.fixedHybridRedundant = false
	d.fixedHybridHandled = false
	d.hybridDec.SetFixedHighband(d.fixedHybridHook)
	return true, nil
}

func (d *streamState) celtFixedRes(parsed parsedOpusPacket, frameSize int, toc streamTOC, res []int32) bool {
	if len(parsed.frames) == 0 || frameSize%len(parsed.frames) != 0 {
		return false
	}
	channels := int(d.channels)
	if d.fixedCELT == nil {
		d.fixedCELT = fixedpoint.NewCELTDecoderRate(channels, int(d.sampleRate))
	}
	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	frameSizePerPacketFrame := frameSize / len(parsed.frames)
	coreFrameSize := frameSizePerPacketFrame * downsample
	d.fixedCELT.SetBandRange(0, celt.BandwidthFromOpusConfig(toc.bandwidth).EffectiveBands())
	needed := frameSizePerPacketFrame * channels
	if cap(d.fixedCELTPCM) < needed {
		d.fixedCELTPCM = make([]int16, needed)
	}
	for i, frame := range parsed.frames {
		if len(frame) <= 1 {
			return false
		}
		d.fixedCELT.DecodeWithEC(frame, coreFrameSize, d.fixedCELTPCM[:needed])
		celtRes := d.fixedCELT.LastRes()
		if len(celtRes) < needed {
			return false
		}
		frameRes := res[i*needed : (i+1)*needed]
		copy(frameRes, celtRes[:needed])
	}
	return true
}

func (d *streamState) decodeFixedHybridAccum(rd *rangecoding.Decoder, coreFrameSize int, _ bool, accum []int32) bool {
	if d.fixedCELT == nil {
		return false
	}
	d.fixedCELT.SetBandRange(celt.HybridCELTStartBand, d.fixedHybridEnd)
	d.fixedCELT.DecodeHybridAccum(rd, coreFrameSize, accum)
	return true
}

func (d *streamState) resetFixedDecoderState() {
	if d.fixedCELT != nil {
		d.fixedCELT.Reset()
	}
}
