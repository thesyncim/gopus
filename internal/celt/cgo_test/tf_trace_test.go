// Package cgo provides tests for TF encoding comparison.
// Agent 22: Debug TF encoding divergence
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTFEncodeMatchesLibopus tests TF encoding for various tf_res patterns
func TestTFEncodeMatchesLibopus(t *testing.T) {
	// Test cases with different tf_res patterns
	testCases := []struct {
		name        string
		nbBands     int
		isTransient bool
		tfRes       []int
		tfSelect    int
		lm          int
	}{
		{"all_zeros_20ms_transient", 21, true, make([]int, 21), 0, 3},
		{"all_zeros_20ms_non_transient", 21, false, make([]int, 21), 0, 3},
		{"first_one_transient", 21, true, append([]int{1}, make([]int, 20)...), 0, 3},
		{"alternating_transient", 21, true, alternatingPattern(21), 0, 3},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Trace with libopus
			libTrace, libBytes := TraceTFEncode(0, tc.nbBands, tc.isTransient, tc.tfRes, tc.lm, tc.tfSelect)

			// Encode with gopus
			goBuf := make([]byte, 4096)
			goEnc := &rangecoding.Encoder{}
			goEnc.Init(goBuf)

			// Match the TF encoding from tf.go
			budget := uint32(goEnc.StorageBits())
			tell := goEnc.Tell()
			logp := 4
			if tc.isTransient {
				logp = 2
			}

			tfSelectRsv := tc.lm > 0 && tell+logp+1 <= int(budget)
			if tfSelectRsv {
				budget--
			}

			curr := 0
			tfChanged := 0

			for i := 0; i < tc.nbBands; i++ {
				if goEnc.Tell()+logp <= int(budget) {
					change := tc.tfRes[i] ^ curr
					goEnc.EncodeBit(change, uint(logp))
					curr = tc.tfRes[i]
					if curr != 0 {
						tfChanged = 1
					}
				}

				if tc.isTransient {
					logp = 4
				} else {
					logp = 5
				}
			}

			// TF select table from tables.go
			tfSelectTable := [4][8]int8{
				{0, -1, 0, -1, 0, -1, 0, -1},
				{0, -1, 0, -2, 1, 0, 1, -1},
				{0, -2, 0, -3, 2, 0, 1, -1},
				{0, -2, 0, -3, 3, 0, 1, -1},
			}

			isTransientInt := 0
			if tc.isTransient {
				isTransientInt = 1
			}

			if tfSelectRsv &&
				tfSelectTable[tc.lm][4*isTransientInt+0+tfChanged] !=
					tfSelectTable[tc.lm][4*isTransientInt+2+tfChanged] {
				goEnc.EncodeBit(tc.tfSelect, 1)
			}

			goBytes := goEnc.Done()

			t.Logf("libopus: tell_before=%d, tell_after=%d, tf_changed=%d, tf_select_encoded=%d",
				libTrace.TellBefore, libTrace.TellAfter, libTrace.TFChanged, libTrace.TFSelectEncoded)
			t.Logf("gopus tell_after=%d", goEnc.Tell())
			t.Logf("libopus bytes: %x", libBytes)
			t.Logf("gopus bytes:   %x", goBytes)

			// Compare
			match := len(goBytes) == len(libBytes)
			if match {
				for i := range goBytes {
					if goBytes[i] != libBytes[i] {
						match = false
						break
					}
				}
			}

			if !match {
				t.Errorf("TF encoding mismatch for %s", tc.name)
			}
		})
	}
}

// TestTFAndSpreadEncodeMatchesLibopus tests TF + spread encoding
func TestTFAndSpreadEncodeMatchesLibopus(t *testing.T) {
	// Test with typical first-frame parameters
	testCases := []struct {
		name        string
		nbBands     int
		isTransient bool
		tfRes       []int
		tfSelect    int
		lm          int
		spread      int
	}{
		{"zeros_transient_spread_normal", 21, true, make([]int, 21), 0, 3, 2},
		{"zeros_transient_spread_aggressive", 21, true, make([]int, 21), 0, 3, 3},
		{"ones_transient_spread_normal", 21, true, allOnes(21), 0, 3, 2},
	}

	spreadICDF := []uint8{25, 23, 2, 0}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Trace with libopus
			libTrace, libBytes := TraceTFAndSpreadEncode(0, tc.nbBands, tc.isTransient, tc.tfRes, tc.lm, tc.tfSelect, tc.spread)

			// Encode with gopus
			goBuf := make([]byte, 4096)
			goEnc := &rangecoding.Encoder{}
			goEnc.Init(goBuf)

			// TF encoding
			budget := uint32(goEnc.StorageBits())
			logp := 4
			if tc.isTransient {
				logp = 2
			}

			tfSelectRsv := tc.lm > 0 && goEnc.Tell()+logp+1 <= int(budget)
			if tfSelectRsv {
				budget--
			}

			curr := 0
			tfChanged := 0

			for i := 0; i < tc.nbBands; i++ {
				if goEnc.Tell()+logp <= int(budget) {
					change := tc.tfRes[i] ^ curr
					goEnc.EncodeBit(change, uint(logp))
					curr = tc.tfRes[i]
					if curr != 0 {
						tfChanged = 1
					}
				}

				if tc.isTransient {
					logp = 4
				} else {
					logp = 5
				}
			}

			// TF select table
			tfSelectTable := [4][8]int8{
				{0, -1, 0, -1, 0, -1, 0, -1},
				{0, -1, 0, -2, 1, 0, 1, -1},
				{0, -2, 0, -3, 2, 0, 1, -1},
				{0, -2, 0, -3, 3, 0, 1, -1},
			}

			isTransientInt := 0
			if tc.isTransient {
				isTransientInt = 1
			}

			actualTFSelect := tc.tfSelect
			if tfSelectRsv &&
				tfSelectTable[tc.lm][4*isTransientInt+0+tfChanged] !=
					tfSelectTable[tc.lm][4*isTransientInt+2+tfChanged] {
				goEnc.EncodeBit(tc.tfSelect, 1)
			} else {
				actualTFSelect = 0
			}

			goTellAfterTF := goEnc.Tell()

			// Spread encoding
			goEnc.EncodeICDF(tc.spread, spreadICDF, 5)

			goBytes := goEnc.Done()

			t.Logf("libopus: tell_before_tf=%d, tell_after_tf=%d, tell_after_spread=%d, tf_select=%d",
				libTrace.TellBeforeTF, libTrace.TellAfterTF, libTrace.TellAfterSpread, libTrace.TFSelect)
			t.Logf("gopus: tell_after_tf=%d, tf_select=%d", goTellAfterTF, actualTFSelect)
			t.Logf("libopus bytes: %x", libBytes)
			t.Logf("gopus bytes:   %x", goBytes)

			// Compare
			match := len(goBytes) == len(libBytes)
			if match {
				for i := range goBytes {
					if goBytes[i] != libBytes[i] {
						match = false
						break
					}
				}
			}

			if !match {
				t.Errorf("TF+Spread encoding mismatch for %s", tc.name)
			} else {
				t.Log("MATCH!")
			}
		})
	}
}

func alternatingPattern(n int) []int {
	result := make([]int, n)
	for i := 0; i < n; i++ {
		result[i] = i % 2
	}
	return result
}

func allOnes(n int) []int {
	result := make([]int, n)
	for i := 0; i < n; i++ {
		result[i] = 1
	}
	return result
}
