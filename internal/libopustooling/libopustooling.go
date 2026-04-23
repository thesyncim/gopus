package libopustooling

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	// DefaultVersion is the pinned libopus reference used by fixture tooling.
	DefaultVersion = "1.6.1"
)

// DefaultSearchRoots covers common invocation locations:
// repository root, package subdirs (e.g. testvectors), and deeper test runs.
func DefaultSearchRoots() []string {
	return []string{".", "..", "../.."}
}

func findLibopusTool(version string, roots []string, tool string) (string, bool) {
	return findLibopusToolForOS(version, roots, tool, runtime.GOOS)
}

func findLibopusToolForOS(version string, roots []string, tool, goos string) (string, bool) {
	if version == "" {
		version = DefaultVersion
	}
	if len(roots) == 0 {
		roots = DefaultSearchRoots()
	}

	seen := make(map[string]struct{}, len(roots)*2)
	for _, root := range roots {
		for _, candidate := range libopusToolCandidates(tool, goos) {
			p := filepath.Clean(filepath.Join(root, "tmp_check", "opus-"+version, candidate))
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			if st, err := os.Stat(p); err == nil && libopusToolIsRunnable(st, goos) {
				return p, true
			}
		}
	}
	return "", false
}

func libopusToolCandidates(tool, goos string) []string {
	if goos == "windows" && !strings.HasSuffix(strings.ToLower(tool), ".exe") {
		return []string{tool + ".exe", tool}
	}
	return []string{tool}
}

func libopusToolIsRunnable(st os.FileInfo, goos string) bool {
	if st.IsDir() {
		return false
	}
	if goos == "windows" {
		return true
	}
	return (st.Mode() & 0o111) != 0
}

// FindOpusDemo returns the first executable opus_demo found under tmp_check.
func FindOpusDemo(version string, roots []string) (string, bool) {
	return findLibopusTool(version, roots, "opus_demo")
}

// FindOpusCompare returns the first executable opus_compare found under tmp_check.
func FindOpusCompare(version string, roots []string) (string, bool) {
	return findLibopusTool(version, roots, "opus_compare")
}

// EnsureLibopus invokes tools/ensure_libopus.sh from the first matching root.
func EnsureLibopus(version string, roots []string) {
	if version == "" {
		version = DefaultVersion
	}
	if len(roots) == 0 {
		roots = DefaultSearchRoots()
	}

	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		script := filepath.Clean(filepath.Join(root, "tools", "ensure_libopus.sh"))
		if _, ok := seen[script]; ok {
			continue
		}
		seen[script] = struct{}{}
		if st, err := os.Stat(script); err != nil || st.IsDir() {
			continue
		}

		shell := "bash"
		if _, err := exec.LookPath(shell); err != nil {
			shell = "sh"
		}
		cmd := exec.Command(shell, script)
		cmd.Env = append(os.Environ(), "LIBOPUS_VERSION="+version)
		_, _ = cmd.CombinedOutput()
		return
	}
}

// FindOrEnsureOpusDemo tries to locate opus_demo and auto-bootstraps once if missing.
func FindOrEnsureOpusDemo(version string, roots []string) (string, bool) {
	if p, ok := FindOpusDemo(version, roots); ok {
		return p, true
	}
	EnsureLibopus(version, roots)
	return FindOpusDemo(version, roots)
}

// FindOrEnsureOpusCompare tries to locate opus_compare and auto-bootstraps once if missing.
func FindOrEnsureOpusCompare(version string, roots []string) (string, bool) {
	if p, ok := FindOpusCompare(version, roots); ok {
		return p, true
	}
	EnsureLibopus(version, roots)
	return FindOpusCompare(version, roots)
}

// FindCCompiler returns a GCC/Clang-style C compiler suitable for helper builds.
func FindCCompiler() (string, error) {
	for _, candidate := range []string{"cc", "gcc", "clang"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("no supported C compiler found in PATH (tried: cc, gcc, clang)")
}
