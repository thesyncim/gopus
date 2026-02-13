package encoder

import (
	"math"
	"testing"
)

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
