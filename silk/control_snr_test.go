package silk

import "testing"

func TestControlSNRMatchesLibopusTables(t *testing.T) {
	tests := []struct {
		name        string
		bandwidth   Bandwidth
		targetRate  int
		nbSubfr     int
		expectedQ7  int
	}{
		{"NB-8k-20ms", BandwidthNarrowband, 8000, 4, 1932},
		{"NB-12k-20ms", BandwidthNarrowband, 12000, 4, 2562},
		{"NB-20k-20ms", BandwidthNarrowband, 20000, 4, 3402},
		{"MB-16k-20ms", BandwidthMediumband, 16000, 4, 2625},
		{"WB-32k-20ms", BandwidthWideband, 32000, 4, 3381},
		{"WB-32k-10ms", BandwidthWideband, 32000, 2, 3297},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(tc.bandwidth)
			enc.controlSNR(tc.targetRate, tc.nbSubfr)
			if enc.snrDBQ7 != tc.expectedQ7 {
				t.Fatalf("snrDBQ7=%d, want %d", enc.snrDBQ7, tc.expectedQ7)
			}
		})
	}
}
