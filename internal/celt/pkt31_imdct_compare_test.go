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

func TestPkt31IMDCTCompare(t *testing.T) {
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

	// Decode packets 0-30 to get to packet 31's state
	for i := 0; i < 31; i++ {
		pkt := packets[i]
		toc := pkt[0]
		cfg := toc >> 3
		frameSize := getFrameSize(cfg)
		bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))
		dec.SetBandwidth(bw)
		dec.DecodeFrame(pkt[1:], frameSize)
	}

	// Get packet 31 coefficients
	pkt := packets[31]
	toc := pkt[0]
	cfg := toc >> 3
	frameSize := getFrameSize(cfg)
	bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))
	dec.SetBandwidth(bw)

	rd := &rangecoding.Decoder{}
	rd.Init(pkt[1:])
	dec.SetRangeDecoder(rd)

	mode := GetModeConfig(frameSize)
	lm := mode.LM
	end := EffectiveBandsForFrameSize(dec.Bandwidth(), frameSize)
	if end > mode.EffBands {
		end = mode.EffBands
	}

	prev1LogE := append([]float64(nil), dec.prevLogE...)
	prev2LogE := append([]float64(nil), dec.prevLogE2...)

	totalBits := len(pkt[1:]) * 8
	tell := rd.Tell()
	silence := tell >= totalBits || (tell == 1 && rd.DecodeBit(15) == 1)
	if silence {
		t.Skip("Silence frame")
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
	coeffsL, _, collapse := quantAllBandsDecode(rd, dec.channels, frameSize, lm, 0, end, pulses, 1, spread,
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

	fmt.Printf("Packet 31 coefficients: len=%d\n", len(coeffsL))
	fmt.Printf("Last 20 coeffs [940:960]: ")
	for i := 940; i < 960; i++ {
		fmt.Printf("%.4f ", coeffsL[i])
	}
	fmt.Printf("\n")

	// Compute DFT-based IMDCT (what imdctOverlapWithPrev uses internally)
	n2 := len(coeffsL) // 960
	n := n2 * 2        // 1920
	n4 := n2 / 2       // 480

	trig := getMDCTTrig(n)
	fftIn := make([]complex128, n4)
	for i := 0; i < n4; i++ {
		x1 := coeffsL[2*i]
		x2 := coeffsL[n2-1-2*i]
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := x2*t0 + x1*t1
		yi := x1*t0 - x2*t1
		fftIn[i] = complex(yi, yr)
	}

	fftOut := dft(fftIn)
	bufDFT := make([]float64, n2)
	for i := 0; i < n4; i++ {
		v := fftOut[i]
		bufDFT[2*i] = real(v)
		bufDFT[2*i+1] = imag(v)
	}

	// Post-rotate
	yp0 := 0
	yp1 := n2 - 2
	for i := 0; i < (n4+1)>>1; i++ {
		re := bufDFT[yp0+1]
		im := bufDFT[yp0]
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := re*t0 + im*t1
		yi := re*t1 - im*t0
		re2 := bufDFT[yp1+1]
		im2 := bufDFT[yp1]
		bufDFT[yp0] = yr
		bufDFT[yp1+1] = yi
		t0 = trig[n4-i-1]
		t1 = trig[n2-i-1]
		yr = re2*t0 + im2*t1
		yi = re2*t1 - im2*t0
		bufDFT[yp1] = yr
		bufDFT[yp0+1] = yi
		yp0 += 2
		yp1 -= 2
	}

	fmt.Printf("\nDFT-based IMDCT (N=%d samples):\n", n2)
	fmt.Printf("  First 10: ")
	for i := 0; i < 10; i++ {
		fmt.Printf("%.2f ", bufDFT[i])
	}
	fmt.Printf("\n  [900:910]: ")
	for i := 900; i < 910; i++ {
		fmt.Printf("%.2f ", bufDFT[i])
	}
	fmt.Printf("\n  [950:960]: ")
	for i := 950; i < 960; i++ {
		fmt.Printf("%.2f ", bufDFT[i])
	}
	fmt.Printf("\n")

	// Compute Direct IMDCT
	directOut := IMDCTDirect(coeffsL)
	fmt.Printf("\nDirect IMDCT (2N=%d samples):\n", len(directOut))
	fmt.Printf("  First 10: ")
	for i := 0; i < 10; i++ {
		fmt.Printf("%.2f ", directOut[i])
	}
	fmt.Printf("\n  [900:910]: ")
	for i := 900; i < 910; i++ {
		fmt.Printf("%.2f ", directOut[i])
	}
	fmt.Printf("\n  [950:960]: ")
	for i := 950; i < 960; i++ {
		fmt.Printf("%.2f ", directOut[i])
	}
	fmt.Printf("\n  [1900:1910]: ")
	for i := 1900; i < 1910; i++ {
		fmt.Printf("%.2f ", directOut[i])
	}
	fmt.Printf("\n")

	// Compare DFT-based with Direct IMDCT first N samples
	fmt.Printf("\nComparison DFT vs Direct (first N=%d samples):\n", n2)
	var maxDiff float64
	maxDiffIdx := 0
	for i := 0; i < n2; i++ {
		diff := math.Abs(bufDFT[i] - directOut[i])
		if diff > maxDiff {
			maxDiff = diff
			maxDiffIdx = i
		}
	}
	fmt.Printf("  Max difference: %.4f at index %d\n", maxDiff, maxDiffIdx)
	fmt.Printf("  DFT[%d]=%.4f, Direct[%d]=%.4f\n", maxDiffIdx, bufDFT[maxDiffIdx], maxDiffIdx, directOut[maxDiffIdx])

	// Show comparison at end
	fmt.Printf("\nComparison at end (where overlap comes from):\n")
	fmt.Printf("  idx      DFT     Direct     diff\n")
	for i := 950; i < 960; i++ {
		diff := bufDFT[i] - directOut[i]
		fmt.Printf("  %3d %8.2f %10.2f %8.2f\n", i, bufDFT[i], directOut[i], diff)
	}
}
