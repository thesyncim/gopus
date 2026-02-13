package encoder

import (
	"math"
	"reflect"
	"testing"
)

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
		got := analysisFastAtan2f(tc.y, tc.x)
		if math.Abs(float64(got-tc.want)) > eps {
			t.Fatalf("analysisFastAtan2f(%f,%f)=%.6f, want %.6f (+/- %.3f)", tc.y, tc.x, got, tc.want, eps)
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
	for i := 0; i < 8; i++ {
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
	for i := 0; i < frameSize; i++ {
		loud[i] = 0.8 * float32(math.Sin(2*math.Pi*220.0*float64(i)/48000.0))
		quiet[i] = 1e-4 * float32(math.Sin(2*math.Pi*220.0*float64(i)/48000.0))
	}

	for i := 0; i < 12; i++ {
		_ = s.RunAnalysis(loud, frameSize, 1)
	}
	lowEBefore := s.LowECount

	var info AnalysisInfo
	for i := 0; i < 12; i++ {
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
	for i := 0; i < 3; i++ {
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
	if cap(s.scratchFFTKiss) < 480 {
		t.Fatalf("reset should preserve reusable FFT scratch capacity: got %d", cap(s.scratchFFTKiss))
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
	for i := 0; i < 8; i++ {
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
