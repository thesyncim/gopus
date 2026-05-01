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

	// ScalarDNNBuildCFLAGS keeps x86 libopus helper builds on the generic DNN
	// path. --disable-intrinsics disables libopus RTCD feature selection, but
	// x86 compilers still predefine __SSE2__, which makes dnn/vec.h include
	// vec_avx.h unless the helper build explicitly undefines those macros.
	ScalarDNNBuildCFLAGS = "-g -O2 -fvisibility=hidden -U__AVX__ -U__AVX2__ -U__FMA__ -U__SSE__ -U__SSE2__ -U__SSE3__ -U__SSSE3__ -U__SSE4_1__ -U__SSE4_2__"

	scalarDNNBuildStampFile = ".gopus-scalar-dnn-build"
	scalarDNNBuildStamp     = "gopus scalar libopus DNN helper build v2\nCFLAGS=" + ScalarDNNBuildCFLAGS + "\n"
)

// DefaultSearchRoots covers common invocation locations:
// repository root, package subdirs (e.g. testvectors), and deeper test runs.
func DefaultSearchRoots() []string {
	return []string{".", "..", "../.."}
}

func findLibopusTool(version string, roots []string, tool string) (string, bool) {
	return findLibopusToolInSourceForOS(version, roots, "", tool, runtime.GOOS)
}

func findLibopusToolForOS(version string, roots []string, tool, goos string) (string, bool) {
	return findLibopusToolInSourceForOS(version, roots, "", tool, goos)
}

func findQEXTLibopusTool(version string, roots []string, tool string) (string, bool) {
	return findLibopusToolInSourceForOS(version, roots, "-qext", tool, runtime.GOOS)
}

func findQEXTLibopusToolForOS(version string, roots []string, tool, goos string) (string, bool) {
	return findLibopusToolInSourceForOS(version, roots, "-qext", tool, goos)
}

func findLibopusToolInSourceForOS(version string, roots []string, sourceSuffix string, tool, goos string) (string, bool) {
	if version == "" {
		version = DefaultVersion
	}
	if len(roots) == 0 {
		roots = DefaultSearchRoots()
	}

	seen := make(map[string]struct{}, len(roots)*2)
	for _, root := range roots {
		for _, candidate := range libopusToolCandidates(tool, goos) {
			p := filepath.Clean(filepath.Join(root, "tmp_check", "opus-"+version+sourceSuffix, candidate))
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

// FindQEXTOpusDemo returns the first executable QEXT-enabled opus_demo build
// found under tmp_check.
func FindQEXTOpusDemo(version string, roots []string) (string, bool) {
	return findQEXTLibopusTool(version, roots, "opus_demo")
}

// FindOpusCompare returns the first executable opus_compare found under tmp_check.
func FindOpusCompare(version string, roots []string) (string, bool) {
	return findLibopusTool(version, roots, "opus_compare")
}

// EnsureLibopus invokes tools/ensure_libopus.sh from the first matching root.
func EnsureLibopus(version string, roots []string) {
	ensureLibopus(version, roots, false)
}

// EnsureLibopusQEXT invokes tools/ensure_libopus.sh with ENABLE_QEXT enabled
// from the first matching root.
func EnsureLibopusQEXT(version string, roots []string) {
	ensureLibopus(version, roots, true)
}

func ensureLibopus(version string, roots []string, qext bool) {
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
		env := append(os.Environ(), "LIBOPUS_VERSION="+version)
		if qext {
			env = append(env, "LIBOPUS_ENABLE_QEXT=1")
		}
		cmd.Env = env
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

// FindOrEnsureQEXTOpusDemo tries to locate a QEXT-enabled opus_demo and
// auto-bootstraps once if missing.
func FindOrEnsureQEXTOpusDemo(version string, roots []string) (string, bool) {
	if p, ok := FindQEXTOpusDemo(version, roots); ok {
		return p, true
	}
	EnsureLibopusQEXT(version, roots)
	return FindQEXTOpusDemo(version, roots)
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

// ScalarDNNBuildEnv returns a controlled environment for libopus helper builds.
func ScalarDNNBuildEnv() []string {
	env := os.Environ()
	dst := env[:0]
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if ok && (name == "CFLAGS" || name == "CPPFLAGS") {
			continue
		}
		dst = append(dst, kv)
	}
	return append(dst, "CFLAGS="+ScalarDNNBuildCFLAGS, "CPPFLAGS=")
}

// ScalarDNNBuildIsCurrent reports whether buildDir was produced with the
// current scalar-DNN helper contract.
func ScalarDNNBuildIsCurrent(buildDir string) bool {
	data, err := os.ReadFile(filepath.Join(buildDir, scalarDNNBuildStampFile))
	return err == nil && string(data) == scalarDNNBuildStamp
}

// ResetScalarDNNBuildIfStale removes buildDir when it was produced before the
// current scalar-DNN helper contract. This avoids silently reusing x86-vector
// DNN reference oracles from older local or CI caches.
func ResetScalarDNNBuildIfStale(buildDir string) error {
	if _, err := os.Stat(buildDir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if ScalarDNNBuildIsCurrent(buildDir) {
		return nil
	}
	return os.RemoveAll(buildDir)
}

// WriteScalarDNNBuildStamp records that buildDir satisfies the current
// scalar-DNN helper contract.
func WriteScalarDNNBuildStamp(buildDir string) error {
	return os.WriteFile(filepath.Join(buildDir, scalarDNNBuildStampFile), []byte(scalarDNNBuildStamp), 0o644)
}
