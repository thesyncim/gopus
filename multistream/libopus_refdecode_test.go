package multistream

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
	libopusRefdecodeOnce sync.Once
	libopusRefdecodePath string
	libopusRefdecodeErr  error
)

func getLibopusRefdecodePath() (string, error) {
	libopusRefdecodeOnce.Do(func() {
		ccPath, err := exec.LookPath("cc")
		if err != nil {
			libopusRefdecodeErr = fmt.Errorf("cc not available: %w", err)
			return
		}

		opusDemo, ok := libopustooling.FindOrEnsureOpusDemo(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots())
		if !ok {
			libopusRefdecodeErr = fmt.Errorf("libopus reference tree not found")
			return
		}
		opusRoot := filepath.Dir(opusDemo)
		repoRoot := filepath.Clean(filepath.Join(opusRoot, "..", ".."))

		srcPath := filepath.Join(repoRoot, "tools", "csrc", "libopus_refdecode_multistream.c")
		if _, err := os.Stat(srcPath); err != nil {
			libopusRefdecodeErr = fmt.Errorf("refdecode source not found: %w", err)
			return
		}

		libopusStatic := filepath.Join(opusRoot, ".libs", "libopus.a")
		if _, err := os.Stat(libopusStatic); err != nil {
			libopusRefdecodeErr = fmt.Errorf("libopus static library not found: %w", err)
			return
		}

		outPath := filepath.Join(os.TempDir(), fmt.Sprintf("gopus_libopus_refdecode_%s_%s", runtime.GOOS, runtime.GOARCH))
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
			libopusRefdecodeErr = fmt.Errorf("build libopus refdecode helper failed: %w (%s)", err, bytes.TrimSpace(output))
			return
		}

		libopusRefdecodePath = outPath
	})
	if libopusRefdecodeErr != nil {
		return "", libopusRefdecodeErr
	}
	return libopusRefdecodePath, nil
}

func decodeWithLibopusReferencePackets(
	mappingFamily, channels, streams, coupled, frameSize int,
	mapping []byte,
	demixingMatrix []byte,
	packets [][]byte,
) ([]float32, error) {
	binPath, err := getLibopusRefdecodePath()
	if err != nil {
		return nil, err
	}

	if mappingFamily < 1 || mappingFamily > 3 {
		return nil, fmt.Errorf("unsupported mapping family: %d", mappingFamily)
	}

	var payload bytes.Buffer
	payload.WriteString("GMSI")
	for _, v := range []uint32{
		1,
		uint32(mappingFamily),
		uint32(channels),
		uint32(streams),
		uint32(coupled),
		uint32(frameSize),
		uint32(len(packets)),
		uint32(len(mapping)),
		uint32(len(demixingMatrix)),
	} {
		if err := binary.Write(&payload, binary.LittleEndian, v); err != nil {
			return nil, fmt.Errorf("encode helper header: %w", err)
		}
	}
	if _, err := payload.Write(mapping); err != nil {
		return nil, fmt.Errorf("encode mapping: %w", err)
	}
	if _, err := payload.Write(demixingMatrix); err != nil {
		return nil, fmt.Errorf("encode demixing matrix: %w", err)
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
		return nil, fmt.Errorf("libopus reference decode failed: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}

	outBytes := out.Bytes()
	if len(outBytes) < 8 {
		return nil, fmt.Errorf("libopus reference decode produced short output")
	}
	if string(outBytes[:4]) != "GMSO" {
		return nil, fmt.Errorf("libopus reference decode output magic mismatch")
	}

	nSamples := int(binary.LittleEndian.Uint32(outBytes[4:8]))
	pcmBytes := outBytes[8:]
	if len(pcmBytes) != nSamples*4 {
		return nil, fmt.Errorf("libopus reference decode output size mismatch: samples=%d bytes=%d", nSamples, len(pcmBytes))
	}

	decoded := make([]float32, nSamples)
	for i := 0; i < nSamples; i++ {
		decoded[i] = math.Float32frombits(binary.LittleEndian.Uint32(pcmBytes[i*4 : i*4+4]))
	}
	return decoded, nil
}
