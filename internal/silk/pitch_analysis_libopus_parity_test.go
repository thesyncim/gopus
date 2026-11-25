package silk

import (
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/util"
	"math"
	"math/rand"
	"testing"
	"unsafe"
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
			enc := newTestEncoder(tc.bandwidth)
			enc.pitchEstimationComplexity = int32(tc.complexity)
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

	enc := newTestEncoder(BandwidthWideband)
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
			name:         "nb_max_lag_pulse_train_2subfr",
			bandwidth:    BandwidthNarrowband,
			nbSubfr:      2,
			complexity:   2,
			searchThres1: 0.7,
			searchThres2: 0.15,
			frame:        silkPitchOraclePulseTrain(BandwidthNarrowband, 2, peMaxLagMS*8, 14000),
		},
		{
			name:         "nb_max_lag_pulse_train_4subfr",
			bandwidth:    BandwidthNarrowband,
			nbSubfr:      4,
			complexity:   2,
			searchThres1: 0.7,
			searchThres2: 0.15,
			frame:        silkPitchOraclePulseTrain(BandwidthNarrowband, 4, peMaxLagMS*8, 14000),
		},
		{
			name:         "mb_max_lag_pulse_train_2subfr",
			bandwidth:    BandwidthMediumband,
			nbSubfr:      2,
			complexity:   2,
			searchThres1: 0.7,
			searchThres2: 0.15,
			frame:        silkPitchOraclePulseTrain(BandwidthMediumband, 2, peMaxLagMS*12, 14000),
		},
		{
			name:         "mb_max_lag_pulse_train_4subfr",
			bandwidth:    BandwidthMediumband,
			nbSubfr:      4,
			complexity:   2,
			searchThres1: 0.7,
			searchThres2: 0.15,
			frame:        silkPitchOraclePulseTrain(BandwidthMediumband, 4, peMaxLagMS*12, 14000),
		},
		{
			name:         "wb_max_lag_pulse_train_2subfr",
			bandwidth:    BandwidthWideband,
			nbSubfr:      2,
			complexity:   2,
			searchThres1: 0.7,
			searchThres2: 0.15,
			frame:        silkPitchOraclePulseTrain(BandwidthWideband, 2, peMaxLagMS*16, 14000),
		},
		{
			name:         "wb_max_lag_pulse_train_4subfr",
			bandwidth:    BandwidthWideband,
			nbSubfr:      4,
			complexity:   2,
			searchThres1: 0.7,
			searchThres2: 0.15,
			frame:        silkPitchOraclePulseTrain(BandwidthWideband, 4, peMaxLagMS*16, 14000),
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

// silkPitchOraclePulseTrain returns an impulse train with the given period
// (samples at the SILK rate), whose 4 kHz correlation peaks sharply at one lag.
func silkPitchOraclePulseTrain(bandwidth Bandwidth, nbSubfr, period int, amplitude float32) []float32 {
	cfg := GetBandwidthConfig(bandwidth)
	fsKHz := cfg.SampleRate / 1000
	n := (peLTPMemLengthMS + nbSubfr*peSubfrLengthMS) * fsKHz
	out := make([]float32, n)
	for i := 10 * fsKHz / 4; i < n; i += period {
		out[i] = amplitude
	}
	return out
}

func BenchmarkCeltPitchXcorrFloat(b *testing.B) {
	// Representative sizes for SILK pitch analysis
	// length ~ 80-160 samples (5-10ms at 16kHz)
	// maxPitch ~ 300 samples (to cover wide range)
	length := 120
	maxPitch := 300

	// Ensure x is long enough for the kernel window
	x := make([]float32, length)
	y := make([]float32, maxPitch+length+4)
	out := make([]float32, maxPitch)

	rng := rand.New(rand.NewSource(42))
	for i := range x {
		x[i] = rng.Float32()*2 - 1
	}
	for i := range y {
		y[i] = rng.Float32()*2 - 1
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		celtPitchXcorrFloat(x, y, out, length, maxPitch)
	}
}

func BenchmarkXcorrKernelFloat(b *testing.B) {
	// Kernel benchmarks for the 4-way loop
	length := 120
	x := make([]float32, length)
	y := make([]float32, length+4) // Kernel needs extra padding
	var sum [4]float32

	rng := rand.New(rand.NewSource(43))
	for i := range x {
		x[i] = rng.Float32()*2 - 1
	}
	for i := range y {
		y[i] = rng.Float32()*2 - 1
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		xcorrKernelFloat(x, y, &sum, length)
	}
}

func TestDetectPitchVoicedSignal(t *testing.T) {
	enc := newTestEncoder(BandwidthWideband)
	config := GetBandwidthConfig(BandwidthWideband)

	// Generate voiced signal at 200 Hz (pitch period = 80 samples at 16kHz)
	pitchPeriod := config.SampleRate / 200 // 80 samples
	numSubframes := 4

	// The pitch detector expects a frame that includes LTP memory.
	// Per libopus: frameLength = (PE_LTP_MEM_LENGTH_MS + nb_subfr * PE_SUBFR_LENGTH_MS) * Fs_kHz
	// = (20 + 4*5) * 16 = 640 samples
	fsKHz := config.SampleRate / 1000
	frameSamples := (peLTPMemLengthMS + numSubframes*peSubfrLengthMS) * fsKHz

	pcm := make([]float32, frameSamples)
	for i := range pcm {
		// Sawtooth-like voiced waveform with realistic amplitude
		// The pitch detector normalizer includes a constant term that requires
		// reasonable signal levels to function correctly
		phase := float64(i%pitchPeriod) / float64(pitchPeriod)
		pcm[i] = float32(1.0-2.0*phase) * 10000 // Realistic int16-ish level
	}

	// Detect pitch
	pitchLags, _, _ := enc.detectPitch(pcm, numSubframes, 0, 0)

	if len(pitchLags) != numSubframes {
		t.Fatalf("expected %d pitch lags, got %d", numSubframes, len(pitchLags))
	}

	// All lags should be close to pitch period
	for sf, lag := range pitchLags {
		// Allow 20% error due to edge effects and search granularity
		errorMargin := max(pitchPeriod/5, 2)
		error := util.Abs(int(lag) - pitchPeriod)
		if error > errorMargin {
			t.Errorf("subframe %d: detected lag %d, expected ~%d (error: %d)", sf, lag, pitchPeriod, error)
		}
	}
}

func TestDetectPitchNarrowband(t *testing.T) {
	enc := newTestEncoder(BandwidthNarrowband)
	config := GetBandwidthConfig(BandwidthNarrowband)

	// Generate voiced signal at 150 Hz (pitch period = ~53 samples at 8kHz)
	pitchPeriod := config.SampleRate / 150 // ~53 samples
	numSubframes := 4

	// The pitch detector expects a frame that includes LTP memory.
	fsKHz := config.SampleRate / 1000
	frameSamples := (peLTPMemLengthMS + numSubframes*peSubfrLengthMS) * fsKHz

	pcm := make([]float32, frameSamples)
	for i := range pcm {
		phase := float64(i%pitchPeriod) / float64(pitchPeriod)
		pcm[i] = float32(1.0-2.0*phase) * 10000 // Realistic int16-ish level
	}

	pitchLags, _, _ := enc.detectPitch(pcm, numSubframes, 0, 0)

	if len(pitchLags) != numSubframes {
		t.Fatalf("expected %d pitch lags, got %d", numSubframes, len(pitchLags))
	}

	// Verify lags are within valid range
	for sf, lag := range pitchLags {
		if int(lag) < config.PitchLagMin || int(lag) > config.PitchLagMax {
			t.Errorf("subframe %d: lag %d out of valid range [%d, %d]",
				sf, lag, config.PitchLagMin, config.PitchLagMax)
		}
	}
}

func TestDetectPitchUnvoicedZeroLags(t *testing.T) {
	enc := newTestEncoder(BandwidthWideband)
	config := GetBandwidthConfig(BandwidthWideband)

	numSubframes := 4
	fsKHz := config.SampleRate / 1000
	frameSamples := (peLTPMemLengthMS + numSubframes*peSubfrLengthMS) * fsKHz

	pcm := make([]float32, frameSamples) // Silence -> unvoiced

	pitchLags, _, _ := enc.detectPitch(pcm, numSubframes, 0, 0)
	if len(pitchLags) != numSubframes {
		t.Fatalf("expected %d pitch lags, got %d", numSubframes, len(pitchLags))
	}
	for sf, lag := range pitchLags {
		if lag != 0 {
			t.Fatalf("subframe %d: expected lag 0 for unvoiced, got %d", sf, lag)
		}
	}
	if enc.pitchState.ltpCorr != 0 {
		t.Fatalf("expected ltpCorr=0 for unvoiced, got %.6f", enc.pitchState.ltpCorr)
	}
	if enc.pitchState.prevLag != 0 {
		t.Fatalf("expected prevLag=0 for unvoiced, got %d", enc.pitchState.prevLag)
	}
}

func TestDownsample(t *testing.T) {
	signal := []float32{1, 2, 3, 4, 5, 6, 7, 8}

	ds := downsample(signal, 2)
	if len(ds) != 4 {
		t.Fatalf("expected 4 samples, got %d", len(ds))
	}

	// Average of pairs
	expected := []float32{1.5, 3.5, 5.5, 7.5}
	for i, v := range ds {
		if math.Abs(float64(v-expected[i])) > 0.01 {
			t.Errorf("ds[%d] = %f, expected %f", i, v, expected[i])
		}
	}
}

func TestDownsampleFactor4(t *testing.T) {
	signal := []float32{1, 2, 3, 4, 5, 6, 7, 8}

	ds := downsample(signal, 4)
	if len(ds) != 2 {
		t.Fatalf("expected 2 samples, got %d", len(ds))
	}

	// Average of groups of 4
	expected := []float32{2.5, 6.5}
	for i, v := range ds {
		if math.Abs(float64(v-expected[i])) > 0.01 {
			t.Errorf("ds[%d] = %f, expected %f", i, v, expected[i])
		}
	}
}

func TestDownsampleFactor1(t *testing.T) {
	signal := []float32{1, 2, 3, 4}

	ds := downsample(signal, 1)
	if len(ds) != 4 {
		t.Fatalf("expected 4 samples, got %d", len(ds))
	}

	// No downsampling should occur
	for i, v := range ds {
		if v != signal[i] {
			t.Errorf("ds[%d] = %f, expected %f", i, v, signal[i])
		}
	}
}

func TestCeltPitchXcorrFloatBounds(t *testing.T) {
	length := 40
	x := make([]float32, length)
	y := make([]float32, length) // Only one valid lag available.
	for i := range length {
		x[i] = float32(i + 1)
		y[i] = float32(i + 2)
	}
	out := make([]float32, 8)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("celtPitchXcorrFloat panicked: %v", r)
		}
	}()

	celtPitchXcorrFloat(x, y, out, length, len(out))

	want := innerProductF32Acc(x, y, length)
	if math.Abs(float64(out[0]-want)) > 1e-4 {
		t.Fatalf("out[0] = %f, want %f", out[0], want)
	}
	for i := 1; i < len(out); i++ {
		if out[i] != 0 {
			t.Fatalf("out[%d] = %f, want 0 (no valid lag)", i, out[i])
		}
	}
}

func TestAutocorrPitchSearch(t *testing.T) {
	// Generate periodic signal
	period := 40
	n := 320
	signal := make([]float32, n)
	for i := range signal {
		signal[i] = float32(math.Sin(2 * math.Pi * float64(i) / float64(period)))
	}

	lag := autocorrPitchSearch(signal, 20, 60)

	// Should find the period (or close to it)
	// Due to bias toward shorter lags and edge effects, allow some tolerance
	if lag < period-5 || lag > period+5 {
		t.Errorf("detected lag %d, expected ~%d", lag, period)
	}
}

func TestAutocorrPitchSearchEdgeCases(t *testing.T) {
	// Test with very short signal
	shortSignal := []float32{1, 2, 3, 4, 5}
	lag := autocorrPitchSearch(shortSignal, 1, 3)
	if lag < 1 || lag > 3 {
		t.Errorf("short signal: lag %d out of range [1, 3]", lag)
	}

	// Test with minLag > maxLag
	lag = autocorrPitchSearch(shortSignal, 10, 5)
	if lag != 10 {
		t.Errorf("expected minLag=10 when minLag > maxLag, got %d", lag)
	}
}

func TestQuantizeLTPCoeffs(t *testing.T) {
	// Test with known coefficients
	coeffs := []float32{0.5, 0.3, 0.1, -0.1, -0.05}

	quantized := quantizeLTPCoeffs(coeffs, 2)

	if len(quantized) != 5 {
		t.Fatalf("expected 5 coefficients, got %d", len(quantized))
	}

}

func TestQuantizeLTPCoeffsZeroCoeffs(t *testing.T) {
	// Test with zero coefficients
	coeffs := []float32{0, 0, 0, 0, 0}

	quantized := quantizeLTPCoeffs(coeffs, 1)

	if len(quantized) != 5 {
		t.Fatalf("expected 5 coefficients, got %d", len(quantized))
	}

}

func TestQuantizeLTPCoeffsLargeCoeffs(t *testing.T) {
	// Test with large coefficients (should clip to codebook range)
	coeffs := []float32{2.0, 1.5, 1.0, -1.0, -1.5}

	quantized := quantizeLTPCoeffs(coeffs, 2)

	if len(quantized) != 5 {
		t.Fatalf("expected 5 coefficients, got %d", len(quantized))
	}

}

func TestAnalyzeLTP(t *testing.T) {
	enc := newTestEncoder(BandwidthWideband)
	config := GetBandwidthConfig(BandwidthWideband)

	// Generate voiced signal
	pitchPeriod := 80 // 200 Hz at 16kHz
	frameSamples := config.SubframeSamples * 4

	pcm := make([]float32, frameSamples)
	for i := range pcm {
		phase := float64(i%pitchPeriod) / float64(pitchPeriod)
		pcm[i] = float32(1.0-2.0*phase) * (10000 * int16Scale)
	}

	pitchLags := []int32{int32(pitchPeriod), int32(pitchPeriod), int32(pitchPeriod), int32(pitchPeriod)}
	numSubframes := 4

	ltpCoeffs := enc.analyzeLTP(pcm, pitchLags, numSubframes, 2)

	var nonZeroTaps int
	for sf := range numSubframes {
		for _, tap := range ltpCoeffs[sf] {
			if tap != 0 {
				nonZeroTaps++
			}
		}
	}
	if nonZeroTaps == 0 {
		t.Fatal("analyzeLTP returned only zero taps for a voiced signal")
	}
}

func TestDeterminePeriodicity(t *testing.T) {
	enc := newTestEncoder(BandwidthWideband)
	config := GetBandwidthConfig(BandwidthWideband)

	// Generate highly periodic voiced signal
	pitchPeriod := 80
	frameSamples := config.SubframeSamples * 4

	pcm := make([]float32, frameSamples)
	for i := range pcm {
		pcm[i] = float32(math.Sin(2 * math.Pi * float64(i) / float64(pitchPeriod)))
	}

	pitchLags := []int32{int32(pitchPeriod), int32(pitchPeriod), int32(pitchPeriod), int32(pitchPeriod)}

	periodicity := enc.determinePeriodicity(pcm, pitchLags)

	// Highly periodic signal should result in high periodicity (2)
	if periodicity < 1 {
		t.Errorf("expected high periodicity (>=1) for periodic signal, got %d", periodicity)
	}
}

func TestFindLTPCodebookIndex(t *testing.T) {
	// Test with known codebook entry from LTPFilterHigh
	coeffs := LTPFilterHigh[0]

	idx := findLTPCodebookIndex(coeffs, 2)
	if idx != 0 {
		t.Errorf("expected index 0 for first entry, got %d", idx)
	}

	// Test with middle entry
	coeffs = LTPFilterHigh[15]
	idx = findLTPCodebookIndex(coeffs, 2)
	if idx != 15 {
		t.Errorf("expected index 15 for entry 15, got %d", idx)
	}

	// Test with low periodicity
	coeffs = LTPFilterLow[3]
	idx = findLTPCodebookIndex(coeffs, 0)
	if idx != 3 {
		t.Errorf("expected index 3 for LTPFilterLow[3], got %d", idx)
	}

	// Test with mid periodicity
	coeffs = LTPFilterMid[7]
	idx = findLTPCodebookIndex(coeffs, 1)
	if idx != 7 {
		t.Errorf("expected index 7 for LTPFilterMid[7], got %d", idx)
	}
}

func TestFindBestPitchContour(t *testing.T) {
	// Test with constant pitch lags
	pitchLags := []int32{100, 100, 100, 100}
	config := GetBandwidthConfig(BandwidthWideband)
	contourIdx, baseLag := findBestPitchContour(
		pitchLags,
		4,
		config.PitchLagMin,
		config.PitchLagMax,
		pitchCBLagsStage3Slice,
		len(silk_pitch_contour_iCDF),
	)

	// Should find a contour and base lag close to 100
	if baseLag < 95 || baseLag > 105 {
		t.Errorf("expected base lag ~100, got %d", baseLag)
	}

	// Contour index should be valid
	if contourIdx < 0 || contourIdx >= len(silk_pitch_contour_iCDF) {
		t.Errorf("contour index %d out of valid range [0, %d)", contourIdx, len(silk_pitch_contour_iCDF))
	}
}

func TestPitchMinMax(t *testing.T) {
	if pitchMin(5, 10) != 5 {
		t.Error("pitchMin(5, 10) should be 5")
	}
	if pitchMin(10, 5) != 5 {
		t.Error("pitchMin(10, 5) should be 5")
	}
	if pitchMax(5, 10) != 10 {
		t.Error("pitchMax(5, 10) should be 10")
	}
	if pitchMax(10, 5) != 10 {
		t.Error("pitchMax(10, 5) should be 10")
	}
}

// TestPitchDetectionAccuracyLibopusStyle tests pitch detection with various frequencies
// to validate the libopus-style multi-stage algorithm.
func TestPitchDetectionAccuracyLibopusStyle(t *testing.T) {
	enc := newTestEncoder(BandwidthWideband)
	config := GetBandwidthConfig(BandwidthWideband)
	numSubframes := 4
	fsKHz := config.SampleRate / 1000
	frameSamples := (peLTPMemLengthMS + numSubframes*peSubfrLengthMS) * fsKHz

	// Test various pitch frequencies (Hz)
	testFrequencies := []int{100, 150, 200, 250, 300, 350, 400}

	for _, freq := range testFrequencies {
		pitchPeriod := config.SampleRate / freq

		// Skip if pitch period is outside valid range
		if pitchPeriod < config.PitchLagMin || pitchPeriod > config.PitchLagMax {
			continue
		}

		pcm := make([]float32, frameSamples)
		for i := range pcm {
			// Sawtooth waveform (richer harmonics than sine)
			phase := float64(i%pitchPeriod) / float64(pitchPeriod)
			pcm[i] = float32(1.0-2.0*phase) * 10000
		}

		pitchLags, _, _ := enc.detectPitch(pcm, numSubframes, 0, 0)

		// Check accuracy - allow for harmonic multiples (octave errors are common
		// in pitch detection and may be acceptable depending on use case)
		for sf, lag := range pitchLags {
			// Check if lag matches fundamental or first harmonic (octave)
			isFundamental := util.Abs(int(lag)-pitchPeriod) <= pitchPeriod/10+2
			isOctave := util.Abs(int(lag)-2*pitchPeriod) <= pitchPeriod/5+2

			if !isFundamental && !isOctave {
				t.Errorf("freq=%dHz: subframe %d: detected lag %d, expected ~%d or ~%d",
					freq, sf, lag, pitchPeriod, 2*pitchPeriod)
			}
		}

		// Reset encoder for next test
		enc.reset()
	}
}

// TestPitchDetectionWithSine tests pitch detection with sinusoidal input.
func TestPitchDetectionWithSine(t *testing.T) {
	enc := newTestEncoder(BandwidthWideband)
	config := GetBandwidthConfig(BandwidthWideband)
	numSubframes := 4
	fsKHz := config.SampleRate / 1000
	frameSamples := (peLTPMemLengthMS + numSubframes*peSubfrLengthMS) * fsKHz

	// 200 Hz sine wave
	freq := 200.0
	pitchPeriod := config.SampleRate / int(freq)

	pcm := make([]float32, frameSamples)
	for i := range pcm {
		pcm[i] = float32(math.Sin(2*math.Pi*freq*float64(i)/float64(config.SampleRate))) * 10000
	}

	pitchLags, _, _ := enc.detectPitch(pcm, numSubframes, 0, 0)

	for sf, lag := range pitchLags {
		errorMargin := max(pitchPeriod/5, 2)
		if util.Abs(int(lag)-pitchPeriod) > errorMargin {
			t.Errorf("sine wave: subframe %d: detected lag %d, expected ~%d",
				sf, lag, pitchPeriod)
		}
	}
}

// TestPitchDetectionMediumband tests pitch detection for medium bandwidth.
func TestPitchDetectionMediumband(t *testing.T) {
	enc := newTestEncoder(BandwidthMediumband)
	config := GetBandwidthConfig(BandwidthMediumband)
	numSubframes := 4
	fsKHz := config.SampleRate / 1000
	frameSamples := (peLTPMemLengthMS + numSubframes*peSubfrLengthMS) * fsKHz

	// 150 Hz at 12kHz = 80 sample period
	pitchPeriod := config.SampleRate / 150

	pcm := make([]float32, frameSamples)
	for i := range pcm {
		phase := float64(i%pitchPeriod) / float64(pitchPeriod)
		pcm[i] = float32(1.0-2.0*phase) * 10000
	}

	pitchLags, _, _ := enc.detectPitch(pcm, numSubframes, 0, 0)

	for sf, lag := range pitchLags {
		// Verify lags are within valid range
		if int(lag) < config.PitchLagMin || int(lag) > config.PitchLagMax {
			t.Errorf("mediumband: subframe %d: lag %d out of range [%d, %d]",
				sf, lag, config.PitchLagMin, config.PitchLagMax)
		}
	}
}

// TestDownsampleLowpass tests the low-pass downsampling function.
func TestDownsampleLowpass(t *testing.T) {
	// Create a simple test signal
	signal := make([]float32, 100)
	for i := range signal {
		signal[i] = float32(i)
	}

	ds := downsampleLowpass(signal, 2)

	// Should have half the samples
	if len(ds) != 50 {
		t.Errorf("expected 50 samples, got %d", len(ds))
	}

	// Values should be smoothed versions
	// The filter uses [0.25, 0.5, 0.25] coefficients
	// For sample 1: 0.25*0 + 0.5*2 + 0.25*3 = 1.75
	// Allowing some tolerance for the edge handling
	if ds[1] < 1.0 || ds[1] > 3.0 {
		t.Errorf("ds[1] = %f, expected ~1.75", ds[1])
	}
}

// TestLagrangianInterpolate tests sub-sample pitch refinement.
func TestLagrangianInterpolate(t *testing.T) {
	// Test case where peak is at center
	offset := lagrangianInterpolate(0.9, 1.0, 0.9)
	if abs32(offset) > 0.01 {
		t.Errorf("symmetric case: expected offset ~0, got %f", offset)
	}

	// Test case where peak is shifted right
	offset = lagrangianInterpolate(0.8, 1.0, 0.9)
	if offset < 0 || offset > 0.5 {
		t.Errorf("right-shifted case: expected positive offset, got %f", offset)
	}

	// Test case where peak is shifted left
	offset = lagrangianInterpolate(0.9, 1.0, 0.8)
	if offset > 0 || offset < -0.5 {
		t.Errorf("left-shifted case: expected negative offset, got %f", offset)
	}
}

// TestEnergyFLP tests the energy computation function.
func TestEnergyFLP(t *testing.T) {
	data := []float32{1, 2, 3, 4, 5}
	energy := energyFLP(data)

	// Expected: 1 + 4 + 9 + 16 + 25 = 55
	if math.Abs(float64(energy)-55.0) > 0.001 {
		t.Errorf("expected energy 55, got %f", energy)
	}
}

// TestPitchContourTables verifies the contour table dimensions match libopus.
func TestPitchContourTables(t *testing.T) {
	// Stage 2 contours should have 4 subframes x 11 entries
	if len(pitchCBLagsStage2) != peMaxNbSubfr {
		t.Errorf("pitchCBLagsStage2 has %d subframes, expected %d", len(pitchCBLagsStage2), peMaxNbSubfr)
	}
	if len(pitchCBLagsStage2[0]) != peNbCbksStage2Ext {
		t.Errorf("pitchCBLagsStage2[0] has %d entries, expected %d", len(pitchCBLagsStage2[0]), peNbCbksStage2Ext)
	}

	// Stage 3 contours should have 4 subframes x 34 entries
	if len(pitchCBLagsStage3) != peMaxNbSubfr {
		t.Errorf("pitchCBLagsStage3 has %d subframes, expected %d", len(pitchCBLagsStage3), peMaxNbSubfr)
	}
	if len(pitchCBLagsStage3[0]) != peNbCbksStage3Max {
		t.Errorf("pitchCBLagsStage3[0] has %d entries, expected %d", len(pitchCBLagsStage3[0]), peNbCbksStage3Max)
	}
}

const (
	libopusSILKPitchXcorrInputMagic  = "GSPX"
	libopusSILKPitchXcorrOutputMagic = "GSPY"
)

var libopusSILKPitchXcorrHelper libopustest.HelperCache
var libopusSILKPitchXcorrScalarHelper libopustest.HelperCache

func getLibopusSILKPitchXcorrHelperPath() (string, error) {
	return libopusSILKPitchXcorrHelper.CHelperPath(libopustest.CHelperConfig{
		Label:        "silk pitch xcorr",
		OutputBase:   "gopus_libopus_silk_pitch_xcorr",
		SourceFile:   "libopus_silk_pitch_xcorr_info.c",
		ProbeRelPath: "celt/pitch.c",
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O2", "-ffp-contract=off"},
		RefIncludes:  []string{"celt", "silk"},
		RefSources:   []string{"celt/pitch.c"},
		DeadStrip:    true,
	})
}

func getLibopusSILKPitchXcorrScalarHelperPath() (string, error) {
	return libopusSILKPitchXcorrScalarHelper.CHelperPath(libopustest.CHelperConfig{
		Label:          "compiler-matched scalar SILK pitch xcorr",
		OutputBase:     "gopus_libopus_silk_pitch_xcorr_scalar_matched",
		SourceFile:     "libopus_silk_pitch_xcorr_info.c",
		ProbeRelPath:   "celt/pitch.c",
		CFlags:         []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes:    []string{"celt", "silk"},
		RefSources:     []string{"celt/pitch.c"},
		ForceScalarRef: true,
		DeadStrip:      true,
	})
}

type libopusSILKPitchXcorrCase struct {
	name     string
	length   int
	maxPitch int
	x        []float32
	y        []float32
}

func probeLibopusSILKPitchXcorr(cases []libopusSILKPitchXcorrCase) ([][]float32, error) {
	binPath, err := getLibopusSILKPitchXcorrHelperPath()
	if err != nil {
		return nil, err
	}
	return probeLibopusSILKPitchXcorrHelper(binPath, cases, "silk pitch xcorr non-contracting scalar diagnostic")
}

func probeLibopusSILKPitchXcorrScalar(cases []libopusSILKPitchXcorrCase) ([][]float32, error) {
	binPath, err := getLibopusSILKPitchXcorrScalarHelperPath()
	if err != nil {
		return nil, err
	}
	return probeLibopusSILKPitchXcorrHelper(binPath, cases, "compiler-matched scalar silk pitch xcorr")
}

func probeLibopusSILKPitchXcorrHelper(binPath string, cases []libopusSILKPitchXcorrCase, label string) ([][]float32, error) {
	payload := libopustest.NewOraclePayload(libopusSILKPitchXcorrInputMagic, 0, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.length))
		payload.U32(uint32(tc.maxPitch))
		payload.Float32s(tc.x[:tc.length]...)
		payload.Float32s(tc.y[:tc.length+tc.maxPitch]...)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), label, libopusSILKPitchXcorrOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([][]float32, count)
	for i := range out {
		maxPitch := int(reader.U32())
		if maxPitch <= 0 || maxPitch > 128 {
			return nil, fmt.Errorf("helper maxPitch=%d", maxPitch)
		}
		out[i] = make([]float32, maxPitch)
		for j := range out[i] {
			out[i][j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKPitchXcorrNonContractingScalarDiagnostic(t *testing.T) {
	// This helper calls scalar C xcorr with contraction off to diagnose expression
	// order. Keep its Go comparison independent from the target-selected production
	// MAC so this gate continues to cover the two-rounding source expression.
	libopustest.RequireOracle(t)
	cases := silkPitchXcorrLibopusCases()
	want, err := probeLibopusSILKPitchXcorr(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk pitch xcorr non-contracting scalar diagnostic", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := silkPitchXcorrNonContractingReference(tc.x, tc.y, tc.length, tc.maxPitch)
			if len(got) != len(want[i]) {
				t.Fatalf("xcorr len=%d want %d", len(got), len(want[i]))
			}
			for j := range got {
				if !pitchXcorrFloatMatches(got[j], want[i][j]) {
					t.Fatalf("xcorr[%d]=%08x %.10g want %08x %.10g",
						j,
						math.Float32bits(got[j]), got[j],
						math.Float32bits(want[i][j]), want[i][j])
				}
			}
		})
	}
}

func silkPitchXcorrNonContractingReference(x, y []float32, length, maxPitch int) []float32 {
	out := make([]float32, maxPitch)
	for pitch := range maxPitch {
		var sum float32
		for i := range length {
			sum = round32(sum + noFMA32(x[i], y[pitch+i]))
		}
		out[pitch] = sum
	}
	return out
}

func silkPitchXcorrLibopusCases() []libopusSILKPitchXcorrCase {
	return []libopusSILKPitchXcorrCase{
		{name: "short_tail", length: 7, maxPitch: 5, x: silkPitchXcorrOracleSignal(7, 0x11111111), y: silkPitchXcorrOracleSignal(12, 0x22222222)},
		{name: "unrolled", length: 32, maxPitch: 9, x: silkPitchXcorrOracleSignal(32, 0x33333333), y: silkPitchXcorrOracleSignal(41, 0x44444444)},
		{name: "pitch_frame", length: 120, maxPitch: 37, x: silkPitchXcorrOracleSignal(120, 0x55555555), y: silkPitchXcorrOracleSignal(157, 0x66666666)},
		{name: "odd_frame", length: 119, maxPitch: 31, x: silkPitchXcorrOracleSignal(119, 0x77777777), y: silkPitchXcorrOracleSignal(150, 0x88888888)},
	}
}

func pitchXcorrFloatMatches(got, want float32) bool {
	// The non-contracting reference and C diagnostic compare every result bit.
	return math.Float32bits(got) == math.Float32bits(want)
}

func silkPitchXcorrOracleSignal(n int, seed uint32) []float32 {
	x := make([]float32, n)
	state := seed
	for i := range x {
		state = state*1103515245 + 12345
		v := float32(int32(state>>9)&0x7fff-16384) * (1.0 / 16384.0)
		x[i] = v * float32(0.5+0.5*float32((i%5)+1)/5)
	}
	return x
}
