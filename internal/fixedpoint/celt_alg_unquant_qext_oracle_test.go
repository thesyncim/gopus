//go:build gopus_fixed_point && gopus_qext

package fixedpoint

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func TestAlgUnquantQEXTMatchesSelectedLibopus(t *testing.T) {
	cases := []struct {
		n, k, spread, blocks, extraBits int
		input                           []int32
	}{
		{2, 3, 2, 1, 2, nil},
		{2, 3, 2, 1, 12, []int32{1 << 22, 0}},
		{2, 3, 2, 1, 12, []int32{-(1 << 22), 0}},
		{3, 1, 3, 1, 5, nil},
		{8, 5, 1, 2, 8, nil},
		{24, 7, 2, 4, 12, nil},
		{176, 3, 3, 8, 5, nil},
	}
	for ci, tc := range cases {
		t.Run(fmt.Sprintf("n%d_k%d_extra%d", tc.n, tc.k, tc.extraBits), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(0x7e91 + ci*137)))
			input := append([]int32(nil), tc.input...)
			if input == nil {
				input = make([]int32, tc.n)
				for i := range input {
					input[i] = int32(rng.Intn(1<<23) - (1 << 22))
				}
			}
			encoded, err := libopustest.ProbeCELTFixedQEXTPVQ(libopustest.CELTFixedQEXTPVQParams{
				N: tc.n, K: tc.k, Spread: tc.spread, Blocks: tc.blocks, ExtraBits: tc.extraBits,
				Resynth: true, Gain: q31One, MainStorage: 512, ExtStorage: 1024, X: input,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed-QEXT PVQ packet encoder", err)
			}
			want, err := libopustest.ProbeCELTFixedQEXTPVQDecode(libopustest.CELTFixedQEXTPVQDecodeParams{
				N: tc.n, K: tc.k, Spread: tc.spread, Blocks: tc.blocks, ExtraBits: tc.extraBits,
				Gain: q31One, MainPacket: encoded.MainPacket, ExtPacket: encoded.ExtPacket,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed-QEXT PVQ decoder", err)
			}

			got := make([]int32, tc.n)
			var dec, extDec rangecoding.Decoder
			dec.Init(encoded.MainPacket)
			extDec.Init(encoded.ExtPacket)
			collapse := AlgUnquantQEXT(got, tc.n, tc.k, tc.spread, tc.blocks, &dec, &extDec, q31One, tc.extraBits)
			if collapse != want.CollapseMask {
				t.Fatalf("collapse mask=%x, libopus=%x", collapse, want.CollapseMask)
			}
			for i := range got {
				if got[i] != want.Samples[i] {
					t.Fatalf("sample[%d]=%d, libopus=%d", i, got[i], want.Samples[i])
				}
			}
			assertQEXTRangeDecoderState(t, "main", &dec, want.MainRange, want.MainVal,
				int(want.MainTell), int(want.MainTellFrac), int(want.MainError))
			assertQEXTRangeDecoderState(t, "extension", &extDec, want.ExtRange, want.ExtVal,
				int(want.ExtTell), int(want.ExtTellFrac), int(want.ExtError))

			allocs := testing.AllocsPerRun(10, func() {
				clear(got)
				dec.Init(encoded.MainPacket)
				extDec.Init(encoded.ExtPacket)
				_ = AlgUnquantQEXT(got, tc.n, tc.k, tc.spread, tc.blocks, &dec, &extDec, q31One, tc.extraBits)
			})
			if allocs != 0 {
				t.Fatalf("steady-state allocations=%g, want 0", allocs)
			}
		})
	}
}

func assertQEXTRangeDecoderState(t *testing.T, label string, got *rangecoding.Decoder,
	wantRange, wantVal uint32, wantTell, wantTellFrac, wantError int) {
	t.Helper()
	gotRange, gotVal := got.State()
	if gotRange != wantRange || gotVal != wantVal || got.Tell() != wantTell ||
		got.TellFrac() != wantTellFrac || got.Error() != wantError {
		t.Fatalf("%s decoder=(range=%08x val=%08x tell=%d frac=%d err=%d), libopus=(range=%08x val=%08x tell=%d frac=%d err=%d)",
			label, gotRange, gotVal, got.Tell(), got.TellFrac(), got.Error(),
			wantRange, wantVal, wantTell, wantTellFrac, wantError)
	}
}
