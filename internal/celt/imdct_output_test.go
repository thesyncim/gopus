package celt

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

func TestIMDCTOutputTrace(t *testing.T) {
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

	// Now test IMDCT for packets 30 and 31
	for pktIdx := 30; pktIdx <= 31; pktIdx++ {
		pkt := packets[pktIdx]
		toc := pkt[0]
		cfg := toc >> 3
		frameSize := getFrameSize(cfg)
		bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))
		dec.SetBandwidth(bw)

		fmt.Printf("\n=== PACKET %d IMDCT ANALYSIS ===\n", pktIdx)

		// Decode to get coefficients
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
		silence := tell >= totalBits || (tell == 1 && rd.DecodeBit(15) == 1)
		if silence {
			continue
		}

		// Skip postfilter
		if rd.Tell()+16 <= totalBits && rd.DecodeBit(1) == 1 {
			octave := int(rd.DecodeUniform(6))
			rd.DecodeRawBits(uint(4 + octave))
			rd.DecodeRawBits(3)
			if rd.Tell()+2 <= totalBits {
				rd.DecodeICDF(tapsetICDF, 2)
			}
		}

		transient := lm > 0 && rd.Tell()+3 <= totalBits && rd.DecodeBit(3) == 1
		intra := rd.Tell()+3 <= totalBits && rd.DecodeBit(3) == 1

		shortBlocks := 1
		if transient {
			shortBlocks = mode.ShortBlocks
		}

		// Decode energies and coefficients
		energies := dec.DecodeCoarseEnergy(end, intra, lm)
		tfRes := make([]int, end)
		tfDecode(0, end, transient, tfRes, lm, rd)
		spread := spreadNormal
		if rd.Tell()+4 <= totalBits {
			spread = rd.DecodeICDF(spreadICDF, 5)
		}

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
		coeffsL, _, collapse := quantAllBandsDecode(rd, dec.channels, frameSize, lm, 0, end, pulses, shortBlocks, spread,
			dualStereo, intensity, tfRes, (totalBits<<bitRes)-antiCollapseRsv, balance, codedBands, dec.channels == 1, &dec.rng)

		antiCollapseOn := false
		if antiCollapseRsv > 0 {
			antiCollapseOn = rd.DecodeRawBits(1) == 1
		}
		bitsLeft := totalBits - rd.Tell()
		dec.DecodeEnergyFinalise(energies, end, fineQuant, finePriority, bitsLeft)

		if antiCollapseOn {
			antiCollapse(coeffsL, nil, collapse, lm, dec.channels, 0, end, energies, prev1LogE, prev2LogE, pulses, dec.rng)
		}

		denormalizeCoeffs(coeffsL, energies, end, frameSize)

		// Get current overlap buffer (before synthesis)
		prevOverlap := make([]float64, len(dec.OverlapBuffer()))
		copy(prevOverlap, dec.OverlapBuffer())

		fmt.Printf("PrevOverlap first 10: ")
		for i := 0; i < 10; i++ {
			fmt.Printf("%.2f ", prevOverlap[i])
		}
		fmt.Printf("\n")
		fmt.Printf("PrevOverlap last 10 [110:120]: ")
		for i := 110; i < 120; i++ {
			fmt.Printf("%.2f ", prevOverlap[i])
		}
		fmt.Printf("\n")

		// Do IMDCT directly to see the output before overlap-add
		imdctOut := imdctOverlapWithPrev(coeffsL, prevOverlap, Overlap)

		fmt.Printf("\nimdctOverlapWithPrev output length: %d\n", len(imdctOut))

		fmt.Printf("IMDCT output [0:10] (should be TDAC result): ")
		for i := 0; i < 10; i++ {
			fmt.Printf("%.2f ", imdctOut[i])
		}
		fmt.Printf("\n")

		fmt.Printf("IMDCT output [60:70] (after TDAC region): ")
		for i := 60; i < 70; i++ {
			fmt.Printf("%.2f ", imdctOut[i])
		}
		fmt.Printf("\n")

		fmt.Printf("IMDCT output [900:910] (start of new overlap): ")
		for i := 900; i < 910; i++ {
			fmt.Printf("%.2f ", imdctOut[i])
		}
		fmt.Printf("\n")

		fmt.Printf("IMDCT output [960:970] (new overlap first 10): ")
		for i := 960; i < 970 && i < len(imdctOut); i++ {
			fmt.Printf("%.2f ", imdctOut[i])
		}
		fmt.Printf("\n")

		fmt.Printf("IMDCT output [1020:1030] (should be prevOverlap[60:70]): ")
		for i := 1020; i < 1030 && i < len(imdctOut); i++ {
			fmt.Printf("%.2f ", imdctOut[i])
		}
		fmt.Printf("\n")

		// What we're using as new overlap
		newOverlapFromIMDCT := imdctOut[960:1080]
		fmt.Printf("\nNew overlap [0:10]: ")
		for i := 0; i < 10; i++ {
			fmt.Printf("%.2f ", newOverlapFromIMDCT[i])
		}
		fmt.Printf("\n")
		fmt.Printf("New overlap [50:60]: ")
		for i := 50; i < 60; i++ {
			fmt.Printf("%.2f ", newOverlapFromIMDCT[i])
		}
		fmt.Printf("\n")
		fmt.Printf("New overlap [60:70] (zeros?): ")
		for i := 60; i < 70; i++ {
			fmt.Printf("%.2f ", newOverlapFromIMDCT[i])
		}
		fmt.Printf("\n")

		// Now do the actual synthesis to update state
		samples := dec.Synthesize(coeffsL, transient, shortBlocks)
		dec.updateLogE(energies, end, transient)
		dec.SetPrevEnergyWithPrev(prev1Energy, energies)

		fmt.Printf("\nFinal output samples [0:10]: ")
		for i := 0; i < 10; i++ {
			fmt.Printf("%.2f ", samples[i])
		}
		fmt.Printf("\n")

		fmt.Printf("Overlap buffer after synthesis [0:10]: ")
		for i := 0; i < 10; i++ {
			fmt.Printf("%.2f ", dec.OverlapBuffer()[i])
		}
		fmt.Printf("\n")
		fmt.Printf("Overlap buffer after synthesis [110:120]: ")
		for i := 110; i < 120; i++ {
			fmt.Printf("%.2f ", dec.OverlapBuffer()[i])
		}
		fmt.Printf("\n")
		fmt.Printf("Overlap RMS: %.4f\n", computeRMS(dec.OverlapBuffer()))
	}
}
