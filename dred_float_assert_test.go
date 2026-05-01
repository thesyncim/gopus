//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package gopus

import (
	"math"
	"testing"
)

func assertFloat32BitsEqual(t *testing.T, got, want []float32, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s[%d]=%g want %g", label, i, got[i], want[i])
		}
	}
}
