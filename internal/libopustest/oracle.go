package libopustest

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

type CHelperConfig struct {
	Label        string
	OutputBase   string
	SourceFile   string
	ProbeRelPath string
	CFlags       []string
	RefIncludes  []string
	IncludeDirs  []string
	RefSources   []string
	Sources      []string
	Libs         []string
	LDFlags      []string
	DeadStrip    bool
}

// HelperCache caches a lazily built helper binary path for oracle tests.
type HelperCache struct {
	once sync.Once
	path string
	err  error
}

// Path returns the cached helper path, building it on the first call.
func (c *HelperCache) Path(build func() (string, error)) (string, error) {
	c.once.Do(func() {
		c.path, c.err = build()
	})
	if c.err != nil {
		return "", c.err
	}
	return c.path, nil
}

// CHelperPath returns the cached path for a C oracle helper built from cfg.
func (c *HelperCache) CHelperPath(cfg CHelperConfig) (string, error) {
	return c.Path(func() (string, error) {
		return BuildCHelper(cfg)
	})
}

func OracleEnabled() bool {
	if !StrictRefRequired() {
		switch strings.TrimSpace(strings.ToLower(os.Getenv("GOPUS_LIBOPUS_ORACLE"))) {
		case "0", "false", "off", "skip":
			return false
		}
	}
	if oracleBuildTagEnabled || StrictRefRequired() {
		return true
	}
	switch strings.TrimSpace(strings.ToLower(os.Getenv("GOPUS_TEST_TIER"))) {
	case "fast", "smoke":
		return false
	default:
		return true
	}
}

func RequireOracle(t testing.TB) {
	t.Helper()
	if !OracleEnabled() {
		t.Skip("libopus oracle disabled for this test tier")
	}
}

func HelperUnavailable(t testing.TB, label string, err error) {
	t.Helper()
	if StrictRefRequired() {
		t.Fatalf("libopus %s helper unavailable: %v", label, err)
	}
	t.Skipf("libopus %s helper unavailable: %v", label, err)
}

func BuildCHelper(cfg CHelperConfig) (string, error) {
	if cfg.Label == "" {
		cfg.Label = cfg.OutputBase
	}
	if cfg.OutputBase == "" || cfg.SourceFile == "" {
		return "", fmt.Errorf("helper output base and source file are required")
	}
	ccPath, err := libopustooling.FindCCompiler()
	if err != nil {
		return "", fmt.Errorf("cc not available: %w", err)
	}

	root := repoRoot()
	refDir := RefPath()
	probeRel := cfg.ProbeRelPath
	if probeRel == "" {
		probeRel = "config.h"
	}
	if _, err := os.Stat(filepath.Join(refDir, filepath.FromSlash(probeRel))); err != nil {
		libopustooling.EnsureLibopus(libopustooling.DefaultVersion, []string{root})
	}

	srcPath := cfg.SourceFile
	if !filepath.IsAbs(srcPath) {
		srcPath = filepath.Join(root, "tools", "csrc", filepath.FromSlash(srcPath))
	}
	if _, err := os.Stat(srcPath); err != nil {
		return "", fmt.Errorf("%s helper source not found: %w", cfg.Label, err)
	}

	outDir := filepath.Join(os.TempDir(), "gopus_libopus_test_helpers")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir helper dir: %w", err)
	}
	outPath := helperOutputPath(outDir, cfg.OutputBase, cfg.SourceFile, "ref")

	args := []string{"-std=c99", "-O2"}
	if cfg.DeadStrip {
		args = append(args, "-ffunction-sections", "-fdata-sections")
	}
	args = append(args, cfg.CFlags...)
	args = append(args, "-I", refDir, "-I", filepath.Join(refDir, "include"))
	for _, rel := range cfg.RefIncludes {
		args = append(args, "-I", filepath.Join(refDir, filepath.FromSlash(rel)))
	}
	for _, inc := range cfg.IncludeDirs {
		args = append(args, "-I", inc)
	}
	args = append(args, srcPath)
	for _, rel := range cfg.RefSources {
		args = append(args, filepath.Join(refDir, filepath.FromSlash(rel)))
	}
	args = append(args, cfg.Sources...)
	libs := cfg.Libs
	if len(libs) == 0 {
		libs = []string{"-lm"}
	}
	args = append(args, libs...)
	if cfg.DeadStrip {
		if runtime.GOOS == "darwin" {
			args = append(args, "-Wl,-dead_strip")
		} else {
			args = append(args, "-Wl,--gc-sections")
		}
	}
	args = append(args, cfg.LDFlags...)
	args = append(args, "-o", outPath)

	cmd := exec.Command(ccPath, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build %s helper: %w (%s)", cfg.Label, err, bytes.TrimSpace(output))
	}
	return outPath, nil
}

func helperOutputPath(dir, outputBase, sourceFile, flavor string) string {
	return helperOutputPathForGOOS(dir, outputBase, sourceFile, flavor, runtime.GOOS, runtime.GOARCH)
}

func helperOutputPathForGOOS(dir, outputBase, sourceFile, flavor, goos, goarch string) string {
	stem := strings.TrimSuffix(filepath.Base(filepath.FromSlash(sourceFile)), filepath.Ext(sourceFile))
	base := fmt.Sprintf("%s_%s_%s_%s_%s", outputBase, stem, flavor, goos, goarch)
	if goos == "windows" {
		base += ".exe"
	}
	return filepath.Join(dir, base)
}

func RunHelper(binPath string, input []byte) ([]byte, error) {
	return RunHelperEnv(binPath, input, nil)
}

func RunHelperArgs(binPath string, input []byte, args ...string) ([]byte, error) {
	return runHelper(binPath, input, nil, args)
}

func RunHelperEnv(binPath string, input []byte, env []string) ([]byte, error) {
	return runHelper(binPath, input, env, nil)
}

func RunHelperArgsEnv(binPath string, input []byte, env []string, args ...string) ([]byte, error) {
	return runHelper(binPath, input, env, args)
}

func runHelper(binPath string, input []byte, env []string, args []string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(binPath, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return stdout.Bytes(), nil
}
