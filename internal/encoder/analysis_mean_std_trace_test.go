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
	"github.com/thesyncim/gopus/internal/testsignal"
)

const (
	libopusAnalysisMeanStdInfoSourceSHA256 = "68a03d38b330425339597d56b992022d0f21c2b12799aa1853ee4ae93376cd6f"
	analysisMeanStdTraceVersion            = uint32(2)
)

var libopusAnalysisMeanStdTraceHelper libopustest.HelperCache

type analysisMeanStdOracleRow struct {
	frame, chunk, count, flags uint32
	alpha                      uint32
	cmeanOld                   [4]uint32
	bfcc                       [4]uint32
	mem0                       [4]uint32
	mem8                       [4]uint32
	mem16                      [4]uint32
	mem24                      [4]uint32
	feature                    [9]uint32
	cmeanNew                   [4]uint32
	stdOld                     [9]uint32
	stdNew                     [9]uint32
}

type analysisMeanStdOracleTrace struct {
	rows       [6]analysisMeanStdOracleRow
	rowCount   uint32
	overflow   uint32
	sourceHash string
}

func TestAnalysisMeanStdLiveTrace(t *testing.T) {
	libopustest.RequireOracle(t)
	requireCELTTraceV3(t, "analysis CMean/Std trace")
	if !analysisMeanStdTraceEnabled {
		t.Fatal("analysis CMean/Std trace hook is disabled in this build")
	}

	const (
		fs        = 48000
		channels  = 1
		frameMS   = 60
		frameSize = fs * frameMS / 1000
		frames    = 1000 / frameMS
		lsbDepth  = 24
	)
	samples, err := testsignal.GenerateEncoderSignalVariant(
		testsignal.EncoderVariantImpulseTrainV1,
		fs,
		frames*frameSize*channels,
		channels,
	)
	if err != nil {
		t.Fatalf("generate impulse_train_v1: %v", err)
	}
	clampToOpusDemoF32InPlace(samples)
	input := analysisMLPGANIInput(fs, channels, frameSize, frames, lsbDepth, samples)

	baselinePath, err := libopusAnalysisHelper.Path(buildLibopusAnalysisHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "baseline analysis GANO", err)
	}
	baseline, err := libopustest.RunHelper(baselinePath, input)
	if err != nil {
		t.Fatalf("run baseline analysis helper: %v", err)
	}
	if err := validateAnalysisGANO(baseline, frames); err != nil {
		t.Fatalf("baseline GANO: %v", err)
	}

	tracePath, sourceHash := buildLibopusAnalysisMeanStdTraceHelper(t)
	traced, err := libopustest.RunHelper(tracePath, input)
	if err != nil {
		libopustest.HelperUnavailable(t, "analysis CMean/Std trace", err)
	}
	if len(traced) < len(baseline) {
		t.Fatalf("instrumented output bytes=%d shorter than GANO prefix=%d", len(traced), len(baseline))
	}
	if !bytes.Equal(traced[:len(baseline)], baseline) {
		t.Fatal("source-instrumented analysis changed the complete GANO output")
	}
	cTrace, err := parseAnalysisMeanStdTrace(traced[len(baseline):])
	if err != nil {
		t.Fatalf("parse GMSD: %v", err)
	}
	if cTrace.sourceHash != sourceHash {
		t.Fatalf("instrumented analysis.c hash=%s want %s", cTrace.sourceHash, sourceHash)
	}
	if cTrace.rowCount != 6 || cTrace.overflow != 0 {
		t.Fatalf("GMSD rows=%d overflow=%08x; want six rows and no overflow", cTrace.rowCount, cTrace.overflow)
	}
	wantContexts := [6][3]uint32{
		{0, 0, 0}, {0, 1, 1}, {0, 2, 2},
		{1, 0, 3}, {1, 1, 4}, {1, 2, 5},
	}
	wantFlags := [6]uint32{5, 5, 5, 5, 5, 7}
	for i := range cTrace.rows {
		row := cTrace.rows[i]
		if got := [3]uint32{row.frame, row.chunk, row.count}; got != wantContexts[i] {
			t.Fatalf("C trace row %d context=%v want frame/chunk/pre-count %v", i, got, wantContexts[i])
		}
		if row.flags != wantFlags[i] {
			t.Fatalf("C trace row %d flags=%08x want %08x", i, row.flags, wantFlags[i])
		}
	}

	plain := NewTonalityAnalysisState(fs)
	plain.SetLSBDepth(lsbDepth)
	tracedState := NewTonalityAnalysisState(fs)
	tracedState.SetLSBDepth(lsbDepth)
	var goRows [6]analysisMeanStdTraceSnapshot
	var goCalls int
	var goOverflow bool
	oldHook := analysisMeanStdTraceHook
	t.Cleanup(func() { analysisMeanStdTraceHook = oldHook })
	for frame := 0; frame < frames; frame++ {
		start := frame * frameSize * channels
		framePCM := samples[start : start+frameSize*channels]
		analysisMeanStdTraceHook = nil
		plainInfo := plain.RunAnalysis(framePCM, frameSize, channels)
		analysisMeanStdTraceHook = func(snapshot analysisMeanStdTraceSnapshot) {
			if goCalls >= len(goRows) {
				goOverflow = true
				return
			}
			goRows[goCalls] = snapshot
			goCalls++
		}
		analysisMeanStdTraceSetFrame(int32(frame))
		tracedInfo := tracedState.RunAnalysis(framePCM, frameSize, channels)
		compareGoAnalysisFrame(t, frame, tracedInfo, plainInfo, tracedState, plain)
		if tracedState.Info != plain.Info {
			t.Fatalf("Go CMean/Std hook changed Info ring at frame %d", frame)
		}
	}
	analysisMeanStdTraceHook = nil
	if goOverflow || goCalls != len(goRows) {
		t.Fatalf("Go CMean/Std snapshot calls=%d overflow=%t; want six rows", goCalls, goOverflow)
	}
	wantGoContexts := [6][3]int32{
		{0, 0, 0}, {0, 1, 1}, {0, 2, 2},
		{1, 0, 3}, {1, 1, 4}, {1, 2, 5},
	}
	for i, goRow := range goRows {
		if got := [3]int32{goRow.Frame, goRow.Chunk, goRow.Count}; got != wantGoContexts[i] {
			t.Fatalf("Go trace row %d context=%v want frame/chunk/pre-count %v", i, got, wantGoContexts[i])
		}
		if cTrace.rows[i].alpha != math.Float32bits(goRow.Alpha) {
			t.Fatalf("row %d alpha Go=%08x C=%08x", i, math.Float32bits(goRow.Alpha), cTrace.rows[i].alpha)
		}
		compareAnalysisMeanStdBits(t, i, "CMean old", cTrace.rows[i].cmeanOld[:], goRow.CMeanOld[:])
		compareAnalysisMeanStdBits(t, i, "BFCC", cTrace.rows[i].bfcc[:], goRow.BFCC[:])
		compareAnalysisMeanStdBits(t, i, "CMean new", cTrace.rows[i].cmeanNew[:], goRow.CMeanNew[:])
		if goRow.StdActive != (goRow.Count == 5) {
			t.Fatalf("Go trace row %d std-active=%t for pre-count=%d", i, goRow.StdActive, goRow.Count)
		}
		if goRow.StdActive {
			compareAnalysisMeanStdBits(t, i, "feature Mem[0:4]", cTrace.rows[i].mem0[:], goRow.Mem0[:])
			compareAnalysisMeanStdBits(t, i, "feature Mem[8:12]", cTrace.rows[i].mem8[:], goRow.Mem8[:])
			compareAnalysisMeanStdBits(t, i, "feature Mem[16:20]", cTrace.rows[i].mem16[:], goRow.Mem16[:])
			compareAnalysisMeanStdBits(t, i, "feature Mem[24:28]", cTrace.rows[i].mem24[:], goRow.Mem24[:])
			compareAnalysisMeanStdBits(t, i, "Std old", cTrace.rows[i].stdOld[:], goRow.StdOld[:])
			compareAnalysisMeanStdBits(t, i, "Std feature", cTrace.rows[i].feature[:], goRow.Feature[:9])
			compareAnalysisMeanStdBits(t, i, "Std new", cTrace.rows[i].stdNew[:], goRow.StdNew[:])
		}
		logAnalysisMeanStdDerivedProducts(t, i, goRow)
	}
}

func buildLibopusAnalysisMeanStdTraceHelper(t *testing.T) (string, string) {
	t.Helper()
	path, err := libopusAnalysisMeanStdTraceHelper.Path(func() (string, error) {
		root := celtQuantTraceRepoRoot(t)
		pinnedPath := libopustest.RefPath("src", "analysis.c")
		pinned, err := os.ReadFile(pinnedPath)
		if err != nil {
			return "", fmt.Errorf("read selected analysis.c: %w", err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(pinned)); got != libopusAnalysisSourceSHA256 {
			return "", fmt.Errorf("selected analysis.c SHA256=%s want pinned %s", got, libopusAnalysisSourceSHA256)
		}
		instrumented, err := instrumentAnalysisMeanStdSource(string(pinned))
		if err != nil {
			return "", err
		}
		traceHash := fmt.Sprintf("%x", sha256.Sum256([]byte(instrumented)))
		copyDir := t.TempDir()
		analysisCopy := filepath.Join(copyDir, "analysis_gmsd.c")
		if err := os.WriteFile(analysisCopy, []byte(instrumented), 0o600); err != nil {
			return "", fmt.Errorf("write instrumented analysis.c: %w", err)
		}
		csrc := filepath.Join(root, "tools", "csrc")
		infoSource, err := os.ReadFile(filepath.Join(csrc, "libopus_analysis_info.c"))
		if err != nil {
			return "", fmt.Errorf("read GANI driver source: %w", err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(infoSource)); got != libopusAnalysisMeanStdInfoSourceSHA256 {
			return "", fmt.Errorf("GANI driver SHA256=%s want %s", got, libopusAnalysisMeanStdInfoSourceSHA256)
		}
		config := libopustest.CHelperConfig{
			Label:      "libopus analysis CMean/Std trace",
			OutputBase: "gopus_libopus_analysis_mean_std_trace",
			SourceFile: "libopus_analysis_mean_std_trace_main.c",
			CFlags: []string{
				"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG",
				fmt.Sprintf("-DGOPUS_ANALYSIS_MEAN_STD_SOURCE_SHA256=%q", traceHash),
			},
			RefIncludes: []string{"include", "src", "celt", "silk", "silk/float"},
			IncludeDirs: []string{csrc},
			Sources: []string{
				filepath.Join(csrc, "libopus_analysis_mean_std_trace.c"),
				analysisCopy,
			},
			Libs:      []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
			DeadStrip: true,
		}
		return libopustest.BuildCHelper(config)
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "libopus analysis CMean/Std trace", err)
	}
	pinned, err := os.ReadFile(libopustest.RefPath("src", "analysis.c"))
	if err != nil {
		t.Fatalf("read selected analysis.c for trace hash: %v", err)
	}
	instrumented, err := instrumentAnalysisMeanStdSource(string(pinned))
	if err != nil {
		t.Fatalf("instrument selected analysis.c for trace hash: %v", err)
	}
	return path, fmt.Sprintf("%x", sha256.Sum256([]byte(instrumented)))
}

func instrumentAnalysisMeanStdSource(source string) (string, error) {
	const includeAnchor = "#include \"analysis.h\"\n"
	var err error
	source, err = replaceMeanStdTraceAnchor(source, includeAnchor,
		includeAnchor+"#include \"libopus_analysis_mean_std_trace.h\"\n", "trace header")
	if err != nil {
		return "", err
	}
	const runEntryAnchor = `   int offset;
   int pcm_len;

   analysis_frame_size -= analysis_frame_size&1;`
	const runEntryReplacement = `   int offset;
   int pcm_len;
   int gopus_mean_std_chunk = 0;

   gopus_analysis_mean_std_run_begin();
   analysis_frame_size -= analysis_frame_size&1;`
	source, err = replaceMeanStdTraceAnchor(source, runEntryAnchor, runEntryReplacement, "run_analysis entry")
	if err != nil {
		return "", err
	}
	const chunkAnchor = `      while (pcm_len>0) {
         tonality_analysis(analysis, celt_mode, analysis_pcm, IMIN(Fs/50, pcm_len), offset, c1, c2, C, lsb_depth, downmix);`
	const chunkReplacement = `      while (pcm_len>0) {
         gopus_analysis_mean_std_chunk_begin(analysis->count, gopus_mean_std_chunk);
         tonality_analysis(analysis, celt_mode, analysis_pcm, IMIN(Fs/50, pcm_len), offset, c1, c2, C, lsb_depth, downmix);
         gopus_mean_std_chunk++;`
	source, err = replaceMeanStdTraceAnchor(source, chunkAnchor, chunkReplacement, "analysis chunk")
	if err != nil {
		return "", err
	}
	const meanAnchor = `    for (i=0;i<4;i++)
       tonal->cmean[i] = (1-alpha)*tonal->cmean[i] + alpha*BFCC[i];`
	const meanReplacement = `    if (tonal->count >= 1 && tonal->count <= 6)
       gopus_analysis_mean_std_begin(tonal->count-1, alpha, tonal->cmean, BFCC);
    for (i=0;i<4;i++)
       tonal->cmean[i] = (1-alpha)*tonal->cmean[i] + alpha*BFCC[i];`
	source, err = replaceMeanStdTraceAnchor(source, meanAnchor, meanReplacement, "CMean update")
	if err != nil {
		return "", err
	}
	const stdAnchor = `    if (tonal->count > 5)
    {
       for (i=0;i<9;i++)
          tonal->std[i] = (1-alpha)*tonal->std[i] + alpha*features[i]*features[i];
    }`
	const stdReplacement = `    if (tonal->count > 5)
    {
       if (tonal->count == 6)
          gopus_analysis_mean_std_before_std(tonal->std, features, tonal->mem);
       for (i=0;i<9;i++)
          tonal->std[i] = (1-alpha)*tonal->std[i] + alpha*features[i]*features[i];
    }
	    if (tonal->count >= 1 && tonal->count <= 6)
       gopus_analysis_mean_std_finish(tonal->cmean, tonal->std);`
	source, err = replaceMeanStdTraceAnchor(source, stdAnchor, stdReplacement, "Std update")
	if err != nil {
		return "", err
	}
	return source, nil
}

func replaceMeanStdTraceAnchor(source, anchor, replacement, label string) (string, error) {
	if count := strings.Count(source, anchor); count != 1 {
		return "", fmt.Errorf("selected analysis.c %s anchor count=%d want 1", label, count)
	}
	return strings.Replace(source, anchor, replacement, 1), nil
}

func parseAnalysisMeanStdTrace(data []byte) (analysisMeanStdOracleTrace, error) {
	var trace analysisMeanStdOracleTrace
	const (
		headerFixedBytes = 4 + 4*4
		rowBytes         = 5*4 + (4+4+4+4+4+4+4+9+9+9)*4
	)
	if len(data) < headerFixedBytes {
		return trace, fmt.Errorf("GMSD header truncated: %d bytes", len(data))
	}
	if string(data[:4]) != "GMSD" {
		return trace, fmt.Errorf("GMSD magic=%q", data[:4])
	}
	offset := 4
	readU32 := func() (uint32, error) {
		if offset+4 > len(data) {
			return 0, fmt.Errorf("truncated u32 at byte %d", offset)
		}
		value := binary.LittleEndian.Uint32(data[offset : offset+4])
		offset += 4
		return value, nil
	}
	version, err := readU32()
	if err != nil || version != analysisMeanStdTraceVersion {
		return trace, fmt.Errorf("GMSD version=%d want %d: %v", version, analysisMeanStdTraceVersion, err)
	}
	trace.rowCount, err = readU32()
	if err != nil {
		return trace, fmt.Errorf("read GMSD row count: %w", err)
	}
	trace.overflow, err = readU32()
	if err != nil {
		return trace, fmt.Errorf("read GMSD overflow: %w", err)
	}
	hashLen, err := readU32()
	if err != nil {
		return trace, fmt.Errorf("read GMSD source-hash length: %w", err)
	}
	if hashLen != 64 || offset+int(hashLen) > len(data) {
		return trace, fmt.Errorf("GMSD source-hash length=%d want 64", hashLen)
	}
	trace.sourceHash = string(data[offset : offset+int(hashLen)])
	decodedHash, hashErr := hex.DecodeString(trace.sourceHash)
	if hashErr != nil {
		return trace, fmt.Errorf("GMSD source hash is malformed: %w", hashErr)
	}
	if len(decodedHash) != 32 {
		return trace, fmt.Errorf("GMSD source hash decoded length=%d want 32", len(decodedHash))
	}
	offset += int(hashLen)
	if trace.rowCount != 6 || len(data)-offset != int(trace.rowCount)*rowBytes {
		return trace, fmt.Errorf("GMSD rows=%d remaining bytes=%d want exactly %d", trace.rowCount, len(data)-offset, int(trace.rowCount)*rowBytes)
	}
	for i := uint32(0); i < trace.rowCount; i++ {
		row := &trace.rows[i]
		fields := []*uint32{&row.frame, &row.chunk, &row.count, &row.flags, &row.alpha}
		for _, field := range fields {
			*field, err = readU32()
			if err != nil {
				return trace, fmt.Errorf("read GMSD row %d header: %w", i, err)
			}
		}
		arrays := [][]uint32{row.cmeanOld[:], row.bfcc[:], row.mem0[:], row.mem8[:], row.mem16[:], row.mem24[:], row.feature[:], row.cmeanNew[:], row.stdOld[:], row.stdNew[:]}
		for _, array := range arrays {
			for j := range array {
				array[j], err = readU32()
				if err != nil {
					return trace, fmt.Errorf("read GMSD row %d values: %w", i, err)
				}
			}
		}
		if row.flags != 5 && row.flags != 7 {
			return trace, fmt.Errorf("GMSD row %d flags=%08x want 5 or 7", i, row.flags)
		}
		for _, array := range [][]uint32{
			{row.alpha}, row.cmeanOld[:], row.bfcc[:], row.cmeanNew[:],
			row.mem0[:], row.mem8[:], row.mem16[:], row.mem24[:], row.stdOld[:], row.feature[:], row.stdNew[:],
		} {
			for _, bits := range array {
				if bits&0x7f800000 == 0x7f800000 {
					return trace, fmt.Errorf("GMSD row %d has non-finite value %08x", i, bits)
				}
			}
		}
	}
	if offset != len(data) {
		return trace, fmt.Errorf("GMSD parser consumed %d of %d bytes", offset, len(data))
	}
	return trace, nil
}

func compareAnalysisMeanStdBits(t *testing.T, row int, name string, cBits []uint32, goValues []float32) {
	t.Helper()
	if len(cBits) != len(goValues) {
		t.Fatalf("row %d %s dimensions C=%d Go=%d", row, name, len(cBits), len(goValues))
	}
	for i, cBit := range cBits {
		goBit := math.Float32bits(goValues[i])
		if cBit != goBit {
			t.Fatalf("row %d %s[%d] Go=%08x C=%08x", row, name, i, goBit, cBit)
		}
	}
}

func logAnalysisMeanStdDerivedProducts(t *testing.T, row int, snapshot analysisMeanStdTraceSnapshot) {
	var meanProducts [4]uint32
	for i := range meanProducts {
		meanProducts[i] = math.Float32bits(snapshot.Alpha * snapshot.BFCC[i])
	}
	if snapshot.StdActive {
		var stdProducts [9]uint32
		for i, feature := range snapshot.Feature[:9] {
			first := snapshot.Alpha * feature
			stdProducts[i] = math.Float32bits(first * feature)
		}
		t.Logf("row %d source-derived products (not captured C temporaries): CMean=%08x Std=%08x", row, meanProducts, stdProducts)
		return
	}
	t.Logf("row %d source-derived products (not captured C temporaries): CMean=%08x", row, meanProducts)
}
