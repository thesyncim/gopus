//go:build gopus_qext

package celt

import "testing"

// TestEncoderEnableHD96kMode verifies that EnableHD96kMode reconfigures the
// encoder analysis state for the native 96 kHz HD mode (overlap 240, HD
// pre-emphasis, Fs=96000) and grows the overlap history, mirroring the
// decoder's EnableHD96kMode. The 48 kHz analysis overlap is unaffected before
// the mode is enabled.
func TestEncoderEnableHD96kMode(t *testing.T) {
	for _, ch := range []int{1, 2} {
		e := NewEncoder(ch)
		if e.HD96kEncodeEnabled() {
			t.Fatalf("ch=%d: HD96k unexpectedly enabled on fresh encoder", ch)
		}
		if got := e.analysisOverlap(); got != Overlap {
			t.Fatalf("ch=%d: 48k analysis overlap = %d, want %d", ch, got, Overlap)
		}

		e.EnableHD96kMode()
		if !e.HD96kEncodeEnabled() {
			t.Fatalf("ch=%d: HD96k not enabled after EnableHD96kMode", ch)
		}
		m := NewHD96kMode()
		if got := e.analysisOverlap(); got != m.Overlap {
			t.Fatalf("ch=%d: HD analysis overlap = %d, want %d", ch, got, m.Overlap)
		}
		if e.sampleRate != int32(m.Fs) {
			t.Fatalf("ch=%d: sampleRate = %d, want %d", ch, e.sampleRate, m.Fs)
		}
		if e.hd96kPreemph != m.Preemph {
			t.Fatalf("ch=%d: HD preemph = %v, want %v", ch, e.hd96kPreemph, m.Preemph)
		}
		if len(e.overlapBuffer) < m.Overlap*ch {
			t.Fatalf("ch=%d: overlap history len = %d, want >= %d", ch, len(e.overlapBuffer), m.Overlap*ch)
		}

		// Idempotent: a second call must not change state.
		e.EnableHD96kMode()
		if !e.HD96kEncodeEnabled() || e.analysisOverlap() != m.Overlap {
			t.Fatalf("ch=%d: EnableHD96kMode not idempotent", ch)
		}
	}
}
