package celt

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const celtResidualModeNormaliseResidual = uint32(0)

var libopusCELTResidualHelper libopustest.HelperCache

type residualCollapseOracleCase struct {
	name   string
	pulses []int
	b      int
	gain   float32
}

type residualCollapseOracleResult struct {
	collapse int
	x        []float32
}

func buildLibopusCELTResidualHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "celt residual",
		OutputBase:  "gopus_libopus_celt_residual",
		SourceFile:  "libopus_celt_residual_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		RefSources: []string{
			"celt/cwrs.c",
			"celt/entenc.c",
			"celt/entdec.c",
			"celt/entcode.c",
			"celt/laplace.c",
		},
		DeadStrip: true,
	})
}

func probeLibopusNormaliseResidual(cases []residualCollapseOracleCase) ([]residualCollapseOracleResult, error) {
	binPath, err := libopusCELTResidualHelper.Path(buildLibopusCELTResidualHelper)
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GVRI", celtResidualModeNormaliseResidual, uint32(len(cases)))
	for _, tc := range cases {
		energy := float32(pulseEnergy(tc.pulses))
		payload.U32(uint32(len(tc.pulses)))
		payload.U32(uint32(tc.b))
		payload.Float32(tc.gain)
		payload.Float32(energy)
		for _, pulse := range tc.pulses {
			payload.I32(int32(pulse))
		}
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt residual", "GVRO")
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]residualCollapseOracleResult, count)
	for i := range out {
		out[i].collapse = int(reader.U32())
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

func TestNormalizeResidualKnownEnergyIntoAndCollapseNormMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []residualCollapseOracleCase{
		{name: "n2_unit", pulses: []int{1, -1}, b: 1, gain: 1},
		{name: "n3_sparse", pulses: []int{0, 2, -1}, b: 1, gain: 0.75},
		{name: "n4_split", pulses: []int{0, 3, -2, 0}, b: 2, gain: 0.5},
		{name: "n7_tail", pulses: []int{1, 0, -1, 2, 0, 0, -2}, b: 3, gain: 1.25},
		{name: "n8_all_blocks", pulses: []int{0, 2, 0, -1, 3, 0, 0, -2}, b: 4, gain: 0.625},
		{name: "n12_mixed", pulses: []int{0, 3, -2, 0, 1, 0, -1, 0, 2, 0, 0, -1}, b: 3, gain: 0.875},
		{name: "n16_sparse_blocks", pulses: []int{1, 0, -1, 1, 0, -1, 1, 0, 0, 0, -1, 0, 0, 0, 0, 0}, b: 4, gain: 0.5},
		{name: "n24_light", pulses: makeAlgUnquantPulseVector(24, 13, 0x31415926), b: 6, gain: 0.875},
		{name: "n48_normal", pulses: makeAlgUnquantPulseVector(48, 21, 0xabcdef01), b: 8, gain: 0.625},
	}
	want, err := probeLibopusNormaliseResidual(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt residual", err)
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			energy := float32(pulseEnergy(tc.pulses))
			got := make([]celtNorm, len(tc.pulses))
			gotCollapse := normalizeResidualKnownEnergyIntoAndCollapseNorm(got, append([]int(nil), tc.pulses...), opusVal16(tc.gain), opusVal16(energy), tc.b)
			if gotCollapse != want[i].collapse {
				t.Fatalf("collapse=%d want %d", gotCollapse, want[i].collapse)
			}
			if len(got) != len(want[i].x) {
				t.Fatalf("len(got)=%d want %d", len(got), len(want[i].x))
			}
			for j := range got {
				gotBits := math.Float32bits(float32(got[j]))
				wantBits := math.Float32bits(want[i].x[j])
				if gotBits != wantBits {
					t.Fatalf("x[%d]=%08x/%0.9g want %08x/%0.9g",
						j, gotBits, float32(got[j]), wantBits, want[i].x[j])
				}
			}
		})
	}
}
