//go:build trace
// +build trace

// Package cgo traces full encoder state to find divergence point.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTraceFullEncoderState traces encoder state after every operation
// to find where gopus and libopus diverge.
func TestTraceFullEncoderState(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm32 := make([]float32, frameSize)
	pcm64 := make([]float64, frameSize)
	for i := range pcm32 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm32[i] = float32(val)
		pcm64[i] = val
	}

	// Encode with libopus to get the reference values
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

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM
	targetBytes := 159
	targetBits := targetBytes * 8

	t.Log("=== Full Encoder State Trace ===")
	t.Log("")
	t.Logf("Frame: %d samples, LM=%d, nbBands=%d, targetBytes=%d", frameSize, lm, nbBands, targetBytes)
	t.Log("")

	// Decode libopus packet to get all values
	rd := &rangecoding.Decoder{}
	rd.Init(libPayload)

	// Header
	silence := rd.DecodeBit(15)
	postfilter := rd.DecodeBit(1)
	transient := rd.DecodeBit(3)
	intra := rd.DecodeBit(3)
	t.Logf("Header: silence=%d postfilter=%d transient=%d intra=%d", silence, postfilter, transient, intra)

	// Coarse energy
	goDec := celt.NewDecoder(1)
	_ = goDec.DecodeCoarseEnergyWithDecoder(rd, nbBands, intra == 1, lm)
	tellAfterCoarse := rd.Tell()
	t.Logf("Tell after coarse: %d bits", tellAfterCoarse)

	// TF bits
	tfRes := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		tfRes[i] = rd.DecodeBit(1)
	}
	tfSelect := rd.DecodeBit(1)
	tellAfterTF := rd.Tell()
	t.Logf("Tell after TF: %d bits, tfSelect=%d", tellAfterTF, tfSelect)

	// Spread
	spreadICDF := []uint8{25, 23, 2, 0}
	spread := rd.DecodeICDF(spreadICDF, 5)
	tellAfterSpread := rd.Tell()
	t.Logf("Spread=%d, tell after spread: %d bits", spread, tellAfterSpread)

	// Dynalloc
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
	tellAfterDynalloc := rd.Tell()
	t.Logf("Tell after dynalloc: %d bits, totalBoost=%d", tellAfterDynalloc, totalBoost)

	// Trim
	trimICDF := []uint8{126, 124, 119, 109, 87, 41, 19, 9, 4, 2, 0}
	trim := rd.DecodeICDF(trimICDF, 7)
	tellAfterTrim := rd.Tell()
	t.Logf("Trim=%d, tell after trim: %d bits", trim, tellAfterTrim)

	// Now create encoders and trace state at each step
	t.Log("")
	t.Log("=== Encoder State Comparison ===")

	// Create libopus tracer
	libTracer := NewLibopusEncoderTracer(256)
	defer libTracer.Destroy()

	// Create gopus encoder
	goBuf := make([]byte, 256)
	goEnc := &rangecoding.Encoder{}
	goEnc.Init(goBuf)
	goEnc.Shrink(uint32(targetBytes))

	compareState := func(name string, lib ECEncStateTrace, goRng, goVal uint32, goRem int, goExt, goOffs uint32, goTell int) bool {
		rngMatch := lib.Rng == goRng
		valMatch := lib.Val == goVal
		remMatch := lib.Rem == goRem
		extMatch := lib.Ext == goExt
		offsMatch := lib.Offs == goOffs

		allMatch := rngMatch && valMatch && remMatch && extMatch && offsMatch

		if !allMatch {
			t.Logf("%s: DIVERGE!", name)
			t.Logf("  lib:  rng=%08X val=%08X rem=%d ext=%d offs=%d tell=%d",
				lib.Rng, lib.Val, lib.Rem, lib.Ext, lib.Offs, lib.Tell)
			t.Logf("  go:   rng=%08X val=%08X rem=%d ext=%d offs=%d tell=%d",
				goRng, goVal, goRem, goExt, goOffs, goTell)
			if !rngMatch {
				t.Logf("  rng DIFFERS")
			}
			if !valMatch {
				t.Logf("  val DIFFERS")
			}
			if !remMatch {
				t.Logf("  rem DIFFERS")
			}
			if !extMatch {
				t.Logf("  ext DIFFERS")
			}
			if !offsMatch {
				t.Logf("  offs DIFFERS")
			}
		} else {
			t.Logf("%s: MATCH (rng=%08X tell=%d)", name, lib.Rng, lib.Tell)
		}
		return allMatch
	}

	// Initial state
	libState := libTracer.GetState()
	if !compareState("Initial", libState, goEnc.Range(), goEnc.Val(), goEnc.Rem(), goEnc.Ext(), uint32(goEnc.RangeBytes()), goEnc.Tell()) {
		t.Fatal("Initial state differs!")
	}

	// Encode header
	headerBits := []int{int(silence), int(postfilter), int(transient), int(intra)}
	headerLogps := []int{15, 1, 3, 3}
	headerNames := []string{"silence", "postfilter", "transient", "intra"}

	for i, name := range headerNames {
		_, libState = libTracer.EncodeBitLogp(headerBits[i], uint(headerLogps[i]))
		goEnc.EncodeBit(headerBits[i], uint(headerLogps[i]))
		if !compareState("After "+name, libState, goEnc.Range(), goEnc.Val(), goEnc.Rem(), goEnc.Ext(), uint32(goEnc.RangeBytes()), goEnc.Tell()) {
			t.Fatalf("State diverges after %s!", name)
		}
	}

	// Now we need to encode coarse energy
	// For simplicity, let's skip to after coarse and start from a fresh encoder
	// that we know matches at that point

	t.Log("")
	t.Log("=== Testing TF, Spread, Trim encoding ===")

	// Re-create encoders after header + coarse
	// Use the header bytes from both encoders
	headerBitsVec := []int{0, 0, 1, 0} // silence=0, postfilter=0, transient=1, intra=0
	headerLogpsVec := []int{15, 1, 3, 3}

	libTracer2 := NewLibopusEncoderTracer(256)
	defer libTracer2.Destroy()

	goBuf2 := make([]byte, 256)
	goEnc2 := &rangecoding.Encoder{}
	goEnc2.Init(goBuf2)
	goEnc2.Shrink(uint32(targetBytes))

	// Encode header
	for i := 0; i < 4; i++ {
		_, _ = libTracer2.EncodeBitLogp(headerBitsVec[i], uint(headerLogpsVec[i]))
		goEnc2.EncodeBit(headerBitsVec[i], uint(headerLogpsVec[i]))
	}

	libState2 := libTracer2.GetState()
	t.Logf("After header: lib tell=%d, go tell=%d", libState2.Tell, goEnc2.Tell())

	// Encode TF bits
	tfBits := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		tfBits[i] = tfRes[i]
	}

	for i := 0; i < nbBands; i++ {
		_, libState2 = libTracer2.EncodeBitLogp(tfBits[i], 1)
		goEnc2.EncodeBit(tfBits[i], 1)
	}
	// tf_select
	_, libState2 = libTracer2.EncodeBitLogp(tfSelect, 1)
	goEnc2.EncodeBit(tfSelect, 1)

	if !compareState("After TF", libState2, goEnc2.Range(), goEnc2.Val(), goEnc2.Rem(), goEnc2.Ext(), uint32(goEnc2.RangeBytes()), goEnc2.Tell()) {
		t.Log("TF encoding diverges!")
	}

	// Encode spread
	_, libState2 = libTracer2.EncodeICDF(spread, spreadICDF, 5)
	goEnc2.EncodeICDF(spread, spreadICDF, 5)

	if !compareState("After spread", libState2, goEnc2.Range(), goEnc2.Val(), goEnc2.Rem(), goEnc2.Ext(), uint32(goEnc2.RangeBytes()), goEnc2.Tell()) {
		t.Log("Spread encoding diverges!")
	}

	// Encode trim
	_, libState2 = libTracer2.EncodeICDF(trim, trimICDF, 7)
	goEnc2.EncodeICDF(trim, trimICDF, 7)

	if !compareState("After trim", libState2, goEnc2.Range(), goEnc2.Val(), goEnc2.Rem(), goEnc2.Ext(), uint32(goEnc2.RangeBytes()), goEnc2.Tell()) {
		t.Log("Trim encoding diverges!")
	}

	t.Log("")
	t.Log("=== PVQ Encoding Test ===")

	// Test PVQ encoding with same indices
	pvqIndices := []struct {
		vsize uint32
		idx   uint32
	}{
		{4066763520, 1108958327}, // Band 0
		{4066763520, 2878677914}, // Band 1
	}

	for i, pv := range pvqIndices {
		_, libState2 = libTracer2.EncodeUniform(pv.idx, pv.vsize)
		goEnc2.EncodeUniform(pv.idx, pv.vsize)

		if !compareState("After PVQ band "+string(rune('0'+i)), libState2,
			goEnc2.Range(), goEnc2.Val(), goEnc2.Rem(), goEnc2.Ext(),
			uint32(goEnc2.RangeBytes()), goEnc2.Tell()) {
			t.Logf("PVQ encoding diverges at band %d!", i)
		}
	}

	// Show final bytes
	goBytes2 := goEnc2.Done()
	t.Log("")
	t.Logf("First 20 gopus bytes: %02X", goBytes2[:minInt20(20, len(goBytes2))])
}

func minInt20(a, b int) int {
	if a < b {
		return a
	}
	return b
}
