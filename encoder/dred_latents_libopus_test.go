//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package encoder

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

var (
	encoderLibopusDREDBuildOnce sync.Once
	encoderLibopusDREDRepoRoot  string
	encoderLibopusDREDSourceDir string
	encoderLibopusDREDBuildDir  string
	encoderLibopusDREDBuildErr  error

	encoderLibopusLatentsTraceOnce sync.Once
	encoderLibopusLatentsTracePath string
	encoderLibopusLatentsTraceErr  error

	encoderLibopusPitchBlobOnce sync.Once
	encoderLibopusPitchBlob     []byte
	encoderLibopusPitchBlobErr  error

	encoderLibopusDREDBlobOnce sync.Once
	encoderLibopusDREDBlob     []byte
	encoderLibopusDREDBlobErr  error
)

type encoderLibopusDREDFrameTrace struct {
	frameIdx    int
	latentsFill int
	dredOffset  int
	latentOff   int
	latents     [][rdovae.LatentDim]float32
}

func TestEncoderDREDInitialLatentsTraceMatchesLibopus(t *testing.T) {
	for _, channels := range []int{1, 2} {
		for _, frameSize := range []int{960, 1920, 2880} {
			channels := channels
			frameSize := frameSize
			t.Run(fmt.Sprintf("%dch_%d", channels, frameSize), func(t *testing.T) {
				const (
					sampleRate = 48000
					extraDelay = sampleRate / 250
				)

				want := probeEncoderLibopusDREDLatentsTrace(t, channels, frameSize)
				if len(want) == 0 {
					t.Fatal("libopus DRED latents trace is empty")
				}
				blob := requireEncoderLibopusNeuralModelBlob(t)
				parsed, err := dnnblob.Clone(blob)
				if err != nil {
					t.Fatalf("Clone libopus encoder model blob: %v", err)
				}

				enc := NewEncoder(sampleRate, channels)
				enc.SetDNNBlob(parsed)
				if err := enc.SetDREDDuration(4); err != nil {
					t.Fatalf("SetDREDDuration error: %v", err)
				}

				got := make([]encoderLibopusDREDFrameTrace, len(want))
				for frameIdx := range want {
					frame := encoderLibopusDREDTraceFrame(frameIdx, frameSize, sampleRate, channels)
					emitted := enc.processDREDLatents(frame, extraDelay)
					if emitted == 0 {
						t.Fatalf("frame %d processDREDLatents emitted 0", frameIdx)
					}
					got[frameIdx] = snapshotEncoderDREDTrace(t, enc, frameIdx)
				}

				compareEncoderDREDTraces(t, got, want)
			})
		}
	}
}

func TestEncoderDREDLongCELTLatentsUseLibopusSubframeCadence(t *testing.T) {
	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			const (
				sampleRate = 48000
				frameSize  = 2880
				chunkSize  = 960
				extraDelay = sampleRate / 250
			)

			want := probeEncoderLibopusDREDLatentsTraceWithChunkSize(t, channels, frameSize, chunkSize)
			if len(want) == 0 {
				t.Fatal("libopus DRED latents trace is empty")
			}
			blob := requireEncoderLibopusNeuralModelBlob(t)
			parsed, err := dnnblob.Clone(blob)
			if err != nil {
				t.Fatalf("Clone libopus encoder model blob: %v", err)
			}

			enc := NewEncoder(sampleRate, channels)
			enc.SetDNNBlob(parsed)
			if err := enc.SetDREDDuration(4); err != nil {
				t.Fatalf("SetDREDDuration error: %v", err)
			}

			got := make([]encoderLibopusDREDFrameTrace, len(want))
			for frameIdx := range want {
				frame := encoderLibopusDREDTraceFrame(frameIdx, frameSize, sampleRate, channels)
				emitted := enc.processDREDLatentsForPacket(frame, frameSize, extraDelay, ModeCELT)
				if emitted != frameSize/chunkSize {
					t.Fatalf("frame %d processDREDLatentsForPacket emitted %d want %d", frameIdx, emitted, frameSize/chunkSize)
				}
				got[frameIdx] = snapshotEncoderDREDTrace(t, enc, frameIdx)
			}

			compareEncoderDREDTraces(t, got, want)
		})
	}
}

func snapshotEncoderDREDTrace(t *testing.T, enc *Encoder, frameIdx int) encoderLibopusDREDFrameTrace {
	t.Helper()
	if enc.dred == nil || enc.dred.runtime == nil {
		t.Fatal("DRED runtime is nil")
	}
	rt := enc.dred.runtime
	count := rt.latentsFill
	if count > 4 {
		count = 4
	}
	trace := encoderLibopusDREDFrameTrace{
		frameIdx:    frameIdx,
		latentsFill: rt.latentsFill,
		dredOffset:  rt.dredOffset,
		latentOff:   rt.latentOffset,
		latents:     make([][rdovae.LatentDim]float32, count),
	}
	for pos := 0; pos < count; pos++ {
		copy(trace.latents[pos][:], rt.latentsBuffer[pos*rdovae.LatentDim:(pos+1)*rdovae.LatentDim])
	}
	return trace
}

func compareEncoderDREDTraces(t *testing.T, got, want []encoderLibopusDREDFrameTrace) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("trace count=%d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].frameIdx != want[i].frameIdx {
			t.Fatalf("trace %d frameIdx=%d want %d", i, got[i].frameIdx, want[i].frameIdx)
		}
		if got[i].latentsFill != want[i].latentsFill {
			t.Fatalf("frame %d latentsFill=%d want %d", i, got[i].latentsFill, want[i].latentsFill)
		}
		if got[i].dredOffset != want[i].dredOffset {
			t.Fatalf("frame %d dredOffset=%d want %d", i, got[i].dredOffset, want[i].dredOffset)
		}
		if got[i].latentOff != want[i].latentOff {
			t.Fatalf("frame %d latentOff=%d want %d", i, got[i].latentOff, want[i].latentOff)
		}
		if len(got[i].latents) != len(want[i].latents) {
			t.Fatalf("frame %d latent rows=%d want %d", i, len(got[i].latents), len(want[i].latents))
		}
		for pos := range want[i].latents {
			for k := 0; k < rdovae.LatentDim; k++ {
				if diff := math.Abs(float64(got[i].latents[pos][k] - want[i].latents[pos][k])); diff > 5e-3 {
					t.Fatalf("frame %d row %d k=%d latent=%v want %v diff=%v", i, pos, k, got[i].latents[pos][k], want[i].latents[pos][k], diff)
				}
			}
		}
	}
}

func encoderLibopusDREDTraceFrame(frameIdx, frameSize, sampleRate, channels int) []float64 {
	pcm := make([]float64, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		sample := float64(encoderLibopusDREDTraceSample(frameIdx, i, frameSize, sampleRate))
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = sample
		}
	}
	return pcm
}

func encoderLibopusDREDTraceSample(frameIdx, sampleIdx, frameSize, sampleRate int) float32 {
	n := frameIdx*frameSize + sampleIdx
	t := float64(n) / float64(sampleRate)
	env := 0.82 + 0.18*math.Sin(2*math.Pi*1.3*t)
	s := 0.0
	s += 0.28 * math.Sin(2*math.Pi*110*t)
	s += 0.17 * math.Sin(2*math.Pi*220*t+0.11)
	s += 0.09 * math.Sin(2*math.Pi*330*t+0.23)
	s += 0.05 * math.Sin(2*math.Pi*440*t+0.37)
	return float32(env * s)
}

func requireEncoderLibopusNeuralModelBlob(t *testing.T) []byte {
	t.Helper()
	pitchBlob := probeEncoderLibopusPitchDNNBlob(t)
	dredBlob := probeEncoderLibopusDREDEncoderBlob(t)
	blob := make([]byte, 0, len(pitchBlob)+len(dredBlob))
	blob = append(blob, pitchBlob...)
	blob = append(blob, dredBlob...)
	return blob
}

func probeEncoderLibopusPitchDNNBlob(t *testing.T) []byte {
	t.Helper()
	encoderLibopusPitchBlobOnce.Do(func() {
		binPath, err := buildEncoderLibopusDREDHelper("libopus_pitchdnn_model_blob.c", "gopus_encoder_libopus_pitchdnn_model_blob", true)
		if err != nil {
			encoderLibopusPitchBlobErr = err
			return
		}
		encoderLibopusPitchBlob, encoderLibopusPitchBlobErr = runEncoderLibopusModelBlobHelper(binPath)
	})
	if encoderLibopusPitchBlobErr != nil {
		t.Skipf("libopus pitch model blob helper unavailable: %v", encoderLibopusPitchBlobErr)
	}
	return encoderLibopusPitchBlob
}

func probeEncoderLibopusDREDEncoderBlob(t *testing.T) []byte {
	t.Helper()
	encoderLibopusDREDBlobOnce.Do(func() {
		binPath, err := buildEncoderLibopusDREDHelper("libopus_dred_encoder_model_blob.c", "gopus_encoder_libopus_dred_encoder_model_blob", true)
		if err != nil {
			encoderLibopusDREDBlobErr = err
			return
		}
		encoderLibopusDREDBlob, encoderLibopusDREDBlobErr = runEncoderLibopusModelBlobHelper(binPath)
	})
	if encoderLibopusDREDBlobErr != nil {
		t.Skipf("libopus DRED encoder model blob helper unavailable: %v", encoderLibopusDREDBlobErr)
	}
	return encoderLibopusDREDBlob
}

func probeEncoderLibopusDREDLatentsTrace(t *testing.T, channels, frameSize int) []encoderLibopusDREDFrameTrace {
	return probeEncoderLibopusDREDLatentsTraceWithChunkSize(t, channels, frameSize, frameSize)
}

func probeEncoderLibopusDREDLatentsTraceWithChunkSize(t *testing.T, channels, frameSize, chunkSize int) []encoderLibopusDREDFrameTrace {
	t.Helper()
	binPath, err := getEncoderLibopusDREDLatentsTracePath()
	if err != nil {
		t.Skipf("libopus DRED latents trace helper unavailable: %v", err)
	}
	cmd := exec.Command(binPath, fmt.Sprintf("%d", channels), fmt.Sprintf("%d", frameSize), fmt.Sprintf("%d", chunkSize))
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("run libopus DRED latents trace helper: %v (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return parseEncoderLibopusDREDLatentsTrace(t, stdout.Bytes())
}

func parseEncoderLibopusDREDLatentsTrace(t *testing.T, data []byte) []encoderLibopusDREDFrameTrace {
	t.Helper()
	var traces []encoderLibopusDREDFrameTrace
	for offset := 0; offset < len(data); {
		if len(data)-offset < 24 || string(data[offset:offset+4]) != "GDLT" {
			t.Fatalf("unexpected libopus trace output at offset %d", offset)
		}
		trace := encoderLibopusDREDFrameTrace{
			frameIdx:    int(binary.LittleEndian.Uint32(data[offset+4 : offset+8])),
			latentsFill: int(binary.LittleEndian.Uint32(data[offset+8 : offset+12])),
			dredOffset:  int(binary.LittleEndian.Uint32(data[offset+12 : offset+16])),
			latentOff:   int(binary.LittleEndian.Uint32(data[offset+16 : offset+20])),
		}
		positionCount := int(binary.LittleEndian.Uint32(data[offset+20 : offset+24]))
		pos := offset + 24
		trace.latents = make([][rdovae.LatentDim]float32, positionCount)
		for i := 0; i < positionCount; i++ {
			if len(data)-pos < 4*rdovae.LatentDim {
				t.Fatalf("truncated libopus trace at offset %d", pos)
			}
			for k := 0; k < rdovae.LatentDim; k++ {
				trace.latents[i][k] = math.Float32frombits(binary.LittleEndian.Uint32(data[pos+4*k : pos+4*(k+1)]))
			}
			pos += 4 * rdovae.LatentDim
		}
		traces = append(traces, trace)
		offset = pos
	}
	return traces
}

func getEncoderLibopusDREDLatentsTracePath() (string, error) {
	encoderLibopusLatentsTraceOnce.Do(func() {
		encoderLibopusLatentsTracePath, encoderLibopusLatentsTraceErr = buildEncoderLibopusDREDHelper("libopus_dred_latents_trace.c", "gopus_encoder_libopus_dred_latents_trace", true)
	})
	if encoderLibopusLatentsTraceErr != nil {
		return "", encoderLibopusLatentsTraceErr
	}
	return encoderLibopusLatentsTracePath, nil
}

func runEncoderLibopusModelBlobHelper(binPath string) ([]byte, error) {
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

func buildEncoderLibopusDREDHelper(sourceFile, outputBase string, includeInternal bool) (string, error) {
	ccPath, err := libopustooling.FindCCompiler()
	if err != nil {
		return "", fmt.Errorf("cc not available: %w", err)
	}
	sourceDir, buildDir, err := ensureEncoderLibopusDREDBuild()
	if err != nil {
		return "", err
	}
	srcPath := filepath.Join(encoderLibopusDREDRepoRoot, "tools", "csrc", sourceFile)
	if _, err := os.Stat(srcPath); err != nil {
		return "", fmt.Errorf("DRED helper source not found: %w", err)
	}
	libopusStatic := filepath.Join(buildDir, ".libs", "libopus.a")
	if _, err := os.Stat(libopusStatic); err != nil {
		return "", fmt.Errorf("DRED libopus static library not found: %w", err)
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
		return "", fmt.Errorf("build DRED helper %s: %w (%s)", sourceFile, err, bytes.TrimSpace(output))
	}
	return outPath, nil
}

func ensureEncoderLibopusDREDBuild() (sourceDir, buildDir string, err error) {
	encoderLibopusDREDBuildOnce.Do(func() {
		wd, err := os.Getwd()
		if err != nil {
			encoderLibopusDREDBuildErr = fmt.Errorf("getwd: %w", err)
			return
		}
		repoRoot := filepath.Clean(filepath.Join(wd, ".."))
		encoderLibopusDREDRepoRoot = repoRoot
		referenceDir := filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion)
		sourceDir = filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion+"-dredsrc-clean")
		buildDir = filepath.Join(repoRoot, "tmp_check", fmt.Sprintf("build-opus-dred-scalar-%s-%s", runtime.GOOS, runtime.GOARCH))
		libopusStatic := filepath.Join(buildDir, ".libs", "libopus.a")
		if _, err := os.Stat(libopusStatic); err == nil && libopustooling.ScalarDNNBuildIsCurrent(buildDir) {
			encoderLibopusDREDSourceDir = sourceDir
			encoderLibopusDREDBuildDir = buildDir
			return
		}

		if _, err := os.Stat(filepath.Join(sourceDir, "configure")); err != nil {
			libopustooling.EnsureLibopus(libopustooling.DefaultVersion, []string{repoRoot})
			tarball := filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion+".tar.gz")
			if _, err := os.Stat(tarball); err == nil {
				if err := os.RemoveAll(sourceDir); err != nil {
					encoderLibopusDREDBuildErr = fmt.Errorf("remove stale DRED source dir: %w", err)
					return
				}
				if err := os.MkdirAll(sourceDir, 0o755); err != nil {
					encoderLibopusDREDBuildErr = fmt.Errorf("mkdir DRED source dir: %w", err)
					return
				}
				cmd := exec.Command("tar", "-xzf", tarball, "-C", sourceDir, "--strip-components=1")
				if output, err := cmd.CombinedOutput(); err != nil {
					encoderLibopusDREDBuildErr = fmt.Errorf("extract DRED libopus source: %w (%s)", err, bytes.TrimSpace(output))
					return
				}
			} else if _, refErr := os.Stat(filepath.Join(referenceDir, "configure")); refErr == nil {
				if _, cfgErr := os.Stat(filepath.Join(referenceDir, "Makefile")); cfgErr == nil {
					encoderLibopusDREDBuildErr = fmt.Errorf("clean DRED source tree unavailable: %s is already configured", referenceDir)
					return
				}
				sourceDir = referenceDir
			} else {
				encoderLibopusDREDBuildErr = fmt.Errorf("libopus tarball not found and no prepared source tree present: %w", err)
				return
			}
		}

		if err := libopustooling.ResetScalarDNNBuildIfStale(buildDir); err != nil {
			encoderLibopusDREDBuildErr = fmt.Errorf("reset stale DRED scalar build dir: %w", err)
			return
		}
		if err := os.MkdirAll(buildDir, 0o755); err != nil {
			encoderLibopusDREDBuildErr = fmt.Errorf("mkdir DRED build dir: %w", err)
			return
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
				encoderLibopusDREDBuildErr = fmt.Errorf("configure DRED libopus build: %w (%s)", err, bytes.TrimSpace(output))
				return
			}
		}
		makeCmd := exec.Command("make", fmt.Sprintf("-j%d", max(1, runtime.NumCPU())))
		makeCmd.Dir = buildDir
		makeCmd.Env = libopustooling.ScalarDNNBuildEnv()
		if output, err := makeCmd.CombinedOutput(); err != nil {
			encoderLibopusDREDBuildErr = fmt.Errorf("build DRED libopus: %w (%s)", err, bytes.TrimSpace(output))
			return
		}
		if err := libopustooling.WriteScalarDNNBuildStamp(buildDir); err != nil {
			encoderLibopusDREDBuildErr = fmt.Errorf("write DRED scalar build stamp: %w", err)
			return
		}
		encoderLibopusDREDSourceDir = sourceDir
		encoderLibopusDREDBuildDir = buildDir
	})
	if encoderLibopusDREDBuildErr != nil {
		return "", "", encoderLibopusDREDBuildErr
	}
	return encoderLibopusDREDSourceDir, encoderLibopusDREDBuildDir, nil
}
