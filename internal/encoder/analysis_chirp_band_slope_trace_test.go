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
	analysisBandSlopeSelectedFrame = uint32(0)
	analysisBandSlopeSelectedCount = uint32(1)
)

var libopusAnalysisBandSlopeHelper libopustest.HelperCache

type libopusAnalysisBandSlopeTrace struct {
	frame         uint32
	analysisCount uint32
	writePos      uint32
	latestIndex   uint32
	valid         uint32
	calls         uint32
	stored        uint32
	overflow      uint32
	bandCount     uint32
	driverHash    string
	bandTonality  []uint32
	latestSlope   uint32
}

type goAnalysisBandSlopeTrace struct {
	frame         uint32
	analysisCount uint32
	writePos      uint32
	latestIndex   uint32
	valid         uint32
	bandTonality  [NbTBands]uint32
	latestSlope   uint32
}

func TestAnalysisChirpBandSlopeTrace(t *testing.T) {
	libopustest.RequireOracle(t)
	requireCELTTraceV3(t, "analysis chirp band/slope state trace")
	const (
		fs        = 48000
		channels  = 1
		frameSize = 960
		frames    = 50
		lsbDepth  = 24
	)
	chunkSize := fs / 50
	chunkCount := (frameSize + chunkSize - 1) / chunkSize
	if chunkCount != 1 {
		t.Fatalf("chirp fixture has %d tonality chunks per public frame; selected state requires one", chunkCount)
	}
	t.Logf("fixture=chirp_sweep_v1 channels=%d frame_size=%d frame=0 chunk=0-of-%d analyzer_count=%d frames=%d",
		channels, frameSize, chunkCount, analysisBandSlopeSelectedCount, frames)

	samples, err := testsignal.GenerateEncoderSignalVariant(
		testsignal.EncoderVariantChirpSweepV1,
		fs,
		frameSize*channels*frames,
		channels,
	)
	if err != nil {
		t.Fatalf("generate chirp_sweep_v1 input: %v", err)
	}
	clampToOpusDemoF32InPlace(samples)
	payload := libopustest.NewOraclePayloadVersion("GANI", 1,
		uint32(fs), uint32(channels), uint32(frameSize), uint32(frames), uint32(lsbDepth),
		0, ^uint32(1), 0, uint32(len(samples)),
	)
	payload.Float32s(samples...)
	input := payload.Bytes()

	baselinePath, err := libopusAnalysisHelper.Path(buildLibopusAnalysisHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "baseline chirp tonality analysis", err)
	}
	baseline, err := libopustest.RunHelper(baselinePath, input)
	if err != nil {
		libopustest.HelperUnavailable(t, "baseline chirp tonality analysis", err)
	}
	if err := validateAnalysisGANO(baseline, frames); err != nil {
		t.Fatalf("baseline chirp GANO structure: %v", err)
	}
	tracePath, driverHash := buildLibopusAnalysisBandSlopeTraceHelper(t)
	traced, err := libopustest.RunHelper(tracePath, input)
	if err != nil {
		libopustest.HelperUnavailable(t, "chirp band/slope state trace", err)
	}
	if len(traced) < len(baseline) || !bytes.Equal(traced[:len(baseline)], baseline) {
		limit := min(len(traced), len(baseline))
		diff := 0
		for diff < limit && traced[diff] == baseline[diff] {
			diff++
		}
		var got, want byte
		if diff < len(traced) {
			got = traced[diff]
		}
		if diff < len(baseline) {
			want = baseline[diff]
		}
		t.Fatalf("post-run state capture changed baseline 50-frame GANO: lengths=%d/%d firstDiff=%d got=%02x want=%02x",
			len(traced), len(baseline), diff, got, want)
	}
	cTrace, err := parseLibopusAnalysisBandSlopeTrace(traced[len(baseline):])
	if err != nil {
		t.Fatalf("parse GABS: %v", err)
	}
	if cTrace.driverHash != driverHash {
		t.Fatalf("GABS copied GANI driver hash=%s want %s", cTrace.driverHash, driverHash)
	}

	state := NewTonalityAnalysisState(fs)
	state.SetLSBDepth(lsbDepth)
	var goTrace goAnalysisBandSlopeTrace
	for frame := 0; frame < frames; frame++ {
		start := frame * frameSize * channels
		state.RunAnalysis(samples[start:start+frameSize*channels], frameSize, channels)
		if frame == int(analysisBandSlopeSelectedFrame) {
			latest := int((state.WritePos + int32(DetectSize) - 1) % int32(DetectSize))
			info := state.Info[latest]
			goTrace.frame = uint32(frame)
			goTrace.analysisCount = uint32(state.Count)
			goTrace.writePos = uint32(state.WritePos)
			goTrace.latestIndex = uint32(latest)
			if info.Valid {
				goTrace.valid = 1
			}
			for band, value := range state.PrevBandTonality {
				if !finiteAnalysisBandSlope(value) {
					t.Fatalf("Go frame=%d band=%d has non-finite previous-band tonality %08x", frame, band, math.Float32bits(value))
				}
				goTrace.bandTonality[band] = math.Float32bits(value)
			}
			if !finiteAnalysisBandSlope(info.TonalitySlope) {
				t.Fatalf("Go frame=%d raw latest slope is non-finite: %08x", frame, math.Float32bits(info.TonalitySlope))
			}
			goTrace.latestSlope = math.Float32bits(info.TonalitySlope)
		}
	}
	if goTrace.frame != analysisBandSlopeSelectedFrame || goTrace.analysisCount != analysisBandSlopeSelectedCount ||
		goTrace.valid != 1 || cTrace.analysisCount != goTrace.analysisCount ||
		cTrace.writePos != goTrace.writePos || cTrace.latestIndex != goTrace.latestIndex || cTrace.valid != goTrace.valid {
		t.Fatalf("frame-0 state metadata C(frame=%d count=%d write=%d latest=%d valid=%d) Go(frame=%d count=%d write=%d latest=%d valid=%d)",
			cTrace.frame, cTrace.analysisCount, cTrace.writePos, cTrace.latestIndex, cTrace.valid,
			goTrace.frame, goTrace.analysisCount, goTrace.writePos, goTrace.latestIndex, goTrace.valid)
	}
	compareAnalysisChirpBandSlope(t, cTrace, goTrace)
}

func buildLibopusAnalysisBandSlopeTraceHelper(t *testing.T) (string, string) {
	t.Helper()
	path, err := libopusAnalysisBandSlopeHelper.Path(func() (string, error) {
		root := celtQuantTraceRepoRoot(t)
		csrc := filepath.Join(root, "tools", "csrc")
		infoPath := filepath.Join(csrc, "libopus_analysis_info.c")
		infoSource, err := os.ReadFile(infoPath)
		if err != nil {
			return "", fmt.Errorf("read GANI driver source: %w", err)
		}
		infoHash := fmt.Sprintf("%x", sha256.Sum256(infoSource))
		if infoHash != libopusAnalysisInfoSourceSHA256 {
			return "", fmt.Errorf("GANI driver source SHA256=%s want %s", infoHash, libopusAnalysisInfoSourceSHA256)
		}
		instrumented, err := instrumentLibopusAnalysisBandSlopeDriver(string(infoSource))
		if err != nil {
			return "", err
		}
		driverHash := fmt.Sprintf("%x", sha256.Sum256([]byte(instrumented)))
		copyDir := t.TempDir()
		driverCopy := filepath.Join(copyDir, "analysis_band_slope_trace_driver.c")
		if err := os.WriteFile(driverCopy, []byte(instrumented), 0o600); err != nil {
			return "", fmt.Errorf("write source-copied GANI driver: %w", err)
		}
		header, err := os.ReadFile(filepath.Join(csrc, "libopus_analysis_band_slope_trace.h"))
		if err != nil {
			return "", fmt.Errorf("read band/slope trace header: %w", err)
		}
		headerHash := fmt.Sprintf("%x", sha256.Sum256(header))
		config := libopustest.CHelperConfig{
			Label:      "libopus chirp band/slope post-run state trace",
			OutputBase: "gopus_libopus_analysis_band_slope_count1",
			SourceFile: filepath.Join(csrc, "libopus_analysis_band_slope_trace_main.c"),
			CFlags: []string{
				"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG",
				fmt.Sprintf("-DGOPUS_ANALYSIS_BAND_SLOPE_TRACE_FRAME=%d", analysisBandSlopeSelectedFrame),
				fmt.Sprintf("-DGOPUS_ANALYSIS_BAND_SLOPE_DRIVER_SOURCE=%q", filepath.Base(driverCopy)),
				fmt.Sprintf("-DGOPUS_ANALYSIS_BAND_SLOPE_DRIVER_SHA256=%q", driverHash),
				fmt.Sprintf("-DGOPUS_ANALYSIS_BAND_SLOPE_INFO_SOURCE_SHA256=%q", infoHash),
				fmt.Sprintf("-DGOPUS_ANALYSIS_BAND_SLOPE_HEADER_SHA256=%q", headerHash),
			},
			RefIncludes: []string{"include", "src", "celt", "silk", "silk/float"},
			IncludeDirs: []string{csrc, copyDir},
			Sources:     []string{filepath.Join(csrc, "libopus_analysis_band_slope_trace.c")},
			Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
			DeadStrip:   true,
		}
		return libopustest.BuildCHelper(config)
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "libopus chirp band/slope state trace", err)
	}
	root := celtQuantTraceRepoRoot(t)
	infoSource, err := os.ReadFile(filepath.Join(root, "tools", "csrc", "libopus_analysis_info.c"))
	if err != nil {
		t.Fatalf("read GANI driver source for trace hash: %v", err)
	}
	instrumented, err := instrumentLibopusAnalysisBandSlopeDriver(string(infoSource))
	if err != nil {
		t.Fatalf("instrument GANI driver source: %v", err)
	}
	return path, fmt.Sprintf("%x", sha256.Sum256([]byte(instrumented)))
}

func instrumentLibopusAnalysisBandSlopeDriver(source string) (string, error) {
	const includeAnchor = "#include \"analysis.h\"\n"
	var err error
	source, err = replaceAnalysisTraceAnchor(source, includeAnchor,
		includeAnchor+"#include \"libopus_analysis_band_slope_trace.h\"\n", "band/slope driver trace header")
	if err != nil {
		return "", err
	}
	const endAnchor = "  }\n  fflush(stdout);\n  free(pcm);"
	const endReplacement = `    if (f == GOPUS_ANALYSIS_BAND_SLOPE_TRACE_FRAME) {
      gopus_analysis_band_slope_trace_capture((uint32_t)f, (uint32_t)st->count,
          (uint32_t)st->write_pos, (uint32_t)latest, (uint32_t)st->info[latest].valid,
          st->prev_band_tonality, st->info[latest].tonality_slope);
    }
  }
  fflush(stdout);
  free(pcm);`
	return replaceAnalysisTraceAnchor(source, endAnchor, endReplacement, "post-run GANI frame state")
}

func parseLibopusAnalysisBandSlopeTrace(data []byte) (libopusAnalysisBandSlopeTrace, error) {
	var trace libopusAnalysisBandSlopeTrace
	const (
		fixedHeaderBytes = 11 * 4
		sourceHashBytes  = 64
	)
	if len(data) < 4+fixedHeaderBytes+sourceHashBytes {
		return trace, fmt.Errorf("GABS header truncated: %d bytes", len(data))
	}
	if string(data[:4]) != "GABS" {
		return trace, fmt.Errorf("missing GABS magic")
	}
	offset := 4
	readU32 := func() uint32 {
		value := binary.LittleEndian.Uint32(data[offset:])
		offset += 4
		return value
	}
	version := readU32()
	if version != 2 {
		return trace, fmt.Errorf("GABS version=%d want 2", version)
	}
	trace.frame = readU32()
	trace.analysisCount = readU32()
	trace.writePos = readU32()
	trace.latestIndex = readU32()
	trace.valid = readU32()
	trace.calls = readU32()
	trace.stored = readU32()
	trace.overflow = readU32()
	trace.bandCount = readU32()
	hashLength := readU32()
	if trace.frame != analysisBandSlopeSelectedFrame || trace.analysisCount != analysisBandSlopeSelectedCount ||
		trace.writePos >= uint32(DetectSize) || trace.latestIndex != (trace.writePos+uint32(DetectSize)-1)%uint32(DetectSize) ||
		trace.valid != 1 || trace.calls != 1 || trace.stored != 1 || trace.overflow != 0 ||
		trace.bandCount != uint32(NbTBands) || hashLength != sourceHashBytes {
		return trace, fmt.Errorf("GABS frame=%d count=%d write=%d latest=%d valid=%d calls=%d stored=%d overflow=%d bands=%d hash=%d",
			trace.frame, trace.analysisCount, trace.writePos, trace.latestIndex, trace.valid,
			trace.calls, trace.stored, trace.overflow, trace.bandCount, hashLength)
	}
	trace.driverHash = string(data[offset : offset+sourceHashBytes])
	if _, err := hex.DecodeString(trace.driverHash); err != nil {
		return trace, fmt.Errorf("GABS driver hash malformed: %w", err)
	}
	offset += sourceHashBytes
	wantBytes := 4 + fixedHeaderBytes + sourceHashBytes + NbTBands*2*4 + 4
	if len(data) != wantBytes {
		return trace, fmt.Errorf("GABS byte length=%d want %d", len(data), wantBytes)
	}
	trace.bandTonality = make([]uint32, NbTBands)
	for i := range trace.bandTonality {
		band := readU32()
		if band != uint32(i) {
			return trace, fmt.Errorf("GABS band ordinal=%d want %d", band, i)
		}
		bits := readU32()
		if !finiteAnalysisBandSlope(math.Float32frombits(bits)) {
			return trace, fmt.Errorf("GABS band %d tonality is non-finite", i)
		}
		trace.bandTonality[i] = bits
	}
	trace.latestSlope = readU32()
	if !finiteAnalysisBandSlope(math.Float32frombits(trace.latestSlope)) {
		return trace, fmt.Errorf("GABS latest slope is non-finite")
	}
	if offset != len(data) {
		return trace, fmt.Errorf("GABS parser consumed %d bytes want %d", offset, len(data))
	}
	return trace, nil
}

func compareAnalysisChirpBandSlope(t *testing.T, cTrace libopusAnalysisBandSlopeTrace, goTrace goAnalysisBandSlopeTrace) {
	t.Helper()
	for band := range cTrace.bandTonality {
		if cTrace.bandTonality[band] != goTrace.bandTonality[band] {
			t.Fatalf("first chirp slope input difference: frame=%d count=%d band=%d C=%08x Go=%08x",
				analysisBandSlopeSelectedFrame, analysisBandSlopeSelectedCount, band,
				cTrace.bandTonality[band], goTrace.bandTonality[band])
		}
	}
	t.Logf("chirp frame=%d count=%d prev_band_tonality inputs match bitwise across %d source NB_TBANDS",
		analysisBandSlopeSelectedFrame, analysisBandSlopeSelectedCount, NbTBands)
	if cTrace.latestSlope != goTrace.latestSlope {
		t.Fatalf("first chirp slope result difference: frame=%d count=%d latest=%d C=%08x Go=%08x",
			analysisBandSlopeSelectedFrame, analysisBandSlopeSelectedCount, cTrace.latestIndex,
			cTrace.latestSlope, goTrace.latestSlope)
	}
	t.Logf("chirp frame=%d count=%d raw latest slope matches: %08x",
		analysisBandSlopeSelectedFrame, analysisBandSlopeSelectedCount, cTrace.latestSlope)
}

func finiteAnalysisBandSlope(value float32) bool {
	return !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
}

func TestAnalysisChirpBandSlopeTraceParserRejectsMalformedWire(t *testing.T) {
	valid := validAnalysisBandSlopeTraceWireFixture()
	if trace, err := parseLibopusAnalysisBandSlopeTrace(valid); err != nil {
		t.Fatalf("valid GABS fixture rejected: %v", err)
	} else if len(trace.bandTonality) != NbTBands {
		t.Fatalf("valid GABS bands=%d want source NB_TBANDS=%d", len(trace.bandTonality), NbTBands)
	}
	tests := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{name: "truncated", mutate: func(data []byte) []byte { return data[:len(data)-1] }},
		{name: "trailing bytes", mutate: func(data []byte) []byte { return append(data, 0) }},
		{name: "wrong magic", mutate: func(data []byte) []byte { copy(data, "GAPH"); return data }},
		{name: "unsupported version", mutate: func(data []byte) []byte { putGABSWord(data, 4, 3); return data }},
		{name: "wrong frame", mutate: func(data []byte) []byte { putGABSWord(data, 8, 1); return data }},
		{name: "wrong analyzer count", mutate: func(data []byte) []byte { putGABSWord(data, 12, 2); return data }},
		{name: "out of range write position", mutate: func(data []byte) []byte { putGABSWord(data, 16, uint32(DetectSize)); return data }},
		{name: "wrong latest index", mutate: func(data []byte) []byte { putGABSWord(data, 20, 1); return data }},
		{name: "invalid latest info", mutate: func(data []byte) []byte { putGABSWord(data, 24, 0); return data }},
		{name: "wrong calls", mutate: func(data []byte) []byte { putGABSWord(data, 28, 2); return data }},
		{name: "wrong stored count", mutate: func(data []byte) []byte { putGABSWord(data, 32, 2); return data }},
		{name: "overflow", mutate: func(data []byte) []byte { putGABSWord(data, 36, 1); return data }},
		{name: "wrong band count", mutate: func(data []byte) []byte { putGABSWord(data, 40, uint32(NbTBands+1)); return data }},
		{name: "wrong driver hash length", mutate: func(data []byte) []byte { putGABSWord(data, 44, 63); return data }},
		{name: "malformed driver hash", mutate: func(data []byte) []byte { data[48] = 'z'; return data }},
		{name: "wrong band ordinal", mutate: func(data []byte) []byte { putGABSWord(data, 48+64, 1); return data }},
		{name: "non-finite band tonality", mutate: func(data []byte) []byte { putGABSWord(data, 48+64+4, 0x7fc00000); return data }},
		{name: "non-finite slope", mutate: func(data []byte) []byte { putGABSWord(data, len(data)-4, 0x7fc00000); return data }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := append([]byte(nil), valid...)
			if _, err := parseLibopusAnalysisBandSlopeTrace(test.mutate(data)); err == nil {
				t.Fatal("malformed GABS payload was accepted")
			}
		})
	}
}

func validAnalysisBandSlopeTraceWireFixture() []byte {
	const (
		headerWords   = 11
		driverHashLen = 64
	)
	data := make([]byte, 4+headerWords*4+driverHashLen+NbTBands*2*4+4)
	copy(data, "GABS")
	putGABSWord(data, 4, 2)
	putGABSWord(data, 8, analysisBandSlopeSelectedFrame)
	putGABSWord(data, 12, analysisBandSlopeSelectedCount)
	putGABSWord(data, 16, 1)
	putGABSWord(data, 20, 0)
	putGABSWord(data, 24, 1)
	putGABSWord(data, 28, 1)
	putGABSWord(data, 32, 1)
	putGABSWord(data, 36, 0)
	putGABSWord(data, 40, uint32(NbTBands))
	putGABSWord(data, 44, driverHashLen)
	copy(data[48:], strings.Repeat("a", driverHashLen))
	for band := range NbTBands {
		putGABSWord(data, 48+driverHashLen+band*2*4, uint32(band))
	}
	return data
}

func putGABSWord(data []byte, offset int, value uint32) {
	binary.LittleEndian.PutUint32(data[offset:offset+4], value)
}
