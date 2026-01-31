// Package cgo provides CGO tests for large uniform encoding.
// This tests the specific case where V > 2^24.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestLargeUniformEncodingV16K12 tests encoding of PVQ indices for V(16, 12) = 479,240,480
// This is >2^24 (16,777,216), so requires multi-byte handling in ec_enc_uint.
func TestLargeUniformEncodingV16K12(t *testing.T) {
	n, k := 16, 12
	vSize := celt.PVQ_V(n, k)
	t.Logf("V(%d, %d) = %d (0x%x)", n, k, vSize, vSize)

	// ftb = ilog(vSize - 1)
	ftb := ilog(vSize - 1)
	t.Logf("ftb = ilog(%d - 1) = %d", vSize, ftb)
	t.Logf("EC_UINT_BITS = 8, so multi-byte encoding needed when ftb > 8")

	testCases := []uint32{
		0,                     // minimum
		1,                     // small
		1000,                  // medium
		1000000,               // 1M
		100000000,             // 100M
		200000000,             // 200M
		vSize - 1,             // maximum
		vSize / 2,             // middle
		uint32(1 << 24),       // exactly 2^24
		uint32((1 << 24) - 1), // just under 2^24
		uint32((1 << 24) + 1), // just over 2^24
	}

	for _, testIdx := range testCases {
		if testIdx >= vSize {
			continue
		}

		t.Run("", func(t *testing.T) {
			// Encode with gopus
			goBuf := make([]byte, 16)
			goEnc := &rangecoding.Encoder{}
			goEnc.Init(goBuf)
			goEnc.EncodeUniform(testIdx, vSize)
			goBytes := goEnc.Done()

			// Encode with libopus
			libBytes, _ := LibopusEncodeUniformSequence([]uint32{testIdx}, []uint32{vSize})

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
				t.Errorf("MISMATCH for idx=%d (0x%x):", testIdx, testIdx)
				t.Logf("  Go:      %d bytes = %x", len(goBytes), goBytes)
				t.Logf("  libopus: %d bytes = %x", len(libBytes), libBytes)
			} else {
				t.Logf("MATCH for idx=%d: %x", testIdx, goBytes)
			}
		})
	}
}

// TestUniformEncodingMultipleLargeValues tests a sequence of large uniform encodings.
func TestUniformEncodingMultipleLargeValues(t *testing.T) {
	// Use actual V(16, 12) value
	n, k := 16, 12
	vSize := celt.PVQ_V(n, k) // 3575055360

	// A sequence of large V values
	vals := []uint32{100000, 200000, 300000}
	fts := []uint32{vSize, vSize, vSize}

	// Encode with gopus
	goBuf := make([]byte, 256)
	goEnc := &rangecoding.Encoder{}
	goEnc.Init(goBuf)
	for i := range vals {
		goEnc.EncodeUniform(vals[i], fts[i])
	}
	goBytes := goEnc.Done()

	// Encode with libopus
	libBytes, _ := LibopusEncodeUniformSequence(vals, fts)

	t.Logf("Go bytes:     %x (%d bytes)", goBytes, len(goBytes))
	t.Logf("libopus bytes: %x (%d bytes)", libBytes, len(libBytes))

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
		t.Errorf("Mismatch!")
	}
}

// TestEncodeUniformStateTrace traces encoder state during large uniform encoding.
func TestEncodeUniformStateTrace(t *testing.T) {
	// Use actual V(16, 12) value
	n, k := 16, 12
	vSize := celt.PVQ_V(n, k)
	testIdx := uint32(100000000)

	t.Logf("Testing EncodeUniform(%d, %d)", testIdx, vSize)
	t.Logf("vSize = %d = 0x%x", vSize, vSize)

	// Check ftb calculation
	ftb := ilog(vSize - 1)
	t.Logf("ftb = ilog(%d) = %d", vSize-1, ftb)

	// EC_UINT_BITS = 8, so if ftb > 8, we split:
	// - High bits (ftb - 8 bits) via range coder
	// - Low bits (8 bits) as raw bits
	if ftb > 8 {
		ftbLow := uint(ftb - 8)
		ft1 := ((vSize - 1) >> ftbLow) + 1
		highVal := testIdx >> ftbLow
		lowVal := testIdx & ((1 << ftbLow) - 1)

		t.Logf("Multi-byte encoding:")
		t.Logf("  ftb = %d > EC_UINT_BITS(8), so split", ftb)
		t.Logf("  ftbLow = %d (bits for raw encoding)", ftbLow)
		t.Logf("  ft1 = (vSize-1) >> ftbLow + 1 = %d", ft1)
		t.Logf("  highVal = testIdx >> ftbLow = %d (encoded with range coder)", highVal)
		t.Logf("  lowVal = testIdx & mask = 0x%x (encoded as raw bits)", lowVal)
	}

	// Now trace actual encoding
	goBuf := make([]byte, 16)
	goEnc := &rangecoding.Encoder{}
	goEnc.Init(goBuf)

	t.Logf("\nGo encoder initial state:")
	t.Logf("  rng=0x%x val=0x%x offs=%d", goEnc.Range(), goEnc.Val(), goEnc.RangeBytes())

	goEnc.EncodeUniform(testIdx, vSize)

	t.Logf("Go encoder after EncodeUniform:")
	t.Logf("  rng=0x%x val=0x%x offs=%d", goEnc.Range(), goEnc.Val(), goEnc.RangeBytes())

	goBytes := goEnc.Done()
	t.Logf("Go final bytes: %x (%d bytes)", goBytes, len(goBytes))

	// Compare with libopus
	libBytes, _ := LibopusEncodeUniformSequence([]uint32{testIdx}, []uint32{vSize})
	t.Logf("libopus bytes:  %x (%d bytes)", libBytes, len(libBytes))
}

// TestTraceRangeEncoderStep traces step-by-step encoder state
func TestTraceRangeEncoderStep(t *testing.T) {
	// Simple test: encode fl=5, fh=6, ft=214 (the high-part range encoding)
	t.Run("simple_encode", func(t *testing.T) {
		fl, fh, ft := uint32(5), uint32(6), uint32(214)

		goBuf := make([]byte, 16)
		goEnc := &rangecoding.Encoder{}
		goEnc.Init(goBuf)

		t.Logf("Initial: rng=0x%08x val=0x%08x rem=%d ext=%d offs=%d",
			goEnc.Range(), goEnc.Val(), goEnc.Rem(), goEnc.Ext(), goEnc.RangeBytes())

		goEnc.Encode(fl, fh, ft)

		t.Logf("After Encode(%d,%d,%d): rng=0x%08x val=0x%08x rem=%d ext=%d offs=%d",
			fl, fh, ft, goEnc.Range(), goEnc.Val(), goEnc.Rem(), goEnc.Ext(), goEnc.RangeBytes())

		goBytes := goEnc.Done()
		t.Logf("Final bytes: %x", goBytes)

		// Compare with libopus
		libStates, libBytes := TraceEncodeSequence([]uint32{fl}, []uint32{fh}, []uint32{ft})
		if libStates != nil {
			t.Logf("libopus after Encode: rng=0x%08x val=0x%08x rem=%d ext=%d offs=%d",
				libStates[1].Rng, libStates[1].Val, libStates[1].Rem, libStates[1].Ext, libStates[1].Offs)
		}
		t.Logf("libopus bytes: %x", libBytes)
	})

	// Test with raw bits added
	t.Run("encode_with_raw_bits", func(t *testing.T) {
		fl, fh, ft := uint32(5), uint32(6), uint32(214)
		rawVal := uint32(0xF5E100)
		rawBits := uint(24)

		goBuf := make([]byte, 16)
		goEnc := &rangecoding.Encoder{}
		goEnc.Init(goBuf)

		t.Logf("Initial: rng=0x%08x val=0x%08x rem=%d ext=%d offs=%d",
			goEnc.Range(), goEnc.Val(), goEnc.Rem(), goEnc.Ext(), goEnc.RangeBytes())

		goEnc.Encode(fl, fh, ft)
		t.Logf("After Encode: rng=0x%08x val=0x%08x rem=%d ext=%d offs=%d",
			goEnc.Range(), goEnc.Val(), goEnc.Rem(), goEnc.Ext(), goEnc.RangeBytes())

		goEnc.EncodeRawBits(rawVal, rawBits)
		t.Logf("After EncodeRawBits(0x%x, %d): rem=%d ext=%d offs=%d nendBits=%d",
			rawVal, rawBits, goEnc.Rem(), goEnc.Ext(), goEnc.RangeBytes(), goEnc.Tell())

		goBytes := goEnc.Done()
		t.Logf("Go final bytes: %x (%d bytes)", goBytes, len(goBytes))

		// Now use EncodeUniform which combines both steps
		goBuf2 := make([]byte, 16)
		goEnc2 := &rangecoding.Encoder{}
		goEnc2.Init(goBuf2)
		n, k := 16, 12
		vSize := celt.PVQ_V(n, k)
		goEnc2.EncodeUniform(100000000, vSize)
		goBytes2 := goEnc2.Done()
		t.Logf("Go EncodeUniform bytes: %x (%d bytes)", goBytes2, len(goBytes2))

		// Get libopus detailed state
		libBytes, libOffs, libEndOffs, libNendBits := LibopusEncodeUniformDetailed(100000000, vSize)
		t.Logf("libopus bytes: %x (%d bytes)", libBytes, len(libBytes))
		t.Logf("libopus offs=%d, end_offs=%d, nend_bits=%d", libOffs, libEndOffs, libNendBits)
	})
}
