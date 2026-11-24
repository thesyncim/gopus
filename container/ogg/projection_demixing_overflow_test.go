package ogg

import "testing"

func TestIdentityDemixingMatrixNoUint8Wrap(t *testing.T) {
	// streams + coupled = 300 > 255; must not wrap uint8 to 44.
	got := identityDemixingMatrix(2, 200, 100)
	want := expectedDemixingMatrixSize(2, 200, 100)
	if len(got) != want {
		t.Fatalf("identityDemixingMatrix(2, 200, 100) len = %d, want %d", len(got), want)
	}
}
