package celt

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/rangecoding"
)

// SetRangeDecoder sets the range decoder for the current frame.
// This must be called before decoding each frame.
func (d *Decoder) SetRangeDecoder(rd *rangecoding.Decoder) {
	d.rangeDecoder = rd
}

// RangeDecoder returns the current range decoder.
func (d *Decoder) RangeDecoder() *rangecoding.Decoder {
	return d.rangeDecoder
}

// FinalRange returns the final range coder state after decoding.
// This matches libopus OPUS_GET_FINAL_RANGE and is used for bitstream verification.
// Must be called after decoding a frame to get a meaningful value.
func (d *Decoder) FinalRange() uint32 {
	if d.rangeDecoder != nil {
		return d.rangeDecoder.Range()
	}
	return 0
}

func (d *Decoder) takeQEXTPayload() []byte {
	if !extsupport.QEXT {
		if d.qext != nil {
			d.qext.pendingPayload = nil
		}
		return nil
	}
	if d.qext == nil {
		return nil
	}
	payload := d.qext.pendingPayload
	d.qext.pendingPayload = nil
	return payload
}

func (d *Decoder) prepareMainBandQEXTDecode(payload []byte, mainRD *rangecoding.Decoder, end, lm int) (*rangecoding.Decoder, []int, []int, int) {
	if len(payload) == 0 || mainRD == nil || end <= 0 {
		return nil, nil, nil, 0
	}
	qextState := d.ensureQEXTState()
	extDec := &qextState.rangeDecoderScratch
	extDec.Init(payload)
	_ = decodeQEXTHeader(extDec, d.channels, len(payload))

	extraPulses := ensureIntSlice(&qextState.scratchPulses, end)
	extraQuant := ensureIntSlice(&qextState.scratchFineQuant, end)
	totalBitsQ3 := (len(payload) * 8 << bitRes) - mainRD.TellFrac() - 1
	computeQEXTExtraAllocationDecode(0, end, totalBitsQ3, d.channels, lm, extDec, extraPulses, extraQuant)
	return extDec, extraPulses, extraQuant, len(payload) * 8 << bitRes
}

func (d *Decoder) decodeFineEnergyWithDecoderPrev(rd *rangecoding.Decoder, energies []float64, nbBands int, prevQuant, extraQuant []int) {
	if rd == nil {
		return
	}
	oldRD := d.rangeDecoder
	d.rangeDecoder = rd
	d.decodeFineEnergy(energies, nbBands, prevQuant, extraQuant)
	d.rangeDecoder = oldRD
}

func combineFinalRange(mainRD, extRD *rangecoding.Decoder) uint32 {
	if mainRD == nil {
		return 0
	}
	rng := mainRD.Range()
	if extRD != nil {
		rng ^= extRD.Range()
	}
	return rng
}

// Channels returns the number of audio channels (1 or 2).
func (d *Decoder) Channels() int {
	return d.channels
}

// SetBandwidth sets the CELT bandwidth derived from the Opus TOC.
func (d *Decoder) SetBandwidth(bw CELTBandwidth) {
	d.bandwidth = bw
}

// Bandwidth returns the current CELT bandwidth setting.
func (d *Decoder) Bandwidth() CELTBandwidth {
	return d.bandwidth
}

// SampleRate returns the output sample rate (always 48000 for CELT).
func (d *Decoder) SampleRate() int {
	return d.sampleRate
}
