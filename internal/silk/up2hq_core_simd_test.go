//go:build (arm64 || amd64) && goexperiment.simd && !nosimd && !purego

package silk

import (
	"math/rand"
	"testing"
)

func TestUp2HQCoreSIMDBitExact(t *testing.T) {
	rng := rand.New(rand.NewSource(0x511c))
	for _, n := range []int{0, 1, 7, 16, 160, 320, 480} {
		in := make([]int16, n)
		for i := range in {
			switch rng.Intn(8) {
			case 0:
				in[i] = -32768
			case 1:
				in[i] = 32767
			default:
				in[i] = int16(rng.Intn(65536) - 32768)
			}
		}
		var wantState, gotState [6]int32
		for i := range wantState {
			v := int32(rng.Intn(1<<26) - (1 << 25))
			wantState[i] = v
			gotState[i] = v
		}
		want := make([]int16, 2*n)
		got := make([]int16, 2*n)
		up2HQCoreGo(want, in, &wantState)
		up2HQCore(got, in, &gotState)
		if gotState != wantState {
			t.Fatalf("n=%d: state mismatch got=%v want=%v", n, gotState, wantState)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("n=%d out[%d]: got=%d want=%d", n, i, got[i], want[i])
			}
		}
	}
}
