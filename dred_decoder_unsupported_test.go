//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

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
	if dred.ProcessStage() != DREDProcessStageEmpty || !dred.Empty() || dred.NeedsProcessing() || dred.Processed() {
		t.Fatalf("Parse without model mutated dred state: stage=%d empty=%v needs=%v processed=%v", dred.ProcessStage(), dred.Empty(), dred.NeedsProcessing(), dred.Processed())
	}
}

func TestDREDDecoderParseAndProcessRetainsMetadata(t *testing.T) {
	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(makeValidDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	packet := makeTwoFramePacketWithDREDForStandaloneTest(t, 8, -4)
	dred := NewDRED()

	available, dredEnd, err := dec.Parse(dred, packet, 960, 48000, true)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if available != 480 || dredEnd != 480 {
		t.Fatalf("Parse=(available=%d,dredEnd=%d) want (480,480)", available, dredEnd)
	}
	if dred.ProcessStage() != DREDProcessStageDeferred || !dred.NeedsProcessing() || dred.Processed() {
		t.Fatalf("stage after deferred parse = (%d, needs=%v, processed=%v) want (%d,true,false)", dred.ProcessStage(), dred.NeedsProcessing(), dred.Processed(), DREDProcessStageDeferred)
	}
	if dred.Len() == 0 {
		t.Fatal("deferred parse did not retain DRED payload")
	}
	if got := dred.Parsed().Header.DredOffset; got != -4 {
		t.Fatalf("Parsed().Header.DredOffset=%d want -4", got)
	}
	if got := dred.Parsed().Header.DredFrameOffset; got != 8 {
		t.Fatalf("Parsed().Header.DredFrameOffset=%d want 8", got)
	}

	processed := NewDRED()
	if err := dec.Process(dred, processed); err != nil {
		t.Fatalf("Process error: %v", err)
	}
	if processed.ProcessStage() != DREDProcessStageProcessed || processed.NeedsProcessing() || !processed.Processed() {
		t.Fatalf("processed stage = (%d, needs=%v, processed=%v) want (%d,false,true)", processed.ProcessStage(), processed.NeedsProcessing(), processed.Processed(), DREDProcessStageProcessed)
	}
	result := processed.Result(960, 48000)
	if result.Availability.FeatureFrames != 4 || result.Availability.MaxLatents != 0 || result.Availability.OffsetSamples != -480 || result.Availability.EndSamples != 480 || result.Availability.AvailableSamples != 480 {
		t.Fatalf("Result=%+v want availability {FeatureFrames:4 MaxLatents:0 OffsetSamples:-480 EndSamples:480 AvailableSamples:480}", result)
	}
	if got := processed.Availability(960, 48000); got != result.Availability {
		t.Fatalf("Availability()=%+v want %+v", got, result.Availability)
	}
	if got := processed.MaxAvailableSamples(960, 48000); got != 480 {
		t.Fatalf("MaxAvailableSamples()=%d want 480", got)
	}
	quant := make([]int, 6)
	if n := processed.FillQuantizerLevels(quant, 10080, 48000); n != 0 {
		t.Fatalf("FillQuantizerLevels count=%d want 0", n)
	}
	window := result.FeatureWindow(3840, 960, 0)
	if window.FeatureOffsetBase != 5 || window.RecoverableFeatureFrames != 0 || window.MissingPositiveFrames != 2 {
		t.Fatalf("FeatureWindow=%+v want base=5 recoverable=0 missing=2", window)
	}
	if got := processed.FeatureWindow(960, 48000, 3840, 960, 0); got != window {
		t.Fatalf("FeatureWindow()=%+v want %+v", got, window)
	}
	if err := dec.Process(processed, processed); err != nil {
		t.Fatalf("Process(processed, processed) error: %v", err)
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
