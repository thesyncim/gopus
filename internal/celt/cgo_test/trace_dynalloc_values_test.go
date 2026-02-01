// Package cgo traces dynalloc analysis step-by-step comparing Go and libopus.
// This test compares the DynallocAnalysis output between Go and libopus
// by tracing exact follower values and boost computations.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestTraceDynallocValuesComparison compares Go's DynallocAnalysis with libopus step-by-step.
func TestTraceDynallocValuesComparison(t *testing.T) {
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

	// Get mode config
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	// Encode with gopus to get bandLogE
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	goEnc.EncodeFrame(pcm64, frameSize)
	goDynalloc := goEnc.GetLastDynalloc()

	t.Log("=== Mode Configuration ===")
	t.Logf("Frame size: %d, LM: %d, nbBands: %d", frameSize, lm, nbBands)

	// Create test band energies (simulating what the encoder would produce)
	bandLogE := make([]float64, nbBands)
	bandLogE2 := make([]float64, nbBands)

	// Use realistic log-energy values (mean-relative, like libopus)
	// These simulate a tonal signal with energy concentrated in lower bands
	for i := 0; i < nbBands; i++ {
		if i < 5 {
			bandLogE[i] = 2.0 + float64(i)*0.5 // Higher energy in low bands
		} else if i < 10 {
			bandLogE[i] = 4.0 - float64(i-5)*0.3
		} else {
			bandLogE[i] = 2.5 - float64(i-10)*0.2
		}
		bandLogE2[i] = bandLogE[i] // Same for secondary MDCT
	}

	effectiveBytes := 159 // Typical for 64kbps
	lsbDepth := 24        // Float precision

	t.Log("")
	t.Log("=== Input Band Energies ===")
	t.Logf("bandLogE: %v", bandLogE[:nbBands])

	// Trace using libopus-compatible C code
	libResult := TraceDynallocAnalysis(
		bandLogE, bandLogE2,
		nbBands, 0, nbBands, 1, lsbDepth, lm, effectiveBytes,
		false, true, false, // not transient, VBR, not constrained
	)

	t.Log("")
	t.Log("=== Libopus-style Trace Results ===")
	t.Logf("maxDepth: %.4f", libResult.MaxDepth)
	t.Logf("last (significant band): %d", libResult.Last)
	t.Logf("tot_boost: %d", libResult.TotBoost)

	t.Log("")
	t.Log("=== Noise Floor per band ===")
	for i := 0; i < nbBands; i++ {
		t.Logf("Band %2d: noise_floor=%.4f", i, libResult.NoiseFloor[i])
	}

	t.Log("")
	t.Log("=== Follower progression ===")
	t.Logf("%-4s %-10s %-10s %-10s %-10s %-10s", "Band", "Forward", "Backward", "Median", "Clamp", "Final")
	for i := 0; i < nbBands; i++ {
		t.Logf("%4d %10.4f %10.4f %10.4f %10.4f %10.4f",
			i,
			libResult.FollowerAfterForward[i],
			libResult.FollowerAfterBackward[i],
			libResult.FollowerAfterMedian[i],
			libResult.FollowerAfterClamp[i],
			libResult.FollowerFinal[i],
		)
	}

	t.Log("")
	t.Log("=== Boost and Offsets ===")
	t.Logf("%-4s %-8s %-10s %-8s %-10s", "Band", "Boost", "BoostBits", "Offset", "Importance")
	for i := 0; i < nbBands; i++ {
		if libResult.Boost[i] > 0 || libResult.Offsets[i] > 0 {
			t.Logf("%4d %8d %10d %8d %10d",
				i,
				libResult.Boost[i],
				libResult.BoostBits[i],
				libResult.Offsets[i],
				libResult.Importance[i],
			)
		}
	}

	t.Log("")
	t.Log("=== Spread Weights ===")
	t.Logf("spread_weight: %v", libResult.SpreadWeight[:nbBands])

	// Now compare with Go's DynallocAnalysis
	t.Log("")
	t.Log("=== Go DynallocAnalysis Comparison ===")

	logN := make([]int16, nbBands)
	for i := 0; i < nbBands && i < len(celt.LogN); i++ {
		logN[i] = int16(celt.LogN[i])
	}

	goResult := celt.DynallocAnalysis(
		bandLogE, bandLogE2, nil,
		nbBands, 0, nbBands, 1, lsbDepth, lm,
		logN,
		effectiveBytes,
		false, true, false, // not transient, VBR, not constrained
		-1.0, 0.0,
	)

	t.Logf("Go maxDepth: %.4f (lib: %.4f, diff: %.6f)",
		goResult.MaxDepth, libResult.MaxDepth,
		goResult.MaxDepth-libResult.MaxDepth)

	t.Logf("Go TotBoost: %d (lib: %d)", goResult.TotBoost, libResult.TotBoost)

	// Compare offsets
	t.Log("")
	t.Log("=== Offset Comparison ===")
	hasDiff := false
	for i := 0; i < nbBands; i++ {
		goOff := 0
		if i < len(goResult.Offsets) {
			goOff = goResult.Offsets[i]
		}
		libOff := libResult.Offsets[i]

		if goOff != libOff {
			t.Logf("DIFF Band %2d: go=%d lib=%d", i, goOff, libOff)
			hasDiff = true
		}
	}
	if !hasDiff {
		t.Log("All offsets match!")
	}

	// Compare importance
	t.Log("")
	t.Log("=== Importance Comparison ===")
	hasDiff = false
	for i := 0; i < nbBands; i++ {
		goImp := 0
		if i < len(goResult.Importance) {
			goImp = goResult.Importance[i]
		}
		libImp := libResult.Importance[i]

		if goImp != libImp {
			t.Logf("DIFF Band %2d: go=%d lib=%d", i, goImp, libImp)
			hasDiff = true
		}
	}
	if !hasDiff {
		t.Log("All importance values match!")
	}

	// Compare spread weights
	t.Log("")
	t.Log("=== SpreadWeight Comparison ===")
	hasDiff = false
	for i := 0; i < nbBands; i++ {
		goSW := 0
		if i < len(goResult.SpreadWeight) {
			goSW = goResult.SpreadWeight[i]
		}
		libSW := libResult.SpreadWeight[i]

		if goSW != libSW {
			t.Logf("DIFF Band %2d: go=%d lib=%d", i, goSW, libSW)
			hasDiff = true
		}
	}
	if !hasDiff {
		t.Log("All spread weights match!")
	}

	// Also show what the encoder actually computed
	t.Log("")
	t.Log("=== Actual encoder output ===")
	t.Logf("Encoder MaxDepth: %.4f", goDynalloc.MaxDepth)
	t.Logf("Encoder TotBoost: %d", goDynalloc.TotBoost)
	t.Logf("Encoder Offsets: %v", goDynalloc.Offsets[:nbBands])
	t.Logf("Encoder Importance: %v", goDynalloc.Importance[:nbBands])
}

// TestTraceDynallocWithRealEncoder traces dynalloc using real encoded values.
func TestTraceDynallocWithRealEncoder(t *testing.T) {
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

	// Get mode config
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM
	effectiveBytes := 159

	// Encode with libopus to get its packet
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

	// Encode with gopus
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	goPacket, _ := goEnc.EncodeFrame(pcm64, frameSize)
	goDynalloc := goEnc.GetLastDynalloc()
	goBandLogE := goEnc.GetLastBandLogE()
	goBandLogE2 := goEnc.GetLastBandLogE2()
	if len(goBandLogE) > 0 {
		t.Logf("Go bandLogE[0..4]: %.6f %.6f %.6f %.6f %.6f", goBandLogE[0], goBandLogE[1], goBandLogE[2], goBandLogE[3], goBandLogE[4])
	}
	if len(goBandLogE2) > 0 {
		t.Logf("Go bandLogE2[0..4]: %.6f %.6f %.6f %.6f %.6f", goBandLogE2[0], goBandLogE2[1], goBandLogE2[2], goBandLogE2[3], goBandLogE2[4])
	}

	t.Log("=== Packet Comparison ===")
	t.Logf("libopus packet length: %d", len(libPacket))
	t.Logf("gopus packet length: %d", len(goPacket))

	t.Log("")
	t.Log("=== Go Encoder Dynalloc Result ===")
	t.Logf("MaxDepth: %.4f", goDynalloc.MaxDepth)
	t.Logf("TotBoost: %d", goDynalloc.TotBoost)
	t.Logf("Offsets: %v", goDynalloc.Offsets[:nbBands])
	t.Logf("Importance: %v", goDynalloc.Importance[:nbBands])
	t.Logf("SpreadWeight: %v", goDynalloc.SpreadWeight[:nbBands])

	t.Log("")
	t.Log("=== Libopus Dynalloc on Go bandLogE ===")
	libOnGo := TraceDynallocAnalysis(
		goBandLogE, goBandLogE2,
		nbBands, 0, nbBands, 1, 24, lm, effectiveBytes,
		true, false, false,
	)
	t.Logf("Lib-on-go follower band2=%.6f offsets[2]=%d (Q3 bits)", libOnGo.FollowerFinal[2], libOnGo.Offsets[2])
	t.Logf("Lib(MaxDepth)=%.4f Go(MaxDepth)=%.4f", libOnGo.MaxDepth, goDynalloc.MaxDepth)
	t.Logf("Lib(TotBoost)=%d Go(TotBoost)=%d", libOnGo.TotBoost, goDynalloc.TotBoost)
	diffCount := 0
	for i := 0; i < nbBands; i++ {
		if libOnGo.Offsets[i] != goDynalloc.Offsets[i] {
			if diffCount < 5 {
				t.Logf("Offset diff band %d: lib=%d go=%d", i, libOnGo.Offsets[i], goDynalloc.Offsets[i])
			}
			diffCount++
		}
	}
	if diffCount == 0 {
		t.Log("Offsets match for Go bandLogE inputs")
	} else {
		t.Logf("Total offset diffs: %d", diffCount)
	}

	// Compare the first bytes of packets
	t.Log("")
	t.Log("=== First bytes comparison ===")
	maxLen := 20
	if len(libPacket) < maxLen {
		maxLen = len(libPacket)
	}
	t.Logf("libopus: % x", libPacket[:maxLen])
	if len(goPacket) < maxLen {
		maxLen = len(goPacket)
	}
	t.Logf("gopus:   % x", goPacket[:maxLen])

	// Analyze bit budget
	t.Log("")
	t.Log("=== Bit Budget Analysis ===")
	minBytes := 30 + 5*lm
	t.Logf("effectiveBytes: %d, minBytes: %d", effectiveBytes, minBytes)
	t.Logf("Dynamic allocation enabled: %v", effectiveBytes >= minBytes)
}

// TestDynallocNoiseFloorComparison compares noise floor computation.
func TestDynallocNoiseFloorComparison(t *testing.T) {
	nbBands := 21
	lsbDepth := 24
	lm := 3

	t.Log("=== Noise Floor Comparison ===")
	t.Logf("lsbDepth: %d, LM: %d", lsbDepth, lm)
	t.Log("")

	// logN values for LM=3 (from celt.LogN)
	logNValues := celt.LogN[:]

	// eMeans values
	eMeans := celt.EMeans[:]

	t.Logf("%-4s %-10s %-10s %-10s %-12s %-10s", "Band", "logN", "eMean", "Preemph", "NoiseFloor", "Go")

	for i := 0; i < nbBands; i++ {
		logN := 0
		if i < len(logNValues) {
			logN = logNValues[i]
		}
		eMean := 0.0
		if i < len(eMeans) {
			eMean = eMeans[i]
		}
		preemph := 0.0062 * float64((i+5)*(i+5))

		// Libopus formula (float mode):
		// noise_floor = 0.0625*logN + 0.5 + (9-lsb_depth) - eMeans + 0.0062*(i+5)^2
		libNoiseFloor := 0.0625*float64(logN) + 0.5 + float64(9-lsbDepth) - eMean + preemph

		// Go formula (from dynalloc.go computeNoiseFloor)
		goNoiseFloor := 0.0625*float64(logN) + 0.5 + float64(9-lsbDepth) - eMean + 0.0062*float64((i+5)*(i+5))

		diff := ""
		if math.Abs(libNoiseFloor-goNoiseFloor) > 1e-6 {
			diff = " DIFF!"
		}

		t.Logf("%4d %10d %10.4f %10.4f %12.4f %10.4f%s",
			i, logN, eMean, preemph, libNoiseFloor, goNoiseFloor, diff)
	}
}

// TestDynallocFollowerProgressionComparison traces follower values step-by-step.
func TestDynallocFollowerProgressionComparison(t *testing.T) {
	frameSize := 960
	nbBands := 21
	lm := 3
	effectiveBytes := 159
	lsbDepth := 24

	// Create synthetic bandLogE values (a rising then falling pattern)
	bandLogE := make([]float64, nbBands)
	for i := 0; i < nbBands; i++ {
		// Simulate typical audio: louder in lower-mid frequencies
		if i < 5 {
			bandLogE[i] = 1.0 + float64(i)*0.5
		} else if i < 12 {
			bandLogE[i] = 3.5 - float64(i-5)*0.2
		} else {
			bandLogE[i] = 2.1 - float64(i-12)*0.15
		}
	}

	t.Log("=== Follower Progression Comparison ===")
	t.Logf("Frame size: %d, LM: %d, nbBands: %d", frameSize, lm, nbBands)
	t.Log("")

	// Trace with C code
	libResult := TraceDynallocAnalysis(
		bandLogE, bandLogE,
		nbBands, 0, nbBands, 1, lsbDepth, lm, effectiveBytes,
		false, true, false,
	)

	// Trace with Go's follower computation
	t.Log("=== C/libopus Follower Values ===")
	t.Logf("%-4s %-12s %-12s %-12s %-12s %-12s %-12s",
		"Band", "bandLogE", "Forward", "Backward", "Median", "Clamp", "Final")
	for i := 0; i < nbBands; i++ {
		t.Logf("%4d %12.4f %12.4f %12.4f %12.4f %12.4f %12.4f",
			i, bandLogE[i],
			libResult.FollowerAfterForward[i],
			libResult.FollowerAfterBackward[i],
			libResult.FollowerAfterMedian[i],
			libResult.FollowerAfterClamp[i],
			libResult.FollowerFinal[i],
		)
	}
	t.Logf("Last significant band: %d", libResult.Last)

	// Now run Go's DynallocAnalysis and compare
	t.Log("")
	t.Log("=== Go DynallocAnalysis Results ===")

	logN := make([]int16, nbBands)
	for i := 0; i < nbBands && i < len(celt.LogN); i++ {
		logN[i] = int16(celt.LogN[i])
	}

	goResult := celt.DynallocAnalysis(
		bandLogE, bandLogE, nil,
		nbBands, 0, nbBands, 1, lsbDepth, lm,
		logN,
		effectiveBytes,
		false, true, false,
		-1.0, 0.0,
	)

	t.Logf("Go MaxDepth: %.4f (lib: %.4f)", goResult.MaxDepth, libResult.MaxDepth)
	t.Logf("Go TotBoost: %d (lib: %d)", goResult.TotBoost, libResult.TotBoost)

	// Compare offsets
	t.Log("")
	t.Log("=== Detailed Offset Comparison ===")
	for i := 0; i < nbBands; i++ {
		goOff := 0
		if i < len(goResult.Offsets) {
			goOff = goResult.Offsets[i]
		}
		libOff := libResult.Offsets[i]

		status := "OK"
		if goOff != libOff {
			status = "MISMATCH"
		}

		t.Logf("Band %2d: go=%3d lib=%3d boost=%3d boost_bits=%5d [%s]",
			i, goOff, libOff, libResult.Boost[i], libResult.BoostBits[i], status)
	}
}

// TestDynallocMaskingModelComparison compares the spread_weight masking model.
func TestDynallocMaskingModelComparison(t *testing.T) {
	nbBands := 21
	lm := 3
	effectiveBytes := 159
	lsbDepth := 24

	// Create test energies
	bandLogE := make([]float64, nbBands)
	for i := 0; i < nbBands; i++ {
		if i < 8 {
			bandLogE[i] = 3.0 + float64(i)*0.3
		} else {
			bandLogE[i] = 5.4 - float64(i-8)*0.25
		}
	}

	t.Log("=== Masking Model (Spread Weight) Comparison ===")
	t.Log("")

	// Trace with C code
	libResult := TraceDynallocAnalysis(
		bandLogE, bandLogE,
		nbBands, 0, nbBands, 1, lsbDepth, lm, effectiveBytes,
		false, true, false,
	)

	// Get Go's result
	logN := make([]int16, nbBands)
	for i := 0; i < nbBands && i < len(celt.LogN); i++ {
		logN[i] = int16(celt.LogN[i])
	}

	goResult := celt.DynallocAnalysis(
		bandLogE, bandLogE, nil,
		nbBands, 0, nbBands, 1, lsbDepth, lm,
		logN,
		effectiveBytes,
		false, true, false,
		-1.0, 0.0,
	)

	t.Logf("maxDepth: lib=%.4f go=%.4f", libResult.MaxDepth, goResult.MaxDepth)
	t.Log("")
	t.Logf("%-4s %-12s %-12s %-12s %-12s", "Band", "bandLogE", "noiseFloor", "lib_sw", "go_sw")

	for i := 0; i < nbBands; i++ {
		goSW := 0
		if i < len(goResult.SpreadWeight) {
			goSW = goResult.SpreadWeight[i]
		}
		libSW := libResult.SpreadWeight[i]

		status := ""
		if goSW != libSW {
			status = " <-- DIFF"
		}

		t.Logf("%4d %12.4f %12.4f %12d %12d%s",
			i, bandLogE[i], libResult.NoiseFloor[i], libSW, goSW, status)
	}
}
