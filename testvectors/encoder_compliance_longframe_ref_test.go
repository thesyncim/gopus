package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

// TestHybridLongFrameLibopusReferenceParity ensures libopus reference encoding
// for 40/60ms hybrid packets stays aligned with the gopus packetization model.
// This guards against accidental reintroduction of single-frame 40/60ms
// reference packets, which cause large artificial compliance gaps.
func TestHybridLongFrameLibopusReferenceParity(t *testing.T) {
	if !checkOpusdecAvailableEncoder() {
		t.Skip("opusdec not found in PATH")
	}

	cases := []struct {
		name      string
		bandwidth types.Bandwidth
		frameSize int
		bitrate   int
	}{
		{"Hybrid-SWB-40ms-mono-48k", types.BandwidthSuperwideband, 1920, 48000},
		{"Hybrid-FB-60ms-mono-64k", types.BandwidthFullband, 2880, 64000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, _ := runEncoderComplianceTest(t, encoder.ModeHybrid, tc.bandwidth, tc.frameSize, 1, tc.bitrate)
			libQ, _, ok := runLibopusComplianceReferenceTest(t, encoder.ModeHybrid, tc.bandwidth, tc.frameSize, 1, tc.bitrate)
			if !ok {
				t.Skip("libopus compliance reference unavailable")
			}

			snr := SNRFromQuality(q)
			libSNR := SNRFromQuality(libQ)
			gapDB := snr - libSNR
			if math.Abs(gapDB) > EncoderLibopusSpeechGapTightDB {
				t.Fatalf("hybrid long-frame libopus gap regressed: gap=%.2f dB (q=%.2f libQ=%.2f)", gapDB, q, libQ)
			}
		})
	}
}
