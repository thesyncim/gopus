//go:build gopus_libopus_oracle

package celt

import "github.com/thesyncim/gopus/rangecoding"

// BandAllocationProbe captures CELT decode-time band allocation after coarse
// energy and before spectrum decode. Used by libopus parity tests.
type BandAllocationProbe struct {
	Silent       bool
	Transient    bool
	Spread       int
	AllocTrim    int
	CodedBands   int
	Intensity    int
	DualStereo   int
	Balance      int
	TFRes        []int
	Offsets      []int
	Pulses       []int
	FineQuant    []int
	FinePriority []int
}

// ProbeBandAllocationWithDecoder decodes through band allocation only.
func (d *Decoder) ProbeBandAllocationWithDecoder(rd *rangecoding.Decoder, frameSize int) (BandAllocationProbe, error) {
	if rd == nil {
		return BandAllocationProbe{}, ErrNilDecoder
	}
	if !ValidFrameSize(frameSize) {
		return BandAllocationProbe{}, ErrInvalidFrameSize
	}

	d.handleChannelTransition(d.channels)
	d.beginDecodedPacketPLCState()
	d.prepareMonoEnergyFromStereo()
	d.SetRangeDecoder(rd)

	mode := GetModeConfig(frameSize)
	lm := mode.LM
	end := EffectiveBandsForFrameSize(d.bandwidth, frameSize)
	if end > mode.EffBands {
		end = mode.EffBands
	}
	if end < 1 {
		end = 1
	}
	start := 0

	totalBits := rd.StorageBits()
	tell := rd.Tell()
	if tell >= totalBits {
		return BandAllocationProbe{Silent: true}, nil
	}
	if tell == 1 && rd.DecodeBit(15) == 1 {
		return BandAllocationProbe{Silent: true}, nil
	}

	header := d.decodeFrameHeader(rd, totalBits, frameSize, start, end, lm, mode.ShortBlocks)
	d.decodeCoarseEnergyGLogInto(ensureGLogSlice(&d.scratchEnergies, end*d.channels), end, header.intra, lm)

	allocation := d.decodeBandAllocation(rd, totalBits, start, end, lm, header.transient)
	return BandAllocationProbe{
		Transient:    header.transient,
		Spread:       allocation.spread,
		AllocTrim:    allocation.allocTrim,
		CodedBands:   allocation.codedBands,
		Intensity:    allocation.intensity,
		DualStereo:   allocation.dualStereo,
		Balance:      allocation.balance,
		TFRes:        cloneIntSlice(allocation.tfRes[:end]),
		Offsets:      cloneIntSlice(allocation.offsets[:end]),
		Pulses:       cloneIntSlice(allocation.pulses[:end]),
		FineQuant:    cloneIntSlice(allocation.fineQuant[:end]),
		FinePriority: cloneIntSlice(allocation.finePriority[:end]),
	}, nil
}

// ProbeBandAllocationFromPacket probes allocation from a standalone CELT packet.
func (d *Decoder) ProbeBandAllocationFromPacket(packet []byte, frameSize int) (BandAllocationProbe, error) {
	if len(packet) < 2 {
		return BandAllocationProbe{}, ErrInvalidFrame
	}
	var rd rangecoding.Decoder
	rd.Init(packet[1:])
	return d.ProbeBandAllocationWithDecoder(&rd, frameSize)
}

func cloneIntSlice(src []int) []int {
	if len(src) == 0 {
		return nil
	}
	out := make([]int, len(src))
	copy(out, src)
	return out
}
