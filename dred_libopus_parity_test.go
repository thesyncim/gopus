//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import (
	"testing"

	internaldred "github.com/thesyncim/gopus/internal/dred"
)

func TestParsedDREDAvailabilityMatchesLibopus(t *testing.T) {
	base := makeValidCELTPacketForDREDTest(t)
	if len(base) < 2 {
		t.Fatal("base packet too short")
	}

	twoFramePacket := make([]byte, len(base)*2+16)
	n, err := buildPacketWithOptions(base[0]&0xFC, [][]byte{base[1:], base[1:]}, twoFramePacket, 0, false, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 1, Data: append([]byte{'D', internaldred.ExperimentalVersion}, makeExperimentalDREDPayloadBodyForTest(t, 8, -4)...)},
	}, false)
	if err != nil {
		t.Fatalf("buildPacketWithOptions error: %v", err)
	}
	twoFramePacket = twoFramePacket[:n]

	tests := []struct {
		name           string
		packet         []byte
		maxDREDSamples int
	}{
		{
			name: "single_frame_offset_positive",
			packet: buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
				{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, makeExperimentalDREDPayloadBodyForTest(t, 0, 4)...)},
			}),
			maxDREDSamples: 960,
		},
		{
			name:           "single_frame_offset_positive_large_request",
			packet:         buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, makeExperimentalDREDPayloadBodyForTest(t, 0, 4)...)}}),
			maxDREDSamples: 10080,
		},
		{
			name:           "second_frame_negative_offset",
			packet:         twoFramePacket,
			maxDREDSamples: 960,
		},
		{
			name: "first_dred_extension_invalid_second_valid",
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
			payload, frameOffset, ok, err := findDREDPayload(tc.packet)
			if err != nil {
				t.Fatalf("findDREDPayload error: %v", err)
			}
			if !ok {
				t.Fatal("findDREDPayload returned ok=false")
			}

			parsed, err := internaldred.ParsePayload(payload, frameOffset)
			if err != nil {
				t.Fatalf("ParsePayload error: %v", err)
			}
			got := parsed.Availability(tc.maxDREDSamples, 48000)

			want, err := probeLibopusDREDParse(tc.packet, tc.maxDREDSamples, 48000)
			if err != nil {
				t.Skipf("libopus dred helper unavailable: %v", err)
			}
			if want.availableSamples < 0 {
				t.Fatalf("libopus dred parse returned error %d", want.availableSamples)
			}

			if got.AvailableSamples != want.availableSamples {
				t.Fatalf("AvailableSamples=%d want %d", got.AvailableSamples, want.availableSamples)
			}
			if got.EndSamples != want.dredEndSamples {
				t.Fatalf("EndSamples=%d want %d", got.EndSamples, want.dredEndSamples)
			}

			span := internaldred.LatentSpanSamples(48000)
			if span <= 0 {
				t.Fatal("invalid latent span")
			}
			wantLatents := (want.availableSamples + got.OffsetSamples) / span
			if wantLatents < 0 {
				wantLatents = 0
			}
			if got.MaxLatents != wantLatents {
				t.Fatalf("MaxLatents=%d want %d", got.MaxLatents, wantLatents)
			}
		})
	}
}
