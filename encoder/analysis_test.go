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
