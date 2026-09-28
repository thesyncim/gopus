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

func (d *streamState) beginFixedCELTTransition(mode int, gainQ8 int32) {
	d.fixedTransitionArmed = d.qext.decoder != nil && d.haveDecoded && !d.prevRedundancy &&
		((mode == streamModeSILK && d.lastMode == streamModeCELT) || (mode == streamModeCELT && d.lastMode == streamModeSILK))
	d.fixedTransitionReady = false
	d.fixedTransitionHasMain = false
	d.fixedTransitionGainQ8 = gainQ8
}

func (d *streamState) endFixedCELTTransition() {
	d.fixedTransitionArmed = false
	d.fixedTransitionReady = false
	d.fixedTransitionHasMain = false
}

func (d *streamState) captureFixedCELTTransition(main []float32, frameSize, transSize int, active bool) {
	if !d.fixedTransitionArmed {
		return
	}
	d.fixedTransitionArmed = false
	if !active || transSize <= 0 || d.qext.decoder == nil {
		return
	}
	channels := int(d.channels)
	needed := transSize * channels
	if len(main) < needed {
		return
	}
	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	coreFrameSize := transSize * downsample
	if cap(d.fixedTransitionRes) < needed {
		d.fixedTransitionRes = make([]int32, needed)
	}
	if cap(d.fixedTransitionMain) < needed {
		d.fixedTransitionMain = make([]int32, needed)
	}
	d.fixedTransitionRes = d.fixedTransitionRes[:needed]
	d.fixedTransitionMain = d.fixedTransitionMain[:needed]
	if d.qext.decoder.DecodeLost(coreFrameSize, d.fixedTransitionRes) != transSize {
		return
	}
	floatToRes(d.fixedTransitionMain, main[:needed])
	if d.fixedTransitionGainQ8 != 0 {
		fixedpoint.ApplyDecodeGainRes(d.fixedTransitionRes, fixedpoint.DecodeGainQ16(int(d.fixedTransitionGainQ8)))
	}
	d.fixedTransitionHasMain = true
	d.fixedTransitionReady = true
}

func (d *streamState) captureFixedSILKTransition(transition []float32, transSize int, active bool) {
	if !d.fixedTransitionArmed {
		return
	}
	d.fixedTransitionArmed = false
	if !active || transSize <= 0 || d.qext.decoder == nil {
		return
	}
	channels := int(d.channels)
	needed := transSize * channels
	if len(transition) < needed {
		return
	}
	if cap(d.fixedTransitionRes) < needed {
		d.fixedTransitionRes = make([]int32, needed)
	}
	d.fixedTransitionRes = d.fixedTransitionRes[:needed]
	floatToRes(d.fixedTransitionRes, transition[:needed])
	if d.fixedTransitionGainQ8 != 0 {
		fixedpoint.ApplyDecodeGainRes(d.fixedTransitionRes, fixedpoint.DecodeGainQ16(int(d.fixedTransitionGainQ8)))
	}
	d.fixedTransitionHasMain = false
	d.fixedTransitionReady = true
}

func (d *streamState) applyFixedCELTTransition(res []int32, frameSize int) {
	if !d.fixedTransitionReady {
		return
	}
	d.fixedTransitionReady = false
	channels := int(d.channels)
	needed := len(d.fixedTransitionRes)
	if channels <= 0 || needed > len(res) {
		return
	}
	if d.fixedTransitionHasMain {
		if needed > len(d.fixedTransitionMain) {
			return
		}
		copy(res[:needed], d.fixedTransitionMain[:needed])
	}
	f5 := int(d.sampleRate) / 200
	f2_5 := int(d.sampleRate) / 400
	if frameSize >= f5 {
		first := f2_5 * channels
		if first > needed {
			return
		}
		copy(res[:first], d.fixedTransitionRes[:first])
		fixedpoint.SmoothFadeResQEXT(d.fixedTransitionRes[first:needed], res[first:needed], res[first:needed], f2_5, channels, int(d.sampleRate))
	} else {
		fixedpoint.SmoothFadeResQEXT(d.fixedTransitionRes, res[:needed], res[:needed], f2_5, channels, int(d.sampleRate))
	}
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

func (d *streamState) prepareFixedCELTFrame(mode int, parsed parsedOpusPacket, toc streamTOC) error {
	if mode == streamModeCELT {
		if d.ignoreExtensions || len(parsed.padding) == 0 {
			d.qext.payloads.collect(nil, 0, qextPacketExtensionID)
		} else {
			d.qext.payloads.collect(parsed.padding, parsed.paddingFrameCount, qextPacketExtensionID)
		}
	}
	if err := d.resetFixedQEXTForMode(mode); err != nil {
		return err
	}
	startBand := 0
	if mode == streamModeHybrid {
		startBand = celt.HybridCELTStartBand
	}
	d.qext.decoder.SetBandRange(startBand, celt.BandwidthFromOpusConfig(toc.bandwidth).EffectiveBands())
	return nil
}

func (d *streamState) prepareFixedHybridStream(toc streamTOC) (bool, error) {
	if int(d.sampleRate) < 16000 {
		return false, nil
	}
	if err := d.prepareFixedCELTFrame(streamModeHybrid, parsedOpusPacket{}, toc); err != nil {
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

func (d *streamState) celtFixedRes(parsed parsedOpusPacket, frameSize int, toc streamTOC, res []int32) bool {
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
	codedChannels := fixedCELTCodedChannels(toc.stereo)
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
		decoded := d.qext.decoder.DecodeFrameWithEC(&d.qext.rangeDecoder, len(frame), coreFrameSize, codedChannels, d.qext.payloads.frame(i), frameRes)
		if decoded != frameSizePerPacketFrame {
			return false
		}
	}
	return true
}

func (d *streamState) canDecodeLostFixed() bool {
	return d.qext.decoder != nil && d.lastTOCFrameSize > 0
}

func (d *streamState) decodeLostFixed(frameSize int) ([]int32, error) {
	channels := int(d.channels)
	needed := frameSize * channels
	if cap(d.fixedRes) < needed {
		d.fixedRes = make([]int32, needed)
	} else {
		d.fixedRes = d.fixedRes[:needed]
	}
	res := d.fixedRes
	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	frameSize20ms := int(d.sampleRate) / 50
	chunkLimit := min(frameSize20ms, int(d.lastTOCFrameSize))
	if chunkLimit <= 0 {
		return nil, ErrInvalidPacket
	}
	for offset := 0; offset < frameSize; {
		chunk := nextCELTPLCChunk(frameSize-offset, chunkLimit, frameSize20ms)
		coreFrameSize := chunk * downsample
		start := offset * channels
		end := start + chunk*channels
		if decoded := d.qext.decoder.DecodeLost(coreFrameSize, res[start:end]); decoded != chunk {
			return nil, ErrInvalidPacket
		}
		offset += chunk
	}
	return res, nil
}

func (d *streamState) decodeFixedHybridAccum(rd *rangecoding.Decoder, coreFrameSize int, packetStereo bool, accum []int32) bool {
	if d.fixedHybridRedundant || d.qext.decoder == nil || rd == nil {
		return false
	}
	d.qext.decoder.SetBandRange(celt.HybridCELTStartBand, d.fixedHybridEnd)
	decoded := d.qext.decoder.DecodeHybridAccumWithEC(rd, rd.StorageBits()/8, coreFrameSize, fixedCELTCodedChannels(packetStereo), nil, accum)
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
