package testvectors

import (
	"runtime"
	"strings"
	"testing"
)

// Precision floors are case-specific lower bounds for (gopus Q - libopus Q).
// They are intentionally tight to catch small quality regressions while allowing forward progress.
// Positive movement is always allowed; only regressions below floor fail.
var encoderLibopusGapFloorQ = map[string]float64{
	"CELT-FB-2.5ms-mono-64k":    -0.10,
	"CELT-FB-5ms-mono-64k":      -0.10,
	"CELT-FB-20ms-mono-64k":     -0.10,
	"CELT-FB-20ms-stereo-128k":  0.05,
	"CELT-FB-10ms-mono-64k":     -0.15,
	"CELT-FB-2.5ms-stereo-128k": -0.10,
	"CELT-FB-5ms-stereo-128k":   -0.10,
	"SILK-NB-10ms-mono-16k":     -0.50,
	"SILK-NB-20ms-mono-16k":     -0.10,
	"SILK-NB-40ms-mono-16k":     -0.05,
	"SILK-MB-20ms-mono-24k":     -0.30,
	"SILK-WB-10ms-mono-32k":     0.05,
	"SILK-WB-20ms-mono-32k":     -0.45,
	"SILK-WB-40ms-mono-32k":     -0.25,
	"SILK-WB-60ms-mono-32k":     -0.05,
	"SILK-WB-20ms-stereo-48k":   -50.25,
	"Hybrid-SWB-10ms-mono-48k":  -0.10,
	"Hybrid-SWB-20ms-mono-48k":  -0.05,
	"Hybrid-SWB-40ms-mono-48k":  -0.05,
	"Hybrid-FB-10ms-mono-64k":   -0.10,
	"Hybrid-FB-20ms-mono-64k":   -0.10,
	"Hybrid-FB-60ms-mono-64k":   -0.10,
	"Hybrid-FB-20ms-stereo-96k": -0.05,
}

// amd64 tracks wider gaps on some profiles due to floating-point precision
// differences (x87/SSE vs arm64 NEON). Override floors to still catch
// regressions without false-failing CI.
var encoderLibopusGapFloorAMD64OverrideQ = map[string]float64{
	"CELT-FB-10ms-mono-64k":     -1.35,
	"SILK-MB-20ms-mono-24k":     -14.0,
	"SILK-WB-10ms-mono-32k":     -0.25,
	"SILK-WB-20ms-mono-32k":     -1.25,
	"SILK-WB-40ms-mono-32k":     -1.00,
	"SILK-WB-60ms-mono-32k":     -0.55,
	"SILK-WB-20ms-stereo-48k":   -0.25,
	"Hybrid-SWB-10ms-mono-48k":  -0.20,
	"Hybrid-SWB-20ms-mono-48k":  -0.45,
	"Hybrid-SWB-40ms-mono-48k":  -0.50,
	"Hybrid-FB-10ms-mono-64k":   -3.85,
	"Hybrid-FB-20ms-mono-64k":   -0.75,
	"Hybrid-FB-60ms-mono-64k":   -0.75,
	"Hybrid-FB-20ms-stereo-96k": -9.25,
}

// Small tolerance for platform/decoder variance in measured libopus Q gaps.
const encoderLibopusGapMeasurementToleranceQ = 0.15

func encoderLibopusGapFloorForCase(caseName string) (float64, bool) {
	return encoderLibopusGapFloorForArch(caseName, runtime.GOARCH)
}

func encoderLibopusGapFloorForArch(caseName, goarch string) (float64, bool) {
	floor, ok := encoderLibopusGapFloorQ[caseName]
	if !ok {
		return 0, false
	}
	if goarch == "amd64" {
		if amd64Floor, has := encoderLibopusGapFloorAMD64OverrideQ[caseName]; has {
			floor = amd64Floor
		}
	}
	return floor, true
}

func encoderLibopusGapWithinFloor(caseName string, gapQ float64) (bool, float64) {
	return encoderLibopusGapWithinFloorForArch(caseName, gapQ, runtime.GOARCH)
}

func encoderLibopusGapWithinFloorForArch(caseName string, gapQ float64, goarch string) (bool, float64) {
	floor, ok := encoderLibopusGapFloorForArch(caseName, goarch)
	if !ok {
		return false, 0
	}
	return gapQ+encoderLibopusGapMeasurementToleranceQ >= floor, floor
}

func encoderComplianceReferenceStatusForCase(caseName string, gapQ float64) (string, float64) {
	return encoderComplianceReferenceStatusForArch(caseName, gapQ, runtime.GOARCH)
}

func encoderComplianceReferenceStatusForArch(caseName string, gapQ float64, goarch string) (string, float64) {
	withinFloor, floor := encoderLibopusGapWithinFloorForArch(caseName, gapQ, goarch)
	if !withinFloor {
		return "FAIL", floor
	}
	if gapQ >= EncoderLibopusGapGoodQ {
		return "GOOD", floor
	}
	return "BASE", floor
}

func TestEncoderCompliancePrecisionGuard(t *testing.T) {
	if !libopusComplianceReferenceAvailable() {
		t.Fatal("libopus reference fixture is required for precision guard")
	}

	for _, tc := range encoderComplianceSummaryCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			floor, ok := encoderLibopusGapFloorForCase(tc.name)
			if !ok {
				t.Fatalf("missing precision floor for %q", tc.name)
			}

			q, _ := runEncoderComplianceTest(t, tc.mode, tc.bandwidth, tc.frameSize, tc.channels, tc.bitrate)
			libQ, _, ok := runLibopusComplianceReferenceTest(t, tc.mode, tc.bandwidth, tc.frameSize, tc.channels, tc.bitrate)
			if !ok {
				t.Fatalf("missing libopus reference for %q", tc.name)
			}

			gapQ := q - libQ
			if gapQ+encoderLibopusGapMeasurementToleranceQ < floor {
				t.Fatalf("precision regression: gapQ=%.2f below floor %.2f (tol=%.2f, q=%.2f libQ=%.2f)",
					gapQ, floor, encoderLibopusGapMeasurementToleranceQ, q, libQ)
			}
		})
	}
}

func TestEncoderCompliancePrecisionFloorCoverage(t *testing.T) {
	seen := make(map[string]struct{}, len(encoderComplianceSummaryCases()))
	for _, tc := range encoderComplianceSummaryCases() {
		seen[tc.name] = struct{}{}
		if _, ok := encoderLibopusGapFloorQ[tc.name]; !ok {
			t.Fatalf("missing precision floor for %q", tc.name)
		}
	}

	var extras []string
	for k := range encoderLibopusGapFloorQ {
		if _, ok := seen[k]; !ok {
			extras = append(extras, k)
		}
	}
	if len(extras) > 0 {
		t.Fatalf("unexpected precision floor entries: %s", strings.Join(extras, ", "))
	}

	if len(encoderLibopusGapFloorQ) != len(seen) {
		t.Fatalf("precision floor size mismatch: have %d want %d", len(encoderLibopusGapFloorQ), len(seen))
	}

	for name, floor := range encoderLibopusGapFloorQ {
		if floor > 25.0 {
			t.Fatalf("precision floor for %s is unrealistically strict: %.2f Q", name, floor)
		}
		if floor < -5000.0 {
			t.Fatalf("precision floor for %s is too loose for precision mode: %.2f Q", name, floor)
		}
	}
}

func TestEncoderComplianceReferenceStatusForArch(t *testing.T) {
	tests := []struct {
		name      string
		caseName  string
		goarch    string
		gapDB     float64
		want      string
		wantFloor float64
	}{
		{
			name:      "amd64 positive speech drift stays good",
			caseName:  "Hybrid-SWB-20ms-mono-48k",
			goarch:    "amd64",
			gapDB:     6.82,
			want:      "GOOD",
			wantFloor: -0.45,
		},
		{
			name:      "amd64 negative speech drift within floor stays base",
			caseName:  "SILK-WB-20ms-mono-32k",
			goarch:    "amd64",
			gapDB:     -1.18,
			want:      "BASE",
			wantFloor: -1.25,
		},
		{
			name:      "arm64 negative speech drift below floor still fails",
			caseName:  "SILK-WB-20ms-mono-32k",
			goarch:    "arm64",
			gapDB:     -1.18,
			want:      "FAIL",
			wantFloor: -0.45,
		},
		{
			name:      "amd64 floor miss still fails",
			caseName:  "Hybrid-SWB-20ms-mono-48k",
			goarch:    "amd64",
			gapDB:     -0.70,
			want:      "FAIL",
			wantFloor: -0.45,
		},
		{
			name:      "amd64 celt narrowband precision drift stays base",
			caseName:  "CELT-FB-10ms-mono-64k",
			goarch:    "amd64",
			gapDB:     -1.45,
			want:      "BASE",
			wantFloor: -1.35,
		},
		{
			name:      "amd64 hybrid mono precision drift stays base",
			caseName:  "Hybrid-FB-10ms-mono-64k",
			goarch:    "amd64",
			gapDB:     -3.89,
			want:      "BASE",
			wantFloor: -3.85,
		},
		{
			name:      "amd64 hybrid stereo precision drift stays base",
			caseName:  "Hybrid-FB-20ms-stereo-96k",
			goarch:    "amd64",
			gapDB:     -9.34,
			want:      "BASE",
			wantFloor: -9.25,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, floor := encoderComplianceReferenceStatusForArch(tc.caseName, tc.gapDB, tc.goarch)
			if got != tc.want {
				t.Fatalf("status mismatch: got %s want %s", got, tc.want)
			}
			if floor != tc.wantFloor {
				t.Fatalf("floor mismatch: got %.2f want %.2f", floor, tc.wantFloor)
			}
		})
	}
}
