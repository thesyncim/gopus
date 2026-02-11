package rangecoding

import (
	"math/rand"
	"testing"
)

// TestDecoderInit tests decoder initialization with various inputs.
func TestDecoderInit(t *testing.T) {
	tests := []struct {
		name    string
		buf     []byte
		wantRng bool // true if rng should be > EC_CODE_BOT
	}{
		{
			name:    "empty buffer",
			buf:     []byte{},
			wantRng: true,
		},
		{
			name:    "single byte",
			buf:     []byte{0x00},
			wantRng: true,
		},
		{
			name:    "single byte 0xFF",
			buf:     []byte{0xFF},
			wantRng: true,
		},
		{
			name:    "multiple bytes",
			buf:     []byte{0x12, 0x34, 0x56, 0x78},
			wantRng: true,
		},
		{
			name:    "all zeros",
			buf:     []byte{0x00, 0x00, 0x00, 0x00},
			wantRng: true,
		},
		{
			name:    "all ones",
			buf:     []byte{0xFF, 0xFF, 0xFF, 0xFF},
			wantRng: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var d Decoder
			// Should not panic
			d.Init(tc.buf)

			// After normalize, rng must be > EC_CODE_BOT
			if tc.wantRng && d.rng <= EC_CODE_BOT {
				t.Errorf("rng = 0x%X, want > 0x%X (EC_CODE_BOT)", d.rng, EC_CODE_BOT)
			}

			// Error flag should be clear
			if d.Error() != 0 {
				t.Errorf("error flag = %d, want 0", d.Error())
			}
		})
	}
}

// TestDecodeBit tests single bit decoding with various log probabilities.
func TestDecodeBit(t *testing.T) {
	// Test with a known byte sequence
	// With logp=1, P(0) = 0.5, P(1) = 0.5 (equal probability)
	// The actual decoded value depends on the input bytes and range state

	tests := []struct {
		name string
		buf  []byte
		logp uint
	}{
		{
			name: "logp=1 (50/50)",
			buf:  []byte{0x00, 0x00, 0x00, 0x00},
			logp: 1,
		},
		{
			name: "logp=2 (75/25)",
			buf:  []byte{0x80, 0x00, 0x00, 0x00},
			logp: 2,
		},
		{
			name: "logp=8 (high probability 0)",
			buf:  []byte{0xFF, 0xFF, 0xFF, 0xFF},
			logp: 8,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var d Decoder
			d.Init(tc.buf)

			initialTell := d.Tell()

			// Decode a bit
			bit := d.DecodeBit(tc.logp)

			// Bit must be 0 or 1
			if bit != 0 && bit != 1 {
				t.Errorf("DecodeBit returned %d, want 0 or 1", bit)
			}

			// Tell should increase after decoding
			if d.Tell() <= initialTell {
				t.Errorf("Tell() = %d, should be > %d after decode", d.Tell(), initialTell)
			}

			// Range invariant must hold
			if d.rng <= EC_CODE_BOT {
				t.Errorf("rng = 0x%X after decode, want > 0x%X", d.rng, EC_CODE_BOT)
			}
		})
	}
}

// TestDecodeICDF tests ICDF-based symbol decoding.
func TestDecodeICDF(t *testing.T) {
	// ICDF tables have decreasing values from 256 (or 2^ftb) down to 0
	// For a 2-symbol alphabet with uniform distribution: [128, 0]
	// For a 4-symbol alphabet with uniform distribution: [192, 128, 64, 0]

	tests := []struct {
		name string
		buf  []byte
		icdf []uint8
		ftb  uint
	}{
		{
			name: "2-symbol uniform",
			buf:  []byte{0x00, 0x00, 0x00, 0x00},
			icdf: []uint8{128, 0}, // P(0) = 0.5, P(1) = 0.5
			ftb:  8,
		},
		{
			name: "4-symbol uniform",
			buf:  []byte{0x80, 0x00, 0x00, 0x00},
			icdf: []uint8{192, 128, 64, 0}, // Equal probability for each
			ftb:  8,
		},
		{
			name: "skewed distribution",
			buf:  []byte{0xFF, 0xFF, 0xFF, 0xFF},
			icdf: []uint8{240, 128, 16, 0}, // Heavily skewed
			ftb:  8,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var d Decoder
			d.Init(tc.buf)

			initialTell := d.Tell()

			// Decode a symbol
			sym := d.DecodeICDF(tc.icdf, tc.ftb)

			// Symbol must be valid index
			if sym < 0 || sym >= len(tc.icdf) {
				t.Errorf("DecodeICDF returned %d, want 0..%d", sym, len(tc.icdf)-1)
			}

			// Tell should increase
			if d.Tell() <= initialTell {
				t.Errorf("Tell() = %d, should be > %d after decode", d.Tell(), initialTell)
			}

			// Range invariant
			if d.rng <= EC_CODE_BOT {
				t.Errorf("rng = 0x%X after decode, want > 0x%X", d.rng, EC_CODE_BOT)
			}
		})
	}
}

func TestDecodeICDF2MatchesDecodeICDF(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for tc := 0; tc < 200; tc++ {
		buf := make([]byte, 64)
		for i := range buf {
			buf[i] = byte(r.Uint32())
		}
		icdf0 := uint8(r.Intn(255) + 1)
		icdf := [2]uint8{icdf0, 0}

		var d1, d2 Decoder
		d1.Init(buf)
		d2.Init(buf)

		for i := 0; i < 128; i++ {
			sym1 := d1.DecodeICDF(icdf[:], 8)
			sym2 := d2.DecodeICDF2(icdf0, 8)
			if sym1 != sym2 {
				t.Fatalf("symbol mismatch tc=%d i=%d: generic=%d fast=%d", tc, i, sym1, sym2)
			}
			if d1.rng != d2.rng || d1.val != d2.val {
				t.Fatalf("state mismatch tc=%d i=%d: generic(rng=%d,val=%d) fast(rng=%d,val=%d)", tc, i, d1.rng, d1.val, d2.rng, d2.val)
			}
		}
	}
}

// TestTell tests bit consumption tracking.
func TestTell(t *testing.T) {
	var d Decoder
	buf := []byte{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0}
	d.Init(buf)

	// Initial Tell should be positive (some bits consumed during init)
	initialTell := d.Tell()
	if initialTell < 0 {
		t.Errorf("Tell() = %d after init, want >= 0", initialTell)
	}

	// Decode some bits and verify Tell increases
	prevTell := initialTell
	for i := 0; i < 5; i++ {
		d.DecodeBit(1)
		currentTell := d.Tell()
		if currentTell <= prevTell {
			t.Errorf("Tell() = %d after decode %d, want > %d", currentTell, i+1, prevTell)
		}
		prevTell = currentTell
	}
}

// TestTellFrac tests fractional bit precision.
func TestTellFrac(t *testing.T) {
	var d Decoder
	buf := []byte{0x12, 0x34, 0x56, 0x78}
	d.Init(buf)

	// TellFrac returns 1/8 bit precision
	frac := d.TellFrac()
	tell := d.Tell()

	// TellFrac / 8 should be close to Tell (within rounding)
	// TellFrac gives more precision, so TellFrac/8 <= Tell typically
	if frac < 0 {
		t.Errorf("TellFrac() = %d, want >= 0", frac)
	}

	// Rough sanity check: frac/8 should be in same ballpark as tell
	fracApprox := frac / 8
	if fracApprox < tell-2 || fracApprox > tell+2 {
		t.Errorf("TellFrac()/8 = %d, Tell() = %d, expect close values", fracApprox, tell)
	}
}

// TestDecoderSequence tests decoding a sequence of symbols.
func TestDecoderSequence(t *testing.T) {
	// Test decoding multiple symbols in sequence
	buf := []byte{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0}

	var d Decoder
	d.Init(buf)

	// Decode a sequence of bits and ICDF symbols
	icdf := []uint8{128, 0} // 2-symbol uniform

	decoded := make([]int, 0)
	for i := 0; i < 10; i++ {
		if i%2 == 0 {
			// Decode bit
			bit := d.DecodeBit(1)
			decoded = append(decoded, bit)
		} else {
			// Decode ICDF symbol
			sym := d.DecodeICDF(icdf, 8)
			decoded = append(decoded, sym)
		}
	}

	// Verify we decoded the expected number of symbols
	if len(decoded) != 10 {
		t.Errorf("decoded %d symbols, want 10", len(decoded))
	}

	// Verify all decoded values are valid
	for i, v := range decoded {
		if v < 0 || v > 1 {
			t.Errorf("decoded[%d] = %d, want 0 or 1", i, v)
		}
	}

	// Range invariant should still hold
	if d.rng <= EC_CODE_BOT {
		t.Errorf("rng = 0x%X after sequence, want > 0x%X", d.rng, EC_CODE_BOT)
	}
}

// TestDecoderDeterminism verifies that decoding is deterministic.
func TestDecoderDeterminism(t *testing.T) {
	buf := []byte{0xAB, 0xCD, 0xEF, 0x12, 0x34, 0x56, 0x78, 0x9A}
	icdf := []uint8{200, 128, 64, 0}

	// Decode sequence twice with same input
	decode := func() []int {
		var d Decoder
		d.Init(buf)
		result := make([]int, 10)
		for i := range result {
			result[i] = d.DecodeICDF(icdf, 8)
		}
		return result
	}

	seq1 := decode()
	seq2 := decode()

	// Must produce identical results
	for i := range seq1 {
		if seq1[i] != seq2[i] {
			t.Errorf("Non-deterministic: seq1[%d]=%d, seq2[%d]=%d", i, seq1[i], i, seq2[i])
		}
	}
}

// TestIlog tests the integer log function.
func TestIlog(t *testing.T) {
	tests := []struct {
		x    uint32
		want int
	}{
		{0, 0},
		{1, 1},
		{2, 2},
		{3, 2},
		{4, 3},
		{7, 3},
		{8, 4},
		{15, 4},
		{16, 5},
		{255, 8},
		{256, 9},
		{0x7FFFFFFF, 31},
		{0x80000000, 32},
		{0xFFFFFFFF, 32},
	}

	for _, tc := range tests {
		t.Run("", func(t *testing.T) {
			got := ilog(tc.x)
			if got != tc.want {
				t.Errorf("ilog(0x%X) = %d, want %d", tc.x, got, tc.want)
			}
		})
	}
}

// TestBytesUsed tests tracking of bytes consumed.
func TestBytesUsed(t *testing.T) {
	buf := []byte{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0}

	var d Decoder
	d.Init(buf)

	// After init, some bytes should be consumed
	initialUsed := d.BytesUsed()
	if initialUsed < 0 || initialUsed > len(buf) {
		t.Errorf("BytesUsed() = %d after init, want 0..%d", initialUsed, len(buf))
	}

	// Decode several symbols to consume more bytes
	for i := 0; i < 20; i++ {
		d.DecodeBit(1)
	}

	// Should have consumed more bytes (or at least not fewer)
	if d.BytesUsed() < initialUsed {
		t.Errorf("BytesUsed() decreased from %d to %d", initialUsed, d.BytesUsed())
	}
}
