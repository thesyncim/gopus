package encoder

import "testing"

func TestClampRedundancyBytesAfterSilkMatchesLibopusFormula(t *testing.T) {
	tests := []struct {
		name            string
		maxDataBytes    int32
		tellBits        int32
		redundancyBytes int32
		hybrid          bool
		want            int32
	}{
		{
			name:            "hybrid reserves length and celt guard",
			maxDataBytes:    40,
			tellBits:        100,
			redundancyBytes: 30,
			hybrid:          true,
			want:            25,
		},
		{
			name:            "silk packet has no length byte reserve",
			maxDataBytes:    40,
			tellBits:        100,
			redundancyBytes: 30,
			hybrid:          false,
			want:            26,
		},
		{
			name:            "minimum two bytes",
			maxDataBytes:    4,
			tellBits:        16,
			redundancyBytes: 10,
			hybrid:          true,
			want:            2,
		},
		{
			name:            "maximum 257 bytes",
			maxDataBytes:    400,
			tellBits:        0,
			redundancyBytes: 300,
			hybrid:          true,
			want:            257,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := clampRedundancyBytesAfterSilk(tc.maxDataBytes, tc.tellBits, tc.redundancyBytes, tc.hybrid)
			if got != tc.want {
				t.Fatalf("clampRedundancyBytesAfterSilk() = %d, want %d", got, tc.want)
			}
		})
	}
}
