//go:build gopus_fixed_point && gopus_qext

package fixedpoint

import (
	"bytes"
	"slices"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func TestAlgQuantQEXTMatchesFixedLibopus(t *testing.T) {
	cases := []struct {
		name string
		p    libopustest.CELTFixedQEXTPVQParams
	}{
		{
			name: "n2_positive_refine_resynth",
			p: libopustest.CELTFixedQEXTPVQParams{
				N: 2, K: 5, Spread: 1, Blocks: 1, ExtraBits: 5, Resynth: true,
				Gain: 1 << 30, MainStorage: 128, ExtStorage: 128,
				X: []int32{-0x345678, 0x123456},
			},
		},
		{
			name: "n2_silence_without_resynth",
			p: libopustest.CELTFixedQEXTPVQParams{
				N: 2, K: 7, Spread: 0, Blocks: 1, ExtraBits: 8, Resynth: false,
				Gain: 1 << 30, MainStorage: 128, ExtStorage: 128,
				X: []int32{0, 0},
			},
		},
		{
			name: "n8_multiblock_refine_resynth",
			p: libopustest.CELTFixedQEXTPVQParams{
				N: 8, K: 9, Spread: 2, Blocks: 2, ExtraBits: 7, Resynth: true,
				Gain: 1 << 30, MainStorage: 128, ExtStorage: 256,
				X: []int32{0x321987, -0x1ef001, 0x0abcde, 0x145678, -0x245678, 0x01fedc, -0x0aabbc, 0x112233},
			},
		},
		{
			name: "n16_extra_bits_shift",
			p: libopustest.CELTFixedQEXTPVQParams{
				N: 16, K: 12, Spread: 3, Blocks: 4, ExtraBits: 10, Resynth: true,
				Gain: 1 << 30, MainStorage: 128, ExtStorage: 256,
				X: []int32{
					0x123456, -0x321abc, 0x034567, 0x245678,
					-0x145678, 0x056789, -0x2789ab, 0x16789a,
					0x2789ab, -0x056789, 0x145678, -0x245678,
					0x321abc, -0x123456, 0x0789ab, -0x034567,
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, err := libopustest.ProbeCELTFixedQEXTPVQ(tc.p)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed-QEXT CELT PVQ", err)
				return
			}

			gotX := append(slices.Clone(tc.p.X), int32(0x13572468), int32(-0x24681357))
			mainBuf := make([]byte, tc.p.MainStorage)
			extBuf := make([]byte, tc.p.ExtStorage)
			var mainEnc, extEnc rangecoding.Encoder
			mainEnc.Init(mainBuf)
			extEnc.Init(extBuf)
			scratch := &celtEncodeScratch{}
			gotCollapse := AlgQuantQEXT(gotX, tc.p.N, tc.p.K, tc.p.Spread, tc.p.Blocks,
				&mainEnc, &extEnc, tc.p.Gain, tc.p.Resynth, tc.p.ExtraBits, scratch)
			gotMainRange, gotExtRange := mainEnc.Range(), extEnc.Range()
			gotMainPacket := mainEnc.Done()
			gotExtPacket := extEnc.Done()

			if gotCollapse != want.CollapseMask || gotMainRange != want.MainRange || gotExtRange != want.ExtRange ||
				!bytes.Equal(gotMainPacket, want.MainPacket) || !bytes.Equal(gotExtPacket, want.ExtPacket) ||
				!slices.Equal(gotX[:tc.p.N], want.Resynthesized) || gotX[tc.p.N] != 0x13572468 || gotX[tc.p.N+1] != -0x24681357 {
				t.Fatalf("fixed-QEXT PVQ differs: collapse=%08x/%08x mainRange=%08x/%08x extRange=%08x/%08x main=% x/% x ext=% x/% x firstX=%08x/%08x",
					gotCollapse, want.CollapseMask, gotMainRange, want.MainRange, gotExtRange, want.ExtRange,
					gotMainPacket, want.MainPacket, gotExtPacket, want.ExtPacket, gotX[0], want.Resynthesized[0])
			}

			// Warm reusable search/CWRS scratch before measuring the new QEXT path.
			allocs := testing.AllocsPerRun(20, func() {
				copy(gotX, tc.p.X)
				mainEnc.Init(mainBuf)
				extEnc.Init(extBuf)
				AlgQuantQEXT(gotX, tc.p.N, tc.p.K, tc.p.Spread, tc.p.Blocks,
					&mainEnc, &extEnc, tc.p.Gain, tc.p.Resynth, tc.p.ExtraBits, scratch)
			})
			if allocs != 0 {
				t.Fatalf("steady-state fixed-QEXT PVQ allocated %.2f times per call", allocs)
			}
		})
	}
}
