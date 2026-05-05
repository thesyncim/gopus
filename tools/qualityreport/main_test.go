package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseCurrentCompatOutput(t *testing.T) {
	raw := []byte(strings.Join([]string{
		`{"Action":"output","Test":"TestDecoderLossParityLibopusFixture/celt-fb-20ms-mono-64k-plc/periodic9","Output":"    decoder_loss_parity_test.go:296: Q=99.18 delay=0 corr=1.000000 rms_ratio=1.000002 len_ref=48960 len_got=48960\n"}`,
		`{"Action":"pass","Test":"TestDecoderLossParityLibopusFixture/celt-fb-20ms-mono-64k-plc/periodic9"}`,
		`{"Action":"output","Test":"TestDecoderHybridToCELT10msTransitionParity/hybrid-fb-10ms-stereo-24k","Output":"    decoder_transition_parity_test.go:107: transition frame=42 q=99.76 corr=1.000000 meanAbs=0.0 maxAbs=0.0\n"}`,
		`{"Action":"output","Test":"TestDecoderHybridToCELT10msTransitionParity/hybrid-fb-10ms-stereo-24k","Output":"    decoder_transition_parity_test.go:118: next frame=43 q=100.00 corr=1.000000 meanAbs=0.0 maxAbs=0.0\n"}`,
		`{"Action":"pass","Test":"TestDecoderHybridToCELT10msTransitionParity/hybrid-fb-10ms-stereo-24k"}`,
		`{"Action":"pass","Package":"github.com/thesyncim/gopus/testvectors"}`,
		"",
	}, "\n"))

	summary := testRunSummary{
		TestStatus:         make(map[string]string),
		DecoderLossCases:   make(map[string]decoderLossCase),
		TransitionCases:    make(map[string]transitionCase),
		VariantCases:       make(map[string]variantCase),
		EncoderCases:       make(map[string]encoderSummaryCase),
		DecoderParityCases: make(map[string]decoderParityCase),
	}
	if err := parseGoTestJSON(raw, &summary); err != nil {
		t.Fatalf("parse json: %v", err)
	}

	loss := summary.DecoderLossCases["celt-fb-20ms-mono-64k-plc/periodic9"]
	if loss.Q != 99.18 || loss.Delay != 0 || loss.Corr != 1 || loss.RMSRatio != 1.000002 || loss.RefLen != 48960 || loss.GotLen != 48960 {
		t.Fatalf("loss parse mismatch: %+v", loss)
	}
	if loss.Status != "PASS" {
		t.Fatalf("loss status = %q, want PASS", loss.Status)
	}

	transition := summary.TransitionCases["hybrid-fb-10ms-stereo-24k"]
	if transition.TransitionFrame != 42 || transition.TransitionQ != 99.76 || transition.TransitionCorr != 1 {
		t.Fatalf("transition parse mismatch: %+v", transition)
	}
	if transition.NextFrame != 43 || transition.NextQ != 100 || transition.NextCorr != 1 {
		t.Fatalf("next parse mismatch: %+v", transition)
	}
	if transition.Status != "PASS" {
		t.Fatalf("transition status = %q, want PASS", transition.Status)
	}
}

func TestWriteReportIncludesCompatPressurePoints(t *testing.T) {
	out := filepath.Join(t.TempDir(), "quality.md")
	meta := metaInfo{
		GeneratedAt: time.Unix(0, 0).UTC(),
		Branch:      "test",
		Commit:      "abc1234",
		GoVersion:   "go version test",
	}
	quality := testRunSummary{
		Status:             "PASS",
		EncoderSummaryPass: 1,
		EncoderCases:       map[string]encoderSummaryCase{"case-a": {Name: "case-a", Q: 1, LibQ: 1, GapQ: 0, HasLibQ: true, HasGapQ: true, Status: "GOOD"}},
		VariantCases:       map[string]variantCase{},
		DecoderParityCases: map[string]decoderParityCase{},
		TestStatus:         map[string]string{},
	}
	compat := testRunSummary{
		Status: "PASS",
		DecoderLossCases: map[string]decoderLossCase{
			"loss-a": {Name: "loss-a", Q: 99.18, Delay: 0, Corr: 1, RMSRatio: 1.000002},
		},
		TransitionCases: map[string]transitionCase{
			"transition-a": {Name: "transition-a", TransitionFrame: 42, TransitionQ: 99.76, TransitionCorr: 1, NextFrame: 43, NextQ: 100, NextCorr: 1},
		},
		TestStatus: map[string]string{},
	}

	if err := writeReport(out, meta, quality, compat, nil); err != nil {
		t.Fatalf("write report: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	report := string(data)
	for _, want := range []string{
		"min_transition_q",
		"Worst decoder loss case: `loss-a` with `Q=99.18`",
		"Lowest transition-frame Q: `transition-a` at `99.76`",
		"Loss/FEC pressure point: `loss-a` (`Q=99.18`, `delay=0`, `corr=1.000000`, `rms_ratio=1.000002`)",
		"Transition-frame minimum: `transition-a` (`frame=42`, `Q=99.76`, `corr=1.000000`, `meanAbs=0.0`, `maxAbs=0.0`)",
		"Post-transition minimum: `transition-a` (`frame=43`, `Q=100.00`, `corr=1.000000`, `meanAbs=0.0`, `maxAbs=0.0`)",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}

func TestValidateParsedCoverageFailsClosedForPassingRuns(t *testing.T) {
	err := validateParsedCoverage(
		testRunSummary{Status: "PASS"},
		testRunSummary{Status: "PASS"},
	)
	if err == nil {
		t.Fatal("expected missing coverage error")
	}
	msg := err.Error()
	for _, want := range []string{
		"encoder summary cases",
		"encoder variant cases",
		"decoder parity cases",
		"decoder loss cases",
		"transition cases",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("coverage error missing %q: %s", want, msg)
		}
	}
}

func TestValidateParsedCoverageAllowsCompletePassingRuns(t *testing.T) {
	err := validateParsedCoverage(
		testRunSummary{
			Status:             "PASS",
			EncoderCases:       map[string]encoderSummaryCase{"case-a": {Name: "case-a"}},
			VariantCases:       map[string]variantCase{"variant-a": {Name: "variant-a"}},
			DecoderParityCases: map[string]decoderParityCase{"decoder-a": {Name: "decoder-a"}},
		},
		testRunSummary{
			Status:           "PASS",
			DecoderLossCases: map[string]decoderLossCase{"loss-a": {Name: "loss-a"}},
			TransitionCases:  map[string]transitionCase{"transition-a": {Name: "transition-a"}},
		},
	)
	if err != nil {
		t.Fatalf("validate coverage: %v", err)
	}
}
