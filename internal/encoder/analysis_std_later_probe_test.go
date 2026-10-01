//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
)

// TestAnalysisStdLaterActualInputsTrace captures the original C and Go
// recurrence inputs on the first later Std mismatch in the public corpus sweep.
func TestAnalysisStdLaterActualInputsTrace(t *testing.T) {
	libopustest.RequireOracle(t)
	requireCELTTraceV3(t, "later analysis Std trace")
	if !analysisMeanStdTraceEnabled {
		t.Fatal("analysis trace hook is disabled")
	}

	const (
		fs        = 48000
		channels  = 1
		frameMS   = 5
		frameSize = fs * frameMS / 1000
		frames    = 1200 / frameMS
		lsbDepth  = 24
	)
	pcm, err := testsignal.GenerateCorpusSignal(
		testsignal.CorpusPureToneV1,
		fs,
		frames*frameSize*channels,
		channels,
	)
	if err != nil {
		t.Fatalf("generate corpus_pure_tone_v1: %v", err)
	}
	input := analysisMLPGANIInput(fs, channels, frameSize, frames, lsbDepth, pcm)
	proofDir := os.Getenv("GOPUS_MEAN_STD_PROOF_DIR")
	if proofDir == "" {
		proofDir = t.TempDir()
	}
	if err := os.MkdirAll(proofDir, 0o755); err != nil {
		t.Fatalf("create proof directory: %v", err)
	}
	inputHash := fmt.Sprintf("%x", sha256.Sum256(input))
	if err := os.WriteFile(filepath.Join(proofDir, "input.gani"), input, 0o644); err != nil {
		t.Fatalf("save exact GANI input: %v", err)
	}

	basePath, err := libopusAnalysisHelper.Path(buildLibopusAnalysisHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "baseline analysis GANO", err)
	}
	baseline, err := libopustest.RunHelper(basePath, input)
	if err != nil {
		t.Fatalf("run baseline GANI helper: %v", err)
	}
	tracePath, sourceHash, sourcePath, err := buildLibopusAnalysisStdLaterTraceHelper(t, proofDir)
	if err != nil {
		libopustest.HelperUnavailable(t, "later analysis Std trace", err)
	}
	traced, err := libopustest.RunHelper(tracePath, input)
	if err != nil {
		t.Fatalf("run later Std trace helper: %v", err)
	}
	if len(traced) < len(baseline) || !bytes.Equal(traced[:len(baseline)], baseline) {
		t.Fatal("instrumented original C helper changed the complete GANO output")
	}
	cTraceBytes := traced[len(baseline):]
	if err := os.WriteFile(filepath.Join(proofDir, "c-gmsr.bin"), cTraceBytes, 0o644); err != nil {
		t.Fatalf("save C row capture: %v", err)
	}
	cRows, gotSourceHash, err := parseAnalysisStdLaterTrace(cTraceBytes)
	if err != nil {
		t.Fatalf("parse GMSR: %v", err)
	}
	if sourceHash != gotSourceHash {
		t.Fatalf("GMSR analysis.c hash=%s want %s", gotSourceHash, sourceHash)
	}
	if len(cRows) == 0 {
		t.Fatal("original C emitted no later Std rows")
	}

	state := NewTonalityAnalysisState(fs)
	state.SetLSBDepth(lsbDepth)
	goRows := make([]analysisStdLaterGoRow, 0, len(cRows))
	oldHook := analysisMeanStdTraceHook
	oldMaxCount := analysisMeanStdTraceMaxCount
	t.Cleanup(func() {
		analysisMeanStdTraceHook = oldHook
		analysisMeanStdTraceMaxCount = oldMaxCount
	})
	analysisMeanStdTraceMaxCount = 40
	analysisMeanStdTraceHook = func(snapshot analysisMeanStdTraceSnapshot) {
		if snapshot.Frame < 0 || snapshot.Frame > 35 || !snapshot.StdActive {
			return
		}
		goRows = append(goRows, analysisStdLaterGoRowFromSnapshot(snapshot))
	}
	for frame := 0; frame < frames; frame++ {
		analysisMeanStdTraceSetFrame(int32(frame))
		start := frame * frameSize * channels
		state.RunAnalysis(pcm[start:start+frameSize*channels], frameSize, channels)
	}
	analysisMeanStdTraceHook = nil

	artifact := analysisStdLaterTraceArtifact{
		Case:            "48k/ch1/5ms/corpus_pure_tone_v1",
		TargetFrame:     27,
		Frames:          frames,
		InputSHA256:     inputHash,
		CAnalysisSHA256: libopusAnalysisSourceSHA256,
		CTraceSHA256:    sourceHash,
		CSourcePath:     sourcePath,
		CTraceRows:      cRows,
		GoTraceRows:     goRows,
	}
	var capturedTarget bool
	for _, row := range cRows {
		if row.Frame == 27 && row.Chunk == 0 && row.Count == 6 {
			capturedTarget = true
			break
		}
	}
	if !capturedTarget {
		t.Fatalf("original C did not capture target update (frame=27,chunk=0,count=6); rows=%d", len(cRows))
	}
	encoded, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatalf("marshal proof artifact: %v", err)
	}
	if err := os.WriteFile(filepath.Join(proofDir, "actual-operands.json"), encoded, 0o644); err != nil {
		t.Fatalf("save operand proof artifact: %v", err)
	}

	if len(cRows) != len(goRows) {
		t.Fatalf("C later Std rows=%d Go rows=%d; captures at %s", len(cRows), len(goRows), proofDir)
	}
	for i := range cRows {
		if int32(cRows[i].Frame) != goRows[i].Frame || int32(cRows[i].Chunk) != goRows[i].Chunk || int32(cRows[i].Count) != goRows[i].Count {
			t.Fatalf("row %d C key=(%d,%d,%d) Go key=(%d,%d,%d)", i,
				cRows[i].Frame, cRows[i].Chunk, cRows[i].Count,
				goRows[i].Frame, goRows[i].Chunk, goRows[i].Count)
		}
		if cRows[i].Flags != 15 {
			t.Fatalf("row %d C flags=%08x want complete 0x0f", i, cRows[i].Flags)
		}
		if mismatch := compareAnalysisStdLaterRow(cRows[i], goRows[i]); mismatch != "" {
			t.Errorf("first C/Go actual operand mismatch row %d key=(%d,%d,%d): %s; complete rows saved under %s",
				i, cRows[i].Frame, cRows[i].Chunk, cRows[i].Count, mismatch, proofDir)
			break
		}
	}
	t.Logf("C/Go actual CMean/Std rows=%d, first captured key=(%d,%d,%d), input SHA256=%s, trace source SHA256=%s",
		len(cRows), cRows[0].Frame, cRows[0].Chunk, cRows[0].Count, inputHash, sourceHash)
	t.Logf("exact input=%s C rows=%s Go/C JSON=%s instrumented analysis.c=%s",
		filepath.Join(proofDir, "input.gani"),
		filepath.Join(proofDir, "c-gmsr.bin"),
		filepath.Join(proofDir, "actual-operands.json"), sourcePath)
	t.Run("complete_stream", func(t *testing.T) {
		requireAnalysisMatchesLibopus(t, fs, channels, frameSize, pcm)
	})
}

type analysisStdLaterCRow struct {
	Frame    uint32     `json:"frame"`
	Chunk    uint32     `json:"chunk"`
	Count    uint32     `json:"count"`
	Flags    uint32     `json:"flags"`
	Alpha    uint32     `json:"alpha_bits"`
	CMeanOld [4]uint32  `json:"cmean_old_bits"`
	BFCC     [4]uint32  `json:"bfcc_bits"`
	CMeanNew [4]uint32  `json:"cmean_new_bits"`
	Feature  [11]uint32 `json:"feature_bits"`
	StdOld   [9]uint32  `json:"std_old_bits"`
	StdNew   [9]uint32  `json:"std_new_bits"`
	Mem      [32]uint32 `json:"mem_bits"`
}

type analysisStdLaterGoRow struct {
	Frame    int32      `json:"frame"`
	Chunk    int32      `json:"chunk"`
	Count    int32      `json:"count"`
	Alpha    uint32     `json:"alpha_bits"`
	CMeanOld [4]uint32  `json:"cmean_old_bits"`
	BFCC     [4]uint32  `json:"bfcc_bits"`
	CMeanNew [4]uint32  `json:"cmean_new_bits"`
	Feature  [11]uint32 `json:"feature_bits"`
	StdOld   [9]uint32  `json:"std_old_bits"`
	StdNew   [9]uint32  `json:"std_new_bits"`
	Mem0     [4]uint32  `json:"mem_0_3_bits"`
	Mem8     [4]uint32  `json:"mem_8_11_bits"`
	Mem16    [4]uint32  `json:"mem_16_19_bits"`
	Mem24    [4]uint32  `json:"mem_24_27_bits"`
}

type analysisStdLaterTraceArtifact struct {
	Case            string                  `json:"case"`
	TargetFrame     int                     `json:"target_frame"`
	Frames          int                     `json:"frames"`
	InputSHA256     string                  `json:"input_sha256"`
	CAnalysisSHA256 string                  `json:"pinned_analysis_c_sha256"`
	CTraceSHA256    string                  `json:"instrumented_analysis_c_sha256"`
	CSourcePath     string                  `json:"instrumented_analysis_c_path"`
	CTraceRows      []analysisStdLaterCRow  `json:"c_rows"`
	GoTraceRows     []analysisStdLaterGoRow `json:"go_rows"`
}

func buildLibopusAnalysisStdLaterTraceHelper(t *testing.T, proofDir string) (string, string, string, error) {
	t.Helper()
	pinnedPath := libopustest.RefPath("src", "analysis.c")
	pinned, err := os.ReadFile(pinnedPath)
	if err != nil {
		return "", "", "", fmt.Errorf("read original analysis.c: %w", err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(pinned)); got != libopusAnalysisSourceSHA256 {
		return "", "", "", fmt.Errorf("original analysis.c SHA256=%s want pinned %s", got, libopusAnalysisSourceSHA256)
	}
	instrumented, err := instrumentAnalysisStdLaterSource(string(pinned))
	if err != nil {
		return "", "", "", err
	}
	sourcePath := filepath.Join(proofDir, "analysis_std_later_gmsr.c")
	if err := os.WriteFile(sourcePath, []byte(instrumented), 0o644); err != nil {
		return "", "", "", fmt.Errorf("write source-annotated C capture: %w", err)
	}
	sourceHash := fmt.Sprintf("%x", sha256.Sum256([]byte(instrumented)))
	csrc := filepath.Join(celtQuantTraceRepoRoot(t), "tools", "csrc")
	config := libopustest.CHelperConfig{
		Label:      "libopus later analysis Std trace",
		OutputBase: "gopus_libopus_analysis_std_later_trace_v1",
		SourceFile: "libopus_analysis_std_later_trace_main.c",
		CFlags: []string{
			"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG",
			fmt.Sprintf("-DGOPUS_ANALYSIS_STD_LATER_SOURCE_SHA256=%q", sourceHash),
		},
		RefIncludes: []string{"include", "src", "celt", "silk", "silk/float"},
		IncludeDirs: []string{csrc},
		Sources: []string{
			filepath.Join(csrc, "libopus_analysis_std_later_trace.c"),
			sourcePath,
		},
		Libs:      []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip: true,
	}
	path, err := libopustest.BuildCHelper(config)
	return path, sourceHash, sourcePath, err
}

func instrumentAnalysisStdLaterSource(source string) (string, error) {
	replacements := []struct{ old, replacement, label string }{
		{
			"#include \"analysis.h\"\n",
			"#include \"analysis.h\"\n#include \"libopus_analysis_std_later_trace.h\"\n",
			"trace header",
		},
		{
			`   int offset;
   int pcm_len;

   analysis_frame_size -= analysis_frame_size&1;`,
			`   int offset;
   int pcm_len;
   int gopus_std_later_chunk = 0;

   gopus_analysis_std_later_run_begin();
   analysis_frame_size -= analysis_frame_size&1;`,
			"run_analysis entry",
		},
		{
			`      while (pcm_len>0) {
         tonality_analysis(analysis, celt_mode, analysis_pcm, IMIN(Fs/50, pcm_len), offset, c1, c2, C, lsb_depth, downmix);`,
			`      while (pcm_len>0) {
         gopus_analysis_std_later_chunk_begin(analysis->count, gopus_std_later_chunk);
         tonality_analysis(analysis, celt_mode, analysis_pcm, IMIN(Fs/50, pcm_len), offset, c1, c2, C, lsb_depth, downmix);
         gopus_std_later_chunk++;`,
			"analysis chunk",
		},
		{
			`    for (i=0;i<4;i++)
       tonal->cmean[i] = (1-alpha)*tonal->cmean[i] + alpha*BFCC[i];`,
			`    if (tonal->count >= 6)
       gopus_analysis_std_later_mean_begin(tonal->count-1, alpha, tonal->cmean, BFCC);
    for (i=0;i<4;i++)
       tonal->cmean[i] = (1-alpha)*tonal->cmean[i] + alpha*BFCC[i];
    if (tonal->count >= 6)
       gopus_analysis_std_later_mean_finish(tonal->cmean);`,
			"CMean update",
		},
		{
			`    if (tonal->count > 5)
    {
       for (i=0;i<9;i++)
          tonal->std[i] = (1-alpha)*tonal->std[i] + alpha*features[i]*features[i];
    }`,
			`    if (tonal->count > 5)
    {
       if (tonal->count <= 36)
          gopus_analysis_std_later_before_std(tonal->std, features, tonal->mem);
       for (i=0;i<9;i++)
          tonal->std[i] = (1-alpha)*tonal->std[i] + alpha*features[i]*features[i];
       if (tonal->count <= 36)
          gopus_analysis_std_later_finish(tonal->std);
    }`,
			"Std update",
		},
	}
	for _, replacement := range replacements {
		if strings.Count(source, replacement.old) != 1 {
			return "", fmt.Errorf("original analysis.c anchor %q count=%d want 1", replacement.label, strings.Count(source, replacement.old))
		}
		source = strings.Replace(source, replacement.old, replacement.replacement, 1)
	}
	return source, nil
}

func parseAnalysisStdLaterTrace(data []byte) ([]analysisStdLaterCRow, string, error) {
	if len(data) < 4+4*4 {
		return nil, "", fmt.Errorf("GMSR truncated: %d bytes", len(data))
	}
	if string(data[:4]) != "GMSR" {
		return nil, "", fmt.Errorf("GMSR magic=%q", data[:4])
	}
	offset := 4
	readU32 := func() (uint32, error) {
		if offset+4 > len(data) {
			return 0, fmt.Errorf("GMSR truncated u32 at %d", offset)
		}
		value := binary.LittleEndian.Uint32(data[offset : offset+4])
		offset += 4
		return value, nil
	}
	version, err := readU32()
	if err != nil || version != 1 {
		return nil, "", fmt.Errorf("GMSR version=%d want 1: %v", version, err)
	}
	rowCount, err := readU32()
	if err != nil {
		return nil, "", err
	}
	overflow, err := readU32()
	if err != nil || overflow != 0 {
		return nil, "", fmt.Errorf("GMSR overflow=%08x: %v", overflow, err)
	}
	hashLen, err := readU32()
	if err != nil || hashLen != 64 || offset+int(hashLen) > len(data) {
		return nil, "", fmt.Errorf("GMSR source hash length=%d: %v", hashLen, err)
	}
	sourceHash := string(data[offset : offset+int(hashLen)])
	if _, err := hex.DecodeString(sourceHash); err != nil {
		return nil, "", fmt.Errorf("GMSR source hash: %w", err)
	}
	offset += int(hashLen)
	const rowBytes = (5 + 4 + 4 + 4 + 11 + 9 + 9 + 32) * 4
	if uint64(len(data)-offset) != uint64(rowCount)*rowBytes {
		return nil, "", fmt.Errorf("GMSR rows=%d remaining=%d want=%d", rowCount, len(data)-offset, uint64(rowCount)*rowBytes)
	}
	rows := make([]analysisStdLaterCRow, rowCount)
	for i := range rows {
		row := &rows[i]
		for _, field := range []*uint32{&row.Frame, &row.Chunk, &row.Count, &row.Flags, &row.Alpha} {
			*field, err = readU32()
			if err != nil {
				return nil, "", err
			}
		}
		for _, array := range [][]uint32{row.CMeanOld[:], row.BFCC[:], row.CMeanNew[:], row.Feature[:], row.StdOld[:], row.StdNew[:], row.Mem[:]} {
			for j := range array {
				array[j], err = readU32()
				if err != nil {
					return nil, "", err
				}
			}
		}
		for _, array := range [][]uint32{row.CMeanOld[:], row.BFCC[:], row.CMeanNew[:], row.Feature[:], row.StdOld[:], row.StdNew[:], row.Mem[:], {row.Alpha}} {
			for _, bits := range array {
				if bits&0x7f800000 == 0x7f800000 {
					return nil, "", fmt.Errorf("GMSR row %d non-finite float bits %08x", i, bits)
				}
			}
		}
	}
	return rows, sourceHash, nil
}

func analysisStdLaterGoRowFromSnapshot(snapshot analysisMeanStdTraceSnapshot) analysisStdLaterGoRow {
	row := analysisStdLaterGoRow{Frame: snapshot.Frame, Chunk: snapshot.Chunk, Count: snapshot.Count, Alpha: math.Float32bits(snapshot.Alpha)}
	for i := range 4 {
		row.CMeanOld[i] = math.Float32bits(snapshot.CMeanOld[i])
		row.BFCC[i] = math.Float32bits(snapshot.BFCC[i])
		row.CMeanNew[i] = math.Float32bits(snapshot.CMeanNew[i])
		row.Mem0[i] = math.Float32bits(snapshot.Mem0[i])
		row.Mem8[i] = math.Float32bits(snapshot.Mem8[i])
		row.Mem16[i] = math.Float32bits(snapshot.Mem16[i])
		row.Mem24[i] = math.Float32bits(snapshot.Mem24[i])
	}
	for i := range snapshot.Feature {
		row.Feature[i] = math.Float32bits(snapshot.Feature[i])
	}
	for i := range snapshot.StdOld {
		row.StdOld[i] = math.Float32bits(snapshot.StdOld[i])
		row.StdNew[i] = math.Float32bits(snapshot.StdNew[i])
	}
	return row
}

func compareAnalysisStdLaterRow(c analysisStdLaterCRow, goRow analysisStdLaterGoRow) string {
	if c.Alpha != goRow.Alpha {
		return fmt.Sprintf("alpha C=%08x Go=%08x", c.Alpha, goRow.Alpha)
	}
	if s := compareU32Arrays("CMeanOld", c.CMeanOld[:], goRow.CMeanOld[:]); s != "" {
		return s
	}
	if s := compareU32Arrays("BFCC", c.BFCC[:], goRow.BFCC[:]); s != "" {
		return s
	}
	if s := compareU32Arrays("CMeanNew", c.CMeanNew[:], goRow.CMeanNew[:]); s != "" {
		return s
	}
	if s := compareU32Arrays("feature[0:11]", c.Feature[:], goRow.Feature[:]); s != "" {
		return s
	}
	if s := compareU32Arrays("StdOld", c.StdOld[:], goRow.StdOld[:]); s != "" {
		return s
	}
	if s := compareU32Arrays("StdNew", c.StdNew[:], goRow.StdNew[:]); s != "" {
		return s
	}
	for i, idx := range []int{0, 8, 16, 24} {
		goMem := [][4]uint32{goRow.Mem0, goRow.Mem8, goRow.Mem16, goRow.Mem24}[i]
		var cMem [4]uint32
		copy(cMem[:], c.Mem[idx:idx+4])
		if s := compareU32Arrays(fmt.Sprintf("Mem[%d:%d]", idx, idx+4), cMem[:], goMem[:]); s != "" {
			return s
		}
	}
	return ""
}

func compareU32Arrays(name string, c, goValues []uint32) string {
	for i := range c {
		if c[i] != goValues[i] {
			return fmt.Sprintf("%s[%d] C=%08x Go=%08x", name, i, c[i], goValues[i])
		}
	}
	return ""
}

func TestParseAnalysisStdLaterTraceRejectsMalformedPackets(t *testing.T) {
	valid := analysisStdLaterTestPacket()
	rows, sourceHash, err := parseAnalysisStdLaterTrace(valid)
	if err != nil {
		t.Fatalf("parse valid GMSR packet: %v", err)
	}
	if len(rows) != 1 || sourceHash != strings.Repeat("a", 64) {
		t.Fatalf("valid GMSR result rows=%d hash=%q", len(rows), sourceHash)
	}

	const (
		rowStart        = 4 + 4*4 + 64
		rowBytes        = (5 + 4 + 4 + 4 + 11 + 9 + 9 + 32) * 4
		sourceHashStart = 4 + 4*4
	)
	putU32 := func(packet []byte, offset int, value uint32) {
		binary.LittleEndian.PutUint32(packet[offset:offset+4], value)
	}
	tests := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{
			name: "bad magic",
			mutate: func(packet []byte) []byte {
				packet[0] = 'X'
				return packet
			},
		},
		{
			name: "unsupported version",
			mutate: func(packet []byte) []byte {
				putU32(packet, 4, 2)
				return packet
			},
		},
		{
			name: "overflow flag",
			mutate: func(packet []byte) []byte {
				putU32(packet, 12, 1)
				return packet
			},
		},
		{
			name: "row count exceeds payload",
			mutate: func(packet []byte) []byte {
				putU32(packet, 8, 2)
				return packet
			},
		},
		{
			name: "truncated row trailer",
			mutate: func(packet []byte) []byte {
				return packet[:len(packet)-1]
			},
		},
		{
			name: "unexpected trailing byte",
			mutate: func(packet []byte) []byte {
				return append(packet, 0)
			},
		},
		{
			name: "wrong source hash length",
			mutate: func(packet []byte) []byte {
				putU32(packet, 16, 63)
				return packet
			},
		},
		{
			name: "non-hex source hash",
			mutate: func(packet []byte) []byte {
				packet[sourceHashStart] = 'g'
				return packet
			},
		},
		{
			name: "non-finite float",
			mutate: func(packet []byte) []byte {
				binary.LittleEndian.PutUint32(packet[rowStart+5*4:rowStart+6*4], math.Float32bits(float32(math.Inf(1))))
				return packet
			},
		},
	}
	if len(valid) != rowStart+rowBytes {
		t.Fatalf("test packet size=%d want=%d", len(valid), rowStart+rowBytes)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			packet := append([]byte(nil), valid...)
			packet = test.mutate(packet)
			if _, _, err := parseAnalysisStdLaterTrace(packet); err == nil {
				t.Fatal("malformed GMSR packet was accepted")
			}
		})
	}
}

func analysisStdLaterTestPacket() []byte {
	const (
		headerBytes = 4 + 4*4
		hashBytes   = 64
		rowBytes    = (5 + 4 + 4 + 4 + 11 + 9 + 9 + 32) * 4
	)
	packet := make([]byte, headerBytes+hashBytes+rowBytes)
	copy(packet, "GMSR")
	binary.LittleEndian.PutUint32(packet[4:], 1)
	binary.LittleEndian.PutUint32(packet[8:], 1)
	binary.LittleEndian.PutUint32(packet[12:], 0)
	binary.LittleEndian.PutUint32(packet[16:], hashBytes)
	copy(packet[headerBytes:], strings.Repeat("a", hashBytes))
	return packet
}
