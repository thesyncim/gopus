//go:build linux && amd64 && amd64.v3 && goexperiment.simd && !nosimd && !purego && gopus_celt_trace && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"simd/archsimd"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"github.com/thesyncim/gopus/internal/testsignal"
)

const (
	analysisAtan2InputMagic  = "GATI"
	analysisAtan2OutputMagic = "GATO"
	analysisAtan2CaseLimit   = 4096
)

var analysisAtan2KernelHelper libopustest.HelperCache
var analysisAtan2SIMDAllocSink float32

type analysisAtan2KernelCase struct {
	name string
	y, x float32
}

func requireAnalysisAtan2V3SIMDReference(t *testing.T) {
	t.Helper()
	target, err := libopustooling.ResolveLibopusAMD64Target()
	if err == nil && target == "v3" {
		variant, variantErr := libopustooling.ResolveLibopusReferenceVariant()
		if variantErr == nil && variant == libopustooling.LibopusReferenceSIMD {
			return
		}
		if variantErr != nil {
			err = variantErr
		} else {
			err = fmt.Errorf("selected libopus reference lane=%q, want SIMD", variant)
		}
	}
	message := "analysis atan2 source oracle requires GOPUS_LIBOPUS_AMD64_TARGET=v3 and the paired SIMD reference"
	if err != nil {
		message += ": " + err.Error()
	} else {
		message += fmt.Sprintf(", got target %q", target)
	}
	if libopustest.StrictRefRequired() {
		t.Fatal(message)
	}
	t.Skip(message)
}

func buildAnalysisAtan2KernelHelper() (string, string, error) {
	analysisPath := libopustest.RefPath("src", "analysis.c")
	root := filepath.Clean(filepath.Join(filepath.Dir(analysisPath), "..", "..", ".."))
	if !libopustooling.EnsureLibopusSIMD(libopustooling.DefaultVersion, []string{root}) {
		return "", "", fmt.Errorf("ensure paired SIMD libopus reference at %s", root)
	}
	mathopsPath := libopustest.RefPath("celt", "mathops.h")
	mathopsSource, err := os.ReadFile(mathopsPath)
	if err != nil {
		return "", "", fmt.Errorf("read selected libopus celt/mathops.h: %w", err)
	}
	sourceHash := sha256.Sum256(mathopsSource)
	sourceHashHex := hex.EncodeToString(sourceHash[:])
	cfg := libopustest.CHelperConfig{
		Label:      "libopus analysis fast_atan2f source kernel",
		OutputBase: "gopus_libopus_analysis_atan2_kernel",
		SourceFile: "libopus_analysis_atan2_kernel.c",
		SIMDRef:    true,
		CFlags: []string{
			"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG",
			fmt.Sprintf("-DGOPUS_ANALYSIS_MATHOPS_SHA256=%q", sourceHashHex),
		},
		RefIncludes: []string{"include", "src", "celt", "silk", "silk/float"},
		Libs:        []string{libopustest.SIMDRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	}
	path, err := analysisAtan2KernelHelper.CHelperPath(cfg)
	if err != nil {
		return "", "", err
	}
	return path, sourceHashHex, nil
}

func probeAnalysisAtan2Kernel(cases []analysisAtan2KernelCase) ([]uint32, string, error) {
	if len(cases) == 0 {
		return nil, "", fmt.Errorf("analysis atan2 source kernel requires at least one case")
	}
	if len(cases) > analysisAtan2CaseLimit {
		return nil, "", fmt.Errorf("analysis atan2 case count=%d exceeds %d", len(cases), analysisAtan2CaseLimit)
	}
	binPath, sourceHash, err := buildAnalysisAtan2KernelHelper()
	if err != nil {
		return nil, "", err
	}
	payload := libopustest.NewOraclePayload(analysisAtan2InputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.Float32(tc.y)
		payload.Float32(tc.x)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "analysis fast_atan2f source kernel", analysisAtan2OutputMagic)
	if err != nil {
		return nil, "", err
	}
	count := reader.Count(len(cases))
	if count != len(cases) {
		return nil, "", fmt.Errorf("analysis atan2 helper count=%d want %d", count, len(cases))
	}
	if got := string(reader.Bytes(64)); got != sourceHash {
		return nil, "", fmt.Errorf("helper mathops.h SHA256=%s want selected source %s", got, sourceHash)
	}
	reader.ExpectRemaining(4 * count)
	results := make([]uint32, count)
	for i := range results {
		results[i] = reader.U32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, "", err
	}
	return results, sourceHash, nil
}

func analysisAtan2SourceCases() []analysisAtan2KernelCase {
	return []analysisAtan2KernelCase{
		{name: "positive-zero", y: 0, x: 0},
		{name: "negative-y-zero", y: math.Float32frombits(0x80000000), x: 0},
		{name: "negative-x-zero", y: 0, x: math.Float32frombits(0x80000000)},
		{name: "both-negative-zero", y: math.Float32frombits(0x80000000), x: math.Float32frombits(0x80000000)},
		{name: "tiny-below-cutoff-y-heavy", y: 2e-10, x: 1e-10},
		{name: "tiny-below-cutoff-x-heavy", y: -1e-10, x: 2e-10},
		{name: "above-cutoff-positive", y: 2e-9, x: 1e-9},
		{name: "above-cutoff-negative", y: -1e-9, x: 2e-9},
		{name: "y-heavy-positive", y: 3, x: 1},
		{name: "y-heavy-negative", y: -3, x: 1},
		{name: "y-heavy-negative-x", y: 3, x: -1},
		{name: "y-heavy-both-negative", y: -3, x: -1},
		{name: "x-heavy-positive", y: 1, x: 3},
		{name: "x-heavy-negative-y", y: -1, x: 3},
		{name: "x-heavy-negative-x", y: 1, x: -3},
		{name: "x-heavy-both-negative", y: -1, x: -3},
		{name: "equal-positive", y: 1, x: 1},
		{name: "equal-negative-y", y: -1, x: 1},
		{name: "equal-negative-x", y: 1, x: -1},
		{name: "equal-both-negative", y: -1, x: -1},
		{name: "bounded-large", y: 30000, x: -17000},
		{name: "bounded-small", y: -0.125, x: 0.0625},
	}
}

func analysisAtan2CapturedPhaseCases(t *testing.T) []analysisAtan2KernelCase {
	t.Helper()
	const (
		fs        = 48000
		channels  = 2
		frameSize = 960
		frames    = 50
		lsbDepth  = 24
	)
	samples, err := testsignal.GenerateEncoderSignalVariant(
		testsignal.EncoderVariantAMMultisineV1, fs, frameSize*channels*frames, channels,
	)
	if err != nil {
		t.Fatalf("generate analysis phase input: %v", err)
	}
	samples = quantizeCELTTracePCM(samples)
	payload := libopustest.NewOraclePayloadVersion("GANI", 1,
		uint32(fs), uint32(channels), uint32(frameSize), uint32(frames), uint32(lsbDepth),
		0, ^uint32(1), 0, uint32(len(samples)),
	)
	payload.Float32s(samples...)
	input := payload.Bytes()

	baselinePath, err := libopusAnalysisHelper.Path(buildLibopusAnalysisHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "baseline tonality analysis for atan2 operands", err)
	}
	baseline, err := libopustest.RunHelper(baselinePath, input)
	if err != nil {
		libopustest.HelperUnavailable(t, "baseline tonality analysis for atan2 operands", err)
	}
	if err := validateAnalysisGANO(baseline, frames); err != nil {
		t.Fatalf("baseline GANO structure: %v", err)
	}
	tracePath, sourceHash := buildLibopusAnalysisStageTraceHelper(t)
	traced, err := libopustest.RunHelper(tracePath, input)
	if err != nil {
		libopustest.HelperUnavailable(t, "analysis phase operand trace", err)
	}
	if len(traced) < len(baseline) || !bytes.Equal(traced[:len(baseline)], baseline) {
		t.Fatal("phase-instrumented analysis helper changed baseline GANO output")
	}
	trace, phase, err := parseLibopusAnalysisStageTrace(traced[len(baseline):])
	if err != nil {
		t.Fatalf("parse GAST/GAPH for atan2 operands: %v", err)
	}
	if trace.frame != 0 || trace.runCalls != frames || trace.tonalityCalls != 1 ||
		trace.overflow != 0 || trace.stageMask != 15 || trace.sourceHash != sourceHash {
		t.Fatalf("GAST capture frame=%d run=%d tonality=%d overflow=%d mask=%04b source=%s want=%s",
			trace.frame, trace.runCalls, trace.tonalityCalls, trace.overflow, trace.stageMask, trace.sourceHash, sourceHash)
	}
	if phase.frame != trace.frame || phase.totalCalls != 239 || phase.storedCalls != 239 ||
		phase.overflow != 0 || len(phase.records) != 239 {
		t.Fatalf("GAPH capture frame=%d calls=%d stored=%d overflow=%d records=%d",
			phase.frame, phase.totalCalls, phase.storedCalls, phase.overflow, len(phase.records))
	}
	state := NewTonalityAnalysisState(fs)
	state.SetLSBDepth(lsbDepth)
	_ = state.RunAnalysis(samples[:frameSize*channels], frameSize, channels)
	compareAnalysisPhaseInputs(t, phase, state.scratchFFTOut[:])

	cases := make([]analysisAtan2KernelCase, 0, len(phase.records)*2)
	for _, record := range phase.records {
		bin := record.bin
		cases = append(cases,
			analysisAtan2KernelCase{
				name: fmt.Sprintf("captured-bin-%d-angle", bin),
				y:    math.Float32frombits(record.x1i),
				x:    math.Float32frombits(record.x1r),
			},
			analysisAtan2KernelCase{
				name: fmt.Sprintf("captured-bin-%d-angle2", bin),
				y:    math.Float32frombits(record.x2i),
				x:    math.Float32frombits(record.x2r),
			},
		)
	}
	return cases
}

func TestAnalysisAtan2SIMDV3MatchesOriginalLibopusKernel(t *testing.T) {
	requireAnalysisAtan2V3SIMDReference(t)
	libopustest.RequireOracle(t)
	cases := append(analysisAtan2SourceCases(), analysisAtan2CapturedPhaseCases(t)...)
	want, sourceHash, err := probeAnalysisAtan2Kernel(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "analysis fast_atan2f source kernel", err)
	}

	for i, tc := range cases {
		got := analysisAtan2(tc.y, tc.x)
		if gotBits := math.Float32bits(got); gotBits != want[i] {
			t.Fatalf("scalar %s analysisAtan2(%08x,%08x)=%08x C=%08x source=%s",
				tc.name, math.Float32bits(tc.y), math.Float32bits(tc.x), gotBits, want[i], sourceHash)
		}
	}

	for base := 0; base < len(cases); base += 4 {
		var ys, xs [4]float32
		var got [4]float32
		for lane := range 4 {
			index := base + lane
			if index >= len(cases) {
				index = len(cases) - 1
			}
			ys[lane] = cases[index].y
			xs[lane] = cases[index].x
		}
		analysisAtan2x4(archsimd.LoadFloat32x4Array(&ys), archsimd.LoadFloat32x4Array(&xs)).StoreArray(&got)
		for lane := range 4 {
			index := base + lane
			if index >= len(cases) {
				continue
			}
			if gotBits := math.Float32bits(got[lane]); gotBits != want[index] {
				t.Fatalf("SIMD %s analysisAtan2x4(%08x,%08x)=%08x C=%08x source=%s",
					cases[index].name, math.Float32bits(cases[index].y), math.Float32bits(cases[index].x), gotBits, want[index], sourceHash)
			}
		}
	}
}

func TestAnalysisAtan2SIMDV3ZeroAllocs(t *testing.T) {
	var ys = [4]float32{0.75, -0.25, 1e-10, -8}
	var xs = [4]float32{0.125, 3, -2e-10, -1}
	var got [4]float32
	run := func() {
		analysisAtan2x4(archsimd.LoadFloat32x4Array(&ys), archsimd.LoadFloat32x4Array(&xs)).StoreArray(&got)
		analysisAtan2SIMDAllocSink = got[0]
	}
	run()
	if allocs := testing.AllocsPerRun(100, run); allocs != 0 {
		t.Fatalf("analysisAtan2x4 allocated: %g allocs/run", allocs)
	}
}
