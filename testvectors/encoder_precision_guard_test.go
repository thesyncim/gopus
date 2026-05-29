package testvectors

import (
	"runtime"
	"strings"
	"testing"
)

// Precision floors are case-specific lower bounds for (gopus Q - libopus Q).
// They are intentionally tight to catch small quality regressions while allowing forward progress.
// Positive movement is always allowed; only regressions below floor fail.
//
// These floors are arch- and OS-independent by construction: gopus is pure Go,
// so its encoder output is bit-identical for a given GOARCH regardless of GOOS,
// and the libopus reference Q comes from the same comparator decoding the same
// fixture packets. Measured gaps on darwin/arm64, the ubuntu arm64 native
// fixture, and linux/amd64 are all ~0.00 (worst case -0.02 on
// Hybrid-SWB-10ms-mono-48k; positive gaps up to +0.95 on amd64). The negative
// floors below carry ~0.20 of headroom under the worst measured gap, on top of
// the 0.15 measurement tolerance, to absorb cross-build libopus-decode drift;
// the few positive floors are forward-progress ratchets that still hold on both
// arches within tolerance.
var encoderLibopusGapFloorQ = map[string]float64{
	"CELT-FB-2.5ms-mono-64k":    -0.10,
	"CELT-FB-5ms-mono-64k":      -0.10,
	"CELT-FB-20ms-mono-64k":     -0.10,
	"CELT-FB-20ms-stereo-128k":  0.05,
	"CELT-FB-10ms-mono-64k":     -0.15,
	"CELT-FB-2.5ms-stereo-128k": -0.10,
	"CELT-FB-5ms-stereo-128k":   -0.10,
	"SILK-NB-10ms-mono-16k":     -0.20,
	"SILK-NB-20ms-mono-16k":     -0.10,
	"SILK-NB-40ms-mono-16k":     -0.05,
	"SILK-MB-20ms-mono-24k":     -0.20,
	"SILK-WB-10ms-mono-32k":     0.05,
	"SILK-WB-20ms-mono-32k":     -0.20,
	"SILK-WB-40ms-mono-32k":     -0.20,
	"SILK-WB-60ms-mono-32k":     -0.05,
	"SILK-WB-20ms-stereo-48k":   -0.20,
	"Hybrid-SWB-10ms-mono-48k":  -0.10,
	"Hybrid-SWB-20ms-mono-48k":  -0.05,
	"Hybrid-SWB-40ms-mono-48k":  -0.05,
	"Hybrid-FB-10ms-mono-64k":   -0.10,
	"Hybrid-FB-20ms-mono-64k":   -0.10,
	"Hybrid-FB-60ms-mono-64k":   -0.10,
	"Hybrid-FB-20ms-stereo-96k": -0.05,
}

// Small tolerance for platform/decoder variance in measured libopus Q gaps.
const encoderLibopusGapMeasurementToleranceQ = 0.15

func encoderLibopusGapFloorForCase(caseName string) (float64, bool) {
	return encoderLibopusGapFloorForPlatform(caseName, runtime.GOOS, runtime.GOARCH)
}

func encoderLibopusGapFloorForArch(caseName, goarch string) (float64, bool) {
	return encoderLibopusGapFloorForPlatform(caseName, "", goarch)
}

// encoderLibopusGapFloorForPlatform returns the precision floor for a case. The
// floor is platform-independent (gopus encode is bit-identical per GOARCH and
// the libopus reference Q comes from the same comparator); the goos/goarch
// parameters are kept for call-site clarity and forward extensibility.
func encoderLibopusGapFloorForPlatform(caseName, goos, goarch string) (float64, bool) {
	_ = goos
	_ = goarch
	floor, ok := encoderLibopusGapFloorQ[caseName]
	if !ok {
		return 0, false
	}
	return floor, true
}

func encoderLibopusGapWithinFloorForArch(caseName string, gapQ float64, goarch string) (bool, float64) {
	floor, ok := encoderLibopusGapFloorForArch(caseName, goarch)
	if !ok {
		return false, 0
	}
	return gapQ+encoderLibopusGapMeasurementToleranceQ >= floor, floor
}

func encoderLibopusGapWithinFloorForPlatform(caseName string, gapQ float64, goos, goarch string) (bool, float64) {
	floor, ok := encoderLibopusGapFloorForPlatform(caseName, goos, goarch)
	if !ok {
		return false, 0
	}
	return gapQ+encoderLibopusGapMeasurementToleranceQ >= floor, floor
}

func encoderComplianceReferenceStatusForCase(caseName string, gapQ float64) (string, float64) {
	return encoderComplianceReferenceStatusForPlatform(caseName, gapQ, runtime.GOOS, runtime.GOARCH)
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

func encoderComplianceReferenceStatusForPlatform(caseName string, gapQ float64, goos, goarch string) (string, float64) {
	withinFloor, floor := encoderLibopusGapWithinFloorForPlatform(caseName, gapQ, goos, goarch)
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
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
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
			wantFloor: -0.05,
		},
		{
			// amd64 and arm64 share the single floor table; no per-arch override.
			name:      "amd64 speech regression below floor fails",
			caseName:  "SILK-WB-20ms-mono-32k",
			goarch:    "amd64",
			gapDB:     -1.18,
			want:      "FAIL",
			wantFloor: -0.20,
		},
		{
			name:      "arm64 speech regression below floor fails",
			caseName:  "SILK-WB-20ms-mono-32k",
			goarch:    "arm64",
			gapDB:     -1.18,
			want:      "FAIL",
			wantFloor: -0.20,
		},
		{
			name:      "amd64 floor miss still fails",
			caseName:  "Hybrid-SWB-20ms-mono-48k",
			goarch:    "amd64",
			gapDB:     -0.70,
			want:      "FAIL",
			wantFloor: -0.05,
		},
		{
			// Within floor+tolerance: -0.28+0.15=-0.13 >= -0.15, and gap >= GOOD (-0.5).
			name:      "celt narrowband minor regression within tolerance stays good",
			caseName:  "CELT-FB-10ms-mono-64k",
			goarch:    "amd64",
			gapDB:     -0.28,
			want:      "GOOD",
			wantFloor: -0.15,
		},
		{
			name:      "celt stereo minor positive drift stays good",
			caseName:  "CELT-FB-20ms-stereo-128k",
			goarch:    "amd64",
			gapDB:     0.02,
			want:      "GOOD",
			wantFloor: 0.05,
		},
		{
			name:      "hybrid mono regression below floor fails",
			caseName:  "Hybrid-FB-10ms-mono-64k",
			goarch:    "amd64",
			gapDB:     -3.89,
			want:      "FAIL",
			wantFloor: -0.10,
		},
		{
			name:      "hybrid stereo regression below floor fails",
			caseName:  "Hybrid-FB-20ms-stereo-96k",
			goarch:    "amd64",
			gapDB:     -9.34,
			want:      "FAIL",
			wantFloor: -0.05,
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

func TestEncoderComplianceReferenceStatusForPlatform(t *testing.T) {
	tests := []struct {
		name      string
		caseName  string
		goos      string
		goarch    string
		gapDB     float64
		want      string
		wantFloor float64
	}{
		{
			// windows/amd64 uses the same platform-independent floor table; the
			// former -1.20 windows override was a stale mask (measured gap ~0.00).
			name:      "windows amd64 celt stereo regression below floor fails",
			caseName:  "CELT-FB-20ms-stereo-128k",
			goos:      "windows",
			goarch:    "amd64",
			gapDB:     -1.13,
			want:      "FAIL",
			wantFloor: 0.05,
		},
		{
			name:      "linux amd64 celt stereo regression below floor fails",
			caseName:  "CELT-FB-20ms-stereo-128k",
			goos:      "linux",
			goarch:    "amd64",
			gapDB:     -1.13,
			want:      "FAIL",
			wantFloor: 0.05,
		},
		{
			name:      "windows arm64 celt stereo keeps generic floor",
			caseName:  "CELT-FB-20ms-stereo-128k",
			goos:      "windows",
			goarch:    "arm64",
			gapDB:     -0.04,
			want:      "GOOD",
			wantFloor: 0.05,
		},
		{
			// linux/arm64 shares the platform-independent floor; the former
			// LinuxARM64 native-fixture overrides were stale masks. The ubuntu
			// arm64 native fixture measures the same ~0.00 gap as darwin/arm64.
			name:      "linux arm64 celt 10ms regression below floor fails",
			caseName:  "CELT-FB-10ms-mono-64k",
			goos:      "linux",
			goarch:    "arm64",
			gapDB:     -2.55,
			want:      "FAIL",
			wantFloor: -0.15,
		},
		{
			name:      "linux arm64 celt stereo regression below floor fails",
			caseName:  "CELT-FB-20ms-stereo-128k",
			goos:      "linux",
			goarch:    "arm64",
			gapDB:     -9.28,
			want:      "FAIL",
			wantFloor: 0.05,
		},
		{
			name:      "linux arm64 silk mb regression below floor fails",
			caseName:  "SILK-MB-20ms-mono-24k",
			goos:      "linux",
			goarch:    "arm64",
			gapDB:     -4.54,
			want:      "FAIL",
			wantFloor: -0.20,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, floor := encoderComplianceReferenceStatusForPlatform(tc.caseName, tc.gapDB, tc.goos, tc.goarch)
			if got != tc.want {
				t.Fatalf("status mismatch: got %s want %s", got, tc.want)
			}
			if floor != tc.wantFloor {
				t.Fatalf("floor mismatch: got %.2f want %.2f", floor, tc.wantFloor)
			}
		})
	}
}
