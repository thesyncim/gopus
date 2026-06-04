//go:build gopus_fixedpoint

package silk

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

// fixed_oracle_helper_test.go centralizes the FIXED_POINT libopus oracle build
// shared by every internal/silk *_fixedpoint_libopus_parity_test.go file. Each
// SILK fixed-point kernel has a matching tools/csrc/libopus_silk_*_info.c probe
// that is compiled once and linked against the pinned --enable-fixed-point
// libopus reference tree (built on demand via tools/ensure_libopus.sh). The
// per-kernel test files supply only their probe source name plus a unique
// binary slug and decode the oracle's wire output themselves.

var (
	fixedOracleMu   sync.Mutex
	fixedOracleBins = map[string]fixedOracleResult{}
)

type fixedOracleResult struct {
	bin string
	err error
}

// buildFixedSILKOracle compiles tools/csrc/<srcName> against the FIXED_POINT
// libopus reference and returns the cached helper binary path. srcName is the
// probe source basename (for example "libopus_silk_fixed_schur_info.c") and
// binSlug is a filesystem-safe name for the produced binary (for example
// "schur"); results are memoized per binSlug so each probe is built at most
// once per test process.
func buildFixedSILKOracle(srcName, binSlug string) (string, error) {
	fixedOracleMu.Lock()
	defer fixedOracleMu.Unlock()
	if got, ok := fixedOracleBins[binSlug]; ok {
		return got.bin, got.err
	}
	bin, err := compileFixedSILKOracle(srcName, binSlug)
	fixedOracleBins[binSlug] = fixedOracleResult{bin: bin, err: err}
	return bin, err
}

func compileFixedSILKOracle(srcName, binSlug string) (string, error) {
	_, file, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))

	refDir := fixedRefPath()
	staticLib := fixedRefPath(".libs", "libopus.a")
	if _, err := os.Stat(staticLib); err != nil {
		cmd := exec.Command("bash", filepath.Join("tools", "ensure_libopus.sh"))
		cmd.Dir = repoRoot
		cmd.Env = append(os.Environ(), "LIBOPUS_ENABLE_FIXED=1")
		if out, berr := cmd.CombinedOutput(); berr != nil {
			return "", fmt.Errorf("ensure fixed libopus: %w (%s)", berr, out)
		}
	}
	if _, err := os.Stat(staticLib); err != nil {
		return "", fmt.Errorf("fixed libopus static lib missing: %w", err)
	}

	cc, err := libopustooling.FindCCompiler()
	if err != nil {
		return "", err
	}

	src := filepath.Join(repoRoot, "tools", "csrc", srcName)
	outDir := filepath.Join(os.TempDir(), "gopus_libopus_test_helpers")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(outDir, fmt.Sprintf("gopus_silk_fixed_%s_%s_%s", binSlug, runtime.GOOS, runtime.GOARCH))

	args := []string{
		"-std=c99", "-O2", "-DHAVE_CONFIG_H",
		"-I", refDir,
		"-I", filepath.Join(refDir, "include"),
		"-I", filepath.Join(refDir, "celt"),
		"-I", filepath.Join(refDir, "silk"),
		"-I", filepath.Join(refDir, "silk", "fixed"),
		src, staticLib, "-lm",
		"-o", out,
	}
	cmd := exec.Command(cc, args...)
	if combined, cerr := cmd.CombinedOutput(); cerr != nil {
		return "", fmt.Errorf("build silk fixed %s helper: %w (%s)", binSlug, cerr, combined)
	}
	return out, nil
}
