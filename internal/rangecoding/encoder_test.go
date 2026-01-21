package rangecoding

import "testing"

// TestEncoderInit tests encoder initialization.
func TestEncoderInit(t *testing.T) {
	tests := []struct {
		name    string
		bufSize int
	}{
		{"small buffer", 16},
		{"medium buffer", 256},
		{"large buffer", 4096},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, tt.bufSize)
			enc := &Encoder{}
			enc.Init(buf)

			// Verify initial state
			if enc.rng != EC_CODE_TOP {
				t.Errorf("rng = %#x, want %#x", enc.rng, EC_CODE_TOP)
			}
			if enc.val != 0 {
				t.Errorf("val = %d, want 0", enc.val)
			}
			if enc.rem != -1 {
				t.Errorf("rem = %d, want -1", enc.rem)
			}
			if enc.offs != 0 {
				t.Errorf("offs = %d, want 0", enc.offs)
			}
			if enc.storage != uint32(tt.bufSize) {
				t.Errorf("storage = %d, want %d", enc.storage, tt.bufSize)
			}
		})
	}
}

// TestEncodeBit tests single bit encoding.
func TestEncodeBit(t *testing.T) {
	tests := []struct {
		name   string
		bits   []int
		logp   uint
		minLen int // Minimum expected output length
	}{
		{"single 0 bit", []int{0}, 1, 1},
		{"single 1 bit", []int{1}, 1, 1},
		{"alternating bits", []int{0, 1, 0, 1, 0, 1}, 1, 1},
		{"all zeros", []int{0, 0, 0, 0, 0, 0, 0, 0}, 1, 1},
		{"all ones", []int{1, 1, 1, 1, 1, 1, 1, 1}, 1, 1},
		{"logp=2", []int{0, 1, 0, 1}, 2, 1},
		{"logp=4", []int{0, 1, 0, 1}, 4, 1},
		{"logp=8", []int{0, 1, 0, 1}, 8, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, 256)
			enc := &Encoder{}
			enc.Init(buf)

			for _, bit := range tt.bits {
				enc.EncodeBit(bit, tt.logp)
			}

			result := enc.Done()
			if len(result) < tt.minLen {
				t.Errorf("output length = %d, want >= %d", len(result), tt.minLen)
			}
		})
	}
}

// TestEncodeBitDeterminism verifies that encoding the same sequence
// always produces the same output.
func TestEncodeBitDeterminism(t *testing.T) {
	bits := []int{1, 0, 1, 1, 0, 0, 1, 0, 1, 1, 1, 0, 0, 0, 1, 1}

	var results [][]byte
	for i := 0; i < 3; i++ {
		buf := make([]byte, 256)
		enc := &Encoder{}
		enc.Init(buf)

		for _, bit := range bits {
			enc.EncodeBit(bit, 1)
		}

		result := enc.Done()
		// Make a copy since Done returns a slice of the internal buffer
		resultCopy := make([]byte, len(result))
		copy(resultCopy, result)
		results = append(results, resultCopy)
	}

	// All results should be identical
	for i := 1; i < len(results); i++ {
		if len(results[i]) != len(results[0]) {
			t.Errorf("run %d: length %d, want %d", i, len(results[i]), len(results[0]))
			continue
		}
		for j := range results[0] {
			if results[i][j] != results[0][j] {
				t.Errorf("run %d: byte %d = %#x, want %#x", i, j, results[i][j], results[0][j])
			}
		}
	}
}

// TestEncodeICDF tests ICDF encoding.
func TestEncodeICDF(t *testing.T) {
	// Uniform distribution: 4 symbols, 8-bit precision
	// ICDF: [192, 128, 64, 0] means P(0)=P(1)=P(2)=P(3)=1/4
	uniformICDF := []uint8{192, 128, 64, 0}

	// Skewed distribution: symbol 0 is most likely
	// ICDF: [64, 32, 16, 0]
	skewedICDF := []uint8{64, 32, 16, 0}

	tests := []struct {
		name    string
		symbols []int
		icdf    []uint8
		ftb     uint
		minLen  int
	}{
		{"uniform single symbol 0", []int{0}, uniformICDF, 8, 1},
		{"uniform single symbol 3", []int{3}, uniformICDF, 8, 1},
		{"uniform all symbols", []int{0, 1, 2, 3}, uniformICDF, 8, 1},
		{"uniform repeated", []int{2, 2, 2, 2}, uniformICDF, 8, 1},
		{"skewed symbols", []int{0, 1, 2, 3}, skewedICDF, 8, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, 256)
			enc := &Encoder{}
			enc.Init(buf)

			for _, sym := range tt.symbols {
				enc.EncodeICDF(sym, tt.icdf, tt.ftb)
			}

			result := enc.Done()
			if len(result) < tt.minLen {
				t.Errorf("output length = %d, want >= %d", len(result), tt.minLen)
			}
		})
	}
}

// TestEncoderTell verifies bit counting.
func TestEncoderTell(t *testing.T) {
	buf := make([]byte, 256)
	enc := &Encoder{}
	enc.Init(buf)

	initialTell := enc.Tell()

	// Encode some bits
	for i := 0; i < 8; i++ {
		enc.EncodeBit(i%2, 1)
	}

	afterTell := enc.Tell()

	// Tell should increase after encoding
	if afterTell <= initialTell {
		t.Errorf("Tell did not increase: initial=%d, after=%d", initialTell, afterTell)
	}

	// TellFrac should be roughly 8x Tell
	tellFrac := enc.TellFrac()
	tellWhole := enc.Tell()
	// Allow some variation due to rounding
	if tellFrac < (tellWhole-1)*8 || tellFrac > (tellWhole+1)*8 {
		t.Errorf("TellFrac=%d not close to Tell*8=%d", tellFrac, tellWhole*8)
	}
}

// TestEncoderTellFrac verifies fractional bit counting.
func TestEncoderTellFrac(t *testing.T) {
	buf := make([]byte, 256)
	enc := &Encoder{}
	enc.Init(buf)

	// Initial TellFrac should be reasonable
	initialFrac := enc.TellFrac()
	if initialFrac < 0 {
		t.Errorf("initial TellFrac = %d, want >= 0", initialFrac)
	}

	// Encode with different probabilities
	enc.EncodeBit(0, 8) // Very likely 0
	frac1 := enc.TellFrac()

	enc.EncodeBit(1, 1) // 50% probability
	frac2 := enc.TellFrac()

	// TellFrac should increase
	if frac2 <= frac1 {
		t.Errorf("TellFrac did not increase: %d -> %d", frac1, frac2)
	}
}

// TestEncoderDone verifies finalization.
func TestEncoderDone(t *testing.T) {
	buf := make([]byte, 256)
	enc := &Encoder{}
	enc.Init(buf)

	// Encode something
	enc.EncodeBit(1, 1)
	enc.EncodeBit(0, 1)

	result := enc.Done()

	// Result should be non-nil and non-empty
	if result == nil {
		t.Error("Done returned nil")
	}
	if len(result) == 0 {
		t.Error("Done returned empty slice")
	}

	// Result should be a slice of the original buffer
	if &result[0] != &buf[0] {
		t.Error("Done returned different buffer")
	}
}

// TestEncoderDoneMultipleCalls verifies that Done can be called multiple times.
func TestEncoderDoneMultipleCalls(t *testing.T) {
	buf := make([]byte, 256)
	enc := &Encoder{}
	enc.Init(buf)

	enc.EncodeBit(1, 1)

	result1 := enc.Done()
	result2 := enc.Done()

	// Second call should return some result (behavior depends on implementation)
	// Main requirement is it shouldn't panic
	_ = result1
	_ = result2
}

// TestEncoderEncode tests direct Encode method.
func TestEncoderEncode(t *testing.T) {
	tests := []struct {
		name    string
		fl, fh  uint32 // Cumulative frequencies
		ft      uint32 // Total
		minLen  int
	}{
		{"first symbol", 0, 64, 256, 1},
		{"middle symbol", 64, 128, 256, 1},
		{"last symbol", 192, 256, 256, 1},
		{"narrow range", 100, 101, 256, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, 256)
			enc := &Encoder{}
			enc.Init(buf)

			enc.Encode(tt.fl, tt.fh, tt.ft)

			result := enc.Done()
			if len(result) < tt.minLen {
				t.Errorf("output length = %d, want >= %d", len(result), tt.minLen)
			}
		})
	}
}

// TestEncoderRangeInvariant verifies range stays above EC_CODE_BOT after normalize.
func TestEncoderRangeInvariant(t *testing.T) {
	buf := make([]byte, 1024)
	enc := &Encoder{}
	enc.Init(buf)

	// Encode many symbols to stress test
	for i := 0; i < 100; i++ {
		enc.EncodeBit(i%2, uint(1+(i%8)))
		// After normalize, rng should be > EC_CODE_BOT
		if enc.rng <= EC_CODE_BOT {
			t.Errorf("after iteration %d: rng=%#x <= EC_CODE_BOT=%#x", i, enc.rng, EC_CODE_BOT)
		}
	}
}

// TestEncoderAccessors tests Range and Val accessor methods.
func TestEncoderAccessors(t *testing.T) {
	buf := make([]byte, 256)
	enc := &Encoder{}
	enc.Init(buf)

	// After init, Range should be EC_CODE_TOP
	if enc.Range() != EC_CODE_TOP {
		t.Errorf("Range() = %#x, want %#x", enc.Range(), EC_CODE_TOP)
	}

	// After init, Val should be 0
	if enc.Val() != 0 {
		t.Errorf("Val() = %d, want 0", enc.Val())
	}

	// After encoding, values should change
	enc.EncodeBit(1, 1)
	// Range should still be valid (> EC_CODE_BOT)
	if enc.Range() <= EC_CODE_BOT {
		t.Errorf("Range() = %#x <= EC_CODE_BOT after encode", enc.Range())
	}
}
