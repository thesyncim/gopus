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

func TestCoefficientPipelineTrace(t *testing.T) {
	testDir := filepath.Join("..", "testvectors", "testdata", "opus_testvectors")
	bitFile := filepath.Join(testDir, "testvector07.bit")

	bitData, err := os.ReadFile(bitFile)
	if err != nil {
		t.Skipf("Test data not available: %v", err)
	}

	// Parse first packet
	packetLen := binary.BigEndian.Uint32(bitData[0:])
	pkt := bitData[8 : 8+int(packetLen)]

	toc := pkt[0]
	cfg := toc >> 3
	frameSize := getFrameSize(cfg)
	bw := BandwidthFromOpusConfig(int(getBandwidthType(cfg)))

	fmt.Printf("First packet: %d bytes, cfg=%d, frameSize=%d, bw=%v\n", len(pkt), cfg, frameSize, bw)

	// Initialize decoder
	dec := NewDecoder(1)
	dec.SetBandwidth(bw)

	// Initialize range decoder
	rd := &rangecoding.Decoder{}
	rd.Init(pkt[1:])
	dec.SetRangeDecoder(rd)

	// Get mode configuration
	mode := GetModeConfig(frameSize)
	lm := mode.LM
	end := EffectiveBandsForFrameSize(bw, frameSize)
	if end > mode.EffBands {
		end = mode.EffBands
	}
	if end < 1 {
		end = 1
	}
	start := 0

	fmt.Printf("Mode: LM=%d, EffBands=%d, end=%d\n", lm, mode.EffBands, end)

	totalBits := len(pkt[1:]) * 8
	tell := rd.Tell()

	// Check silence
	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}
	fmt.Printf("Silence: %v\n", silence)
	if silence {
		return
	}

	// Skip postfilter
	if start == 0 && tell+16 <= totalBits {
		if rd.DecodeBit(1) == 1 {
			octave := int(rd.DecodeUniform(6))
			rd.DecodeRawBits(uint(4 + octave))
			rd.DecodeRawBits(3)
			if rd.Tell()+2 <= totalBits {
				rd.DecodeICDF([]uint8{2, 1, 0}, 2)
			}
		}
	}

	// Decode transient and intra
	transient := false
	if lm > 0 {
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
	fmt.Printf("ShortBlocks: %d\n", shortBlocks)

	// Decode coarse energy
	energies := dec.DecodeCoarseEnergy(end, intra, lm)
	fmt.Printf("\nCoarse energies (first 10 bands):\n")
	for i := 0; i < 10 && i < end; i++ {
		fmt.Printf("  Band %d: %.4f\n", i, energies[i])
	}

	// Skip TF, spread, dynalloc, trim
	tfRes := make([]int, end)
	tfDecode(start, end, transient, tfRes, lm, rd)

	tell = rd.Tell()
	spread := spreadNormal
	if tell+4 <= totalBits {
		spread = rd.DecodeICDF(spreadICDF, 5)
	}
	_ = spread

	cap := initCaps(end, lm, 1)
	offsets := make([]int, end)
	dynallocLogp := 6
	totalBitsQ3 := totalBits << bitRes
	tellFrac := rd.TellFrac()
	for i := start; i < end; i++ {
		width := (EBands[i+1] - EBands[i]) << lm
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
	if tellFrac+(6<<bitRes) <= totalBitsQ3 {
		allocTrim = rd.DecodeICDF(trimICDF, 7)
	}
	_ = allocTrim

	bitsQ3 := (totalBits << bitRes) - rd.TellFrac() - 1
	antiCollapseRsv := 0
	if transient && lm >= 2 && bitsQ3 >= (lm+2)<<bitRes {
		antiCollapseRsv = 1 << bitRes
	}
	bitsQ3 -= antiCollapseRsv

	// Compute allocation
	pulses := make([]int, end)
	fineQuant := make([]int, end)
	finePriority := make([]int, end)
	intensity := 0
	dualStereo := 0
	balance := 0
	codedBands := cltComputeAllocation(start, end, offsets, cap, allocTrim, &intensity, &dualStereo,
		bitsQ3, &balance, pulses, fineQuant, finePriority, 1, lm, rd)
	_ = codedBands

	fmt.Printf("\nPulses (first 10 bands):\n")
	for i := 0; i < 10 && i < end; i++ {
		fmt.Printf("  Band %d: pulses=%d\n", i, pulses[i])
	}

	// Decode fine energy
	dec.DecodeFineEnergy(energies, end, fineQuant)
	fmt.Printf("\nFine energies (first 10 bands):\n")
	for i := 0; i < 10 && i < end; i++ {
		fmt.Printf("  Band %d: %.4f\n", i, energies[i])
	}

	// Decode PVQ coefficients (shape)
	coeffsL, _, collapse := quantAllBandsDecode(rd, 1, frameSize, lm, start, end, pulses, shortBlocks, spread,
		dualStereo, intensity, tfRes, (totalBits<<bitRes)-antiCollapseRsv, balance, codedBands, &dec.rng)
	_ = collapse

	fmt.Printf("\nRaw coefficients (before denormalization):\n")
	offset := 0
	for band := 0; band < 10 && band < end; band++ {
		width := ScaledBandWidth(band, frameSize)
		var rms float64
		for i := 0; i < width && offset+i < len(coeffsL); i++ {
			rms += coeffsL[offset+i] * coeffsL[offset+i]
		}
		rms = math.Sqrt(rms / float64(width))
		fmt.Printf("  Band %d: width=%d, RMS=%.6f, first=%.6f\n", band, width, rms, coeffsL[offset])
		offset += width
	}

	// Denormalize
	denormalizeCoeffs(coeffsL, energies, end, frameSize)

	fmt.Printf("\nDenormalized coefficients:\n")
	offset = 0
	for band := 0; band < 10 && band < end; band++ {
		width := ScaledBandWidth(band, frameSize)
		var rms float64
		for i := 0; i < width && offset+i < len(coeffsL); i++ {
			rms += coeffsL[offset+i] * coeffsL[offset+i]
		}
		rms = math.Sqrt(rms / float64(width))
		gain := math.Exp2(energies[band] / DB6)
		fmt.Printf("  Band %d: width=%d, RMS=%.9f, gain=%.6f, energy=%.4f\n", band, width, rms, gain, energies[band])
		offset += width
	}

	// Total coefficient RMS
	var totalRMS float64
	for _, c := range coeffsL {
		totalRMS += c * c
	}
	totalRMS = math.Sqrt(totalRMS / float64(len(coeffsL)))
	fmt.Printf("\nTotal coefficient RMS: %.9f\n", totalRMS)

	// Synthesize
	samples := dec.Synthesize(coeffsL, transient, shortBlocks)
	fmt.Printf("\nSynthesized samples:\n")
	var sampleRMS float64
	for _, s := range samples {
		sampleRMS += s * s
	}
	sampleRMS = math.Sqrt(sampleRMS / float64(len(samples)))
	fmt.Printf("  Count: %d, RMS=%.9f\n", len(samples), sampleRMS)

	// After scaling (as done in DecodeFrame)
	scaleSamples(samples, 1.0/32768.0)
	sampleRMS = 0
	for _, s := range samples {
		sampleRMS += s * s
	}
	sampleRMS = math.Sqrt(sampleRMS / float64(len(samples)))
	fmt.Printf("  After scaling: RMS=%.12f\n", sampleRMS)
}
