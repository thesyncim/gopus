package testvectors

import (
	"strings"
	"testing"
)

// Precision floors are case-specific lower bounds for (gopus SNR - libopus SNR) in dB.
// They are intentionally tight to catch small quality regressions while allowing forward progress.
// Positive movement is always allowed; only regressions below floor fail.
var encoderLibopusGapFloorDB = map[string]float64{
	"CELT-FB-20ms-mono-64k":     -0.90,
	"CELT-FB-20ms-stereo-128k":  -2.00,
	"CELT-FB-10ms-mono-64k":     0.80,
	"SILK-NB-10ms-mono-16k":     -0.50,
	"SILK-NB-20ms-mono-16k":     0.40,
	"SILK-NB-40ms-mono-16k":     0.20,
	"SILK-MB-20ms-mono-24k":     -0.30,
	"SILK-WB-10ms-mono-32k":     -0.10,
	"SILK-WB-20ms-mono-32k":     -0.45,
	"SILK-WB-40ms-mono-32k":     -0.30,
	"SILK-WB-60ms-mono-32k":     -0.30,
	"SILK-WB-20ms-stereo-48k":   -0.15,
	"Hybrid-SWB-10ms-mono-48k":  -0.35,
	"Hybrid-SWB-20ms-mono-48k":  -0.45,
	"Hybrid-SWB-40ms-mono-48k":  -0.45,
	"Hybrid-FB-10ms-mono-64k":   -0.30,
	"Hybrid-FB-20ms-mono-64k":   -0.35,
	"Hybrid-FB-60ms-mono-64k":   -0.35,
	"Hybrid-FB-20ms-stereo-96k": -0.30,
}

func TestEncoderCompliancePrecisionGuard(t *testing.T) {
	if !libopusComplianceReferenceAvailable() {
		t.Fatal("libopus reference fixture is required for precision guard")
	}

	for _, tc := range encoderComplianceSummaryCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			floor, ok := encoderLibopusGapFloorDB[tc.name]
			if !ok {
				t.Fatalf("missing precision floor for %q", tc.name)
			}

			q, _ := runEncoderComplianceTest(t, tc.mode, tc.bandwidth, tc.frameSize, tc.channels, tc.bitrate)
			libQ, _, ok := runLibopusComplianceReferenceTest(t, tc.mode, tc.bandwidth, tc.frameSize, tc.channels, tc.bitrate)
			if !ok {
				t.Fatalf("missing libopus reference for %q", tc.name)
			}

			snr := SNRFromQuality(q)
			libSNR := SNRFromQuality(libQ)
			gapDB := snr - libSNR
			if gapDB < floor {
				t.Fatalf("precision regression: gap=%.2f dB below floor %.2f dB (q=%.2f libQ=%.2f)", gapDB, floor, q, libQ)
			}
		})
	}
}

func TestEncoderCompliancePrecisionFloorCoverage(t *testing.T) {
	seen := make(map[string]struct{}, len(encoderComplianceSummaryCases()))
	for _, tc := range encoderComplianceSummaryCases() {
		seen[tc.name] = struct{}{}
		if _, ok := encoderLibopusGapFloorDB[tc.name]; !ok {
			t.Fatalf("missing precision floor for %q", tc.name)
		}
	}

	var extras []string
	for k := range encoderLibopusGapFloorDB {
		if _, ok := seen[k]; !ok {
			extras = append(extras, k)
		}
	}
	if len(extras) > 0 {
		t.Fatalf("unexpected precision floor entries: %s", strings.Join(extras, ", "))
	}

	if len(encoderLibopusGapFloorDB) != len(seen) {
		t.Fatalf("precision floor size mismatch: have %d want %d", len(encoderLibopusGapFloorDB), len(seen))
	}

	for name, floor := range encoderLibopusGapFloorDB {
		if floor > 2.0 {
			t.Fatalf("precision floor for %s is unrealistically strict: %.2f dB", name, floor)
		}
		if floor < -6.0 {
			t.Fatalf("precision floor for %s is too loose for precision mode: %.2f dB", name, floor)
		}
	}
}
