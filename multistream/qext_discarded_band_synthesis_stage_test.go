//go:build gopus_qext && !gopus_fixed_point

package multistream

import (
	"bytes"
	"testing"

	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestQEXTDiscardedBandSynthesisStagesMatchSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	base := encodeModeSwitchSingleStreamPackets(t, 2, 960, []encoder.Mode{encoder.ModeCELT})[0]
	pad := append([]byte{248}, bytes.Repeat([]byte{255}, 32)...)
	packet := append([]byte{base[0] | 3, 0x41, byte(len(pad))}, base[1:]...)
	packet = append(packet, pad...)
	frame := parseQEXTStreamFrameForTest(t, "wide", packet)
	stream := newStreamDecoder(48000, 2)
	stage := stream.celtDec.EnableSynthesisStageTrace()
	got, err := stream.decodeFramePayload(frame.rawFrame, 960, frame.toc, frame.qextPayload)
	if err != nil {
		t.Fatal(err)
	}
	want := traceQEXTCELTSynthesis(t, packet)
	for ch := 0; ch < 2; ch++ {
		for _, p := range []struct {
			name string
			g, w []float32
		}{{"energy", stage.QEXTEnergy(ch)[:2], want.qextEnergy[ch][:2]}, {"qnorm", stage.QEXTNorm(ch)[800:], want.qextNorm[ch][800:]}, {"baseE", stage.BaseEnergy(ch), want.baseEnergy[ch]}, {"baseNorm", stage.BaseNorm(ch)[:800], want.baseNorm[ch][:800]}, {"spec", stage.Spec(ch), want.freq[ch]}, {"imdct", stage.IMDCT(ch), want.imdct[ch]}, {"post", stage.PostComb(ch), want.postComb[ch]}} {
			i, g, w := firstQEXTFloat32Difference(p.g, p.w)
			if i >= 0 {
				t.Errorf("channel %d %s sample %d: Go=%08x C=%08x", ch, p.name, i, g, w)
			}
		}
	}
	i, g, w := firstQEXTFloat32Difference(got, want.final)
	if i >= 0 {
		t.Errorf("PCM sample %d: Go=%08x C=%08x", i, g, w)
	}
}
