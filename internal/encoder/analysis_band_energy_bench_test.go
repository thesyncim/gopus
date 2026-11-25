package encoder

import (
	"encoding/binary"
	"fmt"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
	"math"
	"math/rand"
	"reflect"
	"testing"
)

const (
	analysisBenchBinStart = 4
	analysisBenchBinEnd   = 240
	analysisBenchNumBins  = analysisBenchBinEnd - analysisBenchBinStart
)

const (
	analysisBenchLog2Scale = float32(0.7213475)
	analysisBenchBinScale  = (1.0 / (celtSigScale * celtSigScale)) * analysisFFTEnergyScale
)

type analysisBandBenchInput struct {
	binE      [analysisBenchNumBins]float32
	tonality  [analysisBenchBinEnd]float32
	noisiness [analysisBenchBinEnd]float32
}

var analysisBandBenchSink float32

func newAnalysisBandBenchInput() analysisBandBenchInput {
	var in analysisBandBenchInput
	for i := range analysisBenchNumBins {
		phase := 0.19 * float64(i+1)
		v := 0.52 + 0.48*math.Sin(phase)
		in.binE[i] = float32(0.001 + v*v)
	}
	for i := range analysisBenchBinEnd {
		phase := 0.11 * float64(i+1)
		in.tonality[i] = float32(0.72*math.Sin(phase) + 0.12)
		in.noisiness[i] = float32(0.22 + 0.18*math.Cos(0.07*float64(i+1)))
	}
	return in
}

func newAnalysisBandBenchState() *TonalityAnalysisState {
	s := NewTonalityAnalysisState(48000)
	s.Count = 512
	s.ECount = 3
	s.PrevBandwidth = 18

	for b := range NbTBands {
		s.LowE[b] = -8.0 + 0.1*float32(b)
		s.HighE[b] = s.LowE[b] + 5.5
		s.PrevBandTonality[b] = 0.35 + 0.02*float32((b+3)%5)
		s.MeanE[b] = 0.25 + 0.04*float32((b+1)%7)
	}

	for i := range NbFrames {
		for b := range NbTBands {
			e := 0.15 + 0.01*float32((i+b+2)%11)
			s.E[i][b] = e
			s.SqrtE[i][b] = float32(math.Sqrt(float64(e)))
			s.LogE[i][b] = float32(math.Log(float64(e) + 1e-10))
		}
	}

	return s
}

func analysisBandEnergyLegacy(
	s *TonalityAnalysisState,
	in *analysisBandBenchInput,
	logE []float32,
	bandLog2 []float32,
	masked []bool,
) float32 {
	frameNoisiness := float32(0)
	frameStationarity := float32(0)
	frameTonality := float32(0)
	maxFrameTonality := float32(0)
	relativeE := float32(0)
	frameLoudness := float32(0)
	slope := float32(0)
	bandwidthMask := float32(0)
	bandwidth := 0
	maxE := float32(0)
	belowMaxPitch := float32(0)
	aboveMaxPitch := float32(0)
	var bandTonality [NbTBands]float32

	for b := range NbTBands {
		var bandE, tE, nE float32
		for i := tbands[b]; i < tbands[b+1]; i++ {
			binE := in.binE[i-analysisBenchBinStart] * analysisBenchBinScale
			bandE += binE
			tE += binE * maxf(0, in.tonality[i])
			nE += binE * 2.0 * (0.5 - in.noisiness[i])
		}

		s.E[s.ECount][b] = bandE
		logE[b] = float32(math.Log(float64(bandE) + 1e-10))
		bandLog2[b+1] = analysisBenchLog2Scale * float32(math.Log(float64(bandE)+1e-10))
		s.LogE[s.ECount][b] = logE[b]

		frameNoisiness += nE / (1e-15 + bandE)
		frameLoudness += float32(math.Sqrt(float64(bandE + 1e-10)))

		if logE[b] > s.HighE[b] {
			s.HighE[b] = logE[b]
			s.LowE[b] = maxf(s.LowE[b], s.HighE[b]-15)
		} else if logE[b] < s.LowE[b] {
			s.LowE[b] = logE[b]
			s.HighE[b] = minf(s.HighE[b], s.LowE[b]+15)
		}
		relativeE += (logE[b] - s.LowE[b]) / (1e-5 + (s.HighE[b] - s.LowE[b]))

		var L1, L2 float32
		for i := range NbFrames {
			L1 += float32(math.Sqrt(float64(s.E[i][b])))
			L2 += s.E[i][b]
		}
		stationarity := minf(0.99, L1/float32(math.Sqrt(float64(1e-15+float32(NbFrames)*L2))))
		stationarity *= stationarity
		stationarity *= stationarity
		frameStationarity += stationarity

		bandTonality[b] = maxf(tE/(1e-15+bandE), stationarity*s.PrevBandTonality[b])
		frameTonality += bandTonality[b]
		if b >= NbTBands-NbTonalSkipBands {
			frameTonality -= bandTonality[b-NbTBands+NbTonalSkipBands]
		}
		maxFrameTonality = maxf(maxFrameTonality, (1.0+0.03*float32(b-NbTBands))*frameTonality)
		slope += bandTonality[b] * float32(b-8)
		s.PrevBandTonality[b] = bandTonality[b]
	}

	for b := range NbTBands {
		bandStart := tbands[b]
		bandEnd := tbands[b+1]
		Eraw := float32(0)
		for i := bandStart; i < bandEnd; i++ {
			Eraw += in.binE[i-analysisBenchBinStart]
		}
		E := Eraw * analysisBenchBinScale
		maxE = maxf(maxE, E)
		if bandStart < 64 {
			belowMaxPitch += E
		} else {
			aboveMaxPitch += E
		}
		s.MeanE[b] = maxf(0.95*s.MeanE[b], E)
		Em := maxf(E, s.MeanE[b])
		width := float32(bandEnd - bandStart)
		if E*1e9 > maxE && (Em > 0.001*width || E > 0.0003*width) {
			bandwidth = b + 1
		}
		maskThresh := float32(0.05)
		if int(s.PrevBandwidth) >= b+1 {
			maskThresh = 0.01
		}
		masked[b] = E < maskThresh*bandwidthMask
		bandwidthMask = maxf(0.05*bandwidthMask, E)
	}

	if aboveMaxPitch > belowMaxPitch {
		_ = belowMaxPitch / aboveMaxPitch
	}

	s.ECount = (s.ECount + 1) % NbFrames
	return frameNoisiness + frameStationarity + maxFrameTonality + relativeE + frameLoudness + slope + float32(bandwidth)
}

func analysisBandEnergyCurrent(
	s *TonalityAnalysisState,
	in *analysisBandBenchInput,
	logE []float32,
	bandLog2 []float32,
	masked []bool,
) float32 {
	frameNoisiness := float32(0)
	frameStationarity := float32(0)
	frameTonality := float32(0)
	maxFrameTonality := float32(0)
	relativeE := float32(0)
	frameLoudness := float32(0)
	slope := float32(0)
	bandwidthMask := float32(0)
	bandwidth := 0
	maxE := float32(0)
	belowMaxPitch := float32(0)
	aboveMaxPitch := float32(0)
	var bandTonality [NbTBands]float32
	var bandERaw [NbTBands]float32

	for b := range NbTBands {
		var bandE, tE, nE, rawE float32
		for i := tbands[b]; i < tbands[b+1]; i++ {
			binERaw := in.binE[i-analysisBenchBinStart]
			binE := binERaw * analysisBenchBinScale
			rawE += binERaw
			bandE += binE
			tE += binE * maxf(0, in.tonality[i])
			nE += binE * 2.0 * (0.5 - in.noisiness[i])
		}
		bandERaw[b] = rawE

		s.E[s.ECount][b] = bandE
		logBandE := float32(math.Log(float64(bandE) + 1e-10))
		logE[b] = logBandE
		bandLog2[b+1] = analysisBenchLog2Scale * logBandE
		s.LogE[s.ECount][b] = logE[b]
		s.SqrtE[s.ECount][b] = float32(math.Sqrt(float64(bandE)))

		frameNoisiness += nE / (1e-15 + bandE)
		frameLoudness += float32(math.Sqrt(float64(bandE + 1e-10)))

		if logE[b] > s.HighE[b] {
			s.HighE[b] = logE[b]
			s.LowE[b] = maxf(s.LowE[b], s.HighE[b]-15)
		} else if logE[b] < s.LowE[b] {
			s.LowE[b] = logE[b]
			s.HighE[b] = minf(s.HighE[b], s.LowE[b]+15)
		}
		relativeE += (logE[b] - s.LowE[b]) / (1e-5 + (s.HighE[b] - s.LowE[b]))

		var L1, L2 float32
		for i := range NbFrames {
			L1 += s.SqrtE[i][b]
			L2 += s.E[i][b]
		}
		stationarity := minf(0.99, L1/float32(math.Sqrt(float64(1e-15+float32(NbFrames)*L2))))
		stationarity *= stationarity
		stationarity *= stationarity
		frameStationarity += stationarity

		bandTonality[b] = maxf(tE/(1e-15+bandE), stationarity*s.PrevBandTonality[b])
		frameTonality += bandTonality[b]
		if b >= NbTBands-NbTonalSkipBands {
			frameTonality -= bandTonality[b-NbTBands+NbTonalSkipBands]
		}
		maxFrameTonality = maxf(maxFrameTonality, (1.0+0.03*float32(b-NbTBands))*frameTonality)
		slope += bandTonality[b] * float32(b-8)
		s.PrevBandTonality[b] = bandTonality[b]
	}

	for b := range NbTBands {
		bandStart := tbands[b]
		bandEnd := tbands[b+1]
		E := bandERaw[b] * analysisBenchBinScale
		maxE = maxf(maxE, E)
		if bandStart < 64 {
			belowMaxPitch += E
		} else {
			aboveMaxPitch += E
		}
		s.MeanE[b] = maxf(0.95*s.MeanE[b], E)
		Em := maxf(E, s.MeanE[b])
		width := float32(bandEnd - bandStart)
		if E*1e9 > maxE && (Em > 0.001*width || E > 0.0003*width) {
			bandwidth = b + 1
		}
		maskThresh := float32(0.05)
		if int(s.PrevBandwidth) >= b+1 {
			maskThresh = 0.01
		}
		masked[b] = E < maskThresh*bandwidthMask
		bandwidthMask = maxf(0.05*bandwidthMask, E)
	}

	if aboveMaxPitch > belowMaxPitch {
		_ = belowMaxPitch / aboveMaxPitch
	}

	s.ECount = (s.ECount + 1) % NbFrames
	return frameNoisiness + frameStationarity + maxFrameTonality + relativeE + frameLoudness + slope + float32(bandwidth)
}

func BenchmarkAnalysisBandEnergyLegacy(b *testing.B) {
	in := newAnalysisBandBenchInput()
	s := newAnalysisBandBenchState()
	logE := make([]float32, NbTBands)
	bandLog2 := make([]float32, NbTBands+1)
	masked := make([]bool, NbTBands+1)

	b.ReportAllocs()
	b.ResetTimer()

	acc := float32(0)
	for i := 0; i < b.N; i++ {
		acc += analysisBandEnergyLegacy(s, &in, logE, bandLog2, masked)
	}
	analysisBandBenchSink = acc
}

func BenchmarkAnalysisBandEnergyCurrent(b *testing.B) {
	in := newAnalysisBandBenchInput()
	s := newAnalysisBandBenchState()
	logE := make([]float32, NbTBands)
	bandLog2 := make([]float32, NbTBands+1)
	masked := make([]bool, NbTBands+1)

	b.ReportAllocs()
	b.ResetTimer()

	acc := float32(0)
	for i := 0; i < b.N; i++ {
		acc += analysisBandEnergyCurrent(s, &in, logE, bandLog2, masked)
	}
	analysisBandBenchSink = acc
}

func analysisBinsEdgeValue(rng *rand.Rand) float32 {
	switch rng.Intn(10) {
	case 0:
		return 0
	case 1:
		return float32(math.Copysign(0, -1))
	case 2:
		return float32(rng.NormFloat64()) * 1e-10
	case 3:
		return float32(rng.Intn(5) - 2)
	case 4:
		return float32(rng.NormFloat64()) * 3e4
	default:
		return float32(rng.NormFloat64()) * 100
	}
}

// TestAnalysisBinsMatchesScalar requires the per-bin loop the build selects
// (the amd64 SIMD kernel for most bins) to reproduce the scalar loop bit for
// bit in every output and in the phase history.
func TestAnalysisBinsMatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5eed7a5))
	for trial := range 400 {
		var out [960]float32
		for i := range 480 {
			if trial%3 == 0 {
				out[2*i] = analysisBinsEdgeValue(rng)
				out[2*i+1] = analysisBinsEdgeValue(rng)
			} else {
				out[2*i] = float32(rng.NormFloat64()) * 1000
				out[2*i+1] = float32(rng.NormFloat64()) * 1000
			}
		}
		if trial%7 == 0 {
			// Mirror pairs with equal and opposite magnitudes.
			for i := 1; i < 240; i += 5 {
				out[2*(480-i)] = out[2*i]
				out[2*(480-i)+1] = -out[2*i+1]
			}
		}
		var want, got TonalityAnalysisState
		for i := range want.Angle {
			want.Angle[i] = float32(rng.Float64() - 0.5)
			want.DAngle[i] = float32(rng.Float64() - 0.5)
			want.D2Angle[i] = float32(rng.Float64())
		}
		got.Angle, got.DAngle, got.D2Angle = want.Angle, want.DAngle, want.D2Angle
		var wantT, wantT2, wantN, gotT, gotT2, gotN [240]float32
		want.analysisBinsScalar(&out, 1, wantT[:], wantT2[:], wantN[:])
		got.analysisBins(&out, gotT[:], gotT2[:], gotN[:])
		for i := 1; i < 240; i++ {
			for _, c := range []struct {
				name string
				g, w float32
			}{
				{"tonality", gotT[i], wantT[i]},
				{"tonality2", gotT2[i], wantT2[i]},
				{"noisiness", gotN[i], wantN[i]},
				{"angle", got.Angle[i], want.Angle[i]},
				{"dAngle", got.DAngle[i], want.DAngle[i]},
				{"d2Angle", got.D2Angle[i], want.D2Angle[i]},
			} {
				if math.Float32bits(c.g) != math.Float32bits(c.w) {
					t.Fatalf("trial %d bin %d %s: got %v (%08x), want %v (%08x)", trial, i, c.name,
						c.g, math.Float32bits(c.g), c.w, math.Float32bits(c.w))
				}
			}
		}
	}
}

func TestAnalysisBinsZeroAllocs(t *testing.T) {
	var s TonalityAnalysisState
	var out [960]float32
	for i := range 480 {
		out[2*i] = float32(i%17) - 8
		out[2*i+1] = float32(i%11) - 5
	}
	var tn, tn2, noise [240]float32
	run := func() { s.analysisBins(&out, tn[:], tn2[:], noise[:]) }
	run()
	if got := testing.AllocsPerRun(100, run); got != 0 {
		t.Fatalf("analysisBins allocated: %g allocs/run", got)
	}
}

// fastAtan2fRef is analysis.c fast_atan2f written with its branches.
func fastAtan2fRef(y, x float32) float32 {
	x2 := round32(x * x)
	y2 := round32(y * y)
	if x2+y2 < 1e-18 {
		return 0
	}
	cE := analysisAtanCE
	s1 := cE
	if y < 0 {
		s1 = -cE
	}
	if x2 < y2 {
		den := (y2 + analysisAtanCB*x2) * (y2 + analysisAtanCC*x2)
		return -x*y*(y2+analysisAtanCA*x2)/den + s1
	}
	den := (x2 + analysisAtanCB*y2) * (x2 + analysisAtanCC*y2)
	s2 := cE
	if x*y < 0 {
		s2 = -cE
	}
	return x*y*(x2+analysisAtanCA*y2)/den + s1 - s2
}

func TestAnalysisAtan2MatchesBranchyForm(t *testing.T) {
	rng := rand.New(rand.NewSource(0xa7a2))
	for i := range 200000 {
		var y, x float32
		if i%2 == 0 {
			y, x = analysisBinsEdgeValue(rng), analysisBinsEdgeValue(rng)
		} else {
			y, x = float32(rng.NormFloat64()), float32(rng.NormFloat64())
		}
		got, want := analysisAtan2(y, x), fastAtan2fRef(y, x)
		if math.Float32bits(got) != math.Float32bits(want) {
			t.Fatalf("analysisAtan2(%v, %v) = %v (%08x), want %v (%08x)", y, x, got, math.Float32bits(got), want, math.Float32bits(want))
		}
	}
}

var libopusAnalysisHelper libopustest.HelperCache
var libopusFixedAnalysisHelper libopustest.HelperCache

func buildLibopusAnalysisHelper() (string, error) {
	config := libopustest.CHelperConfig{
		Label:       "tonality analysis",
		OutputBase:  "gopus_libopus_analysis_info",
		SourceFile:  "libopus_analysis_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"include", "src", "celt", "silk", "silk/float"},
		DeadStrip:   true,
	}
	switch {
	case fixedPointBuild && extsupport.QEXT:
		config.FixedQEXTRef = true
		config.Libs = []string{libopustest.FixedQEXTRefPath(".libs", "libopus.a"), "-lm"}
	case fixedPointBuild:
		config.FixedRef = true
		config.Libs = []string{libopustest.FixedRefPath(".libs", "libopus.a"), "-lm"}
	case extsupport.QEXT:
		config.QEXTRef = true
		config.Libs = []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"}
	default:
		config.Libs = []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"}
	}
	return libopustest.BuildCHelper(config)
}

func buildLibopusFixedAnalysisHelper() (string, error) {
	config := libopustest.CHelperConfig{
		Label:       "fixed-point tonality analysis",
		OutputBase:  "gopus_libopus_fixed_analysis_info",
		SourceFile:  "libopus_analysis_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"include", "src", "celt", "silk", "silk/float"},
		DeadStrip:   true,
	}
	switch {
	case extsupport.QEXT:
		config.FixedQEXTRef = true
		config.Libs = []string{libopustest.FixedQEXTRefPath(".libs", "libopus.a"), "-lm"}
	default:
		config.FixedRef = true
		config.Libs = []string{libopustest.FixedRefPath(".libs", "libopus.a"), "-lm"}
	}
	return libopustest.BuildCHelper(config)
}

// analysisOracleInfo is one AnalysisInfo as the helper reports it.
type analysisOracleInfo struct {
	valid                         uint32
	tonality, tonalitySlope       uint32
	noisiness, activity           uint32
	musicProb, musicMin, musicMax uint32
	bandwidth                     uint32
	activityProb, maxPitchRatio   uint32
	leakBoost                     [19]uint8
}

// analysisOracleFrame is the helper's per-frame record.
type analysisOracleFrame struct {
	ret, latest  analysisOracleInfo
	scalars      [12]uint32
	hashes       [12]uint32
	downmixState [3]int32
	inmem        []int32
}

var analysisOracleScalarNames = [12]string{
	"Etracker", "lowECount", "hp_ener_accum", "prev_tonality", "E_count", "count",
	"analysis_offset", "write_pos", "read_pos", "read_subframe", "mem_fill", "prev_bandwidth",
}

var analysisOracleHashNames = [12]string{
	"angle", "d_angle", "d2_angle", "E", "logE", "lowE",
	"highE", "meanE", "mem", "cmean", "std", "rnn_state",
}

func runLibopusAnalysisOracle(t *testing.T, fs, channels, frameSize, lsbDepth int, pcm []float32) []analysisOracleFrame {
	return runLibopusAnalysisOracleConfigured(t, fs, channels, frameSize, lsbDepth, 0, -2, 0, pcm, nil)
}

func runLibopusAnalysisOracleShort(t *testing.T, fs, channels, frameSize, lsbDepth, c1, c2 int, pcm []int16) []analysisOracleFrame {
	return runLibopusAnalysisOracleConfigured(t, fs, channels, frameSize, lsbDepth, c1, c2, 1, nil, pcm)
}

func runLibopusAnalysisOracleConfigured(t *testing.T, fs, channels, frameSize, lsbDepth, c1, c2, downmix int, pcm32 []float32, pcm16 []int16) []analysisOracleFrame {
	return runLibopusAnalysisOracleConfiguredWithHelper(t, fs, channels, frameSize, lsbDepth, c1, c2, downmix, pcm32, pcm16,
		&libopusAnalysisHelper, buildLibopusAnalysisHelper)
}

func runLibopusFixedAnalysisOracle(t *testing.T, fs, channels, frameSize, lsbDepth int, pcm []float32) []analysisOracleFrame {
	t.Helper()
	return runLibopusAnalysisOracleConfiguredWithHelperVersion(t, fs, channels, frameSize, lsbDepth, 0, -2, 0, pcm, nil,
		&libopusFixedAnalysisHelper, buildLibopusFixedAnalysisHelper, 2)
}

func runLibopusAnalysisOracleConfiguredWithHelper(t *testing.T, fs, channels, frameSize, lsbDepth, c1, c2, downmix int, pcm32 []float32, pcm16 []int16,
	helper *libopustest.HelperCache, build func() (string, error)) []analysisOracleFrame {
	return runLibopusAnalysisOracleConfiguredWithHelperVersion(t, fs, channels, frameSize, lsbDepth, c1, c2, downmix, pcm32, pcm16,
		helper, build, 1)
}

func runLibopusAnalysisOracleConfiguredWithHelperVersion(t *testing.T, fs, channels, frameSize, lsbDepth, c1, c2, downmix int, pcm32 []float32, pcm16 []int16,
	helper *libopustest.HelperCache, build func() (string, error), version uint32) []analysisOracleFrame {
	t.Helper()
	bin, err := helper.Path(build)
	if err != nil {
		libopustest.HelperUnavailable(t, "tonality analysis", err)
	}
	numSamples := len(pcm32)
	if downmix == 1 {
		numSamples = len(pcm16)
	}
	numFrames := numSamples / (frameSize * channels)
	payload := libopustest.NewOraclePayloadVersion("GANI", version,
		uint32(fs), uint32(channels), uint32(frameSize), uint32(numFrames), uint32(lsbDepth),
		uint32(c1), uint32(int32(c2)), uint32(downmix), uint32(numFrames*frameSize*channels))
	if downmix == 1 {
		for _, v := range pcm16[:numFrames*frameSize*channels] {
			payload.I16(v)
		}
		if numFrames*frameSize*channels&1 != 0 {
			payload.I16(0)
		}
	} else {
		for _, v := range pcm32[:numFrames*frameSize*channels] {
			payload.U32(math.Float32bits(v))
		}
	}
	out, err := libopustest.RunHelper(bin, payload.Bytes())
	if err != nil {
		t.Fatalf("run tonality analysis helper: %v", err)
	}
	if len(out) < 12 || string(out[:4]) != "GANO" || binary.LittleEndian.Uint32(out[4:]) != version {
		t.Fatalf("malformed helper output header")
	}
	if n := int(binary.LittleEndian.Uint32(out[8:])); n != numFrames {
		t.Fatalf("helper returned %d frames, want %d", n, numFrames)
	}
	const infoBytes = 12*4 + 20
	const baseRecordBytes = 2*infoBytes + 12*4 + 12*4
	recordBytes := baseRecordBytes
	if version == 2 {
		recordBytes += (3 + AnalysisBufSize) * 4
	}
	body := out[12:]
	if len(body) != numFrames*recordBytes {
		t.Fatalf("helper output has %d bytes, want %d", len(body), numFrames*recordBytes)
	}
	readInfo := func(b []byte) analysisOracleInfo {
		u := func(i int) uint32 { return binary.LittleEndian.Uint32(b[4*i:]) }
		in := analysisOracleInfo{
			valid: u(0), tonality: u(1), tonalitySlope: u(2), noisiness: u(3), activity: u(4),
			musicProb: u(5), musicMin: u(6), musicMax: u(7), bandwidth: u(8),
			activityProb: u(9), maxPitchRatio: u(10),
		}
		copy(in.leakBoost[:], b[48:48+19])
		return in
	}
	frames := make([]analysisOracleFrame, numFrames)
	for f := range frames {
		rec := body[f*recordBytes:]
		frames[f].ret = readInfo(rec)
		frames[f].latest = readInfo(rec[infoBytes:])
		for i := range 12 {
			frames[f].scalars[i] = binary.LittleEndian.Uint32(rec[2*infoBytes+4*i:])
			frames[f].hashes[i] = binary.LittleEndian.Uint32(rec[2*infoBytes+48+4*i:])
		}
		if version == 2 {
			stage := rec[baseRecordBytes:]
			for i := range 3 {
				frames[f].downmixState[i] = int32(binary.LittleEndian.Uint32(stage[4*i:]))
			}
			frames[f].inmem = make([]int32, AnalysisBufSize)
			for i := range frames[f].inmem {
				frames[f].inmem[i] = int32(binary.LittleEndian.Uint32(stage[4*(3+i):]))
			}
		}
	}
	return frames
}

func TestAnalysisShortProjectionInputMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize, frameCount = 960, 24
	for _, channels := range []int{9, 16} {
		t.Run(fmt.Sprintf("ch%d", channels), func(t *testing.T) {
			pcm := make([]int16, channels*frameSize*frameCount)
			for sample := range frameSize * frameCount {
				tt := float64(sample) / 48000
				amp := 0.25 + 0.1*math.Sin(2*math.Pi*1.5*tt)
				for ch := range channels {
					v := float32(amp * math.Sin(2*math.Pi*110*float64(ch+1)*tt))
					pcm[sample*channels+ch] = int16(math.Round(float64(v) * 32768))
				}
			}
			want := runLibopusAnalysisOracleShort(t, 48000, channels, frameSize, 16, 0, 1, pcm)
			an := NewTonalityAnalysisState(48000)
			an.SetLSBDepth(16)
			frame := make([]float32, 2*frameSize)
			for f := range frameCount {
				for sample := range frameSize {
					frame[2*sample] = float32(pcm[(f*frameSize+sample)*channels]) / 32768
					frame[2*sample+1] = float32(pcm[(f*frameSize+sample)*channels+1]) / 32768
				}
				got := analysisInfoToOracle(an.RunAnalysis(frame, frameSize, 2))
				if d := diffAnalysisInfo(got, want[f].ret); d != "" {
					t.Errorf("frame %d returned info: %s; state:%s", f, d, analysisStateDiff(an, want[f]))
					break
				}
				if d := analysisStateDiff(an, want[f]); d != "" {
					t.Errorf("frame %d state: %s", f, d)
					break
				}
			}
		})
	}
}

func analysisInfoToOracle(in AnalysisInfo) analysisOracleInfo {
	out := analysisOracleInfo{
		tonality: math.Float32bits(in.Tonality), tonalitySlope: math.Float32bits(in.TonalitySlope),
		noisiness: math.Float32bits(in.NoisySpeech), activity: math.Float32bits(in.Activity),
		musicProb: math.Float32bits(in.MusicProb), musicMin: math.Float32bits(in.MusicProbMin),
		musicMax: math.Float32bits(in.MusicProbMax), bandwidth: uint32(in.BandwidthIndex),
		activityProb: math.Float32bits(in.VADProb), maxPitchRatio: math.Float32bits(in.MaxPitchRatio),
		leakBoost: in.LeakBoost,
	}
	if in.Valid {
		out.valid = 1
	}
	return out
}

func hashAnalysisFloats(v []float32) uint32 {
	h := uint32(2166136261)
	for _, x := range v {
		h = (h ^ math.Float32bits(x)) * 16777619
	}
	return h
}

func analysisStateScalars(s *TonalityAnalysisState) [12]uint32 {
	return [12]uint32{
		math.Float32bits(s.ETracker), math.Float32bits(s.LowECount), math.Float32bits(s.HPEnerAccum),
		math.Float32bits(s.PrevTonality), uint32(s.ECount), uint32(s.Count), uint32(s.AnalysisOffset),
		uint32(s.WritePos), uint32(s.ReadPos), uint32(s.ReadSubframe), uint32(s.MemFill), uint32(s.PrevBandwidth),
	}
}

func analysisStateHashes(s *TonalityAnalysisState) [12]uint32 {
	flat := func(rows [][NbTBands]float32) []float32 {
		out := make([]float32, 0, len(rows)*NbTBands)
		for i := range rows {
			out = append(out, rows[i][:]...)
		}
		return out
	}
	return [12]uint32{
		hashAnalysisFloats(s.Angle[:]), hashAnalysisFloats(s.DAngle[:]), hashAnalysisFloats(s.D2Angle[:]),
		hashAnalysisFloats(flat(s.E[:])), hashAnalysisFloats(flat(s.LogE[:])), hashAnalysisFloats(s.LowE[:]),
		hashAnalysisFloats(s.HighE[:]), hashAnalysisFloats(s.MeanE[:]), hashAnalysisFloats(s.Mem[:]),
		hashAnalysisFloats(s.CMean[:]), hashAnalysisFloats(s.Std[:]), hashAnalysisFloats(s.RNNState[:]),
	}
}

func diffAnalysisInfo(got, want analysisOracleInfo) string {
	switch {
	case got.valid != want.valid:
		return fmt.Sprintf("valid %d want %d", got.valid, want.valid)
	case got.tonality != want.tonality:
		return fmt.Sprintf("tonality %08x want %08x", got.tonality, want.tonality)
	case got.tonalitySlope != want.tonalitySlope:
		return fmt.Sprintf("tonality_slope %08x want %08x", got.tonalitySlope, want.tonalitySlope)
	case got.noisiness != want.noisiness:
		return fmt.Sprintf("noisiness %08x want %08x", got.noisiness, want.noisiness)
	case got.activity != want.activity:
		return fmt.Sprintf("activity %08x want %08x", got.activity, want.activity)
	case got.musicProb != want.musicProb:
		return fmt.Sprintf("music_prob %08x want %08x", got.musicProb, want.musicProb)
	case got.musicMin != want.musicMin:
		return fmt.Sprintf("music_prob_min %08x want %08x", got.musicMin, want.musicMin)
	case got.musicMax != want.musicMax:
		return fmt.Sprintf("music_prob_max %08x want %08x", got.musicMax, want.musicMax)
	case got.bandwidth != want.bandwidth:
		return fmt.Sprintf("bandwidth %d want %d", got.bandwidth, want.bandwidth)
	case got.activityProb != want.activityProb:
		return fmt.Sprintf("activity_probability %08x want %08x", got.activityProb, want.activityProb)
	case got.maxPitchRatio != want.maxPitchRatio:
		return fmt.Sprintf("max_pitch_ratio %08x want %08x", got.maxPitchRatio, want.maxPitchRatio)
	case got.leakBoost != want.leakBoost:
		return fmt.Sprintf("leak_boost %v want %v", got.leakBoost, want.leakBoost)
	}
	return ""
}

// requireAnalysisMatchesLibopus runs gopus's tonality analysis and libopus
// run_analysis() over the same PCM, frame by frame as opus_encode_native calls
// it, and requires every AnalysisInfo field and the analyzer state to match
// bit for bit.
func requireAnalysisMatchesLibopus(t *testing.T, fs, channels, frameSize int, pcm []float32) {
	t.Helper()
	const lsbDepth = 24
	want := runLibopusAnalysisOracle(t, fs, channels, frameSize, lsbDepth, pcm)
	an := NewTonalityAnalysisState(fs)
	an.SetLSBDepth(lsbDepth)
	for f := range want {
		frame := pcm[f*frameSize*channels : (f+1)*frameSize*channels]
		got := analysisInfoToOracle(an.RunAnalysis(frame, frameSize, channels))
		if d := diffAnalysisInfo(got, want[f].ret); d != "" {
			latestIdx := (int(an.WritePos) - 1 + DetectSize) % DetectSize
			latest := diffAnalysisInfo(analysisInfoToOracle(an.Info[latestIdx]), want[f].latest)
			t.Fatalf("frame %d: returned info differs: %s (latest chunk info: %q)%s", f, d, latest, analysisStateDiff(an, want[f]))
		}
		if s := analysisStateDiff(an, want[f]); s != "" {
			t.Fatalf("frame %d: analyzer state differs:%s", f, s)
		}
	}
}

// TestAnalysisMatchesLibopusLive sweeps the corpus signal classes over every
// analysis sample rate, both channel counts and every frame duration.
func TestAnalysisMatchesLibopusLive(t *testing.T) {
	libopustest.RequireOracle(t)
	classes := []string{testsignal.CorpusMusicV1, testsignal.CorpusCleanSpeechV1, testsignal.CorpusMixedV1,
		testsignal.CorpusSilenceBurstsV1, testsignal.CorpusCastanetTransientV1, testsignal.CorpusPureToneV1}
	for _, fs := range []int{48000, 24000, 16000} {
		for _, channels := range []int{1, 2} {
			for _, ms := range []int{5, 10, 20, 40, 60} {
				for _, class := range classes {
					frameSize := fs * ms / 1000
					name := fmt.Sprintf("%dk/ch%d/%dms/%s", fs/1000, channels, ms, class)
					t.Run(name, func(t *testing.T) {
						numFrames := max(1200/ms, 12)
						pcm, err := testsignal.GenerateCorpusSignal(class, fs, numFrames*frameSize*channels, channels)
						if err != nil {
							t.Fatalf("GenerateCorpusSignal: %v", err)
						}
						requireAnalysisMatchesLibopus(t, fs, channels, frameSize, pcm)
					})
				}
			}
		}
	}
}

// TestAnalysisMatchesLibopusLiveEncoderVariants runs the encoder signal
// variants at 48 kHz, quantized to 24 bits as opus_demo reads float input,
// over one second of audio per frame duration.
func TestAnalysisMatchesLibopusLiveEncoderVariants(t *testing.T) {
	libopustest.RequireOracle(t)
	const fs = 48000
	layouts := []struct{ ms, channels int }{{10, 1}, {20, 1}, {20, 2}, {40, 1}, {60, 1}}
	for _, variant := range testsignal.EncoderSignalVariants() {
		for _, l := range layouts {
			frameSize := fs * l.ms / 1000
			t.Run(fmt.Sprintf("%s/ch%d/%dms", variant, l.channels, l.ms), func(t *testing.T) {
				numFrames := 1000 / l.ms
				pcm, err := testsignal.GenerateEncoderSignalVariant(variant, fs, numFrames*frameSize*l.channels, l.channels)
				if err != nil {
					t.Fatalf("GenerateEncoderSignalVariant: %v", err)
				}
				clampToOpusDemoF32InPlace(pcm)
				requireAnalysisMatchesLibopus(t, fs, l.channels, frameSize, pcm)
			})
		}
	}
}

func analysisStateDiff(an *TonalityAnalysisState, want analysisOracleFrame) string {
	out := ""
	scalars := analysisStateScalars(an)
	for i, v := range scalars {
		if v != want.scalars[i] {
			out += fmt.Sprintf(" %s=%08x want %08x;", analysisOracleScalarNames[i], v, want.scalars[i])
		}
	}
	hashes := analysisStateHashes(an)
	for i, v := range hashes {
		if v != want.hashes[i] {
			out += fmt.Sprintf(" %s hash differs;", analysisOracleHashNames[i])
		}
	}
	return out
}

func gemmAccumF32GenericReference(out []float32, weights []float32, rows, cols, colStride int, x []float32) {
	if rows <= 0 || cols <= 0 {
		return
	}
	for j := range cols {
		xj := x[j]
		w := weights[j*colStride : j*colStride+rows]
		for i := range rows {
			out[i] += gemmAccumF32GenericReferenceProduct(w[i], xj)
		}
	}
}

func TestGemmAccumF32MatchesGenericReference(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	for _, tc := range []struct {
		rows      int
		cols      int
		colStride int
	}{
		{rows: 2, cols: 24, colStride: 2},
		{rows: 24, cols: 32, colStride: 72},
		{rows: 32, cols: 25, colStride: 32},
		{rows: 7, cols: 9, colStride: 11},
	} {
		outCurrent := make([]float32, tc.rows)
		outWant := make([]float32, tc.rows)
		weights := make([]float32, tc.cols*tc.colStride)
		input := make([]float32, tc.cols)
		for i := range outCurrent {
			v := rng.Float32()*2 - 1
			outCurrent[i] = v
			outWant[i] = v
		}
		for i := range weights {
			weights[i] = rng.Float32()*2 - 1
		}
		for i := range input {
			input[i] = rng.Float32()*2 - 1
		}

		gemmAccumF32(outCurrent, weights, tc.rows, tc.cols, tc.colStride, input)
		gemmAccumF32GenericReference(outWant, weights, tc.rows, tc.cols, tc.colStride, input)

		if !reflect.DeepEqual(outCurrent, outWant) {
			t.Fatalf("rows=%d cols=%d stride=%d mismatch", tc.rows, tc.cols, tc.colStride)
		}
	}
}

func TestGemmAccumF32Rows24PairMatchesSeparatePasses(t *testing.T) {
	rng := rand.New(rand.NewSource(29))
	rows := 24
	cols := layer1.NbNeurons
	stride := 3 * rows
	out0 := make([]float32, rows)
	out1 := make([]float32, rows)
	want0 := make([]float32, rows)
	want1 := make([]float32, rows)
	input := make([]float32, cols)
	for i := range out0 {
		v0 := rng.Float32()*2 - 1
		v1 := rng.Float32()*2 - 1
		out0[i], want0[i] = v0, v0
		out1[i], want1[i] = v1, v1
	}
	for i := range input {
		input[i] = rng.Float32()*2 - 1
	}

	gemmAccumF32Rows24Pair(out0, out1, layer1.recurrentWeightsF32, cols, stride, input)
	gemmAccumF32(want0, layer1.recurrentWeightsF32, rows, cols, stride, input)
	gemmAccumF32(want1, layer1.recurrentWeightsF32[rows:], rows, cols, stride, input)

	if !reflect.DeepEqual(out0, want0) || !reflect.DeepEqual(out1, want1) {
		t.Fatal("rows24 pair mismatch")
	}
}

func TestGemmAccumF32Rows24TripleMatchesSeparatePasses(t *testing.T) {
	rng := rand.New(rand.NewSource(31))
	rows := 24
	cols := layer1.NbInputs
	stride := 3 * rows
	out0 := make([]float32, rows)
	out1 := make([]float32, rows)
	out2 := make([]float32, rows)
	want0 := make([]float32, rows)
	want1 := make([]float32, rows)
	want2 := make([]float32, rows)
	input := make([]float32, cols)
	for i := range out0 {
		v0 := rng.Float32()*2 - 1
		v1 := rng.Float32()*2 - 1
		v2 := rng.Float32()*2 - 1
		out0[i], want0[i] = v0, v0
		out1[i], want1[i] = v1, v1
		out2[i], want2[i] = v2, v2
	}
	for i := range input {
		input[i] = rng.Float32()*2 - 1
	}

	gemmAccumF32Rows24Triple(out0, out1, out2, layer1.inputWeightsF32, cols, stride, input)
	gemmAccumF32(want0, layer1.inputWeightsF32, rows, cols, stride, input)
	gemmAccumF32(want1, layer1.inputWeightsF32[rows:], rows, cols, stride, input)
	gemmAccumF32(want2, layer1.inputWeightsF32[2*rows:], rows, cols, stride, input)

	if !reflect.DeepEqual(out0, want0) || !reflect.DeepEqual(out1, want1) || !reflect.DeepEqual(out2, want2) {
		t.Fatal("rows24 triple mismatch")
	}
}

func TestUpdateFrameActivityReusesFreshAnalysis(t *testing.T) {
	const frameSize = 1920

	enc := NewEncoder(48000, 1)
	enc.SetComplexity(10)
	pcmRes := make([]opusRes, frameSize)
	for i := range pcmRes {
		s := 0.25 * math.Sin(2*math.Pi*220*float64(i)/48000.0)
		pcmRes[i] = opusRes(s)
	}

	_ = enc.autoSignalFromPCM(pcmRes, frameSize)
	if !enc.lastAnalysisValid || !enc.lastAnalysisFresh {
		t.Fatalf("expected fresh analysis after autoSignalFromPCM, valid=%v fresh=%v", enc.lastAnalysisValid, enc.lastAnalysisFresh)
	}

	countBefore := enc.analyzer.Count
	enc.updateFrameActivity(pcmRes, false, ModeSILK)
	if enc.analyzer.Count != countBefore {
		t.Fatalf("updateFrameActivity consumed fresh analysis but still advanced analyzer count: got %d want %d", enc.analyzer.Count, countBefore)
	}
	if enc.lastAnalysisFresh {
		t.Fatal("expected fresh analysis flag to be consumed")
	}
	if !enc.lastOpusVADValid {
		t.Fatal("expected valid Opus VAD after consuming fresh analysis")
	}

	// libopus opus_encoder.c never re-runs the tonality analysis in the VAD
	// path: opus_encode_frame_native() derives activity from the analysis_info
	// produced once per frame by run_analysis(). A second updateFrameActivity call
	// without a fresh analysis must therefore reuse the last valid snapshot and
	// must NOT advance the analyzer (re-running RunAnalysis would mutate
	// write_pos/read cursor and desynchronise the next frame's curr_lookahead).
	enc.updateFrameActivity(pcmRes, false, ModeSILK)
	if enc.analyzer.Count != countBefore {
		t.Fatalf("second updateFrameActivity call must not advance analyzer: countBefore=%d countAfter=%d", countBefore, enc.analyzer.Count)
	}
	if !enc.lastOpusVADValid {
		t.Fatal("expected reused valid Opus VAD on second call")
	}
}

func TestRestrictedSilkApplicationSkipsAnalysis(t *testing.T) {
	const frameSize = 960

	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeSILK)
	enc.SetRestrictedSilkApplication(true)

	pcm := make([]float32, frameSize)
	pcmRes := make([]opusRes, frameSize)
	for i := range pcm {
		s := 0.25 * math.Sin(2*math.Pi*220*float64(i)/48000.0)
		pcm[i] = float32(s)
		pcmRes[i] = opusRes(s)
	}

	enc.refreshFrameAnalysisF32(pcm, frameSize)
	if enc.lastAnalysisValid || enc.lastAnalysisFresh {
		t.Fatalf("restricted SILK analysis valid=%v fresh=%v, want disabled", enc.lastAnalysisValid, enc.lastAnalysisFresh)
	}
	if enc.analyzer.Initialized {
		t.Fatal("restricted SILK should leave analyzer reset")
	}

	if got := enc.autoSignalFromPCM(pcmRes, frameSize*2); got != types.SignalAuto {
		t.Fatalf("restricted SILK autoSignalFromPCM=%v, want auto", got)
	}
	if enc.analyzer.Initialized {
		t.Fatal("restricted SILK auto signal fallback should not initialize analyzer")
	}
}

func TestLongHybridMultiframeReusesAnalysisCadence(t *testing.T) {
	const frameSize = 1920

	enc := NewEncoder(48000, 1)
	enc.SetComplexity(10)
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(types.BandwidthSuperwideband)
	enc.SetBitrate(48000)

	pcm := make([]float64, frameSize)
	for i := range pcm {
		pcm[i] = 0.25 * math.Sin(2*math.Pi*220*float64(i)/48000.0)
	}

	packet, err := encodeTest(enc, pcm, frameSize)
	if err != nil {
		t.Fatalf("encode long hybrid frame: %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("expected encoded packet")
	}
	if enc.analyzer == nil {
		t.Fatal("expected analyzer state")
	}

	if enc.analyzer.Count != 2 {
		t.Fatalf("unexpected analyzer count: got %d want 2", enc.analyzer.Count)
	}
	if enc.analyzer.WritePos != 2 {
		t.Fatalf("unexpected analyzer write pos: got %d want 2", enc.analyzer.WritePos)
	}
	if enc.analyzer.ReadPos != 2 {
		t.Fatalf("unexpected analyzer read pos: got %d want 2", enc.analyzer.ReadPos)
	}
	if enc.analyzer.ReadSubframe != 0 {
		t.Fatalf("unexpected analyzer read subframe: got %d want 0", enc.analyzer.ReadSubframe)
	}
}

func TestSpeechProbabilityMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	classes := []string{testsignal.CorpusCleanSpeechV1, testsignal.CorpusMixedV1, testsignal.CorpusSpeechInNoiseV1,
		testsignal.CorpusWhiteNoiseV1, testsignal.CorpusMusicV1, testsignal.CorpusSilenceBurstsV1}
	for _, fs := range []int{16000, 24000, 48000} {
		for _, ms := range []int{10, 20} {
			for _, class := range classes {
				t.Run(fmt.Sprintf("%dk/%dms/%s", fs/1000, ms, class), func(t *testing.T) {
					frameSize := fs * ms / 1000
					numFrames := 2000 / ms
					pcm, err := testsignal.GenerateCorpusSignal(class, fs, numFrames*frameSize, 1)
					if err != nil {
						t.Fatalf("GenerateCorpusSignal: %v", err)
					}
					want := runLibopusAnalysisOracle(t, fs, 1, frameSize, 24, pcm)
					an := NewTonalityAnalysisState(fs)
					silent := 0
					for f := range want {
						got := an.SpeechProbability(pcm[f*frameSize : (f+1)*frameSize])
						expect := float32(0)
						if an.silentWindow {
							silent++
						} else if want[f].ret.valid != 0 {
							expect = math.Float32frombits(want[f].ret.activityProb)
						}
						if math.Float32bits(got) != math.Float32bits(expect) {
							t.Fatalf("frame %d: probability %v want %v", f, got, expect)
						}
						if s := analysisStateDiff(an, want[f]); s != "" {
							t.Fatalf("frame %d: analyzer state differs:%s", f, s)
						}
					}
					if (class == testsignal.CorpusSilenceBurstsV1) != (silent > 0) {
						t.Fatalf("%d frames ended on a silent window for %s", silent, class)
					}
				})
			}
		}
	}
}

// TestSpeechProbabilityInt16MatchesLibopus feeds 16-bit PCM scaled to the float
// range, as the public detector does, against libopus's 16-bit input path.
func TestSpeechProbabilityInt16MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, fs := range []int{16000, 48000} {
		t.Run(fmt.Sprintf("%dk", fs/1000), func(t *testing.T) {
			const frameMS = 20
			frameSize := fs * frameMS / 1000
			numFrames := 2000 / frameMS
			sig, err := testsignal.GenerateCorpusSignal(testsignal.CorpusCleanSpeechV1, fs, numFrames*frameSize, 1)
			if err != nil {
				t.Fatalf("GenerateCorpusSignal: %v", err)
			}
			pcm := make([]int16, len(sig))
			scaled := make([]float32, len(sig))
			for i, v := range sig {
				pcm[i] = int16(math.Max(-32768, math.Min(32767, math.Round(float64(v)*32768))))
				scaled[i] = float32(pcm[i]) * (1.0 / 32768.0)
			}
			want := runLibopusAnalysisOracleShort(t, fs, 1, frameSize, 24, 0, -2, pcm)
			an := NewTonalityAnalysisState(fs)
			for f := range want {
				got := an.SpeechProbability(scaled[f*frameSize : (f+1)*frameSize])
				expect := float32(0)
				if want[f].ret.valid != 0 {
					expect = math.Float32frombits(want[f].ret.activityProb)
				}
				if math.Float32bits(got) != math.Float32bits(expect) {
					t.Fatalf("frame %d: probability %v want %v", f, got, expect)
				}
			}
		})
	}
}

func TestSpeechProbabilityDigitalSilence(t *testing.T) {
	const fs, frameSize = 16000, 320
	speech, err := testsignal.GenerateCorpusSignal(testsignal.CorpusCleanSpeechV1, fs, 40*frameSize, 1)
	if err != nil {
		t.Fatalf("GenerateCorpusSignal: %v", err)
	}
	an := NewTonalityAnalysisState(fs)
	var last float32
	for f := range 40 {
		last = an.SpeechProbability(speech[f*frameSize : (f+1)*frameSize])
	}
	if last == 0 || an.silentWindow {
		t.Fatalf("speech probability %v silentWindow %v after speech", last, an.silentWindow)
	}
	zeros := make([]float32, frameSize)
	var held float32
	for f := range 3 {
		got := an.SpeechProbability(zeros)
		if f == 2 {
			held = an.GetInfo().VADProb
			if !an.silentWindow || got != 0 {
				t.Fatalf("silent window: probability %v silentWindow %v", got, an.silentWindow)
			}
		}
	}
	if held == 0 {
		t.Fatal("the analyzer's own entry does not repeat the last speech estimate through silence")
	}
	if got := an.SpeechProbability(speech[:frameSize]); an.silentWindow || got == 0 {
		t.Fatalf("speech after silence: probability %v silentWindow %v", got, an.silentWindow)
	}
	an.SpeechProbability(zeros)
	an.SpeechProbability(zeros)
	an.Reset()
	if an.silentWindow {
		t.Fatal("Reset keeps the silent-window flag")
	}
}

func TestAnalysisFloat2IntRoundToEven(t *testing.T) {
	tests := []struct {
		in   float32
		want int32
	}{
		{0.5, 0},
		{1.5, 2},
		{2.5, 2},
		{-0.5, 0},
		{-1.5, -2},
		{-2.5, -2},
		{1.4, 1},
		{1.6, 2},
	}
	for _, tc := range tests {
		if got := analysisFloat2Int(tc.in); got != tc.want {
			t.Fatalf("analysisFloat2Int(%f)=%d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestAnalysisFastAtan2fParityShape(t *testing.T) {
	const eps = 0.05
	tests := []struct {
		y    float32
		x    float32
		want float32
	}{
		{1, 1, float32(math.Pi / 4)},
		{-1, 1, float32(-math.Pi / 4)},
		{1, -1, float32(3 * math.Pi / 4)},
		{-1, -1, float32(-3 * math.Pi / 4)},
	}
	for _, tc := range tests {
		got := analysisAtan2(tc.y, tc.x)
		if math.Abs(float64(got-tc.want)) > eps {
			t.Fatalf("analysisAtan2(%f,%f)=%.6f, want %.6f (+/- %.3f)", tc.y, tc.x, got, tc.want, eps)
		}
	}
}

func TestAnalysisSmoke(t *testing.T) {
	s := NewTonalityAnalysisState(48000)

	// Create a simple 1kHz sine wave (highly tonal)
	// We need 960 samples at 48kHz to produce 480 samples at 24kHz for analysis.
	pcm := make([]float32, 1000)
	for i := range pcm {
		pcm[i] = float32(math.Sin(2 * math.Pi * 1000.0 * float64(i) / 48000.0))
	}

	info := s.RunAnalysis(pcm, 1000, 1)

	if !info.Valid {
		// If not valid, it means it didn't have enough data yet.
		// Let's feed another frame to be sure.
		info = s.RunAnalysis(pcm, 1000, 1)
	}

	if !info.Valid {
		t.Fatal("Analysis info should be valid after feeding enough data")
	}

	t.Logf("MusicProb: %f, VADProb: %f", info.MusicProb, info.VADProb)

	if info.MusicProb < 0 || info.MusicProb > 1 {
		t.Errorf("Invalid MusicProb: %f", info.MusicProb)
	}
	if info.MusicProbMin < 0 || info.MusicProbMin > 1 {
		t.Errorf("Invalid MusicProbMin: %f", info.MusicProbMin)
	}
	if info.MusicProbMax < 0 || info.MusicProbMax > 1 {
		t.Errorf("Invalid MusicProbMax: %f", info.MusicProbMax)
	}
	if info.MusicProbMin > info.MusicProb {
		t.Errorf("MusicProbMin > MusicProb (%f > %f)", info.MusicProbMin, info.MusicProb)
	}
	if info.MusicProb > info.MusicProbMax {
		t.Errorf("MusicProb > MusicProbMax (%f > %f)", info.MusicProb, info.MusicProbMax)
	}
	if info.Activity < 0 || info.Activity > 1 {
		t.Errorf("Invalid Activity: %f", info.Activity)
	}
	if math.IsNaN(float64(info.NoisySpeech)) || math.IsInf(float64(info.NoisySpeech), 0) {
		t.Errorf("Invalid NoisySpeech: %f", info.NoisySpeech)
	}
	if math.IsNaN(float64(info.StationarySpeech)) || math.IsInf(float64(info.StationarySpeech), 0) {
		t.Errorf("Invalid StationarySpeech: %f", info.StationarySpeech)
	}
	if info.MaxPitchRatio <= 0 {
		t.Errorf("Invalid MaxPitchRatio: %f", info.MaxPitchRatio)
	}
}

func TestRunAnalysisLongFrameUses20msChunks(t *testing.T) {
	s := NewTonalityAnalysisState(48000)

	const frameSize = 1920
	pcm := make([]float32, frameSize)
	for i := range pcm {
		pcm[i] = float32(math.Sin(2 * math.Pi * 330.0 * float64(i) / 48000.0))
	}

	info := s.RunAnalysis(pcm, frameSize, 1)
	if !info.Valid {
		t.Fatal("expected valid analysis info")
	}
	if s.Count != 2 {
		t.Fatalf("unexpected analysis count: got %d want 2", s.Count)
	}
	if s.WritePos != 2 {
		t.Fatalf("unexpected write pos: got %d want 2", s.WritePos)
	}
	if s.ReadPos != 2 {
		t.Fatalf("unexpected read pos: got %d want 2", s.ReadPos)
	}
	if s.ReadSubframe != 0 {
		t.Fatalf("unexpected read subframe: got %d want 0", s.ReadSubframe)
	}
	if s.AnalysisOffset != 0 {
		t.Fatalf("unexpected analysis offset: got %d want 0", s.AnalysisOffset)
	}
}

func TestRunAnalysisMaxPitchRatioTracksHighBandEnergy(t *testing.T) {
	s := NewTonalityAnalysisState(48000)

	const frameSize = 960
	pcm := make([]float32, frameSize)
	for i := range pcm {
		pcm[i] = 0.7 * float32(math.Sin(2*math.Pi*10000.0*float64(i)/48000.0))
	}

	var info AnalysisInfo
	for range 8 {
		info = s.RunAnalysis(pcm, frameSize, 1)
	}
	if !info.Valid {
		t.Fatal("expected valid analysis info")
	}
	if info.MaxPitchRatio >= 0.98 {
		t.Fatalf("expected high-band dominant frame to reduce max pitch ratio, got %.4f", info.MaxPitchRatio)
	}
}

func TestRunAnalysisLowEnergyCounterIncreasesAfterLoudnessDrop(t *testing.T) {
	s := NewTonalityAnalysisState(48000)

	const frameSize = 960
	loud := make([]float32, frameSize)
	quiet := make([]float32, frameSize)
	for i := range frameSize {
		loud[i] = 0.8 * float32(math.Sin(2*math.Pi*220.0*float64(i)/48000.0))
		quiet[i] = 1e-4 * float32(math.Sin(2*math.Pi*220.0*float64(i)/48000.0))
	}

	for range 12 {
		_ = s.RunAnalysis(loud, frameSize, 1)
	}
	lowEBefore := s.LowECount

	var info AnalysisInfo
	for range 12 {
		info = s.RunAnalysis(quiet, frameSize, 1)
	}
	if !info.Valid {
		t.Fatal("expected valid analysis info")
	}
	if s.LowECount <= lowEBefore {
		t.Fatalf("expected low-energy counter to increase after loudness drop: before=%.6f after=%.6f", lowEBefore, s.LowECount)
	}
}

func TestRunAnalysis16kProducesValidInfo(t *testing.T) {
	s := NewTonalityAnalysisState(16000)

	const frameSize = 320 // 20 ms at 16 kHz
	pcm := make([]float32, frameSize)
	for i := range pcm {
		pcm[i] = float32(math.Sin(2 * math.Pi * 440.0 * float64(i) / 16000.0))
	}

	info := s.RunAnalysis(pcm, frameSize, 1)
	if !info.Valid {
		t.Fatal("expected valid analysis info at 16 kHz")
	}
	if s.Count != 1 {
		t.Fatalf("unexpected analysis count: got %d want 1", s.Count)
	}
	if s.WritePos != 1 {
		t.Fatalf("unexpected write pos: got %d want 1", s.WritePos)
	}
	if s.ReadPos != 1 {
		t.Fatalf("unexpected read pos: got %d want 1", s.ReadPos)
	}
}

func TestRunAnalysis16kLongFrameUses20msChunks(t *testing.T) {
	s := NewTonalityAnalysisState(16000)

	const frameSize = 640 // 40 ms at 16 kHz
	pcm := make([]float32, frameSize)
	for i := range pcm {
		pcm[i] = float32(math.Sin(2 * math.Pi * 330.0 * float64(i) / 16000.0))
	}

	info := s.RunAnalysis(pcm, frameSize, 1)
	if !info.Valid {
		t.Fatal("expected valid analysis info")
	}
	if s.Count != 2 {
		t.Fatalf("unexpected analysis count: got %d want 2", s.Count)
	}
	if s.WritePos != 2 {
		t.Fatalf("unexpected write pos: got %d want 2", s.WritePos)
	}
	if s.ReadPos != 2 {
		t.Fatalf("unexpected read pos: got %d want 2", s.ReadPos)
	}
	if s.ReadSubframe != 0 {
		t.Fatalf("unexpected read subframe: got %d want 0", s.ReadSubframe)
	}
	if s.AnalysisOffset != 0 {
		t.Fatalf("unexpected analysis offset: got %d want 0", s.AnalysisOffset)
	}
}

func TestTonalityAnalysisResetClearsState(t *testing.T) {
	s := NewTonalityAnalysisState(48000)

	const frameSize = 960
	pcm := make([]float32, frameSize)
	for i := range pcm {
		pcm[i] = 0.6 * float32(math.Sin(2*math.Pi*440.0*float64(i)/48000.0))
	}
	for range 3 {
		_ = s.RunAnalysis(pcm, frameSize, 1)
	}
	if s.Count == 0 {
		t.Fatal("expected non-zero analyzer count before reset")
	}

	s.Reset()

	if s.Fs != 48000 {
		t.Fatalf("reset should preserve sample rate: got %d want 48000", s.Fs)
	}
	if s.Count != 0 || s.ECount != 0 || s.MemFill != 0 {
		t.Fatalf("reset should clear counters/memfill: count=%d ecount=%d memfill=%d", s.Count, s.ECount, s.MemFill)
	}
	if s.AnalysisOffset != 0 || s.WritePos != 0 || s.ReadPos != 0 || s.ReadSubframe != 0 {
		t.Fatalf("reset should clear analysis cursors: offset=%d write=%d read=%d subframe=%d", s.AnalysisOffset, s.WritePos, s.ReadPos, s.ReadSubframe)
	}
	if s.HPEnerAccum != 0 || s.PrevTonality != 0 || s.PrevBandwidth != 0 || s.ETracker != 0 || s.LowECount != 0 {
		t.Fatalf("reset should clear scalar state: hp=%.6f tonality=%.6f bw=%d etracker=%.6f lowE=%.6f", s.HPEnerAccum, s.PrevTonality, s.PrevBandwidth, s.ETracker, s.LowECount)
	}
	if s.Angle[10] != 0 || s.DAngle[10] != 0 || s.D2Angle[10] != 0 {
		t.Fatal("reset should clear angle history")
	}
	if s.E[0][0] != 0 || s.LogE[0][0] != 0 || s.LowE[0] != 0 || s.HighE[0] != 0 || s.MeanE[0] != 0 {
		t.Fatal("reset should clear energy history and trackers")
	}
	if s.Mem[0] != 0 || s.CMean[0] != 0 || s.Std[0] != 0 {
		t.Fatal("reset should clear feature history state")
	}
	if s.RNNState[0] != 0 || s.DownmixState[0] != 0 || s.InMem[0] != 0 {
		t.Fatal("reset should clear runtime buffers/state")
	}
	if s.Info[0].Valid {
		t.Fatal("reset should clear queued analysis info")
	}
	if !fixedPointBuild && cap(s.scratchFFTOut) < 960 {
		t.Fatalf("reset should preserve reusable FFT scratch capacity: got %d", cap(s.scratchFFTOut))
	}
}

func TestTonalityAnalysisResetPreservesLSBDepth(t *testing.T) {
	s := NewTonalityAnalysisState(48000)
	s.SetLSBDepth(12)
	_ = s.RunAnalysis(make([]float32, 960), 960, 1)
	s.Reset()
	if s.LSBDepth != 12 {
		t.Fatalf("reset should preserve configured LSB depth: got %d want 12", s.LSBDepth)
	}
}

func TestRunAnalysisNoiseFloorRespectsLSBDepth(t *testing.T) {
	const (
		frameSize = 960
		freq      = 10500.0
		amp       = 0.0015
	)

	makeFrame := func() []float32 {
		pcm := make([]float32, frameSize)
		for i := range pcm {
			pcm[i] = amp * float32(math.Sin(2*math.Pi*freq*float64(i)/48000.0))
		}
		return pcm
	}

	s24 := NewTonalityAnalysisState(48000)
	s24.SetLSBDepth(24)
	s8 := NewTonalityAnalysisState(48000)
	s8.SetLSBDepth(8)

	var info24, info8 AnalysisInfo
	for range 8 {
		pcm := makeFrame()
		info24 = s24.RunAnalysis(pcm, frameSize, 1)
		info8 = s8.RunAnalysis(pcm, frameSize, 1)
	}
	if !info24.Valid || !info8.Valid {
		t.Fatal("expected valid analysis info")
	}
	if info8.BandwidthIndex > info24.BandwidthIndex {
		t.Fatalf("lower LSB depth should not increase detected bandwidth: lsb8=%d lsb24=%d", info8.BandwidthIndex, info24.BandwidthIndex)
	}
}

func TestEncoderSetLSBDepthPropagatesToAnalyzer(t *testing.T) {
	enc := NewEncoder(48000, 1)
	if enc.analyzer == nil {
		t.Fatal("expected analyzer")
	}
	if enc.analyzer.LSBDepth != 24 {
		t.Fatalf("unexpected default analyzer LSB depth: got %d want 24", enc.analyzer.LSBDepth)
	}

	enc.SetLSBDepth(12)
	if enc.analyzer.LSBDepth != 12 {
		t.Fatalf("analyzer LSB depth should follow encoder setting: got %d want 12", enc.analyzer.LSBDepth)
	}

	enc.Reset()
	if enc.analyzer.LSBDepth != 12 {
		t.Fatalf("encoder reset should keep analyzer LSB depth: got %d want 12", enc.analyzer.LSBDepth)
	}

	enc.SetLSBDepth(4)
	if enc.analyzer.LSBDepth != 8 {
		t.Fatalf("analyzer LSB depth should clamp low values: got %d want 8", enc.analyzer.LSBDepth)
	}
	enc.SetLSBDepth(30)
	if enc.analyzer.LSBDepth != 24 {
		t.Fatalf("analyzer LSB depth should clamp high values: got %d want 24", enc.analyzer.LSBDepth)
	}
}

func TestRunAnalysisSilenceCopiesPreviousInfo(t *testing.T) {
	s := NewTonalityAnalysisState(48000)

	const frameSize = 960
	prevInfo := AnalysisInfo{
		Valid:          true,
		MusicProb:      0.42,
		VADProb:        0.11,
		Tonality:       0.33,
		BandwidthIndex: 18,
		MaxPitchRatio:  0.77,
	}
	prevInfo.LeakBoost[0] = 7
	prevInfo.LeakBoost[1] = 3
	s.Info[DetectSize-1] = prevInfo
	s.Count = 7
	s.ECount = 3

	countBefore := s.Count
	eCountBefore := s.ECount
	writeBefore := s.WritePos

	silence := make([]float32, frameSize)
	_ = s.RunAnalysis(silence, frameSize, 1)

	if s.Count != countBefore {
		t.Fatalf("silence should not advance analysis count: got %d want %d", s.Count, countBefore)
	}
	if s.ECount != eCountBefore {
		t.Fatalf("silence should not advance energy history count: got %d want %d", s.ECount, eCountBefore)
	}
	if s.WritePos != (writeBefore+1)%DetectSize {
		t.Fatalf("silence should advance write pos by one slot: got %d want %d", s.WritePos, (writeBefore+1)%DetectSize)
	}

	copiedSlot := (s.WritePos + DetectSize - 1) % DetectSize
	if !reflect.DeepEqual(s.Info[copiedSlot], prevInfo) {
		t.Fatal("silence frame should copy previous analysis info slot")
	}
}

func TestRunAnalysisInitialSilenceKeepsInvalidInfo(t *testing.T) {
	s := NewTonalityAnalysisState(48000)

	const frameSize = 960
	silence := make([]float32, frameSize)
	info := s.RunAnalysis(silence, frameSize, 1)

	if info.Valid {
		t.Fatal("first silence frame should not produce valid analysis info")
	}
	if s.Count != 0 {
		t.Fatalf("silence should not advance analysis count from reset: got %d want 0", s.Count)
	}
	if s.ECount != 0 {
		t.Fatalf("silence should not advance energy count from reset: got %d want 0", s.ECount)
	}
	copiedSlot := (s.WritePos + DetectSize - 1) % DetectSize
	if s.Info[copiedSlot].Valid {
		t.Fatal("copied silence info slot should remain invalid after reset")
	}
}

func TestRunAnalysisNaNInputMarksInfoInvalid(t *testing.T) {
	s := NewTonalityAnalysisState(48000)

	const frameSize = 960
	pcm := make([]float32, frameSize)
	pcm[0] = float32(math.NaN())

	info := s.RunAnalysis(pcm, frameSize, 1)
	if info.Valid {
		t.Fatal("NaN analysis input should yield invalid analysis info")
	}
	if s.Count != 0 {
		t.Fatalf("NaN analysis input should not advance analysis count: got %d want 0", s.Count)
	}
	if s.ECount != 0 {
		t.Fatalf("NaN analysis input should not advance energy count: got %d want 0", s.ECount)
	}
	if s.WritePos != 1 {
		t.Fatalf("NaN analysis input should advance write position by one slot: got %d want 1", s.WritePos)
	}
	slot := (s.WritePos + DetectSize - 1) % DetectSize
	if s.Info[slot].Valid {
		t.Fatal("NaN analysis slot must be marked invalid")
	}
}
