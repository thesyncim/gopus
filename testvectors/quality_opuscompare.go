package testvectors

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

var (
	opusCompareOnce sync.Once
	opusComparePath string
	opusCompareErr  error
)

var (
	opusCompareQualityRE = regexp.MustCompile(`Opus quality metric:\s*([-+0-9.]+)\s*%`)
	opusCompareErrorRE   = regexp.MustCompile(`(?i)internal weighted error is\s*([-+0-9.eE]+)`)
)

func getOpusComparePath() (string, error) {
	opusCompareOnce.Do(func() {
		path, ok := libopustooling.FindOrEnsureOpusCompare(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots())
		if !ok {
			opusCompareErr = fmt.Errorf("opus_compare not found in pinned libopus tree")
			return
		}
		opusComparePath = path
	})
	if opusCompareErr != nil {
		return "", opusCompareErr
	}
	return opusComparePath, nil
}

func parseOpusCompareOutput(out []byte) (float64, error) {
	text := string(out)
	if m := opusCompareQualityRE.FindStringSubmatch(text); len(m) == 2 {
		q, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			return 0, fmt.Errorf("parse opus_compare quality: %w", err)
		}
		return q, nil
	}

	m := opusCompareErrorRE.FindStringSubmatch(text)
	if len(m) != 2 {
		return 0, fmt.Errorf("unexpected opus_compare output: %s", strings.TrimSpace(text))
	}
	internalErr, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, fmt.Errorf("parse opus_compare internal error: %w", err)
	}

	// Match libopus src/opus_compare.c exactly for fail cases where the tool
	// prints only the internal weighted error and exits non-zero.
	q := 100.0 * (1.0 - 0.5*math.Log(1.0+internalErr)/math.Log(1.13))
	return q, nil
}

func writePCM16LE(path string, samples []int16) error {
	data := make([]byte, len(samples)*2)
	for i, s := range samples {
		u := uint16(s)
		data[i*2] = byte(u)
		data[i*2+1] = byte(u >> 8)
	}
	return os.WriteFile(path, data, 0o600)
}

func clampFloat32ToInt16(sample float32) int16 {
	if sample >= 1.0 {
		return math.MaxInt16
	}
	if sample <= -1.0 {
		return math.MinInt16
	}
	v := int32(math.Round(float64(sample) * 32768.0))
	if v > math.MaxInt16 {
		return math.MaxInt16
	}
	if v < math.MinInt16 {
		return math.MinInt16
	}
	return int16(v)
}

func float32ToPCM16(samples []float32) []int16 {
	out := make([]int16, len(samples))
	for i, s := range samples {
		out[i] = clampFloat32ToInt16(s)
	}
	return out
}

func duplicateMonoToStereo(samples []int16) []int16 {
	out := make([]int16, len(samples)*2)
	for i, s := range samples {
		out[i*2] = s
		out[i*2+1] = s
	}
	return out
}

func runOpusCompareCLI(reference, decoded []int16, sampleRate, channels int) (float64, error) {
	if sampleRate != 48000 {
		return 0, fmt.Errorf("opus_compare helper currently supports 48 kHz inputs only (got %d)", sampleRate)
	}
	if channels != 1 && channels != 2 {
		return 0, fmt.Errorf("unsupported channel count %d", channels)
	}
	if len(reference) == 0 || len(decoded) == 0 {
		return math.Inf(-1), nil
	}

	n := len(reference)
	if len(decoded) < n {
		n = len(decoded)
	}
	n -= n % channels
	if n <= 0 {
		return math.Inf(-1), nil
	}
	reference = reference[:n]
	decoded = decoded[:n]
	if n/channels < 480 {
		return math.Inf(-1), nil
	}

	opusCompare, err := getOpusComparePath()
	if err != nil {
		return 0, err
	}

	tmpDir, err := os.MkdirTemp("", "gopus-opus-compare-*")
	if err != nil {
		return 0, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	refPath := filepath.Join(tmpDir, "reference.sw")
	decPath := filepath.Join(tmpDir, "decoded.sw")

	refSamples := reference
	args := make([]string, 0, 3)
	if channels == 2 {
		args = append(args, "-s")
	} else {
		// The libopus opus_compare tool expects the reference side in the RFC 8251
		// stereo-interleaved format even for mono comparisons. Mirror that here by
		// duplicating the mono reference into L/R.
		refSamples = duplicateMonoToStereo(reference)
	}
	args = append(args, refPath, decPath)

	if err := writePCM16LE(refPath, refSamples); err != nil {
		return 0, fmt.Errorf("write reference pcm: %w", err)
	}
	if err := writePCM16LE(decPath, decoded); err != nil {
		return 0, fmt.Errorf("write decoded pcm: %w", err)
	}

	cmd := exec.Command(opusCompare, args...)
	out, err := cmd.CombinedOutput()
	q, parseErr := parseOpusCompareOutput(out)
	if parseErr != nil {
		if err != nil {
			return 0, fmt.Errorf("opus_compare failed: %w (%s)", err, strings.TrimSpace(string(out)))
		}
		return 0, parseErr
	}
	return q, nil
}

func alignedPCM16ForDelay(decoded, reference []int16, channels, delay int) ([]int16, []int16) {
	if channels <= 0 {
		return nil, nil
	}

	refStart := 0
	decStart := 0
	if delay > 0 {
		decStart = delay
	} else if delay < 0 {
		refStart = -delay
	}
	if refStart >= len(reference) || decStart >= len(decoded) {
		return nil, nil
	}

	n := len(reference) - refStart
	if rem := len(decoded) - decStart; rem < n {
		n = rem
	}
	n -= n % channels
	if n <= 0 {
		return nil, nil
	}
	return decoded[decStart : decStart+n], reference[refStart : refStart+n]
}

func runOpusCompareCandidates(reference, decoded []int16, sampleRate, channels int, delays []int) (float64, int, error) {
	if len(delays) == 0 {
		delays = []int{0}
	}

	if q, delay, err := runOpusCompareHelper(reference, decoded, sampleRate, channels, delays); err == nil {
		return q, delay, nil
	}

	bestQ := math.Inf(-1)
	bestDelay := delays[0]
	seen := make(map[int]struct{}, len(delays))
	for _, delay := range delays {
		if _, ok := seen[delay]; ok {
			continue
		}
		seen[delay] = struct{}{}

		alignedDecoded, alignedReference := alignedPCM16ForDelay(decoded, reference, channels, delay)
		if len(alignedDecoded) == 0 || len(alignedReference) == 0 {
			continue
		}

		q, err := runOpusCompareCLI(alignedReference, alignedDecoded, sampleRate, channels)
		if err != nil {
			return 0, 0, err
		}
		if q > bestQ || (q == bestQ && qualityAbsInt(delay) < qualityAbsInt(bestDelay)) {
			bestQ = q
			bestDelay = delay
		}
	}
	return bestQ, bestDelay, nil
}

func ComputeOpusCompareQuality(decoded, reference []int16, sampleRate, channels int) (float64, error) {
	q, _, err := runOpusCompareCandidates(reference, decoded, sampleRate, channels, []int{0})
	return q, err
}

func ComputeOpusCompareQualityFloat32(decoded, reference []float32, sampleRate, channels int) (float64, error) {
	q, _, err := runOpusCompareCandidates(float32ToPCM16(reference), float32ToPCM16(decoded), sampleRate, channels, []int{0})
	return q, err
}

func estimateDelayByWaveformCorrelation(decoded, reference []float32, maxDelay int) int {
	if len(decoded) == 0 || len(reference) == 0 {
		return 0
	}

	bestCorr := math.Inf(-1)
	bestDelay := 0
	for d := -maxDelay; d <= maxDelay; d++ {
		var dot float64
		var refPower float64
		var decPower float64
		count := 0

		const margin = 120
		for i := margin; i < len(reference)-margin; i++ {
			decIdx := i + d
			if decIdx >= margin && decIdx < len(decoded)-margin {
				ref := float64(reference[i])
				dec := float64(decoded[decIdx])
				dot += ref * dec
				refPower += ref * ref
				decPower += dec * dec
				count++
			}
		}

		if count == 0 || refPower <= 0 || decPower <= 0 {
			continue
		}

		candidateCorr := dot / math.Sqrt(refPower*decPower)
		if candidateCorr > bestCorr || (candidateCorr == bestCorr && qualityAbsInt(d) < qualityAbsInt(bestDelay)) {
			bestCorr = candidateCorr
			bestDelay = d
		}
	}
	return bestDelay
}

func opusCompareDelayCandidates(decoded, reference []float32, maxDelay int) []int {
	if maxDelay <= 32 {
		candidates := make([]int, 0, 2*maxDelay+1)
		for delay := -maxDelay; delay <= maxDelay; delay++ {
			candidates = append(candidates, delay)
		}
		return candidates
	}

	estimatedDelay := estimateDelayByWaveformCorrelation(decoded, reference, maxDelay)
	candidates := make([]int, 0, 18)
	seen := make(map[int]struct{}, 17)

	addDelay := func(delay int) {
		if delay < -maxDelay || delay > maxDelay {
			return
		}
		if _, ok := seen[delay]; ok {
			return
		}
		seen[delay] = struct{}{}
		candidates = append(candidates, delay)
	}

	addDelay(estimatedDelay)
	addDelay(0)
	for delta := 1; delta <= 8; delta++ {
		addDelay(estimatedDelay - delta)
		addDelay(estimatedDelay + delta)
	}
	if len(candidates) == 0 {
		candidates = append(candidates, 0)
	}
	return candidates
}

func ComputeOpusCompareQualityFloat32WithDelay(decoded, reference []float32, sampleRate, channels, maxDelay int) (float64, int, error) {
	if len(decoded) == 0 || len(reference) == 0 {
		return math.Inf(-1), 0, nil
	}
	if channels != 1 && channels != 2 {
		return 0, 0, fmt.Errorf("unsupported channel count %d", channels)
	}

	return runOpusCompareCandidates(
		float32ToPCM16(reference),
		float32ToPCM16(decoded),
		sampleRate,
		channels,
		opusCompareDelayCandidates(decoded, reference, maxDelay),
	)
}
