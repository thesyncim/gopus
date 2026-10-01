//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/testsignal"
)

const (
	analysisGetInfoTraceVersion = uint32(1)
	analysisGetInfoTraceFrames  = 20
	analysisGetInfoTraceMetaLen = 16
)

var libopusAnalysisGetInfoTraceHelpers [3]libopustest.HelperCache

func TestAnalysisGetInfoFrameZeroReplayTrace(t *testing.T) {
	libopustest.RequireOracle(t)
	requireCELTTraceV3(t, "analysis getInfo replay trace")

	const (
		fs        = 48000
		channels  = 1
		frameSize = 2880
		lsbDepth  = 24
		frames    = analysisGetInfoTraceFrames
	)
	samples, err := testsignal.GenerateCorpusSignal(
		testsignal.CorpusCleanSpeechV1, fs, frameSize*channels*frames, channels)
	if err != nil {
		t.Fatalf("generate corpus_clean_speech_v1 getInfo fixture: %v", err)
	}
	payload := libopustest.NewOraclePayloadVersion("GANI", 1,
		uint32(fs), uint32(channels), uint32(frameSize), uint32(frames), uint32(lsbDepth),
		0, ^uint32(1), 0, uint32(len(samples)),
	)
	payload.Float32s(samples...)
	input := payload.Bytes()
	pcmBytes := make([]byte, 4*len(samples))
	for i, sample := range samples {
		binary.LittleEndian.PutUint32(pcmBytes[4*i:], math.Float32bits(sample))
	}
	const expectedGANIHash = "dc0f7eca9e53ab774b597dc86a282693ec9d878f227ae3eb0cb042f6569ff2ee"
	inputHash := fmt.Sprintf("%x", sha256.Sum256(input))
	if inputHash != expectedGANIHash {
		t.Fatalf("clean-speech GANI SHA256=%s want %s", inputHash, expectedGANIHash)
	}
	t.Logf("exact corpus input: generator=GenerateCorpusSignal(CorpusCleanSpeechV1) fs=%d channels=%d samples=%d frames=%d frameSize=%d lsbDepth=%d clampToOpusDemoF32InPlace=false float32LE_SHA256=%x GANI_SHA256=%s",
		fs, channels, len(samples), frames, frameSize, lsbDepth, sha256.Sum256(pcmBytes), inputHash)

	baselinePath, err := libopusAnalysisHelper.Path(buildLibopusAnalysisHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "baseline tonality analysis", err)
	}
	baseline, err := libopustest.RunHelper(baselinePath, input)
	if err != nil {
		t.Fatalf("run baseline GANI helper: %v", err)
	}
	baseFrame, err := parseAnalysisGetInfoBaselineFrame(baseline, frames, 0)
	if err != nil {
		t.Fatalf("parse baseline GANO: %v", err)
	}

	tracePath, driverHash := buildLibopusAnalysisGetInfoTraceHelper(t, 0)
	traced, err := libopustest.RunHelper(tracePath, input)
	if err != nil {
		t.Fatalf("run getInfo trace helper: %v", err)
	}
	if len(traced) < len(baseline) || !bytes.Equal(traced[:len(baseline)], baseline) {
		t.Fatal("getInfo driver copy changed the linked 20-frame GANO output")
	}
	trace, err := parseAnalysisGetInfoTrace(traced[len(baseline):])
	if err != nil {
		t.Fatalf("parse GGET: %v", err)
	}
	if trace.driverHash != driverHash {
		t.Fatalf("GGET driver source SHA256=%s want %s", trace.driverHash, driverHash)
	}
	if trace.frame != 0 || trace.frames != frames || trace.fs != uint32(fs) || trace.channels != uint32(channels) ||
		trace.frameSize != uint32(frameSize) || trace.lsbDepth != uint32(lsbDepth) {
		t.Fatalf("GGET fixture metadata frame=%d frames=%d fs=%d channels=%d frame_size=%d lsb=%d",
			trace.frame, trace.frames, trace.fs, trace.channels, trace.frameSize, trace.lsbDepth)
	}
	if trace.meta[12] != 1 || trace.meta[13] != 1 || trace.meta[14] != 1 || trace.meta[15] != 1 {
		t.Fatalf("GGET replay calls/info/state/source-shape flags=%v; all must be 1", trace.meta[12:])
	}
	wantMeta := [10]uint32{0, 0, 3, 0, 0, 0, 3, 0, 3, 3}
	for i, want := range wantMeta {
		if trace.meta[i] != want {
			t.Fatalf("frame-0 C state metadata[%d]=%d want %d", i, trace.meta[i], want)
		}
	}

	// Compare the original C getter replay with a Go replay built from the exact
	// C Info ring and every state field read by tonality_get_info(). This runs
	// before the broader Go RunAnalysis endpoint assertion, so an analyzer-side
	// mismatch cannot hide a getter postprocessing difference.
	cReplayState := analysisGetInfoStateFromTrace(trace)
	goCReplay := cReplayState.tonalityGetInfo(frameSize)
	cReplayDiff := diffAnalysisInfo(analysisInfoToOracle(goCReplay), trace.replayReturn)
	t.Logf("same-state original C replay: pre-read=%d/%d post-read=%d/%d restored=%d/%d replay-final=%d/%d write=%d count=%d diff=%q",
		trace.meta[0], trace.meta[1], trace.meta[2], trace.meta[3], trace.meta[4], trace.meta[5],
		trace.meta[6], trace.meta[7], trace.meta[8], trace.meta[9], cReplayDiff)
	logAnalysisGetInfoWeightedOperands(t, trace, goCReplay)
	if d := diffAnalysisInfo(trace.linkedReturn, trace.replayReturn); d != "" {
		t.Fatalf("original linked C getter replay versus GANO frame 0: %s", d)
	}
	if d := diffAnalysisInfo(trace.linkedReturn, baseFrame.ret); d != "" {
		t.Fatalf("GGET original linked replay versus baseline GANO frame 0: %s", d)
	}
	state := NewTonalityAnalysisState(fs)
	state.SetLSBDepth(lsbDepth)
	initialReadPos, initialReadSubframe := state.ReadPos, state.ReadSubframe
	frame0 := samples[:frameSize*channels]
	goReturned := state.RunAnalysis(frame0, frameSize, channels)
	compareAnalysisGetInfoStateMetadata(t, trace, state, initialReadPos, initialReadSubframe)
	compareAnalysisGetInfoRing(t, trace.info, state.Info)
	if d := analysisStateDiff(state, baseFrame); d != "" {
		t.Fatalf("frame-0 Go analyzer state versus original GANO:%s", d)
	}
	endpointDiff := diffAnalysisInfo(analysisInfoToOracle(goReturned), baseFrame.ret)
	t.Logf("same-input Go RunAnalysis endpoint versus original GANO: C music_prob=%08x Go music_prob=%08x diff=%q",
		baseFrame.ret.musicProb, math.Float32bits(goReturned.MusicProb), endpointDiff)
	// The Go run starts at the same initial cursor captured by the C driver;
	// replay its buffered state to check the getter/RunAnalysis boundary too.
	replayState := *state
	replayState.ReadPos = initialReadPos
	replayState.ReadSubframe = initialReadSubframe
	goReplay := replayState.tonalityGetInfo(frameSize)
	if d := diffAnalysisInfo(analysisInfoToOracle(goReplay), analysisInfoToOracle(goReturned)); d != "" {
		t.Fatalf("source-derived Go getter replay versus original RunAnalysis endpoint: %s", d)
	}
	compareAnalysisGetInfoReplayState(t, &replayState, state)
	t.Logf("frame-0 GANO endpoint: original C and Go state, all 100 Info entries, and restored cursors match")
	if cReplayDiff != "" {
		t.Fatalf("Go tonality_get_info replay from original C snapshot versus linked original C replay: %s", cReplayDiff)
	}
	if endpointDiff != "" {
		t.Fatalf("frame-0 Go RunAnalysis versus original GANO: %s", endpointDiff)
	}
}

func buildLibopusAnalysisGetInfoTraceHelper(t *testing.T, captureFrame uint32) (string, string) {
	t.Helper()
	if captureFrame != 0 && captureFrame != 2 {
		t.Fatalf("unsupported fixed getInfo capture frame %d", captureFrame)
	}
	path, err := libopusAnalysisGetInfoTraceHelpers[captureFrame].Path(func() (string, error) {
		root := celtQuantTraceRepoRoot(t)
		pinnedAnalysis, err := os.ReadFile(libopustest.RefPath("src", "analysis.c"))
		if err != nil {
			return "", fmt.Errorf("read selected pinned analysis.c: %w", err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(pinnedAnalysis)); got != libopusAnalysisSourceSHA256 {
			return "", fmt.Errorf("selected pinned analysis.c SHA256=%s want %s", got, libopusAnalysisSourceSHA256)
		}
		pinnedDriver, err := os.ReadFile(filepath.Join(root, "tools", "csrc", "libopus_analysis_info.c"))
		if err != nil {
			return "", fmt.Errorf("read pinned GANI driver source: %w", err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(pinnedDriver)); got != libopusAnalysisInfoSourceSHA256 {
			return "", fmt.Errorf("pinned GANI driver SHA256=%s want %s", got, libopusAnalysisInfoSourceSHA256)
		}
		instrumented, err := instrumentAnalysisGetInfoDriver(string(pinnedDriver))
		if err != nil {
			return "", err
		}
		driverHash := analysisGetInfoDriverHash(instrumented, captureFrame)
		sourcePath := filepath.Join(t.TempDir(), "libopus_analysis_get_info_trace.c")
		if err := os.WriteFile(sourcePath, []byte(instrumented), 0o600); err != nil {
			return "", fmt.Errorf("write source-bound GANI driver copy: %w", err)
		}
		mapDir := t.TempDir()
		if proofDir := os.Getenv("GOPUS_GETINFO_TRACE_PROOF_DIR"); proofDir != "" {
			mapDir = proofDir
			if err := os.MkdirAll(mapDir, 0o700); err != nil {
				return "", fmt.Errorf("create persistent getInfo proof directory: %w", err)
			}
		}
		mapPath := filepath.Join(mapDir, fmt.Sprintf("libopus_analysis_get_info_trace_frame_%d.map", captureFrame))
		driverEvidencePath := filepath.Join(mapDir, fmt.Sprintf("libopus_analysis_get_info_trace_frame_%d.c", captureFrame))
		if err := os.WriteFile(driverEvidencePath, []byte(instrumented), 0o644); err != nil {
			return "", fmt.Errorf("save exact instrumented GANI driver: %w", err)
		}
		t.Logf("original-C trace driver frame=%d SHA256=%s source=%s link-map=%s",
			captureFrame, driverHash, driverEvidencePath, mapPath)
		config := libopustest.CHelperConfig{
			Label:      fmt.Sprintf("libopus original getInfo replay trace frame %d", captureFrame),
			OutputBase: fmt.Sprintf("gopus_libopus_analysis_get_info_trace_frame_%d", captureFrame),
			SourceFile: sourcePath,
			CFlags: []string{
				"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG",
				fmt.Sprintf("-DGOPUS_ANALYSIS_GET_INFO_CAPTURE_FRAME=%d", captureFrame),
				fmt.Sprintf("-DGOPUS_ANALYSIS_GET_INFO_DRIVER_SHA256=%q", driverHash),
			},
			RefIncludes: []string{"include", "src", "celt", "silk", "silk/float"},
			Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
			LDFlags:     []string{"-Wl,-Map=" + mapPath},
			DeadStrip:   true,
		}
		helperPath, err := libopustest.BuildCHelper(config)
		if err != nil {
			return "", err
		}
		if err := requireAnalysisGetInfoOriginalLinkMap(mapPath); err != nil {
			return "", err
		}
		return helperPath, nil
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "libopus original getInfo replay trace", err)
	}
	return path, analysisGetInfoGeneratedDriverHash(t, captureFrame)
}

func analysisGetInfoGeneratedDriverHash(t *testing.T, captureFrame uint32) string {
	t.Helper()
	root := celtQuantTraceRepoRoot(t)
	pinnedDriver, err := os.ReadFile(filepath.Join(root, "tools", "csrc", "libopus_analysis_info.c"))
	if err != nil {
		t.Fatalf("read pinned GANI driver source for trace hash: %v", err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(pinnedDriver)); got != libopusAnalysisInfoSourceSHA256 {
		t.Fatalf("pinned GANI driver SHA256=%s want %s", got, libopusAnalysisInfoSourceSHA256)
	}
	instrumented, err := instrumentAnalysisGetInfoDriver(string(pinnedDriver))
	if err != nil {
		t.Fatalf("instrument pinned GANI driver: %v", err)
	}
	return analysisGetInfoDriverHash(instrumented, captureFrame)
}

func analysisGetInfoDriverHash(instrumented string, captureFrame uint32) string {
	keyedSource := fmt.Sprintf("%s\nGOPUS_ANALYSIS_GET_INFO_CAPTURE_FRAME=%d\n", instrumented, captureFrame)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(keyedSource)))
}

func instrumentAnalysisGetInfoDriver(source string) (string, error) {
	var err error
	source, err = replaceAnalysisGetInfoAnchor(source, "#include \"modes.h\"\n",
		"#include \"modes.h\"\n#include <math.h>\n", "math include")
	if err != nil {
		return "", err
	}
	const helperAnchor = "int main(void) {\n"
	const helperText = `
#ifndef GOPUS_ANALYSIS_GET_INFO_DRIVER_SHA256
#error GOPUS_ANALYSIS_GET_INFO_DRIVER_SHA256 is required
#endif
#ifndef GOPUS_ANALYSIS_GET_INFO_CAPTURE_FRAME
#error GOPUS_ANALYSIS_GET_INFO_CAPTURE_FRAME is required
#endif

static int gopus_put_getinfo_trace(const TonalityAnalysisState *frame0,
                                   const AnalysisInfo *original_return,
                                   int32_t initial_read_pos,
                                   int32_t initial_read_subframe,
                                   uint32_t fs, uint32_t channels,
                                   uint32_t frame_size, uint32_t lsb_depth,
                                   uint32_t num_frames,
                                   uint32_t capture_frame) {
  TonalityAnalysisState replay;
  AnalysisInfo replay_return;
  uint32_t original_read_pos = (uint32_t)frame0->read_pos;
  uint32_t original_read_subframe = (uint32_t)frame0->read_subframe;
  uint32_t restored_read_pos = (uint32_t)initial_read_pos;
  uint32_t restored_read_subframe = (uint32_t)initial_read_subframe;
  uint32_t replay_calls = 1;
  uint32_t replay_info_matches;
  uint32_t replay_state_matches;
  uint32_t source_shape_valid;
  uint32_t info_count = DETECT_SIZE;
  uint32_t metadata_count = 16;
  uint32_t metadata[16];
  uint32_t i;
  uint32_t supported_fixture =
      (fs == 48000 && channels == 1 && frame_size == 2880 && lsb_depth == 24 && num_frames == 20) ||
      (fs == 16000 && channels == 2 && frame_size == 320 && lsb_depth == 24 && num_frames == 60);

  memcpy(&replay, frame0, sizeof(replay));
  replay.read_pos = initial_read_pos;
  replay.read_subframe = initial_read_subframe;
  memset(&replay_return, 0, sizeof(replay_return));
  tonality_get_info(&replay, &replay_return, (int)frame_size);
  replay_info_matches = memcmp(&replay_return, original_return, sizeof(replay_return)) == 0;
  replay_state_matches = memcmp(&replay, frame0, sizeof(replay)) == 0;

  source_shape_valid = supported_fixture && capture_frame < num_frames &&
      initial_read_pos >= 0 && initial_read_pos < DETECT_SIZE &&
      initial_read_subframe >= 0 && initial_read_subframe < 8 &&
      original_read_pos < DETECT_SIZE && original_read_subframe < 8 &&
      frame0->write_pos >= 0 && frame0->write_pos < DETECT_SIZE && frame0->count > 0 &&
      replay_return.valid == original_return->valid && original_return->valid;

  metadata[0] = (uint32_t)initial_read_pos;
  metadata[1] = (uint32_t)initial_read_subframe;
  metadata[2] = original_read_pos;
  metadata[3] = original_read_subframe;
  metadata[4] = restored_read_pos;
  metadata[5] = restored_read_subframe;
  metadata[6] = (uint32_t)replay.read_pos;
  metadata[7] = (uint32_t)replay.read_subframe;
  metadata[8] = (uint32_t)frame0->write_pos;
  metadata[9] = (uint32_t)frame0->count;
  metadata[10] = (uint32_t)frame0->analysis_offset;
  metadata[11] = (uint32_t)frame0->E_count;
  metadata[12] = replay_calls;
  metadata[13] = replay_info_matches;
  metadata[14] = replay_state_matches;
  metadata[15] = source_shape_valid;
  if (!write_exact("GGET", 4) || !put_u32(1) || !put_u32(capture_frame) ||
      !put_u32(num_frames) || !put_u32(fs) || !put_u32(channels) ||
      !put_u32(frame_size) || !put_u32(lsb_depth) || !put_u32(64) ||
      !write_exact(GOPUS_ANALYSIS_GET_INFO_DRIVER_SHA256, 64) ||
      !put_u32(metadata_count)) return 0;
  for (i = 0; i < metadata_count; i++) if (!put_u32(metadata[i])) return 0;
  if (!put_info(original_return) || !put_info(&replay_return) ||
      !put_u32(info_count)) return 0;
  for (i = 0; i < info_count; i++) if (!put_info(&frame0->info[i])) return 0;
  return 1;
}

int main(void) {
`
	source, err = replaceAnalysisGetInfoAnchor(source, helperAnchor, cleanAnalysisGetInfoPatch(helperText), "source-shaped replay helper")
	if err != nil {
		return "", err
	}
	const declarationAnchor = "  uint32_t f;\n"
	const declarationReplacement = `  uint32_t f;
  TonalityAnalysisState *gopus_capture_snapshot = NULL;
  AnalysisInfo gopus_capture_return;
  int32_t gopus_capture_initial_read_pos = -1;
  int32_t gopus_capture_initial_read_subframe = -1;
`
	source, err = replaceAnalysisGetInfoAnchor(source, declarationAnchor, cleanAnalysisGetInfoPatch(declarationReplacement), "frame-zero replay state declarations")
	if err != nil {
		return "", err
	}
	const allocAnchor = `  st = (TonalityAnalysisState *)calloc(1, sizeof(*st));
  if (mode == NULL || st == NULL) return 4;
  tonality_analysis_init(st, (opus_int32)fs);`
	const allocReplacement = `  st = (TonalityAnalysisState *)calloc(1, sizeof(*st));
  gopus_capture_snapshot = (TonalityAnalysisState *)malloc(sizeof(*gopus_capture_snapshot));
  if (mode == NULL || st == NULL || gopus_capture_snapshot == NULL) return 4;
  tonality_analysis_init(st, (opus_int32)fs);`
	source, err = replaceAnalysisGetInfoAnchor(source, allocAnchor, cleanAnalysisGetInfoPatch(allocReplacement), "frame-zero replay snapshot allocation")
	if err != nil {
		return "", err
	}
	const runAnchor = `    memset(&info, 0, sizeof(info));
    run_analysis(st, mode, frame, (int)frame_size, (int)frame_size, c1, c2, (int)channels,
                 (opus_int32)fs, (int)lsb_depth, downmix, &info);`
	const runReplacement = `    memset(&info, 0, sizeof(info));
    if (f == GOPUS_ANALYSIS_GET_INFO_CAPTURE_FRAME) {
      gopus_capture_initial_read_pos = st->read_pos;
      gopus_capture_initial_read_subframe = st->read_subframe;
    }
    run_analysis(st, mode, frame, (int)frame_size, (int)frame_size, c1, c2, (int)channels,
                 (opus_int32)fs, (int)lsb_depth, downmix, &info);
    if (f == GOPUS_ANALYSIS_GET_INFO_CAPTURE_FRAME) {
      memcpy(gopus_capture_snapshot, st, sizeof(*st));
      gopus_capture_return = info;
    }`
	source, err = replaceAnalysisGetInfoAnchor(source, runAnchor, cleanAnalysisGetInfoPatch(runReplacement), "original frame-zero run_analysis snapshot")
	if err != nil {
		return "", err
	}
	const endAnchor = `  }
  fflush(stdout);
  free(pcm);
  free(st);`
	const endReplacement = `  }
  if (gopus_capture_snapshot == NULL || gopus_capture_initial_read_pos < 0 ||
      !gopus_put_getinfo_trace(gopus_capture_snapshot, &gopus_capture_return,
          gopus_capture_initial_read_pos, gopus_capture_initial_read_subframe,
          fs, channels, frame_size, lsb_depth, num_frames,
          GOPUS_ANALYSIS_GET_INFO_CAPTURE_FRAME)) return 5;
  fflush(stdout);
  free(pcm);
  free(st);
  free(gopus_capture_snapshot);`
	source, err = replaceAnalysisGetInfoAnchor(source, endAnchor, cleanAnalysisGetInfoPatch(endReplacement), "GGET trailer and driver cleanup")
	if err != nil {
		return "", err
	}
	return source, nil
}

func replaceAnalysisGetInfoAnchor(source, anchor, replacement, label string) (string, error) {
	if count := strings.Count(source, anchor); count != 1 {
		return "", fmt.Errorf("GANI getInfo trace anchor %q occurs %d times, want exactly one", label, count)
	}
	return strings.Replace(source, anchor, replacement, 1), nil
}

func cleanAnalysisGetInfoPatch(source string) string {
	lines := strings.Split(strings.TrimPrefix(source, "\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimPrefix(lines[i], "+")
	}
	return strings.Join(lines, "\n")
}

func requireAnalysisGetInfoOriginalLinkMap(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read getInfo helper link map: %w", err)
	}
	mapText := string(data)
	if !strings.Contains(mapText, "libopus.a(analysis.o)") {
		return fmt.Errorf("getInfo helper link map does not bind original libopus analysis.o")
	}
	if strings.Contains(mapText, "analysis_get_info_trace.c.o") || strings.Contains(mapText, "analysis_gget.c.o") {
		return fmt.Errorf("getInfo helper link map contains a copied analysis implementation object")
	}
	return nil
}

func analysisGetInfoStateFromTrace(trace analysisGetInfoTrace) *TonalityAnalysisState {
	state := NewTonalityAnalysisState(int(trace.fs))
	state.SetLSBDepth(int(trace.lsbDepth))
	state.ReadPos = int32(trace.meta[4])
	state.ReadSubframe = int32(trace.meta[5])
	state.WritePos = int32(trace.meta[8])
	state.Count = int32(trace.meta[9])
	state.AnalysisOffset = int32(trace.meta[10])
	state.ECount = int32(trace.meta[11])
	for i := range trace.info {
		state.Info[i] = analysisGetInfoAnalysisInfo(trace.info[i])
	}
	return state
}

func analysisGetInfoAnalysisInfo(in analysisOracleInfo) AnalysisInfo {
	return AnalysisInfo{
		Valid:          in.valid != 0,
		Tonality:       math.Float32frombits(in.tonality),
		TonalitySlope:  math.Float32frombits(in.tonalitySlope),
		NoisySpeech:    math.Float32frombits(in.noisiness),
		Activity:       math.Float32frombits(in.activity),
		MusicProb:      math.Float32frombits(in.musicProb),
		MusicProbMin:   math.Float32frombits(in.musicMin),
		MusicProbMax:   math.Float32frombits(in.musicMax),
		VADProb:        math.Float32frombits(in.activityProb),
		BandwidthIndex: int32(in.bandwidth),
		Bandwidth:      bandwidthTypeFromIndex(int(in.bandwidth)),
		MaxPitchRatio:  math.Float32frombits(in.maxPitchRatio),
		LeakBoost:      in.leakBoost,
	}
}

func logAnalysisGetInfoWeightedOperands(t *testing.T, trace analysisGetInfoTrace, goReturned AnalysisInfo) {
	t.Helper()
	start := int(trace.meta[0])
	write := int(trace.meta[8])
	lookahead := write - start
	if lookahead < 0 {
		lookahead += DetectSize
	}
	if trace.frameSize > trace.fs/50 && start != write {
		start++
		if start == DetectSize {
			start = 0
		}
	}
	if start == write {
		start--
	}
	if start < 0 {
		start = DetectSize - 1
	}
	mpos, vpos := start, start
	if lookahead > 15 {
		mpos += 5
		if mpos >= DetectSize {
			mpos -= DetectSize
		}
		vpos++
		if vpos >= DetectSize {
			vpos -= DetectSize
		}
	}
	weight := func(v uint32) float32 { return maxf(0.1, math.Float32frombits(v)) }
	w0 := weight(trace.info[vpos].activityProb)
	p0 := math.Float32frombits(trace.info[mpos].musicProb)
	count := w0
	avgSeparate := analysisGetInfoTraceMul32(w0, p0)
	avgFused := avgSeparate
	terms := 1
	t.Logf("captured-operand model seed: mpos=%d vpos=%d weight=%08x Info[%d].music_prob=%08x count=%08x avg=%08x",
		mpos, vpos, math.Float32bits(w0), mpos, math.Float32bits(p0), math.Float32bits(count), math.Float32bits(avgSeparate))
	for {
		mpos++
		if mpos == DetectSize {
			mpos = 0
		}
		if mpos == write {
			break
		}
		vpos++
		if vpos == DetectSize {
			vpos = 0
		}
		if vpos == write {
			break
		}
		posWeight := weight(trace.info[vpos].activityProb)
		musicProb := math.Float32frombits(trace.info[mpos].musicProb)
		product := analysisGetInfoTraceMul32(posWeight, musicProb)
		avgSeparate = analysisGetInfoTraceAdd32(avgSeparate, product)
		avgFused = opusmath.FMA32(posWeight, musicProb, avgFused)
		count = analysisGetInfoTraceAdd32(count, posWeight)
		terms++
		t.Logf("captured-operand model term %d: mpos=%d vpos=%d weight=%08x music_prob=%08x product32=%08x separate_sum=%08x fma_sum=%08x count=%08x",
			terms-1, mpos, vpos, math.Float32bits(posWeight), math.Float32bits(musicProb),
			math.Float32bits(product), math.Float32bits(avgSeparate), math.Float32bits(avgFused), math.Float32bits(count))
	}
	separateResult := avgSeparate / count
	fusedResult := avgFused / count
	t.Logf("captured-operand model results (actual linked C and Go getter outputs follow): terms=%d count=%08x separate-result=%08x fma-result=%08x C-return=%08x Go-return=%08x",
		terms, math.Float32bits(count), math.Float32bits(separateResult), math.Float32bits(fusedResult),
		trace.replayReturn.musicProb, math.Float32bits(goReturned.MusicProb))
}

//go:noinline
func analysisGetInfoTraceMul32(a, b float32) float32 { return a * b }

//go:noinline
func analysisGetInfoTraceAdd32(a, b float32) float32 { return a + b }

func compareAnalysisGetInfoStateMetadata(t *testing.T, trace analysisGetInfoTrace, state *TonalityAnalysisState,
	initialReadPos, initialReadSubframe int32) {
	t.Helper()
	meta := trace.meta
	want := [12]uint32{
		uint32(initialReadPos), uint32(initialReadSubframe),
		uint32(state.ReadPos), uint32(state.ReadSubframe),
		uint32(initialReadPos), uint32(initialReadSubframe),
		uint32(state.ReadPos), uint32(state.ReadSubframe),
		uint32(state.WritePos), uint32(state.Count), uint32(state.AnalysisOffset), uint32(state.ECount),
	}
	for i, value := range want {
		if meta[i] != value {
			t.Fatalf("GGET state metadata[%d]=%d want %d", i, meta[i], value)
		}
	}
	if trace.fs != uint32(state.Fs) || trace.lsbDepth != uint32(state.LSBDepth) {
		t.Fatalf("GGET state Fs/LSB=%d/%d Go=%d/%d", trace.fs, trace.lsbDepth, state.Fs, state.LSBDepth)
	}
}

func compareAnalysisGetInfoRing(t *testing.T, cRing []analysisOracleInfo, goRing [DetectSize]AnalysisInfo) {
	t.Helper()
	if len(cRing) != DetectSize {
		t.Fatalf("C Info ring entries=%d want %d", len(cRing), DetectSize)
	}
	for i := range cRing {
		goInfo := analysisInfoToOracle(goRing[i])
		if diff := diffAnalysisInfo(goInfo, cRing[i]); diff != "" {
			t.Fatalf("Info[%d] C/Go full C-layout mismatch: %s", i, diff)
		}
	}
}

func compareAnalysisGetInfoReplayState(t *testing.T, replay, original *TonalityAnalysisState) {
	t.Helper()
	if analysisStateScalars(replay) != analysisStateScalars(original) ||
		analysisStateHashes(replay) != analysisStateHashes(original) ||
		replay.Info != original.Info || replay.DownmixState != original.DownmixState ||
		replay.InMem != original.InMem || replay.PrevBandTonality != original.PrevBandTonality ||
		replay.SqrtE != original.SqrtE || replay.Fs != original.Fs || replay.LSBDepth != original.LSBDepth ||
		replay.Initialized != original.Initialized || replay.fixed != original.fixed {
		t.Fatal("source-derived Go getInfo replay changed analyzer state outside its restored cursor transition")
	}
}

func parseAnalysisGetInfoBaselineFrame(data []byte, frames, frame int) (analysisOracleFrame, error) {
	const infoBytes = 12*4 + 20
	const recordBytes = 2*infoBytes + 12*4 + 12*4
	if frame < 0 || frame >= frames || len(data) != 12+frames*recordBytes || len(data) < 12 || string(data[:4]) != "GANO" ||
		readAnalysisGetInfoU32(data, 4) != 1 || readAnalysisGetInfoU32(data, 8) != uint32(frames) {
		return analysisOracleFrame{}, fmt.Errorf("invalid GANO header/length")
	}
	rec := data[12+frame*recordBytes:]
	out := analysisOracleFrame{
		ret:    readAnalysisGetInfoInfo(rec[:infoBytes]),
		latest: readAnalysisGetInfoInfo(rec[infoBytes : 2*infoBytes]),
	}
	for i := range 12 {
		out.scalars[i] = readAnalysisGetInfoU32(rec, 2*infoBytes+4*i)
		out.hashes[i] = readAnalysisGetInfoU32(rec, 2*infoBytes+48+4*i)
	}
	return out, nil
}

func readAnalysisGetInfoInfo(data []byte) analysisOracleInfo {
	return analysisOracleInfo{
		valid: readAnalysisGetInfoU32(data, 0), tonality: readAnalysisGetInfoU32(data, 4),
		tonalitySlope: readAnalysisGetInfoU32(data, 8), noisiness: readAnalysisGetInfoU32(data, 12),
		activity: readAnalysisGetInfoU32(data, 16), musicProb: readAnalysisGetInfoU32(data, 20),
		musicMin: readAnalysisGetInfoU32(data, 24), musicMax: readAnalysisGetInfoU32(data, 28),
		bandwidth: readAnalysisGetInfoU32(data, 32), activityProb: readAnalysisGetInfoU32(data, 36),
		maxPitchRatio: readAnalysisGetInfoU32(data, 40),
		leakBoost: [19]uint8{
			data[48], data[49], data[50], data[51], data[52], data[53], data[54], data[55], data[56],
			data[57], data[58], data[59], data[60], data[61], data[62], data[63], data[64], data[65], data[66],
		},
	}
}

func readAnalysisGetInfoU32(data []byte, offset int) uint32 {
	return binary.LittleEndian.Uint32(data[offset : offset+4])
}

func analysisGetInfoHashBytes(hash string) []byte {
	decoded, _ := hex.DecodeString(hash)
	return decoded
}
