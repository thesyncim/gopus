//go:build amd64.v3 && !goexperiment.simd && !gopus_fixed_point

package celt

import (
	"encoding/hex"
	"os"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// celtMono20msVBR1FailingPacketHex is the first CELT-only mono packet from
// TestDecodeDifferentialEncodeThenDecode/celt_fb_ch1_20ms_96000bps_vbr1. Its
// public decode output differs in the scalar AMD64 v3 audit; this trace checks
// each decoder boundary against libopus using that same packet.
const celtMono20msVBR1FailingPacketHex = "f88a80f9b1f279043ccaa4438b1af0a01a527afff72a64d64fa8335d6bdaaa73ef0ca69d8b45928fc7874db179baa757dd607ed384984fcec62aaca858b3c26aa0b05f88753cc837f8721e125980e1c2003036864468a9e3e551ad57e1341dbd3e69f5aff85cdcbc48b3398bbc421fb4817df56c6e9fd633c1a1dda8f647bc7efff1061f79b4dad2182b0fdcf30214a28f0f03ae735245cd8becada0df434bfef3643ddcd482739a48088d273929a77e3cc6e3ee201255495a0fadd30495ca66e72b68ac282d93bd5e1556a996a95237494b12fcdf4a222eace55fe71d4bbbea95e1fcb155fc7e7bc33b1f194fcdccdb45b8324308d74a146032b42efe25f32b92584d4dbed2d3e9242f9ef5feb3ba950069d3e85cb67fd01313a154"

func TestCELTDecodeFailingPacketV3FirstStageWitness(t *testing.T) {
	libopustest.RequireOracle(t)
	if target := os.Getenv("GOPUS_LIBOPUS_AMD64_TARGET"); target != "v3" {
		message := "CELT decode stage witness requires GOPUS_LIBOPUS_AMD64_TARGET=v3, got " + target
		if libopustest.StrictRefRequired() {
			t.Fatal(message)
		}
		t.Skip(message)
	}

	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
	)
	packet, err := hex.DecodeString(celtMono20msVBR1FailingPacketHex)
	if err != nil {
		t.Fatalf("decode captured packet: %v", err)
	}
	if len(packet) < 2 || packet[0] != 0xf8 {
		t.Fatalf("unexpected CELT-only mono packet TOC=%#x len=%d", packet[0], len(packet))
	}

	cTrace := traceLibopusCELTSynthesis(t, sampleRate, channels, frameSize, 0, [][]byte{packet})
	if cTrace.n != frameSize || cTrace.channels != channels {
		t.Fatalf("C trace n=%d channels=%d want %d/%d", cTrace.n, cTrace.channels, frameSize, channels)
	}

	dec := NewDecoder(channels)
	if err := dec.SetAPISampleRate(sampleRate); err != nil {
		t.Fatalf("SetAPISampleRate: %v", err)
	}
	dec.SetBandwidth(CELTFullband)
	trace := dec.EnableSynthesisStageTrace()
	got := make([]float32, frameSize*channels)
	if err := dec.DecodeFrameWithPacketStereoToFloat32AtAPIRate(packet[1:], frameSize, false, got); err != nil {
		t.Fatalf("DecodeFrameWithPacketStereoToFloat32AtAPIRate: %v", err)
	}
	if !trace.Captured() {
		t.Fatal("gopus CELT synthesis trace did not capture")
	}

	assertCELTDecodeStageEqual(t, "base energy", trace.BaseEnergy(0), cTrace.baseEnergy[0])
	endBand := len(cTrace.baseEnergy[0])
	edges := dec.modeEdges()
	if endBand >= len(edges) {
		t.Fatalf("C base energy count %d exceeds CELT band edges %d", endBand, len(edges)-1)
	}
	activeNormCount := (frameSize / 120) * edges[endBand]
	if activeNormCount > len(trace.BaseNorm(0)) || activeNormCount > len(cTrace.baseNorm[0]) {
		t.Fatalf("active base norm length %d exceeds Go/C buffers %d/%d", activeNormCount,
			len(trace.BaseNorm(0)), len(cTrace.baseNorm[0]))
	}
	assertCELTDecodeStageEqual(t, "base normalized coefficients", trace.BaseNorm(0)[:activeNormCount], cTrace.baseNorm[0][:activeNormCount])
	assertCELTDecodeStageEqual(t, "post-denormalise spectrum", trace.Spec(0), cTrace.freq[0])
	assertCELTDecodeStageEqual(t, "post-IMDCT", trace.IMDCT(0), cTrace.imdct[0])
	assertCELTDecodeStageEqual(t, "post-comb-filter", trace.PostComb(0), cTrace.postComb[0])
	assertCELTDecodeStageEqual(t, "post-deemphasis PCM", got, cTrace.final)
}
