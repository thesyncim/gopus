package testvectors

import (
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func TestLaplaceRoundTrip(t *testing.T) {
	// Test values to encode
	testValues := []int{0, 1, -1, 2, -2, 3, -3, 5, -5, 10, -10}
	fs := 32768 >> 1 // typical fs value
	decay := 16384   // typical decay value

	for _, val := range testValues {
		// Encode
		buf := make([]byte, 256)
		re := &rangecoding.Encoder{}
		re.Init(buf)

		enc := celt.NewEncoder(1)
		enc.SetRangeEncoder(re)

		// Use the exported test helper if available, otherwise we need to test via energy encoding
		// For now, let's test via the full coarse energy path with a single band
		encodedVal := testEncodeLaplace(enc, val, fs, decay)
		t.Logf("Encoded val=%d, got=%d", val, encodedVal)

		data := re.Done()
		t.Logf("  Encoded bytes: %d", len(data))

		// Decode
		rd := &rangecoding.Decoder{}
		rd.Init(data)

		dec := celt.NewDecoder(1)
		dec.SetRangeDecoder(rd)

		decodedVal := testDecodeLaplace(dec, fs, decay)
		t.Logf("  Decoded val=%d", decodedVal)

		if encodedVal != decodedVal {
			t.Errorf("Laplace round-trip failed: encoded %d (from %d), decoded %d", encodedVal, val, decodedVal)
		}
	}
}

// testEncodeLaplace calls the encoder's private encodeLaplace via reflection or direct access
func testEncodeLaplace(enc *celt.Encoder, val, fs, decay int) int {
	// We can't call private methods directly, so let's expose them via public test helpers
	// For now, return val as a placeholder
	return enc.TestEncodeLaplace(val, fs, decay)
}

func testDecodeLaplace(dec *celt.Decoder, fs, decay int) int {
	return dec.TestDecodeLaplace(fs, decay)
}
