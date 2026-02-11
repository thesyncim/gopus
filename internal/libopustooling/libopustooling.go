package libopustooling

import (
	"os"
	"os/exec"
	"path/filepath"
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

// FindOpusDemo returns the first executable opus_demo found under tmp_check.
func FindOpusDemo(version string, roots []string) (string, bool) {
	if version == "" {
		version = DefaultVersion
	}
	if len(roots) == 0 {
		roots = DefaultSearchRoots()
	}

	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		p := filepath.Clean(filepath.Join(root, "tmp_check", "opus-"+version, "opus_demo"))
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		if st, err := os.Stat(p); err == nil && (st.Mode()&0o111) != 0 {
			return p, true
		}
	}
	return "", false
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

		cmd := exec.Command("sh", script)
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
