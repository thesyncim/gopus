//go:build gopus_fixed_point && !gopus_qext

package multistream

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

type streamFixedQEXTFields struct{}

func (d *streamState) beginFixedCELTTransition(mode int, gainQ8 int32) {
	d.fixedTransitionArmed = d.fixedCELT != nil && d.haveDecoded && !d.prevRedundancy &&
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
	if !active || transSize <= 0 || d.fixedCELT == nil {
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
	if cap(d.fixedCELTPCM) < needed {
		d.fixedCELTPCM = make([]int16, needed)
	}
	if d.fixedCELT.DecodeWithECChannels(nil, coreFrameSize, fixedCELTCodedChannels(d.lastPacketStereo), d.fixedCELTPCM[:needed]) != transSize {
		return
	}
	transition := d.fixedCELT.LastRes()
	if len(transition) < needed {
		return
	}
	if cap(d.fixedTransitionRes) < needed {
		d.fixedTransitionRes = make([]int32, needed)
	}
	if cap(d.fixedTransitionMain) < needed {
		d.fixedTransitionMain = make([]int32, needed)
	}
	d.fixedTransitionRes = d.fixedTransitionRes[:needed]
	d.fixedTransitionMain = d.fixedTransitionMain[:needed]
	copy(d.fixedTransitionRes, transition[:needed])
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
	if !active || transSize <= 0 || d.fixedCELT == nil {
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
		fixedpoint.SmoothFadeRes(d.fixedTransitionRes[first:needed], res[first:needed], res[first:needed], f2_5, channels, int(d.sampleRate))
	} else {
		fixedpoint.SmoothFadeRes(d.fixedTransitionRes, res[:needed], res[:needed], f2_5, channels, int(d.sampleRate))
	}
}

func (d *streamState) prepareFixedCELTFrame(mode int, _ parsedOpusPacket, toc streamTOC) error {
	if d.fixedCELT == nil {
		d.fixedCELT = fixedpoint.NewCELTDecoderRate(int(d.channels), int(d.sampleRate))
	}
	if d.haveDecoded && int(d.lastMode) != mode && !d.prevRedundancy {
		d.fixedCELT.Reset()
	}
	d.fixedCELT.SetPhaseInversionDisabled(d.celtDec.PhaseInversionDisabled())
	startBand := 0
	if mode == streamModeHybrid {
		startBand = celt.HybridCELTStartBand
	}
	d.fixedCELT.SetBandRange(startBand, celt.BandwidthFromOpusConfig(toc.bandwidth).EffectiveBands())
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
	d.fixedCELT.SetBandRange(celt.HybridCELTStartBand, d.fixedHybridEnd)
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
	codedChannels := fixedCELTCodedChannels(toc.stereo)
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
		d.fixedCELT.DecodeWithECChannels(frame, coreFrameSize, codedChannels, d.fixedCELTPCM[:needed])
		celtRes := d.fixedCELT.LastRes()
		if len(celtRes) < needed {
			return false
		}
		frameRes := res[i*needed : (i+1)*needed]
		copy(frameRes, celtRes[:needed])
	}
	return true
}

func (d *streamState) canDecodeLostFixed() bool {
	return d.fixedCELT != nil && d.lastTOCFrameSize > 0
}

func (d *streamState) decodeLostFixed(frameSize int) ([]int32, error) {
	channels := int(d.channels)
	needed := frameSize * channels
	if d.fixedCELT == nil {
		return nil, ErrInvalidPacket
	}
	if cap(d.fixedRes) < needed {
		d.fixedRes = make([]int32, needed)
	} else {
		d.fixedRes = d.fixedRes[:needed]
	}
	if cap(d.fixedCELTPCM) < needed {
		d.fixedCELTPCM = make([]int16, needed)
	}
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
		if decoded := d.fixedCELT.DecodeWithECChannels(nil, coreFrameSize, fixedCELTCodedChannels(d.lastPacketStereo), d.fixedCELTPCM[start:end]); decoded != chunk {
			return nil, ErrInvalidPacket
		}
		lastRes := d.fixedCELT.LastRes()
		if len(lastRes) < chunk*channels {
			return nil, ErrInvalidPacket
		}
		copy(d.fixedRes[start:end], lastRes[:chunk*channels])
		offset += chunk
	}
	return d.fixedRes, nil
}

func (d *streamState) decodeFixedHybridAccum(rd *rangecoding.Decoder, coreFrameSize int, packetStereo bool, accum []int32) bool {
	if d.fixedCELT == nil {
		return false
	}
	d.fixedCELT.SetBandRange(celt.HybridCELTStartBand, d.fixedHybridEnd)
	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	return d.fixedCELT.DecodeHybridAccumChannels(rd, coreFrameSize, fixedCELTCodedChannels(packetStereo), accum) == coreFrameSize/downsample
}

func (d *streamState) resetFixedDecoderState() {
	if d.fixedCELT != nil {
		d.fixedCELT.Reset()
	}
}
