package testvectors

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

	"github.com/thesyncim/gopus/internal/libopustooling"
)

var (
	libopusRefdecodeSingleOnce sync.Once
	libopusRefdecodeSinglePath string
	libopusRefdecodeSingleErr  error
)

func getLibopusRefdecodeSinglePath() (string, error) {
	libopusRefdecodeSingleOnce.Do(func() {
		ccPath, err := exec.LookPath("cc")
		if err != nil {
			libopusRefdecodeSingleErr = fmt.Errorf("cc not available: %w", err)
			return
		}

		opusDemo, ok := libopustooling.FindOrEnsureOpusDemo(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots())
		if !ok {
			libopusRefdecodeSingleErr = fmt.Errorf("libopus reference tree not found")
			return
		}
		opusRoot := filepath.Dir(opusDemo)
		repoRoot := filepath.Clean(filepath.Join(opusRoot, "..", ".."))

		srcPath := filepath.Join(repoRoot, "tools", "csrc", "libopus_refdecode_single.c")
		if _, err := os.Stat(srcPath); err != nil {
			libopusRefdecodeSingleErr = fmt.Errorf("refdecode source not found: %w", err)
			return
		}

		libopusStatic := filepath.Join(opusRoot, ".libs", "libopus.a")
		if _, err := os.Stat(libopusStatic); err != nil {
			libopusRefdecodeSingleErr = fmt.Errorf("libopus static library not found: %w", err)
			return
		}

		outPath := filepath.Join(os.TempDir(), fmt.Sprintf("gopus_libopus_refdecode_single_%s_%s", runtime.GOOS, runtime.GOARCH))
		args := []string{
			"-std=c99",
			"-O2",
			"-I", filepath.Join(opusRoot, "include"),
			srcPath,
			libopusStatic,
			"-lm",
			"-o", outPath,
		}
		cmd := exec.Command(ccPath, args...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			libopusRefdecodeSingleErr = fmt.Errorf("build libopus single refdecode helper failed: %w (%s)", err, bytes.TrimSpace(output))
			return
		}

		libopusRefdecodeSinglePath = outPath
	})
	if libopusRefdecodeSingleErr != nil {
		return "", libopusRefdecodeSingleErr
	}
	return libopusRefdecodeSinglePath, nil
}

func decodeWithLibopusReferencePacketsSingle(channels, frameSize int, packets [][]byte) ([]float32, error) {
	binPath, err := getLibopusRefdecodeSinglePath()
	if err != nil {
		return nil, err
	}
	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("unsupported single-stream channel count: %d", channels)
	}

	var payload bytes.Buffer
	payload.WriteString("GOSI")
	for _, v := range []uint32{
		1,
		uint32(channels),
		uint32(frameSize),
		uint32(len(packets)),
	} {
		if err := binary.Write(&payload, binary.LittleEndian, v); err != nil {
			return nil, fmt.Errorf("encode helper header: %w", err)
		}
	}
	for _, packet := range packets {
		if err := binary.Write(&payload, binary.LittleEndian, uint32(len(packet))); err != nil {
			return nil, fmt.Errorf("encode packet length: %w", err)
		}
		if _, err := payload.Write(packet); err != nil {
			return nil, fmt.Errorf("encode packet bytes: %w", err)
		}
	}

	cmd := exec.Command(binPath)
	cmd.Stdin = bytes.NewReader(payload.Bytes())
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("libopus single reference decode failed: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}

	outBytes := out.Bytes()
	if len(outBytes) < 8 {
		return nil, fmt.Errorf("libopus single reference decode produced short output")
	}
	if string(outBytes[:4]) != "GOSO" {
		return nil, fmt.Errorf("libopus single reference decode output magic mismatch")
	}
	nSamples := int(binary.LittleEndian.Uint32(outBytes[4:8]))
	pcmBytes := outBytes[8:]
	if len(pcmBytes) != nSamples*4 {
		return nil, fmt.Errorf("libopus single reference decode output size mismatch: samples=%d bytes=%d", nSamples, len(pcmBytes))
	}

	decoded := make([]float32, nSamples)
	for i := 0; i < nSamples; i++ {
		decoded[i] = math.Float32frombits(binary.LittleEndian.Uint32(pcmBytes[i*4 : i*4+4]))
	}
	return decoded, nil
}
