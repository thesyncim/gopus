package silk

import (
	"fmt"
	"math"
	"testing"
	"unsafe"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/rangecoding"
)

const (
	libopusSILKStereoInputMagic  = "GSSI"
	libopusSILKStereoOutputMagic = "GSSO"

	libopusSILKStereoModeQuantPred     = uint32(0)
	libopusSILKStereoModeFindPredictor = uint32(1)
	libopusSILKStereoModeLRToMS        = uint32(2)
	libopusSILKStereoModeStateSizes    = uint32(3)
	libopusSILKStereoModePacket0       = uint32(4)
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
	targetRateBps        int32
	midTargetRateBps     int32
	sideTargetRateBps    int32
	midVAD               int32
	sideVAD              int32
	midSpeechActivityQ8  int32
	sideSpeechActivityQ8 int32
	midInputTiltQ15      int32
	sideInputTiltQ15     int32
	midSNRDBQ7           int32
	sideSNRDBQ7          int32
	maxBits              int32
	useCBR               int32
	condCoding           int32
	tellAfterSideInfo    int32
	midOnly              int32
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
	reader.ExpectRemaining(count * 16 * 4)
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
		midOnly:              reader.I32(),
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
			var enc Encoder
			setStereoStateFromOracle(&enc.stereo, tc.state)
			mid, side, ix, midOnly, midRate, sideRate, widthQ14 := enc.StereoLRToMSWithRates(
				int16PCMToFloat32(tc.left), int16PCMToFloat32(tc.right),
				tc.frameLength, tc.fsKHz, tc.totalRateBps, tc.speechActQ8, tc.toMono,
			)
			if libopusStereoBoolWord(midOnly) != want[i].midOnly {
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

	left, right := downsampleStereo48kTo16kPacket0(t, signal)
	enc := NewEncoder(BandwidthWideband)
	sideEnc := NewEncoder(BandwidthWideband)
	enc.SetBitrate(bitRate)
	sideEnc.SetBitrate(bitRate)
	enc.SetMaxBits(maxBits)
	sideEnc.SetMaxBits(maxBits)
	enc.SetVBR(false)
	sideEnc.SetVBR(false)
	enc.ResetPacketState()
	sideEnc.ResetPacketState()
	enc.nFramesPerPacket = 1
	sideEnc.nFramesPerPacket = 1

	var re rangecoding.Encoder
	re.Init(make([]byte, maxSilkPacketBytes))
	re.EncodeICDF16(0, []uint16{256 - (256 >> 4), 0}, 8)
	encodeStereoLBRRPacket(&re, enc, sideEnc, 1, &enc.stereo)
	totalRate := stereoAllocationTargetRate(enc, bitRate, len(left), re.Tell())
	midOut, sideOut, ix, _, midRate, sideRate, _ := enc.StereoLRToMSWithRates(
		left, right, len(left), 16, totalRate, enc.speechActivityQ8, false,
	)
	_ = midOut
	_ = sideOut
	EncodeStereoIndices(&re, ix)
	gotTell := int32(re.Tell())

	if int32(totalRate) != want.targetRateBps {
		t.Fatalf("TargetRate=%d want %d", totalRate, want.targetRateBps)
	}
	if int32(midRate) != want.midTargetRateBps || int32(sideRate) != want.sideTargetRateBps {
		t.Fatalf("MStargetRates=%d/%d want %d/%d", midRate, sideRate, want.midTargetRateBps, want.sideTargetRateBps)
	}
	if int32(maxBits/2) != want.maxBits || want.useCBR != 0 || want.condCoding != codeIndependently {
		t.Fatalf("frame controls maxBits/useCBR/condCoding=%d/%d/%d want %d/0/%d", maxBits/2, 0, codeIndependently, want.maxBits, codeIndependently)
	}
	if gotTell != want.tellAfterSideInfo {
		t.Fatalf("tellAfterSideInfo=%d want %d (libopus midVAD=%d sideVAD=%d midOnly=%d speech=%d/%d snr=%d/%d tilt=%d/%d)",
			gotTell, want.tellAfterSideInfo, want.midVAD, want.sideVAD, want.midOnly,
			want.midSpeechActivityQ8, want.sideSpeechActivityQ8, want.midSNRDBQ7, want.sideSNRDBQ7,
			want.midInputTiltQ15, want.sideInputTiltQ15)
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
	for i := 0; i < frameSize48; i++ {
		left48[i] = signal[2*i]
		right48[i] = signal[2*i+1]
	}
	left16 := make([]float32, frameSize16)
	right16 := make([]float32, frameSize16)
	if n := NewLibopusResampler(sampleRate, 16000).ProcessInto(left48, left16); n != frameSize16 {
		t.Fatalf("left resampler output=%d want %d", n, frameSize16)
	}
	if n := NewLibopusResampler(sampleRate, 16000).ProcessInto(right48, right16); n != frameSize16 {
		t.Fatalf("right resampler output=%d want %d", n, frameSize16)
	}
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
