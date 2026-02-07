//go:build cgo_libopus

package testvectors

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const updateLongFrameFixtureEnv = "GOPUS_UPDATE_LONGFRAME_FIXTURES"

// TestUpdateLongFrameLibopusReferenceFixture refreshes frozen reference packets
// used by TestLongFrameLibopusReferenceParityFromFixture.
func TestUpdateLongFrameLibopusReferenceFixture(t *testing.T) {
	if os.Getenv(updateLongFrameFixtureEnv) != "1" {
		t.Skipf("set %s=1 to update fixture", updateLongFrameFixtureEnv)
	}
	if !checkOpusdecAvailableEncoder() {
		t.Skip("opusdec not found in PATH")
	}

	out := longFrameFixtureFile{
		Version: 1,
		Cases:   make([]longFrameFixtureCase, 0, len(longFrameFixtureTargets())),
	}

	for _, tc := range longFrameFixtureTargets() {
		numFrames := 48000 / tc.FrameSize
		totalSamples := numFrames * tc.FrameSize * tc.Channels
		original := generateEncoderTestSignal(totalSamples, tc.Channels)

		packets := encodeWithLibopusComplianceReference(
			original,
			48000,
			tc.Channels,
			tc.Bitrate,
			tc.FrameSize,
			tc.Mode,
			tc.Bandwidth,
		)
		if len(packets) == 0 {
			t.Fatalf("libopus reference encode failed for %s", tc.Name)
		}

		libQ, err := computeComplianceQualityFromPackets(packets, original, tc.Channels, tc.FrameSize)
		if err != nil {
			t.Fatalf("compute fixture quality for %s: %v", tc.Name, err)
		}

		packetB64 := make([]string, len(packets))
		for i, pkt := range packets {
			packetB64[i] = base64.StdEncoding.EncodeToString(pkt)
		}

		out.Cases = append(out.Cases, longFrameFixtureCase{
			Name:      tc.Name,
			Mode:      fixtureModeName(tc.Mode),
			Bandwidth: fixtureBandwidthName(tc.Bandwidth),
			FrameSize: tc.FrameSize,
			Channels:  tc.Channels,
			Bitrate:   tc.Bitrate,
			LibQ:      libQ,
			Packets:   packetB64,
		})
		t.Logf("fixture: %s libQ=%.2f packets=%d", tc.Name, libQ, len(packets))
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	data = append(data, '\n')

	path := filepath.Join(longFrameFixturePath)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	t.Logf("updated fixture: %s", path)
}
