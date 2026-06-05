package main

import (
	"testing"

	"github.com/thesyncim/gopus"
)

// TestLowDelayUsesCELT verifies that the low-delay profile encodes CELT-only
// packets at each short frame size and decodes cleanly.
func TestLowDelayUsesCELT(t *testing.T) {
	for _, frame := range []int{480, 240, 120} { // 10 ms, 5 ms, 2.5 ms
		t.Run(frameLabel(frame), func(t *testing.T) {
			lookahead, mode, err := roundtripLowDelay(frame)
			if err != nil {
				t.Fatalf("roundtripLowDelay: %v", err)
			}
			if mode != gopus.ModeCELT {
				t.Fatalf("decoded mode = %s, want CELT", modeName(mode))
			}
			if lookahead <= 0 {
				t.Fatalf("lookahead = %d, want > 0", lookahead)
			}
		})
	}
}

// TestLowDelayBeatsDefaultLookahead confirms the low-delay profile reports a
// smaller algorithmic delay than the default audio profile.
func TestLowDelayBeatsDefaultLookahead(t *testing.T) {
	def, err := newEncoder(gopus.ApplicationAudio, 960)
	if err != nil {
		t.Fatalf("default encoder: %v", err)
	}
	ld, err := newEncoder(gopus.ApplicationLowDelay, 240)
	if err != nil {
		t.Fatalf("low-delay encoder: %v", err)
	}
	if ld.Lookahead() >= def.Lookahead() {
		t.Fatalf("low-delay lookahead %d not less than default %d", ld.Lookahead(), def.Lookahead())
	}
}
