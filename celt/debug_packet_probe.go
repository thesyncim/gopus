package celt

import "fmt"

// DebugPacketDecision captures the CELT-side header/allocation decisions
// decoded from a raw CELT frame payload. This is intended for parity
// investigations and focused regression tests.
type DebugPacketDecision struct {
	PostfilterPeriod int
	PostfilterQG     int
	PostfilterTapset int
	Transient        bool
	Intra            bool
	ShortBlocks      int
	Spread           int
	AllocTrim        int
	Intensity        int
	DualStereo       int
	CodedBands       int
	Balance          int
	TFRes            string
	DynallocOffsets  string
	Pulses           string
	FineQuant        string
	FinePriority     string
	RangeAfterHeader uint32
	RangeAfterCoarse uint32
	RangeAfterAlloc  uint32
	RangeAfterFine   uint32
	RangeAfterPVQ    uint32
	RangeAfterAC     uint32
	FinalRange       uint32
}

// ProbeRawPacketDecision decodes a raw CELT frame payload and returns the
// packet-level header/allocation decisions while advancing decoder state just
// like a normal decode. The input must exclude the Opus TOC byte.
func (d *Decoder) ProbeRawPacketDecision(data []byte, frameSize int) (DebugPacketDecision, error) {
	setup, err := d.prepareDecodeFrame(data, frameSize)
	if err != nil {
		return DebugPacketDecision{}, err
	}
	start := 0
	rd := setup.rd
	mode := setup.mode
	lm := setup.lm
	end := setup.end
	prev1Energy := setup.prev1Energy
	prev1LogE := setup.prev1LogE
	prev2LogE := setup.prev2LogE

	totalBits := len(data) * 8
	tell := rd.Tell()
	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}
	if silence {
		return DebugPacketDecision{}, ErrEncodingFailed
	}

	header := d.decodeFrameHeader(rd, totalBits, frameSize, start, end, lm, mode.ShortBlocks)
	rangeAfterHeader := rd.Range()
	energies := d.decodeCoarseEnergyInto(ensureFloat64Slice(&d.scratchEnergies, end*d.channels), end, header.intra, lm)
	rangeAfterCoarse := rd.Range()
	allocation := d.decodeBandAllocation(rd, totalBits, start, end, lm, header.transient)
	rangeAfterAlloc := rd.Range()
	d.DecodeFineEnergy(energies, end, allocation.fineQuant)
	rangeAfterFine := rd.Range()

	spectrum := decodedFrameSpectrum{}
	spectrum.coeffsL, spectrum.coeffsR, spectrum.collapse = quantAllBandsDecodeWithScratch(rd, d.channels, frameSize, lm, start, end, allocation.pulses, header.shortBlocks, allocation.spread,
		allocation.dualStereo, allocation.intensity, allocation.tfRes, (totalBits<<bitRes)-allocation.antiCollapseRsv, allocation.balance, allocation.codedBands, d.channels == 1, &d.rng, &d.scratchBands, nil, nil, 0)
	rangeAfterPVQ := rd.Range()
	if allocation.antiCollapseRsv > 0 {
		spectrum.antiCollapseOn = rd.DecodeRawBits(1) == 1
	}
	rangeAfterAC := rd.Range()
	bitsLeft := totalBits - rd.Tell()
	d.DecodeEnergyFinalise(energies, end, allocation.fineQuant, allocation.finePriority, bitsLeft)
	if spectrum.antiCollapseOn {
		antiCollapse(spectrum.coeffsL, spectrum.coeffsR, spectrum.collapse, lm, d.channels, start, end, energies, prev1LogE, prev2LogE, allocation.pulses, d.rng)
	}
	d.applyPendingPLCPrefilterAndFold()
	d.synthesizeDecodedFrame(frameSize, mode.LM, end, lm, header.shortBlocks, header.transient, header.postfilterPeriod, header.postfilterGain, header.postfilterTapset, energies, spectrum.coeffsL, spectrum.coeffsR, nil)
	if err := d.finalizeDecodedFrameState(frameSize, start, end, lm, header.transient, energies, prev1Energy, nil, rd); err != nil {
		return DebugPacketDecision{}, err
	}

	return DebugPacketDecision{
		PostfilterPeriod: header.postfilterPeriod,
		PostfilterQG:     debugPacketProbeQG(header.postfilterGain),
		PostfilterTapset: header.postfilterTapset,
		Transient:        header.transient,
		Intra:            header.intra,
		ShortBlocks:      header.shortBlocks,
		Spread:           allocation.spread,
		AllocTrim:        allocation.allocTrim,
		Intensity:        allocation.intensity,
		DualStereo:       allocation.dualStereo,
		CodedBands:       allocation.codedBands,
		Balance:          allocation.balance,
		TFRes:            debugPacketProbeSliceString(allocation.tfRes),
		DynallocOffsets:  debugPacketProbeSliceString(allocation.offsets),
		Pulses:           debugPacketProbeSliceString(allocation.pulses),
		FineQuant:        debugPacketProbeSliceString(allocation.fineQuant),
		FinePriority:     debugPacketProbeSliceString(allocation.finePriority),
		RangeAfterHeader: rangeAfterHeader,
		RangeAfterCoarse: rangeAfterCoarse,
		RangeAfterAlloc:  rangeAfterAlloc,
		RangeAfterFine:   rangeAfterFine,
		RangeAfterPVQ:    rangeAfterPVQ,
		RangeAfterAC:     rangeAfterAC,
		FinalRange:       rd.Range(),
	}, nil
}

func debugPacketProbeQG(gain float64) int {
	if gain <= 0 {
		return 0
	}
	return int(gain/0.09375 + 0.5)
}

func debugPacketProbeSliceString[T any](v []T) string {
	if len(v) == 0 {
		return "[]"
	}
	return fmt.Sprint(v)
}
