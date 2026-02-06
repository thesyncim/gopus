package silk

import (
	"fmt"
	"math"
	"testing"
)

// TestDiagnose10msGainEvolution traces gain evolution across many 10ms frames
// to find where the 2x amplitude corruption begins.
func TestDiagnose10msGainEvolution(t *testing.T) {
	bw := BandwidthNarrowband
	cfg := GetBandwidthConfig(bw)
	subfrSamples := cfg.SubframeSamples
	numSubframes := 2 // 10ms
	frameSamples := numSubframes * subfrSamples

	enc := NewEncoder(bw)
	enc.SetBitrate(32000)

	dec := NewDecoder()

	numFrames := 60

	// Generate AM multi-frequency test signal at SILK rate
	totalSamples := numFrames * frameSamples
	pcm := make([]float32, totalSamples+frameSamples)
	freqs := []float64{440.0 * float64(cfg.SampleRate) / 48000.0} // Scale to 8kHz
	amp := 0.3
	for i := range pcm {
		t := float64(i) / float64(cfg.SampleRate)
		var val float64
		for _, freq := range freqs {
			val += amp * math.Sin(2*math.Pi*freq*t)
		}
		pcm[i] = float32(val)
	}

	fmt.Println("Frame  pktLen  sigType  prevGainIdx  gainInd[0]  gainInd[1]  nsqPrevGainQ16  decRMS     origRMS    ratio")
	for frame := 0; frame < numFrames; frame++ {
		start := frame * frameSamples
		end := start + frameSamples
		framePCM := pcm[start:end]

		// Capture state before encode
		prevGainIdx := enc.previousGainIndex
		nsqPrevGain := int32(0)
		if enc.nsqState != nil {
			nsqPrevGain = enc.nsqState.prevGainQ16
		}

		pkt := enc.EncodeFrame(framePCM, nil, true)
		if pkt == nil || len(pkt) == 0 {
			continue
		}

		// Capture state after encode
		gainInd0 := int8(0)
		gainInd1 := int8(0)
		if len(enc.scratchGainInd) >= 2 {
			gainInd0 = enc.scratchGainInd[0]
			gainInd1 = enc.scratchGainInd[1]
		}
		sigType := enc.ecPrevSignalType

		// Decode
		cp := make([]byte, len(pkt))
		copy(cp, pkt)
		fsAt48k := frameSamples * 48000 / cfg.SampleRate
		out, err := dec.Decode(cp, bw, fsAt48k, true)
		if err != nil {
			continue
		}

		// Compute RMS
		var origE, decE float64
		for _, v := range framePCM {
			origE += float64(v) * float64(v)
		}
		origRMS := math.Sqrt(origE / float64(len(framePCM)))
		for _, v := range out {
			decE += float64(v) * float64(v)
		}
		decRMS := math.Sqrt(decE / float64(len(out)))

		ratio := decRMS / (origRMS + 1e-10)
		marker := ""
		if ratio > 1.5 || ratio < 0.3 {
			marker = " <-- BAD"
		}

		fmt.Printf("  %2d    %3d      %d        %2d         %3d         %3d       %10d    %.4f    %.4f    %.2f%s\n",
			frame, len(pkt), sigType, prevGainIdx, gainInd0, gainInd1, nsqPrevGain, decRMS, origRMS, ratio, marker)
	}
}

// TestDiagnose10msGainEvolution20ms traces gain evolution for 20ms frames as baseline
func TestDiagnose10msGainEvolution20ms(t *testing.T) {
	bw := BandwidthNarrowband
	cfg := GetBandwidthConfig(bw)
	subfrSamples := cfg.SubframeSamples
	numSubframes := 4 // 20ms
	frameSamples := numSubframes * subfrSamples

	enc := NewEncoder(bw)
	enc.SetBitrate(32000)

	dec := NewDecoder()

	numFrames := 30

	// Generate same test signal at SILK rate
	totalSamples := numFrames * frameSamples
	pcm := make([]float32, totalSamples+frameSamples)
	freqs := []float64{440.0 * float64(cfg.SampleRate) / 48000.0}
	amp := 0.3
	for i := range pcm {
		t := float64(i) / float64(cfg.SampleRate)
		var val float64
		for _, freq := range freqs {
			val += amp * math.Sin(2*math.Pi*freq*t)
		}
		pcm[i] = float32(val)
	}

	fmt.Println("Frame  pktLen  sigType  prevGainIdx  gainInd[0..3]  nsqPrevGainQ16  decRMS     origRMS    ratio")
	for frame := 0; frame < numFrames; frame++ {
		start := frame * frameSamples
		end := start + frameSamples
		framePCM := pcm[start:end]

		prevGainIdx := enc.previousGainIndex

		pkt := enc.EncodeFrame(framePCM, nil, true)
		if pkt == nil || len(pkt) == 0 {
			continue
		}

		gainInds := [4]int8{}
		for i := 0; i < 4 && i < len(enc.scratchGainInd); i++ {
			gainInds[i] = enc.scratchGainInd[i]
		}
		sigType := enc.ecPrevSignalType

		cp := make([]byte, len(pkt))
		copy(cp, pkt)
		fsAt48k := frameSamples * 48000 / cfg.SampleRate
		out, err := dec.Decode(cp, bw, fsAt48k, true)
		if err != nil {
			continue
		}

		var origE, decE float64
		for _, v := range framePCM {
			origE += float64(v) * float64(v)
		}
		origRMS := math.Sqrt(origE / float64(len(framePCM)))
		for _, v := range out {
			decE += float64(v) * float64(v)
		}
		decRMS := math.Sqrt(decE / float64(len(out)))

		ratio := decRMS / (origRMS + 1e-10)
		fmt.Printf("  %2d    %3d      %d        %2d     [%3d,%3d,%3d,%3d]      %10s    %.4f    %.4f    %.2f\n",
			frame, len(pkt), sigType, prevGainIdx, gainInds[0], gainInds[1], gainInds[2], gainInds[3], "", decRMS, origRMS, ratio)
	}
}
