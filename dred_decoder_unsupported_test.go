//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import (
	"errors"
	"math"
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
	decodeWant, err := probeLibopusDREDDecode(packet, 960, 48000)
	if err != nil {
		t.Skipf("libopus dred decode helper unavailable: %v", err)
	}
	if decodeWant.availableSamples < 0 {
		t.Fatalf("libopus dred decode returned error %d", decodeWant.availableSamples)
	}
	if got := dred.LatentCount(); got != decodeWant.nbLatents {
		t.Fatalf("LatentCount()=%d want %d", got, decodeWant.nbLatents)
	}
	state := make([]float32, internaldred.StateDim)
	if n := dred.FillState(state); n != internaldred.StateDim {
		t.Fatalf("FillState count=%d want %d", n, internaldred.StateDim)
	}
	assertFloat32BitsEqual(t, state, decodeWant.state[:], "state")
	latents := make([]float32, internaldred.MaxLatents*internaldred.LatentStride)
	wantLatents := decodeWant.nbLatents * internaldred.LatentStride
	if n := dred.FillLatents(latents); n != wantLatents {
		t.Fatalf("FillLatents count=%d want %d", n, wantLatents)
	}
	assertFloat32BitsEqual(t, latents[:wantLatents], decodeWant.latents, "latents")

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
	processedState := make([]float32, internaldred.StateDim)
	if n := processed.FillState(processedState); n != internaldred.StateDim {
		t.Fatalf("processed FillState count=%d want %d", n, internaldred.StateDim)
	}
	assertFloat32BitsEqual(t, processedState, state, "processed state")
	processedLatents := make([]float32, internaldred.MaxLatents*internaldred.LatentStride)
	if n := processed.FillLatents(processedLatents); n != wantLatents {
		t.Fatalf("processed FillLatents count=%d want %d", n, wantLatents)
	}
	assertFloat32BitsEqual(t, processedLatents[:wantLatents], latents[:wantLatents], "processed latents")
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

func TestStandaloneDREDParseMatchesLibopus(t *testing.T) {
	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(makeValidDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}

	base := makeValidCELTPacketForDREDTest(t)
	tests := []struct {
		name           string
		packet         []byte
		maxDREDSamples int
	}{
		{
			name: "valid_payload",
			packet: buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
				{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, makeExperimentalDREDPayloadBodyForTest(t, 0, 4)...)},
			}),
			maxDREDSamples: 960,
		},
		{
			name: "first_extension_invalid_second_valid",
			packet: buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
				{ID: internaldred.ExtensionID, Frame: 0, Data: []byte{'X', internaldred.ExperimentalVersion, 0x10}},
				{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, makeExperimentalDREDPayloadBodyForTest(t, 0, 4)...)},
			}),
			maxDREDSamples: 960,
		},
		{
			name: "short_experimental_payload",
			packet: buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
				{ID: internaldred.ExtensionID, Frame: 0, Data: []byte{'D', internaldred.ExperimentalVersion, 0xaa, 0xbb}},
			}),
			maxDREDSamples: 960,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dred := NewDRED()
			available, dredEnd, err := dec.Parse(dred, tc.packet, tc.maxDREDSamples, 48000, true)
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			if dred.ProcessStage() != DREDProcessStageDeferred || !dred.NeedsProcessing() || dred.Processed() {
				t.Fatalf("stage after deferred Parse = (%d, needs=%v, processed=%v) want (%d,true,false)", dred.ProcessStage(), dred.NeedsProcessing(), dred.Processed(), DREDProcessStageDeferred)
			}

			want, err := probeLibopusDREDParse(tc.packet, tc.maxDREDSamples, 48000)
			if err != nil {
				t.Skipf("libopus dred helper unavailable: %v", err)
			}
			if want.availableSamples < 0 {
				t.Fatalf("libopus dred parse returned error %d", want.availableSamples)
			}
			if available != want.availableSamples {
				t.Fatalf("available=%d want %d", available, want.availableSamples)
			}
			if dredEnd != want.dredEndSamples {
				t.Fatalf("dredEnd=%d want %d", dredEnd, want.dredEndSamples)
			}

			decodeWant, err := probeLibopusDREDDecode(tc.packet, tc.maxDREDSamples, 48000)
			if err != nil {
				t.Skipf("libopus dred decode helper unavailable: %v", err)
			}
			if decodeWant.availableSamples < 0 {
				t.Fatalf("libopus dred decode returned error %d", decodeWant.availableSamples)
			}
			if int(dred.ProcessStage()) != decodeWant.processStage {
				t.Fatalf("ProcessStage=%d want %d", dred.ProcessStage(), decodeWant.processStage)
			}
			if got := dred.Parsed().Header.DredOffset; got != decodeWant.dredOffset {
				t.Fatalf("Parsed().Header.DredOffset=%d want %d", got, decodeWant.dredOffset)
			}
			if got := dred.LatentCount(); got != decodeWant.nbLatents {
				t.Fatalf("LatentCount()=%d want %d", got, decodeWant.nbLatents)
			}

			state := make([]float32, internaldred.StateDim)
			if n := dred.FillState(state); n != internaldred.StateDim {
				t.Fatalf("FillState count=%d want %d", n, internaldred.StateDim)
			}
			assertFloat32BitsEqual(t, state, decodeWant.state[:], "state")

			latents := make([]float32, internaldred.MaxLatents*internaldred.LatentStride)
			wantLatents := decodeWant.nbLatents * internaldred.LatentStride
			if n := dred.FillLatents(latents); n != wantLatents {
				t.Fatalf("FillLatents count=%d want %d", n, wantLatents)
			}
			assertFloat32BitsEqual(t, latents[:wantLatents], decodeWant.latents, "latents")
		})
	}
}

func assertFloat32BitsEqual(t *testing.T, got, want []float32, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s[%d]=%g want %g", label, i, got[i], want[i])
		}
	}
}
