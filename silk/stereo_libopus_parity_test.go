package silk

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKStereoInputMagic  = "GSSI"
	libopusSILKStereoOutputMagic = "GSSO"

	libopusSILKStereoModeQuantPred     = uint32(0)
	libopusSILKStereoModeFindPredictor = uint32(1)
)

var (
	libopusSILKStereoHelperOnce sync.Once
	libopusSILKStereoHelperPath string
	libopusSILKStereoHelperErr  error
)

type libopusSILKStereoRecord struct {
	first  int32
	second int32
	extra  [6]int32
}

func buildLibopusSILKStereoHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:        "silk stereo",
		OutputBase:   "gopus_libopus_silk_stereo",
		SourceFile:   "libopus_silk_stereo_info.c",
		ProbeRelPath: "silk/main.h",
		CFlags:       []string{"-DHAVE_CONFIG_H"},
		RefIncludes:  []string{"celt", "silk"},
		RefSources: []string{
			"silk/stereo_quant_pred.c",
			"silk/stereo_find_predictor.c",
			"silk/sum_sqr_shift.c",
			"silk/inner_prod_aligned.c",
			"silk/tables_other.c",
		},
	})
}

func getLibopusSILKStereoHelperPath() (string, error) {
	libopusSILKStereoHelperOnce.Do(func() {
		libopusSILKStereoHelperPath, libopusSILKStereoHelperErr = buildLibopusSILKStereoHelper()
	})
	if libopusSILKStereoHelperErr != nil {
		return "", libopusSILKStereoHelperErr
	}
	return libopusSILKStereoHelperPath, nil
}

func probeLibopusSILKStereo(mode uint32, records [][]int32) ([]libopusSILKStereoRecord, error) {
	binPath, err := getLibopusSILKStereoHelperPath()
	if err != nil {
		return nil, err
	}
	var payload bytes.Buffer
	payload.WriteString(libopusSILKStereoInputMagic)
	for _, v := range []uint32{1, mode, uint32(len(records))} {
		if err := binary.Write(&payload, binary.LittleEndian, v); err != nil {
			return nil, err
		}
	}
	for _, record := range records {
		for _, word := range record {
			if err := binary.Write(&payload, binary.LittleEndian, uint32(word)); err != nil {
				return nil, err
			}
		}
	}

	data, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return nil, fmt.Errorf("run silk stereo helper: %w", err)
	}
	if len(data) < 12 || string(data[:4]) != libopusSILKStereoOutputMagic {
		return nil, fmt.Errorf("unexpected silk stereo helper output")
	}
	count := int(binary.LittleEndian.Uint32(data[8:12]))
	if count != len(records) {
		return nil, fmt.Errorf("helper count=%d want %d", count, len(records))
	}
	wantLen := 12 + count*32
	if len(data) != wantLen {
		return nil, fmt.Errorf("helper output length=%d want %d", len(data), wantLen)
	}
	out := make([]libopusSILKStereoRecord, count)
	offset := 12
	for i := range out {
		out[i].first = int32(binary.LittleEndian.Uint32(data[offset:]))
		offset += 4
		out[i].second = int32(binary.LittleEndian.Uint32(data[offset:]))
		offset += 4
		for j := range out[i].extra {
			out[i].extra[j] = int32(binary.LittleEndian.Uint32(data[offset:]))
			offset += 4
		}
	}
	return out, nil
}

func TestSILKStereoQuantPredMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name string
		pred [2]int32
	}{
		{name: "zero", pred: [2]int32{0, 0}},
		{name: "positive", pred: [2]int32{5000, 2000}},
		{name: "negative", pred: [2]int32{-5000, -2000}},
		{name: "mixed", pred: [2]int32{3000, -3000}},
		{name: "clipped_high", pred: [2]int32{16384, 13732}},
		{name: "clipped_low", pred: [2]int32{-16384, -13732}},
	}

	records := make([][]int32, len(cases))
	for i, tc := range cases {
		records[i] = []int32{tc.pred[0], tc.pred[1]}
	}
	want, err := probeLibopusSILKStereo(libopusSILKStereoModeQuantPred, records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk stereo", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pred := tc.pred
			ix := stereoQuantPred(&pred)
			if pred[0] != want[i].first || pred[1] != want[i].second {
				t.Fatalf("pred=%v want [%d %d]", pred, want[i].first, want[i].second)
			}
			got := [6]int32{
				int32(ix.Ix[0][0]), int32(ix.Ix[0][1]), int32(ix.Ix[0][2]),
				int32(ix.Ix[1][0]), int32(ix.Ix[1][1]), int32(ix.Ix[1][2]),
			}
			if got != want[i].extra {
				t.Fatalf("ix=%v want %v", got, want[i].extra)
			}
		})
	}
}

func TestSILKStereoFindPredictorMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name      string
		x         []int16
		y         []int16
		midResAmp [2]int32
		smooth    int32
	}{
		{name: "silent_target", x: stereoRamp(32, 40, 17), y: make([]int16, 32), midResAmp: [2]int32{100, 20}, smooth: 4096},
		{name: "positive_corr", x: stereoRamp(40, -320, 23), y: stereoScaledRamp(40, -320, 23, 2, 5), midResAmp: [2]int32{400, 100}, smooth: 8192},
		{name: "negative_corr", x: stereoRamp(48, 250, -11), y: stereoScaledRamp(48, 250, -11, -3, 7), midResAmp: [2]int32{1200, 500}, smooth: 2048},
	}

	records := make([][]int32, len(cases))
	for i, tc := range cases {
		record := []int32{int32(len(tc.x)), tc.midResAmp[0], tc.midResAmp[1], tc.smooth}
		for _, v := range tc.x {
			record = append(record, int32(v))
		}
		for _, v := range tc.y {
			record = append(record, int32(v))
		}
		records[i] = record
	}
	want, err := probeLibopusSILKStereo(libopusSILKStereoModeFindPredictor, records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk stereo", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			midResAmp := tc.midResAmp
			gotPred, gotRatio := stereoFindPredictorQ13WithRatioQ14(tc.x, tc.y, len(tc.x), &midResAmp, tc.smooth)
			if gotPred != want[i].first || gotRatio != want[i].second {
				t.Fatalf("pred/ratio=%d/%d want %d/%d", gotPred, gotRatio, want[i].first, want[i].second)
			}
			if midResAmp[0] != want[i].extra[0] || midResAmp[1] != want[i].extra[1] {
				t.Fatalf("midResAmp=%v want [%d %d]", midResAmp, want[i].extra[0], want[i].extra[1])
			}
		})
	}
}

func stereoRamp(n int, start, step int16) []int16 {
	out := make([]int16, n)
	v := int32(start)
	for i := range out {
		out[i] = int16(v)
		v += int32(step)
	}
	return out
}

func stereoScaledRamp(n int, start, step, num, den int16) []int16 {
	out := make([]int16, n)
	v := int32(start)
	for i := range out {
		out[i] = int16((v * int32(num)) / int32(den))
		v += int32(step)
	}
	return out
}
