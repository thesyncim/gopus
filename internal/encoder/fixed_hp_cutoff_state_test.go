//go:build gopus_fixed_point

package encoder

import (
	"bytes"
	"testing"

	"github.com/thesyncim/gopus/internal/silk"
	"github.com/thesyncim/gopus/types"
)

func TestFixedVoIPHPCutoffResetAndLowSpaceState(t *testing.T) {
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
	encode := func(e *Encoder, maxBytes int) ([]byte, error) {
		return e.EncodeFloat32WithAnalysisMaxBytes(pcm, frameSize, pcm, maxBytes)
	}

	e := newEncoder()
	packet, err := encode(e, 1)
	if err != nil || len(packet) != 1 {
		t.Fatalf("initial low-space packet len=%d err=%v", len(packet), err)
	}
	if e.fixedHPMem != ([4]int32{}) || e.hpMem != ([4]float32{}) || e.variableHPSmth2Inited {
		t.Fatalf("low-space packet changed HP state: fixed=%v float=%v smoother=%d initialized=%t",
			e.fixedHPMem, e.hpMem, e.variableHPSmth2Q15, e.variableHPSmth2Inited)
	}
	e.variableHPSmth2Q15 = silk.InitVariableHPSmth2Q15() + 8192
	e.variableHPSmth2Inited = true
	packet, err = encode(e, 400)
	if err != nil || len(packet) < 2 {
		t.Fatalf("real VoIP frame len=%d err=%v", len(packet), err)
	}
	memAfterReal := e.fixedHPMem
	if memAfterReal == ([4]int32{}) || !e.variableHPSmth2Inited {
		t.Fatalf("real frame did not advance fixed HP state: mem=%v smoother=%d initialized=%t",
			memAfterReal, e.variableHPSmth2Q15, e.variableHPSmth2Inited)
	}
	smootherAfterReal := e.variableHPSmth2Q15
	packet, err = encode(e, 1)
	if err != nil || len(packet) != 1 {
		t.Fatalf("second low-space packet len=%d err=%v", len(packet), err)
	}
	if e.fixedHPMem != memAfterReal || e.variableHPSmth2Q15 != smootherAfterReal {
		t.Fatalf("second low-space packet advanced state: fixed=%v/%v smoother=%d/%d",
			e.fixedHPMem, memAfterReal, e.variableHPSmth2Q15, smootherAfterReal)
	}

	e.Reset()
	if e.fixedHPMem != ([4]int32{}) || e.hpMem != ([4]float32{}) ||
		e.variableHPSmth2Q15 != 0 || e.variableHPSmth2Inited {
		t.Fatalf("Reset did not clear VoIP HP state: fixed=%v float=%v smoother=%d initialized=%t",
			e.fixedHPMem, e.hpMem, e.variableHPSmth2Q15, e.variableHPSmth2Inited)
	}

	resetPacket, err := encode(e, 400)
	if err != nil {
		t.Fatalf("encode after Reset: %v", err)
	}
	fresh := newEncoder()
	freshPacket, err := encode(fresh, 400)
	if err != nil {
		t.Fatalf("encode fresh state: %v", err)
	}
	if !bytes.Equal(resetPacket, freshPacket) || e.FinalRange() != fresh.FinalRange() || e.fixedHPMem != fresh.fixedHPMem {
		t.Fatalf("Reset replay differs from fresh VoIP state: packet=%x/%x range=%08x/%08x mem=%v/%v",
			resetPacket, freshPacket, e.FinalRange(), fresh.FinalRange(), e.fixedHPMem, fresh.fixedHPMem)
	}
}
