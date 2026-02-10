package encoder

import (
	"math"
	"testing"
)

func TestUpdateOpusVADReusesFreshAnalysis(t *testing.T) {
	const frameSize = 1920

	enc := NewEncoder(48000, 1)
	pcm := make([]float64, frameSize)
	for i := range pcm {
		pcm[i] = 0.25 * math.Sin(2*math.Pi*220*float64(i)/48000.0)
	}

	_ = enc.autoSignalFromPCM(pcm, frameSize)
	if !enc.lastAnalysisValid || !enc.lastAnalysisFresh {
		t.Fatalf("expected fresh analysis after autoSignalFromPCM, valid=%v fresh=%v", enc.lastAnalysisValid, enc.lastAnalysisFresh)
	}

	countBefore := enc.analyzer.Count
	enc.updateOpusVAD(pcm, frameSize)
	if enc.analyzer.Count != countBefore {
		t.Fatalf("updateOpusVAD consumed fresh analysis but still advanced analyzer count: got %d want %d", enc.analyzer.Count, countBefore)
	}
	if enc.lastAnalysisFresh {
		t.Fatal("expected fresh analysis flag to be consumed")
	}
	if !enc.lastOpusVADValid {
		t.Fatal("expected valid Opus VAD after consuming fresh analysis")
	}

	enc.updateOpusVAD(pcm, frameSize)
	if enc.analyzer.Count <= countBefore {
		t.Fatalf("expected fallback analyzer run on second updateOpusVAD call, countBefore=%d countAfter=%d", countBefore, enc.analyzer.Count)
	}
}
