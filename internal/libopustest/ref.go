package libopustest

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

// RefPath returns a path under the tree selected by the build-aware paired
// reference resolver. Invalid or conflicting overrides panic with their cause;
// helper constructors that return errors resolve the variant directly.
func RefPath(elem ...string) string {
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		panic(err)
	}
	suffix, err := libopustooling.LibopusReferenceSourceSuffix(variant)
	if err != nil {
		panic(err)
	}
	base := []string{repoRoot(), "tmp_check", "opus-" + libopustooling.DefaultVersion + suffix}
	return filepath.Join(append(base, elem...)...)
}

// ScalarRefPath returns a path under the scalar generic-C reference tree,
// built without assembly, RTCD, intrinsics, or compiler loop/SLP vectorization.
// FMA contraction stays enabled to match scalar Go arithmetic.
func ScalarRefPath(elem ...string) string {
	base := []string{repoRoot(), "tmp_check", "opus-" + libopustooling.DefaultVersion + "-scalar"}
	return filepath.Join(append(base, elem...)...)
}

// QEXTRefPath returns a path under the QEXT-enabled reference tree paired with
// the current Go instruction lane.
func QEXTRefPath(elem ...string) string {
	variant, err := libopustooling.ResolveLibopusQEXTReferenceVariant()
	if err != nil {
		panic(err)
	}
	suffix, err := libopustooling.LibopusReferenceSourceSuffix(variant)
	if err != nil {
		panic(err)
	}
	base := []string{repoRoot(), "tmp_check", "opus-" + libopustooling.DefaultVersion + suffix}
	return filepath.Join(append(base, elem...)...)
}

// FixedRefPath returns a FIXED_POINT tree paired with the current Go build.
func FixedRefPath(elem ...string) string {
	variant, err := libopustooling.ResolveLibopusFixedReferenceVariant()
	if err != nil {
		panic(err)
	}
	suffix, err := libopustooling.LibopusReferenceSourceSuffix(variant)
	if err != nil {
		panic(err)
	}
	base := []string{repoRoot(), "tmp_check", "opus-" + libopustooling.DefaultVersion + suffix}
	return filepath.Join(append(base, elem...)...)
}

// FixedQEXTRefPath returns a FIXED_POINT + ENABLE_QEXT tree paired with the
// current Go build's scalar or SIMD instruction lane.
func FixedQEXTRefPath(elem ...string) string {
	variant, err := libopustooling.ResolveLibopusFixedQEXTReferenceVariant()
	if err != nil {
		panic(err)
	}
	suffix, err := libopustooling.LibopusReferenceSourceSuffix(variant)
	if err != nil {
		panic(err)
	}
	base := []string{repoRoot(), "tmp_check", "opus-" + libopustooling.DefaultVersion + suffix}
	return filepath.Join(append(base, elem...)...)
}

// DREDQEXTRefPath returns the ENABLE_DRED + ENABLE_DEEP_PLC + ENABLE_QEXT
// reference tree paired with the current Go scalar or SIMD instruction lane.
func DREDQEXTRefPath(elem ...string) string {
	variant, err := resolveDREDQEXTReferenceVariantForCurrentBuild()
	if err != nil {
		panic(err)
	}
	suffix, err := libopustooling.LibopusReferenceSourceSuffix(variant)
	if err != nil {
		panic(err)
	}
	base := []string{repoRoot(), "tmp_check", "opus-" + libopustooling.DefaultVersion + suffix}
	return filepath.Join(append(base, elem...)...)
}

// SIMDRefPath returns a path under the SIMD/RTCD-enabled libopus reference tree
// (built by `make ensure-libopus-simd`). Pair it with a Go SIMD build for
// build-wide comparisons, or use it for a kernel test that invokes the matching
// Go SIMD function directly.
func SIMDRefPath(elem ...string) string {
	base := []string{repoRoot(), "tmp_check", "opus-" + libopustooling.DefaultVersion + "-simd"}
	return filepath.Join(append(base, elem...)...)
}

// CustomRefPath returns a path under the pinned custom-modes (--enable-custom-modes)
// libopus reference tree. Its config.h defines CUSTOM_MODES and the Opus Custom
// API, so C oracle helpers built against it can call opus_custom_mode_create /
// opus_custom_encoder_create / opus_custom_decoder_create. Scalar Go builds use
// the matching custom-scalar tree.
func CustomRefPath(elem ...string) string {
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		panic(err)
	}
	if variant == libopustooling.LibopusReferenceScalar {
		return CustomScalarRefPath(elem...)
	}
	base := []string{repoRoot(), "tmp_check", "opus-" + libopustooling.DefaultVersion + "-custom"}
	return filepath.Join(append(base, elem...)...)
}

// CustomScalarRefPath returns a path under the scalar custom-modes libopus
// reference tree (--enable-custom-modes on the generic-C kernels, built with
// --disable-asm --disable-rtcd --disable-intrinsics). It is the bit-reproducible
// Opus Custom oracle for the scalar Go celt/custom parity gate.
func CustomScalarRefPath(elem ...string) string {
	base := []string{repoRoot(), "tmp_check", "opus-" + libopustooling.DefaultVersion + "-custom-scalar"}
	return filepath.Join(append(base, elem...)...)
}

// CustomQEXTRefPath returns the CUSTOM_MODES + ENABLE_QEXT reference tree
// paired with the current Go instruction lane.
func CustomQEXTRefPath(elem ...string) string {
	variant, err := libopustooling.ResolveLibopusCustomQEXTReferenceVariant()
	if err != nil {
		panic(err)
	}
	suffix, err := libopustooling.LibopusReferenceSourceSuffix(variant)
	if err != nil {
		panic(err)
	}
	base := []string{repoRoot(), "tmp_check", "opus-" + libopustooling.DefaultVersion + suffix}
	return filepath.Join(append(base, elem...)...)
}

// CustomFixedRefPath returns the CUSTOM_MODES + FIXED_POINT reference tree
// paired with the current Go instruction lane.
func CustomFixedRefPath(elem ...string) string {
	variant, err := libopustooling.ResolveLibopusCustomFixedReferenceVariant()
	if err != nil {
		panic(err)
	}
	suffix, err := libopustooling.LibopusReferenceSourceSuffix(variant)
	if err != nil {
		panic(err)
	}
	base := []string{repoRoot(), "tmp_check", "opus-" + libopustooling.DefaultVersion + suffix}
	return filepath.Join(append(base, elem...)...)
}

// CustomFixedQEXTRefPath returns the CUSTOM_MODES + FIXED_POINT + ENABLE_QEXT
// reference tree paired with the current Go instruction lane.
func CustomFixedQEXTRefPath(elem ...string) string {
	variant, err := libopustooling.ResolveLibopusCustomFixedQEXTReferenceVariant()
	if err != nil {
		panic(err)
	}
	suffix, err := libopustooling.LibopusReferenceSourceSuffix(variant)
	if err != nil {
		panic(err)
	}
	base := []string{repoRoot(), "tmp_check", "opus-" + libopustooling.DefaultVersion + suffix}
	return filepath.Join(append(base, elem...)...)
}

// ReadRefFileOrSkip reads a pinned libopus reference file. Missing references
// skip local tests unless GOPUS_STRICT_LIBOPUS_REF asks for hard failures.
func ReadRefFileOrSkip(t testing.TB, label string, elem ...string) []byte {
	t.Helper()
	return ReadRefPathOrSkip(t, RefPath(elem...), label)
}

// ReadPinnedSourceFileOrSkip reads from the unsuffixed pinned source checkout.
// It is for source-only checks that do not consume build-specific artifacts.
func ReadPinnedSourceFileOrSkip(t testing.TB, label string, elem ...string) []byte {
	t.Helper()
	return readPinnedSourceFileOrSkip(t, repoRoot(), label, elem...)
}

func readPinnedSourceFileOrSkip(t testing.TB, root, label string, elem ...string) []byte {
	t.Helper()
	return ReadRefPathOrSkip(t, pinnedSourcePath(root, elem...), label)
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

func pinnedSourcePath(root string, elem ...string) string {
	base := []string{root, "tmp_check", "opus-" + libopustooling.DefaultVersion}
	return filepath.Join(append(base, elem...)...)
}
