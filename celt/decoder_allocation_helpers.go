package celt

import "github.com/thesyncim/gopus/rangecoding"

type decodedBandAllocation struct {
	tfRes           []int
	offsets         []int
	pulses          []int
	fineQuant       []int
	finePriority    []int
	spread          int
	allocTrim       int
	intensity       int
	dualStereo      int
	balance         int
	codedBands      int
	antiCollapseRsv int
}

func (d *Decoder) decodeBandAllocation(rd *rangecoding.Decoder, totalBits, start, end, lm int, transient bool) decodedBandAllocation {
	allocation := decodedBandAllocation{
		spread: spreadNormal,
	}

	allocation.tfRes = ensureIntSlice(&d.scratchTFRes, end)
	tfDecode(start, end, transient, allocation.tfRes, lm, rd)

	tell := rd.Tell()
	if tell+4 <= totalBits {
		allocation.spread = rd.DecodeICDF(spreadICDF, 5)
	}

	cap := ensureIntSlice(&d.scratchCaps, end)
	initCapsInto(cap, end, lm, d.channels)
	offsets := ensureIntSlice(&d.scratchOffsets, end)
	dynallocLogp := 6
	totalBitsQ3 := totalBits << bitRes
	tellFrac := rd.TellFrac()
	for i := start; i < end; i++ {
		width := d.channels * (EBands[i+1] - EBands[i]) << lm
		quanta := min(width<<bitRes, max(6<<bitRes, width))
		dynallocLoopLogp := dynallocLogp
		boost := 0
		j := 0
		for ; tellFrac+(dynallocLoopLogp<<bitRes) < totalBitsQ3 && boost < cap[i]; j++ {
			flag := rd.DecodeBit(uint(dynallocLoopLogp))
			tellFrac = rd.TellFrac()
			if flag == 0 {
				break
			}
			boost += quanta
			totalBitsQ3 -= quanta
			dynallocLoopLogp = 1
		}
		offsets[i] = boost
		if j > 0 {
			dynallocLogp = max(2, dynallocLogp-1)
		}
	}
	allocation.offsets = offsets[:end]

	allocTrim := 5
	encodedTrim := tellFrac+(6<<bitRes) <= totalBitsQ3
	if encodedTrim {
		allocTrim = rd.DecodeICDF(trimICDF, 7)
	}
	allocation.allocTrim = allocTrim

	bitsQ3 := (totalBits << bitRes) - rd.TellFrac() - 1
	if transient && lm >= 2 && bitsQ3 >= (lm+2)<<bitRes {
		allocation.antiCollapseRsv = 1 << bitRes
	}
	bitsQ3 -= allocation.antiCollapseRsv

	allocation.pulses = ensureIntSlice(&d.scratchPulses, end)
	allocation.fineQuant = ensureIntSlice(&d.scratchFineQuant, end)
	allocation.finePriority = ensureIntSlice(&d.scratchFinePriority, end)
	allocScratch := d.allocationScratch()
	allocation.codedBands = cltComputeAllocationWithScratch(start, end, offsets, cap, allocTrim, &allocation.intensity, &allocation.dualStereo,
		bitsQ3, &allocation.balance, allocation.pulses, allocation.fineQuant, allocation.finePriority, d.channels, lm, rd, allocScratch)

	return allocation
}
