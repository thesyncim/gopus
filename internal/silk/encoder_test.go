package silk

import "testing"

func TestNewEncoder(t *testing.T) {
	tests := []struct {
		name           string
		bandwidth      Bandwidth
		wantLPCOrder   int
		wantSampleRate int
	}{
		{"narrowband", BandwidthNarrowband, 10, 8000},
		{"mediumband", BandwidthMediumband, 10, 12000},
		{"wideband", BandwidthWideband, 16, 16000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc := NewEncoder(tt.bandwidth)

			if int(enc.lpcOrder) != tt.wantLPCOrder {
				t.Errorf("lpcOrder = %d, want %d", enc.lpcOrder, tt.wantLPCOrder)
			}
			if enc.SampleRate() != tt.wantSampleRate {
				t.Errorf("SampleRate() = %d, want %d", enc.SampleRate(), tt.wantSampleRate)
			}
			if enc.Bandwidth() != tt.bandwidth {
				t.Errorf("Bandwidth() = %v, want %v", enc.Bandwidth(), tt.bandwidth)
			}
			if len(enc.prevLSFQ15) != tt.wantLPCOrder {
				t.Errorf("prevLSFQ15 length = %d, want %d", len(enc.prevLSFQ15), tt.wantLPCOrder)
			}
			// silk_init_encoder followed by the first silk_setup_fs
			// (silk/control_codec.c) leaves these non-zero values.
			if enc.pitchState.prevLag != 100 {
				t.Errorf("prevLag = %d, want 100", enc.pitchState.prevLag)
			}
			if enc.nsqState.lagPrev != 100 {
				t.Errorf("sNSQ.lagPrev = %d, want 100", enc.nsqState.lagPrev)
			}
			if enc.nsqState.prevGainQ16 != 1<<16 {
				t.Errorf("sNSQ.prev_gain_Q16 = %d, want %d", enc.nsqState.prevGainQ16, 1<<16)
			}
			if enc.previousGainIndex != 10 {
				t.Errorf("LastGainIndex = %d, want 10", enc.previousGainIndex)
			}
			if !enc.firstFrameAfterReset {
				t.Error("first_frame_after_reset should be set")
			}
		})
	}
}
