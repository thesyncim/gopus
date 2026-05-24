package silk

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKLPCInputMagic  = "GSLI"
	libopusSILKLPCOutputMagic = "GSLO"

	libopusSILKLPCModeBurgModified      = uint32(0)
	libopusSILKLPCModeLPCAnalysisFilter = uint32(1)
)

var libopusSILKLPCHelper libopustest.HelperCache

func getLibopusSILKLPCHelperPath() (string, error) {
	return libopusSILKLPCHelper.CHelperPath(libopustest.CHelperConfig{
		Label:        "silk lpc",
		OutputBase:   "gopus_libopus_silk_lpc",
		SourceFile:   "libopus_silk_lpc_info.c",
		ProbeRelPath: "silk/float/SigProc_FLP.h",
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes:  []string{"celt", "silk", "silk/float"},
		RefSources: []string{
			"silk/float/burg_modified_FLP.c",
			"silk/float/energy_FLP.c",
			"silk/float/inner_product_FLP.c",
			"silk/float/LPC_analysis_filter_FLP.c",
		},
	})
}

type libopusSILKBurgCase struct {
	name       string
	subfrLen   int
	nbSubfr    int
	order      int
	minInvGain float32
	x          []float32
}

type libopusSILKBurgResult struct {
	resNrg float32
	a      []float32
}

type libopusSILKLPCFilterCase struct {
	name  string
	order int
	pred  []float32
	x     []float32
}

func probeLibopusSILKBurgModified(cases []libopusSILKBurgCase) ([]libopusSILKBurgResult, error) {
	binPath, err := getLibopusSILKLPCHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKLPCInputMagic, libopusSILKLPCModeBurgModified, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.subfrLen))
		payload.U32(uint32(tc.nbSubfr))
		payload.U32(uint32(tc.order))
		payload.Float32(tc.minInvGain)
		payload.Float32s(tc.x...)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk lpc burg", libopusSILKLPCOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]libopusSILKBurgResult, count)
	for i := range out {
		out[i].resNrg = reader.Float32()
		order := int(reader.U32())
		if order != 10 && order != 16 {
			return nil, fmt.Errorf("helper order=%d", order)
		}
		out[i].a = make([]float32, order)
		for j := 0; j < 16; j++ {
			v := reader.Float32()
			if j < order {
				out[i].a[j] = v
			}
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusSILKLPCAnalysisFilter(cases []libopusSILKLPCFilterCase) ([][]float32, error) {
	binPath, err := getLibopusSILKLPCHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKLPCInputMagic, libopusSILKLPCModeLPCAnalysisFilter, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.x)))
		payload.U32(uint32(tc.order))
		payload.Float32s(tc.pred...)
		payload.Float32s(tc.x...)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk lpc analysis filter", libopusSILKLPCOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([][]float32, count)
	for i := range out {
		length := int(reader.U32())
		if length <= 0 || length > 512 {
			return nil, fmt.Errorf("helper length=%d", length)
		}
		out[i] = make([]float32, length)
		for j := range out[i] {
			out[i][j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKBurgModifiedFLPMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusSILKBurgCase{
		{name: "order10_four_subframes", subfrLen: 90, nbSubfr: 4, order: 10, minInvGain: 1e-4, x: silkBurgOracleSignal(360, 0x12345678)},
		{name: "order16_four_subframes", subfrLen: 96, nbSubfr: 4, order: 16, minInvGain: 1e-4, x: silkBurgOracleSignal(384, 0x9abcdef0)},
		{name: "order16_two_subframes", subfrLen: 96, nbSubfr: 2, order: 16, minInvGain: 5e-5, x: silkBurgOracleSignal(192, 0x31415926)},
		{name: "order10_tight_gain", subfrLen: 80, nbSubfr: 2, order: 10, minInvGain: 0.08, x: silkBurgOracleSignal(160, 0x27182818)},
	}
	want, err := probeLibopusSILKBurgModified(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk lpc burg", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc := &Encoder{}
			gotA, gotRes := enc.burgModifiedFLPZeroAllocF32(tc.x, tc.minInvGain, tc.subfrLen, tc.nbSubfr, tc.order)
			gotRes32 := float32(gotRes)
			if math.Float32bits(gotRes32) != math.Float32bits(want[i].resNrg) {
				t.Fatalf("resNrg=%08x %.10g want %08x %.10g",
					math.Float32bits(gotRes32), gotRes32,
					math.Float32bits(want[i].resNrg), want[i].resNrg)
			}
			if len(gotA) != len(want[i].a) {
				t.Fatalf("A len=%d want %d", len(gotA), len(want[i].a))
			}
			for j := range gotA {
				got := float32(gotA[j])
				if math.Float32bits(got) != math.Float32bits(want[i].a[j]) {
					t.Fatalf("A[%d]=%08x %.10g want %08x %.10g",
						j,
						math.Float32bits(got), got,
						math.Float32bits(want[i].a[j]), want[i].a[j])
				}
			}
		})
	}
}

func TestSILKLPCAnalysisFilterFLPMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusSILKLPCFilterCase{
		{name: "order10", order: 10, pred: silkLPCOraclePred(10, 0.44), x: silkBurgOracleSignal(160, 0x10203040)},
		{name: "order16", order: 16, pred: silkLPCOraclePred(16, 0.38), x: silkBurgOracleSignal(224, 0x50607080)},
		{name: "order16_short", order: 16, pred: silkLPCOraclePred(16, 0.25), x: silkBurgOracleSignal(64, 0xa0b0c0d0)},
	}
	want, err := probeLibopusSILKLPCAnalysisFilter(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk lpc analysis filter", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := make([]float32, len(tc.x))
			lpcAnalysisFilterF32(got, tc.pred, tc.x, len(tc.x), tc.order)
			if len(got) != len(want[i]) {
				t.Fatalf("residual len=%d want %d", len(got), len(want[i]))
			}
			for j := range got {
				if math.Float32bits(got[j]) != math.Float32bits(want[i][j]) {
					t.Fatalf("residual[%d]=%08x %.10g want %08x %.10g",
						j,
						math.Float32bits(got[j]), got[j],
						math.Float32bits(want[i][j]), want[i][j])
				}
			}
		})
	}
}

func silkBurgOracleSignal(n int, seed uint32) []float32 {
	x := make([]float32, n)
	state := seed
	for i := range x {
		state = state*1664525 + 1013904223
		noise := float32(int32(state>>8)&0xffff-32768) / 32768.0
		ramp := float32((i%31)-15) * (1.0 / 256.0)
		x[i] = 0.35*noise + ramp
	}
	return x
}

func silkLPCOraclePred(order int, gain float32) []float32 {
	pred := make([]float32, order)
	for i := range pred {
		sign := float32(1)
		if i&1 != 0 {
			sign = -1
		}
		pred[i] = sign * gain / float32(i+2)
	}
	return pred
}
