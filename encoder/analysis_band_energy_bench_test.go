package encoder

import (
	"math"
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
	for i := 0; i < analysisBenchNumBins; i++ {
		phase := 0.19 * float64(i+1)
		v := 0.52 + 0.48*math.Sin(phase)
		in.binE[i] = float32(0.001 + v*v)
	}
	for i := 0; i < analysisBenchBinEnd; i++ {
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

	for b := 0; b < NbTBands; b++ {
		s.LowE[b] = -8.0 + 0.1*float32(b)
		s.HighE[b] = s.LowE[b] + 5.5
		s.PrevBandTonality[b] = 0.35 + 0.02*float32((b+3)%5)
		s.MeanE[b] = 0.25 + 0.04*float32((b+1)%7)
	}

	for i := 0; i < NbFrames; i++ {
		for b := 0; b < NbTBands; b++ {
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

	for b := 0; b < NbTBands; b++ {
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
		for i := 0; i < NbFrames; i++ {
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

	for b := 0; b < NbTBands; b++ {
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
		if s.PrevBandwidth >= b+1 {
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

	for b := 0; b < NbTBands; b++ {
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
		for i := 0; i < NbFrames; i++ {
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

	for b := 0; b < NbTBands; b++ {
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
		if s.PrevBandwidth >= b+1 {
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
