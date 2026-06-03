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
// This is a SINGLE tight, platform-independent floor table. It is valid because
// the precision guard always compares gopus against a NATIVE
// same-arch-and-toolchain libopus reference: each CI runner regenerates its
// libopus fixture from the libopus it just built (fixtures-gen-platform), so the
// reference matches the runtime arch/toolchain. gopus has one portable float
// path that is <=1-ULP-correct against that native libopus, so the gap is ~0.00
// on every arch (darwin/arm64 Apple-NEON, linux/arm64 gcc-NEON, linux/amd64
// SSE/AVX), and a single tight floor holds per case on every arch.
//
// There are intentionally NO large per-arch "budgets": a multi-dB gap only
// arises when the reference libopus was built for a DIFFERENT arch/toolchain
// than the runtime, because libopus's own SIMD float order then diverges from
// gopus's by libopus's cross-toolchain self-variance on the am_multisine CELT
// knife-edge. A same-arch reference removes that, leaving only <=1-ULP drift.
// If a genuine same-arch SIMD-order knife-edge residual ever appears on a runner
// (gcc-NEON or amd64-SSE flipping a band decision vs gopus's order), add a
// MINIMAL, individually documented per-case residual here -- never a blanket
// multi-dB budget.
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

func TestEncoderCompliancePrecisionGuard(t *testing.T) {
	t.Parallel()
	// The tight gap floors are only fair against a native same-arch libopus
	// reference. Jobs that regenerate the platform fixture on the runner enforce
	// the gap; other jobs log it for visibility but do not gate.
	native := nativeLibopusComplianceReferenceAvailable()

	for _, tc := range encoderComplianceSummaryCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			floor, ok := encoderLibopusGapFloorForCase(tc.name)
			if !ok {
				t.Fatalf("missing precision floor for %q", tc.name)
			}

			// Both Q values are measured on the real-content source: gopus encodes
			// it, and the native same-arch libopus opus_demo encodes the identical
			// samples. On real audio the cross-toolchain float-order spread is
			// negligible, so the tight base floor holds on every arch.
			q := runRealContentPrecisionGopus(t, tc.mode, tc.bandwidth, tc.frameSize, tc.channels, tc.bitrate)
			libQ, refOK := runRealContentPrecisionLibopusReference(t, tc.mode, tc.bandwidth, tc.frameSize, tc.channels, tc.bitrate)
			if !refOK {
				if native {
					t.Fatalf("native real-content libopus reference unavailable for %q", tc.name)
				}
				t.Logf("real-content libopus reference unavailable for %s (gopus Q=%.2f); gap guard skipped", tc.name, q)
				return
			}

			gapQ := q - libQ
			if !native {
				t.Logf("non-native lane: %s gapQ=%.2f floor=%.2f (gap guard skipped; native jobs enforce)", tc.name, gapQ, floor)
				return
			}
			if gapQ+encoderLibopusGapMeasurementToleranceQ < floor {
				t.Fatalf("precision regression: gapQ=%.2f below floor %.2f (tol=%.2f, q=%.2f libQ=%.2f, source=%s)",
					gapQ, floor, encoderLibopusGapMeasurementToleranceQ, q, libQ, precisionGuardSignalName)
			}
		})
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
