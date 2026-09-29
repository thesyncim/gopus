package main

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/thesyncim/gopus/internal/libopustooling"
	"github.com/thesyncim/gopus/internal/testsignal"
)

func TestLibopusHelperCompileFlagsIncludeAMD64Target(t *testing.T) {
	target := []string{"-march=x86-64-v3", "-mtune=generic"}
	got := libopusHelperCompileFlags(libopustooling.LibopusReferenceScalar, target)
	want := []string{
		"-O3", "-DNDEBUG",
		"-fno-tree-vectorize", "-fno-tree-slp-vectorize",
		"-march=x86-64-v3", "-mtune=generic",
	}
	assertLibopusHelperCompilerDefaults(t, got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scalar helper flags=%v want %v", got, want)
	}
	got = libopusHelperCompileFlags(libopustooling.LibopusReferenceSIMD, target)
	want = []string{"-O3", "-DNDEBUG", "-march=x86-64-v3", "-mtune=generic"}
	assertLibopusHelperCompilerDefaults(t, got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SIMD helper flags=%v want %v", got, want)
	}
}

func assertLibopusHelperCompilerDefaults(t *testing.T, flags []string) {
	t.Helper()
	for _, flag := range flags {
		if strings.HasPrefix(flag, "-std=") || strings.HasPrefix(flag, "-ffp-contract=") {
			t.Errorf("libopus helper flag %q overrides compiler-default C dialect or FP contraction: %v", flag, flags)
		}
	}
}

func TestFormatWorkloadHashesReportsExactFloat32LEInputs(t *testing.T) {
	workloads := []encoderWorkload{
		{Name: "celt", Variant: testsignal.EncoderVariantAMMultisineV1, FrameSize: 960, Channels: 2, PCM: []float32{0, 0.25, -0.5}},
		{Name: "silk", Variant: testsignal.EncoderVariantSpeechLikeV1, FrameSize: 480, Channels: 1, PCM: []float32{0.125, -0.25}},
	}
	got := formatWorkloadHashes(workloads, "linux", "amd64", "v3", "v3")
	want := strings.Join([]string{
		"encoderbenchcmp-workload-manifest\tgoos=linux\tgoarch=amd64\tgoamd64=v3\ttarget=v3",
		"encoderbenchcmp-workload\tname=celt\tvariant=" + testsignal.EncoderVariantAMMultisineV1 + "\tframe_size=960\tchannels=2\tsamples=3\tsha256_float32le=" + testsignal.HashFloat32LE(workloads[0].PCM),
		"encoderbenchcmp-workload\tname=silk\tvariant=" + testsignal.EncoderVariantSpeechLikeV1 + "\tframe_size=480\tchannels=1\tsamples=2\tsha256_float32le=" + testsignal.HashFloat32LE(workloads[1].PCM),
		"",
	}, "\n")
	if got != want {
		t.Fatalf("workload hash manifest:\n%s\nwant:\n%s", got, want)
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
			Path:           "Float32",
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
