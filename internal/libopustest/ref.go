package libopustest

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

// RefPath returns a path under the pinned libopus reference tree.
func RefPath(elem ...string) string {
	base := []string{repoRoot(), "tmp_check", "opus-" + libopustooling.DefaultVersion}
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

// CustomRefPath returns a path under the pinned custom-modes (--enable-custom-modes)
// libopus reference tree. Its config.h defines CUSTOM_MODES and the Opus Custom
// API, so C oracle helpers built against it can call opus_custom_mode_create /
// opus_custom_encoder_create / opus_custom_decoder_create.
func CustomRefPath(elem ...string) string {
	base := []string{repoRoot(), "tmp_check", "opus-" + libopustooling.DefaultVersion + "-custom"}
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
