package encoder

import (
	"math"
	"testing"
)

func TestGetVADActivityUsesAvailableOpusDecision(t *testing.T) {
	enc := NewEncoder(48000, 1)
	if got := enc.GetVADActivity(); got != 0 {
		t.Fatalf("initial GetVADActivity()=%d, want 0", got)
	}

	tests := []struct {
		name     string
		observed bool
		valid    bool
		prob     float32
		want     int
	}{
		{name: "not observed", valid: true, prob: 0.5, want: 0},
		{name: "unavailable decision", observed: true, prob: 0.5, want: 0},
		{name: "zero activity", observed: true, valid: true, prob: 0, want: 0},
		{name: "fractional Q8", observed: true, valid: true, prob: 0.1, want: 25},
		{name: "half scale", observed: true, valid: true, prob: 0.5, want: 128},
		{name: "one saturates", observed: true, valid: true, prob: 1, want: 255},
		{name: "above one saturates", observed: true, valid: true, prob: 1.5, want: 255},
		{name: "negative clamps", observed: true, valid: true, prob: -0.5, want: 0},
		{name: "positive infinity saturates", observed: true, valid: true, prob: float32(math.Inf(1)), want: 255},
		{name: "NaN clamps", observed: true, valid: true, prob: float32(math.NaN()), want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc.lastOpusVADActivityObserved = tt.observed
			enc.lastOpusVADValid = tt.valid
			enc.lastOpusVADProb = tt.prob
			enc.lastOpusVADActive = tt.prob >= 0.1
			beforeValid := enc.lastOpusVADValid
			beforeActive := enc.lastOpusVADActive
			beforeProb := math.Float32bits(enc.lastOpusVADProb)
			if got := enc.GetVADActivity(); got != tt.want {
				t.Fatalf("GetVADActivity()=%d, want %d", got, tt.want)
			}
			if got := enc.GetVADActivity(); got < 0 || got > 255 {
				t.Fatalf("GetVADActivity()=%d outside Q8 range", got)
			}
			if enc.lastOpusVADValid != beforeValid || enc.lastOpusVADActive != beforeActive || math.Float32bits(enc.lastOpusVADProb) != beforeProb {
				t.Fatal("GetVADActivity mutated the encoder decision state")
			}
		})
	}

	enc.lastOpusVADActivityObserved = true
	enc.lastOpusVADValid = true
	enc.lastOpusVADActive = true
	enc.lastOpusVADProb = 0.75
	enc.Reset()
	if got := enc.GetVADActivity(); got != 0 {
		t.Fatalf("GetVADActivity() after Reset=%d, want 0", got)
	}
	if !enc.lastOpusVADValid || !enc.lastOpusVADActive || enc.lastOpusVADProb != 0.75 {
		t.Fatal("getter availability reset changed the encoder decision state")
	}

	enc.lastOpusVADActivityObserved = true
	enc.lastOpusVADValid = true
	enc.lastOpusVADProb = 0.5
	if n := testing.AllocsPerRun(100, func() { _ = enc.GetVADActivity() }); n != 0 {
		t.Fatalf("GetVADActivity allocations=%v, want 0", n)
	}
}
