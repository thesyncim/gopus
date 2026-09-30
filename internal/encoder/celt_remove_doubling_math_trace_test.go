//go:build linux && amd64 && gopus_celt_trace && gopus_remove_doubling_trace && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
)

const (
	celtRemoveDoublingTraceFrame        = 95
	celtRemoveDoublingPitchSourceSHA256 = "ad93368ae01b6fcadd28905e6ed5a55ad165ae5031654540af77d1ffe55acfd5"
	celtRemoveDoublingTraceMaxDual      = 16
	celtRemoveDoublingTraceMaxYY        = 512
	celtRemoveDoublingTraceMaxGain      = 16
)

var celtRemoveDoublingBoundaryHelper libopustest.HelperCache

type celtRemoveDoublingCTrace struct {
	Overflow    uint32
	Arch        int32
	SourceT0    int32
	SourceGain  float32
	ArchiveT0   int32
	ArchiveGain float32
	Dual        [][2]float32
	YY          []celt.EncodeRemoveDoublingYYTrace
	Gains       []celt.EncodeRemoveDoublingGainTrace
	SqrtDen     []float32
	Sqrt        []float32
	Tail        uint32
}

func TestCELTRemoveDoublingMathBoundariesAgainstPinnedSource(t *testing.T) {
	libopustest.RequireOracle(t)
	requireCELTTraceV3(t, "remove-doubling operand trace")

	pcm, err := testsignal.GenerateEncoderSignalVariant(
		testsignal.EncoderVariantAMMultisineV1,
		48000,
		celtLateFrameSize*celtLateChannels*celtLateFrames,
		celtLateChannels,
	)
	if err != nil {
		t.Fatalf("generate late-frame CBR input: %v", err)
	}
	pcm = quantizeCELTTracePCM(pcm)
	input := celtTraceCBRInputFor(pcm, celtTraceAppCELT, celtTraceBWFULL, celtLateChannels,
		celtLateBitrate, celtLateFrameSize, celtLateFrames, 10)

	ordinaryPath := buildCELTTraceOracle(t, false)
	ordinaryBytes, err := libopustest.RunHelper(ordinaryPath, input)
	if err != nil {
		t.Fatalf("run ordinary CBR oracle: %v", err)
	}
	ordinary, err := parseCELTTraceCBRPrefix(ordinaryBytes)
	if err != nil {
		t.Fatalf("parse ordinary CBR output: %v", err)
	}
	tracePath := buildCELTTraceOracleAtFrame(t, true, celtRemoveDoublingTraceFrame)
	traceBytes, err := libopustest.RunHelper(tracePath, input)
	if err != nil {
		t.Fatalf("run instrumented CBR oracle: %v", err)
	}
	tracePrefix, tracePayload, err := splitCELTTraceOutput(traceBytes)
	if err != nil {
		t.Fatalf("split instrumented CBR output: %v", err)
	}
	instrumented, err := parseCELTTraceCBRPrefix(tracePrefix)
	if err != nil {
		t.Fatalf("parse instrumented CBR output: %v", err)
	}
	if !sameCELTTraceCBROutput(ordinary, instrumented) {
		t.Fatal("ordinary and instrumented CBR packet/range streams differ")
	}
	cStage, err := parseCELTEncodeTrace(tracePayload)
	if err != nil {
		t.Fatalf("parse instrumented C stage output: %v", err)
	}
	if cStage.TraceFrame != celtRemoveDoublingTraceFrame || cStage.Overflow != 0 || len(cStage.RemoveDoubling) != 1 {
		t.Fatalf("C remove-doubling trace shape: frame=%d overflow=%d calls=%d",
			cStage.TraceFrame, cStage.Overflow, len(cStage.RemoveDoubling))
	}

	goEncoder := newCELTTraceEncoderConfig(celtLateChannels, celtLateBitrate)
	plainEncoder := newCELTTraceEncoderConfig(celtLateChannels, celtLateBitrate)
	frameSamples := celtLateFrameSize * celtLateChannels
	for frame := range celtRemoveDoublingTraceFrame + 1 {
		framePCM := pcm[frame*frameSamples : (frame+1)*frameSamples]
		if frame == celtRemoveDoublingTraceFrame {
			goEncoder.celtEncoder.EnableEncodeStageTraceForTesting()
			goEncoder.celtEncoder.EnablePitchAnalysisTraceForTesting()
			goEncoder.celtEncoder.EnableRemoveDoublingOperandTraceForTesting()
		}
		gotPacket, gotErr := goEncoder.Encode(framePCM, celtLateFrameSize)
		if gotErr != nil {
			t.Fatalf("encode traced Go frame %d: %v", frame, gotErr)
		}
		plainPacket, plainErr := plainEncoder.Encode(framePCM, celtLateFrameSize)
		if plainErr != nil {
			t.Fatalf("encode plain Go frame %d: %v", frame, plainErr)
		}
		if !bytes.Equal(gotPacket, plainPacket) || goEncoder.FinalRange() != plainEncoder.FinalRange() {
			t.Fatalf("Go trace changed frame %d packet/range", frame)
		}
	}
	goStage := goEncoder.celtEncoder.EncodeStageTraceForTesting()
	if goStage.StageOverflow || len(goStage.RemoveDoubling) != 1 {
		t.Fatalf("Go remove-doubling trace shape: overflow=%t calls=%d", goStage.StageOverflow, len(goStage.RemoveDoubling))
	}
	goCall, cCall := goStage.RemoveDoubling[0], cStage.RemoveDoubling[0]
	if err := validateCELTRemoveDoublingCallInputs(goCall, cCall); err != nil {
		t.Fatalf("Go/C remove-doubling call inputs differ: %v", err)
	}

	cMath := runCELTRemoveDoublingSourceTrace(t, cCall)
	if cMath.Overflow != 0 || cMath.Tail != 0 {
		t.Fatalf("instrumented pitch.c capture overflow: header=%d tail=%d", cMath.Overflow, cMath.Tail)
	}
	if cMath.Arch != cCall.Arch || cMath.SourceT0 != cCall.T0After || math.Float32bits(cMath.SourceGain) != math.Float32bits(cCall.Gain) ||
		cMath.ArchiveT0 != cCall.T0After || math.Float32bits(cMath.ArchiveGain) != math.Float32bits(cCall.Gain) ||
		cMath.SourceT0 != cMath.ArchiveT0 || math.Float32bits(cMath.SourceGain) != math.Float32bits(cMath.ArchiveGain) {
		t.Fatalf("included-source/selected-archive transparency failed: arch=%d/%d source T0/gain=%d/%08x archive=%d/%08x stream=%d/%08x",
			cMath.Arch, cCall.Arch, cMath.SourceT0, math.Float32bits(cMath.SourceGain), cMath.ArchiveT0, math.Float32bits(cMath.ArchiveGain),
			cCall.T0After, math.Float32bits(cCall.Gain))
	}

	goMath := goCall.Math
	if goMath.Overflow || goMath.DualCount != int32(len(cMath.Dual)) || goMath.GainCount != int32(len(cMath.Gains)) ||
		goMath.GainCount != int32(len(cMath.SqrtDen)) || len(cMath.SqrtDen) != len(cMath.Sqrt) {
		t.Fatalf("Go/C math trace counts differ: Go dual/yy/gain=%d/%d/%d overflow=%t C dual/yy/gain/sqrt=%d/%d/%d/%d overflow=%d/%d",
			goMath.DualCount, goMath.YYCount, goMath.GainCount, goMath.Overflow,
			len(cMath.Dual), len(cMath.YY), len(cMath.Gains), len(cMath.SqrtDen), cMath.Overflow, cMath.Tail)
	}
	if len(cMath.YY) != int(cCall.MaxPeriod/2) || len(cMath.Dual) != len(cMath.Gains) || len(cMath.SqrtDen) != len(cMath.Gains) {
		t.Fatalf("C source trace cardinality differs from pitch.c geometry: maxperiod=%d yy=%d dual=%d gain=%d sqrt=%d",
			cCall.MaxPeriod, len(cMath.YY), len(cMath.Dual), len(cMath.Gains), len(cMath.SqrtDen))
	}
	if len(cMath.Dual) == 0 || goMath.DualCount == 0 {
		t.Fatal("remove-doubling trace lacks its initial dual products")
	}
	validateCELTRemoveDoublingYYSourceModel(t, goMath, cMath)
	for i := range cMath.Dual {
		assertCELTRemoveDoublingFloat(t, fmt.Sprintf("dual[%d].first", i), goMath.DualFirst[i], cMath.Dual[i][0])
		assertCELTRemoveDoublingFloat(t, fmt.Sprintf("dual[%d].second", i), goMath.DualSecond[i], cMath.Dual[i][1])
	}
	if int(goMath.YYCount) > len(cMath.YY) {
		t.Fatalf("Go yy prefix=%d exceeds actual C table=%d", goMath.YYCount, len(cMath.YY))
	}
	for i := range int(goMath.YYCount) {
		got, want := goMath.YY[i], cMath.YY[i]
		if got.Index != want.Index {
			t.Fatalf("yy[%d] index=%d C=%d", i, got.Index, want.Index)
		}
		assertCELTRemoveDoublingFloat(t, fmt.Sprintf("yy[%d].x_before", i), got.XBefore, want.XBefore)
		assertCELTRemoveDoublingFloat(t, fmt.Sprintf("yy[%d].x_after", i), got.XAfter, want.XAfter)
		assertCELTRemoveDoublingFloat(t, fmt.Sprintf("yy[%d].updated", i), got.UpdatedYY, want.UpdatedYY)
		assertCELTRemoveDoublingFloat(t, fmt.Sprintf("yy[%d].lookup", i), got.LookupYY, want.LookupYY)
	}
	for i := range cMath.Gains {
		got, want := goMath.Gains[i], cMath.Gains[i]
		assertCELTRemoveDoublingFloat(t, fmt.Sprintf("gain[%d].xy", i), got.XY, want.XY)
		assertCELTRemoveDoublingFloat(t, fmt.Sprintf("gain[%d].xx", i), got.XX, want.XX)
		assertCELTRemoveDoublingFloat(t, fmt.Sprintf("gain[%d].yy", i), got.YY, want.YY)
		assertCELTRemoveDoublingFloat(t, fmt.Sprintf("gain[%d].denominator", i), got.Denominator, cMath.SqrtDen[i])
		assertCELTRemoveDoublingFloat(t, fmt.Sprintf("gain[%d].sqrt", i), got.Sqrt, cMath.Sqrt[i])
		assertCELTRemoveDoublingFloat(t, fmt.Sprintf("gain[%d].result", i), got.Gain, want.Gain)
	}
	t.Logf("frame %d remove_doubling operands match at dual=%d, yy-prefix=%d, gain=%d; C fills %d yy entries",
		celtRemoveDoublingTraceFrame, goMath.DualCount, goMath.YYCount, goMath.GainCount, len(cMath.YY))
}

func validateCELTRemoveDoublingCallInputs(goCall celt.EncodeRemoveDoublingTrace, cCall celtCBRStageRemoveDoubling) error {
	if goCall.MaxPeriod != cCall.MaxPeriod || goCall.MinPeriod != cCall.MinPeriod || goCall.N != cCall.N ||
		goCall.T0Before != cCall.T0Before || goCall.PrevPeriod != cCall.PrevPeriod ||
		math.Float32bits(goCall.PrevGain) != math.Float32bits(cCall.PrevGain) {
		return fmt.Errorf("geometry/state Go=%d/%d/%d T0=%d prev=%d/%08x C=%d/%d/%d T0=%d prev=%d/%08x",
			goCall.MaxPeriod, goCall.MinPeriod, goCall.N, goCall.T0Before, goCall.PrevPeriod, math.Float32bits(goCall.PrevGain),
			cCall.MaxPeriod, cCall.MinPeriod, cCall.N, cCall.T0Before, cCall.PrevPeriod, math.Float32bits(cCall.PrevGain))
	}
	if len(goCall.Buffer) != len(cCall.Buffer) {
		return fmt.Errorf("buffer lengths Go=%d C=%d", len(goCall.Buffer), len(cCall.Buffer))
	}
	for i := range goCall.Buffer {
		if math.Float32bits(goCall.Buffer[i]) != math.Float32bits(cCall.Buffer[i]) {
			return fmt.Errorf("buffer[%d] Go=%08x C=%08x", i, math.Float32bits(goCall.Buffer[i]), math.Float32bits(cCall.Buffer[i]))
		}
	}
	return nil
}

func assertCELTRemoveDoublingFloat(t *testing.T, name string, got, want float32) {
	t.Helper()
	if math.Float32bits(got) != math.Float32bits(want) {
		t.Fatalf("%s Go=%08x C=%08x", name, math.Float32bits(got), math.Float32bits(want))
	}
}

func validateCELTRemoveDoublingYYSourceModel(t *testing.T, goTrace celt.EncodeRemoveDoublingMathTrace, cTrace celtRemoveDoublingCTrace) {
	t.Helper()
	goYY := goTrace.DualFirst[0]
	for i := range int(goTrace.YYCount) {
		row := goTrace.YY[i]
		want := celtRemoveDoublingYYStepSource32(goYY, row.XBefore, row.XAfter)
		if math.Float32bits(row.UpdatedYY) != math.Float32bits(want) {
			t.Fatalf("Go YY source recurrence[%d]=%08x want %08x", row.Index, math.Float32bits(row.UpdatedYY), math.Float32bits(want))
		}
		goYY = row.UpdatedYY
	}
	cYY := cTrace.Dual[0][0]
	for _, row := range cTrace.YY {
		want := celtRemoveDoublingYYStepSource32(cYY, row.XBefore, row.XAfter)
		if math.Float32bits(row.UpdatedYY) != math.Float32bits(want) {
			t.Fatalf("C YY source recurrence[%d]=%08x want %08x", row.Index, math.Float32bits(row.UpdatedYY), math.Float32bits(want))
		}
		cYY = row.UpdatedYY
	}
}

//go:noinline
func celtRemoveDoublingYYMul32(a, b float32) float32 { return a * b }

//go:noinline
func celtRemoveDoublingYYAdd32(a, b float32) float32 { return a + b }

//go:noinline
func celtRemoveDoublingYYSub32(a, b float32) float32 { return a - b }

func celtRemoveDoublingYYStepSource32(yy, xBefore, xAfter float32) float32 {
	beforeProduct := celtRemoveDoublingYYMul32(xBefore, xBefore)
	updated := celtRemoveDoublingYYAdd32(yy, beforeProduct)
	afterProduct := celtRemoveDoublingYYMul32(xAfter, xAfter)
	return celtRemoveDoublingYYSub32(updated, afterProduct)
}

func buildCELTRemoveDoublingSourceHelper(t *testing.T) string {
	t.Helper()
	returnPath, err := celtRemoveDoublingBoundaryHelper.Path(func() (string, error) {
		refPitchPath := libopustest.RefPath("celt", "pitch.c")
		pitchSource, err := os.ReadFile(refPitchPath)
		if err != nil {
			return "", fmt.Errorf("read selected pitch.c: %w", err)
		}
		sourceHash := sha256.Sum256(pitchSource)
		if got := hex.EncodeToString(sourceHash[:]); got != celtRemoveDoublingPitchSourceSHA256 {
			return "", fmt.Errorf("selected pitch.c sha256=%s want pinned %s", got, celtRemoveDoublingPitchSourceSHA256)
		}
		const call = "compute_pitch_gain(xy, xx, yy)"
		if count := strings.Count(string(pitchSource), call); count != 2 {
			return "", fmt.Errorf("selected pitch.c has %d compute_pitch_gain call sites, want 2", count)
		}
		instrumentedPitch := strings.ReplaceAll(string(pitchSource), call,
			"capture_pitch_gain(xy, xx, yy, compute_pitch_gain(xy, xx, yy))")
		repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(refPitchPath))))
		helperPath := filepath.Join(repoRoot, "tools", "csrc", "libopus_remove_doubling_boundary_trace.c")
		helperSource, err := os.ReadFile(helperPath)
		if err != nil {
			return "", fmt.Errorf("read remove-doubling helper source: %w", err)
		}
		const placeholder = `#include "GOPUS_INSTRUMENTED_PITCH_SOURCE"`
		if strings.Count(string(helperSource), placeholder) != 1 {
			return "", fmt.Errorf("remove-doubling helper lacks one instrumented pitch source marker")
		}
		combined := strings.Replace(string(helperSource), placeholder, instrumentedPitch, 1)
		generated := filepath.Join(t.TempDir(), "libopus_remove_doubling_boundary_trace.c")
		if err := os.WriteFile(generated, []byte(combined), 0o600); err != nil {
			return "", fmt.Errorf("write source-bound remove-doubling helper: %w", err)
		}
		cfg := libopustest.CHelperConfig{
			Label:       "CELT remove-doubling source boundary trace",
			OutputBase:  "gopus_libopus_celt_remove_doubling_boundary_trace",
			SourceFile:  generated,
			CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
			RefIncludes: []string{"src", "celt", "silk"},
			Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
			DeadStrip:   true,
		}
		return libopustest.BuildCHelper(cfg)
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT remove-doubling source boundary trace", err)
	}
	return returnPath
}

func runCELTRemoveDoublingSourceTrace(t *testing.T, cCall celtCBRStageRemoveDoubling) celtRemoveDoublingCTrace {
	t.Helper()
	if len(cCall.Buffer) == 0 || len(cCall.Buffer) > 8192 {
		t.Fatalf("C remove-doubling input length out of bounds: %d", len(cCall.Buffer))
	}
	payload := libopustest.NewOraclePayloadVersion("GPRQ", 2, uint32(len(cCall.Buffer)), uint32(cCall.MaxPeriod),
		uint32(cCall.MinPeriod), uint32(cCall.N), uint32(cCall.T0Before), uint32(cCall.PrevPeriod))
	payload.I32(cCall.Arch)
	payload.Float32(cCall.PrevGain)
	payload.Float32s(cCall.Buffer...)
	out, err := libopustest.RunHelper(buildCELTRemoveDoublingSourceHelper(t), payload.Bytes())
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT remove-doubling source boundary trace", err)
	}
	reader, version, err := libopustest.NewOracleReaderMagicVersion("CELT remove-doubling source boundary trace", "GPRS", out)
	if err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("CELT remove-doubling helper version=%d want 2", version)
	}
	trace := celtRemoveDoublingCTrace{
		Overflow: reader.U32(), Arch: reader.I32(), SourceT0: reader.I32(), SourceGain: reader.Float32(),
		ArchiveT0: reader.I32(), ArchiveGain: reader.Float32(),
	}
	dualCount := int(reader.U32())
	if dualCount == 0 || dualCount > celtRemoveDoublingTraceMaxDual {
		t.Fatalf("C dual count out of bounds: %d", dualCount)
	}
	trace.Dual = make([][2]float32, dualCount)
	for i := range trace.Dual {
		trace.Dual[i] = [2]float32{reader.Float32(), reader.Float32()}
	}
	yyCount := int(reader.U32())
	if yyCount == 0 || yyCount > celtRemoveDoublingTraceMaxYY {
		t.Fatalf("C yy count out of bounds: %d", yyCount)
	}
	trace.YY = make([]celt.EncodeRemoveDoublingYYTrace, yyCount)
	for i := range trace.YY {
		trace.YY[i] = celt.EncodeRemoveDoublingYYTrace{
			Index: reader.I32(), XBefore: reader.Float32(), XAfter: reader.Float32(),
			UpdatedYY: reader.Float32(), LookupYY: reader.Float32(),
		}
	}
	gainCount := int(reader.U32())
	if gainCount == 0 || gainCount > celtRemoveDoublingTraceMaxGain {
		t.Fatalf("C gain count out of bounds: %d", gainCount)
	}
	trace.Gains = make([]celt.EncodeRemoveDoublingGainTrace, gainCount)
	for i := range trace.Gains {
		trace.Gains[i] = celt.EncodeRemoveDoublingGainTrace{
			XY: reader.Float32(), XX: reader.Float32(), YY: reader.Float32(), Gain: reader.Float32(),
		}
	}
	sqrtCount := int(reader.U32())
	if sqrtCount == 0 || sqrtCount > celtRemoveDoublingTraceMaxGain {
		t.Fatalf("C sqrt count out of bounds: %d", sqrtCount)
	}
	trace.SqrtDen = make([]float32, sqrtCount)
	trace.Sqrt = make([]float32, sqrtCount)
	for i := range trace.SqrtDen {
		trace.SqrtDen[i], trace.Sqrt[i] = reader.Float32(), reader.Float32()
	}
	trace.Tail = reader.U32()
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return trace
}
