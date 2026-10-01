//go:build !gopus_dred && !gopus_osce

package silk

import "testing"

func TestDeepPLCDoesNotSkipRecoveryRampWithoutFeature(t *testing.T) {
	for _, fsKHz := range []int32{8, 12, 16} {
		if deepPLCSkipsRecoveryRamp(fsKHz) {
			t.Errorf("deepPLCSkipsRecoveryRamp(%d)=true without deep PLC", fsKHz)
		}
	}
}
