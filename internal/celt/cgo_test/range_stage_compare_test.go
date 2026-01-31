// Package cgo compares range decoder stage states between gopus and libopus packets.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

type stageState struct {
	rng      uint32
	tell     int
	tellFrac int
}

type stageTracer struct {
	celt.NoopTracer
	stages map[string]stageState
	order  []string
}

func (t *stageTracer) TraceRange(stage string, rng uint32, tell, tellFrac int) {
	if t.stages == nil {
		t.stages = make(map[string]stageState)
	}
	if _, ok := t.stages[stage]; !ok {
		t.order = append(t.order, stage)
	}
	t.stages[stage] = stageState{rng: rng, tell: tell, tellFrac: tellFrac}
}

func TestRangeStageCompareLibopus(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm64 := make([]float64, frameSize)
	pcm32 := make([]float32, frameSize)
	for i := range pcm64 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm64[i] = val
		pcm32[i] = float32(val)
	}

	// Encode with libopus
	libEnc, err := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("libopus encoder creation failed: %v", err)
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(false)

	libPacket, libLen := libEnc.EncodeFloat(pcm32, frameSize)
	if libLen <= 0 {
		t.Fatalf("libopus encode failed: length=%d", libLen)
	}

	// Encode with gopus
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	goPacket, err := goEnc.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	// Decode gopus packet with range stage tracing
	goTracer := &stageTracer{}
	origTracer := celt.DefaultTracer
	celt.DefaultTracer = goTracer
	{
		dec := celt.NewDecoder(1)
		if _, err := dec.DecodeFrame(goPacket, frameSize); err != nil {
			t.Fatalf("gopus decode failed: %v", err)
		}
	}
	celt.DefaultTracer = origTracer

	// Decode libopus packet (skip TOC)
	libTracer := &stageTracer{}
	celt.DefaultTracer = libTracer
	{
		dec := celt.NewDecoder(1)
		if _, err := dec.DecodeFrame(libPacket[1:], frameSize); err != nil {
			t.Fatalf("libopus decode failed: %v", err)
		}
	}
	celt.DefaultTracer = origTracer

	// Compare stage states in order observed by gopus decode
	firstMismatch := ""
	for _, stage := range goTracer.order {
		goState, okGo := goTracer.stages[stage]
		libState, okLib := libTracer.stages[stage]
		if !okGo || !okLib {
			continue
		}
		if goState.tell != libState.tell || goState.rng != libState.rng {
			firstMismatch = stage
			break
		}
	}

	if firstMismatch == "" {
		t.Log("All traced range stages match (tell/rng)")
		return
	}

	// Log a small table to identify divergence
	t.Logf("Range stage mismatch at: %s", firstMismatch)
	t.Log("stage | gopus tell/rng | libopus tell/rng")
	for _, stage := range goTracer.order {
		goState, okGo := goTracer.stages[stage]
		libState, okLib := libTracer.stages[stage]
		if !okGo || !okLib {
			continue
		}
		status := "MATCH"
		if goState.tell != libState.tell || goState.rng != libState.rng {
			status = "DIFF"
		}
		t.Logf("%s | %d/0x%08X | %d/0x%08X (%s)", stage, goState.tell, goState.rng, libState.tell, libState.rng, status)
	}

	t.Fatalf("range stage mismatch at %s", firstMismatch)
}
