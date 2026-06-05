package types

import "testing"

// TestModeValues locks the Mode enumeration's value mapping and ordering.
//
// Mode is stored as a small iota-based uint8 (it never crosses the entropy-coded
// wire), so the exact integers are an implementation choice — but the ordering
// SILK < Hybrid < CELT and the distinctness of the three values are relied on by
// the codec layers that import this package. A reordering here would silently
// remap every TOC mode decision.
func TestModeValues(t *testing.T) {
	cases := []struct {
		mode Mode
		want uint8
		name string
	}{
		{ModeSILK, 0, "ModeSILK"},
		{ModeHybrid, 1, "ModeHybrid"},
		{ModeCELT, 2, "ModeCELT"},
	}
	for _, tc := range cases {
		if got := uint8(tc.mode); got != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, got, tc.want)
		}
	}

	// The three modes must be pairwise distinct.
	all := []Mode{ModeSILK, ModeHybrid, ModeCELT}
	seen := map[Mode]bool{}
	for _, m := range all {
		if seen[m] {
			t.Errorf("duplicate Mode value %d", m)
		}
		seen[m] = true
	}
}

// TestBandwidthValues locks the Bandwidth enumeration's value mapping and
// ascending order. The values mirror the OPUS_BANDWIDTH_* ordering (narrowband
// is the lowest, fullband the highest); codec layers compare bandwidths with <
// and >, so the monotonic ordering is load-bearing.
func TestBandwidthValues(t *testing.T) {
	cases := []struct {
		bw   Bandwidth
		want uint8
		name string
	}{
		{BandwidthNarrowband, 0, "BandwidthNarrowband"},
		{BandwidthMediumband, 1, "BandwidthMediumband"},
		{BandwidthWideband, 2, "BandwidthWideband"},
		{BandwidthSuperwideband, 3, "BandwidthSuperwideband"},
		{BandwidthFullband, 4, "BandwidthFullband"},
	}
	for _, tc := range cases {
		if got := uint8(tc.bw); got != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, got, tc.want)
		}
	}

	// Ascending, strictly increasing order (narrowband..fullband).
	ordered := []Bandwidth{
		BandwidthNarrowband,
		BandwidthMediumband,
		BandwidthWideband,
		BandwidthSuperwideband,
		BandwidthFullband,
	}
	for i := 1; i < len(ordered); i++ {
		if !(ordered[i-1] < ordered[i]) {
			t.Errorf("Bandwidth not strictly increasing at index %d: %d !< %d",
				i, ordered[i-1], ordered[i])
		}
	}
}

// TestSignalValues locks the Signal constants to the exact libopus ABI integers.
// Unlike Mode and Bandwidth, these values are part of the public encoder control
// ABI (OPUS_AUTO / OPUS_SIGNAL_VOICE / OPUS_SIGNAL_MUSIC from
// include/opus_defines.h) and are compared against directly, so the magic numbers
// must not drift.
func TestSignalValues(t *testing.T) {
	cases := []struct {
		signal Signal
		want   int
		name   string
	}{
		{SignalAuto, -1000, "SignalAuto"},
		{SignalVoice, 3001, "SignalVoice"},
		{SignalMusic, 3002, "SignalMusic"},
	}
	for _, tc := range cases {
		if got := int(tc.signal); got != tc.want {
			t.Errorf("%s = %d, want %d (libopus ABI value)", tc.name, got, tc.want)
		}
	}

	// The three signal hints must be pairwise distinct.
	all := []Signal{SignalAuto, SignalVoice, SignalMusic}
	seen := map[Signal]bool{}
	for _, s := range all {
		if seen[s] {
			t.Errorf("duplicate Signal value %d", s)
		}
		seen[s] = true
	}
}
