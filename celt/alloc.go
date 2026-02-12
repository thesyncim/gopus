// Package celt implements the CELT decoder per RFC 6716 Section 4.3.
package celt

import (
	"fmt"

	"github.com/thesyncim/gopus/rangecoding"
)

// AllocationResult holds the output of bit allocation computation.
type AllocationResult struct {
	BandBits     []int // PVQ bit budget per band in Q3 (a.k.a. pulses[] in libopus)
	FineBits     []int // Fine energy bits per band
	FinePriority []int // Fine energy priority flags per band
	Caps         []int // PVQ caps per band in Q3
	Balance      int   // Bit balance carried into quant_all_bands (Q3)
	CodedBands   int   // Number of coded bands
	Intensity    int   // Intensity stereo start band (0 when disabled)
	DualStereo   bool  // Dual stereo flag
}

// ComputeAllocation computes bit allocation without consuming a range coder.
// This mirrors libopus clt_compute_allocation() math but skips entropy reads.
func ComputeAllocation(totalBits, nbBands, channels int, cap, offsets []int, trim int, intensity int, dualStereo bool, lm int) AllocationResult {
	return computeAllocation(nil, totalBits, nbBands, channels, cap, offsets, trim, intensity, dualStereo, lm)
}

// ComputeAllocationWithDecoder computes bit allocation and consumes the range decoder
// for skip/intensity/dual-stereo decisions.
func ComputeAllocationWithDecoder(rd *rangecoding.Decoder, totalBits, nbBands, channels int, cap, offsets []int, trim int, intensity int, dualStereo bool, lm int) AllocationResult {
	return computeAllocation(rd, totalBits, nbBands, channels, cap, offsets, trim, intensity, dualStereo, lm)
}

func computeAllocation(rd *rangecoding.Decoder, totalBits, nbBands, channels int, cap, offsets []int, trim int, intensity int, dualStereo bool, lm int) AllocationResult {
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands < 0 {
		nbBands = 0
	}
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}

	result := AllocationResult{
		BandBits:     make([]int, nbBands),
		FineBits:     make([]int, nbBands),
		FinePriority: make([]int, nbBands),
		Caps:         make([]int, nbBands),
		Balance:      0,
		CodedBands:   nbBands,
		Intensity:    0,
		DualStereo:   false,
	}

	if nbBands == 0 || totalBits <= 0 {
		return result
	}

	if cap == nil || len(cap) < nbBands {
		cap = initCaps(nbBands, lm, channels)
	}
	copy(result.Caps, cap[:nbBands])

	if offsets == nil {
		offsets = make([]int, nbBands)
	}

	intensityVal := intensity
	dualVal := 0
	if dualStereo {
		dualVal = 1
	}
	balance := 0
	pulses := result.BandBits
	fineBits := result.FineBits
	finePriority := result.FinePriority

	codedBands := cltComputeAllocation(0, nbBands, offsets, cap, trim, &intensityVal, &dualVal,
		totalBits<<bitRes, &balance, pulses, fineBits, finePriority, channels, lm, rd)

	result.CodedBands = codedBands
	result.Balance = balance
	result.Intensity = intensityVal
	result.DualStereo = dualVal != 0

	return result
}

func cltComputeAllocation(start, end int, offsets, cap []int, allocTrim int, intensity, dualStereo *int,
	totalBitsQ3 int, balance *int, pulses, ebits, finePriority []int, channels, lm int,
	rd *rangecoding.Decoder) int {
	return cltComputeAllocationWithScratch(start, end, offsets, cap, allocTrim, intensity, dualStereo,
		totalBitsQ3, balance, pulses, ebits, finePriority, channels, lm, rd, nil)
}

func cltComputeAllocationWithScratch(start, end int, offsets, cap []int, allocTrim int, intensity, dualStereo *int,
	totalBitsQ3 int, balance *int, pulses, ebits, finePriority []int, channels, lm int,
	rd *rangecoding.Decoder, scratch []int) int {
	origTotalBits := totalBitsQ3
	lenBands := MaxBands
	if end > lenBands {
		end = lenBands
	}
	if start < 0 {
		start = 0
	}

	if totalBitsQ3 < 0 {
		totalBitsQ3 = 0
	}

	skipStart := start
	skipRsv := 0
	if totalBitsQ3 >= 1<<bitRes {
		skipRsv = 1 << bitRes
		totalBitsQ3 -= skipRsv
	}

	intensityRsv := 0
	dualStereoRsv := 0
	if channels == 2 {
		intensityRsv = int(log2FracTable[end-start])
		if intensityRsv > totalBitsQ3 {
			intensityRsv = 0
		} else {
			totalBitsQ3 -= intensityRsv
			if totalBitsQ3 >= 1<<bitRes {
				dualStereoRsv = 1 << bitRes
				totalBitsQ3 -= dualStereoRsv
			}
		}
	}
	if debugDualStereoAllocEnabled {
		fmt.Printf("cltComputeAllocation: start=%d, end=%d, channels=%d, origTotalBits=%d, intensityRsv=%d, dualStereoRsv=%d\n",
			start, end, channels, origTotalBits, intensityRsv, dualStereoRsv)
	}

	if len(scratch) < lenBands*4 {
		scratch = make([]int, lenBands*4)
	}
	bits1 := scratch[:lenBands]
	bits2 := scratch[lenBands : 2*lenBands]
	thresh := scratch[2*lenBands : 3*lenBands]
	trimOffset := scratch[3*lenBands : 4*lenBands]

	for j := start; j < end; j++ {
		width := EBands[j+1] - EBands[j]
		thresh[j] = max(channels<<bitRes, (3*(width<<lm)<<bitRes)>>4)
		trimOffset[j] = int(int64(channels*width*(allocTrim-5-lm)*(end-j-1)*(1<<(lm+bitRes))) >> 6)
		if (width << lm) == 1 {
			trimOffset[j] -= channels << bitRes
		}
	}

	lo := 1
	hi := len(BandAlloc) - 1
	for lo <= hi {
		done := 0
		psum := 0
		mid := (lo + hi) >> 1
		for j := end; j > start; j-- {
			idx := j - 1
			width := EBands[idx+1] - EBands[idx]
			bitsj := (channels * width * BandAlloc[mid][idx] << lm) >> 2
			if bitsj > 0 {
				bitsj = max(0, bitsj+trimOffset[idx])
			}
			bitsj += offsets[idx]
			if bitsj >= thresh[idx] || done != 0 {
				done = 1
				psum += min(bitsj, cap[idx])
			} else if bitsj >= channels<<bitRes {
				psum += channels << bitRes
			}
		}
		if psum > totalBitsQ3 {
			hi = mid - 1
		} else {
			lo = mid + 1
		}
	}
	hi = lo
	lo--
	if lo < 0 {
		lo = 0
	}
	if hi < 0 {
		hi = 0
	}

	for j := start; j < end; j++ {
		width := EBands[j+1] - EBands[j]
		bits1j := (channels * width * BandAlloc[lo][j] << lm) >> 2
		bits2j := cap[j]
		if hi < len(BandAlloc) {
			bits2j = (channels * width * BandAlloc[hi][j] << lm) >> 2
		}
		if bits1j > 0 {
			bits1j = max(0, bits1j+trimOffset[j])
		}
		if bits2j > 0 {
			bits2j = max(0, bits2j+trimOffset[j])
		}
		if lo > 0 {
			bits1j += offsets[j]
		}
		bits2j += offsets[j]
		if offsets[j] > 0 {
			skipStart = j
		}
		bits2j = max(0, bits2j-bits1j)
		bits1[j] = bits1j
		bits2[j] = bits2j
	}

	codedBands := interpBits2Pulses(start, end, skipStart, bits1, bits2, thresh, cap, totalBitsQ3, balance,
		skipRsv, intensity, intensityRsv, dualStereo, dualStereoRsv, pulses, ebits, finePriority, channels, lm, rd)

	return codedBands
}

func interpBits2Pulses(start, end, skipStart int, bits1, bits2, thresh, cap []int,
	total int, balance *int, skipRsv int, intensity *int, intensityRsv int,
	dualStereo *int, dualStereoRsv int, bits, ebits, finePriority []int,
	channels, lm int, rd *rangecoding.Decoder) int {
	allocFloor := channels << bitRes
	stereo := 0
	if channels > 1 {
		stereo = 1
	}
	logM := lm << bitRes
	lo := 0
	hi := 1 << allocSteps
	for i := 0; i < allocSteps; i++ {
		mid := (lo + hi) >> 1
		psum := 0
		done := 0
		for j := end; j > start; j-- {
			idx := j - 1
			tmp := bits1[idx] + int((int64(mid)*int64(bits2[idx]))>>allocSteps)
			if tmp >= thresh[idx] || done != 0 {
				done = 1
				psum += min(tmp, cap[idx])
			} else if tmp >= allocFloor {
				psum += allocFloor
			}
		}
		if psum > total {
			hi = mid
		} else {
			lo = mid
		}
	}
	psum := 0
	done := 0
	for j := end; j > start; j-- {
		idx := j - 1
		tmp := bits1[idx] + int((int64(lo)*int64(bits2[idx]))>>allocSteps)
		if tmp < thresh[idx] && done == 0 {
			if tmp >= allocFloor {
				tmp = allocFloor
			} else {
				tmp = 0
			}
		} else {
			done = 1
		}
		tmp = min(tmp, cap[idx])
		bits[idx] = tmp
		psum += tmp
	}

	codedBands := end
	for {
		j := codedBands - 1
		if j <= skipStart {
			total += skipRsv
			break
		}

		left := total - psum
		percoeff := celtUdiv(left, EBands[codedBands]-EBands[start])
		left -= (EBands[codedBands] - EBands[start]) * percoeff
		rem := max(left-(EBands[j]-EBands[start]), 0)
		bandWidth := EBands[codedBands] - EBands[j]
		bandBits := bits[j] + percoeff*bandWidth + rem

		if bandBits >= max(thresh[j], allocFloor+(1<<bitRes)) {
			if rd != nil {
				if rd.DecodeBit(1) == 1 {
					break
				}
			} else {
				break
			}
			psum += 1 << bitRes
			bandBits -= 1 << bitRes
		}

		psum -= bits[j] + intensityRsv
		if intensityRsv > 0 {
			intensityRsv = int(log2FracTable[j-start])
		}
		psum += intensityRsv
		if bandBits >= allocFloor {
			psum += allocFloor
			bits[j] = allocFloor
		} else {
			bits[j] = 0
		}
		codedBands--
	}

	if intensityRsv > 0 {
		if rd != nil {
			*intensity = start + int(rd.DecodeUniform(uint32(codedBands+1-start)))
		} else {
			if *intensity > codedBands {
				*intensity = codedBands
			}
			if *intensity < start {
				*intensity = start
			}
		}
	} else {
		*intensity = 0
	}
	if *intensity <= start {
		total += dualStereoRsv
		dualStereoRsv = 0
	}
	if dualStereoRsv > 0 {
		if rd != nil {
			*dualStereo = rd.DecodeBit(1)
			if debugDualStereoAllocEnabled {
				fmt.Printf("  Decoded dualStereo=%d from bitstream\n", *dualStereo)
			}
		}
	} else {
		*dualStereo = 0
		if debugDualStereoAllocEnabled {
			fmt.Printf("  dualStereoRsv=0, forcing dualStereo=0\n")
		}
	}

	left := total - psum
	percoeff := celtUdiv(left, EBands[codedBands]-EBands[start])
	left -= (EBands[codedBands] - EBands[start]) * percoeff
	for j := start; j < codedBands; j++ {
		bits[j] += percoeff * (EBands[j+1] - EBands[j])
	}
	for j := start; j < codedBands; j++ {
		tmp := min(left, EBands[j+1]-EBands[j])
		bits[j] += tmp
		left -= tmp
	}

	bal := 0
	for j := start; j < codedBands; j++ {
		N0 := EBands[j+1] - EBands[j]
		N := N0 << lm
		bit := bits[j] + bal
		excess := 0
		if N > 1 {
			excess = max(bit-cap[j], 0)
			bits[j] = bit - excess

			den := channels * N
			if channels == 2 && N > 2 && *dualStereo == 0 && j < *intensity {
				den++
			}
			NClogN := den * (LogN[j] + logM)
			offset := (NClogN >> 1) - den*fineOffset
			if N == 2 {
				offset += (den << bitRes) >> 2
			}
			if bits[j]+offset < den*2<<bitRes {
				offset += NClogN >> 2
			} else if bits[j]+offset < den*3<<bitRes {
				offset += NClogN >> 3
			}

			ebits[j] = max(0, bits[j]+offset+(den<<(bitRes-1)))
			ebits[j] = celtUdiv(ebits[j], den) >> bitRes
			if channels*ebits[j] > (bits[j] >> bitRes) {
				ebits[j] = bits[j] >> stereo >> bitRes
			}
			ebits[j] = min(ebits[j], maxFineBits)
			finePriority[j] = boolToInt(ebits[j]*(den<<bitRes) >= bits[j]+offset)
			bits[j] -= channels * ebits[j] << bitRes
		} else {
			excess = max(0, bit-(channels<<bitRes))
			bits[j] = bit - excess
			ebits[j] = 0
			finePriority[j] = 1
		}

		if excess > 0 {
			extraFine := min(excess>>(stereo+bitRes), maxFineBits-ebits[j])
			ebits[j] += extraFine
			extraBits := extraFine * channels << bitRes
			finePriority[j] = boolToInt(extraBits >= excess-bal)
			excess -= extraBits
			bal = excess
		} else {
			bal = 0
		}
	}
	*balance = bal

	for j := codedBands; j < end; j++ {
		ebits[j] = bits[j] >> stereo >> bitRes
		bits[j] = 0
		finePriority[j] = boolToInt(ebits[j] < 1)
	}

	return codedBands
}

// InitCaps initializes band caps for allocation.
// Exported for testing.
func InitCaps(nbBands, lm, channels int) []int {
	return initCaps(nbBands, lm, channels)
}

func initCaps(nbBands, lm, channels int) []int {
	caps := make([]int, nbBands)
	initCapsInto(caps, nbBands, lm, channels)
	return caps
}

func initCapsInto(caps []int, nbBands, lm, channels int) {
	if nbBands > len(caps) {
		nbBands = len(caps)
	}
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}
	row := 2*lm + (channels - 1)
	for i := 0; i < nbBands; i++ {
		N := (EBands[i+1] - EBands[i]) << lm
		idx := MaxBands*row + i
		cap := int(cacheCaps[idx])
		caps[i] = (cap + 64) * channels * N >> 2
	}
}

// InitCapsInto initializes band caps into the provided slice.
// This is an exported wrapper around initCapsInto for callers outside celt.
func InitCapsInto(caps []int, nbBands, lm, channels int) {
	initCapsInto(caps, nbBands, lm, channels)
}

// InitCapsForHybrid initializes band caps for hybrid mode.
// In hybrid mode, bands before startBand get zero cap (no bits allocated).
func InitCapsForHybrid(nbBands, lm, channels, startBand int) []int {
	caps := initCaps(nbBands, lm, channels)
	// Zero out caps for bands handled by SILK
	for i := 0; i < startBand && i < len(caps); i++ {
		caps[i] = 0
	}
	return caps
}

// ComputeAllocationWithEncoder computes bit allocation in Q3 and encodes the stereo params
// to the range encoder. This is the encoding counterpart to ComputeAllocationWithDecoder.
// prev is the last coded band count used for skip hysteresis (0 = no history).
// signalBandwidth is the highest band index considered to carry signal (>= start).
func ComputeAllocationWithEncoder(re *rangecoding.Encoder, totalBitsQ3, nbBands, channels int, cap, offsets []int, trim int, intensity int, dualStereo bool, lm int, prev int, signalBandwidth int) AllocationResult {
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands < 0 {
		nbBands = 0
	}
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}

	result := AllocationResult{
		BandBits:     make([]int, nbBands),
		FineBits:     make([]int, nbBands),
		FinePriority: make([]int, nbBands),
		Caps:         make([]int, nbBands),
		Balance:      0,
		CodedBands:   nbBands,
		Intensity:    0,
		DualStereo:   false,
	}

	if nbBands == 0 || totalBitsQ3 <= 0 {
		return result
	}

	if cap == nil || len(cap) < nbBands {
		cap = initCaps(nbBands, lm, channels)
	}
	copy(result.Caps, cap[:nbBands])

	if offsets == nil {
		offsets = make([]int, nbBands)
	}

	intensityVal := intensity
	dualVal := 0
	if dualStereo {
		dualVal = 1
	}
	balance := 0
	pulses := result.BandBits
	fineBits := result.FineBits
	finePriority := result.FinePriority

	codedBands := cltComputeAllocationEncode(re, 0, nbBands, offsets, cap, trim, &intensityVal, &dualVal,
		totalBitsQ3, &balance, pulses, fineBits, finePriority, channels, lm, prev, signalBandwidth)

	result.CodedBands = codedBands
	result.Balance = balance
	result.Intensity = intensityVal
	result.DualStereo = dualVal != 0

	return result
}

// ComputeAllocationHybrid computes bit allocation for hybrid mode CELT encoding.
// In hybrid mode, CELT only encodes bands 17-21 (start=HybridCELTStartBand).
// This properly sets bits for bands 0-16 to 0.
func ComputeAllocationHybrid(re *rangecoding.Encoder, totalBitsQ3, nbBands, channels int, cap, offsets []int, trim int, intensity int, dualStereo bool, lm int, prev int, signalBandwidth int) AllocationResult {
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands < 0 {
		nbBands = 0
	}
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}

	result := AllocationResult{
		BandBits:     make([]int, nbBands),
		FineBits:     make([]int, nbBands),
		FinePriority: make([]int, nbBands),
		Caps:         make([]int, nbBands),
		Balance:      0,
		CodedBands:   nbBands,
		Intensity:    0,
		DualStereo:   false,
	}

	if nbBands == 0 || totalBitsQ3 <= 0 {
		return result
	}

	if cap == nil || len(cap) < nbBands {
		cap = initCaps(nbBands, lm, channels)
	}
	copy(result.Caps, cap[:nbBands])

	if offsets == nil {
		offsets = make([]int, nbBands)
	}

	intensityVal := intensity
	dualVal := 0
	if dualStereo {
		dualVal = 1
	}
	balance := 0
	pulses := result.BandBits
	fineBits := result.FineBits
	finePriority := result.FinePriority

	// Use HybridCELTStartBand (17) as the start band for hybrid mode
	codedBands := cltComputeAllocationEncode(re, HybridCELTStartBand, nbBands, offsets, cap, trim, &intensityVal, &dualVal,
		totalBitsQ3, &balance, pulses, fineBits, finePriority, channels, lm, prev, signalBandwidth)

	result.CodedBands = codedBands
	result.Balance = balance
	result.Intensity = intensityVal
	result.DualStereo = dualVal != 0

	return result
}

func cltComputeAllocationEncode(re *rangecoding.Encoder, start, end int, offsets, cap []int, allocTrim int, intensity, dualStereo *int,
	totalBitsQ3 int, balance *int, pulses, ebits, finePriority []int, channels, lm int, prev int, signalBandwidth int) int {
	lenBands := MaxBands
	if end > lenBands {
		end = lenBands
	}
	if start < 0 {
		start = 0
	}

	if totalBitsQ3 < 0 {
		totalBitsQ3 = 0
	}

	skipStart := start
	skipRsv := 0
	if totalBitsQ3 >= 1<<bitRes {
		skipRsv = 1 << bitRes
		totalBitsQ3 -= skipRsv
	}

	intensityRsv := 0
	dualStereoRsv := 0
	if channels == 2 {
		intensityRsv = int(log2FracTable[end-start])
		if intensityRsv > totalBitsQ3 {
			intensityRsv = 0
		} else {
			totalBitsQ3 -= intensityRsv
			if totalBitsQ3 >= 1<<bitRes {
				dualStereoRsv = 1 << bitRes
				totalBitsQ3 -= dualStereoRsv
			}
		}
	}

	bits1 := make([]int, lenBands)
	bits2 := make([]int, lenBands)
	thresh := make([]int, lenBands)
	trimOffset := make([]int, lenBands)

	for j := start; j < end; j++ {
		width := EBands[j+1] - EBands[j]
		thresh[j] = max(channels<<bitRes, (3*(width<<lm)<<bitRes)>>4)
		trimOffset[j] = int(int64(channels*width*(allocTrim-5-lm)*(end-j-1)*(1<<(lm+bitRes))) >> 6)
		if (width << lm) == 1 {
			trimOffset[j] -= channels << bitRes
		}
	}

	lo := 1
	hi := len(BandAlloc) - 1
	for lo <= hi {
		done := 0
		psum := 0
		mid := (lo + hi) >> 1
		for j := end; j > start; j-- {
			idx := j - 1
			width := EBands[idx+1] - EBands[idx]
			bitsj := (channels * width * BandAlloc[mid][idx] << lm) >> 2
			if bitsj > 0 {
				bitsj = max(0, bitsj+trimOffset[idx])
			}
			bitsj += offsets[idx]
			if bitsj >= thresh[idx] || done != 0 {
				done = 1
				psum += min(bitsj, cap[idx])
			} else if bitsj >= channels<<bitRes {
				psum += channels << bitRes
			}
		}
		if psum > totalBitsQ3 {
			hi = mid - 1
		} else {
			lo = mid + 1
		}
	}
	hi = lo
	lo--
	if lo < 0 {
		lo = 0
	}
	if hi < 0 {
		hi = 0
	}

	for j := start; j < end; j++ {
		width := EBands[j+1] - EBands[j]
		bits1j := (channels * width * BandAlloc[lo][j] << lm) >> 2
		bits2j := cap[j]
		if hi < len(BandAlloc) {
			bits2j = (channels * width * BandAlloc[hi][j] << lm) >> 2
		}
		if bits1j > 0 {
			bits1j = max(0, bits1j+trimOffset[j])
		}
		if bits2j > 0 {
			bits2j = max(0, bits2j+trimOffset[j])
		}
		if lo > 0 {
			bits1j += offsets[j]
		}
		bits2j += offsets[j]
		if offsets[j] > 0 {
			skipStart = j
		}
		bits2j = max(0, bits2j-bits1j)
		bits1[j] = bits1j
		bits2[j] = bits2j
	}

	codedBands := interpBits2PulsesEncode(re, start, end, skipStart, bits1, bits2, thresh, cap, totalBitsQ3, balance,
		skipRsv, intensity, intensityRsv, dualStereo, dualStereoRsv, pulses, ebits, finePriority, channels, lm, prev, signalBandwidth)

	return codedBands
}

func interpBits2PulsesEncode(re *rangecoding.Encoder, start, end, skipStart int, bits1, bits2, thresh, cap []int,
	total int, balance *int, skipRsv int, intensity *int, intensityRsv int,
	dualStereo *int, dualStereoRsv int, bits, ebits, finePriority []int,
	channels, lm int, prev int, signalBandwidth int) int {
	allocFloor := channels << bitRes
	stereo := 0
	if channels > 1 {
		stereo = 1
	}
	if prev < 0 {
		prev = 0
	}
	if signalBandwidth < start {
		signalBandwidth = start
	}
	if signalBandwidth > end-1 {
		signalBandwidth = end - 1
	}
	logM := lm << bitRes
	lo := 0
	hi := 1 << allocSteps
	for i := 0; i < allocSteps; i++ {
		mid := (lo + hi) >> 1
		psum := 0
		done := 0
		for j := end; j > start; j-- {
			idx := j - 1
			tmp := bits1[idx] + int((int64(mid)*int64(bits2[idx]))>>allocSteps)
			if tmp >= thresh[idx] || done != 0 {
				done = 1
				psum += min(tmp, cap[idx])
			} else if tmp >= allocFloor {
				psum += allocFloor
			}
		}
		if psum > total {
			hi = mid
		} else {
			lo = mid
		}
	}
	psum := 0
	done := 0
	for j := end; j > start; j-- {
		idx := j - 1
		tmp := bits1[idx] + int((int64(lo)*int64(bits2[idx]))>>allocSteps)
		if tmp < thresh[idx] && done == 0 {
			if tmp >= allocFloor {
				tmp = allocFloor
			} else {
				tmp = 0
			}
		} else {
			done = 1
		}
		tmp = min(tmp, cap[idx])
		bits[idx] = tmp
		psum += tmp
	}

	codedBands := end
	for {
		j := codedBands - 1
		if j <= skipStart {
			total += skipRsv
			break
		}

		left := total - psum
		percoeff := celtUdiv(left, EBands[codedBands]-EBands[start])
		left -= (EBands[codedBands] - EBands[start]) * percoeff
		rem := max(left-(EBands[j]-EBands[start]), 0)
		bandWidth := EBands[codedBands] - EBands[j]
		bandBits := bits[j] + percoeff*bandWidth + rem

		if bandBits >= max(thresh[j], allocFloor+(1<<bitRes)) {
			// Compute the skip/keep decision (same logic whether encoding or not)
			// Match libopus exactly:
			//   if (codedBands<=start+2 || (band_bits > (depth_threshold*band_width<<LM<<BITRES)>>4 && j<=signalBandwidth))
			//
			// When codedBands > 17, depth_threshold is 7 or 9 depending on hysteresis.
			// When codedBands <= 17, depth_threshold is 0, which makes threshold=0,
			// so the condition simplifies to: codedBands<=start+2 || j<=signalBandwidth
			depthThreshold := 0
			if codedBands > 17 {
				if j < prev {
					depthThreshold = 7
				} else {
					depthThreshold = 9
				}
			}
			threshold := (depthThreshold * bandWidth << lm << bitRes) >> 4
			keepBand := codedBands <= start+2 || (bandBits > threshold && j <= signalBandwidth)

			// Encode the decision if we have an encoder
			if re != nil {
				if keepBand {
					re.EncodeBit(1, 1)
				} else {
					re.EncodeBit(0, 1)
				}
			}

			if keepBand {
				break
			}
			psum += 1 << bitRes
			bandBits -= 1 << bitRes
		}

		psum -= bits[j] + intensityRsv
		if intensityRsv > 0 {
			intensityRsv = int(log2FracTable[j-start])
		}
		psum += intensityRsv
		if bandBits >= allocFloor {
			psum += allocFloor
			bits[j] = allocFloor
		} else {
			bits[j] = 0
		}
		codedBands--
	}

	// Encode intensity and dual stereo params
	if intensityRsv > 0 {
		// Clamp intensity to valid range
		if *intensity > codedBands {
			*intensity = codedBands
		}
		if *intensity < start {
			*intensity = start
		}
		// Encode intensity using uniform distribution
		if re != nil {
			re.EncodeUniform(uint32(*intensity-start), uint32(codedBands+1-start))
		}
	} else {
		*intensity = 0
	}
	if *intensity <= start {
		total += dualStereoRsv
		dualStereoRsv = 0
	}
	if dualStereoRsv > 0 {
		// Encode dual stereo bit
		if re != nil {
			re.EncodeBit(*dualStereo, 1)
		}
	} else {
		*dualStereo = 0
	}

	left := total - psum
	percoeff := celtUdiv(left, EBands[codedBands]-EBands[start])
	left -= (EBands[codedBands] - EBands[start]) * percoeff
	for j := start; j < codedBands; j++ {
		bits[j] += percoeff * (EBands[j+1] - EBands[j])
	}
	for j := start; j < codedBands; j++ {
		tmp := min(left, EBands[j+1]-EBands[j])
		bits[j] += tmp
		left -= tmp
	}

	bal := 0
	for j := start; j < codedBands; j++ {
		N0 := EBands[j+1] - EBands[j]
		N := N0 << lm
		bit := bits[j] + bal
		excess := 0
		if N > 1 {
			excess = max(bit-cap[j], 0)
			bits[j] = bit - excess

			den := channels * N
			if channels == 2 && N > 2 && *dualStereo == 0 && j < *intensity {
				den++
			}
			NClogN := den * (LogN[j] + logM)
			offset := (NClogN >> 1) - den*fineOffset
			if N == 2 {
				offset += (den << bitRes) >> 2
			}
			if bits[j]+offset < den*2<<bitRes {
				offset += NClogN >> 2
			} else if bits[j]+offset < den*3<<bitRes {
				offset += NClogN >> 3
			}

			ebits[j] = max(0, bits[j]+offset+(den<<(bitRes-1)))
			ebits[j] = celtUdiv(ebits[j], den) >> bitRes
			if channels*ebits[j] > (bits[j] >> bitRes) {
				ebits[j] = bits[j] >> stereo >> bitRes
			}
			ebits[j] = min(ebits[j], maxFineBits)
			finePriority[j] = boolToInt(ebits[j]*(den<<bitRes) >= bits[j]+offset)
			bits[j] -= channels * ebits[j] << bitRes
		} else {
			excess = max(0, bit-(channels<<bitRes))
			bits[j] = bit - excess
			ebits[j] = 0
			finePriority[j] = 1
		}

		if excess > 0 {
			extraFine := min(excess>>(stereo+bitRes), maxFineBits-ebits[j])
			ebits[j] += extraFine
			extraBits := extraFine * channels << bitRes
			finePriority[j] = boolToInt(extraBits >= excess-bal)
			excess -= extraBits
			bal = excess
		} else {
			bal = 0
		}
	}
	*balance = bal

	for j := codedBands; j < end; j++ {
		ebits[j] = bits[j] >> stereo >> bitRes
		bits[j] = 0
		finePriority[j] = boolToInt(ebits[j] < 1)
	}

	return codedBands
}
