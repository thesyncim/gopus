// Package cgo traces libopus spread decision for debugging.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTraceLibopusSpread decodes the spread value from a libopus packet.
func TestTraceLibopusSpread(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm32 := make([]float32, frameSize)
	for i := range pcm32 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm32[i] = float32(val)
	}

	// Encode with libopus
	libEnc, err := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("libopus encoder creation failed: %v", err)
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(false)

	libPacket, _ := libEnc.EncodeFloat(pcm32, frameSize)
	libPayload := libPacket[1:]
	t.Logf("libopus packet: %d bytes (payload %d bytes)", len(libPacket), len(libPayload))
	t.Logf("First 16 bytes: %02x", libPayload[:16])

	// Decode the packet to extract spread decision
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM
	targetBits := 159 * 8

	rd := &rangecoding.Decoder{}
	rd.Init(libPayload)

	// Decode header
	silence := rd.DecodeBit(15)
	postfilter := rd.DecodeBit(1)
	transient := rd.DecodeBit(3)
	intra := rd.DecodeBit(3)
	t.Logf("Header: silence=%d postfilter=%d transient=%d intra=%d", silence, postfilter, transient, intra)

	// Decode coarse energy
	goDec := celt.NewDecoder(1)
	coarse := goDec.DecodeCoarseEnergyWithDecoder(rd, nbBands, intra == 1, lm)
	t.Logf("Tell after coarse: %d bits", rd.Tell())
	t.Logf("Coarse energies (first 5): %.4f, %.4f, %.4f, %.4f, %.4f",
		coarse[0], coarse[1], coarse[2], coarse[3], coarse[4])

	// Decode TF
	tfRes := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		tfRes[i] = rd.DecodeBit(1)
	}
	tfSelect := rd.DecodeBit(1)
	t.Logf("TF select=%d, Tell after TF: %d bits", tfSelect, rd.Tell())

	// Now decode spread
	spreadICDF := []uint8{25, 23, 2, 0}
	spread := rd.DecodeICDF(spreadICDF, 5)
	t.Logf("SPREAD=%d (0=NONE, 1=LIGHT, 2=NORMAL, 3=AGGRESSIVE), Tell after spread: %d bits", spread, rd.Tell())

	// The key question: is transient=1 compatible with spread=3?
	// In libopus, for CELT mode:
	// - If shortBlocks (transient), spread should be NORMAL (2)
	// - spread=3 (AGGRESSIVE) only happens in:
	//   a) hybrid mode with !isTransient
	//   b) CELT mode with !shortBlocks AND spreading_decision() returns 3

	if transient == 1 && spread == 3 {
		t.Log("")
		t.Log("ANOMALY: transient=1 but spread=3 (AGGRESSIVE)")
		t.Log("This shouldn't happen in standard CELT mode.")
		t.Log("Possible causes:")
		t.Log("  1. hybrid mode is active (but we set fullband bandwidth)")
		t.Log("  2. shortBlocks is 0 despite isTransient=1")
		t.Log("  3. Bug in our understanding of libopus code")
	}

	// Also decode dynalloc to continue
	bitRes := 3
	caps := celt.InitCaps(nbBands, lm, 1)
	offsets := make([]int, nbBands)
	totalBitsQ3ForDynalloc := targetBits << bitRes
	dynallocLogp := 6
	totalBoost := 0
	tellFracDynalloc := rd.TellFrac()

	for i := 0; i < nbBands; i++ {
		width := celt.ScaledBandWidth(i, 120<<lm)
		if width <= 0 {
			width = 1
		}
		innerMax := 6 << bitRes
		if width > innerMax {
			innerMax = width
		}
		quanta := width << bitRes
		if quanta > innerMax {
			quanta = innerMax
		}

		dynallocLoopLogp := dynallocLogp
		boost := 0

		for j := 0; tellFracDynalloc+(dynallocLoopLogp<<bitRes) < totalBitsQ3ForDynalloc-totalBoost && boost < caps[i]; j++ {
			flag := rd.DecodeBit(uint(dynallocLoopLogp))
			tellFracDynalloc = rd.TellFrac()
			if flag == 0 {
				break
			}
			boost += quanta
			totalBoost += quanta
			dynallocLoopLogp = 1
		}

		if boost > 0 && dynallocLogp > 2 {
			dynallocLogp--
		}
		offsets[i] = boost
	}
	t.Logf("Tell after dynalloc: %d bits", rd.Tell())

	// Decode trim
	trimICDF := []uint8{126, 124, 119, 109, 87, 41, 19, 9, 4, 2, 0}
	trim := rd.DecodeICDF(trimICDF, 7)
	t.Logf("TRIM=%d, Tell after trim: %d bits", trim, rd.Tell())

	t.Log("")
	t.Log("Summary:")
	t.Logf("  Transient: %d", transient)
	t.Logf("  Spread: %d", spread)
	t.Logf("  Trim: %d", trim)
}

// TestCompareMultipleFrames compares spread across multiple frames.
func TestCompareMultipleFrames(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000
	numFrames := 5

	// Generate audio
	totalSamples := frameSize * numFrames
	pcm32 := make([]float32, totalSamples)
	for i := range pcm32 {
		ti := float64(i) / float64(sampleRate)
		pcm32[i] = float32(0.5 * math.Sin(2*math.Pi*440*ti))
	}

	// Encode multiple frames with libopus
	libEnc, err := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("libopus encoder creation failed: %v", err)
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(false)

	spreadICDF := []uint8{25, 23, 2, 0}
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	t.Log("Frame | Transient | Spread | Trim")
	t.Log("------+-----------+--------+------")

	for frame := 0; frame < numFrames; frame++ {
		framePCM := pcm32[frame*frameSize : (frame+1)*frameSize]
		packet, _ := libEnc.EncodeFloat(framePCM, frameSize)
		payload := packet[1:]

		rd := &rangecoding.Decoder{}
		rd.Init(payload)

		// Decode header
		rd.DecodeBit(15) // silence
		rd.DecodeBit(1)  // postfilter
		transient := rd.DecodeBit(3)
		intra := rd.DecodeBit(3)

		// Decode coarse energy
		goDec := celt.NewDecoder(1)
		goDec.DecodeCoarseEnergyWithDecoder(rd, nbBands, intra == 1, lm)

		// Decode TF
		for i := 0; i < nbBands; i++ {
			rd.DecodeBit(1)
		}
		rd.DecodeBit(1)

		// Decode spread
		spread := rd.DecodeICDF(spreadICDF, 5)

		// Skip dynalloc, decode trim
		targetBits := 159 * 8
		bitRes := 3
		caps := celt.InitCaps(nbBands, lm, 1)
		totalBitsQ3ForDynalloc := targetBits << bitRes
		dynallocLogp := 6
		totalBoost := 0
		tellFracDynalloc := rd.TellFrac()

		for i := 0; i < nbBands; i++ {
			width := celt.ScaledBandWidth(i, 120<<lm)
			if width <= 0 {
				width = 1
			}
			innerMax := 6 << bitRes
			if width > innerMax {
				innerMax = width
			}
			quanta := width << bitRes
			if quanta > innerMax {
				quanta = innerMax
			}

			dynallocLoopLogp := dynallocLogp
			boost := 0

			for j := 0; tellFracDynalloc+(dynallocLoopLogp<<bitRes) < totalBitsQ3ForDynalloc-totalBoost && boost < caps[i]; j++ {
				flag := rd.DecodeBit(uint(dynallocLoopLogp))
				tellFracDynalloc = rd.TellFrac()
				if flag == 0 {
					break
				}
				boost += quanta
				totalBoost += quanta
				dynallocLoopLogp = 1
			}

			if boost > 0 && dynallocLogp > 2 {
				dynallocLogp--
			}
		}

		trimICDF := []uint8{126, 124, 119, 109, 87, 41, 19, 9, 4, 2, 0}
		trim := rd.DecodeICDF(trimICDF, 7)

		t.Logf("%5d | %9d | %6d | %4d", frame, transient, spread, trim)
	}
}
