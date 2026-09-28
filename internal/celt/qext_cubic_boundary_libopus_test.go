//go:build gopus_qext

package celt

import (
	"math"
	"strconv"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

var qextCubicBoundaryGridHelper libopustest.HelperCache

func buildQEXTCubicBoundaryGridHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "QEXT cubic boundary grid",
		OutputBase:  "gopus_libopus_celt_qext_cubic_boundary_grid",
		SourceFile:  "libopus_celt_qext_cubic_decode_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func TestQEXTCubicReductionBoundaryGrid(t *testing.T) {
	libopustest.RequireOracle(t)
	bin, err := qextCubicBoundaryGridHelper.Path(buildQEXTCubicBoundaryGridHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT cubic boundary grid", err)
		return
	}
	cases := []struct {
		n, res, blocks int
		gainBits       uint32
	}{
		{3, 2, 1, 0x3f123457},
		{4, 7, 1, 0x3eaaaaab},
		{5, 14, 1, 0x3f7ffdd0},
		{15, 2, 1, 0x3f400001},
		{16, 8, 1, 0x3f000001},
		{17, 14, 1, 0x3f6abcde},
		{31, 7, 2, 0x3f123457},
		{32, 14, 1, 0x3eaaaaab},
		{33, 3, 2, 0x3f7ffdd0},
		{63, 14, 1, 0x3f400001},
		{64, 8, 1, 0x3f6abcde},
	}
	for _, tc := range cases {
		t.Run("n"+strconv.Itoa(tc.n), func(t *testing.T) {
			k := 1 << tc.res
			if tc.blocks != 1 {
				k--
			}
			face := tc.n / 2
			sign := tc.n & 1
			iy := make([]int32, tc.n)
			for i := range tc.n {
				iy[i] = int32((i*97 + tc.n*31 + 13) % k)
			}
			var enc rangecoding.Encoder
			enc.Init(make([]byte, 256))
			enc.EncodeUniform(uint32(face), uint32(tc.n))
			enc.EncodeRawBits(uint32(sign), 1)
			for i, digit := range iy {
				if i != face {
					enc.EncodeRawBits(uint32(digit), uint(tc.res))
				}
			}
			packet := append([]byte(nil), enc.Done()...)
			if enc.Error() != 0 || len(packet) == 0 {
				t.Fatalf("range encode: error=%v bytes=%d", enc.Error(), len(packet))
			}
			payload := libopustest.NewOraclePayload("GQDI", uint32(tc.n), uint32(tc.res), uint32(tc.blocks), tc.gainBits, uint32(len(packet)))
			payload.Raw(packet)
			reader, err := libopustest.RunOracle(bin, payload.Bytes(), "QEXT cubic boundary grid", "GQDO")
			if err != nil {
				t.Fatal(err)
			}
			if got := reader.U32(); got != uint32(tc.n) {
				t.Fatalf("C vector length=%d want %d", got, tc.n)
			}
			wantCollapse, wantRange, wantTell := reader.U32(), reader.U32(), reader.U32()
			want := make([]uint32, tc.n)
			for i := range want {
				want[i] = reader.U32()
			}
			if err := reader.ExpectConsumed(); err != nil {
				t.Fatal(err)
			}

			var dec rangecoding.Decoder
			dec.Init(packet)
			got := make([]celtNorm, tc.n)
			gotCollapse := cubicUnquant(got, tc.n, tc.res, tc.blocks, &dec, math.Float32frombits(tc.gainBits), nil)
			if uint32(gotCollapse) != wantCollapse || dec.Range() != wantRange || uint32(dec.TellFrac()) != wantTell {
				t.Fatalf("coder state: Go collapse=%d range=%08x tell=%d, C collapse=%d range=%08x tell=%d",
					gotCollapse, dec.Range(), dec.TellFrac(), wantCollapse, wantRange, wantTell)
			}
			for i := range got {
				if bits := math.Float32bits(float32(got[i])); bits != want[i] {
					t.Fatalf("X[%d]: Go=%08x C=%08x", i, bits, want[i])
				}
			}

			var scratch bandDecodeScratch
			dec.Init(packet)
			warm := make([]celtNorm, tc.n)
			cubicUnquant(warm, tc.n, tc.res, tc.blocks, &dec, math.Float32frombits(tc.gainBits), &scratch)
			allocs := testing.AllocsPerRun(20, func() {
				dec.Init(packet)
				cubicUnquant(warm, tc.n, tc.res, tc.blocks, &dec, math.Float32frombits(tc.gainBits), &scratch)
			})
			if allocs != 0 {
				t.Fatalf("warm cubic decode allocs/call=%g, want 0", allocs)
			}
		})
	}
}
