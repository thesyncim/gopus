//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package gopus

import (
	"testing"

	internaldred "github.com/thesyncim/gopus/internal/dred"
)

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
			if dred.RawProcessStage() != decodeWant.processStage {
				t.Fatalf("RawProcessStage()=%d want %d", dred.RawProcessStage(), decodeWant.processStage)
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
