package main

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

func TestLibopusHelperCompileFlagsIncludeAMD64Target(t *testing.T) {
	target := []string{"-march=x86-64-v3", "-mtune=generic"}
	got := libopusHelperCompileFlags(libopustooling.LibopusReferenceScalar, target)
	want := []string{
		"-std=c99", "-O3", "-DNDEBUG",
		"-fno-tree-vectorize", "-fno-tree-slp-vectorize",
		"-march=x86-64-v3", "-mtune=generic",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scalar helper flags=%v want %v", got, want)
	}
	got = libopusHelperCompileFlags(libopustooling.LibopusReferenceSIMD, target)
	want = []string{"-std=c99", "-O3", "-DNDEBUG", "-march=x86-64-v3", "-mtune=generic"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SIMD helper flags=%v want %v", got, want)
	}
}

func TestEvaluatePerformanceGuardrails(t *testing.T) {
	allocs := 0.0
	results := []benchmarkResult{
		{
			Implementation: "gopus",
			Path:           "Float32",
			Vector:         "all",
			MinDuration:    200 * time.Millisecond,
			NsPerSample:    11,
			Allocations:    &allocs,
		},
		{
			Implementation: "libopus",
			Path:           "Float32",
			Vector:         "all",
			MinDuration:    200 * time.Millisecond,
			NsPerSample:    10,
		},
	}

	cfg := runConfig{
		maxGopusLibopusRatio: 1.2,
		maxGopusAllocsPerOp:  0,
	}
	if violations := evaluatePerformanceGuardrails(results, cfg); len(violations) != 0 {
		t.Fatalf("unexpected violations: %v", violations)
	}

	cfg.maxGopusLibopusRatio = 1.05
	violations := evaluatePerformanceGuardrails(results, cfg)
	if len(violations) != 1 {
		t.Fatalf("violations=%v, want one ratio violation", violations)
	}
	if !strings.Contains(violations[0], "gopus/libopus regression") {
		t.Fatalf("violation %q does not describe ratio regression", violations[0])
	}
}

func TestEvaluatePerformanceGuardrailsRequiresLibopusBaseline(t *testing.T) {
	allocs := 0.0
	results := []benchmarkResult{
		{
			Implementation: "gopus",
			Path:           "Int16",
			Vector:         "all",
			MinDuration:    200 * time.Millisecond,
			NsPerSample:    10,
			Allocations:    &allocs,
		},
	}
	cfg := runConfig{maxGopusLibopusRatio: 1.2}

	violations := evaluatePerformanceGuardrails(results, cfg)
	if len(violations) != 1 {
		t.Fatalf("violations=%v, want missing baseline violation", violations)
	}
	if !strings.Contains(violations[0], "missing libopus baseline") {
		t.Fatalf("violation %q does not describe missing baseline", violations[0])
	}
}

func TestEvaluatePerformanceGuardrailsChecksAllocations(t *testing.T) {
	allocs := 1.0
	results := []benchmarkResult{
		{
			Implementation: "gopus",
			Path:           "Float32",
			Vector:         "all",
			MinDuration:    200 * time.Millisecond,
			NsPerSample:    10,
			Allocations:    &allocs,
		},
	}
	cfg := runConfig{maxGopusAllocsPerOp: 0}

	violations := evaluatePerformanceGuardrails(results, cfg)
	if len(violations) != 1 {
		t.Fatalf("violations=%v, want allocation violation", violations)
	}
	if !strings.Contains(violations[0], "allocations regression") {
		t.Fatalf("violation %q does not describe allocation regression", violations[0])
	}
}
