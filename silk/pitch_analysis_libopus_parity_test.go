package silk

import (
	"math"
	"testing"
	"unsafe"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKPitchAnalysisInputMagic  = "GSPA"
	libopusSILKPitchAnalysisOutputMagic = "GSPB"
)

var libopusSILKPitchAnalysisHelper libopustest.HelperCache

type libopusSILKPitchAnalysisCase struct {
	name         string
	bandwidth    Bandwidth
	nbSubfr      int
	complexity   int
	prevLag      int
	ltpCorr      float32
	searchThres1 float32
	searchThres2 float32
	frame        []float32
}

type libopusSILKPitchAnalysisResult struct {
	ret          int
	pitchOut     [peMaxNbSubfr]int
	lagIndex     int
	contourIndex int
	ltpCorr      float32
}

type libopusSILKPitchAnalysisTypeSizes struct {
	silkFloat int
	opusVal32 int
	opusInt16 int
}

func getLibopusSILKPitchAnalysisHelperPath() (string, error) {
	return libopusSILKPitchAnalysisHelper.CHelperPath(libopustest.CHelperConfig{
		Label:        "silk pitch analysis",
		OutputBase:   "gopus_libopus_silk_pitch_analysis",
		SourceFile:   "libopus_silk_pitch_analysis_info.c",
		ProbeRelPath: "silk/float/SigProc_FLP.h",
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes:  []string{"celt", "silk", "silk/float"},
		Libs:         []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}

func probeLibopusSILKPitchAnalysis(cases []libopusSILKPitchAnalysisCase) ([]libopusSILKPitchAnalysisResult, libopusSILKPitchAnalysisTypeSizes, error) {
	binPath, err := getLibopusSILKPitchAnalysisHelperPath()
	if err != nil {
		return nil, libopusSILKPitchAnalysisTypeSizes{}, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKPitchAnalysisInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		cfg := GetBandwidthConfig(tc.bandwidth)
		payload.I32(int32(cfg.SampleRate / 1000))
		payload.I32(int32(tc.nbSubfr))
		payload.I32(int32(tc.complexity))
		payload.I32(int32(tc.prevLag))
		payload.Float32(tc.ltpCorr)
		payload.Float32(tc.searchThres1)
		payload.Float32(tc.searchThres2)
		payload.I32(int32(len(tc.frame)))
		payload.Float32s(tc.frame...)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk pitch analysis", libopusSILKPitchAnalysisOutputMagic)
	if err != nil {
		return nil, libopusSILKPitchAnalysisTypeSizes{}, err
	}
	count := reader.Count(len(cases))
	sizes := libopusSILKPitchAnalysisTypeSizes{
		silkFloat: int(reader.U32()),
		opusVal32: int(reader.U32()),
		opusInt16: int(reader.U32()),
	}
	reader.ExpectRemaining(count * ((1 + peMaxNbSubfr + 2 + 1) * 4))
	out := make([]libopusSILKPitchAnalysisResult, count)
	for i := range out {
		out[i].ret = int(reader.I32())
		for j := range out[i].pitchOut {
			out[i].pitchOut[j] = int(reader.I32())
		}
		out[i].lagIndex = int(reader.I32())
		out[i].contourIndex = int(reader.I32())
		out[i].ltpCorr = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, libopusSILKPitchAnalysisTypeSizes{}, err
	}
	return out, sizes, nil
}

func TestSILKPitchAnalysisCoreMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := silkPitchAnalysisOracleCases()
	want, _, err := probeLibopusSILKPitchAnalysis(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk pitch analysis", err)
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(tc.bandwidth)
			enc.pitchEstimationComplexity = tc.complexity
			enc.pitchState.prevLag = int32(tc.prevLag)
			enc.pitchState.ltpCorr = tc.ltpCorr
			gotLags, gotLagIndex, gotContourIndex := enc.detectPitch(tc.frame, tc.nbSubfr, tc.searchThres1, tc.searchThres2)

			gotRet := 1
			for _, lag := range gotLags {
				if lag != 0 {
					gotRet = 0
					break
				}
			}
			if gotRet != want[i].ret {
				t.Fatalf("ret=%d want %d lags=%v", gotRet, want[i].ret, gotLags)
			}
			for sf := 0; sf < tc.nbSubfr; sf++ {
				if got := gotLags[sf]; got != int32(want[i].pitchOut[sf]) {
					t.Fatalf("pitch_out[%d]=%d want %d", sf, got, want[i].pitchOut[sf])
				}
			}
			if gotLagIndex != want[i].lagIndex {
				t.Fatalf("lagIndex=%d want %d", gotLagIndex, want[i].lagIndex)
			}
			if gotContourIndex != want[i].contourIndex {
				t.Fatalf("contourIndex=%d want %d", gotContourIndex, want[i].contourIndex)
			}
			if math.Float32bits(enc.pitchState.ltpCorr) != math.Float32bits(want[i].ltpCorr) {
				t.Fatalf("LTPCorr=%08x %.10g want %08x %.10g",
					math.Float32bits(enc.pitchState.ltpCorr), enc.pitchState.ltpCorr,
					math.Float32bits(want[i].ltpCorr), want[i].ltpCorr)
			}
		})
	}
}

func TestSILKPitchAnalysisScratchMatchesLibopusFloatSize(t *testing.T) {
	libopustest.RequireOracle(t)
	_, sizes, err := probeLibopusSILKPitchAnalysis(nil)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk pitch analysis", err)
	}
	if sizes.silkFloat != 4 || sizes.opusVal32 != 4 || sizes.opusInt16 != 2 {
		t.Fatalf("libopus SILK pitch sizes: silk_float=%d opus_val32=%d opus_int16=%d",
			sizes.silkFloat, sizes.opusVal32, sizes.opusInt16)
	}

	enc := NewEncoder(BandwidthWideband)
	frame := silkPitchOracleWave(BandwidthWideband, 4, 200, 8000)
	enc.detectPitch(frame, 4, 0.3, 0.2)

	got := []struct {
		name string
		size uintptr
		want int
	}{
		{"scratchPitchC", unsafe.Sizeof(enc.scratchPitchC[0]), sizes.silkFloat},
		{"scratchPitchCorrSt3", unsafe.Sizeof(enc.scratchPitchCorrSt3[0]), sizes.silkFloat},
		{"scratchPitchEnergySt3", unsafe.Sizeof(enc.scratchPitchEnergySt3[0]), sizes.silkFloat},
		{"scratchPitchXcorr", unsafe.Sizeof(enc.scratchPitchXcorr[0]), sizes.opusVal32},
		{"scratchFrame16Fix", unsafe.Sizeof(enc.scratchFrame16Fix[0]), sizes.opusInt16},
	}
	for _, tc := range got {
		if tc.size != uintptr(tc.want) {
			t.Fatalf("%s element size=%d want libopus size %d", tc.name, tc.size, tc.want)
		}
	}
}

func silkPitchAnalysisOracleCases() []libopusSILKPitchAnalysisCase {
	return []libopusSILKPitchAnalysisCase{
		{
			name:         "nb_voiced_4subfr",
			bandwidth:    BandwidthNarrowband,
			nbSubfr:      4,
			complexity:   2,
			searchThres1: 0.3,
			searchThres2: 0.2,
			frame:        silkPitchOracleWave(BandwidthNarrowband, 4, 150, 9000),
		},
		{
			name:         "mb_voiced_4subfr",
			bandwidth:    BandwidthMediumband,
			nbSubfr:      4,
			complexity:   2,
			searchThres1: 0.3,
			searchThres2: 0.2,
			frame:        silkPitchOracleWave(BandwidthMediumband, 4, 180, 8500),
		},
		{
			name:         "wb_voiced_4subfr",
			bandwidth:    BandwidthWideband,
			nbSubfr:      4,
			complexity:   2,
			searchThres1: 0.3,
			searchThres2: 0.2,
			frame:        silkPitchOracleWave(BandwidthWideband, 4, 200, 8000),
		},
		{
			name:         "wb_voiced_2subfr",
			bandwidth:    BandwidthWideband,
			nbSubfr:      2,
			complexity:   2,
			searchThres1: 0.3,
			searchThres2: 0.2,
			frame:        silkPitchOracleWave(BandwidthWideband, 2, 200, 8000),
		},
		{
			name:         "wb_prev_lag_bias",
			bandwidth:    BandwidthWideband,
			nbSubfr:      4,
			complexity:   2,
			prevLag:      96,
			ltpCorr:      0.62,
			searchThres1: 0.3,
			searchThres2: 0.2,
			frame:        silkPitchOracleWave(BandwidthWideband, 4, 170, 7600),
		},
		{
			name:         "nb_max_lag_boundary_4subfr",
			bandwidth:    BandwidthNarrowband,
			nbSubfr:      4,
			complexity:   2,
			searchThres1: 0.2,
			searchThres2: 0.15,
			frame:        silkPitchOraclePeriodWave(BandwidthNarrowband, 4, peMaxLagMS*8, 14000),
		},
		{
			name:         "mb_max_lag_boundary_4subfr",
			bandwidth:    BandwidthMediumband,
			nbSubfr:      4,
			complexity:   2,
			searchThres1: 0.2,
			searchThres2: 0.15,
			frame:        silkPitchOraclePeriodWave(BandwidthMediumband, 4, peMaxLagMS*12, 14000),
		},
		{
			name:         "wb_max_lag_boundary_4subfr",
			bandwidth:    BandwidthWideband,
			nbSubfr:      4,
			complexity:   2,
			searchThres1: 0.2,
			searchThres2: 0.15,
			frame:        silkPitchOraclePeriodWave(BandwidthWideband, 4, peMaxLagMS*16, 14000),
		},
		{
			name:         "wb_max_lag_boundary_2subfr",
			bandwidth:    BandwidthWideband,
			nbSubfr:      2,
			complexity:   2,
			searchThres1: 0.2,
			searchThres2: 0.15,
			frame:        silkPitchOraclePeriodWave(BandwidthWideband, 2, peMaxLagMS*16, 14000),
		},
		{
			name:         "wb_silence",
			bandwidth:    BandwidthWideband,
			nbSubfr:      4,
			complexity:   2,
			searchThres1: 0.3,
			searchThres2: 0.2,
			frame:        make([]float32, (peLTPMemLengthMS+4*peSubfrLengthMS)*16),
		},
	}
}

func silkPitchOraclePeriodWave(bandwidth Bandwidth, nbSubfr, period int, amplitude float32) []float32 {
	cfg := GetBandwidthConfig(bandwidth)
	fsKHz := cfg.SampleRate / 1000
	n := (peLTPMemLengthMS + nbSubfr*peSubfrLengthMS) * fsKHz
	out := make([]float32, n)
	for i := range out {
		phase := 2 * math.Pi * float64(i%period) / float64(period)
		out[i] = amplitude * float32(math.Sin(phase)+0.15*math.Sin(2*phase+0.1))
	}
	return out
}

func silkPitchOracleWave(bandwidth Bandwidth, nbSubfr, frequency int, amplitude float32) []float32 {
	cfg := GetBandwidthConfig(bandwidth)
	fsKHz := cfg.SampleRate / 1000
	n := (peLTPMemLengthMS + nbSubfr*peSubfrLengthMS) * fsKHz
	out := make([]float32, n)
	for i := range out {
		phase := 2 * math.Pi * float64(frequency) * float64(i) / float64(cfg.SampleRate)
		harm := 0.35 * math.Sin(2*phase+0.25)
		out[i] = amplitude * float32(math.Sin(phase)+harm)
	}
	return out
}
