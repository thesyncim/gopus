// Package cgo provides detailed header flag encoding trace tests.
// This test compares gopus vs libopus range encoder state after EACH header flag.
package cgo

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestHeaderFlagsDetailTrace traces exact range encoder state after each header flag
// for both gopus and libopus to find the FIRST point of divergence.
//
// Header encoding order for non-hybrid, non-silence, 20ms mono frame:
// 1. Silence flag (logp=15) - value=0 (not silent)
// 2. Postfilter flag (logp=1) - value=0 (no postfilter)
// 3. Transient flag (logp=3) - value=0 (no transient) for non-transient frame
// 4. Intra flag (logp=3) - value=1 (intra mode for first frame) or value=0 (inter)
//
// We trace the state (rng, val, tell) after each encoding step.
func TestHeaderFlagsDetailTrace(t *testing.T) {
	t.Log("=== Detailed Header Flags Range Encoder State Comparison ===")
	t.Log("")
	t.Log("Testing: Non-transient, non-silence, non-hybrid, 20ms mono frame")
	t.Log("Expected flags:")
	t.Log("  Silence=0 (logp=15)")
	t.Log("  Postfilter=0 (logp=1)")
	t.Log("  Transient=0 (logp=3)")
	t.Log("  Intra=1 (logp=3) for first frame")
	t.Log("")

	// Define the header flag sequence
	// These are the VALUES being encoded, in order
	bits := []int{
		0, // Silence flag = 0 (not silent)
		0, // Postfilter flag = 0 (no postfilter)
		0, // Transient flag = 0 (no transient)
		1, // Intra flag = 1 (intra mode for first frame)
	}
	logps := []int{
		15, // Silence uses logp=15
		1,  // Postfilter uses logp=1
		3,  // Transient uses logp=3
		3,  // Intra uses logp=3
	}
	flagNames := []string{
		"Silence flag",
		"Postfilter flag",
		"Transient flag",
		"Intra flag",
	}

	t.Log("=== libopus Range Encoder State Trace ===")
	libStates, libBytes := TraceBitSequence(bits, logps)
	if libStates == nil {
		t.Fatal("Failed to trace libopus encoding")
	}

	t.Logf("Initial: rng=0x%08X, val=0x%08X, rem=%d, ext=%d, offs=%d, tell=%d",
		libStates[0].Rng, libStates[0].Val, libStates[0].Rem, libStates[0].Ext,
		libStates[0].Offs, libStates[0].Tell)

	for i := 0; i < len(bits); i++ {
		t.Logf("After %s (val=%d, logp=%d):", flagNames[i], bits[i], logps[i])
		t.Logf("  rng=0x%08X, val=0x%08X, rem=%d, ext=%d, offs=%d, tell=%d",
			libStates[i+1].Rng, libStates[i+1].Val, libStates[i+1].Rem,
			libStates[i+1].Ext, libStates[i+1].Offs, libStates[i+1].Tell)
	}

	t.Log("")
	t.Log("=== gopus Range Encoder State Trace ===")

	// Replicate encoding with gopus
	bufSize := 256
	buf := make([]byte, bufSize)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	type gopusState struct {
		rng  uint32
		val  uint32
		rem  int
		ext  uint32
		tell int
	}

	goStates := make([]gopusState, len(bits)+1)

	// Record initial state
	goStates[0] = gopusState{
		rng:  re.Range(),
		val:  re.Val(),
		rem:  re.Rem(),
		ext:  re.Ext(),
		tell: re.Tell(),
	}

	t.Logf("Initial: rng=0x%08X, val=0x%08X, rem=%d, ext=%d, tell=%d",
		goStates[0].rng, goStates[0].val, goStates[0].rem, goStates[0].ext, goStates[0].tell)

	// Encode each flag and record state
	for i := 0; i < len(bits); i++ {
		re.EncodeBit(bits[i], uint(logps[i]))
		goStates[i+1] = gopusState{
			rng:  re.Range(),
			val:  re.Val(),
			rem:  re.Rem(),
			ext:  re.Ext(),
			tell: re.Tell(),
		}
		t.Logf("After %s (val=%d, logp=%d):", flagNames[i], bits[i], logps[i])
		t.Logf("  rng=0x%08X, val=0x%08X, rem=%d, ext=%d, tell=%d",
			goStates[i+1].rng, goStates[i+1].val, goStates[i+1].rem,
			goStates[i+1].ext, goStates[i+1].tell)
	}

	goBytes := re.Done()

	t.Log("")
	t.Log("=== Comparison ===")
	t.Log("")
	t.Logf("%-20s | %-12s | %-12s | %-8s | %-8s | %-8s",
		"Step", "gopus rng", "libopus rng", "go tell", "lib tell", "Match?")
	t.Log("--------------------+---------------+---------------+----------+----------+---------")

	firstDivergence := -1
	for i := 0; i <= len(bits); i++ {
		stepName := "Initial"
		if i > 0 {
			stepName = flagNames[i-1]
		}

		rngMatch := goStates[i].rng == libStates[i].Rng
		tellMatch := goStates[i].tell == libStates[i].Tell
		allMatch := rngMatch && tellMatch

		matchStr := "YES"
		if !allMatch {
			matchStr = "NO <---"
			if firstDivergence < 0 {
				firstDivergence = i
			}
		}

		t.Logf("%-20s | 0x%08X   | 0x%08X   | %8d | %8d | %s",
			stepName, goStates[i].rng, libStates[i].Rng,
			goStates[i].tell, libStates[i].Tell, matchStr)
	}

	t.Log("")
	t.Log("=== Output Bytes Comparison ===")
	t.Logf("gopus bytes (%d): %v", len(goBytes), goBytes)
	t.Logf("libopus bytes (%d): %v", len(libBytes), libBytes)

	if firstDivergence >= 0 {
		t.Logf("")
		t.Logf("RESULT: First divergence at step %d", firstDivergence)
		if firstDivergence > 0 {
			t.Logf("  After encoding: %s (val=%d, logp=%d)",
				flagNames[firstDivergence-1], bits[firstDivergence-1], logps[firstDivergence-1])
		} else {
			t.Logf("  At initial state (before any encoding)")
		}
		t.Errorf("Range encoder state diverges!")
	} else {
		t.Log("")
		t.Log("RESULT: All header flag states match between gopus and libopus")
	}
}

// TestHeaderFlagsWithIntraZero tests with intra=0 (inter-frame mode)
func TestHeaderFlagsWithIntraZero(t *testing.T) {
	t.Log("=== Header Flags Trace with Intra=0 (inter-frame mode) ===")
	t.Log("")

	// Same as above but with intra=0
	bits := []int{0, 0, 0, 0} // Silence=0, Postfilter=0, Transient=0, Intra=0
	logps := []int{15, 1, 3, 3}

	// Trace libopus
	libStates, libBytes := TraceBitSequence(bits, logps)
	if libStates == nil {
		t.Fatal("Failed to trace libopus encoding")
	}

	// Trace gopus
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	for i := 0; i < len(bits); i++ {
		re.EncodeBit(bits[i], uint(logps[i]))
	}
	goBytes := re.Done()

	// Compare final state
	finalGo := struct {
		rng  uint32
		val  uint32
		tell int
	}{re.Range(), re.Val(), re.Tell()}

	finalLib := libStates[len(bits)]

	t.Logf("Final gopus:   rng=0x%08X, tell=%d", finalGo.rng, finalGo.tell)
	t.Logf("Final libopus: rng=0x%08X, tell=%d", finalLib.Rng, finalLib.Tell)
	t.Logf("gopus bytes:   %v", goBytes)
	t.Logf("libopus bytes: %v", libBytes)

	// Note: After Done(), the gopus rng may differ from pre-Done state
	// The meaningful comparison is the output bytes
	if len(goBytes) != len(libBytes) {
		t.Errorf("Byte length mismatch: gopus=%d, libopus=%d", len(goBytes), len(libBytes))
	}
	for i := 0; i < len(goBytes) && i < len(libBytes); i++ {
		if goBytes[i] != libBytes[i] {
			t.Errorf("Byte %d differs: gopus=0x%02X, libopus=0x%02X", i, goBytes[i], libBytes[i])
		}
	}
}

// TestHeaderFlagsWithTransient tests with transient=1
func TestHeaderFlagsWithTransient(t *testing.T) {
	t.Log("=== Header Flags Trace with Transient=1 ===")
	t.Log("")

	// Transient frame
	bits := []int{0, 0, 1, 1} // Silence=0, Postfilter=0, Transient=1, Intra=1
	logps := []int{15, 1, 3, 3}

	// Trace libopus
	libStates, libBytes := TraceBitSequence(bits, logps)
	if libStates == nil {
		t.Fatal("Failed to trace libopus encoding")
	}

	// Trace gopus
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	for i := 0; i < len(bits); i++ {
		re.EncodeBit(bits[i], uint(logps[i]))
	}
	goBytes := re.Done()

	t.Logf("gopus bytes:   %v", goBytes)
	t.Logf("libopus bytes: %v", libBytes)

	// Detailed state comparison
	t.Log("")
	t.Log("State after each flag:")
	flagNames := []string{"Silence", "Postfilter", "Transient", "Intra"}
	allMatch := true
	for i := 0; i <= len(bits); i++ {
		// Recompute gopus state at this point
		buf2 := make([]byte, 256)
		re2 := &rangecoding.Encoder{}
		re2.Init(buf2)
		for j := 0; j < i; j++ {
			re2.EncodeBit(bits[j], uint(logps[j]))
		}
		stepName := "Initial"
		if i > 0 {
			stepName = fmt.Sprintf("After %s=%d", flagNames[i-1], bits[i-1])
		}

		goRng := re2.Range()
		libRng := libStates[i].Rng
		match := "YES"
		if goRng != libRng {
			match = "NO"
			allMatch = false
		}
		t.Logf("%-25s: gopus rng=0x%08X, lib rng=0x%08X [%s]", stepName, goRng, libRng, match)
	}

	if !allMatch {
		t.Errorf("Range values diverge!")
	}
}

// TestCompareActualEncoderOutput compares actual gopus encoder output with libopus
// for the same input signal to see if header flags are encoded identically.
func TestCompareActualEncoderOutput(t *testing.T) {
	t.Log("=== Compare Actual Encoder Outputs ===")
	t.Log("")
	t.Log("Encoding identical input with both encoders and comparing bitstreams")
	t.Log("")

	frameSize := 960
	channels := 1
	sampleRate := 48000
	bitrate := 64000

	// Generate a simple 440Hz sine wave
	samples := make([]float32, frameSize)
	amplitude := float32(0.5)
	for i := range samples {
		samples[i] = amplitude * float32(sinApprox(2*3.14159265*440.0*float64(i)/float64(sampleRate)))
	}

	// Encode with libopus
	libEnc, err := NewLibopusEncoder(sampleRate, channels, 2049) // OPUS_APPLICATION_AUDIO
	if err != nil || libEnc == nil {
		t.Fatalf("Failed to create libopus encoder: %v", err)
	}
	defer libEnc.Destroy()

	// Configure libopus to match gopus settings
	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10) // High complexity
	libEnc.SetVBR(true)
	libEnc.SetBandwidth(1105) // OPUS_BANDWIDTH_FULLBAND
	libEnc.SetSignal(3002)    // OPUS_SIGNAL_MUSIC

	// Reset to ensure clean state
	libEnc.Reset()

	// Encode first frame with libopus
	libBytes, libLen := libEnc.EncodeFloat(samples, frameSize)
	if libLen <= 0 {
		t.Fatalf("libopus encode failed: %d", libLen)
	}
	libRng := libEnc.GetFinalRange()

	t.Logf("libopus encoded: %d bytes, final_range=0x%08X", libLen, libRng)
	t.Logf("libopus first 20 bytes: %v", libBytes[:minInt20(20, libLen)])

	// The TOC byte tells us what mode libopus used
	if libLen > 0 {
		toc := libBytes[0]
		config := (toc >> 3) & 0x1F
		stereo := (toc >> 2) & 0x01
		frameCount := toc & 0x03
		t.Logf("libopus TOC: config=%d, stereo=%d, frameCount=%d", config, stereo, frameCount)

		// Config >= 28 means CELT-only mode
		if config >= 28 {
			t.Log("libopus is using CELT-only mode")
		} else if config >= 16 {
			t.Log("libopus is using Hybrid mode")
		} else {
			t.Log("libopus is using SILK-only mode")
		}
	}

	// Note: We can't easily compare with gopus here because:
	// 1. gopus CELT encoder doesn't produce TOC byte
	// 2. libopus may use different mode (SILK, Hybrid, or CELT)
	// 3. The encoder parameters need exact matching

	t.Log("")
	t.Log("To properly compare, we need to ensure both encoders:")
	t.Log("  - Use CELT-only mode (config >= 28)")
	t.Log("  - Have identical signal analysis results")
	t.Log("  - Use same bit budget")
}

// sinApprox is a simple sine approximation for test signal generation
func sinApprox(x float64) float64 {
	// Normalize to [0, 2pi)
	for x < 0 {
		x += 2 * 3.14159265
	}
	for x >= 2*3.14159265 {
		x -= 2 * 3.14159265
	}
	// Simple Taylor approximation
	if x > 3.14159265 {
		x -= 3.14159265
		return -sinTaylor(x)
	}
	return sinTaylor(x)
}

func sinTaylor(x float64) float64 {
	x3 := x * x * x
	x5 := x3 * x * x
	x7 := x5 * x * x
	return x - x3/6 + x5/120 - x7/5040
}

func minInt20(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestHeaderPlusFirstQiTrace traces range encoder state through header flags AND first qi value
// This is the key test to find where divergence happens between gopus and libopus
func TestHeaderPlusFirstQiTrace(t *testing.T) {
	t.Log("=== Header Flags + First Coarse Energy QI Trace ===")
	t.Log("")
	t.Log("This test traces exact encoder state through the complete sequence:")
	t.Log("  1. Silence flag (logp=15, val=0)")
	t.Log("  2. Postfilter flag (logp=1, val=0)")
	t.Log("  3. Transient flag (logp=3, val=0)")
	t.Log("  4. Intra flag (logp=3, val=1)")
	t.Log("  5. First coarse energy qi (Laplace encoded)")
	t.Log("")

	// Step 1: Encode header flags with libopus
	headerBits := []int{0, 0, 0, 1} // Silence, Postfilter, Transient, Intra
	headerLogps := []int{15, 1, 3, 3}

	libStates, _ := TraceBitSequence(headerBits, headerLogps)
	if libStates == nil {
		t.Fatal("Failed to trace libopus header encoding")
	}

	t.Log("=== libopus Header States ===")
	flagNames := []string{"Initial", "After silence", "After postfilter", "After transient", "After intra"}
	for i, name := range flagNames {
		t.Logf("%s: rng=0x%08X, val=0x%08X, tell=%d",
			name, libStates[i].Rng, libStates[i].Val, libStates[i].Tell)
	}

	// Step 2: Continue with first qi Laplace encoding
	// Use typical band 0 parameters: fs=72<<7, decay=127<<6 (from e_prob_model LM=3, intra)
	qi := 1 // Typical first qi value
	fs := 72 << 7
	decay := 127 << 6

	t.Log("")
	t.Logf("=== First QI Encoding (qi=%d, fs=%d, decay=%d) ===", qi, fs, decay)

	// Encode with libopus: header flags + qi
	libQiBytes, libQiVal, err := EncodeLaplace(qi, fs, decay)
	if err != nil {
		t.Fatalf("libopus Laplace encode failed: %v", err)
	}

	t.Logf("libopus: encoded qi=%d, output bytes=%v", libQiVal, libQiBytes)

	// Now trace gopus through the same sequence
	t.Log("")
	t.Log("=== gopus Complete Trace ===")

	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	type stateSnapshot struct {
		rng  uint32
		val  uint32
		tell int
	}

	// Record initial state
	goStates := []stateSnapshot{}
	goStates = append(goStates, stateSnapshot{re.Range(), re.Val(), re.Tell()})
	t.Logf("Initial: rng=0x%08X, val=0x%08X, tell=%d", re.Range(), re.Val(), re.Tell())

	// Encode header flags one by one
	for i := 0; i < len(headerBits); i++ {
		re.EncodeBit(headerBits[i], uint(headerLogps[i]))
		goStates = append(goStates, stateSnapshot{re.Range(), re.Val(), re.Tell()})
		t.Logf("%s: rng=0x%08X, val=0x%08X, tell=%d",
			flagNames[i+1], re.Range(), re.Val(), re.Tell())
	}

	// Now encode qi with gopus Laplace
	celtEnc := celt.NewEncoder(1)
	celtEnc.SetRangeEncoder(re)
	goQiVal := celtEnc.TestEncodeLaplace(qi, fs, decay)

	goStates = append(goStates, stateSnapshot{re.Range(), re.Val(), re.Tell()})
	t.Logf("After qi encode: rng=0x%08X, val=0x%08X, tell=%d", re.Range(), re.Val(), re.Tell())
	t.Logf("gopus: encoded qi=%d", goQiVal)

	goBytes := re.Done()
	t.Logf("gopus output bytes: %v", goBytes)

	// Comparison
	t.Log("")
	t.Log("=== State Comparison ===")
	t.Logf("%-20s | %-12s | %-12s | %-8s | %-8s | %s",
		"Step", "gopus rng", "libopus rng", "go tell", "lib tell", "Match?")
	t.Log("--------------------+---------------+---------------+----------+----------+---------")

	diverged := false
	for i := 0; i < len(flagNames) && i < len(goStates); i++ {
		goState := goStates[i]
		libState := libStates[i]

		match := "YES"
		if goState.rng != libState.Rng || goState.tell != libState.Tell {
			match = "NO <---"
			if !diverged {
				diverged = true
				t.Logf("")
				t.Logf("*** FIRST DIVERGENCE at %s ***", flagNames[i])
			}
		}

		t.Logf("%-20s | 0x%08X   | 0x%08X   | %8d | %8d | %s",
			flagNames[i], goState.rng, libState.Rng, goState.tell, libState.Tell, match)
	}

	t.Log("")
	t.Logf("=== QI Value Comparison ===")
	if goQiVal != libQiVal {
		t.Errorf("QI value mismatch: gopus=%d, libopus=%d", goQiVal, libQiVal)
	} else {
		t.Logf("QI values match: %d", goQiVal)
	}

	if !diverged {
		t.Log("")
		t.Log("RESULT: All header states match. Divergence (if any) is in qi encoding.")
	}
}

// TestTraceHeaderPlusIntraFlag traces header AND intra flag encoding combined
// to verify that the complete sequence up to coarse energy is correct.
func TestTraceHeaderPlusIntraFlag(t *testing.T) {
	t.Log("=== Header + Intra Flag Combined Trace ===")
	t.Log("")
	t.Log("Testing the exact sequence that starts a CELT frame.")
	t.Log("")

	// The complete header sequence for a 20ms mono frame:
	// 1. Silence=0 (logp=15)
	// 2. Postfilter=0 (logp=1)
	// 3. Transient=0 (logp=3)
	// 4. Intra=1 (logp=3)
	// Then coarse energy encoding begins with first qi

	bits := []int{0, 0, 0, 1}
	logps := []int{15, 1, 3, 3}

	// Get libopus states and output
	libStates, libBytes := TraceBitSequence(bits, logps)
	if libStates == nil {
		t.Fatal("Failed to get libopus trace")
	}

	// Get gopus states
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	type state struct {
		rng  uint32
		val  uint32
		tell int
	}
	goStates := []state{}
	goStates = append(goStates, state{re.Range(), re.Val(), re.Tell()})

	for i := range bits {
		re.EncodeBit(bits[i], uint(logps[i]))
		goStates = append(goStates, state{re.Range(), re.Val(), re.Tell()})
	}

	// Compare
	t.Log("State comparison:")
	t.Logf("%-15s | %-12s | %-12s | %-12s | %-12s",
		"Step", "gopus rng", "libopus rng", "gopus val", "libopus val")
	t.Log("----------------+---------------+---------------+---------------+---------------")

	names := []string{"Initial", "Silence", "Postfilter", "Transient", "Intra"}
	allMatch := true
	for i := range names {
		rngMatch := goStates[i].rng == libStates[i].Rng
		valMatch := goStates[i].val == libStates[i].Val
		status := ""
		if !rngMatch || !valMatch {
			status = " <-- MISMATCH"
			allMatch = false
		}
		t.Logf("%-15s | 0x%08X   | 0x%08X   | 0x%08X   | 0x%08X %s",
			names[i], goStates[i].rng, libStates[i].Rng,
			goStates[i].val, libStates[i].Val, status)
	}

	// Get output bytes
	goBytes := re.Done()

	t.Log("")
	t.Logf("gopus output:   %v (len=%d)", goBytes, len(goBytes))
	t.Logf("libopus output: %v (len=%d)", libBytes, len(libBytes))

	// Bit-level comparison
	if len(goBytes) > 0 && len(libBytes) > 0 {
		t.Log("")
		t.Log("Byte comparison:")
		for i := 0; i < len(goBytes) && i < len(libBytes); i++ {
			match := ""
			if goBytes[i] != libBytes[i] {
				match = " <-- DIFFER"
			}
			t.Logf("  Byte %d: gopus=0x%02X (%08b), libopus=0x%02X (%08b)%s",
				i, goBytes[i], goBytes[i], libBytes[i], libBytes[i], match)
		}
	}

	if allMatch && string(goBytes) == string(libBytes) {
		t.Log("")
		t.Log("SUCCESS: Header encoding matches exactly")
	} else if allMatch {
		t.Log("")
		t.Log("States match but output bytes differ (likely normalization timing)")
	}
}

// TestTraceMultipleQiValues traces encoding of multiple qi values (full coarse energy)
func TestTraceMultipleQiValues(t *testing.T) {
	t.Log("=== Trace Multiple Coarse Energy QI Values ===")
	t.Log("")

	// Get probability model for LM=3, intra mode
	probModel := celt.GetEProbModel()
	lm := 3

	// Test qi sequence: typical first-frame values
	qiValues := []int{1, 0, -1, 2, 0, -1, 0, 1, 0, 0}
	nbQi := len(qiValues)

	// Compute fs and decay for each band
	fsArr := make([]int, nbQi)
	decayArr := make([]int, nbQi)
	for i := 0; i < nbQi; i++ {
		pi := 2 * i
		if pi > 40 {
			pi = 40
		}
		fsArr[i] = int(probModel[lm][1][pi]) << 7 // [1] for intra mode
		decayArr[i] = int(probModel[lm][1][pi+1]) << 6
	}

	// Encode with libopus
	libBytes, libVals, err := EncodeLaplaceSequence(qiValues, fsArr, decayArr)
	if err != nil {
		t.Fatalf("libopus encode failed: %v", err)
	}

	// Encode with gopus (standalone, no header flags)
	goBuf := make([]byte, 256)
	goEnc := &rangecoding.Encoder{}
	goEnc.Init(goBuf)

	celtEnc := celt.NewEncoder(1)
	celtEnc.SetRangeEncoder(goEnc)

	goVals := make([]int, nbQi)
	for i := 0; i < nbQi; i++ {
		goVals[i] = celtEnc.TestEncodeLaplace(qiValues[i], fsArr[i], decayArr[i])
	}

	goBytes := goEnc.Done()

	// Compare
	t.Log("QI Encoding Comparison:")
	t.Logf("%-6s | %-10s | %-8s | %-8s | %-8s | %s",
		"Band", "Input", "fs", "go_qi", "lib_qi", "Match")
	t.Log("-------+------------+----------+----------+----------+------")

	allMatch := true
	for i := 0; i < nbQi; i++ {
		match := "YES"
		if goVals[i] != libVals[i] {
			match = "NO"
			allMatch = false
		}
		t.Logf("%6d | %10d | %8d | %8d | %8d | %s",
			i, qiValues[i], fsArr[i], goVals[i], libVals[i], match)
	}

	t.Log("")
	t.Logf("gopus bytes:   %v (len=%d)", goBytes, len(goBytes))
	t.Logf("libopus bytes: %v (len=%d)", libBytes, len(libBytes))

	if !allMatch {
		t.Error("QI values differ between gopus and libopus")
	}

	if string(goBytes) != string(libBytes) {
		t.Errorf("Output bytes differ")
	} else {
		t.Log("Output bytes MATCH")
	}
}
