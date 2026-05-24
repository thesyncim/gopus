package silk

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKNLSFInputMagic  = "GSNI"
	libopusSILKNLSFOutputMagic = "GSNO"

	libopusSILKNLSFModeDecode    = uint32(0)
	libopusSILKNLSFModeNLSF2A    = uint32(1)
	libopusSILKNLSFModeA2NLSF    = uint32(2)
	libopusSILKNLSFModeStabilize = uint32(3)
	libopusSILKNLSFModeWeights   = uint32(4)
	libopusSILKNLSFModeVQ        = uint32(5)
	libopusSILKNLSFModeDelDec    = uint32(6)
	libopusSILKNLSFModeEncode    = uint32(7)

	libopusSILKNLSFCBNBMB = uint32(0)
	libopusSILKNLSFCBWB   = uint32(1)
)

var libopusSILKNLSFHelper libopustest.HelperCache

func buildLibopusSILKNLSFHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:        "silk nlsf",
		OutputBase:   "gopus_libopus_silk_nlsf",
		SourceFile:   "libopus_silk_nlsf_info.c",
		ProbeRelPath: "silk/main.h",
		CFlags:       []string{"-DHAVE_CONFIG_H"},
		RefIncludes:  []string{"celt", "silk"},
		Libs:         []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		RefSources: []string{
			"silk/NLSF_decode.c",
			"silk/NLSF_del_dec_quant.c",
			"silk/NLSF_encode.c",
			"silk/NLSF_VQ.c",
			"silk/NLSF_VQ_weights_laroia.c",
			"silk/NLSF_unpack.c",
			"silk/NLSF_stabilize.c",
			"silk/NLSF2A.c",
			"silk/A2NLSF.c",
			"silk/LPC_fit.c",
			"silk/LPC_inv_pred_gain.c",
			"silk/bwexpander_32.c",
			"silk/sort.c",
			"silk/tables_NLSF_CB_NB_MB.c",
			"silk/tables_NLSF_CB_WB.c",
			"silk/table_LSF_cos.c",
		},
	})
}

func getLibopusSILKNLSFHelperPath() (string, error) {
	return libopusSILKNLSFHelper.Path(buildLibopusSILKNLSFHelper)
}

func probeLibopusSILKNLSF(mode uint32, records [][]uint32) ([][]int16, error) {
	binPath, err := getLibopusSILKNLSFHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKNLSFInputMagic, mode, uint32(len(records)))
	for _, record := range records {
		for _, word := range record {
			payload.U32(word)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk nlsf", libopusSILKNLSFOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(records))
	reader.ExpectRemaining(count * (4 + 16*4))
	out := make([][]int16, count)
	for i := range out {
		order := int(reader.U32())
		if order != 10 && order != 16 {
			return nil, fmt.Errorf("helper order=%d", order)
		}
		out[i] = make([]int16, order)
		for j := 0; j < 16; j++ {
			if j < order {
				out[i][j] = int16(reader.I32())
			} else {
				reader.I32()
			}
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusSILKNLSFVQ(records [][]uint32) ([][]int32, error) {
	binPath, err := getLibopusSILKNLSFHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKNLSFInputMagic, libopusSILKNLSFModeVQ, uint32(len(records)))
	for _, record := range records {
		for _, word := range record {
			payload.U32(word)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk nlsf vq", libopusSILKNLSFOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(records))
	out := make([][]int32, count)
	for i := range out {
		nVectors := int(reader.U32())
		if nVectors <= 0 || nVectors > 32 {
			return nil, fmt.Errorf("helper nVectors=%d", nVectors)
		}
		out[i] = make([]int32, nVectors)
		for j := 0; j < 32; j++ {
			v := reader.I32()
			if j < nVectors {
				out[i][j] = v
			}
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

type libopusSILKNLSFDelDecResult struct {
	rdQ25   int32
	indices []int8
}

func probeLibopusSILKNLSFDelDec(records [][]uint32) ([]libopusSILKNLSFDelDecResult, error) {
	binPath, err := getLibopusSILKNLSFHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKNLSFInputMagic, libopusSILKNLSFModeDelDec, uint32(len(records)))
	for _, record := range records {
		for _, word := range record {
			payload.U32(word)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk nlsf delayed decision", libopusSILKNLSFOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(records))
	out := make([]libopusSILKNLSFDelDecResult, count)
	for i := range out {
		out[i].rdQ25 = reader.I32()
		order := int(reader.U32())
		if order != 10 && order != 16 {
			return nil, fmt.Errorf("helper order=%d", order)
		}
		out[i].indices = make([]int8, order)
		for j := 0; j < 16; j++ {
			v := reader.I32()
			if j < order {
				out[i].indices[j] = int8(v)
			}
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

type libopusSILKNLSFEncodeResult struct {
	rdQ25   int32
	indices []int8
	nlsf    []int16
}

func probeLibopusSILKNLSFEncode(records [][]uint32) ([]libopusSILKNLSFEncodeResult, error) {
	binPath, err := getLibopusSILKNLSFHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKNLSFInputMagic, libopusSILKNLSFModeEncode, uint32(len(records)))
	for _, record := range records {
		for _, word := range record {
			payload.U32(word)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk nlsf encode", libopusSILKNLSFOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(records))
	out := make([]libopusSILKNLSFEncodeResult, count)
	for i := range out {
		out[i].rdQ25 = reader.I32()
		order := int(reader.U32())
		if order != 10 && order != 16 {
			return nil, fmt.Errorf("helper order=%d", order)
		}
		out[i].indices = make([]int8, order+1)
		for j := 0; j < maxLPCOrder+1; j++ {
			v := reader.I32()
			if j < order+1 {
				out[i].indices[j] = int8(v)
			}
		}
		out[i].nlsf = make([]int16, order)
		for j := 0; j < 16; j++ {
			v := reader.I32()
			if j < order {
				out[i].nlsf[j] = int16(v)
			}
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func cbIDForNLSF(cb *nlsfCB) uint32 {
	if cb == &silk_NLSF_CB_WB {
		return libopusSILKNLSFCBWB
	}
	return libopusSILKNLSFCBNBMB
}

func uint32FromInt32(v int32) uint32 {
	return uint32(v)
}

func TestSILKNLSFDecodeMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name    string
		cb      *nlsfCB
		indices []int8
	}{
		{name: "nbmb_frame0", cb: &silk_NLSF_CB_NB_MB, indices: []int8{17, 0, -1, 0, 2, 0, 0, 0, -3, 1, -1}},
		{name: "nbmb_frame1", cb: &silk_NLSF_CB_NB_MB, indices: []int8{23, 0, 0, -1, 1, -1, -1, -1, -2, 1, -2}},
		{name: "nbmb_frame2", cb: &silk_NLSF_CB_NB_MB, indices: []int8{14, 0, -1, -2, 2, 1, 0, 0, -2, 1, 0}},
		{name: "wb_low_residual", cb: &silk_NLSF_CB_WB, indices: []int8{3, 0, -1, 1, 0, 2, -2, 1, 0, -1, 1, 0, 2, -1, 0, 1, -2}},
		{name: "wb_high_residual", cb: &silk_NLSF_CB_WB, indices: []int8{31, 1, 0, -1, 2, -2, 1, 0, -1, 2, -2, 1, 0, -1, 2, -2, 1}},
	}

	records := make([][]uint32, len(cases))
	for i, tc := range cases {
		record := []uint32{cbIDForNLSF(tc.cb)}
		for _, idx := range tc.indices {
			record = append(record, uint32FromInt32(int32(idx)))
		}
		records[i] = record
	}
	want, err := probeLibopusSILKNLSF(libopusSILKNLSFModeDecode, records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk nlsf", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := make([]int16, tc.cb.order)
			silkNLSFDecode(got, tc.indices, tc.cb)
			if !sameInt16s(got, want[i]) {
				t.Fatalf("silkNLSFDecode=%v want %v", got, want[i])
			}
		})
	}
}

func TestSILKNLSFWeightsLaroiaMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name string
		nlsf []int16
	}{
		{name: "nbmb_regular", nlsf: []int16{2676, 3684, 7247, 12558, 14555, 16405, 18875, 19753, 26306, 27425}},
		{name: "nbmb_clustered", nlsf: []int16{128, 256, 512, 1024, 2048, 4096, 8192, 16384, 24576, 32640}},
		{name: "wb_regular", nlsf: []int16{1200, 2600, 4300, 6100, 8200, 10100, 12200, 14300, 16400, 18600, 20700, 22800, 24900, 27000, 29100, 31200}},
		{name: "wb_tight_edges", nlsf: []int16{1, 2, 4, 8, 16, 64, 256, 1024, 4096, 8192, 12288, 16384, 24576, 28672, 32765, 32766}},
	}

	records := make([][]uint32, len(cases))
	for i, tc := range cases {
		record := []uint32{uint32(len(tc.nlsf))}
		for _, v := range tc.nlsf {
			record = append(record, uint32FromInt32(int32(v)))
		}
		records[i] = record
	}
	want, err := probeLibopusSILKNLSF(libopusSILKNLSFModeWeights, records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk nlsf weights", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := make([]int16, len(tc.nlsf))
			silkNLSFWeightsLaroia(got, tc.nlsf, len(tc.nlsf))
			if !sameInt16s(got, want[i]) {
				t.Fatalf("silkNLSFWeightsLaroia=%v want %v", got, want[i])
			}
		})
	}
}

func TestSILKNLSFVQMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name string
		cb   *nlsfCB
		nlsf []int16
	}{
		{name: "nbmb_regular", cb: &silk_NLSF_CB_NB_MB, nlsf: []int16{2676, 3684, 7247, 12558, 14555, 16405, 18875, 19753, 26306, 27425}},
		{name: "nbmb_clustered", cb: &silk_NLSF_CB_NB_MB, nlsf: []int16{128, 256, 512, 1024, 2048, 4096, 8192, 16384, 24576, 32640}},
		{name: "wb_regular", cb: &silk_NLSF_CB_WB, nlsf: []int16{1200, 2600, 4300, 6100, 8200, 10100, 12200, 14300, 16400, 18600, 20700, 22800, 24900, 27000, 29100, 31200}},
		{name: "wb_tight_edges", cb: &silk_NLSF_CB_WB, nlsf: []int16{1, 2, 4, 8, 16, 64, 256, 1024, 4096, 8192, 12288, 16384, 24576, 28672, 32765, 32766}},
	}

	records := make([][]uint32, len(cases))
	for i, tc := range cases {
		record := []uint32{cbIDForNLSF(tc.cb)}
		for _, v := range tc.nlsf {
			record = append(record, uint32FromInt32(int32(v)))
		}
		records[i] = record
	}
	want, err := probeLibopusSILKNLSFVQ(records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk nlsf vq", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := make([]int32, tc.cb.nVectors)
			silkNLSFVQ(got, tc.nlsf, tc.cb.cb1NLSFQ8, tc.cb.cb1WghtQ9, tc.cb.nVectors, tc.cb.order)
			if len(got) != len(want[i]) {
				t.Fatalf("silkNLSFVQ len=%d want %d", len(got), len(want[i]))
			}
			for j := range got {
				if got[j] != want[i][j] {
					t.Fatalf("silkNLSFVQ[%d]=%d want %d", j, got[j], want[i][j])
				}
			}
		})
	}
}

func TestSILKNLSFDelDecQuantMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name   string
		cb     *nlsfCB
		ind1   int
		muQ20  int32
		nlsf   []int16
		weight []int16
	}{
		{name: "nbmb_stage3", cb: &silk_NLSF_CB_NB_MB, ind1: 3, muQ20: 3146, nlsf: []int16{2676, 3684, 7247, 12558, 14555, 16405, 18875, 19753, 26306, 27425}},
		{name: "nbmb_stage19_active", cb: &silk_NLSF_CB_NB_MB, ind1: 19, muQ20: 1800, nlsf: []int16{128, 256, 512, 1024, 2048, 4096, 8192, 16384, 24576, 32640}},
		{name: "wb_stage7", cb: &silk_NLSF_CB_WB, ind1: 7, muQ20: 3146, nlsf: []int16{1200, 2600, 4300, 6100, 8200, 10100, 12200, 14300, 16400, 18600, 20700, 22800, 24900, 27000, 29100, 31200}},
		{name: "wb_stage31_active", cb: &silk_NLSF_CB_WB, ind1: 31, muQ20: 1200, nlsf: []int16{1, 2, 4, 8, 16, 64, 256, 1024, 4096, 8192, 12288, 16384, 24576, 28672, 32765, 32766}},
	}
	records := make([][]uint32, len(cases))
	for i := range cases {
		cases[i].weight = make([]int16, cases[i].cb.order)
		silkNLSFWeightsLaroia(cases[i].weight, cases[i].nlsf, cases[i].cb.order)
		record := []uint32{cbIDForNLSF(cases[i].cb), uint32(cases[i].ind1), uint32FromInt32(cases[i].muQ20)}
		for _, v := range cases[i].nlsf {
			record = append(record, uint32FromInt32(int32(v)))
		}
		for _, v := range cases[i].weight {
			record = append(record, uint32FromInt32(int32(v)))
		}
		records[i] = record
	}
	want, err := probeLibopusSILKNLSFDelDec(records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk nlsf delayed decision", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			order := tc.cb.order
			baseIdx := tc.ind1 * order
			var resQ10 [maxLPCOrder]int16
			var wAdjQ5 [maxLPCOrder]int16
			var ecIx [maxLPCOrder]int16
			var predQ8 [maxLPCOrder]uint8
			var gotIndices [maxLPCOrder]int8
			for j := 0; j < order; j++ {
				wTmpQ9 := int32(tc.cb.cb1WghtQ9[baseIdx+j])
				diff := int32(tc.nlsf[j]) - (int32(tc.cb.cb1NLSFQ8[baseIdx+j]) << 7)
				resQ10[j] = int16(silkRSHIFT(silkSMULBB(diff, wTmpQ9), 14))
				denom := silkSMULBB(wTmpQ9, wTmpQ9)
				if denom == 0 {
					denom = 1
				}
				wAdjQ5[j] = int16(silk_DIV32_varQ(int32(tc.weight[j]), denom, 21))
			}
			silkNLSFUnpack(ecIx[:order], predQ8[:order], tc.cb, tc.ind1)
			gotRD := silkNLSFDelDecQuant(gotIndices[:], resQ10[:], wAdjQ5[:], predQ8[:], ecIx[:], tc.cb.ecRatesQ5, tc.cb.quantStepSizeQ16, tc.cb.invQuantStepSizeQ6, tc.muQ20, order)
			if gotRD != want[i].rdQ25 {
				t.Fatalf("RD_Q25=%d want %d", gotRD, want[i].rdQ25)
			}
			for j := 0; j < order; j++ {
				if gotIndices[j] != want[i].indices[j] {
					t.Fatalf("indices[%d]=%d want %d", j, gotIndices[j], want[i].indices[j])
				}
			}
		})
	}
}

func TestSILKNLSFEncodeMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name       string
		cb         *nlsfCB
		muQ20      int32
		nSurvivors int
		signalType int
		nlsf       []int16
		weight     []int16
	}{
		{name: "nbmb_unvoiced_four", cb: &silk_NLSF_CB_NB_MB, muQ20: 3146, nSurvivors: 4, signalType: typeUnvoiced, nlsf: []int16{2676, 3684, 7247, 12558, 14555, 16405, 18875, 19753, 26306, 27425}},
		{name: "nbmb_inactive_two_clustered", cb: &silk_NLSF_CB_NB_MB, muQ20: 1200, nSurvivors: 2, signalType: typeNoVoiceActivity, nlsf: []int16{128, 256, 512, 1024, 2048, 4096, 8192, 16384, 24576, 32640}},
		{name: "wb_voiced_eight", cb: &silk_NLSF_CB_WB, muQ20: 3146, nSurvivors: 8, signalType: typeVoiced, nlsf: []int16{1200, 2600, 4300, 6100, 8200, 10100, 12200, 14300, 16400, 18600, 20700, 22800, 24900, 27000, 29100, 31200}},
		{name: "wb_voiced_sixteen_tight_edges", cb: &silk_NLSF_CB_WB, muQ20: 900, nSurvivors: 16, signalType: typeVoiced, nlsf: []int16{1, 2, 4, 8, 16, 64, 256, 1024, 4096, 8192, 12288, 16384, 24576, 28672, 32765, 32766}},
	}
	records := make([][]uint32, len(cases))
	for i := range cases {
		cases[i].weight = make([]int16, cases[i].cb.order)
		silkNLSFWeightsLaroia(cases[i].weight, cases[i].nlsf, cases[i].cb.order)
		record := []uint32{
			cbIDForNLSF(cases[i].cb),
			uint32FromInt32(cases[i].muQ20),
			uint32(cases[i].nSurvivors),
			uint32(cases[i].signalType),
		}
		for _, v := range cases[i].nlsf {
			record = append(record, uint32FromInt32(int32(v)))
		}
		for _, v := range cases[i].weight {
			record = append(record, uint32FromInt32(int32(v)))
		}
		records[i] = record
	}
	want, err := probeLibopusSILKNLSFEncode(records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk nlsf encode", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			order := tc.cb.order
			gotNLSF := append([]int16(nil), tc.nlsf...)
			enc := &Encoder{}
			gotStage1, gotResiduals, gotRD := enc.nlsfEncode(gotNLSF, tc.cb, tc.weight, tc.muQ20, tc.nSurvivors, tc.signalType)
			if gotRD != want[i].rdQ25 {
				t.Fatalf("RD_Q25=%d want %d", gotRD, want[i].rdQ25)
			}
			gotIndices := make([]int8, order+1)
			gotIndices[0] = int8(gotStage1)
			for j := 0; j < order; j++ {
				gotIndices[j+1] = int8(gotResiduals[j])
			}
			if !sameInt8s(gotIndices, want[i].indices) {
				t.Fatalf("indices=%v want %v", gotIndices, want[i].indices)
			}
			if !sameInt16s(gotNLSF[:order], want[i].nlsf) {
				t.Fatalf("quantized NLSF=%v want %v", gotNLSF[:order], want[i].nlsf)
			}
		})
	}
}

func TestSILKNLSF2AMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name string
		nlsf []int16
	}{
		{name: "nbmb_frame0", nlsf: []int16{2676, 3684, 7247, 12558, 14555, 16405, 18875, 19753, 26306, 27425}},
		{name: "nbmb_interpolated", nlsf: []int16{2682, 3603, 6874, 12676, 14282, 16142, 18786, 19989, 26467, 27307}},
		{name: "wb_even", nlsf: []int16{1200, 2600, 4300, 6100, 8200, 10100, 12200, 14300, 16400, 18600, 20700, 22800, 24900, 27000, 29100, 31200}},
		{name: "wb_clustered", nlsf: []int16{900, 1500, 2800, 5200, 7400, 9300, 11800, 14200, 16800, 19000, 21100, 23000, 25200, 27500, 29800, 31800}},
	}

	records := make([][]uint32, len(cases))
	for i, tc := range cases {
		record := []uint32{uint32(len(tc.nlsf))}
		for _, v := range tc.nlsf {
			record = append(record, uint32FromInt32(int32(v)))
		}
		records[i] = record
	}
	want, err := probeLibopusSILKNLSF(libopusSILKNLSFModeNLSF2A, records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk nlsf", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := make([]int16, len(tc.nlsf))
			if ok := silkNLSF2A(got, tc.nlsf, len(tc.nlsf)); !ok {
				t.Fatal("silkNLSF2A returned false")
			}
			if !sameInt16s(got, want[i]) {
				t.Fatalf("silkNLSF2A=%v want %v", got, want[i])
			}
		})
	}
}

func TestSILKA2NLSFMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name  string
		aQ16  []int32
		order int
	}{
		{name: "order10_speech_like", order: 10, aQ16: speechLikeA2NLSFInput(10, 0.80)},
		{name: "order16_speech_like", order: 16, aQ16: speechLikeA2NLSFInput(16, 0.85)},
		{name: "order10_gentle", order: 10, aQ16: []int32{8192, -4096, 2731, -2048, 1638, -1365, 1170, -1024, 910, -819}},
	}

	records := make([][]uint32, len(cases))
	for i, tc := range cases {
		record := []uint32{uint32(tc.order)}
		for _, v := range tc.aQ16 {
			record = append(record, uint32FromInt32(v))
		}
		records[i] = record
	}
	want, err := probeLibopusSILKNLSF(libopusSILKNLSFModeA2NLSF, records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk nlsf", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := make([]int16, tc.order)
			aQ16 := append([]int32(nil), tc.aQ16...)
			silkA2NLSF(got, aQ16, tc.order)
			if !sameInt16s(got, want[i]) {
				t.Fatalf("silkA2NLSF=%v want %v", got, want[i])
			}
		})
	}
}

func TestSILKNLSFStabilizeMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name string
		cb   *nlsfCB
		nlsf []int16
	}{
		{name: "nbmb_overlap", cb: &silk_NLSF_CB_NB_MB, nlsf: []int16{2701, 3363, 5756, 13031, 13464, 15353, 18521, 20697, 27019, 26883}},
		{name: "nbmb_fallback", cb: &silk_NLSF_CB_NB_MB, nlsf: []int16{32000, 31500, 31000, 30000, 29000, 28000, 25000, 20000, 12000, 1000}},
		{name: "wb_overlap", cb: &silk_NLSF_CB_WB, nlsf: []int16{800, 1200, 1400, 1600, 3000, 5000, 7000, 9000, 12000, 15000, 18000, 21000, 24000, 27000, 30000, 31900}},
	}

	records := make([][]uint32, len(cases))
	for i, tc := range cases {
		record := []uint32{cbIDForNLSF(tc.cb)}
		for _, v := range tc.nlsf {
			record = append(record, uint32FromInt32(int32(v)))
		}
		records[i] = record
	}
	want, err := probeLibopusSILKNLSF(libopusSILKNLSFModeStabilize, records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk nlsf", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := append([]int16(nil), tc.nlsf...)
			silkNLSFStabilize(got, tc.cb.deltaMinQ15, tc.cb.order)
			if !sameInt16s(got, want[i]) {
				t.Fatalf("silkNLSFStabilize=%v want %v", got, want[i])
			}
		})
	}
}

func speechLikeA2NLSFInput(order int, decay float64) []int32 {
	aQ16 := make([]int32, order)
	for i := 0; i < order; i++ {
		aQ16[i] = int32(float64(1<<15) * math.Pow(decay, float64(i+1)))
		if i%2 == 1 {
			aQ16[i] = -aQ16[i]
		}
	}
	return aQ16
}

func sameInt16s(a, b []int16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameInt8s(a, b []int8) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
