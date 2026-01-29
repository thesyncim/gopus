// Package cgo provides header + Laplace encoding trace tests for finding divergence.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestHeaderPlusLaplaceTrace compares header + Laplace encoding between gopus and libopus.
// This test traces the exact point where encoding diverges.
func TestHeaderPlusLaplaceTrace(t *testing.T) {
	t.Log("=== Header + Laplace Encoding Trace ===")
	t.Log("")
	t.Log("This test encodes header flags followed by Laplace values")
	t.Log("and compares state/bytes at each step between gopus and libopus.")
	t.Log("")

	// Header bits: silence=0 (logp=15), postfilter=0 (logp=1), transient=0 (logp=3), intra=1 (logp=3)
	headerBits := []int{0, 0, 0, 1}
	headerLogps := []int{15, 1, 3, 3}

	// Get probability model from gopus for LM=3 (20ms), intra mode
	probModel := celt.GetEProbModel()
	lm := 3
	prob := probModel[lm][1] // intra mode

	// Test with first 5 bands' qi values (simple test: qi=1 for each)
	nbBands := 5
	laplaceVals := make([]int, nbBands)
	laplaceFS := make([]int, nbBands)
	laplaceDecay := make([]int, nbBands)

	for band := 0; band < nbBands; band++ {
		laplaceVals[band] = 1 // All qi = 1
		pi := 2 * band
		if pi > 40 {
			pi = 40
		}
		laplaceFS[band] = int(prob[pi]) << 7
		laplaceDecay[band] = int(prob[pi+1]) << 6
	}

	t.Logf("Header: silence=%d, postfilter=%d, transient=%d, intra=%d",
		headerBits[0], headerBits[1], headerBits[2], headerBits[3])
	t.Logf("Laplace values: %v", laplaceVals)
	t.Log("")

	// === GOPUS ENCODING ===
	t.Log("=== GOPUS Encoding ===")
	buf := make([]byte, 4096)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	goStates := make([]RangeEncoderStateSnapshot, 5+nbBands)

	// Record initial state
	goStates[0] = RangeEncoderStateSnapshot{
		Rng:  re.Range(),
		Val:  re.Val(),
		Tell: re.Tell(),
	}
	t.Logf("Initial: rng=0x%08X, val=0x%08X, tell=%d", goStates[0].Rng, goStates[0].Val, goStates[0].Tell)

	// Encode header bits
	headerNames := []string{"silence", "postfilter", "transient", "intra"}
	for i := 0; i < 4; i++ {
		re.EncodeBit(headerBits[i], uint(headerLogps[i]))
		goStates[i+1] = RangeEncoderStateSnapshot{
			Rng:  re.Range(),
			Val:  re.Val(),
			Tell: re.Tell(),
		}
		t.Logf("After %s=%d: rng=0x%08X, val=0x%08X, tell=%d",
			headerNames[i], headerBits[i], goStates[i+1].Rng, goStates[i+1].Val, goStates[i+1].Tell)
	}

	// Encode Laplace values
	enc := celt.NewEncoder(1)
	enc.SetRangeEncoder(re)
	enc.SetFrameBitsForTest(len(buf) * 8)

	goLaplaceVals := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		goLaplaceVals[i] = enc.TestEncodeLaplace(laplaceVals[i], laplaceFS[i], laplaceDecay[i])
		goStates[5+i] = RangeEncoderStateSnapshot{
			Rng:  re.Range(),
			Val:  re.Val(),
			Tell: re.Tell(),
		}
		t.Logf("After Laplace[%d]=%d (out=%d): rng=0x%08X, val=0x%08X, tell=%d",
			i, laplaceVals[i], goLaplaceVals[i], goStates[5+i].Rng, goStates[5+i].Val, goStates[5+i].Tell)
	}

	goBytes := re.Done()
	t.Logf("Gopus output: %d bytes", len(goBytes))
	if len(goBytes) > 20 {
		t.Logf("Gopus bytes: %X", goBytes[:20])
	} else {
		t.Logf("Gopus bytes: %X", goBytes)
	}
	t.Log("")

	// === LIBOPUS ENCODING ===
	t.Log("=== LIBOPUS Encoding ===")
	libStates, libLaplaceVals, libBytes := TraceHeaderPlusLaplace(headerBits, headerLogps, laplaceVals, laplaceFS, laplaceDecay)

	if libStates == nil {
		t.Fatal("libopus TraceHeaderPlusLaplace failed")
	}

	// Print libopus states
	t.Logf("Initial: rng=0x%08X, val=0x%08X, tell=%d", libStates[0].Rng, libStates[0].Val, libStates[0].Tell)
	for i := 0; i < 4; i++ {
		t.Logf("After %s=%d: rng=0x%08X, val=0x%08X, tell=%d",
			headerNames[i], headerBits[i], libStates[i+1].Rng, libStates[i+1].Val, libStates[i+1].Tell)
	}
	for i := 0; i < nbBands; i++ {
		t.Logf("After Laplace[%d]=%d (out=%d): rng=0x%08X, val=0x%08X, tell=%d",
			i, laplaceVals[i], libLaplaceVals[i], libStates[5+i].Rng, libStates[5+i].Val, libStates[5+i].Tell)
	}
	t.Logf("Libopus output: %d bytes", len(libBytes))
	if len(libBytes) > 20 {
		t.Logf("Libopus bytes: %X", libBytes[:20])
	} else {
		t.Logf("Libopus bytes: %X", libBytes)
	}
	t.Log("")

	// === COMPARISON ===
	t.Log("=== State Comparison ===")
	t.Log("Point           | gopus_rng    | lib_rng      | gopus_val    | lib_val      | gopus_tell | lib_tell | Match")
	t.Log("----------------|--------------|--------------|--------------|--------------|------------|----------|------")

	allMatch := true
	names := append([]string{"initial"}, headerNames...)
	for i := 0; i < nbBands; i++ {
		names = append(names, "laplace_"+string('0'+byte(i)))
	}

	for i := 0; i < len(goStates) && i < len(libStates); i++ {
		goS := goStates[i]
		libS := libStates[i]

		match := "YES"
		if goS.Rng != libS.Rng || goS.Val != libS.Val || goS.Tell != libS.Tell {
			match = "NO"
			allMatch = false
		}

		name := names[i]
		if len(name) > 14 {
			name = name[:14]
		}
		t.Logf("%-15s | 0x%08X   | 0x%08X   | 0x%08X   | 0x%08X   | %10d | %8d | %s",
			name, goS.Rng, libS.Rng, goS.Val, libS.Val, goS.Tell, libS.Tell, match)
	}

	t.Log("")

	// Compare Laplace output values
	t.Log("Laplace Output Value Comparison:")
	laplaceMatch := true
	for i := 0; i < nbBands; i++ {
		match := "YES"
		if goLaplaceVals[i] != libLaplaceVals[i] {
			match = "NO"
			laplaceMatch = false
		}
		t.Logf("  Band %d: gopus=%d, libopus=%d [%s]", i, goLaplaceVals[i], libLaplaceVals[i], match)
	}
	t.Log("")

	// Compare output bytes
	t.Log("=== Byte Comparison ===")
	minLen := len(goBytes)
	if len(libBytes) < minLen {
		minLen = len(libBytes)
	}
	showLen := 20
	if showLen > minLen {
		showLen = minLen
	}

	bytesMatch := true
	for i := 0; i < showLen; i++ {
		match := ""
		if goBytes[i] != libBytes[i] {
			match = " DIFFER"
			bytesMatch = false
		}
		t.Logf("Byte %2d: gopus=0x%02X, libopus=0x%02X%s", i, goBytes[i], libBytes[i], match)
	}

	if len(goBytes) != len(libBytes) {
		t.Logf("Length mismatch: gopus=%d, libopus=%d", len(goBytes), len(libBytes))
		bytesMatch = false
	}

	t.Log("")
	if allMatch && laplaceMatch && bytesMatch {
		t.Log("SUCCESS: All states, values, and bytes MATCH!")
	} else {
		if !allMatch {
			t.Log("MISMATCH: Range encoder states differ")
		}
		if !laplaceMatch {
			t.Log("MISMATCH: Laplace output values differ")
		}
		if !bytesMatch {
			t.Log("MISMATCH: Output bytes differ")
		}
	}
}

// TestHeaderPlusFirstLaplaceOnly tests just header + single Laplace encoding.
func TestHeaderPlusFirstLaplaceOnly(t *testing.T) {
	t.Log("=== Header + Single Laplace Encoding ===")
	t.Log("")

	headerBits := []int{0, 0, 0, 1}
	headerLogps := []int{15, 1, 3, 3}

	// First band only
	probModel := celt.GetEProbModel()
	lm := 3
	prob := probModel[lm][1]

	qi := 1
	fs := int(prob[0]) << 7    // fs0 for band 0
	decay := int(prob[1]) << 6 // decay for band 0

	t.Logf("Encoding: qi=%d, fs=%d, decay=%d", qi, fs, decay)
	t.Log("")

	// GOPUS
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	t.Log("=== GOPUS ===")
	t.Logf("Initial: rng=0x%08X, val=0x%08X, tell=%d", re.Range(), re.Val(), re.Tell())

	re.EncodeBit(0, 15) // silence
	t.Logf("After silence=0: rng=0x%08X, val=0x%08X, tell=%d", re.Range(), re.Val(), re.Tell())

	re.EncodeBit(0, 1) // postfilter
	t.Logf("After postfilter=0: rng=0x%08X, val=0x%08X, tell=%d", re.Range(), re.Val(), re.Tell())

	re.EncodeBit(0, 3) // transient
	t.Logf("After transient=0: rng=0x%08X, val=0x%08X, tell=%d", re.Range(), re.Val(), re.Tell())

	re.EncodeBit(1, 3) // intra
	goStateAfterHeader := RangeEncoderStateSnapshot{Rng: re.Range(), Val: re.Val(), Tell: re.Tell()}
	t.Logf("After intra=1: rng=0x%08X, val=0x%08X, tell=%d", goStateAfterHeader.Rng, goStateAfterHeader.Val, goStateAfterHeader.Tell)

	// Now encode Laplace
	enc := celt.NewEncoder(1)
	enc.SetRangeEncoder(re)
	enc.SetFrameBitsForTest(len(buf) * 8)
	goQi := enc.TestEncodeLaplace(qi, fs, decay)
	goStateAfterLaplace := RangeEncoderStateSnapshot{Rng: re.Range(), Val: re.Val(), Tell: re.Tell()}
	t.Logf("After Laplace(%d): rng=0x%08X, val=0x%08X, tell=%d (out=%d)",
		qi, goStateAfterLaplace.Rng, goStateAfterLaplace.Val, goStateAfterLaplace.Tell, goQi)

	goBytes := re.Done()
	t.Logf("Gopus bytes: %X (%d bytes)", goBytes, len(goBytes))
	t.Log("")

	// LIBOPUS
	t.Log("=== LIBOPUS ===")
	libStates, libQis, libBytes := TraceHeaderPlusLaplace(headerBits, headerLogps, []int{qi}, []int{fs}, []int{decay})
	if libStates == nil {
		t.Fatal("libopus failed")
	}

	t.Logf("Initial: rng=0x%08X, val=0x%08X, tell=%d", libStates[0].Rng, libStates[0].Val, libStates[0].Tell)
	t.Logf("After silence=0: rng=0x%08X, val=0x%08X, tell=%d", libStates[1].Rng, libStates[1].Val, libStates[1].Tell)
	t.Logf("After postfilter=0: rng=0x%08X, val=0x%08X, tell=%d", libStates[2].Rng, libStates[2].Val, libStates[2].Tell)
	t.Logf("After transient=0: rng=0x%08X, val=0x%08X, tell=%d", libStates[3].Rng, libStates[3].Val, libStates[3].Tell)
	t.Logf("After intra=1: rng=0x%08X, val=0x%08X, tell=%d", libStates[4].Rng, libStates[4].Val, libStates[4].Tell)
	t.Logf("After Laplace(%d): rng=0x%08X, val=0x%08X, tell=%d (out=%d)",
		qi, libStates[5].Rng, libStates[5].Val, libStates[5].Tell, libQis[0])
	t.Logf("Libopus bytes: %X (%d bytes)", libBytes, len(libBytes))
	t.Log("")

	// Compare
	t.Log("=== COMPARISON ===")
	headerMatch := goStateAfterHeader.Rng == libStates[4].Rng &&
		goStateAfterHeader.Val == libStates[4].Val &&
		goStateAfterHeader.Tell == libStates[4].Tell

	laplaceMatch := goStateAfterLaplace.Rng == libStates[5].Rng &&
		goStateAfterLaplace.Val == libStates[5].Val &&
		goStateAfterLaplace.Tell == libStates[5].Tell

	t.Logf("After header: gopus=(rng=0x%08X, val=0x%08X, tell=%d) vs libopus=(rng=0x%08X, val=0x%08X, tell=%d) [%v]",
		goStateAfterHeader.Rng, goStateAfterHeader.Val, goStateAfterHeader.Tell,
		libStates[4].Rng, libStates[4].Val, libStates[4].Tell, headerMatch)

	t.Logf("After Laplace: gopus=(rng=0x%08X, val=0x%08X, tell=%d) vs libopus=(rng=0x%08X, val=0x%08X, tell=%d) [%v]",
		goStateAfterLaplace.Rng, goStateAfterLaplace.Val, goStateAfterLaplace.Tell,
		libStates[5].Rng, libStates[5].Val, libStates[5].Tell, laplaceMatch)

	t.Log("")
	if headerMatch {
		t.Log("Header encoding MATCHES")
	} else {
		t.Log("Header encoding DIFFERS - this is the divergence point!")
	}

	if laplaceMatch {
		t.Log("Laplace encoding MATCHES (after header)")
	} else if headerMatch {
		t.Log("Laplace encoding DIFFERS - divergence happens during Laplace!")
	}
}
