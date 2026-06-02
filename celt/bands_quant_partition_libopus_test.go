package celt

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const celtPartitionModeZeroPulse = uint32(0)

var libopusCELTPartitionHelper libopustest.HelperCache

type zeroPulsePartitionOracleCase struct {
	name    string
	n       int
	blocks  int
	lm      int
	band    int
	fill    int
	seed    uint32
	gain    float32
	lowband []float32
}

type zeroPulsePartitionOracleResult struct {
	collapse int
	seed     uint32
	x        []float32
}

func buildLibopusCELTPartitionHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "celt partition",
		OutputBase:  "gopus_libopus_celt_partition",
		SourceFile:  "libopus_celt_partition_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func probeLibopusZeroPulsePartition(cases []zeroPulsePartitionOracleCase) ([]zeroPulsePartitionOracleResult, error) {
	binPath, err := libopusCELTPartitionHelper.Path(buildLibopusCELTPartitionHelper)
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GVPI", celtPartitionModeZeroPulse, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.n))
		payload.U32(uint32(tc.blocks))
		payload.I32(int32(tc.lm))
		payload.U32(uint32(tc.band))
		payload.U32(uint32(tc.fill))
		payload.U32(tc.seed)
		if tc.lowband == nil {
			payload.U32(0)
		} else {
			payload.U32(1)
		}
		payload.Float32(tc.gain)
		for _, sample := range tc.lowband {
			payload.Float32(sample)
		}
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt partition", "GVPO")
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]zeroPulsePartitionOracleResult, count)
	for i := range out {
		out[i].collapse = int(reader.U32())
		out[i].seed = reader.U32()
		n := int(reader.U32())
		out[i].x = make([]float32, n)
		for j := range out[i].x {
			out[i].x[j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func makeZeroPulseLowband(n int, seed uint32) []float32 {
	out := make([]float32, n)
	state := seed
	for i := range out {
		state = state*1664525 + 1013904223
		v := float32(int32(state)>>18) * (1.0 / 64.0)
		if i%5 == 0 {
			v = -v
		}
		out[i] = v
	}
	return out
}

func TestQuantPartitionZeroPulseMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireBitExactFloat(t)
	cases := []zeroPulsePartitionOracleCase{
		{name: "noise_lm0_b1", n: 3, blocks: 1, lm: 0, band: 3, fill: 1, seed: 0x13579bdf, gain: 1},
		{name: "noise_lm1_b2_partial_fill", n: 8, blocks: 2, lm: 1, band: 6, fill: 1, seed: 0x2468ace0, gain: 0.75},
		{name: "noise_lm2_b4", n: 24, blocks: 4, lm: 2, band: 8, fill: 0xf, seed: 0x31415926, gain: 0.625},
		{name: "fold_lm1_b2", n: 8, blocks: 2, lm: 1, band: 6, fill: 2, seed: 0xabcdef01, gain: 0.5, lowband: makeZeroPulseLowband(8, 0x10203040)},
		{name: "fold_lm2_b4_sparse_fill", n: 24, blocks: 4, lm: 2, band: 8, fill: 0xb, seed: 0x0badf00d, gain: 1.25, lowband: makeZeroPulseLowband(24, 0x50607080)},
		{name: "fold_lm3_b8", n: 96, blocks: 8, lm: 3, band: 11, fill: 0xa5, seed: 0xdeadbeef, gain: 0.875, lowband: makeZeroPulseLowband(96, 0x90abcdef)},
		{name: "collapsed_fill_zero", n: 16, blocks: 4, lm: 2, band: 8, fill: 0, seed: 0x01020304, gain: 1, lowband: makeZeroPulseLowband(16, 0x11223344)},
	}
	want, err := probeLibopusZeroPulsePartition(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt partition", err)
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &bandCtx{
				resynth:    true,
				spread:     spreadNormal,
				band:       tc.band,
				seed:       tc.seed,
				seedActive: true,
			}
			x := make([]celtNorm, tc.n)
			var lowband []celtNorm
			if tc.lowband != nil {
				lowband = make([]celtNorm, len(tc.lowband))
				for j, sample := range tc.lowband {
					lowband[j] = celtNorm(sample)
				}
			}
			gotCollapse := quantPartitionDecodeNoExt(ctx, x, tc.n, 0, tc.blocks, lowband, tc.lm, opusVal16(tc.gain), tc.fill)
			if gotCollapse != want[i].collapse {
				t.Fatalf("collapse=%d want %d", gotCollapse, want[i].collapse)
			}
			if ctx.seed != want[i].seed {
				t.Fatalf("seed=%08x want %08x", ctx.seed, want[i].seed)
			}
			if len(x) != len(want[i].x) {
				t.Fatalf("len(got)=%d want %d", len(x), len(want[i].x))
			}
			for j := range x {
				gotBits := math.Float32bits(float32(x[j]))
				wantBits := math.Float32bits(want[i].x[j])
				if gotBits != wantBits {
					t.Fatalf("x[%d]=%08x/%0.9g want %08x/%0.9g",
						j, gotBits, float32(x[j]), wantBits, want[i].x[j])
				}
			}
		})
	}
}
