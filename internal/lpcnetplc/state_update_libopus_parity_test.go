//go:build gopus_extra_controls

package lpcnetplc

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestMarkUpdatedFrameFloatMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
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
	edge := []float32{
		-1.5,
		-1.0,
		-0.9999847412109375,
		-1.5 / 32768.0,
		-0.5 / 32768.0,
		0,
		0.5 / 32768.0,
		1.5 / 32768.0,
		0.999969482421875,
		1.0,
		1.5,
	}
	for i := range frame {
		frame[i] = edge[i%len(edge)]
	}

	want, err := probeLibopusPLCUpdate(st, frame[:])
	if err != nil {
		libopustest.HelperUnavailable(t, "plc update", err)
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

func TestMarkUpdatedFrameInt16MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
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
	var frame [FrameSize]int16
	values := []int16{-32768, -32767, -3, -2, -1, 0, 1, 2, 3, 32766, 32767}
	for i := range frame {
		frame[i] = values[i%len(values)]
	}

	want, err := probeLibopusPLCUpdateInt16(st, frame[:])
	if err != nil {
		libopustest.HelperUnavailable(t, "plc update", err)
	}

	if n := st.MarkUpdatedFrameInt16(frame[:]); n != FrameSize {
		t.Fatalf("MarkUpdatedFrameInt16()=%d want %d", n, FrameSize)
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
