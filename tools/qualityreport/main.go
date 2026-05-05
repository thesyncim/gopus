package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	qualityRunName  = "quality"
	compatRunName   = "compat"
	qualityRunRegex = "TestEncoderComplianceSummary|TestEncoderCompliancePrecisionGuard|TestEncoderVariantProfileParityAgainstLibopusFixture|TestDecoderParityLibopusMatrix"
	compatRunRegex  = "TestDecoderLossParityLibopusFixture|TestDecoderHybridToCELT10msTransitionParity|TestDecoderHybridToCELT20msTransitionParity"
)

var (
	encoderSummaryLineRE = regexp.MustCompile(`^([A-Za-z0-9.-]+)\s+([-+0-9.]+)\s+([-+0-9.]+|-)\s+([-+0-9.]+|-)\s+(GOOD|BASE|FAIL)$`)
	encoderTotalLineRE   = regexp.MustCompile(`^Total:\s+(\d+)\s+passed,\s+(\d+)\s+failed$`)
	variantLineRE        = regexp.MustCompile(`^goQ=([-+0-9.]+)\(goBestDelay=([-+0-9]+) dec=(\d+) cmp=(\d+)\)\s+libQ=([-+0-9.]+)\(libBestDelay=([-+0-9]+) dec=(\d+) cmp=(\d+)\)\s+gapQ=([-+0-9.]+)\s+meanAbs=([-+0-9.]+)\s+p95Abs=([-+0-9.]+)\s+mismatch=([-+0-9.]+)%\s+histL1=([-+0-9.]+)\s+payloadMismatch=(\d+)/(\d+)\s+firstPayloadMismatch=([-+0-9]+)$`)
	decoderParityLineRE  = regexp.MustCompile(`^Q=([-+0-9.]+)\s+delay=([-+0-9]+)\s+corr=([-+0-9.]+)\s+rms_ratio=([-+0-9.]+)$`)
	decoderLossLineRE    = regexp.MustCompile(`^Q=([-+0-9.]+)\s+delay=([-+0-9]+)\s+corr=([-+0-9.]+)\s+rms_ratio=([-+0-9.]+)\s+len_ref=(\d+)\s+len_got=(\d+)$`)
	transitionLineRE     = regexp.MustCompile(`^(transition|next)\s+frame=(\d+)\s+q=([-+0-9.]+)\s+corr=([-+0-9.]+)\s+meanAbs=([-+0-9.]+)\s+maxAbs=([-+0-9.]+)$`)
)

type goTestEvent struct {
	Action  string  `json:"Action"`
	Output  string  `json:"Output"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
}

type encoderSummaryCase struct {
	Name    string
	Q       float64
	HasLibQ bool
	LibQ    float64
	HasGapQ bool
	GapQ    float64
	Status  string
}

type variantCase struct {
	Name                 string
	GoQ                  float64
	LibQ                 float64
	GapQ                 float64
	MeanAbsPacketLen     float64
	P95AbsPacketLen      float64
	ModeMismatchPercent  float64
	HistogramL1          float64
	PayloadMismatch      int
	PayloadCompared      int
	FirstPayloadMismatch int
	Status               string
}

type decoderParityCase struct {
	Name     string
	Q        float64
	Delay    int
	Corr     float64
	RMSRatio float64
	Status   string
}

type decoderLossCase struct {
	Name     string
	Q        float64
	Delay    int
	Corr     float64
	RMSRatio float64
	RefLen   int
	GotLen   int
	Status   string
}

type transitionCase struct {
	Name              string
	TransitionFrame   int
	TransitionQ       float64
	TransitionCorr    float64
	TransitionMeanAbs float64
	TransitionMaxAbs  float64
	NextFrame         int
	NextQ             float64
	NextCorr          float64
	NextMeanAbs       float64
	NextMaxAbs        float64
	Status            string
}

type testRunSummary struct {
	Name          string
	Command       []string
	LogPath       string
	Status        string
	PackageStatus string
	Elapsed       time.Duration
	TestStatus    map[string]string

	EncoderCases       map[string]encoderSummaryCase
	EncoderSummaryPass int
	EncoderSummaryFail int
	VariantCases       map[string]variantCase
	DecoderParityCases map[string]decoderParityCase
	DecoderLossCases   map[string]decoderLossCase
	TransitionCases    map[string]transitionCase
}

type metaInfo struct {
	GeneratedAt time.Time
	Branch      string
	Commit      string
	Dirty       bool
	GoVersion   string
}

type ledgerRow struct {
	Commit      string
	Quality     string
	BenchGuard  string
	MeanGapQ    float64
	MinGapQ     float64
	Score       float64
	Status      string
	Description string
}

func main() {
	var outDir string
	flag.StringVar(&outDir, "out-dir", "reports/quality", "output directory for the report bundle")
	flag.Parse()

	root, err := os.Getwd()
	if err != nil {
		exitf("resolve working directory: %v", err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		exitf("create output directory: %v", err)
	}

	meta, err := collectMeta(root)
	if err != nil {
		exitf("collect repo metadata: %v", err)
	}

	stamp := meta.GeneratedAt.UTC().Format("20060102-150405Z")
	qualityLogPath := filepath.Join(outDir, "quality-report-"+stamp+"."+qualityRunName+".jsonl")
	compatLogPath := filepath.Join(outDir, "quality-report-"+stamp+"."+compatRunName+".jsonl")
	reportPath := filepath.Join(outDir, "quality-report-"+stamp+".md")

	qualitySummary, qualityErr := runGoTestJSON(root, qualityRunName, qualityLogPath, []string{"test", "./testvectors", "-run", qualityRunRegex, "-count=1", "-json"})
	compatSummary, compatErr := runGoTestJSON(root, compatRunName, compatLogPath, []string{"test", "./testvectors", "-run", compatRunRegex, "-count=1", "-json"})

	bestRow, bestErr := readBestQualityLedger(filepath.Join(root, "results.quality.tsv"))
	if bestErr != nil && !errors.Is(bestErr, os.ErrNotExist) {
		exitf("read quality ledger: %v", bestErr)
	}

	if err := writeReport(reportPath, meta, qualitySummary, compatSummary, bestRow); err != nil {
		exitf("write report: %v", err)
	}

	fmt.Printf("wrote %s\n", reportPath)

	coverageErr := validateParsedCoverage(qualitySummary, compatSummary)
	if qualityErr != nil || compatErr != nil || coverageErr != nil {
		var parts []string
		if qualityErr != nil {
			parts = append(parts, fmt.Sprintf("%s failed", qualityRunName))
		}
		if compatErr != nil {
			parts = append(parts, fmt.Sprintf("%s failed", compatRunName))
		}
		if coverageErr != nil {
			parts = append(parts, coverageErr.Error())
		}
		exitf("%s; see %s", strings.Join(parts, " and "), reportPath)
	}
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

func collectMeta(root string) (metaInfo, error) {
	branch, err := gitOutput(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return metaInfo{}, err
	}
	commit, err := gitOutput(root, "rev-parse", "--short", "HEAD")
	if err != nil {
		return metaInfo{}, err
	}
	dirtyOut, err := gitOutput(root, "status", "--porcelain")
	if err != nil {
		return metaInfo{}, err
	}
	goVersion, err := commandOutput(root, nil, "go", "version")
	if err != nil {
		return metaInfo{}, err
	}
	return metaInfo{
		GeneratedAt: time.Now().UTC(),
		Branch:      strings.TrimSpace(branch),
		Commit:      strings.TrimSpace(commit),
		Dirty:       strings.TrimSpace(dirtyOut) != "",
		GoVersion:   strings.TrimSpace(goVersion),
	}, nil
}

func gitOutput(root string, args ...string) (string, error) {
	return commandOutput(root, nil, "git", args...)
}

func commandOutput(root string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = root
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w (%s)", name, strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return string(out), nil
}

func runGoTestJSON(root, runName, logPath string, args []string) (testRunSummary, error) {
	summary := testRunSummary{
		Name:               runName,
		Command:            append([]string{"go"}, args...),
		LogPath:            logPath,
		TestStatus:         make(map[string]string),
		EncoderCases:       make(map[string]encoderSummaryCase),
		VariantCases:       make(map[string]variantCase),
		DecoderParityCases: make(map[string]decoderParityCase),
		DecoderLossCases:   make(map[string]decoderLossCase),
		TransitionCases:    make(map[string]transitionCase),
	}

	cmd := exec.Command("go", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"GOWORK=off",
		"GOPUS_TEST_TIER=parity",
		"GOPUS_STRICT_LIBOPUS_REF=1",
	)

	start := time.Now()
	out, err := cmd.CombinedOutput()
	summary.Elapsed = time.Since(start)
	if writeErr := os.WriteFile(logPath, out, 0o644); writeErr != nil {
		return summary, fmt.Errorf("write %s log: %w", runName, writeErr)
	}

	if parseErr := parseGoTestJSON(out, &summary); parseErr != nil {
		return summary, fmt.Errorf("parse %s json: %w", runName, parseErr)
	}
	if summary.PackageStatus == "" {
		if err == nil {
			summary.PackageStatus = "PASS"
		} else {
			summary.PackageStatus = "FAIL"
		}
	}
	summary.Status = summary.PackageStatus
	if err != nil {
		return summary, err
	}
	return summary, nil
}

func parseGoTestJSON(raw []byte, summary *testRunSummary) error {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Bytes()
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var event goTestEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return fmt.Errorf("decode event %q: %w", line, err)
		}
		switch event.Action {
		case "pass", "fail", "skip":
			status := strings.ToUpper(event.Action)
			if event.Test != "" {
				summary.TestStatus[event.Test] = status
			} else {
				summary.PackageStatus = status
			}
		case "output":
			parseOutputLine(summary, event.Test, strings.TrimRight(event.Output, "\n"))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	for name, c := range summary.VariantCases {
		c.Status = summary.TestStatus[variantTestPrefix()+name]
		summary.VariantCases[name] = c
	}
	for name, c := range summary.DecoderParityCases {
		c.Status = summary.TestStatus[decoderParityTestPrefix()+name]
		summary.DecoderParityCases[name] = c
	}
	for name, c := range summary.DecoderLossCases {
		c.Status = summary.TestStatus[decoderLossTestPrefix()+name]
		summary.DecoderLossCases[name] = c
	}
	for name, c := range summary.TransitionCases {
		c.Status = transitionStatus(summary.TestStatus, name)
		summary.TransitionCases[name] = c
	}

	return nil
}

func parseOutputLine(summary *testRunSummary, testName, output string) {
	if msg, ok := extractFileMessage(output, "encoder_compliance_test.go"); ok {
		parseEncoderSummaryMessage(summary, msg)
		return
	}
	if msg, ok := extractFileMessage(output, "encoder_compliance_variants_fixture_test.go"); ok {
		parseVariantMessage(summary, testName, msg)
		return
	}
	if msg, ok := extractFileMessage(output, "decoder_parity_test.go"); ok {
		parseDecoderParityMessage(summary, testName, msg)
		return
	}
	if msg, ok := extractFileMessage(output, "decoder_loss_parity_test.go"); ok {
		parseDecoderLossMessage(summary, testName, msg)
		return
	}
	if msg, ok := extractFileMessage(output, "decoder_transition_parity_test.go"); ok {
		parseTransitionMessage(summary, testName, msg)
	}
}

func extractFileMessage(line, file string) (string, bool) {
	needle := file + ":"
	idx := strings.Index(line, needle)
	if idx < 0 {
		return "", false
	}
	rest := line[idx+len(needle):]
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return "", false
	}
	return strings.TrimSpace(rest[colon+1:]), true
}

func parseEncoderSummaryMessage(summary *testRunSummary, msg string) {
	if m := encoderSummaryLineRE.FindStringSubmatch(msg); m != nil {
		c := encoderSummaryCase{
			Name:   m[1],
			Q:      mustParseFloat(m[2]),
			Status: m[5],
		}
		if m[3] != "-" {
			c.HasLibQ = true
			c.LibQ = mustParseFloat(m[3])
		}
		if m[4] != "-" {
			c.HasGapQ = true
			c.GapQ = mustParseFloat(m[4])
		}
		summary.EncoderCases[c.Name] = c
		return
	}
	if m := encoderTotalLineRE.FindStringSubmatch(msg); m != nil {
		summary.EncoderSummaryPass = mustParseInt(m[1])
		summary.EncoderSummaryFail = mustParseInt(m[2])
	}
}

func parseVariantMessage(summary *testRunSummary, testName, msg string) {
	if !strings.HasPrefix(testName, variantTestPrefix()) {
		return
	}
	m := variantLineRE.FindStringSubmatch(msg)
	if m == nil {
		return
	}
	name := strings.TrimPrefix(testName, variantTestPrefix())
	summary.VariantCases[name] = variantCase{
		Name:                 name,
		GoQ:                  mustParseFloat(m[1]),
		LibQ:                 mustParseFloat(m[5]),
		GapQ:                 mustParseFloat(m[9]),
		MeanAbsPacketLen:     mustParseFloat(m[10]),
		P95AbsPacketLen:      mustParseFloat(m[11]),
		ModeMismatchPercent:  mustParseFloat(m[12]),
		HistogramL1:          mustParseFloat(m[13]),
		PayloadMismatch:      mustParseInt(m[14]),
		PayloadCompared:      mustParseInt(m[15]),
		FirstPayloadMismatch: mustParseInt(m[16]),
	}
}

func parseDecoderParityMessage(summary *testRunSummary, testName, msg string) {
	if !strings.HasPrefix(testName, decoderParityTestPrefix()) {
		return
	}
	m := decoderParityLineRE.FindStringSubmatch(msg)
	if m == nil {
		return
	}
	name := strings.TrimPrefix(testName, decoderParityTestPrefix())
	summary.DecoderParityCases[name] = decoderParityCase{
		Name:     name,
		Q:        mustParseFloat(m[1]),
		Delay:    mustParseInt(m[2]),
		Corr:     mustParseFloat(m[3]),
		RMSRatio: mustParseFloat(m[4]),
	}
}

func parseDecoderLossMessage(summary *testRunSummary, testName, msg string) {
	if !strings.HasPrefix(testName, decoderLossTestPrefix()) {
		return
	}
	m := decoderLossLineRE.FindStringSubmatch(msg)
	if m == nil {
		return
	}
	name := strings.TrimPrefix(testName, decoderLossTestPrefix())
	summary.DecoderLossCases[name] = decoderLossCase{
		Name:     name,
		Q:        mustParseFloat(m[1]),
		Delay:    mustParseInt(m[2]),
		Corr:     mustParseFloat(m[3]),
		RMSRatio: mustParseFloat(m[4]),
		RefLen:   mustParseInt(m[5]),
		GotLen:   mustParseInt(m[6]),
	}
}

func parseTransitionMessage(summary *testRunSummary, testName, msg string) {
	m := transitionLineRE.FindStringSubmatch(msg)
	if m == nil {
		return
	}
	name := transitionCaseName(testName)
	tc := summary.TransitionCases[name]
	tc.Name = name
	frame := mustParseInt(m[2])
	q := mustParseFloat(m[3])
	corr := mustParseFloat(m[4])
	meanAbs := mustParseFloat(m[5])
	maxAbs := mustParseFloat(m[6])
	if m[1] == "transition" {
		tc.TransitionFrame = frame
		tc.TransitionQ = q
		tc.TransitionCorr = corr
		tc.TransitionMeanAbs = meanAbs
		tc.TransitionMaxAbs = maxAbs
	} else {
		tc.NextFrame = frame
		tc.NextQ = q
		tc.NextCorr = corr
		tc.NextMeanAbs = meanAbs
		tc.NextMaxAbs = maxAbs
	}
	summary.TransitionCases[name] = tc
}

func transitionCaseName(testName string) string {
	const prefix10 = "TestDecoderHybridToCELT10msTransitionParity/"
	const prefix20 = "TestDecoderHybridToCELT20msTransitionParity"
	switch {
	case strings.HasPrefix(testName, prefix10):
		return strings.TrimPrefix(testName, prefix10)
	case testName == prefix20:
		return "hybrid-fb-20ms-stereo-24k"
	default:
		return testName
	}
}

func transitionStatus(statuses map[string]string, name string) string {
	if status := statuses["TestDecoderHybridToCELT10msTransitionParity/"+name]; status != "" {
		return status
	}
	if name == "hybrid-fb-20ms-stereo-24k" {
		return statuses["TestDecoderHybridToCELT20msTransitionParity"]
	}
	return ""
}

func variantTestPrefix() string {
	return "TestEncoderVariantProfileParityAgainstLibopusFixture/cases/"
}

func decoderParityTestPrefix() string {
	return "TestDecoderParityLibopusMatrix/"
}

func decoderLossTestPrefix() string {
	return "TestDecoderLossParityLibopusFixture/"
}

func mustParseFloat(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		panic(err)
	}
	return v
}

func mustParseInt(s string) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		panic(err)
	}
	return v
}

func readBestQualityLedger(path string) (*ledgerRow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var best *ledgerRow
	scanner := bufio.NewScanner(bytes.NewReader(data))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if lineNo == 1 {
			continue
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 8 {
			return nil, fmt.Errorf("malformed quality ledger row %d", lineNo)
		}
		row := &ledgerRow{
			Commit:      fields[0],
			Quality:     fields[1],
			BenchGuard:  fields[2],
			MeanGapQ:    mustParseFloat(fields[3]),
			MinGapQ:     mustParseFloat(fields[4]),
			Score:       mustParseFloat(fields[5]),
			Status:      fields[6],
			Description: fields[7],
		}
		if row.Quality != "PASS" || row.BenchGuard != "PASS" {
			continue
		}
		if row.Status != "baseline" && row.Status != "keep" {
			continue
		}
		if best == nil || row.Score > best.Score {
			best = row
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return best, nil
}

func validateParsedCoverage(quality, compat testRunSummary) error {
	var missing []string
	if quality.Status == "PASS" {
		if len(quality.EncoderCases) == 0 {
			missing = append(missing, "encoder summary cases")
		}
		if len(quality.VariantCases) == 0 {
			missing = append(missing, "encoder variant cases")
		}
		if len(quality.DecoderParityCases) == 0 {
			missing = append(missing, "decoder parity cases")
		}
	}
	if compat.Status == "PASS" {
		if len(compat.DecoderLossCases) == 0 {
			missing = append(missing, "decoder loss cases")
		}
		if len(compat.TransitionCases) == 0 {
			missing = append(missing, "transition cases")
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("parsed coverage missing %s", strings.Join(missing, ", "))
}

func writeReport(path string, meta metaInfo, quality, compat testRunSummary, best *ledgerRow) error {
	var b strings.Builder
	currentScore := qualityScore(quality, compat)
	worstEncoder, hasWorstEncoder := lowestEncoderGap(quality)
	worstVariant, hasWorstVariant := lowestVariantGap(quality)
	worstDecoder, hasWorstDecoder := lowestDecoderParityQ(quality)
	worstLoss, hasWorstLoss := lowestDecoderLossQ(compat)
	lowestTransition, hasTransition := lowestTransitionQ(compat)
	lowestNext, hasNext := lowestNextQ(compat)

	fmt.Fprintf(&b, "# Quality Report\n\n")
	fmt.Fprintf(&b, "- Generated (UTC): %s\n", meta.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "- Branch: `%s`\n", meta.Branch)
	fmt.Fprintf(&b, "- Commit: `%s`%s\n", meta.Commit, dirtySuffix(meta.Dirty))
	fmt.Fprintf(&b, "- Go: `%s`\n", meta.GoVersion)
	fmt.Fprintf(&b, "- `test-quality`: `%s` (%s)\n", quality.Status, quality.Elapsed.Round(time.Second))
	fmt.Fprintf(&b, "- `test-compat`: `%s` (%s)\n", compat.Status, compat.Elapsed.Round(time.Second))
	fmt.Fprintf(&b, "- Raw logs: `%s`, `%s`\n", quality.LogPath, compat.LogPath)
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "Encoder/decoder quality uses libopus-relative `opus_compare` Q. Transition guards report per-frame Q plus correlation and absolute-error telemetry.\n\n")

	fmt.Fprintf(&b, "## Snapshot\n\n")
	if meanGap, ok := meanEncoderGapQ(quality); ok {
		fmt.Fprintf(&b, "- Autoresearch quality score: `%.3f` (`mean_gap_q + min_transition_q / 1000`)\n", currentScore)
		fmt.Fprintf(&b, "- Encoder summary: `%d passed, %d failed`, mean gap `%.2f Q`\n", quality.EncoderSummaryPass, quality.EncoderSummaryFail, meanGap)
	}
	if hasWorstEncoder {
		fmt.Fprintf(&b, "- Worst encoder summary case: `%s` at `%.2f Q` gap\n", worstEncoder.Name, worstEncoder.GapQ)
	}
	if variantCount := len(quality.VariantCases); variantCount > 0 {
		fmt.Fprintf(&b, "- Variant parity cases captured: `%d`\n", variantCount)
	}
	if hasWorstVariant {
		fmt.Fprintf(&b, "- Worst variant case: `%s` at `%.2f Q` gap\n", worstVariant.Name, worstVariant.GapQ)
	}
	if hasWorstDecoder {
		fmt.Fprintf(&b, "- Worst decoder parity case: `%s` with `Q=%.2f`, `corr=%.6f`, `rms_ratio=%.6f`\n", worstDecoder.Name, worstDecoder.Q, worstDecoder.Corr, worstDecoder.RMSRatio)
	}
	if hasWorstLoss {
		fmt.Fprintf(&b, "- Worst decoder loss case: `%s` with `Q=%.2f`, `corr=%.6f`\n", worstLoss.Name, worstLoss.Q, worstLoss.Corr)
	}
	if hasTransition {
		fmt.Fprintf(&b, "- Lowest transition-frame Q: `%s` at `%.2f`\n", lowestTransition.Name, lowestTransition.TransitionQ)
	}
	if hasNext {
		fmt.Fprintf(&b, "- Lowest next-frame Q: `%s` at `%.2f`\n", lowestNext.Name, lowestNext.NextQ)
	}
	if best != nil {
		fmt.Fprintf(&b, "- Best ledger row: commit `%s`, score `%.3f`, mean gap `%.2f Q`, min gap `%.2f Q` (`%s`)\n", best.Commit, best.Score, best.MeanGapQ, best.MinGapQ, best.Description)
	}
	fmt.Fprintf(&b, "\n")

	failingQuality := failingTests(quality)
	failingCompat := failingTests(compat)
	if len(failingQuality) > 0 || len(failingCompat) > 0 {
		fmt.Fprintf(&b, "## Failing Tests\n\n")
		for _, name := range failingQuality {
			fmt.Fprintf(&b, "- `test-quality`: `%s`\n", name)
		}
		for _, name := range failingCompat {
			fmt.Fprintf(&b, "- `test-compat`: `%s`\n", name)
		}
		fmt.Fprintf(&b, "\n")
	}

	if rows := lowestEncoderGapRows(quality, 5); len(rows) > 0 {
		fmt.Fprintf(&b, "## Worst Encoder Summary Cases\n\n")
		fmt.Fprintf(&b, "| Case | GapQ | Q | LibQ | Status |\n")
		fmt.Fprintf(&b, "| --- | ---: | ---: | ---: | --- |\n")
		for _, row := range rows {
			fmt.Fprintf(&b, "| `%s` | %.2f | %.2f | %s | %s |\n", row.Name, row.GapQ, row.Q, formatOptionalFloat(row.HasLibQ, row.LibQ), row.Status)
		}
		fmt.Fprintf(&b, "\n")
	}

	if rows := lowestVariantGapRows(quality, 5); len(rows) > 0 {
		fmt.Fprintf(&b, "## Worst Variant Cases\n\n")
		fmt.Fprintf(&b, "| Case | GapQ | MeanAbsLen | ModeMismatch | HistL1 |\n")
		fmt.Fprintf(&b, "| --- | ---: | ---: | ---: | ---: |\n")
		for _, row := range rows {
			fmt.Fprintf(&b, "| `%s` | %.2f | %.2f | %.2f%% | %.3f |\n", row.Name, row.GapQ, row.MeanAbsPacketLen, row.ModeMismatchPercent, row.HistogramL1)
		}
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, "## Decoder Edge Cases\n\n")
	if hasWorstDecoder {
		fmt.Fprintf(&b, "- Decoder parity pressure point: `%s` (`Q=%.2f`, `delay=%d`, `corr=%.6f`, `rms_ratio=%.6f`)\n", worstDecoder.Name, worstDecoder.Q, worstDecoder.Delay, worstDecoder.Corr, worstDecoder.RMSRatio)
	}
	if hasWorstLoss {
		fmt.Fprintf(&b, "- Loss/FEC pressure point: `%s` (`Q=%.2f`, `delay=%d`, `corr=%.6f`, `rms_ratio=%.6f`)\n", worstLoss.Name, worstLoss.Q, worstLoss.Delay, worstLoss.Corr, worstLoss.RMSRatio)
	}
	if hasTransition {
		fmt.Fprintf(&b, "- Transition-frame minimum: `%s` (`frame=%d`, `Q=%.2f`, `corr=%.6f`, `meanAbs=%.1f`, `maxAbs=%.1f`)\n", lowestTransition.Name, lowestTransition.TransitionFrame, lowestTransition.TransitionQ, lowestTransition.TransitionCorr, lowestTransition.TransitionMeanAbs, lowestTransition.TransitionMaxAbs)
	}
	if hasNext {
		fmt.Fprintf(&b, "- Post-transition minimum: `%s` (`frame=%d`, `Q=%.2f`, `corr=%.6f`, `meanAbs=%.1f`, `maxAbs=%.1f`)\n", lowestNext.Name, lowestNext.NextFrame, lowestNext.NextQ, lowestNext.NextCorr, lowestNext.NextMeanAbs, lowestNext.NextMaxAbs)
	}
	fmt.Fprintf(&b, "\n")

	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func dirtySuffix(dirty bool) string {
	if dirty {
		return " (dirty)"
	}
	return ""
}

func formatOptionalFloat(ok bool, v float64) string {
	if !ok {
		return "-"
	}
	return fmt.Sprintf("%.2f", v)
}

func meanEncoderGapQ(summary testRunSummary) (float64, bool) {
	var sum float64
	var count int
	for _, c := range summary.EncoderCases {
		if !c.HasGapQ {
			continue
		}
		sum += c.GapQ
		count++
	}
	if count == 0 {
		return 0, false
	}
	return sum / float64(count), true
}

func qualityScore(quality, compat testRunSummary) float64 {
	meanGap, ok := meanEncoderGapQ(quality)
	if !ok {
		return 0
	}
	transition, ok := lowestTransitionQ(compat)
	if !ok {
		return meanGap
	}
	return meanGap + transition.TransitionQ/1000.0
}

func lowestEncoderGap(summary testRunSummary) (encoderSummaryCase, bool) {
	rows := lowestEncoderGapRows(summary, 1)
	if len(rows) == 0 {
		return encoderSummaryCase{}, false
	}
	return rows[0], true
}

func lowestEncoderGapRows(summary testRunSummary, n int) []encoderSummaryCase {
	rows := make([]encoderSummaryCase, 0, len(summary.EncoderCases))
	for _, c := range summary.EncoderCases {
		if c.HasGapQ {
			rows = append(rows, c)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].GapQ == rows[j].GapQ {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].GapQ < rows[j].GapQ
	})
	if len(rows) > n {
		rows = rows[:n]
	}
	return rows
}

func lowestVariantGap(summary testRunSummary) (variantCase, bool) {
	rows := lowestVariantGapRows(summary, 1)
	if len(rows) == 0 {
		return variantCase{}, false
	}
	return rows[0], true
}

func lowestVariantGapRows(summary testRunSummary, n int) []variantCase {
	rows := make([]variantCase, 0, len(summary.VariantCases))
	for _, c := range summary.VariantCases {
		rows = append(rows, c)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].GapQ == rows[j].GapQ {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].GapQ < rows[j].GapQ
	})
	if len(rows) > n {
		rows = rows[:n]
	}
	return rows
}

func lowestDecoderParityQ(summary testRunSummary) (decoderParityCase, bool) {
	rows := make([]decoderParityCase, 0, len(summary.DecoderParityCases))
	for _, c := range summary.DecoderParityCases {
		rows = append(rows, c)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Q == rows[j].Q {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Q < rows[j].Q
	})
	if len(rows) == 0 {
		return decoderParityCase{}, false
	}
	return rows[0], true
}

func lowestDecoderLossQ(summary testRunSummary) (decoderLossCase, bool) {
	rows := make([]decoderLossCase, 0, len(summary.DecoderLossCases))
	for _, c := range summary.DecoderLossCases {
		rows = append(rows, c)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Q == rows[j].Q {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Q < rows[j].Q
	})
	if len(rows) == 0 {
		return decoderLossCase{}, false
	}
	return rows[0], true
}

func lowestTransitionQ(summary testRunSummary) (transitionCase, bool) {
	rows := make([]transitionCase, 0, len(summary.TransitionCases))
	for _, c := range summary.TransitionCases {
		if c.TransitionFrame > 0 || c.TransitionQ != 0 {
			rows = append(rows, c)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].TransitionQ == rows[j].TransitionQ {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].TransitionQ < rows[j].TransitionQ
	})
	if len(rows) == 0 {
		return transitionCase{}, false
	}
	return rows[0], true
}

func lowestNextQ(summary testRunSummary) (transitionCase, bool) {
	rows := make([]transitionCase, 0, len(summary.TransitionCases))
	for _, c := range summary.TransitionCases {
		if c.NextFrame > 0 || c.NextQ != 0 {
			rows = append(rows, c)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].NextQ == rows[j].NextQ {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].NextQ < rows[j].NextQ
	})
	if len(rows) == 0 {
		return transitionCase{}, false
	}
	return rows[0], true
}

func failingTests(summary testRunSummary) []string {
	var names []string
	for name, status := range summary.TestStatus {
		if status == "FAIL" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
