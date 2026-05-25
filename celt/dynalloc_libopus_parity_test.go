package celt

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusCELTDynallocInputMagic  = "GCDI"
	libopusCELTDynallocOutputMagic = "GCDO"
)

var libopusCELTDynallocHelper libopustest.HelperCache

type libopusCELTDynallocCase struct {
	name              string
	start             int
	end               int
	channels          int
	lsbDepth          int
	lm                int
	effectiveBytes    int
	isTransient       bool
	vbr               bool
	constrainedVBR    bool
	lfe               bool
	toneFreq          float32
	toneishness       float32
	bandLogE          []float32
	bandLogE2         []float32
	oldBandE          []float32
	surroundDynalloc  [MaxBands]float32
	analysisValid     bool
	analysisLeakBoost [19]uint8
}

type libopusCELTDynallocResult struct {
	maxDepth     float32
	totBoost     int
	offsets      [MaxBands]int
	importance   [MaxBands]int
	spreadWeight [MaxBands]int
}

func getLibopusCELTDynallocHelperPath() (string, error) {
	return libopusCELTDynallocHelper.CHelperPath(libopustest.CHelperConfig{
		Label:        "celt dynalloc",
		OutputBase:   "gopus_libopus_celt_dynalloc",
		SourceFile:   "libopus_celt_dynalloc_info.c",
		ProbeRelPath: "celt/celt_encoder.c",
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O2", "-DNDEBUG"},
		RefIncludes:  []string{"celt", "silk"},
		Libs:         []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}

func probeLibopusCELTDynalloc(cases []libopusCELTDynallocCase) ([]libopusCELTDynallocResult, error) {
	binPath, err := getLibopusCELTDynallocHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusCELTDynallocInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.I32(int32(MaxBands))
		payload.I32(int32(tc.start))
		payload.I32(int32(tc.end))
		payload.I32(int32(tc.channels))
		payload.I32(int32(tc.lsbDepth))
		payload.I32(int32(tc.lm))
		payload.I32(int32(tc.effectiveBytes))
		payload.I32(boolToI32(tc.isTransient))
		payload.I32(boolToI32(tc.vbr))
		payload.I32(boolToI32(tc.constrainedVBR))
		payload.I32(boolToI32(tc.lfe))
		payload.Float32(tc.toneFreq)
		payload.Float32(tc.toneishness)
		payload.I32(boolToI32(tc.analysisValid))
		payload.Float32s(tc.bandLogE...)
		payload.Float32s(tc.bandLogE2...)
		payload.Float32s(tc.oldBandE...)
		payload.Float32s(tc.surroundDynalloc[:]...)
		for _, v := range tc.analysisLeakBoost {
			payload.U32(uint32(v))
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt dynalloc", libopusCELTDynallocOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	reader.ExpectRemaining(count * (2 + 3*MaxBands) * 4)
	out := make([]libopusCELTDynallocResult, count)
	for i := range out {
		out[i].maxDepth = reader.Float32()
		out[i].totBoost = int(reader.I32())
		for band := range out[i].offsets {
			out[i].offsets[band] = int(reader.I32())
		}
		for band := range out[i].importance {
			out[i].importance[band] = int(reader.I32())
		}
		for band := range out[i].spreadWeight {
			out[i].spreadWeight[band] = int(reader.I32())
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestLibopusCELTDynallocAnalysisMatchesReference(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusCELTDynallocCase{
		celtDynallocCase("mono_tonal_analysis", 1, 0, MaxBands, 24, 3, 96, false, true, false, false),
		celtDynallocCase("stereo_last_carry", 2, 0, MaxBands, 24, 1, 80, false, true, false, false),
		celtDynallocCase("cvbr_transient", 2, 2, 18, 16, 2, 88, true, true, true, false),
		celtDynallocCase("low_rate_lfe", 1, 0, 14, 16, 0, 20, false, true, false, true),
	}
	cases[0].analysisValid = true
	for i := range cases[0].analysisLeakBoost {
		cases[0].analysisLeakBoost[i] = uint8((i*11 + 7) & 0xff)
	}
	cases[0].toneFreq = 0.62
	cases[0].toneishness = 0.995
	cases[1].bandLogE2[10] = 1
	cases[1].bandLogE[MaxBands] = 10
	cases[1].bandLogE2[MaxBands] = 10
	cases[2].surroundDynalloc[5] = 1.75
	cases[2].surroundDynalloc[8] = 0.5

	want, err := probeLibopusCELTDynalloc(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt dynalloc", err)
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runGoDynallocCase(tc, nil)
			assertDynallocResultMatches(t, got, want[i])

			var scratch DynallocScratch
			gotScratch := runGoDynallocCase(tc, &scratch)
			assertDynallocResultMatches(t, gotScratch, want[i])
		})
	}
}

func runGoDynallocCase(tc libopusCELTDynallocCase, scratch *DynallocScratch) DynallocResult {
	logN := make([]int16, MaxBands)
	for i := range logN {
		logN[i] = int16(LogN[i])
	}
	bandLogE := float32sToFloat64s(tc.bandLogE)
	bandLogE2 := float32sToFloat64s(tc.bandLogE2)
	oldBandE := tc.oldBandE
	surround := tc.surroundDynalloc[:]
	leak := tc.analysisLeakBoost[:]
	if scratch != nil {
		return DynallocAnalysisWithScratch(
			bandLogE, bandLogE2, oldBandE,
			MaxBands, tc.start, tc.end, tc.channels, tc.lsbDepth, tc.lm,
			logN,
			tc.effectiveBytes,
			tc.isTransient, tc.vbr, tc.constrainedVBR, tc.lfe,
			float64(tc.toneFreq), float64(tc.toneishness),
			surround,
			tc.analysisValid, leak,
			scratch,
		)
	}
	return DynallocAnalysis(
		bandLogE, bandLogE2, oldBandE,
		MaxBands, tc.start, tc.end, tc.channels, tc.lsbDepth, tc.lm,
		logN,
		tc.effectiveBytes,
		tc.isTransient, tc.vbr, tc.constrainedVBR, tc.lfe,
		float64(tc.toneFreq), float64(tc.toneishness),
		surround,
		tc.analysisValid, leak,
	)
}

func assertDynallocResultMatches(t *testing.T, got DynallocResult, want libopusCELTDynallocResult) {
	t.Helper()
	if math.Float32bits(float32(got.MaxDepth)) != math.Float32bits(want.maxDepth) {
		t.Fatalf("maxDepth=%08x %.10g want %08x %.10g",
			math.Float32bits(float32(got.MaxDepth)), got.MaxDepth,
			math.Float32bits(want.maxDepth), want.maxDepth)
	}
	if got.TotBoost != want.totBoost {
		t.Fatalf("totBoost=%d want %d", got.TotBoost, want.totBoost)
	}
	for band := 0; band < MaxBands; band++ {
		if got.Offsets[band] != want.offsets[band] {
			t.Fatalf("offsets[%d]=%d want %d", band, got.Offsets[band], want.offsets[band])
		}
		if got.Importance[band] != want.importance[band] {
			t.Fatalf("importance[%d]=%d want %d", band, got.Importance[band], want.importance[band])
		}
		if got.SpreadWeight[band] != want.spreadWeight[band] {
			t.Fatalf("spreadWeight[%d]=%d want %d", band, got.SpreadWeight[band], want.spreadWeight[band])
		}
	}
}

func celtDynallocCase(name string, channels, start, end, lsbDepth, lm, effectiveBytes int, isTransient, vbr, constrainedVBR, lfe bool) libopusCELTDynallocCase {
	total := channels * MaxBands
	tc := libopusCELTDynallocCase{
		name:           name,
		start:          start,
		end:            end,
		channels:       channels,
		lsbDepth:       lsbDepth,
		lm:             lm,
		effectiveBytes: effectiveBytes,
		isTransient:    isTransient,
		vbr:            vbr,
		constrainedVBR: constrainedVBR,
		lfe:            lfe,
		toneFreq:       -1,
		bandLogE:       make([]float32, total),
		bandLogE2:      make([]float32, total),
		oldBandE:       make([]float32, total),
	}
	for c := 0; c < channels; c++ {
		for band := 0; band < MaxBands; band++ {
			idx := c*MaxBands + band
			base := 1.2 + 0.18*float64((band%7)-3) + 0.07*float64(c)
			wave := 0.31*math.Sin(float64((band+1)*(c+2))*0.67) + 0.11*math.Cos(float64(band+3)*0.41)
			tc.bandLogE[idx] = float32(base + wave)
			tc.bandLogE2[idx] = float32(base + 0.7*wave + 0.05*float64((band+c)%3-1))
			tc.oldBandE[idx] = float32(base - 0.13*wave + 0.03*float64((band+2*c)%5-2))
		}
	}
	return tc
}

func boolToI32(v bool) int32 {
	if v {
		return 1
	}
	return 0
}
