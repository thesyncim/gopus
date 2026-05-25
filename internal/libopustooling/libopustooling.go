package libopustooling

import (
	"crypto/sha256"
	"encoding/hex"
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

	// OSCEScalarDNNBuildCFLAGS keeps OSCE BWE/LACE reference helpers on the
	// generic DNN path even on ARM, where dnn/vec.h checks compiler NEON macros
	// directly.
	OSCEScalarDNNBuildCFLAGS = "-g -O2 -fvisibility=hidden -DDISABLE_NEON -U__ARM_NEON__ -U__ARM_NEON -U__AVX__ -U__AVX2__ -U__FMA__ -U__SSE__ -U__SSE2__ -U__SSE3__ -U__SSSE3__ -U__SSE4_1__ -U__SSE4_2__"

	scalarDNNBuildStampFile = ".gopus-scalar-dnn-build"

	// OSCE-enabled scalar build stamp. The OSCE build pulls in additional
	// source (`dnn/osce.c`, `dnn/osce_features.c`, `dnn/bbwenet_data.c`, ...)
	// because `--enable-osce` / `--enable-osce-bwe` are passed to configure.
	// The CFLAGS are stricter than the regular scalar build because OSCE parity
	// fixtures exercise the generic DNN path on ARM too. The stamp is different
	// so a stale plain scalar build cannot be reused as an OSCE build.
	osceScalarDNNBuildStampFile = ".gopus-scalar-dnn-build-osce"
)

// LibopusBuildProvenance captures the native helper build that produced a
// generated libopus fixture.
type LibopusBuildProvenance struct {
	GOOS                    string `json:"goos"`
	GOARCH                  string `json:"goarch"`
	LibopusVersion          string `json:"libopus_version"`
	QEXT                    string `json:"qext"`
	HostOS                  string `json:"host_os"`
	HostArch                string `json:"host_arch"`
	HostBits                string `json:"host_bits"`
	CC                      string `json:"cc"`
	CCPath                  string `json:"cc_path"`
	CCTarget                string `json:"cc_target"`
	CCVersion               string `json:"cc_version"`
	Configure               string `json:"configure"`
	CFLAGS                  string `json:"cflags"`
	CPPFLAGS                string `json:"cppflags"`
	LDFLAGS                 string `json:"ldflags"`
	LibopusBuildStampSHA256 string `json:"libopus_build_stamp_sha256"`
}

// DefaultSearchRoots covers common invocation locations:
// repository root, package subdirs (e.g. testvectors), and deeper test runs.
func DefaultSearchRoots() []string {
	roots := []string{".", "..", "../.."}
	if workspace := os.Getenv("GITHUB_WORKSPACE"); workspace != "" {
		roots = append(roots, workspace)
	}
	if root, ok := sourceRepoRoot(); ok {
		roots = append(roots, root)
	}
	return roots
}

func sourceRepoRoot() (string, bool) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", false
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if st, err := os.Stat(filepath.Join(root, "go.mod")); err == nil && !st.IsDir() {
		return root, true
	}
	return "", false
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

func libopusSourceDir(version string, root string, sourceSuffix string) string {
	if version == "" {
		version = DefaultVersion
	}
	return filepath.Clean(filepath.Join(root, "tmp_check", "opus-"+version+sourceSuffix))
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

func stampedLibopusBuildPresent(version string, roots []string, qext bool) bool {
	return stampedLibopusBuildPresentForPlatform(version, roots, qext, runtime.GOOS, runtime.GOARCH)
}

func stampedLibopusBuildPresentForPlatform(version string, roots []string, qext bool, goos, goarch string) bool {
	// This fallback exists for Windows CI, which builds libopus under MSYS2 and
	// then runs Go tests from PowerShell where the shell validator may not be
	// runnable. Other platforms can run the validator directly.
	if goos != "windows" {
		return false
	}
	if version == "" {
		version = DefaultVersion
	}
	if len(roots) == 0 {
		roots = DefaultSearchRoots()
	}
	sourceSuffix := ""
	qextValue := "0"
	if qext {
		sourceSuffix = "-qext"
		qextValue = "1"
	}
	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		srcDir := libopusSourceDir(version, root, sourceSuffix)
		if _, ok := seen[srcDir]; ok {
			continue
		}
		seen[srcDir] = struct{}{}
		if !stampedLibopusSourceDirPresent(srcDir, version, qextValue, goos, goarch) {
			continue
		}
		if _, ok := findLibopusToolInSourceForOS(version, []string{root}, sourceSuffix, "opus_demo", goos); !ok {
			continue
		}
		if _, ok := findLibopusToolInSourceForOS(version, []string{root}, sourceSuffix, "opus_compare", goos); !ok {
			continue
		}
		if st, err := os.Stat(filepath.Join(srcDir, ".libs", "libopus.a")); err != nil || st.IsDir() || st.Size() == 0 {
			continue
		}
		return true
	}
	return false
}

func stampedLibopusSourceDirPresent(srcDir, version, qext, goos, goarch string) bool {
	if goos != "windows" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(srcDir, ".gopus-libopus-build"))
	if err != nil {
		return false
	}
	fields, ok := parseLibopusBuildStamp(string(data))
	if !ok {
		return false
	}
	configure := "--enable-static --disable-shared"
	if qext == "1" {
		configure += " --enable-qext"
	}
	required := map[string]string{
		"version":   version,
		"qext":      qext,
		"configure": configure,
		"CFLAGS":    "-O3 -DNDEBUG",
		"CPPFLAGS":  "",
		"LDFLAGS":   "",
	}
	for key, want := range required {
		if fields[key] != want {
			return false
		}
	}
	for _, key := range []string{"host_os", "host_arch", "host_bits", "cc", "cc_path", "cc_target", "cc_version"} {
		if _, ok := fields[key]; !ok {
			return false
		}
	}
	hostOS := fields["host_os"]
	if !(strings.Contains(hostOS, "MINGW") ||
		strings.Contains(hostOS, "MSYS") ||
		strings.Contains(hostOS, "CYGWIN")) {
		return false
	}
	return libopusStampArchitectureMatches(goarch, fields["host_arch"], fields["host_bits"], fields["cc_target"])
}

func libopusStampArchitectureMatches(goarch, hostArch, hostBits, ccTarget string) bool {
	if goarch == "" {
		return false
	}
	wantArch := normalizeLibopusStampArch(goarch)
	if wantArch == "" {
		return false
	}
	if normalizeLibopusStampArch(hostArch) != wantArch {
		return false
	}
	if normalizeLibopusStampArch(ccTarget) != wantArch {
		return false
	}
	target := strings.ToLower(ccTarget)
	if !strings.Contains(target, "mingw") && !strings.Contains(target, "msys") && !strings.Contains(target, "cygwin") {
		return false
	}
	switch wantArch {
	case "amd64", "arm64":
		return hostBits == "64"
	case "386":
		return hostBits == "32"
	default:
		return false
	}
}

func normalizeLibopusStampArch(arch string) string {
	arch = strings.ToLower(arch)
	switch {
	case strings.Contains(arch, "x86_64") || strings.Contains(arch, "amd64"):
		return "amd64"
	case strings.Contains(arch, "aarch64") || strings.Contains(arch, "arm64"):
		return "arm64"
	case strings.Contains(arch, "i686") || strings.Contains(arch, "i386") || arch == "386":
		return "386"
	default:
		return ""
	}
}

func parseLibopusBuildStamp(stamp string) (map[string]string, bool) {
	lines := strings.Split(strings.TrimRight(stamp, "\r\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "gopus libopus helper build v5" {
		return nil, false
	}
	fields := make(map[string]string, len(lines)-1)
	for _, line := range lines[1:] {
		line = strings.TrimRight(line, "\r")
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			return nil, false
		}
		fields[key] = strings.TrimSpace(value)
	}
	return fields, true
}

// LibopusBuildProvenanceForTool returns provenance for a tool produced by
// tools/ensure_libopus.sh. The returned digest is over the exact stamp file
// bytes so fixture metadata changes whenever the validated native helper build
// changes.
func LibopusBuildProvenanceForTool(toolPath string) (LibopusBuildProvenance, bool) {
	data, err := os.ReadFile(filepath.Join(filepath.Dir(toolPath), ".gopus-libopus-build"))
	if err != nil {
		return LibopusBuildProvenance{}, false
	}
	fields, ok := parseLibopusBuildStamp(string(data))
	if !ok {
		return LibopusBuildProvenance{}, false
	}
	sum := sha256.Sum256(data)
	return LibopusBuildProvenance{
		GOOS:                    runtime.GOOS,
		GOARCH:                  runtime.GOARCH,
		LibopusVersion:          fields["version"],
		QEXT:                    fields["qext"],
		HostOS:                  fields["host_os"],
		HostArch:                fields["host_arch"],
		HostBits:                fields["host_bits"],
		CC:                      fields["cc"],
		CCPath:                  fields["cc_path"],
		CCTarget:                fields["cc_target"],
		CCVersion:               fields["cc_version"],
		Configure:               fields["configure"],
		CFLAGS:                  fields["CFLAGS"],
		CPPFLAGS:                fields["CPPFLAGS"],
		LDFLAGS:                 fields["LDFLAGS"],
		LibopusBuildStampSHA256: hex.EncodeToString(sum[:]),
	}, true
}

// EnsureLibopus invokes tools/ensure_libopus.sh from the first matching root.
func EnsureLibopus(version string, roots []string) bool {
	return ensureLibopus(version, roots, false)
}

// EnsureLibopusQEXT invokes tools/ensure_libopus.sh with ENABLE_QEXT enabled
// from the first matching root.
func EnsureLibopusQEXT(version string, roots []string) bool {
	return ensureLibopus(version, roots, true)
}

func ensureLibopus(version string, roots []string, qext bool) bool {
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
		cmd := exec.Command(shell, filepath.ToSlash(filepath.Join("tools", "ensure_libopus.sh")))
		cmd.Dir = root
		env := append(os.Environ(), "LIBOPUS_VERSION="+version)
		if qext {
			env = append(env, "LIBOPUS_ENABLE_QEXT=1")
		}
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "gopus: ensure libopus failed root=%q shell=%q qext=%t MSYSTEM=%q err=%v\n", root, shell, qext, os.Getenv("MSYSTEM"), err)
			fmt.Fprintf(os.Stderr, "gopus: ensure libopus PATH=%q\n", os.Getenv("PATH"))
			if len(out) > 0 {
				fmt.Fprintf(os.Stderr, "gopus: ensure libopus output follows:\n%s\n", tailForLog(string(out), 64*1024))
			}
		}
		return err == nil
	}
	return false
}

func tailForLog(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return "... output truncated ...\n" + s[len(s)-max:]
}

// FindOrEnsureOpusDemo validates the pinned libopus build, then locates
// opus_demo. The validation step matters for fixture generation: an existing
// executable can be from a stale host/compiler build even when it is runnable.
func FindOrEnsureOpusDemo(version string, roots []string) (string, bool) {
	if !EnsureLibopus(version, roots) && !stampedLibopusBuildPresent(version, roots, false) {
		return "", false
	}
	return FindOpusDemo(version, roots)
}

// FindOrEnsureQEXTOpusDemo tries to locate a QEXT-enabled opus_demo and
// validates the separate QEXT build first.
func FindOrEnsureQEXTOpusDemo(version string, roots []string) (string, bool) {
	if !EnsureLibopusQEXT(version, roots) && !stampedLibopusBuildPresent(version, roots, true) {
		return "", false
	}
	return FindQEXTOpusDemo(version, roots)
}

// FindOrEnsureOpusCompare validates the pinned libopus build, then locates
// opus_compare.
func FindOrEnsureOpusCompare(version string, roots []string) (string, bool) {
	return findOrEnsureOpusCompareForPlatform(version, roots, runtime.GOOS, runtime.GOARCH)
}

func findOrEnsureOpusCompareForPlatform(version string, roots []string, goos, goarch string) (string, bool) {
	if !EnsureLibopus(version, roots) && !stampedLibopusBuildPresentForPlatform(version, roots, false, goos, goarch) {
		return "", false
	}
	return findLibopusToolForOS(version, roots, "opus_compare", goos)
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
func ScalarDNNBuildEnv() ([]string, error) {
	return scalarDNNBuildEnv(ScalarDNNBuildCFLAGS)
}

func scalarDNNBuildEnv(cflags string) ([]string, error) {
	cc, err := FindCCompiler()
	if err != nil {
		return nil, err
	}
	env := os.Environ()
	dst := env[:0]
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if ok && (name == "CC" || name == "CFLAGS" || name == "CPPFLAGS" || name == "LDFLAGS") {
			continue
		}
		dst = append(dst, kv)
	}
	return append(dst, "CC="+cc, "CFLAGS="+cflags, "CPPFLAGS=", "LDFLAGS="), nil
}

// OSCEScalarDNNBuildEnv returns a controlled environment for OSCE reference
// helper builds.
func OSCEScalarDNNBuildEnv() ([]string, error) {
	return scalarDNNBuildEnv(OSCEScalarDNNBuildCFLAGS)
}

func scalarDNNBuildStamp(cflags string) (string, error) {
	cc, err := FindCCompiler()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("gopus scalar libopus DNN helper build v4\n")
	b.WriteString("GOOS=" + runtime.GOOS + "\n")
	b.WriteString("GOARCH=" + runtime.GOARCH + "\n")
	b.WriteString("CC=" + cc + "\n")
	b.WriteString("CC_TARGET=" + compilerStampLine(cc, "-dumpmachine") + "\n")
	b.WriteString("CC_VERSION=" + compilerStampLine(cc, "--version") + "\n")
	b.WriteString("CFLAGS=" + cflags + "\n")
	b.WriteString("CPPFLAGS=\n")
	b.WriteString("LDFLAGS=\n")
	return b.String(), nil
}

func compilerStampLine(cc string, arg string) string {
	out, err := exec.Command(cc, arg).CombinedOutput()
	if err != nil {
		return "unavailable"
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	if line == "" {
		return "unavailable"
	}
	return line
}

// ScalarDNNBuildIsCurrent reports whether buildDir was produced with the
// current scalar-DNN helper contract.
func ScalarDNNBuildIsCurrent(buildDir string) bool {
	data, err := os.ReadFile(filepath.Join(buildDir, scalarDNNBuildStampFile))
	if err != nil {
		return false
	}
	stamp, err := scalarDNNBuildStamp(ScalarDNNBuildCFLAGS)
	return err == nil && string(data) == stamp
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
	stamp, err := scalarDNNBuildStamp(ScalarDNNBuildCFLAGS)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(buildDir, scalarDNNBuildStampFile), []byte(stamp), 0o644)
}

// OSCEScalarDNNBuildIsCurrent reports whether buildDir was produced with the
// current OSCE-enabled scalar-DNN helper contract.
func OSCEScalarDNNBuildIsCurrent(buildDir string) bool {
	data, err := os.ReadFile(filepath.Join(buildDir, osceScalarDNNBuildStampFile))
	if err != nil {
		return false
	}
	stamp, err := scalarDNNBuildStamp(OSCEScalarDNNBuildCFLAGS)
	return err == nil && string(data) == stamp
}

// ResetOSCEScalarDNNBuildIfStale removes buildDir when it was produced before
// the current OSCE-enabled scalar-DNN helper contract.
func ResetOSCEScalarDNNBuildIfStale(buildDir string) error {
	if _, err := os.Stat(buildDir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if OSCEScalarDNNBuildIsCurrent(buildDir) {
		return nil
	}
	return os.RemoveAll(buildDir)
}

// WriteOSCEScalarDNNBuildStamp records that buildDir satisfies the current
// OSCE-enabled scalar-DNN helper contract.
func WriteOSCEScalarDNNBuildStamp(buildDir string) error {
	stamp, err := scalarDNNBuildStamp(OSCEScalarDNNBuildCFLAGS)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(buildDir, osceScalarDNNBuildStampFile), []byte(stamp), 0o644)
}
