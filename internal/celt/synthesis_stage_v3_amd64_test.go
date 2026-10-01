//go:build amd64.v3

package celt

import (
	"encoding/hex"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const celtMatrixFirstDivergencePacketHex = "e88a9f3c2aeca6682d52b3ec9ebe808f2705db4675ecc57b8d2e3f5ee0ae3694deaf278c68b1d69349f94d007a87fdd64e9626f1884f562be2eeb8543494876bdeea3f6072efd0ee74884fcb8f86cf2acbbcd6bb2e87f31c1ab0"

func TestCELTDecodeV3FirstDivergenceMatchesLibopusC(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 240
	)
	packet, err := hex.DecodeString(celtMatrixFirstDivergencePacketHex)
	if err != nil {
		t.Fatalf("decode matrix packet hex: %v", err)
	}
	if len(packet) == 0 || packet[0] != 0xe8 {
		t.Fatalf("unexpected matrix packet TOC=%#x len=%d", packet[0], len(packet))
	}

	trace := traceLibopusCELTSynthesis(t, sampleRate, channels, frameSize, 0, [][]byte{packet})
	if trace.n != frameSize || trace.channels != channels {
		t.Fatalf("C trace n=%d channels=%d want %d/%d", trace.n, trace.channels, frameSize, channels)
	}

	dec := NewDecoder(channels)
	if err := dec.SetAPISampleRate(sampleRate); err != nil {
		t.Fatalf("SetAPISampleRate: %v", err)
	}
	dec.SetBandwidth(CELTFullband)
	stage := dec.EnableSynthesisStageTrace()
	got := make([]float32, frameSize*channels)
	if err := dec.DecodeFrameWithPacketStereoToFloat32AtAPIRate(packet[1:], frameSize, false, got); err != nil {
		t.Fatalf("DecodeFrameWithPacketStereoToFloat32AtAPIRate: %v", err)
	}
	if !stage.Captured() {
		t.Fatal("gopus synthesis-stage trace did not capture")
	}

	assertCELTDecodeStageEqual(t, "base energy", stage.BaseEnergy(0), trace.baseEnergy[0])
	// C's X scratch vector is an uninitialised stack allocation, and
	// quant_all_bands only writes through the end of the final CELT band. The
	// standard 48 kHz band-edge table ends at bin 100; this 5 ms frame has M=2, so
	// coefficients at and beyond bin 200 are unspecified in C. They do not feed
	// synthesis: denormalise_bands clears the corresponding frequency tail.
	edges := dec.modeEdges()
	endBand := len(trace.baseEnergy[0])
	if endBand >= len(edges) {
		t.Fatalf("C base energy count %d exceeds CELT band edges %d", endBand, len(edges)-1)
	}
	activeNormCount := (frameSize / 120) * edges[endBand]
	if activeNormCount > len(stage.BaseNorm(0)) || activeNormCount > len(trace.baseNorm[0]) {
		t.Fatalf("active base norm length %d exceeds Go/C buffers %d/%d", activeNormCount,
			len(stage.BaseNorm(0)), len(trace.baseNorm[0]))
	}
	assertCELTDecodeStageEqual(t, "base normalized coefficients", stage.BaseNorm(0)[:activeNormCount], trace.baseNorm[0][:activeNormCount])
	assertCELTDecodeStageEqual(t, "post-denormalise spectrum", stage.Spec(0), trace.freq[0])
	assertCELTDecodeStageEqual(t, "post-IMDCT", stage.IMDCT(0), trace.imdct[0])
	assertCELTDecodeStageEqual(t, "post-comb-filter", stage.PostComb(0), trace.postComb[0])
	assertCELTDecodeStageEqual(t, "post-deemphasis PCM", got, trace.final)
}

func assertCELTDecodeStageEqual(t *testing.T, stage string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("first CELT decode divergence at %s: lengths Go/C=%d/%d", stage, len(got), len(want))
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("first CELT decode divergence at %s[%d]: Go=%08x C=%08x", stage, i,
				math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}
