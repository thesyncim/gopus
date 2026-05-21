package silk

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKStereoInputMagic  = "GSSI"
	libopusSILKStereoOutputMagic = "GSSO"

	libopusSILKStereoModeQuantPred     = uint32(0)
	libopusSILKStereoModeFindPredictor = uint32(1)
	libopusSILKStereoModeLRToMS        = uint32(2)
)

var libopusSILKStereoHelper libopustest.HelperCache

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
			"silk/stereo_LR_to_MS.c",
			"silk/sum_sqr_shift.c",
			"silk/inner_prod_aligned.c",
			"silk/tables_other.c",
		},
	})
}

type libopusSILKLRToMSRecord struct {
	midOnly  int32
	midRate  int32
	sideRate int32
	ix       [6]int32
	state    libopusSILKStereoState
	mid      []int16
	side     []int16
}

type libopusSILKStereoState struct {
	predPrevQ13   [2]int32
	sMid          [2]int32
	sSide         [2]int32
	midSideAmpQ0  [4]int32
	smthWidthQ14  int32
	widthPrevQ14  int32
	silentSideLen int32
}

func getLibopusSILKStereoHelperPath() (string, error) {
	return libopusSILKStereoHelper.Path(buildLibopusSILKStereoHelper)
}

func probeLibopusSILKStereo(mode uint32, records [][]int32) ([]libopusSILKStereoRecord, error) {
	binPath, err := getLibopusSILKStereoHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKStereoInputMagic, mode, uint32(len(records)))
	for _, record := range records {
		for _, word := range record {
			payload.I32(word)
		}
	}

	data, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return nil, fmt.Errorf("run silk stereo helper: %w", err)
	}
	reader, err := libopustest.NewOracleReader("silk stereo", libopusSILKStereoOutputMagic, data)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(records))
	reader.ExpectRemaining(count * 32)
	out := make([]libopusSILKStereoRecord, count)
	for i := range out {
		out[i].first = reader.I32()
		out[i].second = reader.I32()
		for j := range out[i].extra {
			out[i].extra[j] = reader.I32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func probeLibopusSILKLRToMS(records [][]int32, frameLengths []int) ([]libopusSILKLRToMSRecord, error) {
	binPath, err := getLibopusSILKStereoHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKStereoInputMagic, libopusSILKStereoModeLRToMS, uint32(len(records)))
	for _, record := range records {
		for _, word := range record {
			payload.I32(word)
		}
	}

	data, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return nil, fmt.Errorf("run silk lr-to-ms helper: %w", err)
	}
	reader, err := libopustest.NewOracleReader("silk lr-to-ms", libopusSILKStereoOutputMagic, data)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(records))
	out := make([]libopusSILKLRToMSRecord, count)
	for i := range out {
		out[i].midOnly = reader.I32()
		out[i].midRate = reader.I32()
		out[i].sideRate = reader.I32()
		for j := range out[i].ix {
			out[i].ix[j] = reader.I32()
		}
		for j := range out[i].state.predPrevQ13 {
			out[i].state.predPrevQ13[j] = reader.I32()
		}
		for j := range out[i].state.sMid {
			out[i].state.sMid[j] = reader.I32()
		}
		for j := range out[i].state.sSide {
			out[i].state.sSide[j] = reader.I32()
		}
		for j := range out[i].state.midSideAmpQ0 {
			out[i].state.midSideAmpQ0[j] = reader.I32()
		}
		out[i].state.smthWidthQ14 = reader.I32()
		out[i].state.widthPrevQ14 = reader.I32()
		out[i].state.silentSideLen = reader.I32()
		out[i].mid = make([]int16, frameLengths[i])
		out[i].side = make([]int16, frameLengths[i])
		for j := range out[i].mid {
			out[i].mid[j] = int16(reader.I32())
		}
		for j := range out[i].side {
			out[i].side[j] = int16(reader.I32())
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
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

func TestSILKStereoLRToMSMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name         string
		frameLength  int
		fsKHz        int
		totalRateBps int
		speechActQ8  int
		toMono       bool
		state        libopusSILKStereoState
		left         []int16
		right        []int16
	}{
		{
			name:         "full_width_20ms",
			frameLength:  320,
			fsKHz:        16,
			totalRateBps: 32000,
			speechActQ8:  180,
			state: libopusSILKStereoState{
				midSideAmpQ0: [4]int32{0, 1, 0, 1},
				smthWidthQ14: 16384,
				widthPrevQ14: 16384,
			},
			left:  stereoWave(320, 1200, 37, 23),
			right: stereoWave(320, -900, 29, 17),
		},
		{
			name:         "reduced_width_10ms",
			frameLength:  160,
			fsKHz:        16,
			totalRateBps: 13200,
			speechActQ8:  96,
			state: libopusSILKStereoState{
				predPrevQ13:  [2]int32{360, -120},
				sMid:         [2]int32{11, -12},
				sSide:        [2]int32{7, -5},
				midSideAmpQ0: [4]int32{300, 80, 70, 20},
				smthWidthQ14: 6000,
				widthPrevQ14: 5000,
			},
			left:  stereoWave(160, 450, 43, 19),
			right: stereoWave(160, 420, 41, 13),
		},
		{
			name:         "to_mono_transition",
			frameLength:  320,
			fsKHz:        16,
			totalRateBps: 18000,
			speechActQ8:  140,
			toMono:       true,
			state: libopusSILKStereoState{
				predPrevQ13:  [2]int32{420, 90},
				midSideAmpQ0: [4]int32{700, 300, 180, 90},
				smthWidthQ14: 10000,
				widthPrevQ14: 12000,
			},
			left:  stereoWave(320, -600, 31, 11),
			right: stereoWave(320, 700, 27, 7),
		},
	}

	records := make([][]int32, len(cases))
	frameLengths := make([]int, len(cases))
	for i, tc := range cases {
		frameLengths[i] = tc.frameLength
		record := []int32{
			int32(tc.frameLength), int32(tc.fsKHz), int32(tc.totalRateBps),
			int32(tc.speechActQ8), boolWord(tc.toMono),
			tc.state.predPrevQ13[0], tc.state.predPrevQ13[1],
			tc.state.sMid[0], tc.state.sMid[1],
			tc.state.sSide[0], tc.state.sSide[1],
		}
		record = append(record, tc.state.midSideAmpQ0[:]...)
		record = append(record, tc.state.smthWidthQ14, tc.state.widthPrevQ14, tc.state.silentSideLen)
		for _, v := range tc.left {
			record = append(record, int32(v))
		}
		for _, v := range tc.right {
			record = append(record, int32(v))
		}
		records[i] = record
	}
	want, err := probeLibopusSILKLRToMS(records, frameLengths)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk lr-to-ms", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var enc Encoder
			setStereoStateFromOracle(&enc.stereo, tc.state)
			mid, side, ix, midOnly, midRate, sideRate, widthQ14 := enc.StereoLRToMSWithRates(
				int16PCMToFloat32(tc.left), int16PCMToFloat32(tc.right),
				tc.frameLength, tc.fsKHz, tc.totalRateBps, tc.speechActQ8, tc.toMono,
			)
			if boolWord(midOnly) != want[i].midOnly {
				t.Fatalf("midOnly=%v want %d", midOnly, want[i].midOnly)
			}
			if int32(midRate) != want[i].midRate || int32(sideRate) != want[i].sideRate {
				t.Fatalf("rates=%d/%d want %d/%d", midRate, sideRate, want[i].midRate, want[i].sideRate)
			}
			if int32(widthQ14) != want[i].state.widthPrevQ14 {
				t.Fatalf("widthQ14=%d want %d", widthQ14, want[i].state.widthPrevQ14)
			}
			gotIx := [6]int32{
				int32(ix.Ix[0][0]), int32(ix.Ix[0][1]), int32(ix.Ix[0][2]),
				int32(ix.Ix[1][0]), int32(ix.Ix[1][1]), int32(ix.Ix[1][2]),
			}
			if gotIx != want[i].ix {
				t.Fatalf("ix=%v want %v", gotIx, want[i].ix)
			}
			gotState := stereoStateForOracle(enc.stereo)
			if gotState != want[i].state {
				t.Fatalf("state=%+v want %+v", gotState, want[i].state)
			}
			if !samePCM16FromFloat(mid, want[i].mid) {
				t.Fatalf("mid output mismatch")
			}
			if !samePCM16FromFloat(side, want[i].side) {
				t.Fatalf("side output mismatch")
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

func stereoWave(n int, offset, step, wobble int16) []int16 {
	out := make([]int16, n)
	v := int32(offset)
	for i := range out {
		v += int32(step)
		if i%5 == 0 {
			v -= int32(wobble) * 3
		} else {
			v += int32(wobble)
		}
		if v > 18000 {
			v -= 24000
		}
		if v < -18000 {
			v += 24000
		}
		out[i] = int16(v)
	}
	return out
}

func int16PCMToFloat32(in []int16) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v) / 32768.0
	}
	return out
}

func pcmFloat32ToInt16Exact(v float32) int16 {
	return int16(int32(v * 32768.0))
}

func samePCM16FromFloat(got []float32, want []int16) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if pcmFloat32ToInt16Exact(got[i]) != want[i] {
			return false
		}
	}
	return true
}

func setStereoStateFromOracle(st *stereoEncState, src libopusSILKStereoState) {
	st.predPrevQ13 = src.predPrevQ13
	st.sMid = [2]int16{int16(src.sMid[0]), int16(src.sMid[1])}
	st.sSide = [2]int16{int16(src.sSide[0]), int16(src.sSide[1])}
	st.widthPrevQ14 = int16(src.widthPrevQ14)
	st.smthWidthQ14 = int16(src.smthWidthQ14)
	st.silentSideLen = src.silentSideLen
	for i := range st.midSideAmpQ0 {
		st.midSideAmpQ0[i] = float64(src.midSideAmpQ0[i])
	}
}

func stereoStateForOracle(st stereoEncState) libopusSILKStereoState {
	return libopusSILKStereoState{
		predPrevQ13:   st.predPrevQ13,
		sMid:          [2]int32{int32(st.sMid[0]), int32(st.sMid[1])},
		sSide:         [2]int32{int32(st.sSide[0]), int32(st.sSide[1])},
		midSideAmpQ0:  [4]int32{int32(st.midSideAmpQ0[0]), int32(st.midSideAmpQ0[1]), int32(st.midSideAmpQ0[2]), int32(st.midSideAmpQ0[3])},
		smthWidthQ14:  int32(st.smthWidthQ14),
		widthPrevQ14:  int32(st.widthPrevQ14),
		silentSideLen: st.silentSideLen,
	}
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
