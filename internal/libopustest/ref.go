package libopustest

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

// ScalarRefRequested reports whether the libopus reference oracles must link the
// scalar (generic-C, no SIMD/RTCD/intrinsics) libopus build instead of the
// default tree. The pure-Go (-tags purego) gopus build and the celt/custom parity
// gate set GOPUS_LIBOPUS_REF_SCALAR=1 so the C oracle compares like-with-like:
// pure-Go-scalar vs scalar-C. The default tree autotools-enables SIMD on amd64
// and Linux arm64, so comparing a SIMD-C oracle against scalar Go is not bit-exact
// and is the wrong reference for those lanes.
func ScalarRefRequested() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("GOPUS_LIBOPUS_REF_SCALAR")))
	return v == "1" || v == "true" || v == "yes"
}

// RefPath returns a path under the pinned libopus reference tree. When
// GOPUS_LIBOPUS_REF_SCALAR=1 it returns the scalar (generic-C) tree so the pure-Go
// build and custom parity gate link the bit-reproducible reference.
func RefPath(elem ...string) string {
	if ScalarRefRequested() {
		return ScalarRefPath(elem...)
	}
	base := []string{repoRoot(), "tmp_check", "opus-" + libopustooling.DefaultVersion}
	return filepath.Join(append(base, elem...)...)
}

// ScalarRefPath returns a path under the scalar (generic-C, built with
// --disable-asm --disable-rtcd --disable-intrinsics) libopus reference tree. Its
// config.h leaves the platform SIMD macros undefined, so C oracle helpers built
// against it exercise the bit-reproducible scalar kernels that match the pure-Go
// gopus build.
func ScalarRefPath(elem ...string) string {
	base := []string{repoRoot(), "tmp_check", "opus-" + libopustooling.DefaultVersion + "-scalar"}
	return filepath.Join(append(base, elem...)...)
}

// QEXTRefPath returns a path under the pinned QEXT-enabled libopus reference tree.
func QEXTRefPath(elem ...string) string {
	base := []string{repoRoot(), "tmp_check", "opus-" + libopustooling.DefaultVersion + "-qext"}
	return filepath.Join(append(base, elem...)...)
}

// FixedRefPath returns a path under the pinned fixed-point (--enable-fixed-point)
// libopus reference tree. Its config.h defines FIXED_POINT, so C oracle helpers
// built against it exercise the integer CELT/SILK kernels.
func FixedRefPath(elem ...string) string {
	base := []string{repoRoot(), "tmp_check", "opus-" + libopustooling.DefaultVersion + "-fixed"}
	return filepath.Join(append(base, elem...)...)
}

// SIMDRefPath returns a path under the SIMD/RTCD-enabled libopus PERFORMANCE
// reference tree (built by `make ensure-libopus-simd`). Its config.h DEFINES the
// platform SIMD macros (NEON on arm64, SSE/AVX RTCD on amd64), so it is NOT
// bit-reproducible and must never be used as a parity oracle — it exists only so
// the perf scoreboard can compare the gopus asm kernels against a SIMD libopus.
func SIMDRefPath(elem ...string) string {
	base := []string{repoRoot(), "tmp_check", "opus-" + libopustooling.DefaultVersion + "-simd"}
	return filepath.Join(append(base, elem...)...)
}

// CustomRefPath returns a path under the pinned custom-modes (--enable-custom-modes)
// libopus reference tree. Its config.h defines CUSTOM_MODES and the Opus Custom
// API, so C oracle helpers built against it can call opus_custom_mode_create /
// opus_custom_encoder_create / opus_custom_decoder_create. When
// GOPUS_LIBOPUS_REF_SCALAR=1 it returns the scalar custom tree so the celt/custom
// parity gate links the bit-reproducible reference.
func CustomRefPath(elem ...string) string {
	if ScalarRefRequested() {
		return CustomScalarRefPath(elem...)
	}
	base := []string{repoRoot(), "tmp_check", "opus-" + libopustooling.DefaultVersion + "-custom"}
	return filepath.Join(append(base, elem...)...)
}

// CustomScalarRefPath returns a path under the scalar custom-modes libopus
// reference tree (--enable-custom-modes on the generic-C kernels, built with
// --disable-asm --disable-rtcd --disable-intrinsics). It is the bit-reproducible
// Opus Custom oracle for the pure-Go celt/custom parity gate.
func CustomScalarRefPath(elem ...string) string {
	base := []string{repoRoot(), "tmp_check", "opus-" + libopustooling.DefaultVersion + "-custom-scalar"}
	return filepath.Join(append(base, elem...)...)
}

// ReadRefFileOrSkip reads a pinned libopus reference file. Missing references
// skip local tests unless GOPUS_STRICT_LIBOPUS_REF asks for hard failures.
func ReadRefFileOrSkip(t testing.TB, label string, elem ...string) []byte {
	t.Helper()
	return ReadRefPathOrSkip(t, RefPath(elem...), label)
}

// ReadRefPathOrSkip is the path-based form of ReadRefFileOrSkip.
func ReadRefPathOrSkip(t testing.TB, path, label string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err == nil {
		return data
	}
	if os.IsNotExist(err) && !StrictRefRequired() {
		t.Skipf("libopus %s reference unavailable: %v", label, err)
	}
	t.Fatalf("read libopus %s reference: %v", label, err)
	return nil
}

func StrictRefRequired() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("GOPUS_STRICT_LIBOPUS_REF")))
	return v == "1" || v == "true" || v == "yes"
}

func repoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
