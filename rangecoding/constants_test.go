package rangecoding

import "testing"

// TestConstants verifies that all range coder constants match the expected values
// from RFC 6716 Section 4.1 and libopus celt/mfrngcod.h.
func TestConstants(t *testing.T) {
	tests := []struct {
		name     string
		got      uint32
		expected uint32
	}{
		{"EC_SYM_BITS", EC_SYM_BITS, 8},
		{"EC_CODE_BITS", EC_CODE_BITS, 32},
		{"EC_SYM_MAX", EC_SYM_MAX, 255},
		{"EC_CODE_TOP", EC_CODE_TOP, 0x80000000},
		{"EC_CODE_BOT", EC_CODE_BOT, 0x00800000},
		{"EC_CODE_SHIFT", EC_CODE_SHIFT, 23},
		{"EC_CODE_EXTRA", EC_CODE_EXTRA, 7},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.expected {
				t.Errorf("%s = 0x%X, want 0x%X", tc.name, tc.got, tc.expected)
			}
		})
	}
}
