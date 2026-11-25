package silk

import (
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/internal/util"
	"math"
	"slices"
	"testing"
	"unsafe"
)

const (
	libopusSILKStereoInputMagic  = "GSSI"
	libopusSILKStereoOutputMagic = "GSSO"

	libopusSILKStereoModeQuantPred     = uint32(0)
	libopusSILKStereoModeFindPredictor = uint32(1)
	libopusSILKStereoModeLRToMS        = uint32(2)
	libopusSILKStereoModeStateSizes    = uint32(3)
	libopusSILKStereoModePacket0       = uint32(4)

	libopusSILKPacket0WrapperWords = 130
	libopusSILKPacket0PulseBlocks  = maxFrameLength / shellCodecFrameLength
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
		Libs:         []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
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

type libopusSILKPacket0WrapperRecord struct {
	targetRateBps         int32
	midTargetRateBps      int32
	sideTargetRateBps     int32
	midVAD                int32
	sideVAD               int32
	midSpeechActivityQ8   int32
	sideSpeechActivityQ8  int32
	midInputTiltQ15       int32
	sideInputTiltQ15      int32
	midSNRDBQ7            int32
	sideSNRDBQ7           int32
	maxBits               int32
	useCBR                int32
	condCoding            int32
	tellAfterSideInfo     int32
	rangeAfterSideInfo    int32
	midOnly               int32
	midEncodeRet          int32
	midNBytesOut          int32
	midTellAfterFrame     int32
	midRangeAfterFrame    int32
	midLastGainIndex      int32
	midSignalType         int32
	midQuantOffsetType    int32
	midSeed               int32
	midFrameCounter       int32
	midPrevSignalType     int32
	midPrevLag            int32
	midNFramesEncoded     int32
	midInputQualityBands  [4]int32
	midLagIndex           int32
	midContourIndex       int32
	midNLSFInterpCoefQ2   int32
	midPERIndex           int32
	midLTPScaleIndex      int32
	midGainsIndices       [maxNbSubfr]int32
	midLTPIndices         [maxNbSubfr]int32
	midNLSFIndices        [maxLPCOrder + 1]int32
	midPulseAbsSum        int32
	midPulseHash          int32
	midPulseBlockCount    int32
	midPulseBlockAbsSum   [libopusSILKPacket0PulseBlocks]int32
	midGainTraceValid     int32
	midGainsPreQ16        [maxNbSubfr]int32
	midResNrgBits         [maxNbSubfr]int32
	midGainsUnqQ16        [maxNbSubfr]int32
	midGainsQuantQ16      [maxNbSubfr]int32
	midPredGainBits       int32
	midPitchAutoCorr0Bits int32
	midPitchResNrgBits    int32
	midInputQualityBits   int32
	midCodingQualityBits  int32
	midManualTellIndices  int32
	midManualRangeIndices int32
	midManualTellPulses   int32
	midManualRangePulses  int32
	midIndexTraceTell     [encodeFrameIndexTracePointCount]int32
	midIndexTraceRange    [encodeFrameIndexTracePointCount]int32
	sideInfoTraceTell     [3]int32
	sideInfoTraceRange    [3]int32
}

func getLibopusSILKStereoHelperPath() (string, error) {
	return libopusSILKStereoHelper.Path(buildLibopusSILKStereoHelper)
}

func libopusStereoBoolWord(v bool) int32 {
	if v {
		return 1
	}
	return 0
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

func probeLibopusSILKPacket0Wrapper(signal []float32, bitRate, maxBits int, useCBR bool, payloadSizeMs int, activity int) (libopusSILKPacket0WrapperRecord, error) {
	binPath, err := getLibopusSILKStereoHelperPath()
	if err != nil {
		return libopusSILKPacket0WrapperRecord{}, err
	}
	if len(signal)%2 != 0 {
		return libopusSILKPacket0WrapperRecord{}, fmt.Errorf("stereo signal length must be even")
	}
	payload := libopustest.NewOraclePayload(libopusSILKStereoInputMagic, libopusSILKStereoModePacket0, 1)
	payload.I32(int32(len(signal) / 2))
	payload.I32(int32(bitRate))
	payload.I32(int32(maxBits))
	payload.I32(libopusStereoBoolWord(useCBR))
	payload.I32(int32(payloadSizeMs))
	payload.I32(int32(activity))
	for _, sample := range signal {
		payload.Float32(sample)
	}

	data, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return libopusSILKPacket0WrapperRecord{}, fmt.Errorf("run silk packet0 wrapper helper: %w", err)
	}
	reader, err := libopustest.NewOracleReader("silk packet0 wrapper", libopusSILKStereoOutputMagic, data)
	if err != nil {
		return libopusSILKPacket0WrapperRecord{}, err
	}
	count := reader.Count(1)
	reader.ExpectRemaining(count * libopusSILKPacket0WrapperWords * 4)
	out := libopusSILKPacket0WrapperRecord{
		targetRateBps:        reader.I32(),
		midTargetRateBps:     reader.I32(),
		sideTargetRateBps:    reader.I32(),
		midVAD:               reader.I32(),
		sideVAD:              reader.I32(),
		midSpeechActivityQ8:  reader.I32(),
		sideSpeechActivityQ8: reader.I32(),
		midInputTiltQ15:      reader.I32(),
		sideInputTiltQ15:     reader.I32(),
		midSNRDBQ7:           reader.I32(),
		sideSNRDBQ7:          reader.I32(),
		maxBits:              reader.I32(),
		useCBR:               reader.I32(),
		condCoding:           reader.I32(),
		tellAfterSideInfo:    reader.I32(),
		rangeAfterSideInfo:   reader.I32(),
		midOnly:              reader.I32(),
		midEncodeRet:         reader.I32(),
		midNBytesOut:         reader.I32(),
		midTellAfterFrame:    reader.I32(),
		midRangeAfterFrame:   reader.I32(),
		midLastGainIndex:     reader.I32(),
		midSignalType:        reader.I32(),
		midQuantOffsetType:   reader.I32(),
		midSeed:              reader.I32(),
		midFrameCounter:      reader.I32(),
		midPrevSignalType:    reader.I32(),
		midPrevLag:           reader.I32(),
		midNFramesEncoded:    reader.I32(),
	}
	for i := range out.midInputQualityBands {
		out.midInputQualityBands[i] = reader.I32()
	}
	out.midLagIndex = reader.I32()
	out.midContourIndex = reader.I32()
	out.midNLSFInterpCoefQ2 = reader.I32()
	out.midPERIndex = reader.I32()
	out.midLTPScaleIndex = reader.I32()
	for i := range out.midGainsIndices {
		out.midGainsIndices[i] = reader.I32()
	}
	for i := range out.midLTPIndices {
		out.midLTPIndices[i] = reader.I32()
	}
	for i := range out.midNLSFIndices {
		out.midNLSFIndices[i] = reader.I32()
	}
	out.midPulseAbsSum = reader.I32()
	out.midPulseHash = reader.I32()
	out.midPulseBlockCount = reader.I32()
	for i := range out.midPulseBlockAbsSum {
		out.midPulseBlockAbsSum[i] = reader.I32()
	}
	out.midGainTraceValid = reader.I32()
	for i := range out.midGainsPreQ16 {
		out.midGainsPreQ16[i] = reader.I32()
	}
	for i := range out.midResNrgBits {
		out.midResNrgBits[i] = reader.I32()
	}
	for i := range out.midGainsUnqQ16 {
		out.midGainsUnqQ16[i] = reader.I32()
	}
	for i := range out.midGainsQuantQ16 {
		out.midGainsQuantQ16[i] = reader.I32()
	}
	out.midPredGainBits = reader.I32()
	out.midPitchAutoCorr0Bits = reader.I32()
	out.midPitchResNrgBits = reader.I32()
	out.midInputQualityBits = reader.I32()
	out.midCodingQualityBits = reader.I32()
	out.midManualTellIndices = reader.I32()
	out.midManualRangeIndices = reader.I32()
	out.midManualTellPulses = reader.I32()
	out.midManualRangePulses = reader.I32()
	for i := range out.midIndexTraceTell {
		out.midIndexTraceTell[i] = reader.I32()
		out.midIndexTraceRange[i] = reader.I32()
	}
	for i := range out.sideInfoTraceTell {
		out.sideInfoTraceTell[i] = reader.I32()
		out.sideInfoTraceRange[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return libopusSILKPacket0WrapperRecord{}, err
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
			ix := silkStereoQuantPred(&pred)
			if pred[0] != want[i].first || pred[1] != want[i].second {
				t.Fatalf("pred=%v want [%d %d]", pred, want[i].first, want[i].second)
			}
			got := [6]int32{
				int32(ix[0][0]), int32(ix[0][1]), int32(ix[0][2]),
				int32(ix[1][0]), int32(ix[1][1]), int32(ix[1][2]),
			}
			if got != want[i].extra {
				t.Fatalf("ix=%v want %v", got, want[i].extra)
			}
		})
	}
}

func TestSILKStereoStateStorageMatchesLibopusTypes(t *testing.T) {
	libopustest.RequireOracle(t)
	records, err := probeLibopusSILKStereo(libopusSILKStereoModeStateSizes, [][]int32{{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	rec := records[0]
	var enc stereoEncState
	var dec stereoDecState
	checks := []struct {
		name string
		got  uintptr
		want int32
	}{
		{"enc.predPrevQ13", unsafe.Sizeof(enc.predPrevQ13[0]), rec.first},
		{"enc.sMid", unsafe.Sizeof(enc.sMid[0]), rec.second},
		{"enc.sSide", unsafe.Sizeof(enc.sSide[0]), rec.extra[0]},
		{"enc.midSideAmpQ0", unsafe.Sizeof(enc.midSideAmpQ0[0]), rec.extra[1]},
		{"enc.smthWidthQ14", unsafe.Sizeof(enc.smthWidthQ14), rec.extra[2]},
		{"enc.widthPrevQ14", unsafe.Sizeof(enc.widthPrevQ14), rec.extra[3]},
		{"enc.silentSideLen", unsafe.Sizeof(enc.silentSideLen), rec.extra[4]},
		{"dec.predPrevQ13", unsafe.Sizeof(dec.predPrevQ13[0]), rec.first},
		{"dec.sMid", unsafe.Sizeof(dec.sMid[0]), rec.second},
		{"dec.sSide", unsafe.Sizeof(dec.sSide[0]), rec.extra[0]},
	}
	for _, check := range checks {
		if int32(check.got) != check.want {
			t.Fatalf("%s sizeof = %d, want libopus %d", check.name, check.got, check.want)
		}
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
	fixtureLeft, fixtureRight := chirpSweepWB20msStereo48kPacket0LRToMSInput(t)
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
		{
			name:         "chirp_sweep_v1_wb_20ms_stereo_48k_packet0",
			frameLength:  320,
			fsKHz:        16,
			totalRateBps: 47600,
			speechActQ8:  0,
			left:         fixtureLeft,
			right:        fixtureRight,
		},
		{
			name:         "chirp_sweep_v1_wb_20ms_stereo_48k_packet0_48kbps",
			frameLength:  320,
			fsKHz:        16,
			totalRateBps: 48000,
			speechActQ8:  0,
			left:         fixtureLeft,
			right:        fixtureRight,
		},
	}

	records := make([][]int32, len(cases))
	frameLengths := make([]int, len(cases))
	for i, tc := range cases {
		frameLengths[i] = tc.frameLength
		record := []int32{
			int32(tc.frameLength), int32(tc.fsKHz), int32(tc.totalRateBps),
			int32(tc.speechActQ8), libopusStereoBoolWord(tc.toMono),
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
			var state stereoEncState
			var scratch stereoLRToMSScratch
			setStereoStateFromOracle(&state, tc.state)
			// The channel input buffers hold the frame at [2:]; the conversion
			// leaves mid and side where silk_encode_frame reads them (inputBuf+1).
			buf0 := make([]int16, tc.frameLength+2)
			buf1 := make([]int16, tc.frameLength+2)
			copy(buf0[2:], tc.left)
			copy(buf1[2:], tc.right)
			ix, midOnly, rates := silkStereoLRToMS(&state, buf0, buf1,
				int32(tc.totalRateBps), int32(tc.speechActQ8), tc.toMono, tc.fsKHz, tc.frameLength, &scratch)
			if int32(midOnly) != want[i].midOnly {
				t.Fatalf("midOnly=%d want %d", midOnly, want[i].midOnly)
			}
			if rates[0] != want[i].midRate || rates[1] != want[i].sideRate {
				t.Fatalf("rates=%d/%d want %d/%d", rates[0], rates[1], want[i].midRate, want[i].sideRate)
			}
			gotIx := [6]int32{
				int32(ix[0][0]), int32(ix[0][1]), int32(ix[0][2]),
				int32(ix[1][0]), int32(ix[1][1]), int32(ix[1][2]),
			}
			if gotIx != want[i].ix {
				t.Fatalf("ix=%v want %v", gotIx, want[i].ix)
			}
			gotState := stereoStateForOracle(state)
			if gotState != want[i].state {
				t.Fatalf("state=%+v want %+v", gotState, want[i].state)
			}
			if !slices.Equal(buf0[1:tc.frameLength+1], want[i].mid) {
				t.Fatalf("mid output mismatch")
			}
			if !slices.Equal(buf1[1:tc.frameLength+1], want[i].side) {
				t.Fatalf("side output mismatch")
			}
		})
	}
}

func TestSILKStereoPacket0WrapperMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		bitRate       = 47600
		maxBits       = 1275 * 8
		payloadSizeMs = 20
	)
	signal := chirpSweepWB20msStereo48kPacket0Signal(t)
	want, err := probeLibopusSILKPacket0Wrapper(signal, bitRate, maxBits, true, payloadSizeMs, 0)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk packet0 wrapper", err)
	}
	for _, check := range []struct {
		name string
		got  int32
		want int32
	}{
		{"midVAD", want.midVAD, 0},
		{"sideVAD", want.sideVAD, 0},
		{"midSpeechActivityQ8", want.midSpeechActivityQ8, 12},
		{"sideSpeechActivityQ8", want.sideSpeechActivityQ8, 2},
		{"midInputTiltQ15", want.midInputTiltQ15, 32766},
		{"sideInputTiltQ15", want.sideInputTiltQ15, 0},
		{"midSNRDBQ7", want.midSNRDBQ7, 3129},
		{"sideSNRDBQ7", want.sideSNRDBQ7, 0},
	} {
		if check.got != check.want {
			t.Fatalf("libopus packet0 %s=%d want %d", check.name, check.got, check.want)
		}
	}

	prepareSILKPacket0MidFrameCoreOracle(t, signal, bitRate, maxBits, payloadSizeMs, want)
}

// TestSILKStereoPacket0EncodeMatchesLibopusOracle codes the first packet with
// PacketEncoder.Encode and compares the rate split, the VAD and SNR decisions
// and the mid channel state after its frame with the instrumented silk_Encode
// of the oracle.
func TestSILKStereoPacket0EncodeMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name    string
		bitRate int
		maxBits int
	}{
		{"47600bps", 47600, 1275 * 8},
		{"48000bps_capped", 48000, 1500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			signal := chirpSweepWB20msStereo48kPacket0Signal(t)
			want, err := probeLibopusSILKPacket0Wrapper(signal, tc.bitRate, tc.maxBits, true, 20, 0)
			if err != nil {
				libopustest.HelperUnavailable(t, "silk packet0 wrapper", err)
			}
			s := NewPacketEncoder(2)
			ctl := packet0EncControl(tc.bitRate, tc.maxBits, 20)
			var re rangecoding.Encoder
			re.Init(make([]byte, maxSilkPacketBytes))
			if _, err := s.Encode(&ctl, signal, len(signal)/2, &re, 0, VADNoActivity); err != nil {
				t.Fatalf("Encode: %v", err)
			}
			checkSILKPacket0MidState(t, s, want, true)
		})
	}
}

// checkSILKPacket0MidState compares the mid channel state after its first
// frame, and the packet-level decisions that precede it, with the oracle.
// sideCoded reports that the side frame has run its silk_control_SNR too.
func checkSILKPacket0MidState(t testing.TB, s *PacketEncoder, want libopusSILKPacket0WrapperRecord, sideCoded bool) {
	t.Helper()
	mid, side := s.state[0], s.state[1]
	if int32(s.stereo.midOnlyFlags[0]) != want.midOnly {
		t.Fatalf("mid_only_flag=%d want %d", s.stereo.midOnlyFlags[0], want.midOnly)
	}
	if mid.targetRateBps != want.midTargetRateBps {
		t.Fatalf("mid TargetRate_bps=%d want %d", mid.targetRateBps, want.midTargetRateBps)
	}
	if sideCoded && want.sideTargetRateBps > 0 && side.targetRateBps != want.sideTargetRateBps {
		t.Fatalf("side TargetRate_bps=%d want %d", side.targetRateBps, want.sideTargetRateBps)
	}
	checks := []struct {
		name      string
		got, want int32
	}{
		{"midVAD", boolWord(mid.vadFlags[0]), want.midVAD},
		{"sideVAD", boolWord(side.vadFlags[0]), want.sideVAD},
		{"midSpeechActivityQ8", mid.speechActivityQ8, want.midSpeechActivityQ8},
		{"midInputTiltQ15", mid.inputTiltQ15, want.midInputTiltQ15},
		{"midSNRDBQ7", mid.snrDBQ7, want.midSNRDBQ7},
		{"midNFramesEncoded", mid.nFramesEncoded, want.midNFramesEncoded},
	}
	// The oracle runs silk_encode_frame_FLP; the FIXED_POINT build codes the
	// frame with the silk_encode_frame_FIX analysis instead.
	if !silkFixedEncodeBuild {
		checks = append(checks, []struct {
			name      string
			got, want int32
		}{
			{"midLastGainIndex", int32(mid.previousGainIndex), want.midLastGainIndex},
			{"midPrevSignalType", mid.ecPrevSignalType, want.midPrevSignalType},
			{"midPrevLag", int32(mid.ecPrevLagIndex), want.midPrevLag},
			{"midQuantOffsetType", int32(mid.lastQuantOffsetType), want.midQuantOffsetType},
			{"midSeed", int32(mid.lastSeed), want.midSeed},
			{"midFrameCounter", mid.frameCounter, want.midFrameCounter},
		}...)
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Fatalf("%s=%d want %d", check.name, check.got, check.want)
		}
	}
	if want.midOnly == 0 {
		if side.speechActivityQ8 != want.sideSpeechActivityQ8 || side.inputTiltQ15 != want.sideInputTiltQ15 {
			t.Fatalf("side speech/tilt=%d/%d want %d/%d", side.speechActivityQ8, side.inputTiltQ15,
				want.sideSpeechActivityQ8, want.sideInputTiltQ15)
		}
	}
	for i, q := range mid.inputQualityBandsQ15 {
		if q != want.midInputQualityBands[i] {
			t.Fatalf("mid input_quality_bands_Q15[%d]=%d want %d", i, q, want.midInputQualityBands[i])
		}
	}
	if gotSignalType, _ := mid.lastEncodedSignalInfo(); !silkFixedEncodeBuild && gotSignalType != want.midSignalType {
		t.Fatalf("mid signalType=%d want %d", gotSignalType, want.midSignalType)
	}
}

func boolWord(v bool) int32 {
	if v {
		return 1
	}
	return 0
}

func packet0EncControl(bitRate, maxBits, payloadSizeMs int) EncControl {
	return EncControl{
		NChannelsAPI:              2,
		NChannelsInternal:         2,
		APISampleRate:             48000,
		MaxInternalSampleRate:     16000,
		MinInternalSampleRate:     8000,
		DesiredInternalSampleRate: 16000,
		PayloadSizeMs:             int32(payloadSizeMs),
		BitRate:                   int32(bitRate),
		Complexity:                10,
		UseCBR:                    true,
		MaxBits:                   int32(maxBits),
	}
}

// silkPacket0MidFrame is the state prepareSILKPacket0MidFrameCoreOracle leaves
// just before the mid channel's silk_encode_frame_FLP of the first packet.
type silkPacket0MidFrame struct {
	enc        *PacketEncoder
	mid        *Encoder
	re         *rangecoding.Encoder
	maxBits    int
	useCBR     bool
	condCoding int
}

// prepareSILKPacket0MidFrameCoreOracle runs the first packet of a 48 kHz
// stereo stream through the silk_Encode steps (silk/enc_API.c) up to the mid
// channel's silk_encode_frame_FLP, step by step like the instrumented oracle,
// checking the side information and the rate controls on the way.
func prepareSILKPacket0MidFrameCoreOracle(t testing.TB, signal []float32, bitRate, maxBits, payloadSizeMs int, want libopusSILKPacket0WrapperRecord) silkPacket0MidFrame {
	t.Helper()
	const activity = VADNoActivity
	s := NewPacketEncoder(2)
	ctl := packet0EncControl(bitRate, maxBits, payloadSizeMs)
	mid, side := s.state[0], s.state[1]

	// Mono -> stereo transition of the first stereo packet.
	side.reset()
	s.stereo = stereoEncState{midSideAmpQ0: [4]int32{0, 1, 0, 1}, smthWidthQ14: 1 << 14}
	s.nChannelsAPI, s.nChannelsInternal = 2, 2
	for n, st := range []*Encoder{mid, side} {
		st.nFramesEncoded = 0
		// The side channel is forced to the rate of the mid channel.
		var forceFsKHz int32
		if n == 1 {
			forceFsKHz = mid.fsKHz
		}
		st.control(&ctl, false, forceFsKHz)
		st.inDTX = st.useDTX
	}

	// Resample each channel into its input buffer.
	nIn := int(mid.frameLength) * 48000 / (int(mid.fsKHz) * 1000)
	in := make([]int16, nIn)
	for i := range in {
		in[i] = opusmath.Float32ToInt16(signal[2*i])
	}
	mid.resampler.Resample(mid.inputBuf[2:2+mid.frameLength], in)
	for i := range in {
		in[i] = opusmath.Float32ToInt16(signal[2*i+1])
	}
	side.resampler.Resample(side.inputBuf[2:2+side.frameLength], in)

	re := &rangecoding.Encoder{}
	re.Init(make([]byte, maxSilkPacketBytes))
	if lbrrBits := s.encodeLBRR(re, 2); lbrrBits != 0 {
		t.Fatalf("first packet coded %d LBRR bits", lbrrBits)
	}
	if gotTell, gotRange := int32(re.Tell()), int32(re.Range()); gotTell != want.sideInfoTraceTell[0] || gotRange != want.sideInfoTraceRange[0] {
		t.Fatalf("side info after header tell/range=%d/%d want %d/%d",
			gotTell, gotRange, want.sideInfoTraceTell[0], want.sideInfoTraceRange[0])
	}
	mid.hpVariableCutoff()

	// The first packet targets the full rate.
	nBits := int32(bitRate * payloadSizeMs / 1000)
	nBits /= mid.nFramesPerPacket
	totalRate := silkLimit32(silkSMULBB(nBits, 50), int32(bitRate), 5000)
	if totalRate != want.targetRateBps {
		t.Fatalf("TargetRate=%d want %d", totalRate, want.targetRateBps)
	}

	ix, midOnly, rates := silkStereoLRToMS(&s.stereo, mid.inputBuf[:], side.inputBuf[:], totalRate,
		mid.speechActivityQ8, false, int(mid.fsKHz), int(mid.frameLength), &s.stereoScratch)
	s.stereo.predIx[0] = ix
	s.stereo.midOnlyFlags[0] = midOnly
	if midOnly == 0 {
		side.encodeDoVAD(activity)
	} else {
		side.vadFlags[0] = false
	}
	stereoEncodePred(re, ix)
	if gotTell, gotRange := int32(re.Tell()), int32(re.Range()); gotTell != want.sideInfoTraceTell[1] || gotRange != want.sideInfoTraceRange[1] {
		t.Fatalf("side info after stereo pred tell/range=%d/%d want %d/%d",
			gotTell, gotRange, want.sideInfoTraceTell[1], want.sideInfoTraceRange[1])
	}
	if !side.vadFlags[0] {
		stereoEncodeMidOnly(re, midOnly)
	}
	gotTell := int32(re.Tell())
	if gotRange := int32(re.Range()); gotTell != want.sideInfoTraceTell[2] || gotRange != want.sideInfoTraceRange[2] {
		t.Fatalf("side info after mid-only tell/range=%d/%d want %d/%d",
			gotTell, gotRange, want.sideInfoTraceTell[2], want.sideInfoTraceRange[2])
	}
	mid.encodeDoVAD(activity)

	if int32(midOnly) != want.midOnly {
		t.Fatalf("mid_only_flag=%d want %d", midOnly, want.midOnly)
	}
	if rates[0] != want.midTargetRateBps || rates[1] != want.sideTargetRateBps {
		t.Fatalf("MStargetRates=%d/%d want %d/%d", rates[0], rates[1], want.midTargetRateBps, want.sideTargetRateBps)
	}
	if boolWord(mid.vadFlags[0]) != want.midVAD || boolWord(side.vadFlags[0]) != want.sideVAD {
		t.Fatalf("VAD mid/side=%v/%v want %d/%d", mid.vadFlags[0], side.vadFlags[0], want.midVAD, want.sideVAD)
	}
	if mid.speechActivityQ8 != want.midSpeechActivityQ8 || mid.inputTiltQ15 != want.midInputTiltQ15 {
		t.Fatalf("mid speech/tilt=%d/%d want %d/%d", mid.speechActivityQ8, mid.inputTiltQ15,
			want.midSpeechActivityQ8, want.midInputTiltQ15)
	}

	// Rate constraints of the mid frame: a coded side channel takes the CBR
	// flag and half the packet's bits off the mid channel.
	frameMaxBits := int32(maxBits)
	useCBR := ctl.UseCBR
	if rates[1] > 0 {
		useCBR = false
		frameMaxBits -= int32(maxBits) / 2
	}
	mid.controlSNR(int(rates[0]), int(mid.nbSubfr))
	if mid.snrDBQ7 != want.midSNRDBQ7 {
		t.Fatalf("mid SNR_dB_Q7=%d want %d", mid.snrDBQ7, want.midSNRDBQ7)
	}
	if side.snrDBQ7 != want.sideSNRDBQ7 {
		t.Fatalf("side SNR_dB_Q7=%d want %d", side.snrDBQ7, want.sideSNRDBQ7)
	}
	if frameMaxBits != want.maxBits || boolWord(useCBR) != want.useCBR || want.condCoding != codeIndependently {
		t.Fatalf("frame controls maxBits/useCBR/condCoding=%d/%d/%d want %d/%d/%d",
			frameMaxBits, boolWord(useCBR), codeIndependently, want.maxBits, want.useCBR, want.condCoding)
	}
	if gotTell != want.tellAfterSideInfo {
		t.Fatalf("tellAfterSideInfo=%d want %d (libopus midVAD=%d sideVAD=%d midOnly=%d speech=%d/%d snr=%d/%d tilt=%d/%d)",
			gotTell, want.tellAfterSideInfo, want.midVAD, want.sideVAD, want.midOnly,
			want.midSpeechActivityQ8, want.sideSpeechActivityQ8, want.midSNRDBQ7, want.sideSNRDBQ7,
			want.midInputTiltQ15, want.sideInputTiltQ15)
	}
	if gotRange := int32(re.Range()); gotRange != want.rangeAfterSideInfo {
		t.Fatalf("rangeAfterSideInfo=%d want %d", gotRange, want.rangeAfterSideInfo)
	}
	return silkPacket0MidFrame{
		enc:        s,
		mid:        mid,
		re:         re,
		maxBits:    int(frameMaxBits),
		useCBR:     useCBR,
		condCoding: codeIndependently,
	}
}

func chirpSweepWB20msStereo48kPacket0LRToMSInput(t testing.TB) ([]int16, []int16) {
	t.Helper()
	signal := chirpSweepWB20msStereo48kPacket0Signal(t)
	left16, right16 := downsampleStereo48kTo16kPacket0(t, signal)
	left := make([]int16, len(left16))
	right := make([]int16, len(right16))
	for i := range left16 {
		left[i] = float32ToInt16(left16[i])
		right[i] = float32ToInt16(right16[i])
	}
	return left, right
}

func chirpSweepWB20msStereo48kPacket0Signal(t testing.TB) []float32 {
	t.Helper()
	const (
		sampleRate   = 48000
		channels     = 2
		frameSize48  = 960
		signalFrames = sampleRate / frameSize48
	)
	signal, err := testsignal.GenerateEncoderSignalVariant(
		testsignal.EncoderVariantChirpSweepV1,
		sampleRate,
		signalFrames*frameSize48*channels,
		channels,
	)
	if err != nil {
		t.Fatalf("generate chirp sweep fixture signal: %v", err)
	}
	quantizeOpusDemoF32InPlace(signal)
	return signal[:frameSize48*channels]
}

func downsampleStereo48kTo16kPacket0(t testing.TB, signal []float32) ([]float32, []float32) {
	t.Helper()
	const (
		sampleRate  = 48000
		frameSize48 = 960
		frameSize16 = 320
	)
	left48 := make([]float32, frameSize48)
	right48 := make([]float32, frameSize48)
	for i := range frameSize48 {
		left48[i] = signal[2*i]
		right48[i] = signal[2*i+1]
	}
	left16 := make([]float32, frameSize16)
	right16 := make([]float32, frameSize16)
	leftDown := NewDownsamplingResampler(sampleRate, 16000).Process(left48)
	rightDown := NewDownsamplingResampler(sampleRate, 16000).Process(right48)
	if len(leftDown) != frameSize16 {
		t.Fatalf("left resampler output=%d want %d", len(leftDown), frameSize16)
	}
	if len(rightDown) != frameSize16 {
		t.Fatalf("right resampler output=%d want %d", len(rightDown), frameSize16)
	}
	copy(left16, leftDown)
	copy(right16, rightDown)
	return left16, right16
}

func quantizeOpusDemoF32InPlace(in []float32) {
	const inv24 = 1.0 / 8388608.0
	for i, s := range in {
		q := math.Floor(0.5 + float64(s)*8388608.0)
		in[i] = float32(q * inv24)
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

func setStereoStateFromOracle(st *stereoEncState, src libopusSILKStereoState) {
	st.predPrevQ13 = [2]int16{int16(src.predPrevQ13[0]), int16(src.predPrevQ13[1])}
	st.sMid = [2]int16{int16(src.sMid[0]), int16(src.sMid[1])}
	st.sSide = [2]int16{int16(src.sSide[0]), int16(src.sSide[1])}
	st.widthPrevQ14 = int16(src.widthPrevQ14)
	st.smthWidthQ14 = int16(src.smthWidthQ14)
	st.silentSideLen = int16(src.silentSideLen)
	for i := range st.midSideAmpQ0 {
		st.midSideAmpQ0[i] = src.midSideAmpQ0[i]
	}
}

func stereoStateForOracle(st stereoEncState) libopusSILKStereoState {
	return libopusSILKStereoState{
		predPrevQ13:   [2]int32{int32(st.predPrevQ13[0]), int32(st.predPrevQ13[1])},
		sMid:          [2]int32{int32(st.sMid[0]), int32(st.sMid[1])},
		sSide:         [2]int32{int32(st.sSide[0]), int32(st.sSide[1])},
		midSideAmpQ0:  st.midSideAmpQ0,
		smthWidthQ14:  int32(st.smthWidthQ14),
		widthPrevQ14:  int32(st.widthPrevQ14),
		silentSideLen: int32(st.silentSideLen),
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

func TestStereoLPFilter(t *testing.T) {
	// Test the LP filter with a known input
	// LP filter is [1,2,1]/4, so for constant input, output should equal input
	signal := []int16{1000, 1000, 1000, 1000, 1000, 1000}
	frameLength := 4

	lp, hp := stereoLPFilter(signal, frameLength)

	// For constant input, LP should equal input and HP should be 0
	for i := range frameLength {
		if lp[i] != 1000 {
			t.Errorf("LP[%d] = %d, want 1000 (constant input)", i, lp[i])
		}
		if hp[i] != 0 {
			t.Errorf("HP[%d] = %d, want 0 (constant input)", i, hp[i])
		}
	}
}

func TestStereoLPFilterImpulse(t *testing.T) {
	// Test LP filter with an impulse
	// Input: [0, 0, 4000, 0, 0]
	// LP[0] = (0 + 2*0 + 4000 + 2) >> 2 = 1000
	// LP[1] = (0 + 2*4000 + 0 + 2) >> 2 = 2000
	// LP[2] = (4000 + 2*0 + 0 + 2) >> 2 = 1000
	signal := []int16{0, 0, 4000, 0, 0}
	frameLength := 3

	lp, hp := stereoLPFilter(signal, frameLength)

	expectedLP := []int16{1000, 2000, 1000}
	for i := range frameLength {
		if lp[i] != expectedLP[i] {
			t.Errorf("LP[%d] = %d, want %d", i, lp[i], expectedLP[i])
		}
	}

	// HP[n] = signal[n+1] - LP[n]
	// HP[0] = 0 - 1000 = -1000
	// HP[1] = 4000 - 2000 = 2000
	// HP[2] = 0 - 1000 = -1000
	expectedHP := []int16{-1000, 2000, -1000}
	for i := range frameLength {
		if hp[i] != expectedHP[i] {
			t.Errorf("HP[%d] = %d, want %d", i, hp[i], expectedHP[i])
		}
	}
}

func TestStereoConvertLRToMS(t *testing.T) {
	// Test L/R to M/S conversion
	// M = (L + R) / 2
	// S = (L - R) / 2
	left := []int16{1000, 2000, 3000, 4000}
	right := []int16{1000, 0, 1000, 2000}
	mid := make([]int16, 4)
	side := make([]int16, 4)

	stereoConvertLRToMS(left[:2], right[:2], mid[:2], side[:2], 0)

	// For first 2 samples:
	// M[0] = (1000 + 1000) / 2 = 1000
	// S[0] = (1000 - 1000) / 2 = 0
	// M[1] = (2000 + 0) / 2 = 1000
	// S[1] = (2000 - 0) / 2 = 1000

	if mid[0] != 1000 {
		t.Errorf("mid[0] = %d, want 1000", mid[0])
	}
	if side[0] != 0 {
		t.Errorf("side[0] = %d, want 0", side[0])
	}
	if mid[1] != 1000 {
		t.Errorf("mid[1] = %d, want 1000", mid[1])
	}
	if side[1] != 1000 {
		t.Errorf("side[1] = %d, want 1000", side[1])
	}
}

func TestIsqrt32(t *testing.T) {
	tests := []struct {
		input    uint32
		expected uint32
	}{
		{0, 0},
		{1, 1},
		{4, 2},
		{9, 3},
		{16, 4},
		{25, 5},
		{100, 10},
		{10000, 100},
		{1000000, 1000},
	}

	for _, tt := range tests {
		got := isqrt32(tt.input)
		if got != tt.expected {
			t.Errorf("isqrt32(%d) = %d, want %d", tt.input, got, tt.expected)
		}
	}
}

func TestSILKIsqrt32MatchesLibopusCELTOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	inputs := []uint32{
		0, 1, 2, 3, 4, 15, 16, 17,
		(1 << 16) - 1, 1 << 16, (1 << 16) + 1,
		(1 << 24) - 1, 1 << 24, (1 << 24) + 1,
		1000000, ^uint32(0) - 2, ^uint32(0) - 1, ^uint32(0),
	}
	want, err := libopustest.ProbeCELTMathWords(libopustest.CELTMathModeISqrt32, len(inputs), inputs)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt math", err)
	}
	for i, x := range inputs {
		got := isqrt32(x)
		if got != want[i] {
			t.Fatalf("isqrt32(%d)=%d want %d", x, got, want[i])
		}
	}
}

func TestStereoLRToMSKeepsFilterHistory(t *testing.T) {
	// silk_stereo_LR_to_MS carries the last two mid and side samples of a
	// frame into the next one (sMid/sSide).
	const frameLength = 320
	buf0 := make([]int16, frameLength+2)
	buf1 := make([]int16, frameLength+2)
	for i := 2; i < frameLength+2; i++ {
		buf0[i] = 16384
		buf1[i] = 9830
	}
	var state stereoEncState
	var scratch stereoLRToMSScratch
	silkStereoLRToMS(&state, buf0, buf1, 32000, 200, false, 16, frameLength, &scratch)

	wantMid := int16((16384 + 9830 + 1) >> 1)
	wantSide := int16((16384 - 9830 + 1) >> 1)
	if state.sMid != [2]int16{wantMid, wantMid} {
		t.Errorf("sMid = %v, want [%d %d]", state.sMid, wantMid, wantMid)
	}
	if state.sSide != [2]int16{wantSide, wantSide} {
		t.Errorf("sSide = %v, want [%d %d]", state.sSide, wantSide, wantSide)
	}
	// The mid output starts with the previous frame's history (zero here).
	if buf0[0] != 0 || buf0[1] != 0 || buf0[2] != wantMid {
		t.Errorf("mid = %v..., want [0 0 %d ...]", buf0[:3], wantMid)
	}
}

func TestStereoLPFilterMatchesLibopus(t *testing.T) {
	// Test that our LP filter matches the libopus implementation exactly
	// libopus formula: sum = silk_RSHIFT_ROUND(silk_ADD_LSHIFT32(mid[n] + mid[n+2], mid[n+1], 1), 2)
	// Which is: ((mid[n] + mid[n+2]) + (mid[n+1] << 1) + 2) >> 2
	// = (mid[n] + 2*mid[n+1] + mid[n+2] + 2) / 4  (rounded)

	testCases := []struct {
		signal   []int16
		expected []int16
	}{
		// Test case 1: Simple values
		{[]int16{100, 200, 300, 400, 500}, []int16{200, 300, 400}},
		// Test case 2: All same
		{[]int16{1000, 1000, 1000, 1000, 1000}, []int16{1000, 1000, 1000}},
		// Test case 3: Ramp
		{[]int16{0, 100, 200, 300, 400}, []int16{100, 200, 300}},
	}

	for i, tc := range testCases {
		lp, _ := stereoLPFilter(tc.signal, len(tc.expected))
		for j, exp := range tc.expected {
			if lp[j] != exp {
				t.Errorf("Test %d: LP[%d] = %d, want %d", i, j, lp[j], exp)
			}
		}
	}
}

func BenchmarkStereoLPFilter(b *testing.B) {
	signal := make([]int16, 322) // 320 + 2 history
	for i := range signal {
		signal[i] = int16(i * 10)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stereoLPFilter(signal, 320)
	}
}

func makeStereoTestFrame(frameLength, sampleRate int) (left, right []float32) {
	left = make([]float32, frameLength)
	right = make([]float32, frameLength)
	for i := range frameLength {
		tm := float64(i) / float64(sampleRate)
		// Use distinct channels so side stays active and rate-split matters.
		left[i] = 0.35 * float32(math.Sin(2*math.Pi*430.0*tm))
		right[i] = 0.25*float32(math.Sin(2*math.Pi*910.0*tm+0.6)) + 0.10*left[i]
	}
	return left, right
}

// TestEncodeStereoAppliesPerChannelRateSplit checks that each channel's
// silk_control_SNR runs at its share of the silk_stereo_LR_to_MS rate split
// (silk/enc_API.c), and that the split follows the previous frame's speech
// activity.
func TestEncodeStereoAppliesPerChannelRateSplit(t *testing.T) {
	const (
		bw         = BandwidthWideband
		sampleRate = 16000
		frameLen   = 320 // 20 ms at 16 kHz
		totalRate  = 32000
	)
	left, right := makeStereoTestFrame(frameLen, sampleRate)
	pcm := interleaveStereo(left, right)

	var mids [2]int32
	for i, prevActivityQ8 := range []int32{0, 200} {
		p := newTestPacketEncoder(bw, 2)
		p.ctl.BitRate = totalRate
		p.enc.state[0].speechActivityQ8 = prevActivityQ8
		if pkt := p.encodeInto(t, pcm, 1); len(pkt) == 0 {
			t.Fatal("encode stereo returned empty packet")
		}
		if p.enc.stereo.midOnlyFlags[0] != 0 {
			t.Fatalf("activity %d: expected a frame with side coding", prevActivityQ8)
		}
		mid := p.enc.state[0].targetRateBps
		side := p.enc.state[1].targetRateBps
		if mid <= 0 || side <= 0 {
			t.Fatalf("activity %d: invalid split mid=%d side=%d", prevActivityQ8, mid, side)
		}
		// The first packet targets the full rate; the split shares it less
		// 600 bps for the stereo parameters of a 20 ms frame.
		if mid+side != totalRate-600 {
			t.Fatalf("activity %d: mid %d + side %d != %d", prevActivityQ8, mid, side, totalRate-600)
		}
		mids[i] = mid
	}
	if mids[0] == mids[1] {
		t.Fatalf("rate split ignores the previous speech activity: mid=%d for both", mids[0])
	}
}

func TestStereoUnmixBasic(t *testing.T) {
	// Test basic mid-side to left-right conversion without prediction
	mid := []float32{0.5, 0.5, 0.5, 0.5}
	side := []float32{0.25, 0.25, 0.25, 0.25}
	left := make([]float32, 4)
	right := make([]float32, 4)

	if err := stereoUnmix(mid, side, 0, 0, left, right); err != nil {
		t.Fatalf("stereoUnmix failed: %v", err)
	}

	// L = M + S = 0.75
	// R = M - S = 0.25
	for i := range left {
		if math.Abs(float64(left[i]-0.75)) > 0.001 {
			t.Errorf("left[%d] = %f, want 0.75", i, left[i])
		}
		if math.Abs(float64(right[i]-0.25)) > 0.001 {
			t.Errorf("right[%d] = %f, want 0.25", i, right[i])
		}
	}
}

func TestStereoUnmixZeroSide(t *testing.T) {
	// Zero side channel should give L == R == M
	mid := []float32{0.6, -0.3, 0.0, 0.8}
	side := []float32{0.0, 0.0, 0.0, 0.0}
	left := make([]float32, 4)
	right := make([]float32, 4)

	if err := stereoUnmix(mid, side, 0, 0, left, right); err != nil {
		t.Fatalf("stereoUnmix failed: %v", err)
	}

	for i := range mid {
		if math.Abs(float64(left[i]-mid[i])) > 0.001 {
			t.Errorf("left[%d] = %f, want %f", i, left[i], mid[i])
		}
		if math.Abs(float64(right[i]-mid[i])) > 0.001 {
			t.Errorf("right[%d] = %f, want %f", i, right[i], mid[i])
		}
	}
}

func TestStereoUnmixClamping(t *testing.T) {
	// Test that output is clamped to [-1, 1]
	mid := []float32{1.0, -1.0}
	side := []float32{0.5, -0.5}
	left := make([]float32, 2)
	right := make([]float32, 2)

	if err := stereoUnmix(mid, side, 0, 0, left, right); err != nil {
		t.Fatalf("stereoUnmix failed: %v", err)
	}

	// L = 1.0 + 0.5 = 1.5 -> clamped to 1.0
	if left[0] > 1.0 {
		t.Errorf("left[0] = %f, should be clamped to 1.0", left[0])
	}
	// R = 1.0 - 0.5 = 0.5 (no clamp needed)
	if math.Abs(float64(right[0]-0.5)) > 0.001 {
		t.Errorf("right[0] = %f, want 0.5", right[0])
	}

	// L = -1.0 + -0.5 = -1.5 -> clamped to -1.0
	if left[1] < -1.0 {
		t.Errorf("left[1] = %f, should be clamped to -1.0", left[1])
	}
}

func TestFrameDurationConversion(t *testing.T) {
	tests := []struct {
		tocFrameSize int
		wantDuration FrameDuration
	}{
		{480, Frame10ms},
		{960, Frame20ms},
		{1920, Frame40ms},
		{2880, Frame60ms},
	}

	for _, tt := range tests {
		got := FrameDurationFromTOC(tt.tocFrameSize)
		if got != tt.wantDuration {
			t.Errorf("FrameDurationFromTOC(%d) = %d, want %d", tt.tocFrameSize, got, tt.wantDuration)
		}
	}
}

func TestSubframeCount(t *testing.T) {
	tests := []struct {
		duration FrameDuration
		want     int
	}{
		{Frame10ms, 2},
		{Frame20ms, 4},
		{Frame40ms, 8},
		{Frame60ms, 12},
	}

	for _, tt := range tests {
		got := getSubframeCount(tt.duration)
		if got != tt.want {
			t.Errorf("getSubframeCount(%d) = %d, want %d", tt.duration, got, tt.want)
		}
	}
}

func TestFrameSamplesPerBandwidth(t *testing.T) {
	// 20ms frame at different bandwidths
	tests := []struct {
		bandwidth   Bandwidth
		wantSamples int
	}{
		{BandwidthNarrowband, 160}, // 8kHz * 0.02s
		{BandwidthMediumband, 240}, // 12kHz * 0.02s
		{BandwidthWideband, 320},   // 16kHz * 0.02s
	}

	for _, tt := range tests {
		got := getFrameSamples(Frame20ms, tt.bandwidth)
		if got != tt.wantSamples {
			t.Errorf("getFrameSamples(20ms, %v) = %d, want %d", tt.bandwidth, got, tt.wantSamples)
		}
	}
}

func TestGet48kHzSamples(t *testing.T) {
	tests := []struct {
		duration    FrameDuration
		wantSamples int
	}{
		{Frame10ms, 480},  // 10 * 48
		{Frame20ms, 960},  // 20 * 48
		{Frame40ms, 1920}, // 40 * 48
		{Frame60ms, 2880}, // 60 * 48
	}

	for _, tt := range tests {
		got := get48kHzSamples(tt.duration)
		if got != tt.wantSamples {
			t.Errorf("get48kHzSamples(%d) = %d, want %d", tt.duration, got, tt.wantSamples)
		}
	}
}

func TestIs40or60ms(t *testing.T) {
	tests := []struct {
		duration FrameDuration
		want     bool
	}{
		{Frame10ms, false},
		{Frame20ms, false},
		{Frame40ms, true},
		{Frame60ms, true},
	}

	for _, tt := range tests {
		got := is40or60ms(tt.duration)
		if got != tt.want {
			t.Errorf("is40or60ms(%d) = %v, want %v", tt.duration, got, tt.want)
		}
	}
}

func TestSubBlockCount(t *testing.T) {
	tests := []struct {
		duration FrameDuration
		want     int
	}{
		{Frame10ms, 1},
		{Frame20ms, 1},
		{Frame40ms, 2},
		{Frame60ms, 3},
	}

	for _, tt := range tests {
		got := getSubBlockCount(tt.duration)
		if got != tt.want {
			t.Errorf("getSubBlockCount(%d) = %d, want %d", tt.duration, got, tt.want)
		}
	}
}

func TestSamplesPerSubframe(t *testing.T) {
	tests := []struct {
		bandwidth   Bandwidth
		wantSamples int
	}{
		{BandwidthNarrowband, 40}, // 8kHz * 5ms
		{BandwidthMediumband, 60}, // 12kHz * 5ms
		{BandwidthWideband, 80},   // 16kHz * 5ms
	}

	for _, tt := range tests {
		got := getSamplesPerSubframe(tt.bandwidth)
		if got != tt.wantSamples {
			t.Errorf("getSamplesPerSubframe(%v) = %d, want %d", tt.bandwidth, got, tt.wantSamples)
		}
	}
}

func TestStereoPredWeights(t *testing.T) {
	// Verify stereo prediction weight table from libopus is symmetric
	// The table silk_stereo_pred_quant_Q13 has 16 entries
	for i := range 8 {
		// Weights should be symmetric around midpoint
		if silk_stereo_pred_quant_Q13[i] != -silk_stereo_pred_quant_Q13[15-i] {
			t.Errorf("silk_stereo_pred_quant_Q13[%d] = %d should be negative of silk_stereo_pred_quant_Q13[%d] = %d",
				i, silk_stereo_pred_quant_Q13[i], 15-i, silk_stereo_pred_quant_Q13[15-i])
		}
	}
}

func TestStereoQuantPred80Levels(t *testing.T) {
	// Test that silkStereoQuantPred produces valid indices for various Q13 values
	testCases := []struct {
		name    string
		predQ13 [2]int32
		wantIx0 [3]int8 // Expected ix[0] ranges: [0][0-2], [1][0-4], [2][0-4]
		wantIx1 [3]int8 // Expected ix[1] ranges
	}{
		{
			name:    "zero predictors",
			predQ13: [2]int32{0, 0},
		},
		{
			name:    "positive predictor",
			predQ13: [2]int32{5000, 0},
		},
		{
			name:    "negative predictor",
			predQ13: [2]int32{-5000, 0},
		},
		{
			name:    "both predictors",
			predQ13: [2]int32{3000, -2000},
		},
		{
			name:    "extreme positive",
			predQ13: [2]int32{13732, 13732},
		},
		{
			name:    "extreme negative",
			predQ13: [2]int32{-13732, -13732},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			predQ13 := tc.predQ13
			ix := silkStereoQuantPred(&predQ13)

			// Verify index ranges
			for n := range 2 {
				if ix[n][0] < 0 || ix[n][0] > 2 {
					t.Errorf("ix[%d][0] = %d, want [0, 2]", n, ix[n][0])
				}
				if ix[n][1] < 0 || ix[n][1] > 4 {
					t.Errorf("ix[%d][1] = %d, want [0, 4]", n, ix[n][1])
				}
				if ix[n][2] < 0 || ix[n][2] > 4 {
					t.Errorf("ix[%d][2] = %d, want [0, 4]", n, ix[n][2])
				}
			}

			// Verify joint index is valid (< 25)
			jointIdx := 5*int(ix[0][2]) + int(ix[1][2])
			if jointIdx >= 25 {
				t.Errorf("joint index = %d, want < 25", jointIdx)
			}
		})
	}
}

func TestStereoQuantPredDeltaCoding(t *testing.T) {
	// Test that delta coding is applied: predQ13[0] -= predQ13[1]
	predQ13 := [2]int32{5000, 3000}
	originalPred0 := predQ13[0]
	originalPred1 := predQ13[1]

	_ = silkStereoQuantPred(&predQ13)

	// After quantization, predQ13[0] should be the quantized delta
	// predQ13[1] should be the quantized second predictor
	// The relationship: quantized_pred0_original = predQ13[0] + predQ13[1]

	// We can't test exact values since quantization changes them,
	// but we can verify the delta relationship holds approximately
	reconstructedPred0 := predQ13[0] + predQ13[1]

	// The reconstructed value should be close to the original (within quantization error)
	// The quantization step is roughly (max-min)/80 levels
	maxError := int32(2000) // Allow for quantization error

	if util.Abs(reconstructedPred0-originalPred0) > maxError {
		t.Errorf("reconstructed pred[0] = %d, original = %d, difference = %d exceeds max error %d",
			reconstructedPred0, originalPred0, util.Abs(reconstructedPred0-originalPred0), maxError)
	}
	if util.Abs(predQ13[1]-originalPred1) > maxError {
		t.Errorf("quantized pred[1] = %d, original = %d, difference = %d exceeds max error %d",
			predQ13[1], originalPred1, util.Abs(predQ13[1]-originalPred1), maxError)
	}
}

func TestStereoEncodePredRoundtrip(t *testing.T) {
	// Test encoding stereo prediction indices
	testCases := []struct {
		name    string
		predQ13 [2]int32
	}{
		{"zero", [2]int32{0, 0}},
		{"positive", [2]int32{5000, 2000}},
		{"negative", [2]int32{-5000, -2000}},
		{"mixed", [2]int32{3000, -3000}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Quantize
			predQ13 := tc.predQ13
			ix := silkStereoQuantPred(&predQ13)

			// Encode to bitstream
			buf := make([]byte, 100)
			var enc rangecoding.Encoder
			enc.Init(buf)
			stereoEncodePred(&enc, ix)
			encoded := enc.Done()

			// Verify something was encoded (non-empty)
			if len(encoded) == 0 {
				t.Error("encoded data is empty")
			}
		})
	}
}

func TestSmulwb(t *testing.T) {
	// Test SMULWB: (a * int16(b)) >> 16
	// Note: b is truncated to int16, so only bottom 16 bits are used
	// int16 range is [-32768, 32767], so 32768 becomes -32768
	testCases := []struct {
		a, b int32
		want int32
	}{
		// Using values that fit in int16 for b
		{65536, 16384, 16384},   // 65536 * 16384 >> 16 = 16384
		{65536, -16384, -16384}, // 65536 * -16384 >> 16 = -16384
		{131072, 16384, 32768},  // 131072 * 16384 >> 16 = 32768
		{-65536, 16384, -16384}, // -65536 * 16384 >> 16 = -16384
		{0, 16384, 0},           // 0 * anything = 0
		{65536, 0, 0},           // anything * 0 = 0
		// Note: 32768 overflows int16 to -32768
		{65536, 32768, -32768}, // 65536 * int16(32768)=-32768 >> 16 = -32768
	}

	for _, tc := range testCases {
		got := smulwb(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("smulwb(%d, %d) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestInterpolationSamplesCount verifies the correct number of samples are interpolated.
func TestInterpolationSamplesCount(t *testing.T) {
	testCases := []struct {
		fsKHz         int
		wantInterpLen int
	}{
		{8, 64},   // 8kHz: 8ms * 8 = 64 samples
		{12, 96},  // 12kHz: 8ms * 12 = 96 samples
		{16, 128}, // 16kHz: 8ms * 16 = 128 samples
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%dkHz", tc.fsKHz), func(t *testing.T) {
			interpLen := stereoInterpLenMs * tc.fsKHz
			if interpLen != tc.wantInterpLen {
				t.Errorf("Interpolation length for %dkHz: got %d, want %d",
					tc.fsKHz, interpLen, tc.wantInterpLen)
			}
		})
	}
}
