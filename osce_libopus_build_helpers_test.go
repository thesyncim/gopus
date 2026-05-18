//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

// ensureLibopusOSCEBuild prepares an OSCE-enabled scalar libopus build
// (`--enable-osce --enable-osce-bwe`). This sibling of the regular
// `build-opus-dred-scalar-*` tree links the upstream BBWENet symbols
// (`bbwenet_process_frames`, `osce_bwe_calculate_features`, ...) so test
// helpers can drive the libopus reference forward pass directly. The OSCE
// build lives at `tmp_check/build-opus-osce-scalar-{os}-{arch}` so it
// coexists with the non-OSCE DRED-only build that other parity tests rely
// on.
func ensureLibopusOSCEBuild(repoRoot string) (sourceDir, buildDir string, err error) {
	referenceDir := filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion)
	sourceDir = filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion+"-dredsrc-clean")
	buildDir = filepath.Join(repoRoot, "tmp_check", fmt.Sprintf("build-opus-osce-scalar-%s-%s", runtime.GOOS, runtime.GOARCH))
	libopusStatic := filepath.Join(buildDir, ".libs", "libopus.a")
	if _, err := os.Stat(libopusStatic); err == nil && libopustooling.OSCEScalarDNNBuildIsCurrent(buildDir) {
		return sourceDir, buildDir, nil
	}

	if _, err := os.Stat(filepath.Join(sourceDir, "configure")); err != nil {
		libopustooling.EnsureLibopus(libopustooling.DefaultVersion, []string{repoRoot})
		tarball := filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion+".tar.gz")
		if _, err := os.Stat(tarball); err == nil {
			if err := os.RemoveAll(sourceDir); err != nil {
				return "", "", fmt.Errorf("remove stale osce source dir: %w", err)
			}
			if err := os.MkdirAll(sourceDir, 0o755); err != nil {
				return "", "", fmt.Errorf("mkdir osce source dir: %w", err)
			}
			cmd := exec.Command("tar", "-xzf", tarball, "-C", sourceDir, "--strip-components=1")
			if output, err := cmd.CombinedOutput(); err != nil {
				return "", "", fmt.Errorf("extract osce libopus source: %w (%s)", err, bytes.TrimSpace(output))
			}
		} else if _, refErr := os.Stat(filepath.Join(referenceDir, "configure")); refErr == nil {
			if _, cfgErr := os.Stat(filepath.Join(referenceDir, "Makefile")); cfgErr == nil {
				return "", "", fmt.Errorf("clean osce source tree unavailable: %s is already configured", referenceDir)
			}
			sourceDir = referenceDir
		} else {
			return "", "", fmt.Errorf("libopus tarball not found and no prepared source tree present: %w", err)
		}
	}

	if err := libopustooling.ResetOSCEScalarDNNBuildIfStale(buildDir); err != nil {
		return "", "", fmt.Errorf("reset stale osce scalar build dir: %w", err)
	}
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return "", "", fmt.Errorf("mkdir osce build dir: %w", err)
	}

	if _, err := os.Stat(filepath.Join(buildDir, "Makefile")); err != nil {
		cmd := exec.Command(filepath.Join(sourceDir, "configure"),
			"--enable-static",
			"--disable-shared",
			"--disable-extra-programs",
			"--enable-dred",
			"--enable-osce",
			"--enable-osce-bwe",
			"--disable-asm",
			"--disable-rtcd",
			"--disable-intrinsics",
		)
		cmd.Dir = buildDir
		cmd.Env = libopustooling.ScalarDNNBuildEnv()
		if output, err := cmd.CombinedOutput(); err != nil {
			return "", "", fmt.Errorf("configure osce libopus build: %w (%s)", err, bytes.TrimSpace(output))
		}
	}

	makeCmd := exec.Command("make", fmt.Sprintf("-j%d", max(1, runtime.NumCPU())))
	makeCmd.Dir = buildDir
	makeCmd.Env = libopustooling.ScalarDNNBuildEnv()
	if output, err := makeCmd.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("build osce libopus: %w (%s)", err, bytes.TrimSpace(output))
	}
	if err := libopustooling.WriteOSCEScalarDNNBuildStamp(buildDir); err != nil {
		return "", "", fmt.Errorf("write osce scalar build stamp: %w", err)
	}

	return sourceDir, buildDir, nil
}

// buildLibopusOSCEHelper compiles a C helper against the OSCE-enabled
// libopus build so the helper can call `bbwenet_process_frames` /
// `osce_bwe_calculate_features` directly.
func buildLibopusOSCEHelper(sourceFile, outputBase string, includeInternal bool) (string, error) {
	ccPath, err := libopustooling.FindCCompiler()
	if err != nil {
		return "", fmt.Errorf("cc not available: %w", err)
	}

	repoRoot, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}

	sourceDir, buildDir, err := ensureLibopusOSCEBuild(repoRoot)
	if err != nil {
		return "", err
	}

	srcPath := filepath.Join(repoRoot, "tools", "csrc", sourceFile)
	if _, err := os.Stat(srcPath); err != nil {
		return "", fmt.Errorf("osce helper source not found: %w", err)
	}

	libopusStatic := filepath.Join(buildDir, ".libs", "libopus.a")
	if _, err := os.Stat(libopusStatic); err != nil {
		return "", fmt.Errorf("osce libopus static library not found: %w", err)
	}

	outPath := filepath.Join(buildDir, fmt.Sprintf("%s_%s_%s", outputBase, runtime.GOOS, runtime.GOARCH))
	if runtime.GOOS == "windows" {
		outPath += ".exe"
	}

	args := []string{
		"-std=c99",
		"-O2",
		"-DHAVE_CONFIG_H",
		"-I", buildDir,
		"-I", filepath.Join(sourceDir, "include"),
	}
	if includeInternal {
		args = append(args,
			"-I", sourceDir,
			"-I", filepath.Join(sourceDir, "src"),
			"-I", filepath.Join(sourceDir, "celt"),
			"-I", filepath.Join(sourceDir, "dnn"),
			"-I", filepath.Join(sourceDir, "silk"),
		)
	}
	args = append(args, srcPath, libopusStatic, "-lm", "-o", outPath)

	cmd := exec.Command(ccPath, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build osce helper %s: %w (%s)", sourceFile, err, bytes.TrimSpace(output))
	}
	return outPath, nil
}
