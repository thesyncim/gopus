package silk

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKLTPInputMagic  = "GSLT"
	libopusSILKLTPOutputMagic = "GSLU"

	libopusSILKLTPModeQuant = uint32(0)
	libopusSILKLTPModeVQ    = uint32(1)
	libopusSILKLTPModePitch = uint32(2)
	libopusSILKLTPModeFind  = uint32(3)
)

var libopusSILKLTPHelper libopustest.HelperCache

type libopusSILKLTPQuantRecord struct {
	periodicityIndex int32
	sumLogGainQ7     int32
	predGainQ7       int32
	bQ14             [maxNbSubfr * ltpOrderConst]int16
	cbkIndex         [maxNbSubfr]int8
}

type libopusSILKLTPVQRecord struct {
	ind        int32
	resNrgQ15  int32
	rateDistQ8 int32
	gainQ7     int32
}

type libopusSILKLTPFindCase struct {
	name       string
	nbSubfr    int
	subfrLen   int
	resStart   int
	lags       [maxNbSubfr]int32
	residual32 []float32
}

type libopusSILKLTPFindRecord struct {
	XX []float32
	xX []float32
}

func buildLibopusSILKLTPHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:          "silk ltp",
		OutputBase:     "gopus_libopus_silk_ltp",
		SourceFile:     "libopus_silk_ltp_info.c",
		ProbeRelPath:   "silk/float/main_FLP.h",
		CFlags:         []string{"-DHAVE_CONFIG_H", "-ffp-contract=off"},
		RefIncludes:    []string{"celt", "silk", "silk/float"},
		// Compiles a subset of SILK .c files (no libopus.a link) that reach
		// silk_inner_product_FLP / silk_VQ_WMat_EC. Force the scalar _c kernels so
		// the SSE/AVX RTCD dispatch tables (absent from this subset) are not
		// referenced -- otherwise the link fails on amd64/Windows.
		ForceScalarRef: true,
		RefSources: []string{
			"silk/quant_LTP_gains.c",
			"silk/VQ_WMat_EC.c",
			"silk/decode_pitch.c",
			"silk/pitch_est_tables.c",
			"silk/tables_LTP.c",
			"silk/lin2log.c",
			"silk/log2lin.c",
			"silk/float/corrMatrix_FLP.c",
			"silk/float/energy_FLP.c",
			"silk/float/find_LTP_FLP.c",
			"silk/float/inner_product_FLP.c",
			"silk/float/scale_vector_FLP.c",
		},
	})
}

func getLibopusSILKLTPHelperPath() (string, error) {
	return libopusSILKLTPHelper.Path(buildLibopusSILKLTPHelper)
}

func probeLibopusSILKLTPQuant(records [][]int32) ([]libopusSILKLTPQuantRecord, error) {
	data, err := runLibopusSILKLTPHelper(libopusSILKLTPModeQuant, records)
	if err != nil {
		return nil, err
	}
	reader, err := libopustest.NewOracleReader("silk ltp", libopusSILKLTPOutputMagic, data)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(records))
	reader.ExpectRemaining(count * (3 + maxNbSubfr*ltpOrderConst + maxNbSubfr) * 4)
	out := make([]libopusSILKLTPQuantRecord, count)
	for i := range out {
		out[i].periodicityIndex = reader.I32()
		out[i].sumLogGainQ7 = reader.I32()
		out[i].predGainQ7 = reader.I32()
		for j := range out[i].bQ14 {
			out[i].bQ14[j] = int16(reader.I32())
		}
		for j := range out[i].cbkIndex {
			out[i].cbkIndex[j] = int8(reader.I32())
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusSILKLTPVQ(records [][]int32) ([]libopusSILKLTPVQRecord, error) {
	data, err := runLibopusSILKLTPHelper(libopusSILKLTPModeVQ, records)
	if err != nil {
		return nil, err
	}
	reader, err := libopustest.NewOracleReader("silk ltp", libopusSILKLTPOutputMagic, data)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(records))
	reader.ExpectRemaining(count * 16)
	out := make([]libopusSILKLTPVQRecord, count)
	for i := range out {
		out[i] = libopusSILKLTPVQRecord{
			ind:        reader.I32(),
			resNrgQ15:  reader.I32(),
			rateDistQ8: reader.I32(),
			gainQ7:     reader.I32(),
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusSILKDecodePitch(records [][]int32) ([][maxNbSubfr]int32, error) {
	data, err := runLibopusSILKLTPHelper(libopusSILKLTPModePitch, records)
	if err != nil {
		return nil, err
	}
	reader, err := libopustest.NewOracleReader("silk ltp", libopusSILKLTPOutputMagic, data)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(records))
	reader.ExpectRemaining(count * maxNbSubfr * 4)
	out := make([][maxNbSubfr]int32, count)
	for i := range out {
		for j := range out[i] {
			out[i][j] = reader.I32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusSILKFindLTPFLP(cases []libopusSILKLTPFindCase) ([]libopusSILKLTPFindRecord, error) {
	binPath, err := getLibopusSILKLTPHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKLTPInputMagic, libopusSILKLTPModeFind, uint32(len(cases)))
	for _, tc := range cases {
		payload.I32(int32(tc.nbSubfr))
		payload.I32(int32(tc.subfrLen))
		payload.I32(int32(tc.resStart))
		payload.I32(int32(len(tc.residual32)))
		for _, lag := range tc.lags {
			payload.I32(int32(lag))
		}
		payload.Float32s(tc.residual32...)
	}
	data, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return nil, fmt.Errorf("run silk find ltp helper: %w", err)
	}
	reader, err := libopustest.NewOracleReader("silk find ltp", libopusSILKLTPOutputMagic, data)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]libopusSILKLTPFindRecord, count)
	for i := range out {
		xxLen := cases[i].nbSubfr * ltpOrderConst * ltpOrderConst
		xXLen := cases[i].nbSubfr * ltpOrderConst
		out[i].XX = make([]float32, xxLen)
		out[i].xX = make([]float32, xXLen)
		for j := range out[i].XX {
			out[i].XX[j] = reader.Float32()
		}
		for j := range out[i].xX {
			out[i].xX[j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func runLibopusSILKLTPHelper(mode uint32, records [][]int32) ([]byte, error) {
	binPath, err := getLibopusSILKLTPHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKLTPInputMagic, mode, uint32(len(records)))
	for _, record := range records {
		for _, word := range record {
			payload.I32(word)
		}
	}
	data, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return nil, fmt.Errorf("run silk ltp helper: %w", err)
	}
	return data, nil
}

func TestSILKDecodePitchMatchesLibopusOracleExhaustive(t *testing.T) {
	libopustest.RequireOracle(t)
	type pitchCase struct {
		lagIndex     int
		contourIndex int
		fsKHz        int
		nbSubfr      int
	}
	cases := make([]pitchCase, 0, 24000)
	for _, fsKHz := range []int{8, 12, 16} {
		for _, nbSubfr := range []int{2, 4} {
			cbkSize := decodePitchContourCodebookSize(fsKHz, nbSubfr)
			maxLagIndex := (peMaxLagMs - peMinLagMs) * fsKHz
			for lagIndex := 0; lagIndex <= maxLagIndex; lagIndex++ {
				for contourIndex := 0; contourIndex < cbkSize; contourIndex++ {
					cases = append(cases, pitchCase{
						lagIndex:     lagIndex,
						contourIndex: contourIndex,
						fsKHz:        fsKHz,
						nbSubfr:      nbSubfr,
					})
				}
			}
		}
	}
	records := make([][]int32, len(cases))
	for i, tc := range cases {
		records[i] = []int32{
			int32(tc.lagIndex),
			int32(tc.contourIndex),
			int32(tc.fsKHz),
			int32(tc.nbSubfr),
		}
	}
	want, err := probeLibopusSILKDecodePitch(records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk decode pitch", err)
	}

	for i, tc := range cases {
		var got [maxNbSubfr]int32
		silkDecodePitch(int16(tc.lagIndex), int8(tc.contourIndex), got[:], tc.fsKHz, tc.nbSubfr)
		for sf := 0; sf < tc.nbSubfr; sf++ {
			if int32(got[sf]) != want[i][sf] {
				t.Fatalf("fs=%d nbSubfr=%d lagIndex=%d contour=%d subframe=%d got=%d want=%d",
					tc.fsKHz, tc.nbSubfr, tc.lagIndex, tc.contourIndex, sf, got[sf], want[i][sf])
			}
		}
	}
}

func TestSILKDecodePitchLagClampEdgesMatchLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	type pitchCase struct {
		name         string
		lagIndex     int
		contourIndex int
		fsKHz        int
		nbSubfr      int
	}
	var cases []pitchCase
	for _, fsKHz := range []int{8, 12, 16} {
		for _, nbSubfr := range []int{2, 4} {
			cbkSize := decodePitchContourCodebookSize(fsKHz, nbSubfr)
			maxLagIndex := (peMaxLagMs - peMinLagMs) * fsKHz
			contours := []int{0, cbkSize / 2, cbkSize - 1}
			lagIndices := []int{
				-32768,
				-maxLagIndex - 8,
				-2,
				-1,
				maxLagIndex + 1,
				maxLagIndex + 8,
				32767,
			}
			for _, lagIndex := range lagIndices {
				for _, contourIndex := range contours {
					cases = append(cases, pitchCase{
						name:         fmt.Sprintf("fs%d_subfr%d_lag%d_contour%d", fsKHz, nbSubfr, lagIndex, contourIndex),
						lagIndex:     lagIndex,
						contourIndex: contourIndex,
						fsKHz:        fsKHz,
						nbSubfr:      nbSubfr,
					})
				}
			}
		}
	}

	records := make([][]int32, len(cases))
	for i, tc := range cases {
		records[i] = []int32{
			int32(tc.lagIndex),
			int32(tc.contourIndex),
			int32(tc.fsKHz),
			int32(tc.nbSubfr),
		}
	}
	want, err := probeLibopusSILKDecodePitch(records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk decode pitch", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got [maxNbSubfr]int32
			silkDecodePitch(int16(tc.lagIndex), int8(tc.contourIndex), got[:], tc.fsKHz, tc.nbSubfr)
			for sf := 0; sf < tc.nbSubfr; sf++ {
				if int32(got[sf]) != want[i][sf] {
					t.Fatalf("subframe=%d got=%d want=%d", sf, got[sf], want[i][sf])
				}
			}
		})
	}
}

func TestSILKFindLTPFLPMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusSILKLTPFindCase{
		makeLibopusSILKFindLTPCase("two_subframes_40", 2, 40, 64, [maxNbSubfr]int32{18, 22}, 0x10203040, 512),
		makeLibopusSILKFindLTPCase("four_subframes_80", 4, 80, 120, [maxNbSubfr]int32{32, 45, 50, 28}, 0x20304050, 4096),
		makeLibopusSILKFindLTPCase("four_subframes_120", 4, 120, 160, [maxNbSubfr]int32{56, 72, 40, 88}, 0x30405060, 32768),
	}
	want, err := probeLibopusSILKFindLTPFLP(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk find ltp", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotXX := make([]float32, tc.nbSubfr*ltpOrderConst*ltpOrderConst)
			gotxX := make([]float32, tc.nbSubfr*ltpOrderConst)
			findLTPFLP(gotXX, gotxX, tc.residual32, tc.resStart, tc.lags[:tc.nbSubfr], tc.subfrLen, tc.nbSubfr)
			for j := range gotXX {
				if math.Float32bits(gotXX[j]) != math.Float32bits(want[i].XX[j]) {
					t.Fatalf("XX[%d]=%08x %.10g want %08x %.10g",
						j,
						math.Float32bits(gotXX[j]), gotXX[j],
						math.Float32bits(want[i].XX[j]), want[i].XX[j])
				}
			}
			for j := range gotxX {
				if math.Float32bits(gotxX[j]) != math.Float32bits(want[i].xX[j]) {
					t.Fatalf("xX[%d]=%08x %.10g want %08x %.10g",
						j,
						math.Float32bits(gotxX[j]), gotxX[j],
						math.Float32bits(want[i].xX[j]), want[i].xX[j])
				}
			}
		})
	}
}

func decodePitchContourCodebookSize(fsKHz, nbSubfr int) int {
	if fsKHz == 8 {
		if nbSubfr == peMaxNbSubfr {
			return peNbCbksStage2Ext
		}
		return peNbCbksStage2_10ms
	}
	if nbSubfr == peMaxNbSubfr {
		return peNbCbksStage3Max
	}
	return peNbCbksStage3_10ms
}

func makeLibopusSILKFindLTPCase(name string, nbSubfr, subfrLen, resStart int, lags [maxNbSubfr]int32, seed uint32, scale float32) libopusSILKLTPFindCase {
	residualLen := resStart + nbSubfr*subfrLen + ltpOrderConst
	return libopusSILKLTPFindCase{
		name:       name,
		nbSubfr:    nbSubfr,
		subfrLen:   subfrLen,
		resStart:   resStart,
		lags:       lags,
		residual32: ltpFindResidualPattern(residualLen, seed, scale),
	}
}

func ltpFindResidualPattern(n int, seed uint32, scale float32) []float32 {
	out := make([]float32, n)
	state := seed
	for i := range out {
		state = state*1664525 + 1013904223
		noise := float32(int32(state>>8)&0xffff-32768) / 32768.0
		ramp := float32((i%17)-8) * (1.0 / 32.0)
		out[i] = scale * (0.75*noise + ramp)
	}
	return out
}

func TestSILKLTPVQMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name       string
		cbk        int
		subfrLen   int
		maxGainQ7  int32
		xxSeed     int
		xXSeed     int
		xXNegative bool
	}{
		{name: "low_codebook_small_gain", cbk: 0, subfrLen: 40, maxGainQ7: 60, xxSeed: 3, xXSeed: 5},
		{name: "mid_codebook_penalty", cbk: 1, subfrLen: 80, maxGainQ7: 24, xxSeed: 11, xXSeed: 7},
		{name: "high_codebook_negative_corr", cbk: 2, subfrLen: 120, maxGainQ7: 110, xxSeed: 19, xXSeed: 13, xXNegative: true},
	}

	records := make([][]int32, len(cases))
	for i, tc := range cases {
		xx := ltpXXQ17Pattern(tc.xxSeed)
		xX := ltpXxQ17Pattern(tc.xXSeed, tc.xXNegative)
		record := []int32{int32(tc.cbk), int32(tc.subfrLen), tc.maxGainQ7}
		record = append(record, xx[:]...)
		record = append(record, xX[:]...)
		records[i] = record
	}
	want, err := probeLibopusSILKLTPVQ(records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk ltp", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			xx := ltpXXQ17Pattern(tc.xxSeed)
			xX := ltpXxQ17Pattern(tc.xXSeed, tc.xXNegative)
			var ind int8
			var resNrgQ15, rateDistQ8, gainQ7 int32
			silkVQWMatEC(&ind, &resNrgQ15, &rateDistQ8, &gainQ7,
				xx[:], xX[:],
				silk_LTP_vq_ptrs_Q7[tc.cbk],
				silk_LTP_vq_gain_ptrs_Q7[tc.cbk],
				silk_LTP_gain_BITS_Q5_ptrs[tc.cbk],
				tc.subfrLen, tc.maxGainQ7, int(silk_LTP_vq_sizes[tc.cbk]))
			got := libopusSILKLTPVQRecord{
				ind:        int32(ind),
				resNrgQ15:  resNrgQ15,
				rateDistQ8: rateDistQ8,
				gainQ7:     gainQ7,
			}
			if got != want[i] {
				t.Fatalf("vq=%+v want %+v", got, want[i])
			}
		})
	}
}

func TestSILKQuantLTPGainsMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name         string
		nbSubfr      int
		subfrLen     int
		sumLogGainQ7 int32
		seed         int
	}{
		{name: "two_subframes_low_history", nbSubfr: 2, subfrLen: 40, sumLogGainQ7: 0, seed: 4},
		{name: "four_subframes_medium_history", nbSubfr: 4, subfrLen: 80, sumLogGainQ7: 384, seed: 9},
		{name: "four_subframes_high_history", nbSubfr: 4, subfrLen: 120, sumLogGainQ7: 1400, seed: 15},
	}

	records := make([][]int32, len(cases))
	for i, tc := range cases {
		xx, xX := ltpQuantInputs(tc.nbSubfr, tc.seed)
		record := []int32{int32(tc.nbSubfr), int32(tc.subfrLen), tc.sumLogGainQ7}
		record = append(record, xx...)
		record = append(record, xX...)
		records[i] = record
	}
	want, err := probeLibopusSILKLTPQuant(records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk ltp", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			xx, xX := ltpQuantInputs(tc.nbSubfr, tc.seed)
			var bQ14 [maxNbSubfr * ltpOrderConst]int16
			var cbkIndex [maxNbSubfr]int8
			periodicityIndex := int8(0)
			sumLogGainQ7 := tc.sumLogGainQ7
			predGainQ7 := int32(0)
			silkQuantLTPGains(bQ14[:], cbkIndex[:], &periodicityIndex, &sumLogGainQ7, &predGainQ7,
				xx, xX, tc.subfrLen, tc.nbSubfr)
			got := libopusSILKLTPQuantRecord{
				periodicityIndex: int32(periodicityIndex),
				sumLogGainQ7:     sumLogGainQ7,
				predGainQ7:       predGainQ7,
				bQ14:             bQ14,
				cbkIndex:         cbkIndex,
			}
			if got != want[i] {
				t.Fatalf("quant=%+v want %+v", got, want[i])
			}
		})
	}
}

func ltpQuantInputs(nbSubfr, seed int) ([]int32, []int32) {
	xx := make([]int32, nbSubfr*ltpOrderConst*ltpOrderConst)
	xX := make([]int32, nbSubfr*ltpOrderConst)
	for sf := 0; sf < nbSubfr; sf++ {
		xxSub := ltpXXQ17Pattern(seed + sf*7)
		xXSub := ltpXxQ17Pattern(seed+sf*11, sf%2 == 1)
		copy(xx[sf*ltpOrderConst*ltpOrderConst:], xxSub[:])
		copy(xX[sf*ltpOrderConst:], xXSub[:])
	}
	return xx, xX
}

func ltpXXQ17Pattern(seed int) [ltpOrderConst * ltpOrderConst]int32 {
	var out [ltpOrderConst * ltpOrderConst]int32
	for r := 0; r < ltpOrderConst; r++ {
		for c := 0; c < ltpOrderConst; c++ {
			idx := r*ltpOrderConst + c
			if r == c {
				out[idx] = int32(12000 + seed*97 + r*1300)
				continue
			}
			v := int32(((seed+1)*(r+2)*(c+3)*37)%1700 + 80)
			if (r+c+seed)%2 == 1 {
				v = -v
			}
			out[idx] = v
		}
	}
	return out
}

func ltpXxQ17Pattern(seed int, negative bool) [ltpOrderConst]int32 {
	var out [ltpOrderConst]int32
	for i := range out {
		v := int32(((seed+3)*(i+5)*211)%9000 + 300)
		if negative || (seed+i)%3 == 0 {
			v = -v
		}
		out[i] = v
	}
	return out
}
