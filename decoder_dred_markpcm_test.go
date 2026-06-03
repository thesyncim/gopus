//go:build gopus_dred || gopus_extra_controls

package gopus

import (
	"slices"
	"testing"

	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

func TestDecoderMarkDREDUpdatedPCMRefreshesNeuralHistory(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	var pcm [2 * lpcnetplc.FrameSize]float32
	edge := []float32{-1.0, -0.9999847412109375, -1.5 / 32768.0, -0.5 / 32768.0, 0, 0.5 / 32768.0, 1.5 / 32768.0, 0.999969482421875, 1.0}
	for i := range pcm {
		pcm[i] = edge[i%len(edge)]
	}
	dec.ensureDREDRecoveryState()
	state := requireDecoderDREDState(t, dec)
	if got := state.dredPLC.MarkUpdatedFrameFloat(pcm[:lpcnetplc.FrameSize]); got != lpcnetplc.FrameSize {
		t.Fatalf("MarkUpdatedFrameFloat()=%d want %d", got, lpcnetplc.FrameSize)
	}
	state.dredPLC.MarkConcealed()
	var beforeHistory [lpcnetplc.PLCBufSize]float32
	if n := state.dredPLC.FillPCMHistory(beforeHistory[:]); n != lpcnetplc.PLCBufSize {
		t.Fatalf("FillPCMHistory(before)=%d want %d", n, lpcnetplc.PLCBufSize)
	}
	before := state.dredPLC.Snapshot()
	dec.markDREDUpdatedPCM(pcm[:], len(pcm), ModeSILK)
	after := state.dredPLC.Snapshot()
	if after.Blend != 0 {
		t.Fatalf("Blend=%d want 0", after.Blend)
	}
	wantAnalysisPos := max(0, before.AnalysisPos-len(pcm))
	if after.AnalysisPos != wantAnalysisPos {
		t.Fatalf("AnalysisPos=%d want %d", after.AnalysisPos, wantAnalysisPos)
	}
	wantPredictPos := max(0, before.PredictPos-len(pcm))
	if after.PredictPos != wantPredictPos {
		t.Fatalf("PredictPos=%d want %d", after.PredictPos, wantPredictPos)
	}
	if after.LossCount != 0 {
		t.Fatalf("LossCount=%d want 0", after.LossCount)
	}
	var history [lpcnetplc.PLCBufSize]float32
	if n := state.dredPLC.FillPCMHistory(history[:]); n != lpcnetplc.PLCBufSize {
		t.Fatalf("FillPCMHistory()=%d want %d", n, lpcnetplc.PLCBufSize)
	}
	for i := 0; i < len(pcm); i++ {
		want := lpcnetplcTestQuantizePCMUpdateFloat(pcm[i])
		got := history[len(history)-len(pcm)+i]
		if got != want {
			t.Fatalf("history tail[%d]=%v want %v", i, got, want)
		}
	}
	if slices.Equal(history[:], beforeHistory[:]) {
		t.Fatal("history unexpectedly stayed unchanged after good-frame update")
	}
}

func TestDecoderMarkDREDUpdatedPCMSkipsPublicFallbackAfterRawHistory(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	dec.ensureDREDRecoveryState()
	state := requireDecoderDREDState(t, dec)
	var raw [lpcnetplc.FrameSize]int16
	for i := range raw {
		raw[i] = int16((i%17 - 8) * 321)
	}
	dec.primeDREDPCMHistoryInt16(raw[:])
	before := state.dredPLC.Snapshot()
	var beforeHistory [lpcnetplc.PLCBufSize]float32
	if n := state.dredPLC.FillPCMHistory(beforeHistory[:]); n != lpcnetplc.PLCBufSize {
		t.Fatalf("FillPCMHistory(before)=%d want %d", n, lpcnetplc.PLCBufSize)
	}
	state.dredPLC.MarkConcealed()

	public := make([]float32, 2*lpcnetplc.FrameSize)
	for i := range public {
		public[i] = float32((i%31)-15) / 7
	}
	dec.markDREDUpdatedPCM(public, lpcnetplc.FrameSize, ModeSILK)

	after := state.dredPLC.Snapshot()
	if after.Blend != 0 {
		t.Fatalf("Blend=%d want 0", after.Blend)
	}
	if after.AnalysisPos != before.AnalysisPos || after.PredictPos != before.PredictPos || after.LossCount != before.LossCount {
		t.Fatalf("public fallback advanced raw-updated history: before=%+v after=%+v", before, after)
	}
	var afterHistory [lpcnetplc.PLCBufSize]float32
	if n := state.dredPLC.FillPCMHistory(afterHistory[:]); n != lpcnetplc.PLCBufSize {
		t.Fatalf("FillPCMHistory(after)=%d want %d", n, lpcnetplc.PLCBufSize)
	}
	if !slices.Equal(afterHistory[:], beforeHistory[:]) {
		t.Fatal("public fallback rewrote raw-updated DRED PCM history")
	}
}

func TestDecoderMarkDREDUpdatedPCMCELTKeepsBridgeOwnedHistory(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	var pcm [2 * lpcnetplc.FrameSize]float32
	for i := range pcm {
		pcm[i] = float32((i%23)-11) / 23
	}
	dec.ensureDREDRecoveryState()
	state := requireDecoderDREDState(t, dec)
	if got := state.dredPLC.MarkUpdatedFrameFloat(pcm[:lpcnetplc.FrameSize]); got != lpcnetplc.FrameSize {
		t.Fatalf("MarkUpdatedFrameFloat()=%d want %d", got, lpcnetplc.FrameSize)
	}
	state.dredPLC.MarkConcealed()
	before := state.dredPLC.Snapshot()
	var beforeHistory [lpcnetplc.PLCBufSize]float32
	if n := state.dredPLC.FillPCMHistory(beforeHistory[:]); n != lpcnetplc.PLCBufSize {
		t.Fatalf("FillPCMHistory(before)=%d want %d", n, lpcnetplc.PLCBufSize)
	}

	dec.markDREDUpdatedPCM(pcm[:], len(pcm), ModeCELT)

	after := state.dredPLC.Snapshot()
	if after.Blend != 0 {
		t.Fatalf("Blend=%d want 0", after.Blend)
	}
	if after.AnalysisPos != before.AnalysisPos || after.PredictPos != before.PredictPos || after.LossCount != before.LossCount {
		t.Fatalf("unexpected CELT history cursor update: before=%+v after=%+v", before, after)
	}
	var history [lpcnetplc.PLCBufSize]float32
	if n := state.dredPLC.FillPCMHistory(history[:]); n != lpcnetplc.PLCBufSize {
		t.Fatalf("FillPCMHistory(after)=%d want %d", n, lpcnetplc.PLCBufSize)
	}
	if !slices.Equal(history[:], beforeHistory[:]) {
		t.Fatal("CELT good-frame mark unexpectedly rewrote DRED PCM history")
	}
}

func TestDecoderMarkDREDUpdatedPCMDormantWithoutSidecar(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	var pcm [2 * lpcnetplc.FrameSize]float32
	for i := range pcm {
		pcm[i] = float32((i%13)-6) / 13
	}
	dec.markDREDUpdatedPCM(pcm[:], len(pcm), ModeSILK)
	if dec.dredState() != nil {
		t.Fatalf("dred sidecar awakened without sidecar request: %+v", dec.dredState())
	}
}

func TestDecoderMarkDREDUpdatedPCMDoesNotTrackHistoryWithoutNeuralConcealment(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	setValidDREDDecoderBlobForTest(t, dec)

	var pcm [2 * lpcnetplc.FrameSize]float32
	for i := range pcm {
		pcm[i] = float32((i%17)-8) / 17
	}
	dec.markDREDUpdatedPCM(pcm[:], len(pcm), ModeSILK)

	state := requireDecoderDREDState(t, dec)
	if state.decoderDREDRecoveryState != nil {
		t.Fatalf("standalone DRED arm eagerly allocated recovery state after markDREDUpdatedPCM: %+v", state.decoderDREDRecoveryState)
	}
}
