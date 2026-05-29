package testvectors

import "testing"

func TestQualityDelaySearchWindowKeepsShortFramesWideEnough(t *testing.T) {
	if got := qualityDelaySearchWindow(120); got != 240 {
		t.Fatalf("2.5 ms window mismatch: got %d want 240", got)
	}
	if got := qualityDelaySearchWindow(240); got != 240 {
		t.Fatalf("5 ms window mismatch: got %d want 240", got)
	}
	if got := qualityDelaySearchWindow(480); got != 480 {
		t.Fatalf("10 ms window mismatch: got %d want 480", got)
	}
}
