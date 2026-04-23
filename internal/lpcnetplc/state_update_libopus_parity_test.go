//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package lpcnetplc

import "testing"

func TestMarkUpdatedFrameFloatMatchesLibopus(t *testing.T) {
	var st State
	st.runtimeInit = true
	st.blend = 1
	st.lossCount = 4
	st.analysisGap = 0
	st.analysisPos = PLCBufSize - FrameSize
	st.predictPos = PLCBufSize - 2*FrameSize
	for i := range st.pcm {
		st.pcm[i] = float32((i%29)-14) / 31
	}
	var frame [FrameSize]float32
	for i := range frame {
		frame[i] = float32((i%17)-8) / 17
	}

	want, err := probeLibopusPLCUpdate(st, frame[:])
	if err != nil {
		t.Skipf("libopus plc update helper unavailable: %v", err)
	}

	if n := st.MarkUpdatedFrameFloat(frame[:]); n != FrameSize {
		t.Fatalf("MarkUpdatedFrameFloat()=%d want %d", n, FrameSize)
	}
	if st.blend != want.Blend {
		t.Fatalf("blend=%d want %d", st.blend, want.Blend)
	}
	if st.lossCount != want.LossCount {
		t.Fatalf("lossCount=%d want %d", st.lossCount, want.LossCount)
	}
	if st.analysisGap != want.AnalysisGap {
		t.Fatalf("analysisGap=%d want %d", st.analysisGap, want.AnalysisGap)
	}
	if st.analysisPos != want.AnalysisPos {
		t.Fatalf("analysisPos=%d want %d", st.analysisPos, want.AnalysisPos)
	}
	if st.predictPos != want.PredictPos {
		t.Fatalf("predictPos=%d want %d", st.predictPos, want.PredictPos)
	}
	assertFloat32Close(t, st.pcm[:], want.PCM, 1e-7, "updated pcm")
}
