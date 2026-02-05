//go:build cgo_libopus

package silk

import (
	"testing"

	cgowrap "github.com/thesyncim/gopus/celt/cgo_test"
)

func TestResamplerDown2MatchesLibopus(t *testing.T) {
	const inLen = 320
	input := make([]int16, inLen)
	for i := range input {
		input[i] = int16((i*73)%6000 - 3000)
	}

	outLib := cgowrap.ProcessLibopusDown2(input)
	outGo := make([]int16, len(outLib))
	var st [2]int32
	gotLen := resamplerDown2(&st, outGo, input)
	outGo = outGo[:gotLen]

	if len(outGo) != len(outLib) {
		t.Fatalf("length mismatch: go=%d lib=%d", len(outGo), len(outLib))
	}
	for i := range outGo {
		if outGo[i] != outLib[i] {
			t.Fatalf("down2 mismatch at %d: go=%d lib=%d", i, outGo[i], outLib[i])
		}
	}
}

