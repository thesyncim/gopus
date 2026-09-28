//go:build gopus_fixed_point && gopus_qext

package multistream

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

type streamFixedQEXTFields struct {
	decoder      *fixedpoint.QEXTCELTDecoder
	rangeDecoder rangecoding.Decoder
	payloads     streamQEXTPayloads
}

func (d *streamState) ensureFixedQEXTCELT() error {
	if d.qext.decoder != nil {
		return nil
	}
	decoder, err := fixedpoint.NewQEXTCELTDecoder(int(d.channels), int(d.sampleRate))
	if err != nil {
		return err
	}
	d.qext.decoder = decoder
	return nil
}

func (d *streamState) resetFixedQEXTForMode(mode int) error {
	if err := d.ensureFixedQEXTCELT(); err != nil {
		return err
	}
	if d.haveDecoded && int(d.lastMode) != mode && !d.prevRedundancy {
		d.qext.decoder.Reset()
	}
	d.qext.decoder.SetPhaseInversionDisabled(d.celtDec.PhaseInversionDisabled())
	return nil
}

func (d *streamState) prepareFixedQEXTCELTFrame(parsed parsedOpusPacket, toc streamTOC) error {
	if d.ignoreExtensions || len(parsed.padding) == 0 {
		d.qext.payloads.collect(nil, 0, qextPacketExtensionID)
	} else {
		d.qext.payloads.collect(parsed.padding, parsed.paddingFrameCount, qextPacketExtensionID)
	}
	if err := d.resetFixedQEXTForMode(streamModeCELT); err != nil {
		return err
	}
	d.qext.decoder.SetBandRange(0, celt.BandwidthFromOpusConfig(toc.bandwidth).EffectiveBands())
	return nil
}

func (d *streamState) prepareFixedHybridStream(toc streamTOC) (bool, error) {
	if int(d.sampleRate) < 16000 {
		return false, nil
	}
	if err := d.resetFixedQEXTForMode(streamModeHybrid); err != nil {
		return false, err
	}
	if d.fixedHybridHook == nil {
		d.fixedHybridHook = &streamFixedHybridHook{st: d}
	}
	d.fixedHybridEnd = celt.BandwidthFromOpusConfig(toc.bandwidth).EffectiveBands()
	d.qext.decoder.SetBandRange(celt.HybridCELTStartBand, d.fixedHybridEnd)
	d.fixedHybridRedundant = false
	d.fixedHybridHandled = false
	d.hybridDec.SetFixedHighband(d.fixedHybridHook)
	return true, nil
}

func (d *streamState) celtFixedRes(parsed parsedOpusPacket, frameSize int, _ streamTOC, res []int32) bool {
	if len(parsed.frames) == 0 || d.qext.decoder == nil || frameSize%len(parsed.frames) != 0 {
		return false
	}
	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	frameSizePerPacketFrame := frameSize / len(parsed.frames)
	coreFrameSize := frameSizePerPacketFrame * downsample
	channels := int(d.channels)
	frameSamples := frameSizePerPacketFrame * channels
	if len(res) < frameSamples*len(parsed.frames) {
		return false
	}
	for i, frame := range parsed.frames {
		if len(frame) <= 1 {
			return false
		}
		d.qext.rangeDecoder.Init(frame)
		frameRes := res[i*frameSamples : (i+1)*frameSamples]
		decoded := d.qext.decoder.DecodeFrameWithEC(&d.qext.rangeDecoder, len(frame), coreFrameSize, channels, d.qext.payloads.frame(i), frameRes)
		if decoded != frameSizePerPacketFrame {
			return false
		}
	}
	return true
}

func (d *streamState) decodeFixedHybridAccum(rd *rangecoding.Decoder, coreFrameSize int, _ bool, accum []int32) bool {
	if d.fixedHybridRedundant || d.qext.decoder == nil || rd == nil {
		return false
	}
	d.qext.decoder.SetBandRange(celt.HybridCELTStartBand, d.fixedHybridEnd)
	decoded := d.qext.decoder.DecodeHybridAccumWithEC(rd, rd.StorageBits()/8, coreFrameSize, int(d.channels), nil, accum)
	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	return decoded == coreFrameSize/downsample
}

func (d *streamState) resetFixedDecoderState() {
	if d.qext.decoder != nil {
		d.qext.decoder.Reset()
	}
}
