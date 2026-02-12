package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type benchmarkGuard struct {
	MaxNsOp     float64 `json:"max_ns_op"`
	MaxBOp      float64 `json:"max_b_op"`
	MaxAllocsOp float64 `json:"max_allocs_op"`
}

type guardConfig struct {
	Package    string                    `json:"package"`
	BenchRegex string                    `json:"bench_regex"`
	Count      int                       `json:"count"`
	Benchtime  string                    `json:"benchtime"`
	CPU        int                       `json:"cpu"`
	Benchmarks map[string]benchmarkGuard `json:"benchmarks"`
}

type sample struct {
	NsOp     float64
	BOp      float64
	AllocsOp float64
}

type metricTriplet struct {
	NsOp     float64
	BOp      float64
	AllocsOp float64
}

func main() {
	var cfgPath string
	flag.StringVar(&cfgPath, "config", "tools/bench_guardrails.json", "path to bench guardrails config")
	flag.Parse()

	cfg, err := loadConfig(cfgPath)
	if err != nil {
		fatalf("load config: %v", err)
	}
	if err := validateConfig(cfg); err != nil {
		fatalf("invalid config: %v", err)
	}

	out, err := runBench(cfg)
	if err != nil {
		fatalf("run benchmark command: %v", err)
	}

	samples, err := parseBenchmarkOutput(out)
	if err != nil {
		fatalf("parse benchmark output: %v", err)
	}

	violations := evaluate(cfg, samples)
	if len(violations) > 0 {
		for _, v := range violations {
			fmt.Fprintln(os.Stderr, v)
		}
		os.Exit(1)
	}

	fmt.Println("benchguard: all configured benchmarks are within guardrails")
}

func loadConfig(path string) (*guardConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg guardConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func validateConfig(cfg *guardConfig) error {
	if cfg.Package == "" {
		return errors.New("package must be set")
	}
	if cfg.BenchRegex == "" {
		return errors.New("bench_regex must be set")
	}
	if cfg.Count <= 0 {
		return errors.New("count must be > 0")
	}
	if cfg.CPU <= 0 {
		return errors.New("cpu must be > 0")
	}
	if cfg.Benchtime == "" {
		return errors.New("benchtime must be set")
	}
	if len(cfg.Benchmarks) == 0 {
		return errors.New("benchmarks must be non-empty")
	}
	return nil
}

func runBench(cfg *guardConfig) ([]byte, error) {
	args := []string{
		"test",
		"-run", "^$",
		"-bench", cfg.BenchRegex,
		"-benchmem",
		"-count", strconv.Itoa(cfg.Count),
		"-benchtime", cfg.Benchtime,
		"-cpu", strconv.Itoa(cfg.CPU),
		cfg.Package,
	}

	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(), "GOMAXPROCS=1")

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Run()
	fmt.Print(buf.String())
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var benchLineRe = regexp.MustCompile(`^(Benchmark\S+?)(?:-\d+)?\s+\d+\s+([0-9.eE+\-]+)\s+ns/op\s+([0-9.eE+\-]+)\s+B/op\s+([0-9.eE+\-]+)\s+allocs/op$`)

func parseBenchmarkOutput(out []byte) (map[string][]sample, error) {
	result := make(map[string][]sample)
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Benchmark") {
			continue
		}
		m := benchLineRe.FindStringSubmatch(line)
		if len(m) != 5 {
			continue
		}
		name := m[1]
		nsOp, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			return nil, fmt.Errorf("parse ns/op for %s: %w", name, err)
		}
		bOp, err := strconv.ParseFloat(m[3], 64)
		if err != nil {
			return nil, fmt.Errorf("parse B/op for %s: %w", name, err)
		}
		allocsOp, err := strconv.ParseFloat(m[4], 64)
		if err != nil {
			return nil, fmt.Errorf("parse allocs/op for %s: %w", name, err)
		}
		result[name] = append(result[name], sample{NsOp: nsOp, BOp: bOp, AllocsOp: allocsOp})
	}
	if len(result) == 0 {
		return nil, errors.New("no benchmark rows parsed")
	}
	return result, nil
}

func evaluate(cfg *guardConfig, samples map[string][]sample) []string {
	violations := make([]string, 0)
	keys := make([]string, 0, len(cfg.Benchmarks))
	for k := range cfg.Benchmarks {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, name := range keys {
		guard := cfg.Benchmarks[name]
		rows, ok := samples[name]
		if !ok || len(rows) == 0 {
			violations = append(violations, fmt.Sprintf("benchguard: missing benchmark in output: %s", name))
			continue
		}
		measured := medianMetrics(rows)
		fmt.Printf("benchguard: %-30s ns/op=%.1f (max %.1f), B/op=%.1f (max %.1f), allocs/op=%.1f (max %.1f)\n",
			name,
			measured.NsOp, guard.MaxNsOp,
			measured.BOp, guard.MaxBOp,
			measured.AllocsOp, guard.MaxAllocsOp,
		)
		if measured.NsOp > guard.MaxNsOp {
			violations = append(violations, fmt.Sprintf("benchguard: %s ns/op regression: measured %.1f > max %.1f", name, measured.NsOp, guard.MaxNsOp))
		}
		if measured.BOp > guard.MaxBOp {
			violations = append(violations, fmt.Sprintf("benchguard: %s B/op regression: measured %.1f > max %.1f", name, measured.BOp, guard.MaxBOp))
		}
		if measured.AllocsOp > guard.MaxAllocsOp {
			violations = append(violations, fmt.Sprintf("benchguard: %s allocs/op regression: measured %.1f > max %.1f", name, measured.AllocsOp, guard.MaxAllocsOp))
		}
	}

	return violations
}

func medianMetrics(rows []sample) metricTriplet {
	nsVals := make([]float64, 0, len(rows))
	bVals := make([]float64, 0, len(rows))
	allocVals := make([]float64, 0, len(rows))
	for _, r := range rows {
		nsVals = append(nsVals, r.NsOp)
		bVals = append(bVals, r.BOp)
		allocVals = append(allocVals, r.AllocsOp)
	}
	return metricTriplet{
		NsOp:     median(nsVals),
		BOp:      median(bVals),
		AllocsOp: median(allocVals),
	}
}

func median(values []float64) float64 {
	sort.Float64s(values)
	n := len(values)
	if n%2 == 1 {
		return values[n/2]
	}
	return (values[n/2-1] + values[n/2]) / 2
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "benchguard: "+format+"\n", args...)
	os.Exit(2)
}
