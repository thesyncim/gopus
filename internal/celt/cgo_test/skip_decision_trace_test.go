// Package cgo provides skip decision comparison tests between gopus and libopus.
package cgo

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// SkipDecisionTrace captures the skip decision parameters for a single band.
type SkipDecisionTrace struct {
	Band            int
	BandBits        int
	Thresh          int
	AllocFloor      int
	MinThreshold    int // max(thresh, allocFloor + 8)
	Width           int
	PassesThreshold bool
	Decision        string // "KEEP" or "SKIP"
	Reason          string // why this decision
}

// SkipTraceResult holds the full trace for a skip decision loop.
type SkipTraceResult struct {
	Traces      []SkipDecisionTrace
	CodedBands  int
	TotalBitsQ3 int
	Channels    int
	LM          int
	Trim        int
}

// TestSkipDecisionComparison_64kbps_20ms_Mono tests skip decision at 64kbps/20ms mono.
func TestSkipDecisionComparison_64kbps_20ms_Mono(t *testing.T) {
	// Test parameters
	bitrate := 64000
	frameSize := 960
	channels := 1
	nbBands := 21
	lm := 3 // 20ms
	trim := 5
	prev := 0
	signalBandwidth := nbBands - 1

	// Compute total bits in Q3
	totalBitsQ3 := (bitrate * frameSize * 8) / 48000 // Q3 = bits * 8

	t.Logf("=== Skip Decision Comparison: 64kbps/20ms Mono ===")
	t.Logf("Total bits: %d (Q3: %d)", totalBitsQ3/8, totalBitsQ3)
	t.Logf("Channels: %d, LM: %d, Trim: %d", channels, lm, trim)
	t.Logf("")

	// Compute caps using both implementations
	libopusCaps := LibopusComputeCaps(nbBands, lm, channels)
	gopusCaps := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		N := (celt.EBands[i+1] - celt.EBands[i]) << lm
		row := 2*lm + (channels - 1)
		capIdx := celt.MaxBands*row + i
		cap := int(celt.GetCacheCaps()[capIdx])
		gopusCaps[i] = (cap + 64) * channels * N >> 2
	}

	offsets := make([]int, nbBands)

	// Get libopus allocation
	libCodedBands, libBalance, libPulses, libEbits, _, _, _ :=
		LibopusComputeAllocation(0, nbBands, offsets, libopusCaps, trim, nbBands, 0, totalBitsQ3, channels, lm, prev, signalBandwidth)

	// Get gopus allocation using the encoder path
	gopusResult := celt.ComputeAllocationWithEncoder(
		nil, // no encoder - just compute
		totalBitsQ3>>3,
		nbBands,
		channels,
		gopusCaps,
		offsets,
		trim,
		nbBands,
		false,
		lm,
		prev,
		signalBandwidth,
	)

	t.Logf("=== RESULTS ===")
	t.Logf("libopus CodedBands: %d, Balance: %d", libCodedBands, libBalance)
	t.Logf("gopus   CodedBands: %d, Balance: %d", gopusResult.CodedBands, gopusResult.Balance)
	t.Logf("")

	if libCodedBands != gopusResult.CodedBands {
		t.Logf("*** CODED BANDS MISMATCH: libopus=%d, gopus=%d ***", libCodedBands, gopusResult.CodedBands)
		t.Logf("")
	}

	// Print per-band comparison
	t.Logf("Per-band PVQ bits (Q3):")
	t.Logf("%-5s | %6s | %10s | %10s | %6s", "Band", "Width", "libopus", "gopus", "diff")
	t.Logf("------+--------+------------+------------+-------")

	for i := 0; i < nbBands; i++ {
		width := (celt.EBands[i+1] - celt.EBands[i]) << lm
		libVal := libPulses[i]
		gopusVal := gopusResult.BandBits[i]
		diff := gopusVal - libVal

		diffStr := ""
		if diff != 0 {
			diffStr = fmt.Sprintf("%+d", diff)
		}

		// Only show non-zero bands or differences
		if libVal != 0 || gopusVal != 0 || diff != 0 {
			marker := ""
			if i == libCodedBands {
				marker = " <- libopus stops here"
			}
			if i == gopusResult.CodedBands {
				if marker != "" {
					marker = " <- BOTH stop here"
				} else {
					marker = " <- gopus stops here"
				}
			}
			t.Logf("%-5d | %6d | %10d | %10d | %6s%s", i, width, libVal, gopusVal, diffStr, marker)
		}
	}

	t.Logf("")
	t.Logf("Per-band fine bits:")
	t.Logf("%-5s | %10s | %10s | %6s", "Band", "libopus", "gopus", "diff")
	t.Logf("------+------------+------------+-------")

	for i := 0; i < nbBands; i++ {
		libVal := libEbits[i]
		gopusVal := gopusResult.FineBits[i]
		diff := gopusVal - libVal

		if libVal != 0 || gopusVal != 0 || diff != 0 {
			diffStr := ""
			if diff != 0 {
				diffStr = fmt.Sprintf("%+d", diff)
			}
			t.Logf("%-5d | %10d | %10d | %6s", i, libVal, gopusVal, diffStr)
		}
	}

	// Now trace the skip decision loop using gopus internals
	t.Logf("")
	t.Logf("=== SKIP DECISION TRACE (gopus logic) ===")
	traceSkipDecisionGopus(t, nbBands, channels, lm, trim, gopusCaps, offsets, totalBitsQ3, prev, signalBandwidth)
}

// traceSkipDecisionGopus traces through the skip decision logic manually.
func traceSkipDecisionGopus(t *testing.T, nbBands, channels, lm, trim int, caps, offsets []int, totalBitsQ3, prev, signalBandwidth int) {
	t.Helper()

	const bitRes = 3
	const allocSteps = 6

	start := 0
	end := nbBands

	allocFloor := channels << bitRes
	t.Logf("allocFloor = %d (channels=%d << bitRes=%d)", allocFloor, channels, bitRes)

	// Reserve bits for skip
	skipStart := start
	skipRsv := 0
	if totalBitsQ3 >= 1<<bitRes {
		skipRsv = 1 << bitRes
		totalBitsQ3 -= skipRsv
	}
	t.Logf("skipRsv = %d, remaining totalBitsQ3 = %d", skipRsv, totalBitsQ3)

	// Compute thresh and trimOffset
	thresh := make([]int, nbBands)
	trimOffset := make([]int, nbBands)
	for j := start; j < end; j++ {
		width := celt.EBands[j+1] - celt.EBands[j]
		thresh[j] = maxInt(channels<<bitRes, (3*(width<<lm)<<bitRes)>>4)
		trimOffset[j] = int(int64(channels*width*(trim-5-lm)*(end-j-1)*(1<<(lm+bitRes))) >> 6)
		if (width << lm) == 1 {
			trimOffset[j] -= channels << bitRes
		}
	}

	// Binary search for lo/hi
	lo := 1
	hi := len(celt.BandAlloc) - 1
	for lo <= hi {
		done := 0
		psum := 0
		mid := (lo + hi) >> 1
		for j := end; j > start; j-- {
			idx := j - 1
			width := celt.EBands[idx+1] - celt.EBands[idx]
			bitsj := (channels * width * celt.BandAlloc[mid][idx] << lm) >> 2
			if bitsj > 0 {
				bitsj = maxInt(0, bitsj+trimOffset[idx])
			}
			bitsj += offsets[idx]
			if bitsj >= thresh[idx] || done != 0 {
				done = 1
				psum += minInt(bitsj, caps[idx])
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
	t.Logf("Binary search result: lo=%d, hi=%d", lo, hi)

	// Compute bits1 and bits2
	bits1 := make([]int, nbBands)
	bits2 := make([]int, nbBands)
	for j := start; j < end; j++ {
		width := celt.EBands[j+1] - celt.EBands[j]
		bits1j := (channels * width * celt.BandAlloc[lo][j] << lm) >> 2
		bits2j := caps[j]
		if hi < len(celt.BandAlloc) {
			bits2j = (channels * width * celt.BandAlloc[hi][j] << lm) >> 2
		}
		if bits1j > 0 {
			bits1j = maxInt(0, bits1j+trimOffset[j])
		}
		if bits2j > 0 {
			bits2j = maxInt(0, bits2j+trimOffset[j])
		}
		if lo > 0 {
			bits1j += offsets[j]
		}
		bits2j += offsets[j]
		if offsets[j] > 0 {
			skipStart = j
		}
		bits2j = maxInt(0, bits2j-bits1j)
		bits1[j] = bits1j
		bits2[j] = bits2j
	}

	// Interpolation search for allocation
	loInterp := 0
	hiInterp := 1 << allocSteps
	for i := 0; i < allocSteps; i++ {
		mid := (loInterp + hiInterp) >> 1
		psum := 0
		done := 0
		for j := end; j > start; j-- {
			idx := j - 1
			tmp := bits1[idx] + int((int64(mid)*int64(bits2[idx]))>>allocSteps)
			if tmp >= thresh[idx] || done != 0 {
				done = 1
				psum += minInt(tmp, caps[idx])
			} else if tmp >= allocFloor {
				psum += allocFloor
			}
		}
		if psum > totalBitsQ3 {
			hiInterp = mid
		} else {
			loInterp = mid
		}
	}
	t.Logf("Interpolation search result: lo=%d", loInterp)

	// Compute initial bits[] allocation
	bits := make([]int, nbBands)
	psum := 0
	done := 0
	for j := end; j > start; j-- {
		idx := j - 1
		tmp := bits1[idx] + int((int64(loInterp)*int64(bits2[idx]))>>allocSteps)
		if tmp < thresh[idx] && done == 0 {
			if tmp >= allocFloor {
				tmp = allocFloor
			} else {
				tmp = 0
			}
		} else {
			done = 1
		}
		tmp = minInt(tmp, caps[idx])
		bits[idx] = tmp
		psum += tmp
	}
	t.Logf("Initial psum = %d, totalBitsQ3 = %d", psum, totalBitsQ3)

	// Now trace the skip decision loop
	t.Logf("")
	t.Logf("=== SKIP LOOP TRACE ===")
	t.Logf("%-5s | %6s | %10s | %10s | %10s | %12s | %8s | %s",
		"Band", "Width", "bits[j]", "thresh", "allocF+8", "bandBits", "passes?", "decision")
	t.Logf("------+--------+------------+------------+------------+--------------+----------+---------")

	codedBands := end
	for {
		j := codedBands - 1
		if j <= skipStart {
			totalBitsQ3 += skipRsv
			t.Logf("Reached skipStart (%d), adding back skipRsv, final codedBands=%d", skipStart, codedBands)
			break
		}

		left := totalBitsQ3 - psum
		percoeff := celtUdivLocal(left, celt.EBands[codedBands]-celt.EBands[start])
		left -= (celt.EBands[codedBands] - celt.EBands[start]) * percoeff
		rem := maxInt(left-(celt.EBands[j]-celt.EBands[start]), 0)
		bandWidth := celt.EBands[codedBands] - celt.EBands[j]
		bandBits := bits[j] + percoeff*bandWidth + rem

		minThreshold := maxInt(thresh[j], allocFloor+(1<<bitRes))
		passes := bandBits >= minThreshold

		width := celt.EBands[j+1] - celt.EBands[j]

		decision := "SKIP"
		if passes {
			// Encoder would decide here - we're tracing without encoder
			// Check if gopus would keep this band
			depthThreshold := 0
			if codedBands > 17 {
				if j < prev {
					depthThreshold = 7
				} else {
					depthThreshold = 9
				}
			}
			keep := codedBands <= start+2
			if !keep && depthThreshold > 0 {
				threshold := (depthThreshold * bandWidth << lm << bitRes) >> 4
				if bandBits > threshold && j <= signalBandwidth {
					keep = true
				}
			}
			if keep {
				decision = "KEEP"
			}
		}

		t.Logf("%-5d | %6d | %10d | %10d | %10d | %12d | %8v | %s",
			j, width, bits[j], thresh[j], minThreshold, bandBits, passes, decision)

		if passes && decision == "KEEP" {
			t.Logf("FINAL: Keeping band %d, codedBands=%d", j, codedBands)
			break
		}

		// Skip this band
		psum -= bits[j]
		if bandBits >= allocFloor {
			psum += allocFloor
			bits[j] = allocFloor
		} else {
			bits[j] = 0
		}
		codedBands--
	}
}

// TestSkipDecisionVariousBitrates tests skip decisions across various bitrates.
func TestSkipDecisionVariousBitrates(t *testing.T) {
	bitrates := []int{32000, 48000, 64000, 96000, 128000}
	frameSize := 960
	channels := 1
	nbBands := 21
	lm := 3
	trim := 5

	for _, bitrate := range bitrates {
		t.Run(fmt.Sprintf("%dkbps", bitrate/1000), func(t *testing.T) {
			totalBitsQ3 := (bitrate * frameSize * 8) / 48000

			libopusCaps := LibopusComputeCaps(nbBands, lm, channels)
			offsets := make([]int, nbBands)

			libCodedBands, _, _, _, _, _, _ :=
				LibopusComputeAllocation(0, nbBands, offsets, libopusCaps, trim, nbBands, 0, totalBitsQ3, channels, lm, 0, nbBands-1)

			gopusResult := celt.ComputeAllocationWithEncoder(
				nil,
				totalBitsQ3>>3,
				nbBands,
				channels,
				libopusCaps,
				offsets,
				trim,
				nbBands,
				false,
				lm,
				0,
				nbBands-1,
			)

			status := "OK"
			if libCodedBands != gopusResult.CodedBands {
				status = "MISMATCH"
			}

			t.Logf("%dkbps: libopus=%d, gopus=%d - %s",
				bitrate/1000, libCodedBands, gopusResult.CodedBands, status)
		})
	}
}

// celtUdivLocal is a local helper for unsigned division.
func celtUdivLocal(a, b int) int {
	if b == 0 {
		return 0
	}
	return a / b
}
