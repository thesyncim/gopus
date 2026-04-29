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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

const (
	opusCompareHelperInputMagic  = "GOCI"
	opusCompareHelperOutputMagic = "GOCO"
	opusCompareHelperReqTimeout  = 30 * time.Second
)

type opusCompareHelperProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr lockedBuffer
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) trimmedBytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.TrimSpace(append([]byte(nil), b.buf.Bytes()...))
}

var (
	opusCompareHelperPathOnce sync.Once
	opusCompareHelperPath     string
	opusCompareHelperPathErr  error

	opusCompareHelperPoolOnce sync.Once
	opusCompareHelperReqCh    chan opusCompareHelperRequest
)

type opusCompareHelperRequest struct {
	payload []byte
	reply   chan opusCompareHelperResponse
}

type opusCompareHelperResponse struct {
	bestQ     float64
	bestDelay int
	err       error
}

func getOpusCompareHelperPath() (string, error) {
	opusCompareHelperPathOnce.Do(func() {
		ccPath, err := libopustooling.FindCCompiler()
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

		outPath := helperBinaryPath("gopus_libopus_compare_single", runtime.GOOS, runtime.GOARCH)
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

func startOpusCompareHelperProcess() (*opusCompareHelperProcess, error) {
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
	return proc, nil
}

func closeOpusCompareHelperProcess(proc *opusCompareHelperProcess) {
	if proc == nil {
		return
	}

	_ = proc.stdin.Close()
	_ = proc.stdout.Close()
	if proc.cmd.Process != nil {
		_ = proc.cmd.Process.Kill()
	}
	_ = proc.cmd.Wait()
}

func opusCompareHelperError(proc *opusCompareHelperProcess, err error) error {
	stderr := proc.stderr.trimmedBytes()
	if len(stderr) == 0 {
		return err
	}
	return fmt.Errorf("%w (%s)", err, stderr)
}

func encodeOpusCompareHelperPayload(reference, decoded []int16, sampleRate, channels int, delays []int) ([]byte, error) {
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
			return nil, fmt.Errorf("encode compare helper header: %w", err)
		}
	}
	for _, s := range reference {
		if err := binary.Write(&payload, binary.LittleEndian, s); err != nil {
			return nil, fmt.Errorf("encode reference pcm: %w", err)
		}
	}
	for _, s := range decoded {
		if err := binary.Write(&payload, binary.LittleEndian, s); err != nil {
			return nil, fmt.Errorf("encode decoded pcm: %w", err)
		}
	}
	for _, delay := range delays {
		if err := binary.Write(&payload, binary.LittleEndian, int32(delay)); err != nil {
			return nil, fmt.Errorf("encode compare delay: %w", err)
		}
	}
	return payload.Bytes(), nil
}

func runOpusCompareHelperRequest(proc *opusCompareHelperProcess, payload []byte) (float64, int, error) {
	if _, err := proc.stdin.Write(payload); err != nil {
		return 0, 0, opusCompareHelperError(proc, fmt.Errorf("write compare helper request: %w", err))
	}

	var response [16]byte
	if _, err := io.ReadFull(proc.stdout, response[:]); err != nil {
		return 0, 0, opusCompareHelperError(proc, fmt.Errorf("read compare helper response: %w", err))
	}
	if string(response[:4]) != opusCompareHelperOutputMagic {
		return 0, 0, opusCompareHelperError(proc, fmt.Errorf("compare helper output magic mismatch"))
	}

	bestDelay := int(int32(binary.LittleEndian.Uint32(response[4:8])))
	bestQ := math.Float64frombits(binary.LittleEndian.Uint64(response[8:16]))
	return bestQ, bestDelay, nil
}

func runOpusCompareHelperRequestWithTimeout(proc *opusCompareHelperProcess, payload []byte, timeout time.Duration) (float64, int, error) {
	if timeout <= 0 {
		return runOpusCompareHelperRequest(proc, payload)
	}

	type helperResult struct {
		bestQ     float64
		bestDelay int
		err       error
	}

	done := make(chan helperResult, 1)
	go func() {
		bestQ, bestDelay, err := runOpusCompareHelperRequest(proc, payload)
		done <- helperResult{
			bestQ:     bestQ,
			bestDelay: bestDelay,
			err:       err,
		}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case res := <-done:
		return res.bestQ, res.bestDelay, res.err
	case <-timer.C:
		closeOpusCompareHelperProcess(proc)
		res := <-done
		if res.err != nil {
			return 0, 0, fmt.Errorf("compare helper timed out after %s: %w", timeout, res.err)
		}
		return 0, 0, fmt.Errorf("compare helper timed out after %s", timeout)
	}
}

func opusCompareWorkerCount() int {
	// Honor an explicit override for CI runners that want to tune throughput.
	if v := strings.TrimSpace(os.Getenv("GOPUS_OPUSCOMPARE_WORKERS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	n := runtime.GOMAXPROCS(0)
	if n < 1 {
		return 1
	}
	// Helper processes are cheap (one short-lived C binary per worker) and
	// each request is CPU-bound, so scale with core count. Clamp to a sane
	// upper bound to avoid thrashing on very large runners.
	if n > 16 {
		return 16
	}
	return n
}

func startOpusCompareHelperPool() error {
	opusCompareHelperPoolOnce.Do(func() {
		reqCh := make(chan opusCompareHelperRequest)
		for range opusCompareWorkerCount() {
			go func() {
				var proc *opusCompareHelperProcess
				for req := range reqCh {
					var res opusCompareHelperResponse
					for attempt := 0; attempt < 2; attempt++ {
						if proc == nil {
							var err error
							proc, err = startOpusCompareHelperProcess()
							if err != nil {
								res.err = err
								break
							}
						}

						bestQ, bestDelay, err := runOpusCompareHelperRequestWithTimeout(proc, req.payload, opusCompareHelperReqTimeout)
						if err == nil {
							res.bestQ = bestQ
							res.bestDelay = bestDelay
							break
						}

						closeOpusCompareHelperProcess(proc)
						proc = nil
						res.err = err
					}
					req.reply <- res
				}
				closeOpusCompareHelperProcess(proc)
			}()
		}
		opusCompareHelperReqCh = reqCh
	})
	return nil
}

func runOpusCompareHelper(reference, decoded []int16, sampleRate, channels int, delays []int) (float64, int, error) {
	payload, err := encodeOpusCompareHelperPayload(reference, decoded, sampleRate, channels, delays)
	if err != nil {
		return 0, 0, err
	}
	if err := startOpusCompareHelperPool(); err != nil {
		return 0, 0, err
	}

	reply := make(chan opusCompareHelperResponse, 1)
	opusCompareHelperReqCh <- opusCompareHelperRequest{
		payload: payload,
		reply:   reply,
	}
	res := <-reply
	if res.err != nil {
		return 0, 0, res.err
	}
	return res.bestQ, res.bestDelay, nil
}
