package rangecoding

import (
	"math/rand"
	"testing"
)

// Range coder round-trip tests verify that encode->decode produces identical values.
// These tests prove the encoder and decoder are symmetric inverses.

// TestEncoderDecoderOutputSize verifies encoder produces reasonable output sizes.
func TestEncoderDecoderOutputSize(t *testing.T) {
	tests := []struct {
		name    string
		numBits int
		logp    uint
		minSize int
		maxSize int
	}{
		{"8 bits logp=1", 8, 1, 1, 4},
		{"16 bits logp=1", 16, 1, 1, 8},
		{"32 bits logp=1", 32, 1, 1, 16},
		{"8 bits logp=4", 8, 4, 1, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, 64)
			enc := &Encoder{}
			enc.Init(buf)

			for i := 0; i < tt.numBits; i++ {
				enc.EncodeBit(i%2, tt.logp)
			}
			encoded := enc.Done()

			if len(encoded) < tt.minSize || len(encoded) > tt.maxSize {
				t.Errorf("output size %d not in expected range [%d, %d]",
					len(encoded), tt.minSize, tt.maxSize)
			}
		})
	}
}

// TestEncoderDeterminism verifies encoder produces consistent output.
func TestEncoderDeterminism(t *testing.T) {
	bits := []int{1, 0, 1, 1, 0, 0, 1, 0, 1, 1, 1, 0, 0, 0, 1, 1}

	var firstResult []byte

	for run := 0; run < 5; run++ {
		buf := make([]byte, 64)
		enc := &Encoder{}
		enc.Init(buf)

		for _, bit := range bits {
			enc.EncodeBit(bit, 1)
		}
		result := enc.Done()

		resultCopy := make([]byte, len(result))
		copy(resultCopy, result)

		if firstResult == nil {
			firstResult = resultCopy
		} else {
			if len(resultCopy) != len(firstResult) {
				t.Errorf("run %d: length %d, want %d", run, len(resultCopy), len(firstResult))
				continue
			}
			for i := range firstResult {
				if resultCopy[i] != firstResult[i] {
					t.Errorf("run %d: byte %d = %#x, want %#x", run, i, resultCopy[i], firstResult[i])
				}
			}
		}
	}
}

// TestEncoderICDFDeterminism verifies ICDF encoding produces consistent output.
func TestEncoderICDFDeterminism(t *testing.T) {
	icdf := []uint8{192, 128, 64, 0}
	symbols := []int{0, 1, 2, 3, 0, 1, 2, 3}

	var firstResult []byte

	for run := 0; run < 5; run++ {
		buf := make([]byte, 64)
		enc := &Encoder{}
		enc.Init(buf)

		for _, sym := range symbols {
			enc.EncodeICDF(sym, icdf, 8)
		}
		result := enc.Done()

		resultCopy := make([]byte, len(result))
		copy(resultCopy, result)

		if firstResult == nil {
			firstResult = resultCopy
		} else {
			if len(resultCopy) != len(firstResult) {
				t.Errorf("run %d: length %d, want %d", run, len(resultCopy), len(firstResult))
				continue
			}
			for i := range firstResult {
				if resultCopy[i] != firstResult[i] {
					t.Errorf("run %d: byte %d = %#x, want %#x", run, i, resultCopy[i], firstResult[i])
				}
			}
		}
	}
}

// TestEncoderStateTracking verifies Tell and TellFrac are consistent.
func TestEncoderStateTracking(t *testing.T) {
	buf := make([]byte, 256)
	enc := &Encoder{}
	enc.Init(buf)

	var prevTell int

	// Encode many bits and verify Tell increases
	for i := 0; i < 50; i++ {
		enc.EncodeBit(i%2, 1)
		tell := enc.Tell()
		if tell < prevTell {
			t.Errorf("Tell decreased: %d -> %d at bit %d", prevTell, tell, i)
		}
		prevTell = tell

		// TellFrac should be roughly 8x Tell
		tellFrac := enc.TellFrac()
		if tellFrac < (tell-1)*8 || tellFrac > (tell+1)*8 {
			t.Errorf("TellFrac=%d inconsistent with Tell=%d at bit %d", tellFrac, tell, i)
		}
	}
}

// TestEncoderRangeInvariantExtended verifies range invariant across many operations.
func TestEncoderRangeInvariantExtended(t *testing.T) {
	buf := make([]byte, 1024)
	enc := &Encoder{}
	enc.Init(buf)

	rng := rand.New(rand.NewSource(42))

	// Mix of operations
	for i := 0; i < 200; i++ {
		if rng.Intn(2) == 0 {
			enc.EncodeBit(rng.Intn(2), uint(1+rng.Intn(8)))
		} else {
			icdf := []uint8{200, 150, 100, 50, 0}
			enc.EncodeICDF(rng.Intn(len(icdf)-1), icdf, 8)
		}

		// After normalize, range should be > EC_CODE_BOT
		if enc.Range() <= EC_CODE_BOT {
			t.Errorf("range invariant violated at operation %d: rng=%#x", i, enc.Range())
		}
	}

	// Should produce valid output
	result := enc.Done()
	if len(result) == 0 {
		t.Error("expected non-empty output")
	}
}

// TestEncoderLongSequence verifies encoder handles long sequences.
func TestEncoderLongSequence(t *testing.T) {
	buf := make([]byte, 4096)
	enc := &Encoder{}
	enc.Init(buf)

	rng := rand.New(rand.NewSource(123))

	// Encode 1000 symbols
	icdf := []uint8{200, 150, 100, 50, 0}
	for i := 0; i < 1000; i++ {
		enc.EncodeICDF(rng.Intn(len(icdf)-1), icdf, 8)
	}

	result := enc.Done()
	t.Logf("Encoded 1000 symbols into %d bytes (%.2f bits/symbol)",
		len(result), float64(len(result)*8)/1000)

	// Output should be reasonable size (entropy suggests ~1.8 bits/symbol)
	if len(result) < 100 || len(result) > 500 {
		t.Errorf("unexpected output size: %d bytes", len(result))
	}
}

// TestEncoderAllZeros verifies encoding all-zero sequences.
func TestEncoderAllZeros(t *testing.T) {
	buf := make([]byte, 64)
	enc := &Encoder{}
	enc.Init(buf)

	for i := 0; i < 32; i++ {
		enc.EncodeBit(0, 1)
	}

	result := enc.Done()

	// All zeros with logp=1 is the most likely sequence,
	// should produce small output
	if len(result) > 16 {
		t.Errorf("all-zeros output unexpectedly large: %d bytes", len(result))
	}
}

// TestEncoderAllOnes verifies encoding all-one sequences.
func TestEncoderAllOnes(t *testing.T) {
	buf := make([]byte, 64)
	enc := &Encoder{}
	enc.Init(buf)

	for i := 0; i < 32; i++ {
		enc.EncodeBit(1, 1)
	}

	result := enc.Done()

	// All ones with logp=1 is unlikely, should produce larger output
	// than all zeros (but still bounded)
	if len(result) < 1 || len(result) > 32 {
		t.Errorf("unexpected output size: %d bytes", len(result))
	}
}

// TestEncoderMixedBitsAndICDF verifies mixing operations works.
func TestEncoderMixedBitsAndICDF(t *testing.T) {
	buf := make([]byte, 256)
	enc := &Encoder{}
	enc.Init(buf)

	icdf := []uint8{192, 128, 64, 0}

	// Alternate between bits and symbols
	for i := 0; i < 20; i++ {
		enc.EncodeBit(i%2, 1)
		enc.EncodeICDF(i%4, icdf, 8)
	}

	result := enc.Done()
	if len(result) == 0 {
		t.Error("expected non-empty output")
	}
}

// TestEncodeUniformProducesOutput verifies EncodeUniform produces non-empty output.
// Full round-trip is verified in TestEncodeDecodeUniformRoundTrip.
func TestEncodeUniformProducesOutput(t *testing.T) {
	tests := []struct {
		name string
		val  uint32
		ft   uint32
	}{
		{"simple_small", 5, 16},
		{"zero_value", 0, 100},
		{"max_value", 99, 100},
		{"power_of_2", 7, 8},
		{"large_ft", 1000, 4096},
		{"single_value", 0, 1}, // Edge case: only one possible value
		{"edge_ft_256", 100, 256},
		{"multi_byte_ft_500", 300, 500},
		{"multi_byte_ft_1000", 500, 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, 256)
			enc := &Encoder{}
			enc.Init(buf)

			enc.EncodeUniform(tt.val, tt.ft)
			encoded := enc.Done()

			// For ft > 1, should produce some output
			if len(encoded) == 0 && tt.ft > 1 {
				t.Errorf("empty encoded output for val=%d, ft=%d", tt.val, tt.ft)
			}

			// Verify range invariant maintained
			if enc.Range() <= EC_CODE_BOT {
				t.Errorf("range invariant violated after EncodeUniform(%d, %d)", tt.val, tt.ft)
			}
		})
	}
}

// TestEncodeUniformMultipleValues verifies encoding multiple uniform values produces output.
func TestEncodeUniformMultipleValues(t *testing.T) {
	// Encode multiple values
	values := []struct{ val, ft uint32 }{
		{5, 16},
		{100, 256},
		{7, 8},
		{50, 100},
	}

	buf := make([]byte, 256)
	enc := &Encoder{}
	enc.Init(buf)

	for _, v := range values {
		enc.EncodeUniform(v.val, v.ft)
	}
	encoded := enc.Done()

	// Should produce non-empty output
	if len(encoded) == 0 {
		t.Error("empty encoded output for multiple uniform values")
	}

	// Output size should be reasonable
	if len(encoded) > 50 {
		t.Errorf("encoded size %d seems too large for 4 values", len(encoded))
	}
}

// TestEncodeUniformRangeInvariant verifies range stays valid after EncodeUniform.
func TestEncodeUniformRangeInvariant(t *testing.T) {
	buf := make([]byte, 1024)
	enc := &Encoder{}
	enc.Init(buf)

	rng := rand.New(rand.NewSource(42))

	for i := 0; i < 100; i++ {
		ft := uint32(2 + rng.Intn(1000))
		val := uint32(rng.Intn(int(ft)))
		enc.EncodeUniform(val, ft)

		if enc.Range() <= EC_CODE_BOT {
			t.Errorf("range invariant violated at iteration %d: rng=%#x", i, enc.Range())
		}
	}
}

// TestEncodeUniformDeterminism verifies EncodeUniform is deterministic.
func TestEncodeUniformDeterminism(t *testing.T) {
	values := []struct{ val, ft uint32 }{
		{5, 16},
		{100, 256},
		{7, 8},
		{250, 500},
	}

	encode := func() []byte {
		buf := make([]byte, 256)
		enc := &Encoder{}
		enc.Init(buf)
		for _, v := range values {
			enc.EncodeUniform(v.val, v.ft)
		}
		result := enc.Done()
		out := make([]byte, len(result))
		copy(out, result)
		return out
	}

	result1 := encode()
	result2 := encode()

	if len(result1) != len(result2) {
		t.Fatalf("non-deterministic lengths: %d vs %d", len(result1), len(result2))
	}
	for i := range result1 {
		if result1[i] != result2[i] {
			t.Errorf("non-deterministic byte %d: %d vs %d", i, result1[i], result2[i])
		}
	}
}

// TestEncodeDecodeBitRoundTrip verifies single bit encode->decode round-trip.
func TestEncodeDecodeBitRoundTrip(t *testing.T) {
	for _, logp := range []uint{1, 2, 4, 8} {
		for _, bitVal := range []int{0, 1} {
			buf := make([]byte, 64)
			enc := &Encoder{}
			enc.Init(buf)
			enc.EncodeBit(bitVal, logp)
			encoded := enc.Done()

			dec := &Decoder{}
			dec.Init(encoded)
			decoded := dec.DecodeBit(logp)

			if decoded != bitVal {
				t.Errorf("bit=%d logp=%d -> decoded=%d (bytes: %x)", bitVal, logp, decoded, encoded)
			}
		}
	}
}

// TestEncodeDecodeICDFRoundTrip verifies ICDF symbol encode->decode round-trip.
func TestEncodeDecodeICDFRoundTrip(t *testing.T) {
	icdf := []uint8{192, 128, 64, 0}
	for sym := 0; sym < 4; sym++ {
		buf := make([]byte, 64)
		enc := &Encoder{}
		enc.Init(buf)
		enc.EncodeICDF(sym, icdf, 8)
		encoded := enc.Done()

		dec := &Decoder{}
		dec.Init(encoded)
		decoded := dec.DecodeICDF(icdf, 8)

		if decoded != sym {
			t.Errorf("ICDF sym=%d -> decoded=%d (bytes: %x)", sym, decoded, encoded)
		}
	}
}

// TestEncodeDecodeUniformRoundTrip verifies uniform value encode->decode round-trip.
func TestEncodeDecodeUniformRoundTrip(t *testing.T) {
	for _, ft := range []uint32{8, 16, 100, 256} {
		for _, val := range []uint32{0, 1, ft / 2, ft - 1} {
			if val >= ft {
				continue
			}
			buf := make([]byte, 64)
			enc := &Encoder{}
			enc.Init(buf)
			enc.EncodeUniform(val, ft)
			encoded := enc.Done()

			dec := &Decoder{}
			dec.Init(encoded)
			decoded := dec.DecodeUniform(ft)

			if decoded != val {
				t.Errorf("Uniform val=%d ft=%d -> decoded=%d (bytes: %x)", val, ft, decoded, encoded)
			}
		}
	}
}

// TestEncodeDecodeMultipleBitsRoundTrip verifies multiple bits sequence round-trip.
func TestEncodeDecodeMultipleBitsRoundTrip(t *testing.T) {
	bits := []int{1, 0, 1, 1, 0, 0, 1, 0}
	buf := make([]byte, 64)
	enc := &Encoder{}
	enc.Init(buf)
	for _, b := range bits {
		enc.EncodeBit(b, 1)
	}
	encoded := enc.Done()

	dec := &Decoder{}
	dec.Init(encoded)
	for i, want := range bits {
		got := dec.DecodeBit(1)
		if got != want {
			t.Errorf("bit %d: got %d, want %d", i, got, want)
		}
	}
}

// TestEncodeDecodeMixedRoundTrip verifies mixed operations round-trip.
func TestEncodeDecodeMixedRoundTrip(t *testing.T) {
	// Test various combinations of operations
	t.Run("bit_then_icdf", func(t *testing.T) {
		icdf := []uint8{192, 128, 64, 0}
		buf := make([]byte, 64)
		enc := &Encoder{}
		enc.Init(buf)
		enc.EncodeBit(1, 2)
		enc.EncodeICDF(2, icdf, 8)
		encoded := enc.Done()

		dec := &Decoder{}
		dec.Init(encoded)
		bit := dec.DecodeBit(2)
		sym := dec.DecodeICDF(icdf, 8)

		if bit != 1 {
			t.Errorf("bit: got %d, want 1", bit)
		}
		if sym != 2 {
			t.Errorf("sym: got %d, want 2", sym)
		}
	})

	t.Run("icdf_then_bit", func(t *testing.T) {
		icdf := []uint8{192, 128, 64, 0}
		buf := make([]byte, 64)
		enc := &Encoder{}
		enc.Init(buf)
		enc.EncodeICDF(1, icdf, 8)
		enc.EncodeBit(0, 4)
		encoded := enc.Done()

		dec := &Decoder{}
		dec.Init(encoded)
		sym := dec.DecodeICDF(icdf, 8)
		bit := dec.DecodeBit(4)

		if sym != 1 {
			t.Errorf("sym: got %d, want 1", sym)
		}
		if bit != 0 {
			t.Errorf("bit: got %d, want 0", bit)
		}
	})

	t.Run("uniform_then_icdf", func(t *testing.T) {
		icdf := []uint8{192, 128, 64, 0}
		buf := make([]byte, 64)
		enc := &Encoder{}
		enc.Init(buf)
		enc.EncodeUniform(5, 16)
		enc.EncodeICDF(3, icdf, 8)
		encoded := enc.Done()

		dec := &Decoder{}
		dec.Init(encoded)
		val := dec.DecodeUniform(16)
		sym := dec.DecodeICDF(icdf, 8)

		if val != 5 {
			t.Errorf("uniform: got %d, want 5", val)
		}
		if sym != 3 {
			t.Errorf("sym: got %d, want 3", sym)
		}
	})
}

// TestEncodeDecodeRawBitsRoundTrip verifies raw bits round-trip.
func TestEncodeDecodeRawBitsRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		val  uint32
		bits uint
	}{
		{"4_bits", 0xA, 4},
		{"8_bits", 0xAB, 8},
		{"12_bits", 0xABC, 12},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, 64)
			enc := &Encoder{}
			enc.Init(buf)
			enc.EncodeBit(1, 2) // Encode something via range coder first
			enc.EncodeRawBits(tt.val, tt.bits)
			encoded := enc.Done()

			dec := &Decoder{}
			dec.Init(encoded)
			bit := dec.DecodeBit(2)
			raw := dec.DecodeRawBits(tt.bits)

			if bit != 1 {
				t.Errorf("bit: got %d, want 1", bit)
			}
			if raw != tt.val {
				t.Errorf("raw: got %#x, want %#x", raw, tt.val)
			}
		})
	}
}

// TestEncodeRawBitsProducesOutput verifies EncodeRawBits produces output in the buffer.
func TestEncodeRawBitsProducesOutput(t *testing.T) {
	tests := []struct {
		name string
		val  uint32
		bits uint
	}{
		{"1_bit", 1, 1},
		{"4_bits", 0xA, 4},
		{"8_bits", 0xAB, 8},
		{"12_bits", 0xABC, 12},
		{"16_bits", 0xABCD, 16},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, 256)
			enc := &Encoder{}
			enc.Init(buf)

			// Also encode some regular data to test mixing
			enc.EncodeBit(1, 1)
			enc.EncodeRawBits(tt.val, tt.bits)
			encoded := enc.Done()

			// Should produce non-empty output
			if len(encoded) == 0 {
				t.Errorf("empty encoded output for val=%#x, bits=%d", tt.val, tt.bits)
			}
		})
	}
}
