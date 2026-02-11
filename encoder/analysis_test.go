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
}