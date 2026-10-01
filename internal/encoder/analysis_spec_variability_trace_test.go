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

var libopusAnalysisSpecVariabilityTraceHelper libopustest.HelperCache

type analysisSpecVariabilityOracle struct {
	frame, calls, overflow uint32
	rows, cols             uint32
	analysisSHA256         string
	driverSHA256           string
	logE                   [NbFrames][NbTBands]uint32
}

type analysisSpecVariabilityModel struct {
	mindist    [NbFrames]float32
	sum        float32
	normalized float32
	result     float32
	feature    float32
}

func TestAnalysisSpecVariabilityCorpusMusicTrace(t *testing.T) {
	libopustest.RequireOracle(t)
	requireCELTTraceV3(t, "analysis spec-variability corpus trace")
	if !analysisSpecVariabilityTraceEnabled || !analysisMLPTraceEnabled {
		t.Fatal("analysis spec-variability or MLP trace hook is disabled in this build")
	}

	const (
		fs        = 48000
		channels  = 1
		frameSize = 960
		frames    = 60
		lsbDepth  = 24
	)
	samples, err := testsignal.GenerateCorpusSignal(
		testsignal.CorpusMusicV1, fs, frames*frameSize*channels, channels,
	)
	if err != nil {
		t.Fatalf("generate corpus_music_v1 analysis input: %v", err)
	}
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
	tracePath, err := libopusAnalysisSpecVariabilityTraceHelper.Path(func() (string, error) {
		return buildLibopusAnalysisSpecVariabilityTraceHelper(root)
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "analysis spec-variability trace", err)
	}
	tracedC, err := libopustest.RunHelper(tracePath, input)
	if err != nil {
		libopustest.HelperUnavailable(t, "analysis spec-variability trace", err)
	}
	const infoBytes = 12*4 + 20
	const recordBytes = 2*infoBytes + 12*4 + 12*4
	const ganoHeaderBytes = 12
	ganoBytes := ganoHeaderBytes + frames*recordBytes
	if len(tracedC) < ganoBytes {
		t.Fatalf("instrumented C output has %d bytes, shorter than GANO prefix %d", len(tracedC), ganoBytes)
	}
	if !bytes.Equal(tracedC[:ganoBytes], baseline) {
		t.Fatal("instrumented C analyzer changed ordinary 60-frame GANO output")
	}
	const gamlBytes = 4 + 6*4 + 2*64 + 3*5*4 + (25+32+32+24+24+24+2)*4
	if len(tracedC) < ganoBytes+gamlBytes {
		t.Fatalf("instrumented C output has %d bytes, shorter than GANO+GAML=%d", len(tracedC), ganoBytes+gamlBytes)
	}
	cMLP, err := parseAnalysisMLPStageTrace(tracedC[ganoBytes:ganoBytes+gamlBytes], uint32(frames*2), uint32(frames))
	if err != nil {
		t.Fatalf("parse GAML: %v", err)
	}
	cSpec, err := parseAnalysisSpecVariabilityTrace(tracedC[ganoBytes+gamlBytes:])
	if err != nil {
		t.Fatalf("parse GVAR: %v", err)
	}
	cFrame := parseAnalysisMLPFirstGANOFrame(baseline)
	if cMLP.dense2Output != [2]uint32{cFrame.latest.musicProb, cFrame.latest.activityProb} {
		t.Fatalf("C dense2 output %08x/%08x is not linked to frame-0 raw analyzer-ring music/VAD %08x/%08x",
			cMLP.dense2Output[0], cMLP.dense2Output[1], cFrame.latest.musicProb, cFrame.latest.activityProb)
	}
	if cMLP.dense0Output != cMLP.gruInput || cMLP.gruStateAfter != cMLP.dense2Input {
		t.Fatal("C MLP trace does not follow the actual dense0→GRU→dense2 chain")
	}
	if cSpec.analysisSHA256 != libopusAnalysisSourceSHA256 || cSpec.driverSHA256 != libopusAnalysisInfoSourceSHA256 {
		t.Fatalf("GVAR source hashes analysis=%s driver=%s do not match pinned inputs", cSpec.analysisSHA256, cSpec.driverSHA256)
	}

	plain := NewTonalityAnalysisState(fs)
	plain.SetLSBDepth(lsbDepth)
	traced := NewTonalityAnalysisState(fs)
	traced.SetLSBDepth(lsbDepth)
	oldSpecHook, oldMLPHook := analysisSpecVariabilityTraceHook, analysisMLPTraceHook
	t.Cleanup(func() {
		analysisSpecVariabilityTraceHook = oldSpecHook
		analysisMLPTraceHook = oldMLPHook
	})
	var goSpec analysisSpecVariabilityTraceSnapshot
	var goMLP analysisMLPTraceSnapshot
	var goFrame0Info, goFrame0Latest AnalysisInfo
	specCalls, mlpCalls := 0, 0
	frameSamples := frameSize * channels
	for frameIndex := 0; frameIndex < frames; frameIndex++ {
		start := frameIndex * frameSamples
		frame := samples[start : start+frameSamples]
		analysisSpecVariabilityTraceHook = nil
		analysisMLPTraceHook = nil
		plainInfo := plain.RunAnalysis(frame, frameSize, channels)
		if frameIndex == 0 {
			analysisSpecVariabilityTraceHook = func(snapshot analysisSpecVariabilityTraceSnapshot) {
				specCalls++
				goSpec = snapshot
			}
			analysisMLPTraceHook = func(snapshot analysisMLPTraceSnapshot) {
				mlpCalls++
				goMLP = snapshot
			}
		}
		tracedInfo := traced.RunAnalysis(frame, frameSize, channels)
		analysisSpecVariabilityTraceHook = nil
		analysisMLPTraceHook = nil
		compareGoAnalysisFrame(t, frameIndex, tracedInfo, plainInfo, traced, plain)
		if frameIndex == 0 {
			goFrame0Info = tracedInfo
			goFrame0Latest = analysisLatestRawInfo(traced)
		}
	}
	if specCalls != 1 || mlpCalls != 1 {
		t.Fatalf("Go trace calls specvar=%d MLP=%d want one frame-0 call each", specCalls, mlpCalls)
	}
	if goMLP.Frame != 0 || goMLP.Dense0Calls != 1 || goMLP.GRUCalls != 1 || goMLP.Dense2Calls != 1 {
		t.Fatalf("Go MLP metadata frame=%d dense0=%d GRU=%d dense2=%d",
			goMLP.Frame, goMLP.Dense0Calls, goMLP.GRUCalls, goMLP.Dense2Calls)
	}
	if bits := [2]uint32{math.Float32bits(goMLP.Dense2Output[0]), math.Float32bits(goMLP.Dense2Output[1])}; bits != [2]uint32{
		math.Float32bits(goFrame0Latest.MusicProb), math.Float32bits(goFrame0Latest.VADProb),
	} {
		t.Fatalf("Go dense2 output %08x/%08x is not linked to frame-0 raw analyzer-ring music/VAD %08x/%08x",
			bits[0], bits[1], math.Float32bits(goFrame0Latest.MusicProb), math.Float32bits(goFrame0Latest.VADProb))
	}
	if cSpec.logE != analysisSpecVariabilityBits(goSpec.LogE) {
		if i, j, c, g, ok := firstAnalysisSpecVariabilityDifference(cSpec.logE, goSpec.LogE); ok {
			t.Fatalf("actual frame-0 LogE input differs before spec-variability math at [%d][%d]: C=%08x Go=%08x", i, j, c, g)
		}
		t.Fatal("actual frame-0 LogE input differs before spec-variability math")
	}
	if got := math.Float32bits(goSpec.Result - float32(0.78)); got != math.Float32bits(goMLP.Dense0Input[18]) {
		t.Fatalf("Go spec-variability feature link=%08x MLP feature[18]=%08x", got, math.Float32bits(goMLP.Dense0Input[18]))
	}
	if cSpecModel := modelAnalysisSpecVariabilityC(cSpec.logE); math.Float32bits(cSpecModel.feature) != cMLP.dense0Input[18] {
		t.Fatalf("selected C source model feature[18]=%08x does not reproduce original GAML feature[18]=%08x", math.Float32bits(cSpecModel.feature), cMLP.dense0Input[18])
	}
	if goSpecModel := modelAnalysisSpecVariabilityGo(goSpec.LogE); math.Float32bits(goSpecModel.feature) != math.Float32bits(goMLP.Dense0Input[18]) {
		t.Fatalf("Go source model feature[18]=%08x does not reproduce actual GAML feature[18]=%08x", math.Float32bits(goSpecModel.feature), math.Float32bits(goMLP.Dense0Input[18]))
	}
	cModel := modelAnalysisSpecVariabilityC(cSpec.logE)
	goModel := modelAnalysisSpecVariabilityGo(goSpec.LogE)
	assertAnalysisSpecVariabilityMatches(t, "C source model", cModel, "Go actual", goSpec.MinDist,
		goSpec.Sum, goSpec.Normalized, goSpec.Result)
	if got := math.Float32bits(analysisSpecVariabilityNormalizeC(goModel.sum)); got != math.Float32bits(goSpec.Normalized) {
		t.Fatalf("C /8 then /18 normalization changes Go sum: got %08x actual Go /144=%08x",
			got, math.Float32bits(goSpec.Normalized))
	}
	if got := math.Float32bits(analysisSpecVariabilityNormalizeGo(cModel.sum)); got != math.Float32bits(cModel.normalized) {
		t.Fatalf("Go /144 normalization changes C sum: got %08x C /8 then /18=%08x",
			got, math.Float32bits(cModel.normalized))
	}
	if goModel.mindist != goSpec.MinDist || math.Float32bits(goModel.sum) != math.Float32bits(goSpec.Sum) ||
		math.Float32bits(goModel.normalized) != math.Float32bits(goSpec.Normalized) ||
		math.Float32bits(goModel.result) != math.Float32bits(goSpec.Result) {
		t.Fatalf("Go source model does not reproduce actual hook: model sum/norm/result=%08x/%08x/%08x hook=%08x/%08x/%08x",
			math.Float32bits(goModel.sum), math.Float32bits(goModel.normalized), math.Float32bits(goModel.result),
			math.Float32bits(goSpec.Sum), math.Float32bits(goSpec.Normalized), math.Float32bits(goSpec.Result))
	}
	if d := diffAnalysisInfo(analysisInfoToOracle(goFrame0Info), cFrame.ret); d != "" {
		t.Logf("frame-0 postprocessed GANI returned-info difference: %s", d)
	}
	cFeature := math.Float32frombits(cMLP.dense0Input[18])
	goFeature := goMLP.Dense0Input[18]
	if math.Float32bits(cFeature) != math.Float32bits(goFeature) {
		t.Fatalf("corpus_music_v1 frame-0 spec-variability feature differs: C=%08x Go=%08x", math.Float32bits(cFeature), math.Float32bits(goFeature))
	}
	t.Logf("corpus_music_v1 frame-0 spec-variability feature matches bitwise: %08x", math.Float32bits(cFeature))
}

func assertAnalysisSpecVariabilityMatches(t *testing.T, cName string, c analysisSpecVariabilityModel, goName string,
	goMinDist [NbFrames]float32, goSum, goNormalized, goResult float32) {
	t.Helper()
	for i, cMin := range c.mindist {
		if math.Float32bits(cMin) != math.Float32bits(goMinDist[i]) {
			t.Fatalf("spec-variability minimum row %d differs: %s=%08x %s=%08x", i,
				cName, math.Float32bits(cMin), goName, math.Float32bits(goMinDist[i]))
		}
	}
	if math.Float32bits(c.sum) != math.Float32bits(goSum) {
		t.Fatalf("spec-variability minimum sum differs: %s=%08x %s=%08x", cName,
			math.Float32bits(c.sum), goName, math.Float32bits(goSum))
	}
	if math.Float32bits(c.normalized) != math.Float32bits(goNormalized) {
		t.Fatalf("spec-variability normalized value differs: %s=%08x %s=%08x", cName,
			math.Float32bits(c.normalized), goName, math.Float32bits(goNormalized))
	}
	if math.Float32bits(c.result) != math.Float32bits(goResult) {
		t.Fatalf("spec-variability sqrt result differs: %s=%08x %s=%08x", cName,
			math.Float32bits(c.result), goName, math.Float32bits(goResult))
	}
}

func buildLibopusAnalysisSpecVariabilityTraceHelper(root string) (string, error) {
	analysisSource, err := os.ReadFile(libopustest.RefPath("src", "analysis.c"))
	if err != nil {
		return "", fmt.Errorf("read selected src/analysis.c: %w", err)
	}
	analysisHash := fmt.Sprintf("%x", sha256.Sum256(analysisSource))
	if analysisHash != libopusAnalysisSourceSHA256 {
		return "", fmt.Errorf("selected src/analysis.c SHA256=%s want pinned %s", analysisHash, libopusAnalysisSourceSHA256)
	}
	driverPath := filepath.Join(root, "tools", "csrc", "libopus_analysis_info.c")
	driverSource, err := os.ReadFile(driverPath)
	if err != nil {
		return "", fmt.Errorf("read GANI driver: %w", err)
	}
	driverHash := fmt.Sprintf("%x", sha256.Sum256(driverSource))
	if driverHash != libopusAnalysisInfoSourceSHA256 {
		return "", fmt.Errorf("GANI driver SHA256=%s does not match pinned hash %s",
			driverHash, libopusAnalysisInfoSourceSHA256)
	}
	instrumented, err := instrumentAnalysisSpecVariabilityDriver(string(driverSource))
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "gopus-analysis-specvar-")
	if err != nil {
		return "", fmt.Errorf("create copied GANI driver directory: %w", err)
	}
	driverCopy := filepath.Join(dir, "analysis_specvar_driver.c")
	if err := os.WriteFile(driverCopy, []byte(instrumented), 0o600); err != nil {
		return "", fmt.Errorf("write copied GANI driver: %w", err)
	}
	mlpWrapper := filepath.Join(root, "tools", "csrc", "libopus_analysis_mlp_stage_trace.c")
	specWrapper := filepath.Join(root, "tools", "csrc", "libopus_analysis_spec_variability_trace.c")
	cflags := []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG",
		fmt.Sprintf("-DGOPUS_ANALYSIS_SOURCE_SHA256=%q", analysisHash),
		fmt.Sprintf("-DGOPUS_GANI_DRIVER_SHA256=%q", driverHash),
		fmt.Sprintf("-DGOPUS_MLP_SOURCE_SHA256=%q", libopusMLPSourceSHA256),
	}
	config := libopustest.CHelperConfig{
		Label:       "libopus analysis spec-variability trace",
		OutputBase:  "gopus_libopus_analysis_specvar_trace_f0",
		SourceFile:  driverCopy,
		CFlags:      cflags,
		RefIncludes: []string{"include", "src", "celt", "silk", "silk/float"},
		Sources:     []string{mlpWrapper, specWrapper},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		LDFlags: []string{
			"-Wl,--wrap=analysis_compute_dense",
			"-Wl,--wrap=analysis_compute_gru",
		},
		DeadStrip: true,
	}
	return libopustest.BuildCHelper(config)
}

func instrumentAnalysisSpecVariabilityDriver(source string) (string, error) {
	source, err := instrumentAnalysisMLPStageDriver(source)
	if err != nil {
		return "", err
	}
	const declarationAnchor = "int gopus_analysis_mlp_trace_write(void);\n"
	const declarationReplacement = declarationAnchor +
		"void gopus_analysis_spec_variability_capture(const float *loge);\n" +
		"int gopus_analysis_spec_variability_trace_write(void);\n"
	source, err = replaceAnalysisMLPStageAnchor(source, declarationAnchor, declarationReplacement, "GVAR declarations")
	if err != nil {
		return "", err
	}
	const runAnchor = "    run_analysis(st, mode, frame, (int)frame_size, (int)frame_size, c1, c2, (int)channels,\n" +
		"                 (opus_int32)fs, (int)lsb_depth, downmix, &info);\n"
	const runReplacement = runAnchor + "    if (f == 0) gopus_analysis_spec_variability_capture(&st->logE[0][0]);\n"
	source, err = replaceAnalysisMLPStageAnchor(source, runAnchor, runReplacement, "GVAR frame-0 LogE capture")
	if err != nil {
		return "", err
	}
	const writerAnchor = "  if (!gopus_analysis_mlp_trace_write()) return 7;\n  fflush(stdout);"
	const writerReplacement = "  if (!gopus_analysis_mlp_trace_write()) return 7;\n" +
		"  if (!gopus_analysis_spec_variability_trace_write()) return 8;\n  fflush(stdout);"
	return replaceAnalysisMLPStageAnchor(source, writerAnchor, writerReplacement, "GVAR trailer write")
}

func parseAnalysisSpecVariabilityTrace(data []byte) (analysisSpecVariabilityOracle, error) {
	var trace analysisSpecVariabilityOracle
	const headerBytes = 4 + 6*4 + 2*64
	const payloadBytes = NbFrames * NbTBands * 4
	if len(data) != headerBytes+payloadBytes {
		return trace, fmt.Errorf("GVAR bytes=%d want exactly %d", len(data), headerBytes+payloadBytes)
	}
	if string(data[:4]) != "GVAR" {
		return trace, fmt.Errorf("GVAR magic=%q", data[:4])
	}
	pos := 4
	readU32 := func() uint32 {
		v := binary.LittleEndian.Uint32(data[pos : pos+4])
		pos += 4
		return v
	}
	version := readU32()
	trace.frame = readU32()
	trace.calls = readU32()
	trace.overflow = readU32()
	trace.rows = readU32()
	trace.cols = readU32()
	trace.analysisSHA256 = string(data[pos : pos+64])
	pos += 64
	trace.driverSHA256 = string(data[pos : pos+64])
	pos += 64
	if version != 1 || trace.frame != 0 || trace.calls != 1 || trace.overflow != 0 || trace.rows != NbFrames || trace.cols != NbTBands {
		return trace, fmt.Errorf("GVAR metadata version=%d frame=%d calls=%d overflow=%d rows=%d cols=%d",
			version, trace.frame, trace.calls, trace.overflow, trace.rows, trace.cols)
	}
	if trace.analysisSHA256 != libopusAnalysisSourceSHA256 || trace.driverSHA256 != libopusAnalysisInfoSourceSHA256 {
		return trace, fmt.Errorf("GVAR source hashes analysis=%s driver=%s do not match pinned sources",
			trace.analysisSHA256, trace.driverSHA256)
	}
	for i := range NbFrames {
		for j := range NbTBands {
			bits := readU32()
			if bits&0x7f800000 == 0x7f800000 {
				return trace, fmt.Errorf("GVAR LogE[%d][%d] is nonfinite: %08x", i, j, bits)
			}
			trace.logE[i][j] = bits
		}
	}
	if pos != len(data) {
		return trace, fmt.Errorf("GVAR has %d trailing bytes", len(data)-pos)
	}
	return trace, nil
}

func modelAnalysisSpecVariabilityC(logE [NbFrames][NbTBands]uint32) analysisSpecVariabilityModel {
	var model analysisSpecVariabilityModel
	for i := range NbFrames {
		model.mindist[i] = 1e15
	}
	for i := 0; i < NbFrames-1; i++ {
		for j := i + 1; j < NbFrames; j++ {
			dist := float32(0)
			for k := range NbTBands {
				a := math.Float32frombits(logE[i][k])
				b := math.Float32frombits(logE[j][k])
				d := analysisSpecVariabilityRoundSub(a, b)
				if analysisSpecVariabilityModelSIMD && k >= 16 {
					dist = analysisSpecVariabilityNativeFMA(d, d, dist)
				} else {
					dist = analysisSpecVariabilityRoundAdd(dist, analysisSpecVariabilityRoundMul(d, d))
				}
			}
			if dist < model.mindist[i] {
				model.mindist[i] = dist
			}
			if dist < model.mindist[j] {
				model.mindist[j] = dist
			}
		}
	}
	for i := range NbFrames {
		model.sum = analysisSpecVariabilityRoundAdd(model.sum, model.mindist[i])
	}
	model.normalized = analysisSpecVariabilityRoundDiv(analysisSpecVariabilityRoundDiv(model.sum, 8), 18)
	// libopus src/analysis.c:775 calls C double sqrt before narrowing to float.
	model.result = float32(math.Sqrt(float64(model.normalized)))
	model.feature = analysisSpecVariabilityRoundSub(model.result, float32(0.78))
	return model
}

func modelAnalysisSpecVariabilityGo(logE [NbFrames][NbTBands]float32) analysisSpecVariabilityModel {
	var model analysisSpecVariabilityModel
	for i := range NbFrames {
		model.mindist[i] = 1e15
	}
	for i := 0; i < NbFrames-1; i++ {
		for j := i + 1; j < NbFrames; j++ {
			dist := float32(0)
			for k := range NbTBands {
				d := logE[i][k] - logE[j][k]
				if k < 16 {
					dist = analysisSpecVariabilityAddSquare(dist, d)
				} else {
					dist = analysisSpecVariabilityAddFinalSquare(dist, d)
				}
			}
			if dist < model.mindist[i] {
				model.mindist[i] = dist
			}
			if dist < model.mindist[j] {
				model.mindist[j] = dist
			}
		}
	}
	for i := range NbFrames {
		model.sum += model.mindist[i]
	}
	model.normalized = model.sum / float32(NbFrames*NbTBands)
	// libopus src/analysis.c:775 calls C double sqrt before narrowing to float.
	model.result = float32(math.Sqrt(float64(model.normalized)))
	model.feature = model.result - float32(0.78)
	return model
}

func TestAnalysisSpecVariabilityZeroAlloc(t *testing.T) {
	var logE [NbFrames][NbTBands]float32
	for i := range NbFrames {
		for j := range NbTBands {
			logE[i][j] = float32((i+1)*(j+3)) * (1.0 / 64.0)
		}
	}
	oldHook := analysisSpecVariabilityTraceHook
	analysisSpecVariabilityTraceHook = nil
	t.Cleanup(func() { analysisSpecVariabilityTraceHook = oldHook })
	analysisSpecVariability(&logE)
	if got := testing.AllocsPerRun(100, func() { analysisSpecVariability(&logE) }); got != 0 {
		t.Fatalf("analysisSpecVariability steady-state allocations=%g, want 0", got)
	}
}

func analysisSpecVariabilityNormalizeC(sum float32) float32 {
	return analysisSpecVariabilityRoundDiv(analysisSpecVariabilityRoundDiv(sum, 8), 18)
}

func analysisSpecVariabilityNormalizeGo(sum float32) float32 {
	return sum / float32(NbFrames*NbTBands)
}

func analysisSpecVariabilityBits(logE [NbFrames][NbTBands]float32) [NbFrames][NbTBands]uint32 {
	var bits [NbFrames][NbTBands]uint32
	for i := range NbFrames {
		for j := range NbTBands {
			bits[i][j] = math.Float32bits(logE[i][j])
		}
	}
	return bits
}

func firstAnalysisSpecVariabilityDifference(c [NbFrames][NbTBands]uint32, g [NbFrames][NbTBands]float32) (int, int, uint32, uint32, bool) {
	for i := range NbFrames {
		for j := range NbTBands {
			goBits := math.Float32bits(g[i][j])
			if c[i][j] != goBits {
				return i, j, c[i][j], goBits, true
			}
		}
	}
	return 0, 0, 0, 0, false
}

//go:noinline
func analysisSpecVariabilityRoundSub(a, b float32) float32 { return a - b }

//go:noinline
func analysisSpecVariabilityRoundMul(a, b float32) float32 { return a * b }

//go:noinline
func analysisSpecVariabilityRoundAdd(a, b float32) float32 { return a + b }

//go:noinline
func analysisSpecVariabilityRoundDiv(a, b float32) float32 { return a / b }

//go:noinline
func analysisSpecVariabilityNativeFMA(a, b, c float32) float32 { return a*b + c }

func validAnalysisSpecVariabilityTraceBytes() []byte {
	data := make([]byte, 4+6*4+2*64+NbFrames*NbTBands*4)
	copy(data[:4], "GVAR")
	fields := []uint32{1, 0, 1, 0, NbFrames, NbTBands}
	pos := 4
	for _, field := range fields {
		binary.LittleEndian.PutUint32(data[pos:pos+4], field)
		pos += 4
	}
	copy(data[pos:pos+64], libopusAnalysisSourceSHA256)
	pos += 64
	copy(data[pos:pos+64], libopusAnalysisInfoSourceSHA256)
	pos += 64
	for i := 0; i < NbFrames*NbTBands; i++ {
		binary.LittleEndian.PutUint32(data[pos:pos+4], math.Float32bits(float32(i+1)))
		pos += 4
	}
	return data
}

func TestParseAnalysisSpecVariabilityTraceRejectsMalformedGVAR(t *testing.T) {
	valid := validAnalysisSpecVariabilityTraceBytes()
	if _, err := parseAnalysisSpecVariabilityTrace(valid); err != nil {
		t.Fatalf("valid GVAR rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"truncated", func(data []byte) []byte { return data[:len(data)-1] }},
		{"trailing", func(data []byte) []byte { return append(data, 0) }},
		{"version", func(data []byte) []byte { binary.LittleEndian.PutUint32(data[4:8], 2); return data }},
		{"frame", func(data []byte) []byte { binary.LittleEndian.PutUint32(data[8:12], 1); return data }},
		{"call count", func(data []byte) []byte { binary.LittleEndian.PutUint32(data[12:16], 2); return data }},
		{"overflow", func(data []byte) []byte { binary.LittleEndian.PutUint32(data[16:20], 1); return data }},
		{"rows", func(data []byte) []byte { binary.LittleEndian.PutUint32(data[20:24], NbFrames-1); return data }},
		{"columns", func(data []byte) []byte { binary.LittleEndian.PutUint32(data[24:28], NbTBands-1); return data }},
		{"analysis hash", func(data []byte) []byte { data[28] ^= 1; return data }},
		{"driver hash", func(data []byte) []byte { data[92] ^= 1; return data }},
		{"nonfinite", func(data []byte) []byte { binary.LittleEndian.PutUint32(data[156:160], 0x7f800000); return data }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := append([]byte(nil), valid...)
			if _, err := parseAnalysisSpecVariabilityTrace(tc.mutate(data)); err == nil {
				t.Fatal("malformed GVAR accepted")
			}
		})
	}
}
