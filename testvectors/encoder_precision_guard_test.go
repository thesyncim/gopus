package testvectors

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

// Precision floors are case-specific lower bounds for (gopus Q - libopus Q).
// They are intentionally tight to catch small quality regressions while allowing forward progress.
// Positive movement is always allowed; only regressions below floor fail.
//
// Every precision guard run compares gopus with a fresh libopus score produced
// by the validated reference variant selected for the active Go build. A
// successful live comparison enforces this single per-case floor table; stored
// fixture provenance does not control whether the floor runs. These floors
// allow small real-content quality variation without introducing broad per-arch
// budgets for synthetic packet-parity cases.
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
// floor is platform-independent: the comparison always uses a native same-arch
// libopus reference, so a single tight floor table holds on every arch.
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

func precisionGapStatusForCase(caseName string, gopusQ, libopusQ float64) (gapQ float64, status string, floor float64) {
	gapQ = gopusQ - libopusQ
	status, floor = encoderComplianceReferenceStatusForCase(caseName, gapQ)
	return gapQ, status, floor
}

func precisionReferenceErrorRequiresFatal(err error, strict bool) bool {
	if err == nil {
		return false
	}
	var configErr *libopustooling.LibopusReferenceConfigError
	return strict || errors.As(err, &configErr)
}

func TestEncoderCompliancePrecisionGuard(t *testing.T) {
	t.Parallel()

	for _, tc := range encoderComplianceSummaryCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			floor, ok := encoderLibopusGapFloorForCase(tc.name)
			if !ok {
				t.Fatalf("missing precision floor for %q", tc.name)
			}

			// Both Q values use the same real-content PCM and the validated libopus
			// variant selected for this Go build. Every successful live comparison
			// enforces the existing floor, independent of fixture metadata.
			q := runRealContentPrecisionGopus(t, tc.mode, tc.bandwidth, tc.frameSize, tc.channels, tc.bitrate)
			libQ, refOK := runRealContentPrecisionLibopusReference(t, tc.mode, tc.bandwidth, tc.frameSize, tc.channels, tc.bitrate)
			if !refOK {
				t.Logf("paired real-content libopus reference unavailable for %s (gopus Q=%.2f); optional gap guard unavailable", tc.name, q)
				return
			}
			t.Logf("RealContent libopus Q=%.2f (source=%s)", libQ, precisionGuardSignalName)

			gapQ, status, floor := precisionGapStatusForCase(tc.name, q, libQ)
			if gapQ+encoderLibopusGapMeasurementToleranceQ < floor {
				t.Fatalf("precision regression: status=%s gapQ=%.2f below floor %.2f (tol=%.2f, q=%.2f libQ=%.2f, source=%s)", status,
					gapQ, floor, encoderLibopusGapMeasurementToleranceQ, q, libQ, precisionGuardSignalName)
			}
			t.Logf("precision floor PASS: gapQ=%.2f floor=%.2f tol=%.2f status=%s", gapQ, floor, encoderLibopusGapMeasurementToleranceQ, status)
		})
	}
}

func TestPrecisionFloorAppliesWithoutFixtureMetadata(t *testing.T) {
	const caseName = "CELT-FB-5ms-mono-64k"
	for _, metadata := range []struct {
		name  string
		value string
	}{
		{name: "absent"},
		{name: "stale", value: "stale"},
	} {
		t.Run(metadata.name, func(t *testing.T) {
			t.Setenv(requirePlatformFixturesEnv, metadata.value)
			if metadata.value == "" && nativeLibopusComplianceReferenceAvailable() {
				t.Fatal("native reference unexpectedly available without platform fixture metadata")
			}
			gapQ, status, floor := precisionGapStatusForCase(caseName, -1.0, 0)
			if status != "FAIL" || gapQ+encoderLibopusGapMeasurementToleranceQ >= floor {
				t.Fatalf("live paired score must fail below-floor regardless of %s fixture metadata: gap=%.2f status=%s floor=%.2f", metadata.name, gapQ, status, floor)
			}
		})
	}
}

func TestPrecisionReferenceErrorCannotSkipInStrictMode(t *testing.T) {
	missingHelperErr := errors.New("paired opus_demo unavailable")
	configErr := &libopustooling.LibopusReferenceConfigError{Err: errors.New("conflicting paired variant")}

	t.Setenv("GOPUS_STRICT_LIBOPUS_REF", "")
	if precisionReferenceErrorRequiresFatal(missingHelperErr, strictLibopusReferenceRequired()) {
		t.Fatal("ordinary optional reference absence should remain eligible for the documented fallback")
	}
	if !precisionReferenceErrorRequiresFatal(configErr, strictLibopusReferenceRequired()) {
		t.Fatal("invalid reference configuration must fail even outside strict mode")
	}

	t.Setenv("GOPUS_STRICT_LIBOPUS_REF", "1")
	if !precisionReferenceErrorRequiresFatal(missingHelperErr, strictLibopusReferenceRequired()) {
		t.Fatal("strict helper absence must fail instead of skipping the precision floor")
	}
}

func TestEncoderCompliancePrecisionFloorCoverage(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
			// amd64 and arm64 share the single tight floor table; no per-arch budget.
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
			// CELT narrowband mono holds the tight base floor on amd64 too: with a
			// native amd64 libopus reference the gap is ~0.00, so a -0.33 regression
			// fails the tight floor (no per-arch budget masks it).
			name:      "celt narrowband amd64 minor regression fails tight floor",
			caseName:  "CELT-FB-10ms-mono-64k",
			goarch:    "amd64",
			gapDB:     -0.33,
			want:      "FAIL",
			wantFloor: -0.15,
		},
		{
			// Same case on arm64 holds the same tight base floor.
			name:      "celt narrowband arm64 minor regression fails tight floor",
			caseName:  "CELT-FB-10ms-mono-64k",
			goarch:    "arm64",
			gapDB:     -0.33,
			want:      "FAIL",
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
			// Hybrid mono holds the tight floor on amd64; a real regression fails.
			name:      "hybrid mono amd64 regression fails tight floor",
			caseName:  "Hybrid-FB-10ms-mono-64k",
			goarch:    "amd64",
			gapDB:     -4.20,
			want:      "FAIL",
			wantFloor: -0.10,
		},
		{
			// A small -0.33 amd64 gap now fails the tight floor (no -3.85 budget).
			name:      "hybrid mono amd64 minor regression fails tight floor",
			caseName:  "Hybrid-FB-10ms-mono-64k",
			goarch:    "amd64",
			gapDB:     -0.33,
			want:      "FAIL",
			wantFloor: -0.10,
		},
		{
			// Hybrid fullband stereo holds the tight floor on amd64; the old -9.25
			// budget is gone, so a multi-dB regression fails.
			name:      "hybrid stereo amd64 regression fails tight floor",
			caseName:  "Hybrid-FB-20ms-stereo-96k",
			goarch:    "amd64",
			gapDB:     -9.60,
			want:      "FAIL",
			wantFloor: -0.05,
		},
		{
			// The former ~-8.09 amd64 gap was cross-toolchain variance, not a native
			// same-arch gap. Against a native amd64 reference it collapses to ~0.00;
			// were such a gap ever seen again it now fails the tight floor.
			name:      "hybrid stereo amd64 cross-toolchain gap fails tight floor",
			caseName:  "Hybrid-FB-20ms-stereo-96k",
			goarch:    "amd64",
			gapDB:     -8.09,
			want:      "FAIL",
			wantFloor: -0.05,
		},
	}

	for _, tc := range tests {
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
	t.Parallel()
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
			// All platforms share one tight, platform-independent floor table.
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
			// With a native gcc-NEON reference the gap is ~0.00, so a -2.55
			// regression fails the tight floor (no per-arch budget masks it).
			name:      "linux arm64 celt 10ms regression below floor fails",
			caseName:  "CELT-FB-10ms-mono-64k",
			goos:      "linux",
			goarch:    "arm64",
			gapDB:     -2.55,
			want:      "FAIL",
			wantFloor: -0.15,
		},
		{
			// CELT fullband stereo on linux/arm64 holds the tight floor: the old
			// -9.45 gcc-NEON-vs-amd64 cross-toolchain budget is gone. Against a
			// native gcc-NEON reference the gap is ~0.00, so a multi-dB gap fails.
			name:      "linux arm64 celt stereo cross-toolchain gap fails tight floor",
			caseName:  "CELT-FB-20ms-stereo-128k",
			goos:      "linux",
			goarch:    "arm64",
			gapDB:     -9.28,
			want:      "FAIL",
			wantFloor: 0.05,
		},
		{
			// The same case on darwin/arm64 (Apple-NEON reference) holds the same
			// tight floor; darwin's native gap is proven ~0.00.
			name:      "darwin arm64 celt stereo keeps tight floor",
			caseName:  "CELT-FB-20ms-stereo-128k",
			goos:      "darwin",
			goarch:    "arm64",
			gapDB:     -9.28,
			want:      "FAIL",
			wantFloor: 0.05,
		},
		{
			// Short-frame CELT stereo holds the tight floor on linux/arm64 too; the
			// old -0.65 budget is gone.
			name:      "linux arm64 celt short stereo regression fails tight floor",
			caseName:  "CELT-FB-5ms-stereo-128k",
			goos:      "linux",
			goarch:    "arm64",
			gapDB:     -0.43,
			want:      "FAIL",
			wantFloor: -0.10,
		},
		{
			// SILK mediumband on linux/arm64 holds the tight floor; the old -4.75
			// budget is gone, so a multi-dB gap fails.
			name:      "linux arm64 silk mb cross-toolchain gap fails tight floor",
			caseName:  "SILK-MB-20ms-mono-24k",
			goos:      "linux",
			goarch:    "arm64",
			gapDB:     -4.54,
			want:      "FAIL",
			wantFloor: -0.20,
		},
		{
			// A SILK-MB gap below the tight floor still fails.
			name:      "linux arm64 silk mb regression below floor fails",
			caseName:  "SILK-MB-20ms-mono-24k",
			goos:      "linux",
			goarch:    "arm64",
			gapDB:     -5.20,
			want:      "FAIL",
			wantFloor: -0.20,
		},
	}

	for _, tc := range tests {
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
