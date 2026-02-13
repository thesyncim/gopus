package encoder

import "testing"

func TestComputeEquivRate_UnknownModeLossPenaltyMatchesLibopus(t *testing.T) {
	enc := NewEncoder(48000, 2)

	got := enc.computeEquivRate(64000, 2, 50, true, ModeAuto, 10, 20)
	want := 64000 - (64000*20)/(12*20+20)
	if got != want {
		t.Fatalf("unknown-mode equivRate=%d, want %d", got, want)
	}

	gotNoLoss := enc.computeEquivRate(64000, 2, 50, true, ModeAuto, 10, 0)
	if gotNoLoss != 64000 {
		t.Fatalf("unknown-mode no-loss equivRate=%d, want 64000", gotNoLoss)
	}

	silk := enc.computeEquivRate(64000, 2, 50, true, ModeSILK, 10, 20)
	if got <= silk {
		t.Fatalf("unknown-mode should apply weaker loss penalty: unknown=%d silk=%d", got, silk)
	}
}
