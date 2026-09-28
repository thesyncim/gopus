//go:build gopus_dred || gopus_osce

package silk

import "testing"

func TestDeepPLCSkipsRecoveryRampAt16k(t *testing.T) {
	for _, fsKHz := range []int32{8, 12, 16} {
		if got, want := deepPLCSkipsRecoveryRamp(fsKHz), fsKHz == 16; got != want {
			t.Errorf("deepPLCSkipsRecoveryRamp(%d)=%t, want %t", fsKHz, got, want)
		}
	}
}
