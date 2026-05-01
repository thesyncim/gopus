package gopus

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

var (
	defaultLibopusDNNBuildOnce      sync.Once
	defaultLibopusDNNBuildSourceDir string
	defaultLibopusDNNBuildDir       string
	defaultLibopusDNNBuildErr       error

	defaultLibopusPitchDNNBlobHelperOnce sync.Once
	defaultLibopusPitchDNNBlobHelperPath string
	defaultLibopusPitchDNNBlobHelperErr  error

	defaultLibopusPLCBlobHelperOnce sync.Once
	defaultLibopusPLCBlobHelperPath string
	defaultLibopusPLCBlobHelperErr  error

	defaultLibopusFARGANBlobHelperOnce sync.Once
	defaultLibopusFARGANBlobHelperPath string
	defaultLibopusFARGANBlobHelperErr  error

	defaultLibopusDREDEncoderBlobHelperOnce sync.Once
	defaultLibopusDREDEncoderBlobHelperPath string
	defaultLibopusDREDEncoderBlobHelperErr  error
)

func TestDNNBlobControlAcceptsLibopusModelBlobs(t *testing.T) {
	if os.Getenv("GOPUS_STRICT_LIBOPUS_REF") != "1" {
		t.Skip("requires GOPUS_STRICT_LIBOPUS_REF=1")
	}

	encoderBlob := requireDefaultLibopusEncoderDNNBlob(t)
	decoderBlob := requireDefaultLibopusDecoderDNNBlob(t)

	encoderParsed, err := dnnblob.Clone(encoderBlob)
	if err != nil {
		t.Fatalf("Clone(libopus encoder blob) error: %v", err)
	}
	if err := encoderParsed.ValidateEncoderControl(); err != nil {
		t.Fatalf("ValidateEncoderControl(libopus encoder blob) error: %v", err)
	}
	if !encoderParsed.SupportsPitchDNN() || !encoderParsed.SupportsDREDEncoder() {
		t.Fatal("libopus encoder blob missing expected PitchDNN or DRED encoder families")
	}

	decoderParsed, err := dnnblob.Clone(decoderBlob)
	if err != nil {
		t.Fatalf("Clone(libopus decoder blob) error: %v", err)
	}
	if err := decoderParsed.ValidateDecoderControl(false); err != nil {
		t.Fatalf("ValidateDecoderControl(libopus decoder blob) error: %v", err)
	}
	models := decoderParsed.DecoderModels()
	if !models.PitchDNN || !models.PLC || !models.FARGAN {
		t.Fatalf("libopus decoder blob model families=%+v, want PitchDNN/PLC/FARGAN", models)
	}
	if models.DRED || models.OSCE || models.OSCEBWE {
		t.Fatalf("default libopus decoder blob unexpectedly reports quarantine-only families: %+v", models)
	}

	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)
	if err := enc.SetDNNBlob(encoderBlob); err != nil {
		t.Fatalf("Encoder.SetDNNBlob(libopus encoder blob) error: %v", err)
	}
	if enc.dnnBlob == nil || !enc.enc.DNNBlobLoaded() {
		t.Fatal("encoder did not retain libopus encoder DNN blob")
	}
	rejectEnc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)
	if err := rejectEnc.SetDNNBlob(decoderBlob); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Encoder.SetDNNBlob(libopus decoder blob) error=%v want %v", err, ErrInvalidArgument)
	}

	dec := newMonoTestDecoder(t)
	if err := dec.SetDNNBlob(decoderBlob); err != nil {
		t.Fatalf("Decoder.SetDNNBlob(libopus decoder blob) error: %v", err)
	}
	if dec.dnnBlob == nil || !dec.pitchDNNLoaded || !dec.plcModelLoaded || !dec.farganModelLoaded {
		t.Fatal("decoder did not retain libopus decoder DNN blob model state")
	}
	if dec.dredState() != nil {
		t.Fatalf("default decoder SetDNNBlob allocated DRED sidecar: %+v", dec.dredState())
	}
	rejectDec := newMonoTestDecoder(t)
	if err := rejectDec.SetDNNBlob(encoderBlob); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Decoder.SetDNNBlob(libopus encoder blob) error=%v want %v", err, ErrInvalidArgument)
	}

	msEnc := mustNewDefaultMultistreamEncoder(t, 48000, 2, ApplicationAudio)
	if err := msEnc.SetDNNBlob(encoderBlob); err != nil {
		t.Fatalf("MultistreamEncoder.SetDNNBlob(libopus encoder blob) error: %v", err)
	}
	if msEnc.dnnBlob == nil || !msEnc.enc.DNNBlobLoaded() {
		t.Fatal("multistream encoder did not retain libopus encoder DNN blob")
	}
	rejectMSEnc := mustNewDefaultMultistreamEncoder(t, 48000, 2, ApplicationAudio)
	if err := rejectMSEnc.SetDNNBlob(decoderBlob); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("MultistreamEncoder.SetDNNBlob(libopus decoder blob) error=%v want %v", err, ErrInvalidArgument)
	}

	msDec := mustNewDefaultMultistreamDecoder(t, 48000, 2)
	if err := msDec.SetDNNBlob(decoderBlob); err != nil {
		t.Fatalf("MultistreamDecoder.SetDNNBlob(libopus decoder blob) error: %v", err)
	}
	if msDec.dnnBlob == nil || !msDec.dec.PitchDNNLoaded() || !msDec.dec.PLCModelLoaded() || !msDec.dec.FARGANModelLoaded() {
		t.Fatal("multistream decoder did not retain libopus decoder DNN blob model state")
	}
	rejectMSDec := mustNewDefaultMultistreamDecoder(t, 48000, 2)
	if err := rejectMSDec.SetDNNBlob(encoderBlob); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("MultistreamDecoder.SetDNNBlob(libopus encoder blob) error=%v want %v", err, ErrInvalidArgument)
	}
}

func requireDefaultLibopusEncoderDNNBlob(t *testing.T) []byte {
	t.Helper()
	blob, err := probeDefaultLibopusEncoderDNNBlob()
	if err != nil {
		t.Fatalf("libopus encoder DNN blob helper unavailable: %v", err)
	}
	return blob
}

func requireDefaultLibopusDecoderDNNBlob(t *testing.T) []byte {
	t.Helper()
	blob, err := probeDefaultLibopusDecoderDNNBlob()
	if err != nil {
		t.Fatalf("libopus decoder DNN blob helper unavailable: %v", err)
	}
	return blob
}

func probeDefaultLibopusEncoderDNNBlob() ([]byte, error) {
	pitchBlob, err := runDefaultLibopusPitchDNNBlobHelper()
	if err != nil {
		return nil, err
	}
	dredEncoderBlob, err := runDefaultLibopusDREDEncoderBlobHelper()
	if err != nil {
		return nil, err
	}
	blob := make([]byte, 0, len(pitchBlob)+len(dredEncoderBlob))
	blob = append(blob, pitchBlob...)
	blob = append(blob, dredEncoderBlob...)
	return blob, nil
}

func probeDefaultLibopusDecoderDNNBlob() ([]byte, error) {
	pitchBlob, err := runDefaultLibopusPitchDNNBlobHelper()
	if err != nil {
		return nil, err
	}
	plcBlob, err := runDefaultLibopusPLCBlobHelper()
	if err != nil {
		return nil, err
	}
	farganBlob, err := runDefaultLibopusFARGANBlobHelper()
	if err != nil {
		return nil, err
	}
	blob := make([]byte, 0, len(pitchBlob)+len(plcBlob)+len(farganBlob))
	blob = append(blob, pitchBlob...)
	blob = append(blob, plcBlob...)
	blob = append(blob, farganBlob...)
	return blob, nil
}

func runDefaultLibopusPitchDNNBlobHelper() ([]byte, error) {
	defaultLibopusPitchDNNBlobHelperOnce.Do(func() {
		defaultLibopusPitchDNNBlobHelperPath, defaultLibopusPitchDNNBlobHelperErr = buildDefaultLibopusDNNHelper("libopus_pitchdnn_model_blob.c", "gopus_default_libopus_pitchdnn_model_blob")
	})
	if defaultLibopusPitchDNNBlobHelperErr != nil {
		return nil, defaultLibopusPitchDNNBlobHelperErr
	}
	return runDefaultLibopusDNNBlobHelper(defaultLibopusPitchDNNBlobHelperPath)
}

func runDefaultLibopusPLCBlobHelper() ([]byte, error) {
	defaultLibopusPLCBlobHelperOnce.Do(func() {
		defaultLibopusPLCBlobHelperPath, defaultLibopusPLCBlobHelperErr = buildDefaultLibopusDNNHelper("libopus_plc_model_blob.c", "gopus_default_libopus_plc_model_blob")
	})
	if defaultLibopusPLCBlobHelperErr != nil {
		return nil, defaultLibopusPLCBlobHelperErr
	}
	return runDefaultLibopusDNNBlobHelper(defaultLibopusPLCBlobHelperPath)
}

func runDefaultLibopusFARGANBlobHelper() ([]byte, error) {
	defaultLibopusFARGANBlobHelperOnce.Do(func() {
		defaultLibopusFARGANBlobHelperPath, defaultLibopusFARGANBlobHelperErr = buildDefaultLibopusDNNHelper("libopus_fargan_model_blob.c", "gopus_default_libopus_fargan_model_blob")
	})
	if defaultLibopusFARGANBlobHelperErr != nil {
		return nil, defaultLibopusFARGANBlobHelperErr
	}
	return runDefaultLibopusDNNBlobHelper(defaultLibopusFARGANBlobHelperPath)
}

func runDefaultLibopusDREDEncoderBlobHelper() ([]byte, error) {
	defaultLibopusDREDEncoderBlobHelperOnce.Do(func() {
		defaultLibopusDREDEncoderBlobHelperPath, defaultLibopusDREDEncoderBlobHelperErr = buildDefaultLibopusDNNHelper("libopus_dred_encoder_model_blob.c", "gopus_default_libopus_dred_encoder_model_blob")
	})
	if defaultLibopusDREDEncoderBlobHelperErr != nil {
		return nil, defaultLibopusDREDEncoderBlobHelperErr
	}
	return runDefaultLibopusDNNBlobHelper(defaultLibopusDREDEncoderBlobHelperPath)
}

func runDefaultLibopusDNNBlobHelper(binPath string) ([]byte, error) {
	cmd := exec.Command(binPath)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run model blob helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return stdout.Bytes(), nil
}

func buildDefaultLibopusDNNHelper(sourceFile, outputBase string) (string, error) {
	ccPath, err := libopustooling.FindCCompiler()
	if err != nil {
		return "", fmt.Errorf("cc not available: %w", err)
	}

	repoRoot, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	repoRoot = filepath.Clean(repoRoot)

	sourceDir, buildDir, err := ensureDefaultLibopusDNNBuild(repoRoot)
	if err != nil {
		return "", err
	}

	srcPath := filepath.Join(repoRoot, "tools", "csrc", sourceFile)
	if _, err := os.Stat(srcPath); err != nil {
		return "", fmt.Errorf("DNN blob helper source not found: %w", err)
	}

	libopusStatic := filepath.Join(buildDir, ".libs", "libopus.a")
	if _, err := os.Stat(libopusStatic); err != nil {
		return "", fmt.Errorf("DNN blob libopus static library not found: %w", err)
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
		"-I", sourceDir,
		"-I", filepath.Join(sourceDir, "src"),
		"-I", filepath.Join(sourceDir, "celt"),
		"-I", filepath.Join(sourceDir, "dnn"),
		"-I", filepath.Join(sourceDir, "silk"),
		srcPath,
		libopusStatic,
		"-lm",
		"-o",
		outPath,
	}
	cmd := exec.Command(ccPath, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build DNN blob helper %s: %w (%s)", sourceFile, err, bytes.TrimSpace(output))
	}
	return outPath, nil
}

func ensureDefaultLibopusDNNBuild(repoRoot string) (sourceDir, buildDir string, err error) {
	defaultLibopusDNNBuildOnce.Do(func() {
		sourceDir, buildDir, err := prepareDefaultLibopusDNNBuild(repoRoot)
		defaultLibopusDNNBuildSourceDir = sourceDir
		defaultLibopusDNNBuildDir = buildDir
		defaultLibopusDNNBuildErr = err
	})
	if defaultLibopusDNNBuildErr != nil {
		return "", "", defaultLibopusDNNBuildErr
	}
	return defaultLibopusDNNBuildSourceDir, defaultLibopusDNNBuildDir, nil
}

func prepareDefaultLibopusDNNBuild(repoRoot string) (sourceDir, buildDir string, err error) {
	referenceDir := filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion)
	sourceDir = filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion+"-dredsrc-clean")
	buildDir = filepath.Join(repoRoot, "tmp_check", fmt.Sprintf("build-opus-dred-scalar-%s-%s", runtime.GOOS, runtime.GOARCH))
	libopusStatic := filepath.Join(buildDir, ".libs", "libopus.a")
	if _, err := os.Stat(libopusStatic); err == nil && libopustooling.ScalarDNNBuildIsCurrent(buildDir) {
		return sourceDir, buildDir, nil
	}

	if _, err := os.Stat(filepath.Join(sourceDir, "configure")); err != nil {
		libopustooling.EnsureLibopus(libopustooling.DefaultVersion, []string{repoRoot})
		tarball := filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion+".tar.gz")
		if _, err := os.Stat(tarball); err == nil {
			if err := os.RemoveAll(sourceDir); err != nil {
				return "", "", fmt.Errorf("remove stale DNN source dir: %w", err)
			}
			if err := os.MkdirAll(sourceDir, 0o755); err != nil {
				return "", "", fmt.Errorf("mkdir DNN source dir: %w", err)
			}
			cmd := exec.Command("tar", "-xzf", tarball, "-C", sourceDir, "--strip-components=1")
			if output, err := cmd.CombinedOutput(); err != nil {
				return "", "", fmt.Errorf("extract DNN libopus source: %w (%s)", err, bytes.TrimSpace(output))
			}
		} else if _, refErr := os.Stat(filepath.Join(referenceDir, "configure")); refErr == nil {
			if _, cfgErr := os.Stat(filepath.Join(referenceDir, "Makefile")); cfgErr == nil {
				return "", "", fmt.Errorf("clean DNN source tree unavailable: %s is already configured", referenceDir)
			}
			sourceDir = referenceDir
		} else {
			return "", "", fmt.Errorf("libopus tarball not found and no prepared source tree present: %w", err)
		}
	}

	if err := libopustooling.ResetScalarDNNBuildIfStale(buildDir); err != nil {
		return "", "", fmt.Errorf("reset stale DNN scalar build dir: %w", err)
	}
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return "", "", fmt.Errorf("mkdir DNN build dir: %w", err)
	}

	if _, err := os.Stat(filepath.Join(buildDir, "Makefile")); err != nil {
		cmd := exec.Command(filepath.Join(sourceDir, "configure"),
			"--enable-static",
			"--disable-shared",
			"--disable-extra-programs",
			"--enable-dred",
			"--disable-asm",
			"--disable-rtcd",
			"--disable-intrinsics",
		)
		cmd.Dir = buildDir
		cmd.Env = libopustooling.ScalarDNNBuildEnv()
		if output, err := cmd.CombinedOutput(); err != nil {
			return "", "", fmt.Errorf("configure DNN libopus build: %w (%s)", err, bytes.TrimSpace(output))
		}
	}

	makeCmd := exec.Command("make", fmt.Sprintf("-j%d", max(1, runtime.NumCPU())))
	makeCmd.Dir = buildDir
	makeCmd.Env = libopustooling.ScalarDNNBuildEnv()
	if output, err := makeCmd.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("build DNN libopus: %w (%s)", err, bytes.TrimSpace(output))
	}
	if err := libopustooling.WriteScalarDNNBuildStamp(buildDir); err != nil {
		return "", "", fmt.Errorf("write DNN scalar build stamp: %w", err)
	}

	return sourceDir, buildDir, nil
}
