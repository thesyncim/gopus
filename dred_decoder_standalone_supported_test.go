//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package gopus

import (
	"errors"
	"testing"

	internaldred "github.com/thesyncim/gopus/internal/dred"
)

func makeTwoFramePacketWithDREDForStandaloneTest(t *testing.T, dredFrameOffset, dredOffset int) []byte {
	t.Helper()

	base := makeValidCELTPacketForDREDTest(t)
	if len(base) < 2 {
		t.Fatal("base packet too short")
	}
	body := makeExperimentalDREDPayloadBodyForTest(t, dredFrameOffset, dredOffset)
	packet := make([]byte, len(base)*2+16)
	n, err := buildPacketWithOptions(base[0]&0xFC, [][]byte{base[1:], base[1:]}, packet, 0, false, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 1, Data: append([]byte{'D', internaldred.ExperimentalVersion}, body...)},
	}, false)
	if err != nil {
		t.Fatalf("buildPacketWithOptions error: %v", err)
	}
	return packet[:n]
}

func TestDREDDecoderParseRequiresModel(t *testing.T) {
	dec := NewDREDDecoder()
	dred := NewDRED()
	packet := makeTwoFramePacketWithDREDForStandaloneTest(t, 8, -4)

	if _, _, err := dec.Parse(dred, packet, 960, 48000, true); !errors.Is(err, ErrDREDModelNotLoaded) {
		t.Fatalf("Parse without model error=%v want=%v", err, ErrDREDModelNotLoaded)
	}
	if dred.RawProcessStage() != -1 {
		t.Fatalf("RawProcessStage()=%d want -1", dred.RawProcessStage())
	}
	if dred.ProcessStage() != DREDProcessStageEmpty || !dred.Empty() || dred.NeedsProcessing() || dred.Processed() {
		t.Fatalf("Parse without model mutated dred state: stage=%d empty=%v needs=%v processed=%v", dred.ProcessStage(), dred.Empty(), dred.NeedsProcessing(), dred.Processed())
	}
}

func TestDREDDecoderParseClearsStateWhenPacketHasNoDRED(t *testing.T) {
	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(makeValidDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	dred := NewDRED()
	withDRED := makeTwoFramePacketWithDREDForStandaloneTest(t, 8, -4)
	if _, _, err := dec.Parse(dred, withDRED, 960, 48000, false); err != nil {
		t.Fatalf("Parse(withDRED) error: %v", err)
	}
	if dred.Empty() {
		t.Fatal("expected retained DRED payload after Parse(withDRED)")
	}

	withoutDRED := makeValidCELTPacketForDREDTest(t)
	available, dredEnd, err := dec.Parse(dred, withoutDRED, 960, 48000, false)
	if err != nil {
		t.Fatalf("Parse(withoutDRED) error: %v", err)
	}
	if available != 0 || dredEnd != 0 {
		t.Fatalf("Parse(withoutDRED)=(available=%d,dredEnd=%d) want (0,0)", available, dredEnd)
	}
	if !dred.Empty() || dred.ProcessStage() != DREDProcessStageEmpty || dred.NeedsProcessing() || dred.Processed() {
		t.Fatalf("Parse(withoutDRED) left dred state stage=%d empty=%v needs=%v processed=%v", dred.ProcessStage(), dred.Empty(), dred.NeedsProcessing(), dred.Processed())
	}
}

func TestDREDDecoderProcessRejectsEmptyState(t *testing.T) {
	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(makeValidDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	dred := NewDRED()
	if err := dec.Process(dred, dred); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Process(empty, empty) error=%v want=%v", err, ErrInvalidArgument)
	}
}

func TestDREDDecoderProcessDoesNotAllocate(t *testing.T) {
	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(makeValidDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	src := NewDRED()
	packet := makeTwoFramePacketWithDREDForStandaloneTest(t, 8, -4)
	if _, _, err := dec.Parse(src, packet, 960, 48000, true); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	dst := NewDRED()

	allocs := testing.AllocsPerRun(1000, func() {
		if err := dec.Process(src, dst); err != nil {
			t.Fatalf("Process error: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("AllocsPerRun=%v want 0", allocs)
	}
}

func TestDREDDecoderParseAndProcessDoesNotAllocate(t *testing.T) {
	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(makeValidDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	packet := makeTwoFramePacketWithDREDForStandaloneTest(t, 8, -4)
	dred := NewDRED()

	allocs := testing.AllocsPerRun(1000, func() {
		if _, _, err := dec.Parse(dred, packet, 960, 48000, false); err != nil {
			t.Fatalf("Parse error: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("AllocsPerRun=%v want 0", allocs)
	}
}

func TestDREDDecoderParseClearsStateOnMalformedPacket(t *testing.T) {
	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(makeValidDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	dred := NewDRED()
	valid := makeTwoFramePacketWithDREDForStandaloneTest(t, 8, -4)
	if _, _, err := dec.Parse(dred, valid, 960, 48000, false); err != nil {
		t.Fatalf("Parse(valid) error: %v", err)
	}
	if dred.Empty() || !dred.Processed() {
		t.Fatal("expected processed retained DRED state after valid parse")
	}

	malformed := buildMalformedSingleFrameExtensionPacketForTest(t, makeValidCELTPacketForDREDTest(t))
	if _, _, err := dec.Parse(dred, malformed, 960, 48000, false); !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("Parse(malformed) error=%v want=%v", err, ErrInvalidPacket)
	}
	if !dred.Empty() || dred.Len() != 0 || dred.ProcessStage() != DREDProcessStageEmpty || dred.NeedsProcessing() || dred.Processed() {
		t.Fatalf("Parse(malformed) left stale dred state: len=%d stage=%d needs=%v processed=%v", dred.Len(), dred.ProcessStage(), dred.NeedsProcessing(), dred.Processed())
	}
}
