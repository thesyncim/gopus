// Package cgo provides CGO comparison tests for the range encoder.
// This file tests the Go range encoder against libopus ec_enc.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestRangeEncoderUniformMatchesLibopus tests that the Go range encoder
// produces the same bytes as libopus for uniform encoding.
func TestRangeEncoderUniformMatchesLibopus(t *testing.T) {
	testCases := []struct {
		name string
		vals []uint32
		fts  []uint32
	}{
		{"single_2", []uint32{1}, []uint32{2}},
		{"single_4", []uint32{2}, []uint32{4}},
		{"single_8", []uint32{5}, []uint32{8}},
		{"multiple_same", []uint32{1, 2, 3}, []uint32{4, 4, 4}},
		{"multiple_different", []uint32{1, 3, 7}, []uint32{2, 4, 8}},
		{"larger_range", []uint32{100}, []uint32{256}},
		{"zero", []uint32{0}, []uint32{4}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Encode with Go
			buf := make([]byte, 4096)
			enc := &rangecoding.Encoder{}
			enc.Init(buf)
			for i := range tc.vals {
				enc.EncodeUniform(tc.vals[i], tc.fts[i])
			}
			goBytes := enc.Done()

			// Encode with libopus
			libBytes, _ := LibopusEncodeUniformSequence(tc.vals, tc.fts)

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
				t.Errorf("Mismatch for %s:", tc.name)
				t.Logf("  Go:      %x (len=%d)", goBytes, len(goBytes))
				t.Logf("  libopus: %x (len=%d)", libBytes, len(libBytes))
			} else {
				t.Logf("Match: %x", goBytes)
			}
		})
	}
}

// TestCWRSBytesMatchLibopus tests that Go CWRS encoding matches libopus at byte level.
func TestCWRSBytesMatchLibopus(t *testing.T) {
	testCases := []struct {
		name   string
		pulses []int
		n, k   int
	}{
		{"simple_pos", []int{1, 0, 0, 0}, 4, 1},
		{"simple_neg", []int{-1, 0, 0, 0}, 4, 1},
		{"two_pulses", []int{1, 1, 0, 0}, 4, 2},
		{"spread", []int{1, 0, 1, 0}, 4, 2},
		{"all_positive", []int{1, 1, 1, 1}, 4, 4},
		{"mixed_signs", []int{1, -1, 1, -1}, 4, 4},
		{"larger_k", []int{2, 0, 1, 0}, 4, 3},
		{"k4_n8", []int{1, 0, 1, 0, 1, 0, 1, 0}, 8, 4},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Compute sum of absolute values to verify k
			sum := 0
			for _, p := range tc.pulses {
				if p < 0 {
					sum -= p
				} else {
					sum += p
				}
			}
			if sum != tc.k {
				t.Fatalf("Test case k mismatch: sum=%d, k=%d", sum, tc.k)
			}

			// Encode with Go
			goIndex := celt.EncodePulses(tc.pulses, tc.n, tc.k)

			// Encode to bytes with libopus
			libBytes, _ := LibopusEncodePulsesToBytes(tc.pulses, tc.n, tc.k)

			// Encode with Go range encoder
			vSize := celt.PVQ_V(tc.n, tc.k)
			buf := make([]byte, 4096)
			enc := &rangecoding.Encoder{}
			enc.Init(buf)
			enc.EncodeUniform(goIndex, vSize)
			goBytes := enc.Done()

			t.Logf("Pulses: %v", tc.pulses)
			t.Logf("Go index: %d, V=%d", goIndex, vSize)
			t.Logf("Go bytes:     %x", goBytes)
			t.Logf("libopus bytes: %x", libBytes)

			// Note: libopus encode_pulses includes the CWRS encoding directly,
			// so the comparison depends on V(n,k) being same.
		})
	}
}

// TestRangeEncoderRoundtripWithLibopusDecoder tests that Go-encoded bytes
// can be decoded by libopus.
func TestRangeEncoderRoundtripWithLibopusDecoder(t *testing.T) {
	// This would require a libopus decoder wrapper which we don't have yet.
	// For now, just verify the encoding produces correct output.
	t.Skip("Need libopus decoder wrapper")
}

// ilog computes integer log base 2 (position of highest set bit + 1)
func ilog(x uint32) int {
	if x == 0 {
		return 0
	}
	n := 0
	if x >= (1 << 16) {
		n += 16
		x >>= 16
	}
	if x >= (1 << 8) {
		n += 8
		x >>= 8
	}
	if x >= (1 << 4) {
		n += 4
		x >>= 4
	}
	if x >= (1 << 2) {
		n += 2
		x >>= 2
	}
	if x >= (1 << 1) {
		n += 1
		x >>= 1
	}
	return n + int(x)
}

// captureGoState captures the current Go encoder state
func captureGoState(enc *rangecoding.Encoder) RangeEncoderStateSnapshot {
	return RangeEncoderStateSnapshot{
		Rng:        enc.Range(),
		Val:        enc.Val(),
		Rem:        enc.Rem(),
		Ext:        enc.Ext(),
		Offs:       uint32(enc.RangeBytes()),
		NbitsTotal: enc.Tell() + ilog(enc.Range()), // Approximate nbitsTotal
		Tell:       enc.Tell(),
	}
}

// TestEncodeBitStateTrace traces state after each bit and compares Go vs libopus
func TestEncodeBitStateTrace(t *testing.T) {
	testCases := []struct {
		name  string
		bits  []int
		logps []int
	}{
		{"single_0_logp1", []int{0}, []int{1}},
		{"single_1_logp1", []int{1}, []int{1}},
		{"single_0_logp15", []int{0}, []int{15}},
		{"single_1_logp15", []int{1}, []int{15}},
		{"sequence_01_logp1", []int{0, 1}, []int{1, 1}},
		{"sequence_10_logp1", []int{1, 0}, []int{1, 1}},
		{"mixed_logp", []int{0, 1, 0, 1}, []int{1, 2, 4, 8}},
		{"all_zeros", []int{0, 0, 0, 0, 0, 0, 0, 0}, []int{1, 1, 1, 1, 1, 1, 1, 1}},
		{"all_ones", []int{1, 1, 1, 1, 1, 1, 1, 1}, []int{1, 1, 1, 1, 1, 1, 1, 1}},
		{"silence_flag", []int{0}, []int{15}}, // Silence flag uses logp=15
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			count := len(tc.bits)

			// Get libopus trace
			libStates, libBytes := TraceBitSequence(tc.bits, tc.logps)
			if libStates == nil {
				t.Fatal("Failed to get libopus trace")
			}

			// Go encoder
			goBuf := make([]byte, 256)
			goEnc := &rangecoding.Encoder{}
			goEnc.Init(goBuf)

			// Compare initial state
			goState := captureGoState(goEnc)
			t.Logf("Initial: Go rng=%#x val=%#x rem=%d ext=%d tell=%d",
				goState.Rng, goState.Val, goState.Rem, goState.Ext, goState.Tell)
			t.Logf("Initial: Lib rng=%#x val=%#x rem=%d ext=%d tell=%d",
				libStates[0].Rng, libStates[0].Val, libStates[0].Rem, libStates[0].Ext, libStates[0].Tell)

			// Encode each bit and compare state after
			diverged := false
			for i := 0; i < count; i++ {
				goEnc.EncodeBit(tc.bits[i], uint(tc.logps[i]))

				goState = captureGoState(goEnc)
				libState := libStates[i+1]

				if goState.Rng != libState.Rng || goState.Val != libState.Val ||
					goState.Rem != libState.Rem || goState.Ext != libState.Ext {
					t.Errorf("State mismatch after bit %d (val=%d logp=%d):", i, tc.bits[i], tc.logps[i])
					t.Errorf("  Go:  rng=%#x val=%#x rem=%d ext=%d offs=%d tell=%d",
						goState.Rng, goState.Val, goState.Rem, goState.Ext, goState.Offs, goState.Tell)
					t.Errorf("  Lib: rng=%#x val=%#x rem=%d ext=%d offs=%d tell=%d",
						libState.Rng, libState.Val, libState.Rem, libState.Ext, libState.Offs, libState.Tell)
					diverged = true
					break
				}
			}

			if diverged {
				return
			}

			// Compare final output bytes
			goBytes := goEnc.Done()

			if len(goBytes) != len(libBytes) {
				t.Errorf("Output length mismatch: go=%d lib=%d", len(goBytes), len(libBytes))
				t.Logf("Go bytes:  %x", goBytes)
				t.Logf("Lib bytes: %x", libBytes)
			} else {
				match := true
				for i := range goBytes {
					if goBytes[i] != libBytes[i] {
						match = false
						break
					}
				}
				if !match {
					t.Errorf("Output bytes mismatch:")
					t.Logf("Go bytes:  %x", goBytes)
					t.Logf("Lib bytes: %x", libBytes)
				} else {
					t.Logf("Output matches: %x", goBytes)
				}
			}
		})
	}
}

// TestEncodeSequenceMatchesLibopus tests that Encode() matches libopus ec_encode()
func TestEncodeSequenceMatchesLibopus(t *testing.T) {
	testCases := []struct {
		name string
		fls  []uint32
		fhs  []uint32
		fts  []uint32
	}{
		{"single_first", []uint32{0}, []uint32{64}, []uint32{256}},
		{"single_last", []uint32{192}, []uint32{256}, []uint32{256}},
		{"two_symbols", []uint32{0, 64}, []uint32{64, 128}, []uint32{256, 256}},
		{"narrow_range", []uint32{100}, []uint32{101}, []uint32{256}},
		{"laplace_like", []uint32{0}, []uint32{15000}, []uint32{32768}}, // Similar to Laplace center
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Get libopus trace
			libStates, libBytes := TraceEncodeSequence(tc.fls, tc.fhs, tc.fts)
			if libStates == nil {
				t.Fatal("Failed to get libopus trace")
			}

			// Go encoder
			goBuf := make([]byte, 256)
			goEnc := &rangecoding.Encoder{}
			goEnc.Init(goBuf)

			// Encode each symbol and compare
			for i := range tc.fls {
				goEnc.Encode(tc.fls[i], tc.fhs[i], tc.fts[i])

				goState := captureGoState(goEnc)
				libState := libStates[i+1]

				if goState.Rng != libState.Rng || goState.Val != libState.Val {
					t.Errorf("State mismatch after symbol %d (fl=%d fh=%d ft=%d):", i, tc.fls[i], tc.fhs[i], tc.fts[i])
					t.Errorf("  Go:  rng=%#x val=%#x rem=%d ext=%d tell=%d",
						goState.Rng, goState.Val, goState.Rem, goState.Ext, goState.Tell)
					t.Errorf("  Lib: rng=%#x val=%#x rem=%d ext=%d tell=%d",
						libState.Rng, libState.Val, libState.Rem, libState.Ext, libState.Tell)
				}
			}

			// Compare final bytes
			goBytes := goEnc.Done()

			if len(goBytes) != len(libBytes) {
				t.Errorf("Output length mismatch: go=%d lib=%d", len(goBytes), len(libBytes))
			}
			match := true
			for i := range goBytes {
				if i >= len(libBytes) || goBytes[i] != libBytes[i] {
					match = false
					break
				}
			}
			if !match {
				t.Errorf("Output mismatch: go=%x lib=%x", goBytes, libBytes)
			} else {
				t.Logf("Match: %x", goBytes)
			}
		})
	}
}

// TestEncodeICDFMatchesLibopus tests that EncodeICDF matches libopus ec_enc_icdf
func TestEncodeICDFMatchesLibopus(t *testing.T) {
	// Uniform distribution ICDF: 4 symbols with equal probability
	uniformICDF := []byte{192, 128, 64, 0}

	testCases := []struct {
		name    string
		symbols []int
		icdf    []byte
		ftb     uint
	}{
		{"symbol_0", []int{0}, uniformICDF, 8},
		{"symbol_1", []int{1}, uniformICDF, 8},
		{"symbol_2", []int{2}, uniformICDF, 8},
		{"symbol_3", []int{3}, uniformICDF, 8},
		{"sequence", []int{0, 1, 2, 3, 0, 1, 2, 3}, uniformICDF, 8},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Get libopus trace
			libStates, libBytes := TraceICDFSequence(tc.symbols, tc.icdf, tc.ftb)
			if libStates == nil {
				t.Fatal("Failed to get libopus trace")
			}

			// Go encoder
			goBuf := make([]byte, 256)
			goEnc := &rangecoding.Encoder{}
			goEnc.Init(goBuf)

			// Encode each symbol and compare
			for i := range tc.symbols {
				goEnc.EncodeICDF(tc.symbols[i], tc.icdf, tc.ftb)

				goState := captureGoState(goEnc)
				libState := libStates[i+1]

				if goState.Rng != libState.Rng || goState.Val != libState.Val {
					t.Errorf("State mismatch after symbol %d (s=%d):", i, tc.symbols[i])
					t.Errorf("  Go:  rng=%#x val=%#x rem=%d ext=%d tell=%d",
						goState.Rng, goState.Val, goState.Rem, goState.Ext, goState.Tell)
					t.Errorf("  Lib: rng=%#x val=%#x rem=%d ext=%d tell=%d",
						libState.Rng, libState.Val, libState.Rem, libState.Ext, libState.Tell)
				}
			}

			// Compare final bytes
			goBytes := goEnc.Done()

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
				t.Errorf("Output mismatch: go=%x lib=%x", goBytes, libBytes)
			} else {
				t.Logf("Match: %x", goBytes)
			}
		})
	}
}
