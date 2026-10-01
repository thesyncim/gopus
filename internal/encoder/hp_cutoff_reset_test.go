package encoder

import (
	"bytes"
	"testing"

	"github.com/thesyncim/gopus/internal/silk"
	"github.com/thesyncim/gopus/types"
)

func TestVoIPHPCutoffResetMatchesFreshEncoder(t *testing.T) {
	const frameSize = 480
	pcm := make([]float32, frameSize)
	for i := range pcm {
		pcm[i] = float32(.55*float64((i%37)-18)/18 + .22*float64((i%11)-5)/5)
	}
	newEncoder := func() *Encoder {
		e := NewEncoder(48000, 1)
		e.SetVoIPApplication(true)
		e.mode = ModeCELT
		e.bandwidth = types.BandwidthFullband
		e.bitrateMode = ModeCBR
		e.useVBR = false
		e.vbrConstraint = false
		e.bitrate = 64000
		e.complexity = 0
		e.forceChannels = 1
		return e
	}
	encode := func(e *Encoder) ([]byte, error) {
		return e.EncodeFloat32WithAnalysisMaxBytes(pcm, frameSize, pcm, 400)
	}

	e := newEncoder()
	e.variableHPSmth2Q15 = silk.InitVariableHPSmth2Q15() + 8192
	e.variableHPSmth2Inited = true
	_, err := encode(e)
	if err != nil {
		t.Fatalf("seed encode: %v", err)
	}
	if e.hpMem == ([4]float32{}) || !e.variableHPSmth2Inited ||
		e.variableHPSmth2Q15 <= silk.InitVariableHPSmth2Q15() {
		t.Fatalf("seed frame did not retain elevated VoIP HP state: mem=%v smoother=%d initialized=%t",
			e.hpMem, e.variableHPSmth2Q15, e.variableHPSmth2Inited)
	}

	e.Reset()
	if e.hpMem != ([4]float32{}) || e.variableHPSmth2Q15 != 0 || e.variableHPSmth2Inited {
		t.Fatalf("Reset did not clear VoIP HP state: mem=%v smoother=%d initialized=%t",
			e.hpMem, e.variableHPSmth2Q15, e.variableHPSmth2Inited)
	}
	resetPacket, err := encode(e)
	if err != nil {
		t.Fatalf("encode after Reset: %v", err)
	}
	fresh := newEncoder()
	freshPacket, err := encode(fresh)
	if err != nil {
		t.Fatalf("encode fresh state: %v", err)
	}
	if !bytes.Equal(resetPacket, freshPacket) || e.FinalRange() != fresh.FinalRange() || e.hpMem != fresh.hpMem {
		t.Fatalf("Reset replay differs from fresh VoIP state: packet=%x/%x range=%08x/%08x mem=%v/%v",
			resetPacket, freshPacket, e.FinalRange(), fresh.FinalRange(), e.hpMem, fresh.hpMem)
	}
}
