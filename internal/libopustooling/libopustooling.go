package libopustooling

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// LibopusReferenceVariant identifies the native libopus instruction path that
// matches the current Go build.
type LibopusReferenceVariant string

const (
	LibopusReferenceScalar       LibopusReferenceVariant = "scalar"
	LibopusReferenceSIMD         LibopusReferenceVariant = "simd"
	LibopusReferenceCustomScalar LibopusReferenceVariant = "custom-scalar"

	LibopusBaseCFLAGS = "-O3 -DNDEBUG"
	// Scalar C references retain the compiler's normal FMA contraction while
	// disabling loop and SLP vectorization to match the scalar Go arithmetic.
	LibopusScalarCVectorizationFlags = "-fno-tree-vectorize -fno-tree-slp-vectorize"
	LibopusScalarCFLAGS              = LibopusBaseCFLAGS + " " + LibopusScalarCVectorizationFlags
)

// LibopusReferenceConfigError reports a paired-reference override or build
// whose declared configuration does not match the requested Go lane.
type LibopusReferenceConfigError struct {
	Err error
}

func (e *LibopusReferenceConfigError) Error() string { return e.Err.Error() }
func (e *LibopusReferenceConfigError) Unwrap() error { return e.Err }

func referenceConfigErrorf(format string, args ...any) error {
	return &LibopusReferenceConfigError{Err: fmt.Errorf(format, args...)}
}

// ResolveLibopusReferenceVariant maps the current Go build and optional
// GOPUS_LIBOPUS_REF_SCALAR override to the matching explicit libopus tree.
// Empty or "auto" follows the build tags; scalar/1 and simd/0 can only confirm
// the matching lane and fail when they conflict with the Go build.
func ResolveLibopusReferenceVariant() (LibopusReferenceVariant, error) {
	return resolveLibopusReferenceVariantFor(runtime.GOARCH, goLibopusReferenceSIMD, os.Getenv("GOPUS_LIBOPUS_REF_SCALAR"))
}

func resolveLibopusReferenceVariantFor(goarch string, goSIMD bool, override string) (LibopusReferenceVariant, error) {
	want := LibopusReferenceScalar
	if goSIMD && (goarch == "arm64" || goarch == "amd64") {
		want = LibopusReferenceSIMD
	}
	choice := strings.TrimSpace(strings.ToLower(override))
	switch choice {
	case "", "auto":
		return want, nil
	case "1", "scalar":
		if want != LibopusReferenceScalar {
			return "", referenceConfigErrorf("GOPUS_LIBOPUS_REF_SCALAR selects scalar libopus, but this Go build requires the %s reference; unset the override or use a matching Go build", want)
		}
		return LibopusReferenceScalar, nil
	case "0", "simd":
		if want != LibopusReferenceSIMD {
			return "", referenceConfigErrorf("GOPUS_LIBOPUS_REF_SCALAR selects SIMD libopus, but this Go build requires the %s reference; unset the override or use a matching Go build", want)
		}
		return LibopusReferenceSIMD, nil
	default:
		return "", referenceConfigErrorf("invalid GOPUS_LIBOPUS_REF_SCALAR value %q (want auto, scalar/1, or simd/0)", override)
	}
}

// LibopusReferenceSourceSuffix returns the mandatory source-tree suffix for a
// paired reference. The unsuffixed autotools-default tree is never selected.
func LibopusReferenceSourceSuffix(variant LibopusReferenceVariant) (string, error) {
	switch variant {
	case LibopusReferenceScalar:
		return "-scalar", nil
	case LibopusReferenceSIMD:
		return "-simd", nil
	case LibopusReferenceCustomScalar:
		return "-custom-scalar", nil
	default:
		return "", referenceConfigErrorf("unknown libopus reference variant %q", variant)
	}
}

// ValidateLibopusReferenceBuild verifies the stamp, host/compiler target,
// generated config, and static archive for a paired reference tree.
func ValidateLibopusReferenceBuild(refDir string, variant LibopusReferenceVariant, version string) error {
	return validateLibopusReferenceBuildForPlatform(refDir, variant, version, runtime.GOOS, runtime.GOARCH)
}

func validateLibopusReferenceBuildForPlatform(refDir string, variant LibopusReferenceVariant, version, goos, goarch string) error {
	if version == "" {
		version = DefaultVersion
	}
	suffix, err := LibopusReferenceSourceSuffix(variant)
	if err != nil {
		return err
	}
	wantConfigure := "--enable-static --disable-shared"
	wantCustom := "0"
	wantCFLAGS := LibopusBaseCFLAGS
	if variant == LibopusReferenceScalar || variant == LibopusReferenceCustomScalar {
		wantCFLAGS = LibopusScalarCFLAGS
		if variant == LibopusReferenceCustomScalar {
			wantConfigure += " --enable-custom-modes"
			wantCustom = "1"
		}
		wantConfigure += " --disable-asm --disable-rtcd --disable-intrinsics"
	} else {
		wantConfigure += " --enable-rtcd --enable-intrinsics"
	}
	data, err := os.ReadFile(filepath.Join(refDir, ".gopus-libopus-build"))
	if err != nil {
		return referenceConfigErrorf("read libopus %s build stamp in %s: %v", variant, refDir, err)
	}
	fields, ok := parseLibopusBuildStamp(string(data))
	if !ok {
		return referenceConfigErrorf("invalid libopus build stamp in %s", refDir)
	}
	wantFields := map[string]string{
		"version": version, "qext": "0", "fixed": "0", "custom": wantCustom,
		"configure": wantConfigure, "CFLAGS": wantCFLAGS, "CPPFLAGS": "", "LDFLAGS": "",
	}
	for key, want := range wantFields {
		if got := fields[key]; got != want {
			return referenceConfigErrorf("libopus reference %s has %s=%q, want %q (%s tree)", variant, key, got, want, suffix)
		}
	}
	for _, key := range []string{"host_os", "host_arch", "host_bits", "cc", "cc_path", "cc_target", "cc_version"} {
		if strings.TrimSpace(fields[key]) == "" {
			return referenceConfigErrorf("libopus reference %s stamp in %s has no %s", variant, refDir, key)
		}
	}
	if !libopusStampMatchesPlatform(fields, goos, goarch) {
		return referenceConfigErrorf("libopus %s archive in %s was built for a different host/compiler target (host=%s/%s target=%s)", variant, refDir, fields["host_os"], fields["host_arch"], fields["cc_target"])
	}
	configPath := filepath.Join(refDir, "config.h")
	config, err := os.ReadFile(configPath)
	if err != nil {
		return referenceConfigErrorf("read libopus config %s: %v", configPath, err)
	}
	if err := validateLibopusConfigSIMD(string(config), variant, goarch); err != nil {
		return referenceConfigErrorf("libopus %s config in %s: %v", variant, refDir, err)
	}
	archive := filepath.Join(refDir, ".libs", "libopus.a")
	st, err := os.Stat(archive)
	if err != nil {
		return referenceConfigErrorf("libopus %s archive missing at %s: %v", variant, archive, err)
	}
	if st.IsDir() || st.Size() == 0 {
		return referenceConfigErrorf("libopus %s archive at %s is empty or not a file", variant, archive)
	}
	return nil
}

func libopusStampMatchesPlatform(fields map[string]string, goos, goarch string) bool {
	wantArch := normalizeLibopusStampArch(goarch)
	if wantArch == "" || normalizeLibopusStampArch(fields["host_arch"]) != wantArch || normalizeLibopusStampArch(fields["cc_target"]) != wantArch {
		return false
	}
	wantBits := "64"
	if goarch == "386" || goarch == "arm" {
		wantBits = "32"
	}
	if fields["host_bits"] != wantBits {
		return false
	}
	hostOS := strings.ToLower(fields["host_os"])
	target := strings.ToLower(fields["cc_target"])
	switch goos {
	case "darwin":
		return strings.Contains(hostOS, "darwin") && strings.Contains(target, "darwin")
	case "linux":
		return strings.Contains(hostOS, "linux") && strings.Contains(target, "linux")
	case "windows":
		return (strings.Contains(hostOS, "mingw") || strings.Contains(hostOS, "msys") || strings.Contains(hostOS, "cygwin")) && (strings.Contains(target, "mingw") || strings.Contains(target, "msys") || strings.Contains(target, "cygwin"))
	default:
		return strings.Contains(hostOS, goos) && strings.Contains(target, goos)
	}
}

func validateLibopusConfigSIMD(config string, variant LibopusReferenceVariant, goarch string) error {
	defines := make(map[string]bool)
	for _, line := range strings.Split(config, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#define ") {
			name := strings.Fields(strings.TrimPrefix(line, "#define "))
			if len(name) > 0 {
				defines[name[0]] = true
			}
		}
	}
	var simdMacros []string
	for macro := range defines {
		if strings.HasPrefix(macro, "OPUS_ARM_") && (strings.Contains(macro, "NEON") || strings.Contains(macro, "DOTPROD")) ||
			strings.HasPrefix(macro, "OPUS_X86_") && (strings.Contains(macro, "SSE") || strings.Contains(macro, "AVX")) || macro == "OPUS_HAVE_RTCD" {
			simdMacros = append(simdMacros, macro)
		}
	}
	if variant == LibopusReferenceScalar || variant == LibopusReferenceCustomScalar {
		if len(simdMacros) != 0 {
			sort.Strings(simdMacros)
			return fmt.Errorf("scalar config defines SIMD/RTCD macros %v", simdMacros)
		}
		return nil
	}
	if variant != LibopusReferenceSIMD {
		return fmt.Errorf("unsupported variant %q", variant)
	}
	switch goarch {
	case "arm64":
		if !defines["OPUS_ARM_PRESUME_NEON_INTR"] && !defines["OPUS_ARM_MAY_HAVE_NEON_INTR"] && !defines["OPUS_ARM_PRESUME_NEON"] && !defines["OPUS_ARM_MAY_HAVE_NEON"] {
			return fmt.Errorf("arm64 SIMD config has no NEON instruction macro")
		}
	case "amd64":
		if !defines["OPUS_X86_PRESUME_SSE"] && !defines["OPUS_X86_PRESUME_SSE2"] && !defines["OPUS_X86_PRESUME_SSE4_1"] && !defines["OPUS_X86_PRESUME_AVX2"] &&
			!defines["OPUS_X86_MAY_HAVE_SSE"] && !defines["OPUS_X86_MAY_HAVE_SSE2"] && !defines["OPUS_X86_MAY_HAVE_SSE4_1"] && !defines["OPUS_X86_MAY_HAVE_AVX2"] {
			return fmt.Errorf("amd64 SIMD config has no SSE/AVX instruction macro")
		}
	default:
		return fmt.Errorf("no paired SIMD libopus configuration for GOARCH=%s", goarch)
	}
	return nil
}

// ValidateLibopusReferenceArchive validates the provenance stamped beside an
// archive path. Paired archives live under <source>/.libs/libopus.a.
func ValidateLibopusReferenceArchive(archivePath string, variant LibopusReferenceVariant, version string) error {
	archivePath = filepath.Clean(archivePath)
	if filepath.Base(archivePath) != "libopus.a" || filepath.Base(filepath.Dir(archivePath)) != ".libs" {
		return referenceConfigErrorf("libopus archive override %q must be inside a stamped .libs directory", archivePath)
	}
	return ValidateLibopusReferenceBuild(filepath.Dir(filepath.Dir(archivePath)), variant, version)
}

// ValidateLibopusReferenceToolOverride requires an explicit tool executable to
// resolve to the requested tool in a validated, explicitly named paired tree.
// It validates the executable target so a symlink cannot escape into another
// variant while callers can continue returning the user's original path.
func ValidateLibopusReferenceToolOverride(path, tool string, variant LibopusReferenceVariant, version string) error {
	return validateLibopusReferenceToolOverrideForPlatform(path, tool, variant, version, runtime.GOOS, runtime.GOARCH)
}

func validateLibopusReferenceToolOverrideForPlatform(path, tool string, variant LibopusReferenceVariant, version, goos, goarch string) error {
	if version == "" {
		version = DefaultVersion
	}
	suffix, err := LibopusReferenceSourceSuffix(variant)
	if err != nil {
		return err
	}
	if tool == "" || filepath.Base(tool) != tool {
		return referenceConfigErrorf("invalid libopus tool name %q", tool)
	}
	if path == "" {
		return referenceConfigErrorf("libopus %s override is empty", tool)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return referenceConfigErrorf("resolve libopus %s override %q: %v", tool, path, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return referenceConfigErrorf("stat libopus %s override %q: %v", tool, path, err)
	}
	if !libopusToolIsRunnable(info, goos) {
		return referenceConfigErrorf("libopus %s override %q is not executable", tool, path)
	}
	base := filepath.Base(resolved)
	if base != tool && base != tool+".exe" {
		return referenceConfigErrorf("libopus %s override %q resolves to unexpected executable %q", tool, path, base)
	}
	refDir := filepath.Dir(resolved)
	wantDir := "opus-" + version + suffix
	if filepath.Base(refDir) != wantDir {
		return referenceConfigErrorf("libopus %s override %q resolves outside the selected %s tree", tool, path, variant)
	}
	if err := validateLibopusReferenceBuildForPlatform(refDir, variant, version, goos, goarch); err != nil {
		return err
	}
	return nil
}

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

func normalizedRoots(roots []string) []string {
	if len(roots) == 0 {
		return DefaultSearchRoots()
	}
	return roots
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

// FindOpusDemo returns a validated opus_demo from the reference tree paired
// with the current Go build.
func FindOpusDemo(version string, roots []string) (string, error) {
	variant, err := ResolveLibopusReferenceVariant()
	if err != nil {
		return "", err
	}
	return findValidatedReferenceTool(version, roots, "opus_demo", variant, runtime.GOOS, runtime.GOARCH)
}

// FindQEXTOpusDemo returns the first executable QEXT-enabled opus_demo build
// found under tmp_check.
func FindQEXTOpusDemo(version string, roots []string) (string, bool) {
	return findQEXTLibopusTool(version, roots, "opus_demo")
}

// FindOpusCompare returns a validated opus_compare from the reference tree
// paired with the current Go build.
func FindOpusCompare(version string, roots []string) (string, error) {
	variant, err := ResolveLibopusReferenceVariant()
	if err != nil {
		return "", err
	}
	return findValidatedReferenceTool(version, roots, "opus_compare", variant, runtime.GOOS, runtime.GOARCH)
}

func findValidatedReferenceTool(version string, roots []string, tool string, variant LibopusReferenceVariant, goos, goarch string) (string, error) {
	suffix, err := LibopusReferenceSourceSuffix(variant)
	if err != nil {
		return "", err
	}
	var firstConfigError error
	treePresent := false
	for _, root := range normalizedRoots(roots) {
		refDir := libopusSourceDir(version, root, suffix)
		if st, err := os.Stat(refDir); err == nil && st.IsDir() {
			treePresent = true
		}
		if err := validateLibopusReferenceBuildForPlatform(refDir, variant, version, goos, goarch); err != nil {
			if firstConfigError == nil {
				firstConfigError = err
			}
			continue
		}
		if path, ok := findLibopusToolInSourceForOS(version, []string{root}, suffix, tool, goos); ok {
			return path, nil
		}
	}
	if treePresent && firstConfigError != nil {
		return "", firstConfigError
	}
	return "", fmt.Errorf("no validated %s libopus %s found under roots %v", variant, tool, normalizedRoots(roots))
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
	if !strings.Contains(hostOS, "MINGW") && !strings.Contains(hostOS, "MSYS") && !strings.Contains(hostOS, "CYGWIN") {
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

// EnsureLibopusFixed invokes tools/ensure_libopus.sh with ENABLE_FIXED enabled
// (libopus configured with --enable-fixed-point) from the first matching root.
func EnsureLibopusFixed(version string, roots []string) bool {
	return ensureLibopusVariant(version, roots, "fixed")
}

// EnsureLibopusCustom invokes tools/ensure_libopus.sh with ENABLE_CUSTOM enabled
// (libopus configured with --enable-custom-modes, defining CUSTOM_MODES and the
// Opus Custom API) from the first matching root.
func EnsureLibopusCustom(version string, roots []string) bool {
	return ensureLibopusVariant(version, roots, "custom")
}

// EnsureLibopusSIMD invokes tools/ensure_libopus.sh with ENABLE_SIMD enabled
// (libopus configured with its native --enable-rtcd --enable-intrinsics, so
// config.h DEFINES the platform SIMD macros) from the first matching root. It is
// paired with Go SIMD builds and direct tests of matching SIMD kernels.
func EnsureLibopusSIMD(version string, roots []string) bool {
	return ensureLibopusVariant(version, roots, "simd")
}

// EnsureLibopusScalar invokes tools/ensure_libopus.sh with ENABLE_SCALAR enabled
// (generic C with assembly, RTCD, intrinsics, loop vectorization, and SLP
// vectorization disabled) from the first matching root. FMA contraction stays
// enabled to match scalar Go arithmetic.
func EnsureLibopusScalar(version string, roots []string) bool {
	return ensureLibopusVariant(version, roots, "scalar")
}

// EnsureLibopusCustomScalar invokes tools/ensure_libopus.sh with
// ENABLE_CUSTOM_SCALAR enabled (--enable-custom-modes on the scalar generic-C
// kernels) from the first matching root with the standard scalar compiler
// policy.
func EnsureLibopusCustomScalar(version string, roots []string) bool {
	return ensureLibopusVariant(version, roots, "custom-scalar")
}

func ensureLibopus(version string, roots []string, qext bool) bool {
	variant := "float"
	if qext {
		variant = "qext"
	}
	return ensureLibopusVariant(version, roots, variant)
}

func ensureLibopusVariant(version string, roots []string, variant string) bool {
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
		switch variant {
		case "qext":
			env = append(env, "LIBOPUS_ENABLE_QEXT=1")
		case "fixed":
			env = append(env, "LIBOPUS_ENABLE_FIXED=1")
		case "custom":
			env = append(env, "LIBOPUS_ENABLE_CUSTOM=1")
		case "custom-scalar":
			env = append(env, "LIBOPUS_ENABLE_CUSTOM_SCALAR=1")
		case "simd":
			env = append(env, "LIBOPUS_ENABLE_SIMD=1")
		case "scalar":
			env = append(env, "LIBOPUS_ENABLE_SCALAR=1")
		}
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "gopus: ensure libopus failed root=%q shell=%q variant=%q MSYSTEM=%q err=%v\n", root, shell, variant, os.Getenv("MSYSTEM"), err)
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

// FindOrEnsureOpusDemo builds and validates the explicit tree paired with the
// current Go build before locating opus_demo.
func FindOrEnsureOpusDemo(version string, roots []string) (string, error) {
	variant, err := ResolveLibopusReferenceVariant()
	if err != nil {
		return "", err
	}
	return findOrEnsureReferenceTool(version, roots, "opus_demo", variant, runtime.GOOS, runtime.GOARCH)
}

func findOrEnsureReferenceTool(version string, roots []string, tool string, variant LibopusReferenceVariant, goos, goarch string) (string, error) {
	if _, err := LibopusReferenceSourceSuffix(variant); err != nil {
		return "", err
	}
	if path, err := findValidatedReferenceTool(version, roots, tool, variant, goos, goarch); err == nil {
		return path, nil
	}
	ensure := EnsureLibopusScalar
	if variant == LibopusReferenceSIMD {
		ensure = EnsureLibopusSIMD
	}
	ensure(version, roots)
	return findValidatedReferenceTool(version, roots, tool, variant, goos, goarch)
}

// FindOrEnsureQEXTOpusDemo tries to locate a QEXT-enabled opus_demo and
// validates the separate QEXT build first.
func FindOrEnsureQEXTOpusDemo(version string, roots []string) (string, bool) {
	if !EnsureLibopusQEXT(version, roots) && !stampedLibopusBuildPresent(version, roots, true) {
		return "", false
	}
	return FindQEXTOpusDemo(version, roots)
}

// FindOrEnsureOpusCompare builds and validates the explicit tree paired with
// the current Go build before locating opus_compare.
func FindOrEnsureOpusCompare(version string, roots []string) (string, error) {
	variant, err := ResolveLibopusReferenceVariant()
	if err != nil {
		return "", err
	}
	return findOrEnsureReferenceTool(version, roots, "opus_compare", variant, runtime.GOOS, runtime.GOARCH)
}

func findOrEnsureOpusCompareForPlatform(version string, roots []string, goos, goarch string) (string, error) {
	variant, err := resolveLibopusReferenceVariantFor(goarch, goLibopusReferenceSIMD, os.Getenv("GOPUS_LIBOPUS_REF_SCALAR"))
	if err != nil {
		return "", err
	}
	return findOrEnsureReferenceTool(version, roots, "opus_compare", variant, goos, goarch)
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
