package encoder

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/types"
)

func TestUpdateOpusVADReusesFreshAnalysis(t *testing.T) {
	const frameSize = 1920

	enc := NewEncoder(48000, 1)
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
	enc.updateOpusVADRes(pcmRes, frameSize)
	if enc.analyzer.Count != countBefore {
		t.Fatalf("updateOpusVAD consumed fresh analysis but still advanced analyzer count: got %d want %d", enc.analyzer.Count, countBefore)
	}
	if enc.lastAnalysisFresh {
		t.Fatal("expected fresh analysis flag to be consumed")
	}
	if !enc.lastOpusVADValid {
		t.Fatal("expected valid Opus VAD after consuming fresh analysis")
	}

	// libopus opus_encoder.c never re-runs the tonality analysis in the VAD
	// path: opus_encode_frame_native() derives activity from the analysis_info
	// produced once per frame by run_analysis(). A second updateOpusVADRes call
	// without a fresh analysis must therefore reuse the last valid snapshot and
	// must NOT advance the analyzer (re-running RunAnalysis would mutate
	// write_pos/read cursor and desynchronise the next frame's curr_lookahead).
	enc.updateOpusVADRes(pcmRes, frameSize)
	if enc.analyzer.Count != countBefore {
		t.Fatalf("second updateOpusVAD call must not advance analyzer: countBefore=%d countAfter=%d", countBefore, enc.analyzer.Count)
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
