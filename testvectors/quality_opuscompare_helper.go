package testvectors

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

const (
	opusCompareHelperInputMagic  = "GOCI"
	opusCompareHelperOutputMagic = "GOCO"
)

type opusCompareHelperProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr bytes.Buffer
}

var (
	opusCompareHelperPathOnce sync.Once
	opusCompareHelperPath     string
	opusCompareHelperPathErr  error

	opusCompareHelperMu sync.Mutex
	opusCompareHelper   *opusCompareHelperProcess
)

func getOpusCompareHelperPath() (string, error) {
	opusCompareHelperPathOnce.Do(func() {
		ccPath, err := exec.LookPath("cc")
		if err != nil {
			opusCompareHelperPathErr = fmt.Errorf("cc not available: %w", err)
			return
		}

		opusCompare, ok := libopustooling.FindOrEnsureOpusCompare(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots())
		if !ok {
			opusCompareHelperPathErr = fmt.Errorf("opus_compare not found in pinned libopus tree")
			return
		}
		opusRoot := filepath.Dir(opusCompare)
		repoRoot := filepath.Clean(filepath.Join(opusRoot, "..", ".."))

		srcPath := filepath.Join(repoRoot, "tools", "csrc", "libopus_compare_single.c")
		if _, err := os.Stat(srcPath); err != nil {
			opusCompareHelperPathErr = fmt.Errorf("compare helper source not found: %w", err)
			return
		}

		outPath := filepath.Join(os.TempDir(), fmt.Sprintf("gopus_libopus_compare_single_%s_%s", runtime.GOOS, runtime.GOARCH))
		args := []string{
			"-std=c99",
			"-O2",
			srcPath,
			"-lm",
			"-o", outPath,
		}
		cmd := exec.Command(ccPath, args...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			opusCompareHelperPathErr = fmt.Errorf("build opus_compare helper failed: %w (%s)", err, bytes.TrimSpace(output))
			return
		}

		opusCompareHelperPath = outPath
	})
	if opusCompareHelperPathErr != nil {
		return "", opusCompareHelperPathErr
	}
	return opusCompareHelperPath, nil
}

func startOpusCompareHelperLocked() (*opusCompareHelperProcess, error) {
	if opusCompareHelper != nil {
		return opusCompareHelper, nil
	}

	binPath, err := getOpusCompareHelperPath()
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(binPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open compare helper stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open compare helper stdout: %w", err)
	}

	proc := &opusCompareHelperProcess{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
	}
	cmd.Stderr = &proc.stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("start compare helper: %w", err)
	}

	opusCompareHelper = proc
	return proc, nil
}

func closeOpusCompareHelperLocked() {
	if opusCompareHelper == nil {
		return
	}

	proc := opusCompareHelper
	opusCompareHelper = nil

	_ = proc.stdin.Close()
	_ = proc.stdout.Close()
	if proc.cmd.Process != nil {
		_ = proc.cmd.Process.Kill()
	}
	_ = proc.cmd.Wait()
}

func opusCompareHelperError(proc *opusCompareHelperProcess, err error) error {
	stderr := bytes.TrimSpace(proc.stderr.Bytes())
	if len(stderr) == 0 {
		return err
	}
	return fmt.Errorf("%w (%s)", err, stderr)
}

func runOpusCompareHelper(reference, decoded []int16, sampleRate, channels int, delays []int) (float64, int, error) {
	for attempt := 0; attempt < 2; attempt++ {
		opusCompareHelperMu.Lock()
		proc, err := startOpusCompareHelperLocked()
		if err != nil {
			opusCompareHelperMu.Unlock()
			return 0, 0, err
		}

		var payload bytes.Buffer
		payload.WriteString(opusCompareHelperInputMagic)
		for _, v := range []uint32{
			1,
			uint32(sampleRate),
			uint32(channels),
			uint32(len(reference)),
			uint32(len(decoded)),
			uint32(len(delays)),
		} {
			if err := binary.Write(&payload, binary.LittleEndian, v); err != nil {
				opusCompareHelperMu.Unlock()
				return 0, 0, fmt.Errorf("encode compare helper header: %w", err)
			}
		}
		for _, s := range reference {
			if err := binary.Write(&payload, binary.LittleEndian, s); err != nil {
				opusCompareHelperMu.Unlock()
				return 0, 0, fmt.Errorf("encode reference pcm: %w", err)
			}
		}
		for _, s := range decoded {
			if err := binary.Write(&payload, binary.LittleEndian, s); err != nil {
				opusCompareHelperMu.Unlock()
				return 0, 0, fmt.Errorf("encode decoded pcm: %w", err)
			}
		}
		for _, delay := range delays {
			if err := binary.Write(&payload, binary.LittleEndian, int32(delay)); err != nil {
				opusCompareHelperMu.Unlock()
				return 0, 0, fmt.Errorf("encode compare delay: %w", err)
			}
		}

		if _, err := proc.stdin.Write(payload.Bytes()); err != nil {
			err = opusCompareHelperError(proc, fmt.Errorf("write compare helper request: %w", err))
			closeOpusCompareHelperLocked()
			opusCompareHelperMu.Unlock()
			if attempt == 0 {
				continue
			}
			return 0, 0, err
		}

		var response [16]byte
		if _, err := io.ReadFull(proc.stdout, response[:]); err != nil {
			err = opusCompareHelperError(proc, fmt.Errorf("read compare helper response: %w", err))
			closeOpusCompareHelperLocked()
			opusCompareHelperMu.Unlock()
			if attempt == 0 {
				continue
			}
			return 0, 0, err
		}
		opusCompareHelperMu.Unlock()

		if string(response[:4]) != opusCompareHelperOutputMagic {
			opusCompareHelperMu.Lock()
			err := opusCompareHelperError(proc, fmt.Errorf("compare helper output magic mismatch"))
			closeOpusCompareHelperLocked()
			opusCompareHelperMu.Unlock()
			if attempt == 0 {
				continue
			}
			return 0, 0, err
		}

		bestDelay := int(int32(binary.LittleEndian.Uint32(response[4:8])))
		bestQ := math.Float64frombits(binary.LittleEndian.Uint64(response[8:16]))
		return bestQ, bestDelay, nil
	}

	return 0, 0, fmt.Errorf("compare helper exhausted retries")
}
