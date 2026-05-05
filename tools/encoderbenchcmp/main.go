package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"github.com/thesyncim/gopus/internal/testsignal"
)

const (
	sampleRate     = 48000
	libopusVersion = "1.6.1"
	encoderPath    = "Float32"
	maxPacketBytes = 4000
)

type encoderWorkload struct {
	Name               string
	Variant            string
	Application        gopus.Application
	LibopusApplication string
	Bandwidth          gopus.Bandwidth
	LibopusBandwidth   string
	FrameSize          int
	Channels           int
	Bitrate            int
	Signal             gopus.Signal
	LibopusSignal      string
	PCM                []float32
}

type benchmarkCase struct {
	name      string
	workloads []encoderWorkload
}

type benchmarkResult struct {
	Implementation string
	Path           string
	Vector         string
	MinDuration    time.Duration
	Count          int
	Iterations     int64
	ElapsedNS      int64
	BytesPerOp     int64
	PacketsPerOp   int64
	SamplesPerOp   int64
	NsPerSample    float64
	NsPerPacket    float64
	XRealtime      float64
	Allocations    *float64
}

type runConfig struct {
	cases                string
	minDuration          time.Duration
	benchtimes           string
	count                int
	libopusRoot          string
	format               string
	outPath              string
	maxGopusLibopusRatio float64
	maxGopusAllocsPerOp  float64
}

func main() {
	cfg := runConfig{}
	flag.StringVar(&cfg.cases, "cases", "all", "benchmark cases: aggregate, per-case, or all")
	flag.DurationVar(&cfg.minDuration, "benchtime", 200*time.Millisecond, "minimum measurement time per run")
	flag.StringVar(&cfg.benchtimes, "benchtimes", "", "comma-separated minimum measurement times; bare numbers are milliseconds")
	flag.IntVar(&cfg.count, "count", 3, "measurement runs per case; median ns/sample is reported")
	flag.StringVar(&cfg.libopusRoot, "libopus-root", filepath.Join("tmp_check", "opus-"+libopusVersion), "pinned libopus source/build directory")
	flag.StringVar(&cfg.format, "format", "markdown", "output format: markdown or tsv")
	flag.StringVar(&cfg.outPath, "out", "", "optional output path")
	flag.Float64Var(&cfg.maxGopusLibopusRatio, "max-gopus-libopus-ratio", 0, "optional guardrail: fail when gopus/libopus ns/sample ratio exceeds this value")
	flag.Float64Var(&cfg.maxGopusAllocsPerOp, "max-gopus-allocs-per-op", -1, "optional guardrail: fail when gopus allocations/op exceeds this value")
	flag.Parse()

	if err := run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "encoderbenchcmp:", err)
		os.Exit(1)
	}
}

func run(cfg runConfig) error {
	durations, err := measurementDurations(cfg)
	if err != nil {
		return err
	}
	if cfg.count < 1 {
		return errors.New("count must be positive")
	}
	if !validCases(cfg.cases) {
		return fmt.Errorf("invalid cases %q", cfg.cases)
	}
	if cfg.format != "markdown" && cfg.format != "tsv" {
		return fmt.Errorf("invalid format %q", cfg.format)
	}
	if cfg.maxGopusLibopusRatio < 0 {
		return errors.New("max-gopus-libopus-ratio must be >= 0")
	}
	if cfg.maxGopusAllocsPerOp < -1 {
		return errors.New("max-gopus-allocs-per-op must be >= -1")
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}
	libopusRoot := absPath(root, cfg.libopusRoot)

	workloads, err := makeEncoderWorkloads()
	if err != nil {
		return err
	}
	cases := makeBenchmarkCases(workloads, cfg.cases)

	helper, err := buildLibopusHelper(root, libopusRoot)
	if err != nil {
		return err
	}

	tempDir, err := os.MkdirTemp("", "gopus-encoderbenchcmp-")
	if err != nil {
		return err
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()
	caseSpecs, err := writeLibopusPCMInputs(tempDir, workloads)
	if err != nil {
		return err
	}

	var results []benchmarkResult
	for _, duration := range durations {
		runCfg := cfg
		runCfg.minDuration = duration
		gopusResults, err := runGopusBenchmarks(cases, runCfg)
		if err != nil {
			return err
		}
		libopusResults, err := runLibopusBenchmarks(helper, caseSpecs, runCfg)
		if err != nil {
			return err
		}
		results = append(results, gopusResults...)
		results = append(results, libopusResults...)
	}
	sortResults(results)

	var out string
	switch cfg.format {
	case "markdown":
		out = formatMarkdown(results, cfg)
	default:
		out = formatTSV(results)
	}
	violations := evaluatePerformanceGuardrails(results, cfg)

	if cfg.outPath != "" {
		outPath := absPath(root, cfg.outPath)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(outPath, []byte(out), 0o644); err != nil {
			return err
		}
		fmt.Printf("wrote %s\n", outPath)
	} else {
		fmt.Print(out)
	}
	if len(violations) > 0 {
		for _, violation := range violations {
			fmt.Fprintln(os.Stderr, violation)
		}
		return errors.New("performance guardrail failed")
	}
	if performanceGuardrailsEnabled(cfg) {
		fmt.Fprintln(os.Stderr, "encoderbenchcmp: libopus-relative performance guardrails are within limits")
	}
	return nil
}

func measurementDurations(cfg runConfig) ([]time.Duration, error) {
	if strings.TrimSpace(cfg.benchtimes) == "" {
		if cfg.minDuration <= 0 {
			return nil, errors.New("benchtime must be positive")
		}
		return []time.Duration{cfg.minDuration}, nil
	}

	parts := strings.Split(cfg.benchtimes, ",")
	durations := make([]time.Duration, 0, len(parts))
	seen := make(map[time.Duration]bool, len(parts))
	for _, part := range parts {
		duration, err := parseBenchmarkDuration(part)
		if err != nil {
			return nil, err
		}
		if !seen[duration] {
			durations = append(durations, duration)
			seen[duration] = true
		}
	}
	if len(durations) == 0 {
		return nil, errors.New("benchtimes must include at least one duration")
	}
	return durations, nil
}

func parseBenchmarkDuration(raw string) (time.Duration, error) {
	token := strings.TrimSpace(raw)
	if token == "" {
		return 0, errors.New("empty benchtime")
	}

	duration, err := time.ParseDuration(token)
	if err != nil {
		allDigits := true
		for _, r := range token {
			if r < '0' || r > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			duration, err = time.ParseDuration(token + "ms")
		}
	}
	if err != nil {
		return 0, fmt.Errorf("invalid benchtime %q: %w", raw, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("benchtime %q must be positive", raw)
	}
	return duration, nil
}

func validCases(cases string) bool {
	switch cases {
	case "aggregate", "per-case", "all":
		return true
	default:
		return false
	}
}

func makeEncoderWorkloads() ([]encoderWorkload, error) {
	workloads := []encoderWorkload{
		{
			Name:               "CELT-FB-20ms-stereo-128k",
			Variant:            testsignal.EncoderVariantAMMultisineV1,
			Application:        gopus.ApplicationRestrictedCelt,
			LibopusApplication: "restricted-celt",
			Bandwidth:          gopus.BandwidthFullband,
			LibopusBandwidth:   "fb",
			FrameSize:          960,
			Channels:           2,
			Bitrate:            128000,
			Signal:             gopus.SignalMusic,
			LibopusSignal:      "music",
		},
		{
			Name:               "CELT-FB-5ms-mono-64k",
			Variant:            testsignal.EncoderVariantChirpSweepV1,
			Application:        gopus.ApplicationRestrictedCelt,
			LibopusApplication: "restricted-celt",
			Bandwidth:          gopus.BandwidthFullband,
			LibopusBandwidth:   "fb",
			FrameSize:          240,
			Channels:           1,
			Bitrate:            64000,
			Signal:             gopus.SignalMusic,
			LibopusSignal:      "music",
		},
		{
			Name:               "SILK-WB-20ms-mono-32k",
			Variant:            testsignal.EncoderVariantSpeechLikeV1,
			Application:        gopus.ApplicationRestrictedSilk,
			LibopusApplication: "restricted-silk",
			Bandwidth:          gopus.BandwidthWideband,
			LibopusBandwidth:   "wb",
			FrameSize:          960,
			Channels:           1,
			Bitrate:            32000,
			Signal:             gopus.SignalVoice,
			LibopusSignal:      "voice",
		},
		{
			Name:               "Hybrid-FB-20ms-mono-64k",
			Variant:            testsignal.EncoderVariantSpeechLikeV1,
			Application:        gopus.ApplicationAudio,
			LibopusApplication: "audio",
			Bandwidth:          gopus.BandwidthFullband,
			LibopusBandwidth:   "fb",
			FrameSize:          960,
			Channels:           1,
			Bitrate:            64000,
			Signal:             gopus.SignalVoice,
			LibopusSignal:      "voice",
		},
		{
			Name:               "Hybrid-FB-20ms-stereo-96k",
			Variant:            testsignal.EncoderVariantSpeechLikeV1,
			Application:        gopus.ApplicationAudio,
			LibopusApplication: "audio",
			Bandwidth:          gopus.BandwidthFullband,
			LibopusBandwidth:   "fb",
			FrameSize:          960,
			Channels:           2,
			Bitrate:            96000,
			Signal:             gopus.SignalVoice,
			LibopusSignal:      "voice",
		},
	}

	for i := range workloads {
		frames := sampleRate / workloads[i].FrameSize
		samples := frames * workloads[i].FrameSize * workloads[i].Channels
		pcm, err := testsignal.GenerateEncoderSignalVariant(workloads[i].Variant, sampleRate, samples, workloads[i].Channels)
		if err != nil {
			return nil, err
		}
		workloads[i].PCM = pcm
	}
	return workloads, nil
}

func makeBenchmarkCases(workloads []encoderWorkload, cases string) []benchmarkCase {
	out := make([]benchmarkCase, 0, len(workloads)+1)
	if cases == "aggregate" || cases == "all" {
		out = append(out, benchmarkCase{name: "all", workloads: workloads})
	}
	if cases == "per-case" || cases == "all" {
		for _, workload := range workloads {
			out = append(out, benchmarkCase{name: workload.Name, workloads: []encoderWorkload{workload}})
		}
	}
	return out
}

func runGopusBenchmarks(cases []benchmarkCase, cfg runConfig) ([]benchmarkResult, error) {
	results := make([]benchmarkResult, 0, len(cases))
	for _, benchCase := range cases {
		result, err := runGopusBenchmarkCase(benchCase, cfg)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func runGopusBenchmarkCase(benchCase benchmarkCase, cfg runConfig) (benchmarkResult, error) {
	encoders, err := newGopusEncoders(benchCase.workloads)
	if err != nil {
		return benchmarkResult{}, err
	}
	packet := make([]byte, maxPacketBytes)
	if _, err := encodeGopusCase(benchCase, encoders, packet); err != nil {
		return benchmarkResult{}, err
	}

	var allocs *float64
	if cfg.maxGopusAllocsPerOp >= 0 {
		allocEncoders, err := newGopusEncoders(benchCase.workloads)
		if err != nil {
			return benchmarkResult{}, err
		}
		allocPacket := make([]byte, maxPacketBytes)
		allocsValue := testing.AllocsPerRun(20, func() {
			if _, err := encodeGopusCase(benchCase, allocEncoders, allocPacket); err != nil {
				panic(err)
			}
		})
		allocs = &allocsValue
	}

	runs := make([]benchmarkResult, cfg.count)
	for i := 0; i < cfg.count; i++ {
		benchEncoders, err := newGopusEncoders(benchCase.workloads)
		if err != nil {
			return benchmarkResult{}, err
		}
		iter, elapsed, bytesPerOp, err := runTimedGopusCase(benchCase, benchEncoders, packet, cfg.minDuration)
		if err != nil {
			return benchmarkResult{}, err
		}
		packets, samples := summarizeWorkloads(benchCase.workloads)
		runs[i] = makeResult("gopus", benchCase.name, cfg.minDuration, cfg.count, iter, elapsed, bytesPerOp, packets, samples, allocs)
	}
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].NsPerSample < runs[j].NsPerSample
	})
	return runs[len(runs)/2], nil
}

func newGopusEncoders(workloads []encoderWorkload) ([]*gopus.Encoder, error) {
	encoders := make([]*gopus.Encoder, len(workloads))
	for i, workload := range workloads {
		enc, err := gopus.NewEncoder(gopus.EncoderConfig{
			SampleRate:  sampleRate,
			Channels:    workload.Channels,
			Application: workload.Application,
		})
		if err != nil {
			return nil, fmt.Errorf("%s: NewEncoder: %w", workload.Name, err)
		}
		if err := enc.SetFrameSize(workload.FrameSize); err != nil {
			return nil, fmt.Errorf("%s: SetFrameSize: %w", workload.Name, err)
		}
		if err := enc.SetBandwidth(workload.Bandwidth); err != nil {
			return nil, fmt.Errorf("%s: SetBandwidth: %w", workload.Name, err)
		}
		if err := enc.SetBitrate(workload.Bitrate); err != nil {
			return nil, fmt.Errorf("%s: SetBitrate: %w", workload.Name, err)
		}
		if err := enc.SetBitrateMode(gopus.BitrateModeCBR); err != nil {
			return nil, fmt.Errorf("%s: SetBitrateMode: %w", workload.Name, err)
		}
		if err := enc.SetComplexity(10); err != nil {
			return nil, fmt.Errorf("%s: SetComplexity: %w", workload.Name, err)
		}
		if err := enc.SetSignal(workload.Signal); err != nil {
			return nil, fmt.Errorf("%s: SetSignal: %w", workload.Name, err)
		}
		encoders[i] = enc
	}
	return encoders, nil
}

func runTimedGopusCase(benchCase benchmarkCase, encoders []*gopus.Encoder, packet []byte, minDuration time.Duration) (int64, time.Duration, int64, error) {
	start := time.Now()
	var iterations int64
	var totalBytes int64
	for {
		bytesPerOp, err := encodeGopusCase(benchCase, encoders, packet)
		if err != nil {
			return 0, 0, 0, err
		}
		totalBytes += bytesPerOp
		iterations++
		elapsed := time.Since(start)
		if elapsed >= minDuration {
			return iterations, elapsed, totalBytes / iterations, nil
		}
	}
}

func encodeGopusCase(benchCase benchmarkCase, encoders []*gopus.Encoder, packet []byte) (int64, error) {
	var bytesPerOp int64
	for i := range benchCase.workloads {
		workload := benchCase.workloads[i]
		enc := encoders[i]
		enc.Reset()
		samplesPerFrame := workload.FrameSize * workload.Channels
		frames := len(workload.PCM) / samplesPerFrame
		for frame := 0; frame < frames; frame++ {
			start := frame * samplesPerFrame
			n, err := enc.Encode(workload.PCM[start:start+samplesPerFrame], packet)
			if err != nil {
				return 0, fmt.Errorf("%s frame %d: %w", workload.Name, frame, err)
			}
			bytesPerOp += int64(n)
		}
	}
	return bytesPerOp, nil
}

func summarizeWorkloads(workloads []encoderWorkload) (packetsPerOp, samplesPerOp int64) {
	for _, workload := range workloads {
		frames := len(workload.PCM) / (workload.FrameSize * workload.Channels)
		packetsPerOp += int64(frames)
		samplesPerOp += int64(frames * workload.FrameSize)
	}
	return packetsPerOp, samplesPerOp
}

func makeResult(implementation, vector string, minDuration time.Duration, count int, iterations int64, elapsed time.Duration, bytesPerOp, packetsPerOp, samplesPerOp int64, allocs *float64) benchmarkResult {
	elapsedNS := elapsed.Nanoseconds()
	totalSamples := float64(samplesPerOp) * float64(iterations)
	totalPackets := float64(packetsPerOp) * float64(iterations)
	audioSeconds := totalSamples / sampleRate
	return benchmarkResult{
		Implementation: implementation,
		Path:           encoderPath,
		Vector:         vector,
		MinDuration:    minDuration,
		Count:          count,
		Iterations:     iterations,
		ElapsedNS:      elapsedNS,
		BytesPerOp:     bytesPerOp,
		PacketsPerOp:   packetsPerOp,
		SamplesPerOp:   samplesPerOp,
		NsPerSample:    float64(elapsedNS) / totalSamples,
		NsPerPacket:    float64(elapsedNS) / totalPackets,
		XRealtime:      audioSeconds / elapsed.Seconds(),
		Allocations:    allocs,
	}
}

func writeLibopusPCMInputs(tempDir string, workloads []encoderWorkload) ([]string, error) {
	specs := make([]string, 0, len(workloads))
	for _, workload := range workloads {
		path := filepath.Join(tempDir, workload.Name+".f32")
		var buf bytes.Buffer
		buf.Grow(len(workload.PCM) * 4)
		var word [4]byte
		for _, sample := range workload.PCM {
			binary.LittleEndian.PutUint32(word[:], math.Float32bits(sample))
			buf.Write(word[:])
		}
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			return nil, err
		}
		spec := strings.Join([]string{
			workload.Name,
			workload.LibopusApplication,
			workload.LibopusBandwidth,
			strconv.Itoa(workload.FrameSize),
			strconv.Itoa(workload.Channels),
			strconv.Itoa(workload.Bitrate),
			workload.LibopusSignal,
			path,
		}, ":")
		specs = append(specs, spec)
	}
	return specs, nil
}

func buildLibopusHelper(root, libopusRoot string) (string, error) {
	staticLib := filepath.Join(libopusRoot, ".libs", "libopus.a")
	if _, err := os.Stat(staticLib); err != nil {
		libopustooling.EnsureLibopus(libopusVersion, []string{root})
	}
	if _, err := os.Stat(staticLib); err != nil {
		return "", fmt.Errorf("pinned libopus static library not found at %s: %w", staticLib, err)
	}

	cc, err := libopustooling.FindCCompiler()
	if err != nil {
		return "", err
	}
	src := filepath.Join(root, "tools", "csrc", "libopus_encoder_bench.c")
	out := filepath.Join(libopusRoot, fmt.Sprintf("gopus_libopus_encoder_bench_%s_%s", runtime.GOOS, runtime.GOARCH))
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	args := []string{
		"-std=c99",
		"-O2",
		"-I", filepath.Join(libopusRoot, "include"),
		src,
		staticLib,
		"-lm",
		"-o", out,
	}
	cmd := exec.Command(cc, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("build libopus encoder benchmark helper: %w (%s)", err, bytes.TrimSpace(output))
	}
	return out, nil
}

func runLibopusBenchmarks(helper string, caseSpecs []string, cfg runConfig) ([]benchmarkResult, error) {
	args := []string{
		"--min-ns", strconv.FormatInt(cfg.minDuration.Nanoseconds(), 10),
		"--count", strconv.Itoa(cfg.count),
		"--cases", cfg.cases,
	}
	for _, spec := range caseSpecs {
		args = append(args, "--case", spec)
	}
	cmd := exec.Command(helper, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("run libopus encoder benchmark helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}
	results, err := parseLibopusTSV(output)
	if err != nil {
		return nil, err
	}
	for i := range results {
		results[i].MinDuration = cfg.minDuration
	}
	return results, nil
}

func parseLibopusTSV(data []byte) ([]benchmarkResult, error) {
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		return nil, fmt.Errorf("libopus encoder benchmark helper returned no rows")
	}
	var results []benchmarkResult
	for i, line := range lines[1:] {
		fields := strings.Split(line, "\t")
		if len(fields) != 14 {
			return nil, fmt.Errorf("libopus encoder row %d has %d fields", i+1, len(fields))
		}
		count, err := strconv.Atoi(fields[4])
		if err != nil {
			return nil, err
		}
		iterations, err := strconv.ParseInt(fields[5], 10, 64)
		if err != nil {
			return nil, err
		}
		elapsedNS, err := strconv.ParseInt(fields[6], 10, 64)
		if err != nil {
			return nil, err
		}
		bytesPerOp, err := strconv.ParseInt(fields[7], 10, 64)
		if err != nil {
			return nil, err
		}
		packetsPerOp, err := strconv.ParseInt(fields[8], 10, 64)
		if err != nil {
			return nil, err
		}
		samplesPerOp, err := strconv.ParseInt(fields[9], 10, 64)
		if err != nil {
			return nil, err
		}
		nsPerSample, err := strconv.ParseFloat(fields[10], 64)
		if err != nil {
			return nil, err
		}
		nsPerPacket, err := strconv.ParseFloat(fields[11], 64)
		if err != nil {
			return nil, err
		}
		xRealtime, err := strconv.ParseFloat(fields[12], 64)
		if err != nil {
			return nil, err
		}
		results = append(results, benchmarkResult{
			Implementation: fields[0],
			Path:           fields[1],
			Vector:         fields[2],
			Count:          count,
			Iterations:     iterations,
			ElapsedNS:      elapsedNS,
			BytesPerOp:     bytesPerOp,
			PacketsPerOp:   packetsPerOp,
			SamplesPerOp:   samplesPerOp,
			NsPerSample:    nsPerSample,
			NsPerPacket:    nsPerPacket,
			XRealtime:      xRealtime,
		})
	}
	return results, nil
}

func sortResults(results []benchmarkResult) {
	sort.Slice(results, func(i, j int) bool {
		if results[i].MinDuration != results[j].MinDuration {
			return results[i].MinDuration < results[j].MinDuration
		}
		if vectorRank(results[i].Vector) != vectorRank(results[j].Vector) {
			return vectorRank(results[i].Vector) < vectorRank(results[j].Vector)
		}
		if results[i].Vector != results[j].Vector {
			return results[i].Vector < results[j].Vector
		}
		return results[i].Implementation < results[j].Implementation
	})
}

func vectorRank(vector string) int {
	if vector == "all" {
		return 0
	}
	return 1
}

func resultMap(results []benchmarkResult) map[string]benchmarkResult {
	m := make(map[string]benchmarkResult, len(results))
	for _, result := range results {
		m[resultKey(result.MinDuration, result.Implementation, result.Path, result.Vector)] = result
	}
	return m
}

func resultKey(minDuration time.Duration, implementation, path, vector string) string {
	return minDuration.String() + "/" + implementation + "/" + path + "/" + vector
}

func performanceGuardrailsEnabled(cfg runConfig) bool {
	return cfg.maxGopusLibopusRatio > 0 || cfg.maxGopusAllocsPerOp >= 0
}

func evaluatePerformanceGuardrails(results []benchmarkResult, cfg runConfig) []string {
	if !performanceGuardrailsEnabled(cfg) {
		return nil
	}
	m := resultMap(results)
	var violations []string
	for _, result := range results {
		if result.Implementation != "gopus" {
			continue
		}
		if cfg.maxGopusLibopusRatio > 0 {
			libopus, ok := m[resultKey(result.MinDuration, "libopus", result.Path, result.Vector)]
			if !ok {
				violations = append(violations, fmt.Sprintf("encoderbenchcmp: missing libopus baseline for %s/%s/%s", result.MinDuration, result.Path, result.Vector))
			} else if libopus.NsPerSample <= 0 {
				violations = append(violations, fmt.Sprintf("encoderbenchcmp: invalid libopus ns/sample %.6f for %s/%s/%s", libopus.NsPerSample, result.MinDuration, result.Path, result.Vector))
			} else {
				ratio := result.NsPerSample / libopus.NsPerSample
				fmt.Fprintf(os.Stderr, "encoderbenchcmp: %s %-7s %-32s gopus/libopus=%.3fx (max %.3fx), gopus allocs/op=%s\n",
					result.MinDuration,
					result.Path,
					result.Vector,
					ratio,
					cfg.maxGopusLibopusRatio,
					formatAllocs(result.Allocations),
				)
				if ratio > cfg.maxGopusLibopusRatio {
					violations = append(violations, fmt.Sprintf("encoderbenchcmp: %s/%s/%s gopus/libopus regression: %.3fx > max %.3fx",
						result.MinDuration, result.Path, result.Vector, ratio, cfg.maxGopusLibopusRatio))
				}
			}
		}
		if cfg.maxGopusAllocsPerOp >= 0 {
			if result.Allocations == nil {
				violations = append(violations, fmt.Sprintf("encoderbenchcmp: missing gopus allocations/op for %s/%s/%s", result.MinDuration, result.Path, result.Vector))
			} else if *result.Allocations > cfg.maxGopusAllocsPerOp {
				violations = append(violations, fmt.Sprintf("encoderbenchcmp: %s/%s/%s gopus allocations regression: %.1f > max %.1f",
					result.MinDuration, result.Path, result.Vector, *result.Allocations, cfg.maxGopusAllocsPerOp))
			}
		}
	}
	return violations
}

func formatMarkdown(results []benchmarkResult, cfg runConfig) string {
	m := resultMap(results)
	var b strings.Builder
	fmt.Fprintf(&b, "# Encoder Libopus Benchmark Comparison\n\n")
	fmt.Fprintf(&b, "- Pinned reference: libopus %s\n", libopusVersion)
	fmt.Fprintf(&b, "- Cases: `%s`\n", cfg.cases)
	fmt.Fprintf(&b, "- Measurement count: `%d`\n\n", cfg.count)

	fmt.Fprintf(&b, "| Workload | gopus ns/sample | libopus ns/sample | gopus/libopus | gopus realtime | libopus realtime | gopus allocs/op |\n")
	fmt.Fprintf(&b, "| --- | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, result := range results {
		if result.Implementation != "gopus" {
			continue
		}
		cResult, ok := m[resultKey(result.MinDuration, "libopus", result.Path, result.Vector)]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "| %s | %.2f | %.2f | %.2fx | %.1fx | %.1fx | %s |\n",
			result.Vector,
			result.NsPerSample,
			cResult.NsPerSample,
			result.NsPerSample/cResult.NsPerSample,
			result.XRealtime,
			cResult.XRealtime,
			formatAllocs(result.Allocations),
		)
	}
	return b.String()
}

func formatTSV(results []benchmarkResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "implementation\tpath\tvector\tbenchtime\tcount\titerations\telapsed_ns\tbytes_per_op\tpackets_per_op\tsamples_per_op\tns_per_sample\tns_per_packet\tx_realtime\tallocs_per_op\n")
	for _, result := range results {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%.6f\t%.6f\t%.6f\t%s\n",
			result.Implementation,
			result.Path,
			result.Vector,
			result.MinDuration,
			result.Count,
			result.Iterations,
			result.ElapsedNS,
			result.BytesPerOp,
			result.PacketsPerOp,
			result.SamplesPerOp,
			result.NsPerSample,
			result.NsPerPacket,
			result.XRealtime,
			formatAllocs(result.Allocations),
		)
	}
	return b.String()
}

func formatAllocs(allocs *float64) string {
	if allocs == nil {
		return "-"
	}
	return fmt.Sprintf("%.0f", *allocs)
}

func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		wd, wdErr := os.Getwd()
		if wdErr != nil {
			return "", fmt.Errorf("locate repo root: %w", err)
		}
		return wd, nil
	}
	return strings.TrimSpace(string(out)), nil
}

func absPath(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}
