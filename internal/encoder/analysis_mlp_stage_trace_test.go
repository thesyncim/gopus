//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
)

const libopusMLPSourceSHA256 = "162884422e9b91d368b695aa5f84d74f9585cbf2b13a45e6bbca65df880db06c"

var libopusAnalysisMLPStageTraceHelper libopustest.HelperCache

type analysisMLPStageTrace struct {
	frame                                      uint32
	denseCalls, gruCalls, stageCount, overflow uint32
	analysisSourceSHA256, mlpSourceSHA256      string
	dense0Input                                [25]uint32
	dense0Output                               [32]uint32
	gruInput                                   [32]uint32
	gruStateBefore                             [24]uint32
	gruStateAfter                              [24]uint32
	dense2Input                                [24]uint32
	dense2Output                               [2]uint32
}

func TestAnalysisMLPStageTrace(t *testing.T) {
	libopustest.RequireOracle(t)
	requireCELTTraceV3(t, "analysis MLP stage trace")
	if !analysisMLPTraceEnabled {
		t.Fatal("analysis MLP stage trace hook is disabled in this build")
	}

	const (
		fs        = 48000
		channels  = 2
		frameSize = 960
		frames    = 50
		lsbDepth  = 24
	)
	samples, err := testsignal.GenerateEncoderSignalVariant(
		testsignal.EncoderVariantAMMultisineV1,
		fs,
		frameSize*channels*frames,
		channels,
	)
	if err != nil {
		t.Fatalf("generate AMMultisineV1 analysis input: %v", err)
	}
	clampToOpusDemoF32InPlace(samples)
	input := analysisMLPGANIInput(fs, channels, frameSize, frames, lsbDepth, samples)

	baselinePath, err := libopusAnalysisHelper.Path(buildLibopusAnalysisHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "baseline tonality analysis", err)
	}
	baseline, err := libopustest.RunHelper(baselinePath, input)
	if err != nil {
		t.Fatalf("run baseline tonality analysis helper: %v", err)
	}
	if err := validateAnalysisGANO(baseline, frames); err != nil {
		t.Fatalf("baseline GANO structure: %v", err)
	}

	root := celtQuantTraceRepoRoot(t)
	tracePath, err := libopusAnalysisMLPStageTraceHelper.Path(func() (string, error) {
		return buildLibopusAnalysisMLPStageTraceHelper(root)
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "analysis MLP stage trace", err)
	}
	traced, err := libopustest.RunHelper(tracePath, input)
	if err != nil {
		libopustest.HelperUnavailable(t, "analysis MLP stage trace", err)
	}
	const infoBytes = 12*4 + 20
	const recordBytes = 2*infoBytes + 12*4 + 12*4
	const baselineBytes = 12 + frames*recordBytes
	if len(traced) < baselineBytes {
		t.Fatalf("instrumented C output has %d bytes, shorter than GANO prefix %d", len(traced), baselineBytes)
	}
	if !bytes.Equal(traced[:baselineBytes], baseline) {
		t.Fatal("analysis MLP wrappers changed ordinary 50-frame GANO output")
	}
	cTrace, err := parseAnalysisMLPStageTrace(traced[baselineBytes:], uint32(frames*2), uint32(frames))
	if err != nil {
		t.Fatalf("parse GAML: %v", err)
	}
	cFrame := parseAnalysisMLPFirstGANOFrame(baseline)
	if cTrace.dense2Output != [2]uint32{cFrame.latest.musicProb, cFrame.latest.activityProb} {
		t.Fatalf("C dense2 output %08x/%08x is not linked to frame-0 raw analyzer-ring music/VAD %08x/%08x",
			cTrace.dense2Output[0], cTrace.dense2Output[1], cFrame.latest.musicProb, cFrame.latest.activityProb)
	}
	if cTrace.dense0Output != cTrace.gruInput {
		t.Fatal("C dense0 output does not match the actual GRU input")
	}
	if cTrace.gruStateAfter != cTrace.dense2Input {
		t.Fatal("C GRU output state does not match the actual dense2 input")
	}

	plain := NewTonalityAnalysisState(fs)
	plain.SetLSBDepth(lsbDepth)
	tracedState := NewTonalityAnalysisState(fs)
	tracedState.SetLSBDepth(lsbDepth)
	frameSamples := frameSize * channels
	frame0 := samples[:frameSamples]
	oldHook := analysisMLPTraceHook
	t.Cleanup(func() { analysisMLPTraceHook = oldHook })
	var goTrace analysisMLPTraceSnapshot
	var frame0LatestInfo AnalysisInfo
	goCalls := 0
	traceHook := func(snapshot analysisMLPTraceSnapshot) {
		goCalls++
		goTrace = snapshot
	}
	analysisMLPTraceHook = nil
	plainInfo := plain.RunAnalysis(frame0, frameSize, channels)
	analysisMLPTraceHook = traceHook
	frame0TraceInfo := tracedState.RunAnalysis(frame0, frameSize, channels)
	frame0LatestInfo = analysisLatestRawInfo(tracedState)
	analysisMLPTraceHook = nil
	compareGoAnalysisFrame(t, 0, frame0TraceInfo, plainInfo, tracedState, plain)
	for frameIndex := 1; frameIndex < frames; frameIndex++ {
		start := frameIndex * frameSamples
		frame := samples[start : start+frameSamples]
		analysisMLPTraceHook = nil
		plainInfo = plain.RunAnalysis(frame, frameSize, channels)
		analysisMLPTraceHook = traceHook
		traceInfo := tracedState.RunAnalysis(frame, frameSize, channels)
		analysisMLPTraceHook = nil
		compareGoAnalysisFrame(t, frameIndex, traceInfo, plainInfo, tracedState, plain)
	}
	if goCalls != 1 {
		t.Fatalf("Go captured %d MLP snapshots, want one for frame 0", goCalls)
	}
	if goTrace.Frame != 0 || goTrace.Dense0Calls != 1 || goTrace.GRUCalls != 1 || goTrace.Dense2Calls != 1 {
		t.Fatalf("Go MLP snapshot metadata frame=%d dense0=%d GRU=%d dense2=%d",
			goTrace.Frame, goTrace.Dense0Calls, goTrace.GRUCalls, goTrace.Dense2Calls)
	}
	if goTrace.Dense0Output != goTrace.GRUInput {
		t.Fatal("Go dense0 output does not match the actual GRU input")
	}
	if goTrace.GRUStateAfter != goTrace.Dense2Input {
		t.Fatal("Go GRU output state does not match the actual dense2 input")
	}
	if got := [2]uint32{math.Float32bits(goTrace.Dense2Output[0]), math.Float32bits(goTrace.Dense2Output[1])}; got != [2]uint32{math.Float32bits(frame0LatestInfo.MusicProb), math.Float32bits(frame0LatestInfo.VADProb)} {
		t.Fatalf("Go dense2 output %08x/%08x is not linked to frame-0 raw analyzer-ring music/VAD %08x/%08x",
			got[0], got[1], math.Float32bits(frame0LatestInfo.MusicProb), math.Float32bits(frame0LatestInfo.VADProb))
	}

	var firstDifference string
	compareAnalysisMLPArray(t, &firstDifference, "dense0 features", cTrace.dense0Input[:], goTrace.Dense0Input[:])
	compareAnalysisMLPArray(t, &firstDifference, "dense0 output / GRU input", cTrace.dense0Output[:], goTrace.Dense0Output[:])
	compareAnalysisMLPArray(t, &firstDifference, "GRU pre-state", cTrace.gruStateBefore[:], goTrace.GRUStateBefore[:])
	compareAnalysisMLPArray(t, &firstDifference, "GRU post-state", cTrace.gruStateAfter[:], goTrace.GRUStateAfter[:])
	compareAnalysisMLPArray(t, &firstDifference, "dense2 output", cTrace.dense2Output[:], goTrace.Dense2Output[:])
	if firstDifference != "" {
		t.Fatalf("first actual frame-0 MLP difference: %s", firstDifference)
	}
	if d := diffAnalysisInfo(analysisInfoToOracle(frame0TraceInfo), cFrame.ret); d != "" {
		t.Fatalf("frame-0 GANI returned info differs: %s", d)
	}
}

func analysisLatestRawInfo(s *TonalityAnalysisState) AnalysisInfo {
	latest := int(s.WritePos) - 1
	if latest < 0 {
		latest += len(s.Info)
	}
	return s.Info[latest]
}

func compareGoAnalysisFrame(t *testing.T, frame int, tracedInfo, plainInfo AnalysisInfo, traced, plain *TonalityAnalysisState) {
	t.Helper()
	if d := diffAnalysisInfo(analysisInfoToOracle(tracedInfo), analysisInfoToOracle(plainInfo)); d != "" {
		t.Fatalf("Go trace hook changed returned analysis info at frame %d: %s", frame, d)
	}
	if analysisStateScalars(traced) != analysisStateScalars(plain) ||
		analysisStateHashes(traced) != analysisStateHashes(plain) {
		t.Fatalf("Go trace hook changed analyzer state at frame %d", frame)
	}
}

func analysisMLPGANIInput(fs, channels, frameSize, frames, lsbDepth int, samples []float32) []byte {
	payload := libopustest.NewOraclePayloadVersion("GANI", 1,
		uint32(fs), uint32(channels), uint32(frameSize), uint32(frames), uint32(lsbDepth),
		0, ^uint32(1), 0, uint32(len(samples)),
	)
	payload.Float32s(samples...)
	return payload.Bytes()
}

func buildLibopusAnalysisMLPStageTraceHelper(root string) (string, error) {
	analysisPath := libopustest.RefPath("src", "analysis.c")
	analysisSource, err := os.ReadFile(analysisPath)
	if err != nil {
		return "", fmt.Errorf("read selected src/analysis.c: %w", err)
	}
	analysisHash := fmt.Sprintf("%x", sha256.Sum256(analysisSource))
	if analysisHash != libopusAnalysisSourceSHA256 {
		return "", fmt.Errorf("selected src/analysis.c SHA256=%s want pinned %s", analysisHash, libopusAnalysisSourceSHA256)
	}
	mlpSource, err := os.ReadFile(libopustest.RefPath("src", "mlp.c"))
	if err != nil {
		return "", fmt.Errorf("read selected src/mlp.c: %w", err)
	}
	mlpHash := fmt.Sprintf("%x", sha256.Sum256(mlpSource))
	if mlpHash != libopusMLPSourceSHA256 {
		return "", fmt.Errorf("selected src/mlp.c SHA256=%s want pinned %s", mlpHash, libopusMLPSourceSHA256)
	}

	driverPath := filepath.Join(root, "tools", "csrc", "libopus_analysis_info.c")
	driverSource, err := os.ReadFile(driverPath)
	if err != nil {
		return "", fmt.Errorf("read GANI driver: %w", err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(driverSource)); got != libopusAnalysisInfoSourceSHA256 {
		return "", fmt.Errorf("GANI driver SHA256=%s want pinned %s", got, libopusAnalysisInfoSourceSHA256)
	}
	instrumented, err := instrumentAnalysisMLPStageDriver(string(driverSource))
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "gopus-analysis-mlp-stage-")
	if err != nil {
		return "", fmt.Errorf("create copied GANI driver directory: %w", err)
	}
	driverCopy := filepath.Join(dir, "analysis_mlp_stage_driver.c")
	if err := os.WriteFile(driverCopy, []byte(instrumented), 0o600); err != nil {
		return "", fmt.Errorf("write copied GANI driver: %w", err)
	}
	wrapperPath := filepath.Join(root, "tools", "csrc", "libopus_analysis_mlp_stage_trace.c")
	cflags := []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG",
		fmt.Sprintf("-DGOPUS_ANALYSIS_SOURCE_SHA256=%q", analysisHash),
		fmt.Sprintf("-DGOPUS_MLP_SOURCE_SHA256=%q", mlpHash),
	}
	config := libopustest.CHelperConfig{
		Label:       "libopus analysis MLP stage trace",
		OutputBase:  "gopus_libopus_analysis_mlp_stage_trace_f0",
		SourceFile:  driverCopy,
		CFlags:      cflags,
		RefIncludes: []string{"include", "src", "celt", "silk", "silk/float"},
		Sources:     []string{wrapperPath},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		LDFlags: []string{
			"-Wl,--wrap=analysis_compute_dense",
			"-Wl,--wrap=analysis_compute_gru",
		},
		DeadStrip: true,
	}
	return libopustest.BuildCHelper(config)
}

func instrumentAnalysisMLPStageDriver(source string) (string, error) {
	const includeAnchor = "#include \"modes.h\"\n"
	const includeReplacement = includeAnchor + "\nint gopus_analysis_mlp_trace_write(void);\n"
	var err error
	source, err = replaceAnalysisMLPStageAnchor(source, includeAnchor, includeReplacement, "GANI trace declaration")
	if err != nil {
		return "", err
	}
	const endAnchor = "  fflush(stdout);\n  free(pcm);\n  free(st);\n  return 0;"
	const endReplacement = "  if (!gopus_analysis_mlp_trace_write()) return 7;\n" + endAnchor
	return replaceAnalysisMLPStageAnchor(source, endAnchor, endReplacement, "GAML trailer write")
}

func replaceAnalysisMLPStageAnchor(source, old, replacement, label string) (string, error) {
	if bytes.Count([]byte(source), []byte(old)) != 1 {
		return "", fmt.Errorf("GANI source anchor %q is missing or ambiguous", label)
	}
	return string(bytes.Replace([]byte(source), []byte(old), []byte(replacement), 1)), nil
}

func parseAnalysisMLPStageTrace(data []byte, expectedDenseCalls, expectedGRUCalls uint32) (analysisMLPStageTrace, error) {
	var trace analysisMLPStageTrace
	const fixedHeaderBytes = 4 + 6*4 + 2*64
	if len(data) < fixedHeaderBytes {
		return trace, fmt.Errorf("GAML bytes=%d shorter than header=%d", len(data), fixedHeaderBytes)
	}
	if string(data[:4]) != "GAML" {
		return trace, fmt.Errorf("GAML magic=%q", data[:4])
	}
	pos := 4
	readU32 := func() uint32 {
		v := binary.LittleEndian.Uint32(data[pos : pos+4])
		pos += 4
		return v
	}
	version := readU32()
	trace.frame = readU32()
	trace.denseCalls = readU32()
	trace.gruCalls = readU32()
	trace.stageCount = readU32()
	trace.overflow = readU32()
	trace.analysisSourceSHA256 = string(data[pos : pos+64])
	pos += 64
	trace.mlpSourceSHA256 = string(data[pos : pos+64])
	pos += 64
	if version != 1 || trace.frame != 0 || trace.denseCalls != expectedDenseCalls || trace.gruCalls != expectedGRUCalls || trace.stageCount != 3 || trace.overflow != 0 {
		return trace, fmt.Errorf("GAML metadata version=%d frame=%d dense=%d GRU=%d stages=%d overflow=%d",
			version, trace.frame, trace.denseCalls, trace.gruCalls, trace.stageCount, trace.overflow)
	}
	if trace.analysisSourceSHA256 != libopusAnalysisSourceSHA256 || trace.mlpSourceSHA256 != libopusMLPSourceSHA256 {
		return trace, fmt.Errorf("GAML source hashes analysis=%s mlp=%s do not match pinned sources", trace.analysisSourceSHA256, trace.mlpSourceSHA256)
	}
	readHeader := func(stage, inputs, before, after, outputs uint32) error {
		if len(data)-pos < 5*4 {
			return fmt.Errorf("truncated GAML stage %d header", stage)
		}
		got := [5]uint32{readU32(), readU32(), readU32(), readU32(), readU32()}
		want := [5]uint32{stage, inputs, before, after, outputs}
		if got != want {
			return fmt.Errorf("GAML stage header=%v want %v", got, want)
		}
		return nil
	}
	readF32 := func(dst []uint32) error {
		if len(data)-pos < 4*len(dst) {
			return fmt.Errorf("truncated GAML float32 vector: have %d bytes want %d", len(data)-pos, 4*len(dst))
		}
		for i := range dst {
			dst[i] = readU32()
			if dst[i]&0x7f800000 == 0x7f800000 {
				return fmt.Errorf("GAML float32 vector contains nonfinite value at index %d: %08x", i, dst[i])
			}
		}
		return nil
	}
	if err := readHeader(1, 25, 0, 0, 32); err != nil {
		return trace, err
	}
	if err := readF32(trace.dense0Input[:]); err != nil {
		return trace, err
	}
	if err := readF32(trace.dense0Output[:]); err != nil {
		return trace, err
	}
	if err := readHeader(2, 32, 24, 24, 0); err != nil {
		return trace, err
	}
	if err := readF32(trace.gruInput[:]); err != nil {
		return trace, err
	}
	if err := readF32(trace.gruStateBefore[:]); err != nil {
		return trace, err
	}
	if err := readF32(trace.gruStateAfter[:]); err != nil {
		return trace, err
	}
	if err := readHeader(3, 24, 0, 0, 2); err != nil {
		return trace, err
	}
	if err := readF32(trace.dense2Input[:]); err != nil {
		return trace, err
	}
	if err := readF32(trace.dense2Output[:]); err != nil {
		return trace, err
	}
	if pos != len(data) {
		return trace, fmt.Errorf("GAML has %d trailing bytes", len(data)-pos)
	}
	return trace, nil
}

func parseAnalysisMLPFirstGANOFrame(data []byte) analysisOracleFrame {
	const infoBytes = 12*4 + 20
	readInfo := func(b []byte) analysisOracleInfo {
		u := func(i int) uint32 { return binary.LittleEndian.Uint32(b[4*i:]) }
		info := analysisOracleInfo{
			valid: u(0), tonality: u(1), tonalitySlope: u(2), noisiness: u(3), activity: u(4),
			musicProb: u(5), musicMin: u(6), musicMax: u(7), bandwidth: u(8),
			activityProb: u(9), maxPitchRatio: u(10),
		}
		copy(info.leakBoost[:], b[48:48+19])
		return info
	}
	frame := analysisOracleFrame{ret: readInfo(data[12:]), latest: readInfo(data[12+infoBytes:])}
	return frame
}

func compareAnalysisMLPArray(t *testing.T, first *string, name string, cBits []uint32, goValues []float32) {
	t.Helper()
	if len(cBits) != len(goValues) {
		t.Fatalf("%s dimensions C=%d Go=%d", name, len(cBits), len(goValues))
	}
	for i, c := range cBits {
		g := math.Float32bits(goValues[i])
		if c != g {
			if *first == "" {
				*first = name
			}
			t.Logf("%s first difference index=%d C=%08x Go=%08x", name, i, c, g)
			return
		}
	}
	t.Logf("%s matches bitwise (%d values)", name, len(cBits))
}
