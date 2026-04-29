package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
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
	"github.com/thesyncim/gopus/testvectors"
)

const (
	sampleRate       = 48000
	channels         = 2
	maxPacketSamples = 5760
	libopusVersion   = "1.6.1"
)

type benchmarkVector struct {
	name        string
	path        string
	packets     []testvectors.Packet
	packetBytes int64
}

type benchmarkCase struct {
	name    string
	vectors []benchmarkVector
}

type benchmarkResult struct {
	Implementation string
	Path           string
	Vector         string
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
	cases       string
	paths       string
	minDuration time.Duration
	count       int
	vectorDir   string
	libopusRoot string
	format      string
	outPath     string
}

func main() {
	cfg := runConfig{}
	flag.StringVar(&cfg.cases, "cases", "all", "benchmark cases: aggregate, per-vector, or all")
	flag.StringVar(&cfg.paths, "paths", "all", "decode paths: float32, int16, or all")
	flag.DurationVar(&cfg.minDuration, "benchtime", 200*time.Millisecond, "minimum measurement time per run")
	flag.IntVar(&cfg.count, "count", 3, "measurement runs per case; median ns/sample is reported")
	flag.StringVar(&cfg.vectorDir, "vectors", filepath.Join("testvectors", "testdata", "opus_testvectors"), "official test-vector directory")
	flag.StringVar(&cfg.libopusRoot, "libopus-root", filepath.Join("tmp_check", "opus-"+libopusVersion), "pinned libopus source/build directory")
	flag.StringVar(&cfg.format, "format", "markdown", "output format: markdown or tsv")
	flag.StringVar(&cfg.outPath, "out", "", "optional output path")
	flag.Parse()

	if err := run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "testvectorbenchcmp:", err)
		os.Exit(1)
	}
}

func run(cfg runConfig) error {
	if cfg.minDuration <= 0 {
		return errors.New("benchtime must be positive")
	}
	if cfg.count < 1 {
		return errors.New("count must be positive")
	}
	if !validCases(cfg.cases) {
		return fmt.Errorf("invalid cases %q", cfg.cases)
	}
	if !validPaths(cfg.paths) {
		return fmt.Errorf("invalid paths %q", cfg.paths)
	}
	if cfg.format != "markdown" && cfg.format != "tsv" {
		return fmt.Errorf("invalid format %q", cfg.format)
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}
	vectorDir := absPath(root, cfg.vectorDir)
	libopusRoot := absPath(root, cfg.libopusRoot)

	vectors, err := loadVectors(vectorDir)
	if err != nil {
		return err
	}
	cases := makeCases(vectors, cfg.cases)
	paths := makePaths(cfg.paths)

	gopusResults, err := runGopusBenchmarks(paths, cases, cfg)
	if err != nil {
		return err
	}
	helper, err := buildLibopusHelper(root, libopusRoot)
	if err != nil {
		return err
	}
	libopusResults, err := runLibopusBenchmarks(helper, vectors, cfg)
	if err != nil {
		return err
	}

	results := append(gopusResults, libopusResults...)
	sortResults(results)

	var out string
	switch cfg.format {
	case "markdown":
		out = formatMarkdown(results, cfg)
	default:
		out = formatTSV(results)
	}

	if cfg.outPath != "" {
		outPath := absPath(root, cfg.outPath)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(outPath, []byte(out), 0o644); err != nil {
			return err
		}
		fmt.Printf("wrote %s\n", outPath)
		return nil
	}

	fmt.Print(out)
	return nil
}

func repoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd, nil
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			return "", errors.New("repository root not found")
		}
		wd = parent
	}
}

func absPath(root, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(root, path))
}

func validCases(cases string) bool {
	return cases == "aggregate" || cases == "per-vector" || cases == "all"
}

func validPaths(paths string) bool {
	return paths == "float32" || paths == "int16" || paths == "all"
}

func makePaths(paths string) []string {
	switch paths {
	case "float32":
		return []string{"Float32"}
	case "int16":
		return []string{"Int16"}
	default:
		return []string{"Float32", "Int16"}
	}
}

func loadVectors(vectorDir string) ([]benchmarkVector, error) {
	names := []string{
		"testvector01", "testvector02", "testvector03", "testvector04",
		"testvector05", "testvector06", "testvector07", "testvector08",
		"testvector09", "testvector10", "testvector11", "testvector12",
	}
	vectors := make([]benchmarkVector, 0, len(names))
	for _, name := range names {
		path := filepath.Join(vectorDir, name+".bit")
		packets, err := testvectors.ReadBitstreamFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w (run make bench-testvectors once to fetch vectors)", path, err)
		}
		if len(packets) == 0 {
			return nil, fmt.Errorf("%s has no packets", path)
		}
		vector := benchmarkVector{name: name, path: path, packets: packets}
		for _, packet := range packets {
			vector.packetBytes += int64(len(packet.Data))
		}
		vectors = append(vectors, vector)
	}
	return vectors, nil
}

func makeCases(vectors []benchmarkVector, cases string) []benchmarkCase {
	out := make([]benchmarkCase, 0, len(vectors)+1)
	if cases == "aggregate" || cases == "all" {
		out = append(out, benchmarkCase{name: "all", vectors: vectors})
	}
	if cases == "per-vector" || cases == "all" {
		for _, vector := range vectors {
			out = append(out, benchmarkCase{name: vector.name, vectors: []benchmarkVector{vector}})
		}
	}
	return out
}

func summarize(vectors []benchmarkVector) (packetCount int64, packetBytes int64) {
	for _, vector := range vectors {
		packetCount += int64(len(vector.packets))
		packetBytes += vector.packetBytes
	}
	return packetCount, packetBytes
}

func runGopusBenchmarks(paths []string, cases []benchmarkCase, cfg runConfig) ([]benchmarkResult, error) {
	var results []benchmarkResult
	for _, path := range paths {
		for _, benchCase := range cases {
			result, err := runGopusCase(path, benchCase, cfg)
			if err != nil {
				return nil, err
			}
			results = append(results, result)
		}
	}
	return results, nil
}

func runGopusCase(path string, benchCase benchmarkCase, cfg runConfig) (benchmarkResult, error) {
	cfgDec := gopus.DefaultDecoderConfig(sampleRate, channels)
	dec, err := gopus.NewDecoder(cfgDec)
	if err != nil {
		return benchmarkResult{}, err
	}
	packetCount, packetBytes := summarize(benchCase.vectors)

	var expectedSamples int64
	switch path {
	case "Float32":
		pcm := make([]float32, cfgDec.MaxPacketSamples*cfgDec.Channels)
		expectedSamples, err = decodeGopusFloat(dec, benchCase.vectors, pcm)
	case "Int16":
		pcm := make([]int16, cfgDec.MaxPacketSamples*cfgDec.Channels)
		expectedSamples, err = decodeGopusInt16(dec, benchCase.vectors, pcm)
	default:
		err = fmt.Errorf("unknown path %q", path)
	}
	if err != nil {
		return benchmarkResult{}, err
	}
	if expectedSamples <= 0 {
		return benchmarkResult{}, fmt.Errorf("%s/%s decoded no samples", path, benchCase.name)
	}

	allocs, err := gopusAllocsPerRun(path, benchCase.vectors, expectedSamples)
	if err != nil {
		return benchmarkResult{}, err
	}

	runs := make([]benchmarkResult, cfg.count)
	for i := 0; i < cfg.count; i++ {
		iter, elapsed, err := timeGopusCase(path, benchCase.vectors, expectedSamples, cfg)
		if err != nil {
			return benchmarkResult{}, err
		}
		runs[i] = makeResult("gopus", path, benchCase.name, cfg.count, iter, elapsed, packetBytes, packetCount, expectedSamples, &allocs)
	}
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].NsPerSample < runs[j].NsPerSample
	})
	return runs[len(runs)/2], nil
}

func timeGopusCase(path string, vectors []benchmarkVector, expectedSamples int64, cfg runConfig) (int64, time.Duration, error) {
	cfgDec := gopus.DefaultDecoderConfig(sampleRate, channels)
	dec, err := gopus.NewDecoder(cfgDec)
	if err != nil {
		return 0, 0, err
	}
	switch path {
	case "Float32":
		pcm := make([]float32, cfgDec.MaxPacketSamples*cfgDec.Channels)
		start := time.Now()
		var iterations int64
		for {
			got, err := decodeGopusFloat(dec, vectors, pcm)
			if err != nil {
				return 0, 0, err
			}
			if got != expectedSamples {
				return 0, 0, fmt.Errorf("%s decoded samples=%d want=%d", path, got, expectedSamples)
			}
			iterations++
			if time.Since(start) >= cfg.minDuration {
				return iterations, time.Since(start), nil
			}
		}
	case "Int16":
		pcm := make([]int16, cfgDec.MaxPacketSamples*cfgDec.Channels)
		start := time.Now()
		var iterations int64
		for {
			got, err := decodeGopusInt16(dec, vectors, pcm)
			if err != nil {
				return 0, 0, err
			}
			if got != expectedSamples {
				return 0, 0, fmt.Errorf("%s decoded samples=%d want=%d", path, got, expectedSamples)
			}
			iterations++
			if time.Since(start) >= cfg.minDuration {
				return iterations, time.Since(start), nil
			}
		}
	default:
		return 0, 0, fmt.Errorf("unknown path %q", path)
	}
}

func decodeGopusFloat(dec *gopus.Decoder, vectors []benchmarkVector, pcm []float32) (int64, error) {
	var decoded int64
	for _, vector := range vectors {
		dec.Reset()
		for i, packet := range vector.packets {
			n, err := dec.Decode(packet.Data, pcm)
			if err != nil {
				return decoded, fmt.Errorf("%s packet %d: %w", vector.name, i, err)
			}
			decoded += int64(n)
		}
	}
	return decoded, nil
}

func decodeGopusInt16(dec *gopus.Decoder, vectors []benchmarkVector, pcm []int16) (int64, error) {
	var decoded int64
	for _, vector := range vectors {
		dec.Reset()
		for i, packet := range vector.packets {
			n, err := dec.DecodeInt16(packet.Data, pcm)
			if err != nil {
				return decoded, fmt.Errorf("%s packet %d: %w", vector.name, i, err)
			}
			decoded += int64(n)
		}
	}
	return decoded, nil
}

func gopusAllocsPerRun(path string, vectors []benchmarkVector, expectedSamples int64) (float64, error) {
	cfgDec := gopus.DefaultDecoderConfig(sampleRate, channels)
	dec, err := gopus.NewDecoder(cfgDec)
	if err != nil {
		return 0, err
	}
	var decodeErr error
	switch path {
	case "Float32":
		pcm := make([]float32, cfgDec.MaxPacketSamples*cfgDec.Channels)
		if _, err := decodeGopusFloat(dec, vectors, pcm); err != nil {
			return 0, err
		}
		allocs := testing.AllocsPerRun(3, func() {
			if decodeErr != nil {
				return
			}
			got, err := decodeGopusFloat(dec, vectors, pcm)
			if err != nil {
				decodeErr = err
				return
			}
			if got != expectedSamples {
				decodeErr = fmt.Errorf("decoded samples=%d want=%d", got, expectedSamples)
			}
		})
		return allocs, decodeErr
	case "Int16":
		pcm := make([]int16, cfgDec.MaxPacketSamples*cfgDec.Channels)
		if _, err := decodeGopusInt16(dec, vectors, pcm); err != nil {
			return 0, err
		}
		allocs := testing.AllocsPerRun(3, func() {
			if decodeErr != nil {
				return
			}
			got, err := decodeGopusInt16(dec, vectors, pcm)
			if err != nil {
				decodeErr = err
				return
			}
			if got != expectedSamples {
				decodeErr = fmt.Errorf("decoded samples=%d want=%d", got, expectedSamples)
			}
		})
		return allocs, decodeErr
	default:
		return 0, fmt.Errorf("unknown path %q", path)
	}
}

func makeResult(implementation, path, vector string, count int, iterations int64, elapsed time.Duration, bytesPerOp, packetsPerOp, samplesPerOp int64, allocs *float64) benchmarkResult {
	elapsedNS := elapsed.Nanoseconds()
	totalSamples := float64(samplesPerOp) * float64(iterations)
	totalPackets := float64(packetsPerOp) * float64(iterations)
	audioSeconds := totalSamples / sampleRate
	return benchmarkResult{
		Implementation: implementation,
		Path:           path,
		Vector:         vector,
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
	src := filepath.Join(root, "tools", "csrc", "libopus_testvector_bench.c")
	out := filepath.Join(libopusRoot, fmt.Sprintf("gopus_libopus_testvector_bench_%s_%s", runtime.GOOS, runtime.GOARCH))
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
		return "", fmt.Errorf("build libopus benchmark helper: %w (%s)", err, bytes.TrimSpace(output))
	}
	return out, nil
}

func runLibopusBenchmarks(helper string, vectors []benchmarkVector, cfg runConfig) ([]benchmarkResult, error) {
	args := []string{
		"--min-ns", strconv.FormatInt(cfg.minDuration.Nanoseconds(), 10),
		"--count", strconv.Itoa(cfg.count),
		"--cases", cfg.cases,
		"--paths", cfg.paths,
		"--",
	}
	for _, vector := range vectors {
		args = append(args, vector.path)
	}
	cmd := exec.Command(helper, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("run libopus benchmark helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return parseLibopusTSV(output)
}

func parseLibopusTSV(data []byte) ([]benchmarkResult, error) {
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		return nil, fmt.Errorf("libopus benchmark helper returned no rows")
	}
	var results []benchmarkResult
	for i, line := range lines[1:] {
		fields := strings.Split(line, "\t")
		if len(fields) != 12 {
			return nil, fmt.Errorf("libopus row %d has %d fields", i+1, len(fields))
		}
		count, err := strconv.Atoi(fields[3])
		if err != nil {
			return nil, err
		}
		iterations, err := strconv.ParseInt(fields[4], 10, 64)
		if err != nil {
			return nil, err
		}
		elapsedNS, err := strconv.ParseInt(fields[5], 10, 64)
		if err != nil {
			return nil, err
		}
		bytesPerOp, err := strconv.ParseInt(fields[6], 10, 64)
		if err != nil {
			return nil, err
		}
		packetsPerOp, err := strconv.ParseInt(fields[7], 10, 64)
		if err != nil {
			return nil, err
		}
		samplesPerOp, err := strconv.ParseInt(fields[8], 10, 64)
		if err != nil {
			return nil, err
		}
		nsPerSample, err := strconv.ParseFloat(fields[9], 64)
		if err != nil {
			return nil, err
		}
		nsPerPacket, err := strconv.ParseFloat(fields[10], 64)
		if err != nil {
			return nil, err
		}
		xRealtime, err := strconv.ParseFloat(fields[11], 64)
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
		if results[i].Path != results[j].Path {
			return results[i].Path < results[j].Path
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
		m[result.Implementation+"/"+result.Path+"/"+result.Vector] = result
	}
	return m
}

func formatMarkdown(results []benchmarkResult, cfg runConfig) string {
	m := resultMap(results)
	var b strings.Builder
	fmt.Fprintf(&b, "# Official Test Vector Decode Performance\n\n")
	fmt.Fprintf(&b, "This report compares `gopus` with the pinned C reference, libopus %s, on the same RFC 8251 `.bit` vectors.\n\n", libopusVersion)
	fmt.Fprintf(&b, "Methodology: vectors are preloaded, decoder construction and helper startup are excluded, both decoders reset once per vector stream, output is 48 kHz stereo, and each row reports the median run by `ns/sample` across `%d` runs of at least `%s` each.\n\n", cfg.count, cfg.minDuration)
	fmt.Fprintf(&b, "Environment: `%s/%s`, `%s`", runtime.GOOS, runtime.GOARCH, runtime.Version())
	if cpu := hostCPU(); cpu != "" {
		fmt.Fprintf(&b, ", CPU `%s`", cpu)
	}
	fmt.Fprintf(&b, ".\n\n")

	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "| Path | gopus ns/sample | libopus ns/sample | gopus/libopus | gopus realtime | libopus realtime | gopus allocs/op |\n")
	fmt.Fprintf(&b, "| --- | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, path := range []string{"Float32", "Int16"} {
		goResult, okGo := m["gopus/"+path+"/all"]
		cResult, okC := m["libopus/"+path+"/all"]
		if !okGo || !okC {
			continue
		}
		fmt.Fprintf(&b, "| %s | %.2f | %.2f | %.2fx | %.1fx | %.1fx | %s |\n",
			path,
			goResult.NsPerSample,
			cResult.NsPerSample,
			goResult.NsPerSample/cResult.NsPerSample,
			goResult.XRealtime,
			cResult.XRealtime,
			formatAllocs(goResult.Allocations),
		)
	}

	fmt.Fprintf(&b, "\n## Per-Vector Detail\n\n")
	fmt.Fprintf(&b, "| Path | Vector | gopus ns/sample | libopus ns/sample | gopus/libopus | gopus ns/packet | libopus ns/packet | gopus realtime | libopus realtime | gopus allocs/op |\n")
	fmt.Fprintf(&b, "| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, result := range results {
		if result.Implementation != "gopus" || result.Vector == "all" {
			continue
		}
		cResult, ok := m["libopus/"+result.Path+"/"+result.Vector]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "| %s | %s | %.2f | %.2f | %.2fx | %.0f | %.0f | %.1fx | %.1fx | %s |\n",
			result.Path,
			result.Vector,
			result.NsPerSample,
			cResult.NsPerSample,
			result.NsPerSample/cResult.NsPerSample,
			result.NsPerPacket,
			cResult.NsPerPacket,
			result.XRealtime,
			cResult.XRealtime,
			formatAllocs(result.Allocations),
		)
	}

	fmt.Fprintf(&b, "\n## Reproduce\n\n")
	fmt.Fprintf(&b, "```sh\n")
	fmt.Fprintf(&b, "make bench-testvectors-compare\n")
	fmt.Fprintf(&b, "```\n\n")
	fmt.Fprintf(&b, "For raw Go benchmark rows, run:\n\n")
	fmt.Fprintf(&b, "```sh\n")
	fmt.Fprintf(&b, "make bench-testvectors\n")
	fmt.Fprintf(&b, "```\n")
	return b.String()
}

func formatTSV(results []benchmarkResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "implementation\tpath\tvector\tcount\titerations\telapsed_ns\tbytes_per_op\tpackets_per_op\tsamples_per_op\tns_per_sample\tns_per_packet\tx_realtime\tallocs_per_op\n")
	for _, result := range results {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%.6f\t%.6f\t%.6f\t%s\n",
			result.Implementation,
			result.Path,
			result.Vector,
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

func hostCPU() string {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output()
		if err == nil {
			return strings.TrimSpace(string(out))
		}
	case "linux":
		data, err := os.ReadFile("/proc/cpuinfo")
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "model name") || strings.HasPrefix(line, "Hardware") {
					if _, value, ok := strings.Cut(line, ":"); ok {
						return strings.TrimSpace(value)
					}
				}
			}
		}
	}
	return ""
}
