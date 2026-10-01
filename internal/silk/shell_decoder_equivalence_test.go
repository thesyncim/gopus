package silk

import (
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// silkShellDecoderRef decodes the locations of pulses4 pulses within a 16-sample
// shell block by recursively splitting the count down a binary tree of
// silkDecodeShellSplit calls. Mirrors libopus silk/shell_coder.c
// silk_shell_decoder.
func silkShellDecoderRef(pulses []int16, rd *rangecoding.Decoder, pulses4 int32) {
	// These are small fixed-size arrays, using stack allocation via array
	var pulses3 [2]int16
	var pulses2 [4]int16
	var pulses1 [8]int16

	pulses3[0], pulses3[1] = silkDecodeShellSplit(rd, pulses4, &silk_shell_code_table3_rows)
	pulses2[0], pulses2[1] = silkDecodeShellSplit(rd, int32(pulses3[0]), &silk_shell_code_table2_rows)

	pulses1[0], pulses1[1] = silkDecodeShellSplit(rd, int32(pulses2[0]), &silk_shell_code_table1_rows)
	pulses[0], pulses[1] = silkDecodeShellSplit(rd, int32(pulses1[0]), &silk_shell_code_table0_rows)
	pulses[2], pulses[3] = silkDecodeShellSplit(rd, int32(pulses1[1]), &silk_shell_code_table0_rows)

	pulses1[2], pulses1[3] = silkDecodeShellSplit(rd, int32(pulses2[1]), &silk_shell_code_table1_rows)
	pulses[4], pulses[5] = silkDecodeShellSplit(rd, int32(pulses1[2]), &silk_shell_code_table0_rows)
	pulses[6], pulses[7] = silkDecodeShellSplit(rd, int32(pulses1[3]), &silk_shell_code_table0_rows)

	pulses2[2], pulses2[3] = silkDecodeShellSplit(rd, int32(pulses3[1]), &silk_shell_code_table2_rows)

	pulses1[4], pulses1[5] = silkDecodeShellSplit(rd, int32(pulses2[2]), &silk_shell_code_table1_rows)
	pulses[8], pulses[9] = silkDecodeShellSplit(rd, int32(pulses1[4]), &silk_shell_code_table0_rows)
	pulses[10], pulses[11] = silkDecodeShellSplit(rd, int32(pulses1[5]), &silk_shell_code_table0_rows)

	pulses1[6], pulses1[7] = silkDecodeShellSplit(rd, int32(pulses2[3]), &silk_shell_code_table1_rows)
	pulses[12], pulses[13] = silkDecodeShellSplit(rd, int32(pulses1[6]), &silk_shell_code_table0_rows)
	pulses[14], pulses[15] = silkDecodeShellSplit(rd, int32(pulses1[7]), &silk_shell_code_table0_rows)
}

// TestSilkShellDecoderMatchesUnrolledReference decodes random shell blocks
// from random range-coded bytes with the tree walk and with the fully unrolled
// fifteen-step silk_shell_decoder, and requires identical pulses and range
// decoder state after every block.
func TestSilkShellDecoderMatchesUnrolledReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5e11))
	buf := make([]byte, 4096)
	for iter := range 200 {
		for i := range buf {
			buf[i] = byte(rng.Intn(256))
		}
		var got, want rangecoding.Decoder
		got.Init(buf)
		want.Init(buf)
		maxCount := 1 + rng.Intn(silkMaxPulses)
		for blk := range 60 {
			count := int32(rng.Intn(maxCount + 1))
			var gp, wp [shellCodecFrameLength]int16
			for i := range gp {
				gp[i] = int16(rng.Intn(100))
			}
			silkShellDecoder(&gp, &got, count)
			silkShellDecoderRef(wp[:], &want, count)
			if gp != wp {
				t.Fatalf("iter %d block %d count %d: pulses %v want %v", iter, blk, count, gp, wp)
			}
			if got.Range() != want.Range() || got.Val() != want.Val() || got.Tell() != want.Tell() || got.Offs() != want.Offs() {
				t.Fatalf("iter %d block %d: range state diverged", iter, blk)
			}
		}
	}
}
