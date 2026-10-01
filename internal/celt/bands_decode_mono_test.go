package celt

import (
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestQuantAllBandsDecodeMonoMatchesGenericLoop decodes random mono band data
// through the mono band loop and through the generic loop, which runs when an
// extension decoder is present without extension budgets, and requires the
// same coefficients, collapse masks, seed and range decoder state.
func TestQuantAllBandsDecodeMonoMatchesGenericLoop(t *testing.T) {
	rng := rand.New(rand.NewSource(0xba4d))
	buf := make([]byte, 400)
	var dummy rangecoding.Decoder
	dummy.Init([]byte{0})
	for iter := range 1500 {
		lm := rng.Intn(4)
		frameSize := 120 << lm
		start := 0
		if rng.Intn(4) == 0 {
			start = 17
		}
		end := start + 1 + rng.Intn(MaxBands-start)
		shortBlocks := 0
		transient := 0
		if lm > 0 && rng.Intn(3) == 0 {
			shortBlocks = 1 << lm
			transient = 1
		}
		// tf_res takes the tf_decode() values: a tf_select_table entry for
		// the frame's transient flag and tf_select.
		row := &tfSelectTable[lm]
		base := 4*transient + 2*rng.Intn(2)
		pulses := make([]int32, MaxBands)
		tfRes := make([]int32, MaxBands)
		for i := range pulses {
			pulses[i] = int32(rng.Intn(400))
			tfRes[i] = int32(row[base+rng.Intn(2)])
		}
		spread := rng.Intn(4)
		totalBitsQ3 := rng.Intn(len(buf)*64) + 64
		balance := rng.Intn(200) - 100
		codedBands := start + rng.Intn(end-start+1)
		for i := range buf {
			buf[i] = byte(rng.Intn(256))
		}
		seed0 := rng.Uint32()

		var rdA, rdB rangecoding.Decoder
		var scA, scB bandDecodeScratch
		rdA.Init(buf)
		rdB.Init(buf)
		seedA, seedB := seed0, seed0
		leftA, _, colA := quantAllBandsDecodeWithScratchWithMode(&rdA, 1, frameSize, lm, start, end, pulses, shortBlocks, spread,
			0, 0, tfRes, totalBitsQ3, balance, codedBands, false, &seedA, &scA, nil, nil, 0, nil, nil, nil, nil)
		leftB, _, colB := quantAllBandsDecodeWithScratchWithMode(&rdB, 1, frameSize, lm, start, end, pulses, shortBlocks, spread,
			0, 0, tfRes, totalBitsQ3, balance, codedBands, false, &seedB, &scB, &dummy, nil, 0, nil, nil, nil, nil)
		if seedA != seedB || rdA.Tell() != rdB.Tell() || rdA.Range() != rdB.Range() || rdA.Val() != rdB.Val() {
			t.Fatalf("iter %d: seed/range state diverged", iter)
		}
		for i := range leftB {
			if math.Float32bits(float32(leftA[i])) != math.Float32bits(float32(leftB[i])) {
				t.Fatalf("iter %d: left[%d]=%v want %v", iter, i, leftA[i], leftB[i])
			}
		}
		for i := range colB {
			if colA[i] != colB[i] {
				t.Fatalf("iter %d: collapse[%d]=%d want %d", iter, i, colA[i], colB[i])
			}
		}
	}
}
