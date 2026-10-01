//go:build gopus_fixed_point

package fixedpoint

import (
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

func TestCELTCoarseEnergyVariableBudgetAllocs(t *testing.T) {
	const frameSize = 120
	enc := NewCELTEncoderRate(1, 48000)
	enc.SetComplexity(10)
	enc.SetVBR(false)
	enc.SetBitrate(opusBitrateMax)
	pcm := make([]int16, frameSize)
	for i := range pcm {
		pcm[i] = int16((i*7919+17)%65536 - 32768)
	}
	buffer := make([]byte, 1275)
	var coder rangecoding.Encoder
	budget := 32
	encode := func() {
		coder.Init(buffer[:budget])
		if n := enc.EncodeWithEC(pcm, frameSize, &coder, budget); n <= 0 || n > budget {
			panic("invalid fixed CELT output length")
		}
		budget += 8
	}
	encode()
	if allocs := testing.AllocsPerRun(20, encode); allocs != 0 {
		t.Fatalf("increasing fixed CELT packet budgets allocate %g times per frame", allocs)
	}
}
