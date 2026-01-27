package celt

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

func TestPacket31CoeffsTrace(t *testing.T) {
	testDir := filepath.Join("..", "testvectors", "testdata", "opus_testvectors")
	bitFile := filepath.Join(testDir, "testvector07.bit")

	bitData, err := os.ReadFile(bitFile)
	if err != nil {
		t.Skipf("Test data not available: %v", err)
	}

	var packets [][]byte
	offset := 0
	for offset+8 <= len(bitData) {
		packetLen := binary.BigEndian.Uint32(bitData[offset:])
		offset += 8
		if offset+int(packetLen) > len(bitData) {
			break
		}
		packets = append(packets, bitData[offset:offset+int(packetLen)])
		offset += int(packetLen)
	}

	dec := NewDecoder(1)

	// Decode packets 0-29 normally
	for i := 0; i < 30; i++ {
		pkt := packets[i]
		toc := pkt[0]
		cfg := toc >> 3
		frameSize := getFrameSize(cfg)
		bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))
		dec.SetBandwidth(bw)
		dec.DecodeFrame(pkt[1:], frameSize)
	}

	// Now decode packets 30 and 31 with coefficient tracing
	for pktIdx := 30; pktIdx <= 31; pktIdx++ {
		pkt := packets[pktIdx]
		toc := pkt[0]
		cfg := toc >> 3
		frameSize := getFrameSize(cfg)
		bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))
		dec.SetBandwidth(bw)

		fmt.Printf("\n=== PACKET %d COEFFICIENT ANALYSIS ===\n", pktIdx)

		// Manual decode to extract coefficients
		rd := &rangecoding.Decoder{}
		rd.Init(pkt[1:])
		dec.SetRangeDecoder(rd)

		mode := GetModeConfig(frameSize)
		lm := mode.LM
		end := EffectiveBandsForFrameSize(dec.Bandwidth(), frameSize)
		if end > mode.EffBands {
			end = mode.EffBands
		}

		prev1Energy := append([]float64(nil), dec.PrevEnergy()...)
		prev1LogE := append([]float64(nil), dec.prevLogE...)
		prev2LogE := append([]float64(nil), dec.prevLogE2...)

		totalBits := len(pkt[1:]) * 8
		tell := rd.Tell()
		silence := false
		if tell >= totalBits {
			silence = true
		} else if tell == 1 {
			silence = rd.DecodeBit(15) == 1
		}

		if silence {
			fmt.Printf("SILENCE FRAME\n")
			continue
		}

		// Skip postfilter
		if tell+16 <= totalBits {
			if rd.DecodeBit(1) == 1 {
				octave := int(rd.DecodeUniform(6))
				rd.DecodeRawBits(uint(4 + octave))
				rd.DecodeRawBits(3)
				if rd.Tell()+2 <= totalBits {
					rd.DecodeICDF(tapsetICDF, 2)
				}
			}
		}

		transient := false
		if lm > 0 && rd.Tell()+3 <= totalBits {
			transient = rd.DecodeBit(3) == 1
		}
		intra := false
		if rd.Tell()+3 <= totalBits {
			intra = rd.DecodeBit(3) == 1
		}

		fmt.Printf("Transient: %v, Intra: %v\n", transient, intra)

		shortBlocks := 1
		if transient {
			shortBlocks = mode.ShortBlocks
		}

		// Decode energies
		energies := dec.DecodeCoarseEnergy(end, intra, lm)

		// TF decode
		tfRes := make([]int, end)
		tfDecode(0, end, transient, tfRes, lm, rd)

		// Spread
		spread := spreadNormal
		if rd.Tell()+4 <= totalBits {
			spread = rd.DecodeICDF(spreadICDF, 5)
		}

		// Allocation
		cap := initCaps(end, lm, dec.channels)
		offsets := make([]int, end)
		dynallocLogp := 6
		totalBitsQ3 := totalBits << bitRes
		tellFrac := rd.TellFrac()
		for i := 0; i < end; i++ {
			width := dec.channels * (EBands[i+1] - EBands[i]) << lm
			quanta := minInt(width<<bitRes, maxInt(6<<bitRes, width))
			dynallocLoopLogp := dynallocLogp
			boost := 0
			for tellFrac+(dynallocLoopLogp<<bitRes) < totalBitsQ3 && boost < cap[i] {
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
			if boost > 0 {
				dynallocLogp = maxInt(2, dynallocLogp-1)
			}
		}

		allocTrim := 5
		if rd.TellFrac()+(6<<bitRes) <= totalBitsQ3 {
			allocTrim = rd.DecodeICDF(trimICDF, 7)
		}

		bitsQ3 := (totalBits << bitRes) - rd.TellFrac() - 1
		antiCollapseRsv := 0
		if transient && lm >= 2 && bitsQ3 >= (lm+2)<<bitRes {
			antiCollapseRsv = 1 << bitRes
		}
		bitsQ3 -= antiCollapseRsv

		pulses := make([]int, end)
		fineQuant := make([]int, end)
		finePriority := make([]int, end)
		intensity := 0
		dualStereo := 0
		balance := 0
		codedBands := cltComputeAllocation(0, end, offsets, cap, allocTrim, &intensity, &dualStereo,
			bitsQ3, &balance, pulses, fineQuant, finePriority, dec.channels, lm, rd)

		dec.DecodeFineEnergy(energies, end, fineQuant)

		// Decode PVQ coefficients
		coeffsL, _, collapse := quantAllBandsDecode(rd, dec.channels, frameSize, lm, 0, end, pulses, shortBlocks, spread,
			dualStereo, intensity, tfRes, (totalBits<<bitRes)-antiCollapseRsv, balance, codedBands, dec.channels == 1, &dec.rng)

		// Process anti-collapse
		antiCollapseOn := false
		if antiCollapseRsv > 0 {
			antiCollapseOn = rd.DecodeRawBits(1) == 1
		}

		bitsLeft := totalBits - rd.Tell()
		dec.DecodeEnergyFinalise(energies, end, fineQuant, finePriority, bitsLeft)

		if antiCollapseOn {
			antiCollapse(coeffsL, nil, collapse, lm, dec.channels, 0, end, energies, prev1LogE, prev2LogE, pulses, dec.rng)
		}

		fmt.Printf("Coded bands: %d, Collapse mask: %032b\n", codedBands, collapse)

		// Print energy info
		fmt.Printf("\nEnergies (first 10 bands):\n")
		for i := 0; i < 10 && i < end; i++ {
			fmt.Printf("  Band %2d: energy=%.4f, prev=%.4f, diff=%.4f\n",
				i, energies[i], prev1Energy[i], energies[i]-prev1Energy[i])
		}

		// Denormalize
		denormalizeCoeffs(coeffsL, energies, end, frameSize)

		// Print coefficient statistics per band
		fmt.Printf("\nCoefficient stats by band:\n")
		bandStart := 0
		for band := 0; band < end && band < 10; band++ {
			bandEnd := EBands[band+1] << lm
			if bandEnd > frameSize {
				bandEnd = frameSize
			}

			var sum, sumAbs, maxVal float64
			for i := bandStart; i < bandEnd && i < len(coeffsL); i++ {
				c := coeffsL[i]
				sum += c
				if math.Abs(c) > maxVal {
					maxVal = math.Abs(c)
				}
				sumAbs += math.Abs(c)
			}
			count := bandEnd - bandStart
			if count > 0 {
				fmt.Printf("  Band %2d [%4d:%4d]: avg=%.4f, avgAbs=%.4f, max=%.4f\n",
					band, bandStart, bandEnd, sum/float64(count), sumAbs/float64(count), maxVal)
			}
			bandStart = bandEnd
		}

		// Print overall coefficient stats
		var totalSum, totalSumAbs, totalMax float64
		for _, c := range coeffsL {
			totalSum += c
			totalSumAbs += math.Abs(c)
			if math.Abs(c) > totalMax {
				totalMax = math.Abs(c)
			}
		}
		fmt.Printf("\nOverall coeffs: sum=%.4f, avgAbs=%.6f, max=%.4f\n",
			totalSum, totalSumAbs/float64(len(coeffsL)), totalMax)

		// Print last 60 coefficients (which affect overlap)
		fmt.Printf("\nLast 60 coefficients (affect overlap buffer):\n")
		for i := len(coeffsL) - 60; i < len(coeffsL); i += 10 {
			fmt.Printf("  [%d:%d]: ", i, i+10)
			for j := i; j < i+10 && j < len(coeffsL); j++ {
				fmt.Printf("%.3f ", coeffsL[j])
			}
			fmt.Printf("\n")
		}

		// Now do synthesis to see overlap
		samples := dec.Synthesize(coeffsL, transient, shortBlocks)

		// Update state
		dec.updateLogE(energies, end, transient)
		dec.SetPrevEnergyWithPrev(prev1Energy, energies)

		fmt.Printf("\nSynthesis output: %d samples\n", len(samples))
		fmt.Printf("Overlap buffer RMS after synthesis: %.4f\n", computeRMS(dec.OverlapBuffer()))

		// Print first and last samples
		fmt.Printf("First 10 samples: ")
		for i := 0; i < 10 && i < len(samples); i++ {
			fmt.Printf("%.4f ", samples[i])
		}
		fmt.Printf("\nLast 10 samples: ")
		for i := len(samples) - 10; i < len(samples); i++ {
			fmt.Printf("%.4f ", samples[i])
		}
		fmt.Printf("\n")
	}
}
