//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/testsignal"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

const (
	analysisDown2HPInputMagic  = "GSDI"
	analysisDown2HPOutputMagic = "GSDO"
	analysisDown2HPInputLimit  = 960
)

var analysisDown2HPKernelHelper libopustest.HelperCache
var analysisDown2HPAllocSink float32

type analysisDown2HPCase struct {
	name  string
	state [3]float32
	in    []float32
}

type analysisDown2HPResult struct {
	state    [3]float32
	hpEnergy float32
	out      []float32
}

func requireAnalysisDown2HPV3Target(t *testing.T) {
	t.Helper()
	target, err := libopustooling.ResolveLibopusAMD64Target()
	if err == nil && target == "v3" {
		return
	}
	message := "analysis down2hp source oracle requires GOPUS_LIBOPUS_AMD64_TARGET=v3"
	if err != nil {
		message += ": " + err.Error()
	} else {
		message += fmt.Sprintf(", got %q", target)
	}
	if libopustest.StrictRefRequired() {
		t.Fatal(message)
	}
	t.Skip(message)
}

func buildAnalysisDown2HPKernelHelper() (string, string, error) {
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		return "", "", err
	}
	analysisPath := libopustest.RefPath("src", "analysis.c")
	root := filepath.Clean(filepath.Join(filepath.Dir(analysisPath), "..", "..", ".."))
	switch variant {
	case libopustooling.LibopusReferenceScalar:
		if !libopustooling.EnsureLibopusScalar(libopustooling.DefaultVersion, []string{root}) {
			return "", "", fmt.Errorf("ensure selected scalar libopus reference at %s", root)
		}
	case libopustooling.LibopusReferenceSIMD:
		if !libopustooling.EnsureLibopusSIMD(libopustooling.DefaultVersion, []string{root}) {
			return "", "", fmt.Errorf("ensure selected SIMD libopus reference at %s", root)
		}
	default:
		return "", "", fmt.Errorf("analysis down2hp source oracle selected unsupported reference variant %q", variant)
	}

	analysisSource, err := os.ReadFile(analysisPath)
	if err != nil {
		return "", "", fmt.Errorf("read selected libopus src/analysis.c: %w", err)
	}
	sourceHash := sha256.Sum256(analysisSource)
	sourceHashHex := hex.EncodeToString(sourceHash[:])
	config := libopustest.CHelperConfig{
		Label:      "libopus analysis down2hp source kernel",
		OutputBase: "gopus_libopus_analysis_down2hp_kernel",
		SourceFile: "libopus_analysis_down2hp_kernel.c",
		CFlags: []string{
			"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG",
			fmt.Sprintf("-DGOPUS_ANALYSIS_SOURCE_SHA256=%q", sourceHashHex),
		},
		// analysis.c is included with angle brackets by the helper. BuildCHelper
		// places these directories under the already selected reference tree.
		RefIncludes: []string{"include", "src", "celt", "silk", "silk/float"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	}
	path, err := analysisDown2HPKernelHelper.CHelperPath(config)
	if err != nil {
		return "", "", err
	}
	return path, sourceHashHex, nil
}

func analysisDown2HPCases(t *testing.T) []analysisDown2HPCase {
	t.Helper()
	const (
		frameSamples = analysisDown2HPInputLimit
		channels     = 2
	)
	pcm, err := testsignal.GenerateEncoderSignalVariant(
		testsignal.EncoderVariantAMMultisineV1, 48000, frameSamples*channels, channels,
	)
	if err != nil {
		t.Fatalf("generate quantized analysis input: %v", err)
	}
	pcm = quantizeCELTTracePCM(pcm)
	actual := make([]float32, frameSamples)
	for i := range actual {
		// Match opus_encoder.c:downmix_float for c2==-2 with two channels,
		// then analysis.c:downmix_and_resample's HALF32 step.
		value := pcm[2*i]*celtSigScale + pcm[2*i+1]*celtSigScale
		if value < -65536 {
			value = -65536
		}
		if value > 65536 {
			value = 65536
		}
		actual[i] = 0.5 * value
	}

	var synthetic [analysisDown2HPInputLimit]float32
	pattern := [...]float32{-65536, 65536, -0.25, 0.125, 8192, -16384, 1.0 / 32768, -1.0 / 32768}
	for i := range synthetic {
		synthetic[i] = pattern[i%len(pattern)]
	}

	lengths := [...]int{0, 1, 2, 3, 16, 480, 960}
	cases := make([]analysisDown2HPCase, 0, len(lengths)*2)
	for _, n := range lengths {
		cases = append(cases,
			analysisDown2HPCase{name: fmt.Sprintf("quantized-stereo-downmix-%d", n), in: append([]float32(nil), actual[:n]...)},
			analysisDown2HPCase{
				name:  fmt.Sprintf("bounded-synthetic-%d", n),
				state: [3]float32{0.125, -0.25, 0.0625},
				in:    append([]float32(nil), synthetic[:n]...),
			},
		)
	}
	return cases
}

func probeAnalysisDown2HP(cases []analysisDown2HPCase) ([]analysisDown2HPResult, string, error) {
	binPath, sourceHash, err := buildAnalysisDown2HPKernelHelper()
	if err != nil {
		return nil, "", err
	}
	payload := libopustest.NewOraclePayload(analysisDown2HPInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.in)))
		payload.Float32s(tc.state[:]...)
		payload.Float32s(tc.in...)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "analysis down2hp source kernel", analysisDown2HPOutputMagic)
	if err != nil {
		return nil, "", err
	}
	count := reader.Count(len(cases))
	if got := string(reader.Bytes(64)); got != sourceHash {
		return nil, "", fmt.Errorf("included libopus src/analysis.c hash=%s want selected source %s", got, sourceHash)
	}
	results := make([]analysisDown2HPResult, count)
	for i := range results {
		inputLen := int(reader.U32())
		outputLen := int(reader.U32())
		if inputLen != len(cases[i].in) || outputLen != len(cases[i].in)/2 {
			return nil, "", fmt.Errorf("record %d geometry input=%d output=%d want %d/%d", i, inputLen, outputLen, len(cases[i].in), len(cases[i].in)/2)
		}
		for j := range results[i].state {
			results[i].state[j] = reader.Float32()
		}
		results[i].hpEnergy = reader.Float32()
		results[i].out = make([]float32, outputLen)
		for j := range results[i].out {
			results[i].out[j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, "", err
	}
	return results, sourceHash, nil
}

func TestAnalysisDown2HPV3MatchesLibopusSourceKernel(t *testing.T) {
	requireAnalysisDown2HPV3Target(t)
	libopustest.RequireOracle(t)
	cases := analysisDown2HPCases(t)
	want, sourceHash, err := probeAnalysisDown2HP(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "analysis down2hp source kernel", err)
	}
	for i, tc := range cases {
		state := tc.state
		out := make([]float32, len(tc.in)/2)
		hpEnergy := silkResamplerDown2HP(state[:], out, tc.in)
		for j := range state {
			if gotBits, wantBits := math.Float32bits(state[j]), math.Float32bits(want[i].state[j]); gotBits != wantBits {
				t.Fatalf("%s state[%d]=%08x want=%08x source=%s", tc.name, j, gotBits, wantBits, sourceHash)
			}
		}
		if gotBits, wantBits := math.Float32bits(hpEnergy), math.Float32bits(want[i].hpEnergy); gotBits != wantBits {
			t.Fatalf("%s high-pass energy=%08x want=%08x source=%s", tc.name, gotBits, wantBits, sourceHash)
		}
		for j := range out {
			if gotBits, wantBits := math.Float32bits(out[j]), math.Float32bits(want[i].out[j]); gotBits != wantBits {
				t.Fatalf("%s output[%d]=%08x want=%08x source=%s", tc.name, j, gotBits, wantBits, sourceHash)
			}
		}
	}

	input := cases[len(cases)-2].in
	var state [3]float32
	var out [analysisDown2HPInputLimit / 2]float32
	for range 3 {
		state = [3]float32{}
		analysisDown2HPAllocSink = silkResamplerDown2HP(state[:], out[:], input)
	}
	allocs := testing.AllocsPerRun(100, func() {
		state = [3]float32{}
		analysisDown2HPAllocSink = silkResamplerDown2HP(state[:], out[:], input)
	})
	if allocs != 0 {
		t.Fatalf("silkResamplerDown2HP allocations=%g want 0", allocs)
	}
}

func TestAnalysisGetInfoMinMaxHistoryThresholdReplayTrace(t *testing.T) {
	libopustest.RequireOracle(t)
	requireCELTTraceV3(t, "analysis getInfo min/max history replay trace")

	const fs, channels, frameSize, frames, lsbDepth = 16000, 2, 320, 60, 24
	samples, err := testsignal.GenerateCorpusSignal(
		testsignal.CorpusPureToneV1, fs, frameSize*channels*frames, channels)
	if err != nil {
		t.Fatal(err)
	}
	pcmBytes := make([]byte, 4*len(samples))
	for i, sample := range samples {
		binary.LittleEndian.PutUint32(pcmBytes[4*i:], math.Float32bits(sample))
	}
	input := analysisMLPGANIInput(fs, channels, frameSize, frames, lsbDepth, samples)
	const (
		expectedPCMHash  = "b3d8d47ddc460ce32bdc0dea61e55a7b5a3642ddc3b0237f446349c3552de46d"
		expectedGANIHash = "199ef69119ffdd5425ad68a0f7bdd346e580d7b9a4f07cabcbf9dc5dbf2fb329"
	)
	pcmHash, inputHash := fmt.Sprintf("%x", sha256.Sum256(pcmBytes)), fmt.Sprintf("%x", sha256.Sum256(input))
	if pcmHash != expectedPCMHash || inputHash != expectedGANIHash {
		t.Fatalf("pure-tone corpus hashes float32LE=%s GANI=%s want float32LE=%s GANI=%s",
			pcmHash, inputHash, expectedPCMHash, expectedGANIHash)
	}
	t.Logf("exact corpus input: generator=GenerateCorpusSignal(CorpusPureToneV1) fs=%d channels=%d samples=%d frames=%d frameSize=%d lsbDepth=%d clampToOpusDemoF32InPlace=false float32LE_SHA256=%s GANI_SHA256=%s",
		fs, channels, len(samples), frames, frameSize, lsbDepth, pcmHash, inputHash)

	baselinePath, err := libopusAnalysisHelper.Path(buildLibopusAnalysisHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "baseline tonality analysis", err)
	}
	baseline, err := libopustest.RunHelper(baselinePath, input)
	if err != nil {
		t.Fatal(err)
	}

	for _, captureFrame := range [...]uint32{0, 2} {
		t.Run(fmt.Sprintf("frame-%d", captureFrame), func(t *testing.T) {
			tracePath, driverHash := buildLibopusAnalysisGetInfoTraceHelper(t, captureFrame)
			traced, err := libopustest.RunHelper(tracePath, input)
			if err != nil {
				t.Fatal(err)
			}
			if len(traced) < len(baseline) || !bytes.Equal(traced[:len(baseline)], baseline) {
				t.Fatal("C getInfo trace driver changed the ordinary GANO output")
			}
			trace, err := parseAnalysisGetInfoTrace(traced[len(baseline):])
			if err != nil {
				t.Fatalf("parse GGET: %v", err)
			}
			if trace.frame != captureFrame || trace.frames != frames || trace.fs != fs || trace.channels != channels ||
				trace.frameSize != frameSize || trace.lsbDepth != lsbDepth || trace.driverHash != driverHash {
				t.Fatalf("GGET fixture metadata frame=%d frames=%d fs=%d channels=%d size=%d lsb=%d hash=%s want frame=%d hash=%s",
					trace.frame, trace.frames, trace.fs, trace.channels, trace.frameSize, trace.lsbDepth,
					trace.driverHash, captureFrame, driverHash)
			}
			var wantMeta [analysisGetInfoTraceMetaLen]uint32
			switch captureFrame {
			case 0:
				wantMeta = [analysisGetInfoTraceMetaLen]uint32{0, 0, 1, 0, 0, 0, 1, 0, 1, 1, 0, 1, 1, 1, 1, 1}
			case 2:
				wantMeta = [analysisGetInfoTraceMetaLen]uint32{2, 0, 3, 0, 2, 0, 3, 0, 3, 3, 0, 3, 1, 1, 1, 1}
			default:
				t.Fatalf("unexpected unfixed getInfo capture frame %d", captureFrame)
			}
			if trace.meta != wantMeta {
				t.Fatalf("frame%d complete original C metadata=%v want %v", captureFrame, trace.meta, wantMeta)
			}
			if trace.meta[12] != 1 || trace.meta[13] != 1 || trace.meta[14] != 1 || trace.meta[15] != 1 {
				t.Fatalf("C replay flags calls/info/state/source-shape=%v", trace.meta[12:])
			}

			baseFrame, err := parseAnalysisGetInfoBaselineFrame(baseline, frames, int(captureFrame))
			if err != nil {
				t.Fatal(err)
			}
			if d := diffAnalysisInfo(trace.linkedReturn, baseFrame.ret); d != "" {
				t.Fatalf("original linked C GGET versus GANO frame%d: %s", captureFrame, d)
			}
			if d := diffAnalysisInfo(trace.linkedReturn, trace.replayReturn); d != "" {
				t.Fatalf("original linked/replayed C getter differ: %s", d)
			}

			// Replay the original C state before attributing a failure to the Go
			// analyzer or its FFT/MLP path.
			cState := analysisGetInfoStateFromTrace(trace)
			goCReplay := cState.tonalityGetInfo(frameSize)
			cReplayDiff := diffAnalysisInfo(analysisInfoToOracle(goCReplay), trace.replayReturn)

			goState := NewTonalityAnalysisState(fs)
			goState.SetLSBDepth(lsbDepth)
			var goReturn AnalysisInfo
			for frame := 0; frame <= int(captureFrame); frame++ {
				start := frame * frameSize * channels
				goReturn = goState.RunAnalysis(samples[start:start+frameSize*channels], frameSize, channels)
			}
			if d := analysisStateDiff(goState, baseFrame); d != "" {
				t.Fatalf("frame%d analyzer state differs before getter replay: %s", captureFrame, d)
			}
			compareAnalysisGetInfoRing(t, trace.info, goState.Info)
			goEndpointDiff := diffAnalysisInfo(analysisInfoToOracle(goReturn), baseFrame.ret)

			model := modelAnalysisGetInfoPMaxHistory(trace)
			t.Logf("same-original-C-state frame=%d cursor pre=%d/%d post=%d/%d restored=%d/%d replay-final=%d/%d write=%d count=%d lookahead=%d history=%d pos0=%d",
				captureFrame, trace.meta[0], trace.meta[1], trace.meta[2], trace.meta[3], trace.meta[4], trace.meta[5],
				trace.meta[6], trace.meta[7], trace.meta[8], trace.meta[9], model.lookahead, model.history, model.pos0)
			t.Logf("original C GGET complete metadata=%v", trace.meta)
			t.Logf("captured C ring pmax history operands oldest-to-current=%v", model.historyBits)
			t.Logf("source-shaped model (not C temporaries): raw=%08x vad=%08x pmaxBeforeHistory=%08x pmaxAfterHistory=%08x biasMul=%08x biased=%08x blend=%08x delta=%08x roundedMul=%08x separateSum=%08x fusedFMA=%08x linkedC=%08x GoCReplay=%08x GoRunAnalysis=%08x",
				model.rawCurrent, model.vad, model.pmaxBeforeHistory, model.pmaxAfterHistory, model.biasMul,
				model.biasedPmax, model.blend, model.delta, model.blendProduct, model.separateFinal,
				model.fusedFinal, trace.replayReturn.musicMax, math.Float32bits(goCReplay.MusicProbMax), math.Float32bits(goReturn.MusicProbMax))
			t.Logf("original C linked-vs-Go RunAnalysis diff=%q; same-C-state Go replay diff=%q", goEndpointDiff, cReplayDiff)
			if model.fusedFinal != trace.replayReturn.musicMax && model.separateFinal != trace.replayReturn.musicMax {
				t.Fatalf("captured C output %08x matches neither source-shaped separate %08x nor fused %08x model",
					trace.replayReturn.musicMax, model.separateFinal, model.fusedFinal)
			}
			if cReplayDiff != "" {
				t.Fatalf("Go getter replay from original C snapshot differs from linked original C replay: %s", cReplayDiff)
			}
			if goEndpointDiff != "" {
				t.Fatalf("same-input Go RunAnalysis frame%d differs from original GANO: %s", captureFrame, goEndpointDiff)
			}
		})
	}
}

type analysisGetInfoPMaxModel struct {
	lookahead, history                                                         uint32
	pos0                                                                       int
	historyBits                                                                []uint32
	rawCurrent, vad, pmaxBeforeHistory, pmaxAfterHistory                       uint32
	biasMul, biasedPmax, blend, delta, blendProduct, separateFinal, fusedFinal uint32
}

func modelAnalysisGetInfoPMaxHistory(trace analysisGetInfoTrace) analysisGetInfoPMaxModel {
	m := analysisGetInfoPMaxModel{}
	readPos, writePos := int(trace.meta[0]), int(trace.meta[8])
	look := writePos - readPos
	if look < 0 {
		look += DetectSize
	}
	m.lookahead = uint32(look)
	pos := readPos
	if int(trace.frameSize) > int(trace.fs)/50 && pos != writePos {
		pos++
		if pos == DetectSize {
			pos = 0
		}
	}
	if pos == writePos {
		pos--
	}
	if pos < 0 {
		pos = DetectSize - 1
	}
	m.pos0 = pos
	mpos, vpos := pos, pos
	if look > 15 {
		mpos += 5
		if mpos >= DetectSize {
			mpos -= DetectSize
		}
		vpos++
		if vpos >= DetectSize {
			vpos = 0
		}
	}
	vad := math.Float32frombits(trace.info[vpos].activityProb)
	weight := maxf(0.1, vad)
	probCount := weight
	probAvg := analysisGetInfoTraceMul32(weight, math.Float32frombits(trace.info[mpos].musicProb))
	probMax := float32(0)
	for {
		mpos++
		if mpos == DetectSize {
			mpos = 0
		}
		if mpos == writePos {
			break
		}
		vpos++
		if vpos == DetectSize {
			vpos = 0
		}
		if vpos == writePos {
			break
		}
		posVAD := math.Float32frombits(trace.info[vpos].activityProb)
		posWeight := maxf(0.1, posVAD)
		denom := probCount
		if denom < 1e-9 {
			denom = 1e-9
		}
		candidate := analysisGetInfoTraceAdd32(probAvg,
			-analysisGetInfoTraceMul32(transitionPenalty, vad-posVAD))
		probMin := candidate / denom
		_ = probMin
		candidateMax := analysisGetInfoTraceAdd32(probAvg,
			analysisGetInfoTraceMul32(transitionPenalty, vad-posVAD)) / denom
		probMax = maxf(candidateMax, probMax)
		probCount = analysisGetInfoTraceAdd32(probCount, posWeight)
		probAvg = analysisGetInfoTraceAdd32(probAvg,
			analysisGetInfoTraceMul32(posWeight, math.Float32frombits(trace.info[mpos].musicProb)))
	}
	probMusic := probAvg / probCount
	probMax = maxf(probMusic, probMax)
	probMax = minf(probMax, 1)
	m.rawCurrent = math.Float32bits(probMusic)
	m.vad = math.Float32bits(vad)
	m.pmaxBeforeHistory = math.Float32bits(probMax)
	pmax := probMax
	history := max(min(int(trace.meta[9])-1, 15), 0)
	m.history = uint32(history)
	for i := 0; i < history; i++ {
		pos--
		if pos < 0 {
			pos = DetectSize - 1
		}
		bits := trace.info[pos].musicProb
		m.historyBits = append(m.historyBits, bits)
		pmax = maxf(pmax, math.Float32frombits(bits))
	}
	m.pmaxAfterHistory = math.Float32bits(pmax)
	biasMul := analysisGetInfoTraceMul32(0.1, vad)
	biased := minf(1, analysisGetInfoTraceAdd32(pmax, biasMul))
	blend := analysisGetInfoTraceSub32(1, analysisGetInfoTraceMul32(0.1, float32(look)))
	delta := analysisGetInfoTraceSub32(biased, probMax)
	product := analysisGetInfoTraceMul32(blend, delta)
	separate := analysisGetInfoTraceAdd32(probMax, product)
	fused := opusmath.FMA32(blend, delta, probMax)
	m.biasMul, m.biasedPmax, m.blend, m.delta = math.Float32bits(biasMul), math.Float32bits(biased), math.Float32bits(blend), math.Float32bits(delta)
	m.blendProduct, m.separateFinal, m.fusedFinal = math.Float32bits(product), math.Float32bits(separate), math.Float32bits(fused)
	return m
}

//go:noinline
func analysisGetInfoTraceSub32(a, b float32) float32 { return a - b }

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

type analysisGetInfoTrace struct {
	frame, frames, fs, channels, frameSize, lsbDepth uint32
	driverHash                                       string
	meta                                             [analysisGetInfoTraceMetaLen]uint32
	linkedReturn, replayReturn                       analysisOracleInfo
	info                                             []analysisOracleInfo
}

func parseAnalysisGetInfoTrace(data []byte) (analysisGetInfoTrace, error) {
	const headerU32s = 8
	const infoBytes = 12*4 + 20
	var out analysisGetInfoTrace
	if len(data) < 4+headerU32s*4 || string(data[:4]) != "GGET" {
		return out, fmt.Errorf("invalid GGET header")
	}
	pos := 4
	read := func(label string) (uint32, error) {
		if len(data)-pos < 4 {
			return 0, fmt.Errorf("truncated GGET %s", label)
		}
		v := binary.LittleEndian.Uint32(data[pos : pos+4])
		pos += 4
		return v, nil
	}
	var err error
	version, err := read("version")
	if err != nil {
		return out, err
	}
	if version != analysisGetInfoTraceVersion {
		return out, fmt.Errorf("GGET version=%d want %d", version, analysisGetInfoTraceVersion)
	}
	fields := []*uint32{&out.frame, &out.frames, &out.fs, &out.channels, &out.frameSize, &out.lsbDepth}
	for i, dst := range fields {
		*dst, err = read(fmt.Sprintf("header field %d", i))
		if err != nil {
			return out, err
		}
	}
	hashLen, err := read("driver hash length")
	if err != nil {
		return out, err
	}
	if out.frame >= out.frames || hashLen != 64 {
		return out, fmt.Errorf("invalid GGET fixture metadata frame=%d frames=%d fs=%d channels=%d frame_size=%d lsb=%d hash_len=%d",
			out.frame, out.frames, out.fs, out.channels, out.frameSize, out.lsbDepth, hashLen)
	}
	if len(data)-pos < int(hashLen) {
		return out, fmt.Errorf("truncated GGET driver source hash")
	}
	hashBytes := data[pos : pos+int(hashLen)]
	if _, err := hex.DecodeString(string(hashBytes)); err != nil || strings.ToLower(string(hashBytes)) != string(hashBytes) {
		return out, fmt.Errorf("invalid GGET driver source hash")
	}
	out.driverHash = string(hashBytes)
	pos += int(hashLen)
	metaCount, err := read("metadata count")
	if err != nil {
		return out, err
	}
	if metaCount != analysisGetInfoTraceMetaLen {
		return out, fmt.Errorf("GGET metadata count=%d want %d", metaCount, analysisGetInfoTraceMetaLen)
	}
	for i := range out.meta {
		out.meta[i], err = read(fmt.Sprintf("metadata[%d]", i))
		if err != nil {
			return out, err
		}
	}
	if len(data)-pos < 2*infoBytes+4 {
		return out, fmt.Errorf("truncated GGET getter return records")
	}
	if out.linkedReturn, err = parseAnalysisGetInfoInfo(data[pos : pos+infoBytes]); err != nil {
		return out, fmt.Errorf("GGET original linked return: %w", err)
	}
	pos += infoBytes
	if out.replayReturn, err = parseAnalysisGetInfoInfo(data[pos : pos+infoBytes]); err != nil {
		return out, fmt.Errorf("GGET restored-cursor replay return: %w", err)
	}
	pos += infoBytes
	infoCount, err := read("Info ring count")
	if err != nil {
		return out, err
	}
	if infoCount != DetectSize {
		return out, fmt.Errorf("GGET Info ring count=%d want %d", infoCount, DetectSize)
	}
	if len(data)-pos < int(infoCount)*infoBytes {
		return out, fmt.Errorf("truncated GGET Info ring")
	}
	out.info = make([]analysisOracleInfo, infoCount)
	for i := range out.info {
		if out.info[i], err = parseAnalysisGetInfoInfo(data[pos : pos+infoBytes]); err != nil {
			return out, fmt.Errorf("GGET Info[%d]: %w", i, err)
		}
		pos += infoBytes
	}
	if pos != len(data) {
		return out, fmt.Errorf("GGET has %d trailing bytes", len(data)-pos)
	}
	if err := validateAnalysisGetInfoTrace(out); err != nil {
		return out, err
	}
	return out, nil
}

func parseAnalysisGetInfoInfo(data []byte) (analysisOracleInfo, error) {
	if len(data) != 12*4+20 {
		return analysisOracleInfo{}, fmt.Errorf("AnalysisInfo bytes=%d want %d", len(data), 12*4+20)
	}
	if binary.LittleEndian.Uint32(data[44:48]) != 0 || data[67] != 0 {
		return analysisOracleInfo{}, fmt.Errorf("nonzero reserved field or leak_boost padding")
	}
	info := readAnalysisGetInfoInfo(data)
	for _, bits := range [...]uint32{
		info.tonality, info.tonalitySlope, info.noisiness, info.activity,
		info.musicProb, info.musicMin, info.musicMax, info.activityProb, info.maxPitchRatio,
	} {
		if bits&0x7f800000 == 0x7f800000 {
			return analysisOracleInfo{}, fmt.Errorf("non-finite AnalysisInfo float %08x", bits)
		}
	}
	return info, nil
}

func validateAnalysisGetInfoTrace(trace analysisGetInfoTrace) error {
	meta := trace.meta
	if !analysisGetInfoTraceFixtureShape(trace) {
		return fmt.Errorf("unsupported strict GGET fixture/frame fs=%d channels=%d frame_size=%d frames=%d lsb=%d frame=%d",
			trace.fs, trace.channels, trace.frameSize, trace.frames, trace.lsbDepth, trace.frame)
	}
	if meta[0] >= DetectSize || meta[1] >= 8 || meta[4] != meta[0] || meta[5] != meta[1] ||
		meta[2] >= DetectSize || meta[3] >= 8 || meta[6] != meta[2] || meta[7] != meta[3] {
		return fmt.Errorf("GGET cursor restore/replay transition is inconsistent: %v", meta[:8])
	}
	if meta[8] >= DetectSize || meta[9] == 0 || meta[12] != 1 || meta[13] != 1 || meta[14] != 1 || meta[15] != 1 {
		return fmt.Errorf("GGET write/count/call/match/source-shape metadata is invalid: %v", meta[8:])
	}
	if len(trace.info) != DetectSize {
		return fmt.Errorf("GGET ring entries=%d want %d", len(trace.info), DetectSize)
	}
	validCount := 0
	for _, entry := range trace.info {
		if entry.valid != 0 {
			validCount++
		}
	}
	if validCount == 0 || validCount > int(meta[9]) {
		return fmt.Errorf("GGET valid Info ring entries=%d inconsistent with count=%d", validCount, meta[9])
	}
	for i, entry := range trace.info {
		if entry.valid > 1 {
			return fmt.Errorf("GGET Info[%d] validity flag=%d", i, entry.valid)
		}
	}
	if diff := diffAnalysisInfo(trace.linkedReturn, trace.replayReturn); diff != "" {
		return fmt.Errorf("GGET linked/replay return mismatch: %s", diff)
	}
	selected := int(meta[0])
	if trace.frameSize > trace.fs/50 && selected != int(meta[8]) {
		selected++
		if selected == DetectSize {
			selected = 0
		}
	}
	if selected == int(meta[8]) {
		selected--
	}
	if selected < 0 {
		selected = DetectSize - 1
	}
	if diff := diffAnalysisGetInfoRingHead(trace.linkedReturn, trace.info[selected]); diff != "" {
		return fmt.Errorf("GGET linked return/Info[%d] raw-field mismatch outside music postprocessing: %s", selected, diff)
	}
	return nil
}

func analysisGetInfoTraceFixtureShape(trace analysisGetInfoTrace) bool {
	switch {
	case trace.fs == 48000 && trace.channels == 1 && trace.frameSize == 2880 &&
		trace.frames == analysisGetInfoTraceFrames && trace.lsbDepth == 24:
		return trace.frame == 0
	case trace.fs == 16000 && trace.channels == 2 && trace.frameSize == 320 &&
		trace.frames == 60 && trace.lsbDepth == 24:
		return trace.frame == 0 || trace.frame == 2
	default:
		return false
	}
}

func diffAnalysisGetInfoRingHead(returned, ring analysisOracleInfo) string {
	// src/analysis.c:tonality_get_info copies tonal->info[pos] to info_out,
	// then postprocesses tonality, bandwidth, music_prob and its thresholds on
	// that copy. The ring retains the per-chunk values, so compare the rest.
	returned.tonality = ring.tonality
	returned.musicProb = ring.musicProb
	returned.musicMin = ring.musicMin
	returned.musicMax = ring.musicMax
	returned.bandwidth = ring.bandwidth
	return diffAnalysisInfo(returned, ring)
}

func TestParseAnalysisGetInfoTraceRejectsMalformed(t *testing.T) {
	valid := validAnalysisGetInfoTraceWireForTesting()
	if _, err := parseAnalysisGetInfoTrace(valid); err != nil {
		t.Fatalf("valid GGET fixture rejected: %v", err)
	}
	mutations := []struct {
		name string
		edit func([]byte) []byte
	}{
		{name: "short header", edit: func(data []byte) []byte { return data[:3] }},
		{name: "version", edit: func(data []byte) []byte { putAnalysisGetInfoU32(data, 4, 2); return data }},
		{name: "frame", edit: func(data []byte) []byte { putAnalysisGetInfoU32(data, 8, 1); return data }},
		{name: "source hash", edit: func(data []byte) []byte { data[36] = 'g'; return data }},
		{name: "metadata count", edit: func(data []byte) []byte { putAnalysisGetInfoU32(data, 100, analysisGetInfoTraceMetaLen-1); return data }},
		{name: "truncated metadata", edit: func(data []byte) []byte { return data[:len(data)-1] }},
		{name: "restored cursor", edit: func(data []byte) []byte { putAnalysisGetInfoU32(data, 104+4*4, 1); return data }},
		{name: "call count", edit: func(data []byte) []byte { putAnalysisGetInfoU32(data, 104+4*12, 2); return data }},
		{name: "Info ring count", edit: func(data []byte) []byte {
			putAnalysisGetInfoU32(data, analysisGetInfoTraceInfoCountOffset(), DetectSize-1)
			return data
		}},
		{name: "invalid ring validity flag", edit: func(data []byte) []byte {
			putAnalysisGetInfoU32(data, analysisGetInfoTraceInfoRingOffset()+68, 2)
			return data
		}},
		{name: "NaN return", edit: func(data []byte) []byte {
			putAnalysisGetInfoU32(data, 104+4*analysisGetInfoTraceMetaLen+4, 0x7fc00000)
			return data
		}},
		{name: "infinite return", edit: func(data []byte) []byte {
			putAnalysisGetInfoU32(data, 104+4*analysisGetInfoTraceMetaLen+4, 0x7f800000)
			return data
		}},
		{name: "trailing byte", edit: func(data []byte) []byte { return append(data, 0) }},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			data := append([]byte(nil), valid...)
			data = tc.edit(data)
			if _, err := parseAnalysisGetInfoTrace(data); err == nil {
				t.Fatal("malformed GGET accepted")
			}
		})
	}
}

func validAnalysisGetInfoTraceWireForTesting() []byte {
	const infoBytes = 12*4 + 20
	data := make([]byte, 0, 4+8*4+64+4+analysisGetInfoTraceMetaLen*4+2*infoBytes+4+DetectSize*infoBytes)
	data = append(data, "GGET"...)
	for _, v := range [...]uint32{1, 0, 20, 48000, 1, 2880, 24, 64} {
		data = appendAnalysisGetInfoU32(data, v)
	}
	data = append(data, []byte(strings.Repeat("a", 64))...)
	data = appendAnalysisGetInfoU32(data, analysisGetInfoTraceMetaLen)
	for _, v := range [...]uint32{0, 0, 3, 0, 0, 0, 3, 0, 3, 3, 0, 0, 1, 1, 1, 1} {
		data = appendAnalysisGetInfoU32(data, v)
	}
	validInfo := make([]byte, infoBytes)
	validInfo[0] = 1
	data = append(data, validInfo...)
	data = append(data, validInfo...)
	data = appendAnalysisGetInfoU32(data, DetectSize)
	data = append(data, validInfo...)
	data = append(data, validInfo...)
	data = append(data, validInfo...)
	data = append(data, make([]byte, (DetectSize-3)*infoBytes)...)
	return data
}

func analysisGetInfoTraceInfoCountOffset() int {
	return 4 + 8*4 + 64 + 4 + analysisGetInfoTraceMetaLen*4 + 2*(12*4+20)
}

func analysisGetInfoTraceInfoRingOffset() int {
	return analysisGetInfoTraceInfoCountOffset() + 4
}

func appendAnalysisGetInfoU32(data []byte, value uint32) []byte {
	var word [4]byte
	binary.LittleEndian.PutUint32(word[:], value)
	return append(data, word[:]...)
}

func putAnalysisGetInfoU32(data []byte, offset int, value uint32) {
	binary.LittleEndian.PutUint32(data[offset:offset+4], value)
}

const libopusAnalysisSourceSHA256 = "d2442fc330fc2576d30df080d68be4aca8803df15edcfcfa48078c13ebbc3ff8"
const libopusAnalysisInfoSourceSHA256 = "68a03d38b330425339597d56b992022d0f21c2b12799aa1853ee4ae93376cd6f"

var libopusAnalysisStageHelpers [12]libopustest.HelperCache

type libopusAnalysisStageTrace struct {
	frame, runCalls, tonalityCalls, overflow, stageMask uint32
	sourceHash                                          string
	metadata                                            [21]uint32
	inmem, fftInput, fftOutput                          []uint32
	downmixState                                        [3]uint32
	hpEnergyAccum                                       uint32
}

type libopusAnalysisPhaseRecord struct {
	bin                                   uint32
	x1r, x1i, x2r, x2i, angle, angle2     uint32
	angleState, dAngleState, d2AngleState uint32
	avgMod, rawTonality, tonality2        uint32
	noisiness                             uint32
}

type libopusAnalysisPhaseTrace struct {
	frame, totalCalls, storedCalls, overflow uint32
	records                                  []libopusAnalysisPhaseRecord
}

func TestAnalysisInputFFTStageTrace(t *testing.T) {
	libopustest.RequireOracle(t)
	requireCELTTraceV3(t, "analysis input/FFT stage trace")
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
	samples = quantizeCELTTracePCM(samples)
	payload := libopustest.NewOraclePayloadVersion("GANI", 1,
		uint32(fs), uint32(channels), uint32(frameSize), uint32(frames), uint32(lsbDepth),
		0, ^uint32(1), 0, uint32(len(samples)),
	)
	payload.Float32s(samples...)
	input := payload.Bytes()

	baselinePath, err := libopusAnalysisHelper.Path(buildLibopusAnalysisHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "baseline tonality analysis", err)
	}
	baseline, err := libopustest.RunHelper(baselinePath, input)
	if err != nil {
		libopustest.HelperUnavailable(t, "baseline tonality analysis", err)
	}
	if err := validateAnalysisGANO(baseline, frames); err != nil {
		t.Fatalf("baseline GANO structure: %v", err)
	}
	tracePath, sourceHash := buildLibopusAnalysisStageTraceHelper(t, 0)
	traced, err := libopustest.RunHelper(tracePath, input)
	if err != nil {
		libopustest.HelperUnavailable(t, "analysis input/FFT stage trace", err)
	}
	if len(traced) < len(baseline) || !bytes.Equal(traced[:len(baseline)], baseline) {
		t.Fatal("GAST source-instrumented analysis helper changed the baseline GANO output")
	}
	trace, phaseTrace, err := parseLibopusAnalysisStageTrace(traced[len(baseline):], 0)
	if err != nil {
		t.Fatalf("parse GAST: %v", err)
	}
	if trace.frame != 0 || trace.runCalls != frames || trace.tonalityCalls != 1 || trace.overflow != 0 || trace.stageMask != 15 {
		t.Fatalf("GAST capture metadata frame=%d run=%d tonality=%d overflow=%d stages=%04b",
			trace.frame, trace.runCalls, trace.tonalityCalls, trace.overflow, trace.stageMask)
	}
	if trace.sourceHash != sourceHash {
		t.Fatalf("GAST copied analysis.c hash=%s want %s", trace.sourceHash, sourceHash)
	}
	wantMetadata := [21]uint32{
		frameSize, frameSize, fs, 0, ^uint32(1), channels, lsbDepth,
		fs, frameSize / 2, 0, 0, ^uint32(1), channels, lsbDepth,
		240, 240, 480, 480, 480, 3, 1,
	}
	if trace.metadata != wantMetadata {
		t.Fatalf("GAST source geometry=%v want %v", trace.metadata, wantMetadata)
	}
	if phaseTrace.frame != trace.frame || phaseTrace.totalCalls != 239 ||
		phaseTrace.storedCalls != 239 || phaseTrace.overflow != 0 || len(phaseTrace.records) != 239 {
		t.Fatalf("GAPH metadata frame=%d total=%d stored=%d overflow=%d records=%d",
			phaseTrace.frame, phaseTrace.totalCalls, phaseTrace.storedCalls, phaseTrace.overflow, len(phaseTrace.records))
	}

	plainState := NewTonalityAnalysisState(fs)
	plainState.SetLSBDepth(lsbDepth)
	state := NewTonalityAnalysisState(fs)
	state.SetLSBDepth(lsbDepth)
	var goMetrics [239]analysisPerBinTraceSnapshot
	var goMetricCalls int
	oldPerBinHook := analysisPerBinTraceHook
	t.Cleanup(func() { analysisPerBinTraceHook = oldPerBinHook })
	analysisPerBinTraceHook = nil
	framePCM := samples[:frameSize*channels]
	plainInfo := plainState.RunAnalysis(framePCM, frameSize, channels)
	analysisPerBinTraceHook = func(snapshot analysisPerBinTraceSnapshot) {
		if snapshot.Bin != int32(goMetricCalls+1) || goMetricCalls >= len(goMetrics) {
			goMetricCalls = len(goMetrics) + 1
			return
		}
		goMetrics[goMetricCalls] = snapshot
		goMetricCalls++
	}
	tracedInfo := state.RunAnalysis(framePCM, frameSize, channels)
	compareAnalysisPerBinGoTransparency(t, 0, tracedInfo, plainInfo, state, plainState)
	var frame0InMem [480]float32
	copy(frame0InMem[:], state.InMem[240:720])
	frame0FFTInput := state.scratchFFTIn
	frame0FFTOutput := analysisFFTOutAsComplex(state.scratchFFTOut[:])
	frame0Angle := state.Angle
	frame0DAngle := state.DAngle
	frame0D2Angle := state.D2Angle
	frame0DownmixState := state.DownmixState
	frame0HPEnerAccum := state.HPEnerAccum
	frame0MemFill := state.MemFill
	analysisPerBinTraceHook = nil
	for frame := 1; frame < frames; frame++ {
		start := frame * frameSize * channels
		framePCM = samples[start : start+frameSize*channels]
		plainInfo = plainState.RunAnalysis(framePCM, frameSize, channels)
		tracedInfo = state.RunAnalysis(framePCM, frameSize, channels)
		compareAnalysisPerBinGoTransparency(t, frame, tracedInfo, plainInfo, state, plainState)
	}
	if goMetricCalls != len(goMetrics) {
		t.Fatalf("Go per-bin metric trace calls=%d want %d", goMetricCalls, len(goMetrics))
	}
	if frame0MemFill != 240 {
		t.Fatalf("Go frame-0 post-run mem_fill=%d want 240", frame0MemFill)
	}
	compareAnalysisF32Bits(t, "frame-0 downmix/resampler output ring", trace.inmem, frame0InMem[:])
	compareAnalysisComplexBits(t, "frame-0 windowed FFT input", trace.fftInput, frame0FFTInput[:])
	compareAnalysisComplexBits(t, "frame-0 FFT output", trace.fftOutput, frame0FFTOutput[:])
	compareAnalysisPhaseInputs(t, phaseTrace, frame0FFTOutput[:])
	compareAnalysisPhaseState(t, phaseTrace, frame0Angle, frame0DAngle, frame0D2Angle)
	compareAnalysisPerBinMetrics(t, phaseTrace, goMetrics[:])
	compareAnalysisF32Bits(t, "frame-0 downmix state", trace.downmixState[:], frame0DownmixState[:])
	if got := math.Float32bits(frame0HPEnerAccum); got != trace.hpEnergyAccum {
		t.Fatalf("post-run high-pass energy Go=%08x C=%08x", got, trace.hpEnergyAccum)
	} else {
		t.Logf("post-run high-pass energy matches: %08x", got)
	}
}

func compareAnalysisPerBinGoTransparency(t *testing.T, frame int, tracedInfo, plainInfo AnalysisInfo, traced, plain *TonalityAnalysisState) {
	t.Helper()
	if difference := diffAnalysisInfo(analysisInfoToOracle(tracedInfo), analysisInfoToOracle(plainInfo)); difference != "" {
		t.Fatalf("Go per-bin trace hook changed frame-%d AnalysisInfo: %s", frame, difference)
	}
	if analysisStateScalars(traced) != analysisStateScalars(plain) ||
		analysisStateHashes(traced) != analysisStateHashes(plain) || traced.Info != plain.Info {
		t.Fatalf("Go per-bin trace hook changed frame-%d analyzer scalar/state hashes or info ring", frame)
	}
}

func buildLibopusAnalysisStageTraceHelper(t *testing.T, targetFrame uint32) (string, string) {
	t.Helper()
	if targetFrame >= uint32(len(libopusAnalysisStageHelpers)) {
		t.Fatalf("analysis stage trace target frame=%d outside helper cache range [0,%d)", targetFrame, len(libopusAnalysisStageHelpers))
	}
	path, err := libopusAnalysisStageHelpers[targetFrame].Path(func() (string, error) {
		root := celtQuantTraceRepoRoot(t)
		pinnedPath := libopustest.RefPath("src", "analysis.c")
		pinned, err := os.ReadFile(pinnedPath)
		if err != nil {
			return "", fmt.Errorf("read pinned analysis.c: %w", err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(pinned)); got != libopusAnalysisSourceSHA256 {
			return "", fmt.Errorf("pinned analysis.c SHA256=%s want %s", got, libopusAnalysisSourceSHA256)
		}
		instrumented, err := instrumentLibopusAnalysisSource(string(pinned))
		if err != nil {
			return "", err
		}
		traceHash := fmt.Sprintf("%x", sha256.Sum256([]byte(instrumented)))
		copyDir := t.TempDir()
		analysisCopy := filepath.Join(copyDir, "analysis_gast.c")
		if err := os.WriteFile(analysisCopy, []byte(instrumented), 0o600); err != nil {
			return "", fmt.Errorf("write instrumented analysis.c: %w", err)
		}
		csrc := filepath.Join(root, "tools", "csrc")
		analysisInfoPath := filepath.Join(csrc, "libopus_analysis_info.c")
		analysisInfoSource, err := os.ReadFile(analysisInfoPath)
		if err != nil {
			return "", fmt.Errorf("read GANI main source: %w", err)
		}
		analysisInfoHash := fmt.Sprintf("%x", sha256.Sum256(analysisInfoSource))
		if analysisInfoHash != libopusAnalysisInfoSourceSHA256 {
			return "", fmt.Errorf("GANI main source SHA256=%s want %s", analysisInfoHash, libopusAnalysisInfoSourceSHA256)
		}
		stageHeader, err := os.ReadFile(filepath.Join(csrc, "libopus_analysis_stage_trace.h"))
		if err != nil {
			return "", fmt.Errorf("read GAST hook header: %w", err)
		}
		stageHeaderHash := fmt.Sprintf("%x", sha256.Sum256(stageHeader))
		config := libopustest.CHelperConfig{
			Label:      "libopus analysis input/FFT stage trace",
			OutputBase: fmt.Sprintf("gopus_libopus_analysis_stage_trace_f%d", targetFrame),
			SourceFile: filepath.Join(csrc, "libopus_analysis_stage_trace_main.c"),
			CFlags: []string{
				"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG",
				fmt.Sprintf("-DGOPUS_ANALYSIS_STAGE_TRACE_FRAME=%d", targetFrame),
				fmt.Sprintf("-DGOPUS_ANALYSIS_STAGE_SOURCE_SHA256=%q", traceHash),
				fmt.Sprintf("-DGOPUS_ANALYSIS_INFO_SOURCE_SHA256=%q", analysisInfoHash),
				fmt.Sprintf("-DGOPUS_ANALYSIS_STAGE_HEADER_SHA256=%q", stageHeaderHash),
			},
			RefIncludes: []string{"include", "src", "celt", "silk", "silk/float"},
			IncludeDirs: []string{csrc},
			Sources: []string{
				filepath.Join(csrc, "libopus_analysis_stage_trace.c"),
				analysisCopy,
			},
			Libs:      []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
			DeadStrip: true,
		}
		return libopustest.BuildCHelper(config)
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "libopus analysis input/FFT stage trace", err)
	}
	pinned, err := os.ReadFile(libopustest.RefPath("src", "analysis.c"))
	if err != nil {
		t.Fatalf("read pinned analysis.c for trace hash: %v", err)
	}
	instrumented, err := instrumentLibopusAnalysisSource(string(pinned))
	if err != nil {
		t.Fatalf("instrument pinned analysis.c for trace hash: %v", err)
	}
	return path, fmt.Sprintf("%x", sha256.Sum256([]byte(instrumented)))
}

func instrumentLibopusAnalysisSource(source string) (string, error) {
	const includeAnchor = "#include \"analysis.h\"\n"
	var err error
	source, err = replaceAnalysisTraceAnchor(source, includeAnchor,
		includeAnchor+"#include \"libopus_analysis_stage_trace.h\"\n", "analysis trace header")
	if err != nil {
		return "", err
	}

	const runBeginAnchor = `   int pcm_len;

   analysis_frame_size -= analysis_frame_size&1;`
	const runBeginReplacement = `   int pcm_len;

   gopus_analysis_stage_run_begin(analysis_frame_size, frame_size, c1, c2, C,
                                  Fs, lsb_depth);
   analysis_frame_size -= analysis_frame_size&1;`
	source, err = replaceAnalysisTraceAnchor(source, runBeginAnchor, runBeginReplacement, "run_analysis entry")
	if err != nil {
		return "", err
	}

	const tonalityBeginAnchor = `    } else if (tonal->Fs == 16000) {
       len = 3*len/2;
       offset = 3*offset/2;
    }

    kfft = celt_mode->mdct.kfft[0];`
	const tonalityBeginReplacement = `    } else if (tonal->Fs == 16000) {
       len = 3*len/2;
       offset = 3*offset/2;
    }
    gopus_analysis_stage_tonality_begin(tonal->Fs, len, offset, c1, c2, C,
                                        lsb_depth, tonal->mem_fill);

    kfft = celt_mode->mdct.kfft[0];`
	source, err = replaceAnalysisTraceAnchor(source, tonalityBeginAnchor, tonalityBeginReplacement, "tonality analysis entry")
	if err != nil {
		return "", err
	}

	const inmemAnchor = `   tonal->hp_ener_accum += (float)downmix_and_resample(downmix, x,
          &tonal->inmem[tonal->mem_fill], tonal->downmix_state,
          IMIN(len, ANALYSIS_BUF_SIZE-tonal->mem_fill), offset, c1, c2, C, tonal->Fs);`
	const inmemReplacement = inmemAnchor + `
    gopus_analysis_stage_capture_inmem(&tonal->inmem[240], 240, 480);`
	source, err = replaceAnalysisTraceAnchor(source, inmemAnchor, inmemReplacement, "pre-window resampler output")
	if err != nil {
		return "", err
	}

	const fftInputAnchor = `    }
    OPUS_MOVE(tonal->inmem, tonal->inmem+ANALYSIS_BUF_SIZE-240, 240);`
	const fftInputReplacement = `    }
    gopus_analysis_stage_capture_fft_input(in, 480);
    OPUS_MOVE(tonal->inmem, tonal->inmem+ANALYSIS_BUF_SIZE-240, 240);`
	source, err = replaceAnalysisTraceAnchor(source, fftInputAnchor, fftInputReplacement, "windowed FFT input")
	if err != nil {
		return "", err
	}

	const fftOutputAnchor = "    opus_fft(kfft, in, out, tonal->arch);"
	source, err = replaceAnalysisTraceAnchor(source, fftOutputAnchor,
		fftOutputAnchor+"\n    gopus_analysis_stage_capture_fft_output(out, 480);", "FFT output")
	if err != nil {
		return "", err
	}

	const phaseAnchor = `       A[i] = angle2;
       dA[i] = d_angle2;
       d2A[i] = mod2;`
	const phaseReplacement = phaseAnchor + `
       gopus_analysis_stage_capture_phase(i, X1r, X1i, X2r, X2i,
                                          angle, angle2, A[i], dA[i], d2A[i],
                                          avg_mod, tonality[i], tonality2[i],
                                          noisiness[i]);`
	source, err = replaceAnalysisTraceAnchor(source, phaseAnchor, phaseReplacement, "phase analysis output")
	if err != nil {
		return "", err
	}

	const runEndAnchor = "   tonality_get_info(analysis, analysis_info, frame_size);"
	source, err = replaceAnalysisTraceAnchor(source, runEndAnchor,
		runEndAnchor+"\n   gopus_analysis_stage_capture_post_run(analysis->downmix_state, analysis->hp_ener_accum);",
		"run_analysis post-state")
	if err != nil {
		return "", err
	}
	return source, nil
}

func validateAnalysisGANO(data []byte, frames int) error {
	const infoBytes = 12*4 + 20
	const recordBytes = 2*infoBytes + 24*4
	wantBytes := 12 + frames*recordBytes
	if len(data) != wantBytes {
		return fmt.Errorf("byte length=%d want %d", len(data), wantBytes)
	}
	if string(data[:4]) != "GANO" || binary.LittleEndian.Uint32(data[4:8]) != 1 ||
		binary.LittleEndian.Uint32(data[8:12]) != uint32(frames) {
		return fmt.Errorf("header magic/version/frame count invalid")
	}
	return nil
}

func replaceAnalysisTraceAnchor(source, anchor, replacement, label string) (string, error) {
	if count := strings.Count(source, anchor); count != 1 {
		return "", fmt.Errorf("pinned analysis.c %s anchor count=%d want 1", label, count)
	}
	return strings.Replace(source, anchor, replacement, 1), nil
}

func parseLibopusAnalysisStageTrace(data []byte, expectedTargetFrame uint32) (libopusAnalysisStageTrace, libopusAnalysisPhaseTrace, error) {
	var trace libopusAnalysisStageTrace
	var phase libopusAnalysisPhaseTrace
	const (
		fixedHeaderBytes = 7*4 + 64 + 21*4
		floatArrayWords  = 480 + 2*960 + 3 + 1
		phaseBins        = 239
		phaseWords       = 14
	)
	gastBytes := 4 + fixedHeaderBytes + 4*floatArrayWords
	gaphBytes := 4 + 5*4 + phaseBins*phaseWords*4
	if len(data) != gastBytes+gaphBytes {
		return trace, phase, fmt.Errorf("GAST/GAPH byte length=%d want %d", len(data), gastBytes+gaphBytes)
	}
	if string(data[:4]) != "GAST" {
		return trace, phase, fmt.Errorf("missing GAST magic")
	}
	offset := 4
	readU32 := func() uint32 {
		value := binary.LittleEndian.Uint32(data[offset:])
		offset += 4
		return value
	}
	version := readU32()
	if version != 1 {
		return trace, phase, fmt.Errorf("GAST version=%d want 1", version)
	}
	trace.frame = readU32()
	if trace.frame != expectedTargetFrame {
		return trace, phase, fmt.Errorf("GAST selected frame=%d want %d", trace.frame, expectedTargetFrame)
	}
	trace.runCalls = readU32()
	trace.tonalityCalls = readU32()
	trace.overflow = readU32()
	trace.stageMask = readU32()
	hashLen := readU32()
	if hashLen != 64 || offset+int(hashLen) > gastBytes {
		return trace, phase, fmt.Errorf("GAST source hash length=%d want 64", hashLen)
	}
	trace.sourceHash = string(data[offset : offset+int(hashLen)])
	if _, err := hex.DecodeString(trace.sourceHash); err != nil {
		return trace, phase, fmt.Errorf("GAST source hash is malformed: %w", err)
	}
	offset += int(hashLen)
	for i := range trace.metadata {
		trace.metadata[i] = readU32()
	}
	readFloatBits := func(count int) []uint32 {
		out := make([]uint32, count)
		for i := range out {
			out[i] = readU32()
		}
		return out
	}
	trace.inmem = readFloatBits(480)
	trace.fftInput = readFloatBits(960)
	trace.fftOutput = readFloatBits(960)
	state := readFloatBits(3)
	copy(trace.downmixState[:], state)
	trace.hpEnergyAccum = readU32()
	if offset != gastBytes {
		return trace, phase, fmt.Errorf("GAST parser consumed %d bytes want %d", offset, gastBytes)
	}
	phase, err := parseLibopusAnalysisPhaseTrace(data[gastBytes:], expectedTargetFrame)
	if err != nil {
		return trace, phase, err
	}
	return trace, phase, nil
}

func parseLibopusAnalysisPhaseTrace(data []byte, expectedTargetFrame uint32) (libopusAnalysisPhaseTrace, error) {
	var phase libopusAnalysisPhaseTrace
	const (
		phaseBins   = 239
		phaseWords  = 14
		headerBytes = 4 + 5*4
	)
	wantBytes := headerBytes + phaseBins*phaseWords*4
	if len(data) != wantBytes {
		return phase, fmt.Errorf("GAPH byte length=%d want %d", len(data), wantBytes)
	}
	if string(data[:4]) != "GAPH" {
		return phase, fmt.Errorf("missing GAPH magic")
	}
	offset := 4
	readU32 := func() uint32 {
		value := binary.LittleEndian.Uint32(data[offset:])
		offset += 4
		return value
	}
	version := readU32()
	if version != 2 {
		return phase, fmt.Errorf("GAPH version=%d want 2", version)
	}
	phase.frame = readU32()
	phase.totalCalls = readU32()
	phase.storedCalls = readU32()
	phase.overflow = readU32()
	if phase.frame != expectedTargetFrame || phase.totalCalls != phaseBins || phase.storedCalls != phaseBins || phase.overflow != 0 {
		return phase, fmt.Errorf("GAPH frame=%d want %d calls=%d stored=%d overflow=%d", phase.frame,
			expectedTargetFrame, phase.totalCalls, phase.storedCalls, phase.overflow)
	}
	phase.records = make([]libopusAnalysisPhaseRecord, phaseBins)
	readFloatBits := func() (uint32, error) {
		bits := readU32()
		value := math.Float32frombits(bits)
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return bits, fmt.Errorf("GAPH contains non-finite float at byte %d", offset-4)
		}
		return bits, nil
	}
	for i := range phase.records {
		record := &phase.records[i]
		record.bin = readU32()
		if record.bin != uint32(i+1) {
			return phase, fmt.Errorf("GAPH record %d bin=%d want %d", i, record.bin, i+1)
		}
		fields := []*uint32{
			&record.x1r, &record.x1i, &record.x2r, &record.x2i, &record.angle, &record.angle2,
			&record.angleState, &record.dAngleState, &record.d2AngleState,
			&record.avgMod, &record.rawTonality, &record.tonality2, &record.noisiness,
		}
		for _, field := range fields {
			bits, err := readFloatBits()
			if err != nil {
				return phase, err
			}
			*field = bits
		}
		if record.angle2 != record.angleState {
			return phase, fmt.Errorf("GAPH bin %d assigned A=%08x differs from angle2=%08x", record.bin,
				record.angleState, record.angle2)
		}
	}
	if offset != len(data) {
		return phase, fmt.Errorf("GAPH parser consumed %d bytes want %d", offset, len(data))
	}
	return phase, nil
}

func analysisFFTOutAsComplex(ri []float32) []complex64 {
	out := make([]complex64, len(ri)/2)
	for i := range out {
		out[i] = complex(ri[2*i], ri[2*i+1])
	}
	return out
}

func compareAnalysisPhaseInputs(t *testing.T, trace libopusAnalysisPhaseTrace, fft []complex64) {
	t.Helper()
	if len(fft) != 480 || len(trace.records) != 239 {
		t.Fatalf("phase input dimensions FFT=%d records=%d", len(fft), len(trace.records))
	}
	for _, record := range trace.records {
		i := int(record.bin)
		lower, mirrored := fft[i], fft[480-i]
		got := [4]uint32{
			math.Float32bits(real(lower) + real(mirrored)),
			math.Float32bits(imag(lower) - imag(mirrored)),
			math.Float32bits(imag(lower) + imag(mirrored)),
			math.Float32bits(real(mirrored) - real(lower)),
		}
		want := [4]uint32{record.x1r, record.x1i, record.x2r, record.x2i}
		for field := range got {
			if got[field] != want[field] {
				t.Fatalf("phase input bin=%d field=%d GoFFT=%08x Cactual=%08x", i, field, got[field], want[field])
			}
		}
	}
	t.Logf("phase-loop inputs exact for bins 1..239, derived from the matched FFT output")
}

func compareAnalysisPhaseState(t *testing.T, trace libopusAnalysisPhaseTrace, angle, dAngle, d2Angle [240]float32) {
	t.Helper()
	fields := []struct {
		name string
		c    func(libopusAnalysisPhaseRecord) uint32
		goAt func(int) float32
	}{
		{"angle", func(r libopusAnalysisPhaseRecord) uint32 { return r.angleState }, func(i int) float32 { return angle[i] }},
		{"d_angle", func(r libopusAnalysisPhaseRecord) uint32 { return r.dAngleState }, func(i int) float32 { return dAngle[i] }},
		{"d2_angle", func(r libopusAnalysisPhaseRecord) uint32 { return r.d2AngleState }, func(i int) float32 { return d2Angle[i] }},
	}
	for _, field := range fields {
		for _, record := range trace.records {
			bin := int(record.bin)
			goBits := math.Float32bits(field.goAt(bin))
			cBits := field.c(record)
			if goBits != cBits {
				t.Fatalf("phase %s bin=%d Go=%08x C=%08x (C x1=%08x,%08x x2=%08x,%08x angle=%08x angle2=%08x)",
					field.name, bin, goBits, cBits, record.x1r, record.x1i, record.x2r, record.x2i,
					record.angle, record.angle2)
			}
		}
		t.Logf("phase %s exact for bins 1..239", field.name)
	}
}

func compareAnalysisPerBinMetrics(t *testing.T, trace libopusAnalysisPhaseTrace, goMetrics []analysisPerBinTraceSnapshot) {
	t.Helper()
	if len(trace.records) != len(goMetrics) {
		t.Fatalf("per-bin metric dimensions C=%d Go=%d", len(trace.records), len(goMetrics))
	}
	fields := []struct {
		name string
		c    func(libopusAnalysisPhaseRecord) uint32
		goAt func(analysisPerBinTraceSnapshot) float32
	}{
		{"avg_mod", func(r libopusAnalysisPhaseRecord) uint32 { return r.avgMod }, func(s analysisPerBinTraceSnapshot) float32 { return s.AvgMod }},
		{"raw_tonality", func(r libopusAnalysisPhaseRecord) uint32 { return r.rawTonality }, func(s analysisPerBinTraceSnapshot) float32 { return s.Tonality }},
		{"tonality2", func(r libopusAnalysisPhaseRecord) uint32 { return r.tonality2 }, func(s analysisPerBinTraceSnapshot) float32 { return s.Tonality2 }},
		{"noisiness", func(r libopusAnalysisPhaseRecord) uint32 { return r.noisiness }, func(s analysisPerBinTraceSnapshot) float32 { return s.Noisiness }},
	}
	for _, field := range fields {
		firstBin := -1
		var firstGo, firstC uint32
		differences := 0
		for i, record := range trace.records {
			goBits := math.Float32bits(field.goAt(goMetrics[i]))
			cBits := field.c(record)
			if goBits != cBits {
				if firstBin < 0 {
					firstBin, firstGo, firstC = int(record.bin), goBits, cBits
				}
				differences++
			}
		}
		if differences == 0 {
			t.Logf("per-bin %s exact for bins 1..239", field.name)
		} else {
			t.Fatalf("per-bin %s first difference bin=%d Go=%08x C=%08x differing bins=%d/239",
				field.name, firstBin, firstGo, firstC, differences)
		}
	}
}

func compareAnalysisF32Bits(t *testing.T, stage string, cBits []uint32, goValues []float32) {
	t.Helper()
	if len(cBits) != len(goValues) {
		t.Fatalf("%s length C=%d Go=%d", stage, len(cBits), len(goValues))
	}
	first := -1
	differences := 0
	for i, value := range goValues {
		bits := math.Float32bits(value)
		if bits != cBits[i] {
			if first < 0 {
				first = i
			}
			differences++
		}
	}
	if first >= 0 {
		t.Fatalf("%s first mismatch index=%d Go=%08x C=%08x total=%d/%d", stage, first,
			math.Float32bits(goValues[first]), cBits[first], differences, len(cBits))
	} else {
		t.Logf("%s exact (%d float32 values)", stage, len(cBits))
	}
}

func compareAnalysisComplexBits(t *testing.T, stage string, cBits []uint32, goValues []complex64) {
	t.Helper()
	if len(cBits) != 2*len(goValues) {
		t.Fatalf("%s word count C=%d Go=%d complex values", stage, len(cBits), len(goValues))
	}
	first := -1
	differences := 0
	for i, value := range goValues {
		for component, scalar := range []float32{real(value), imag(value)} {
			word := 2*i + component
			bits := math.Float32bits(scalar)
			if bits != cBits[word] {
				if first < 0 {
					first = word
				}
				differences++
			}
		}
	}
	if first >= 0 {
		t.Fatalf("%s first mismatch word=%d complex=%d component=%d Go=%08x C=%08x total=%d/%d",
			stage, first, first/2, first%2, math.Float32bits([]float32{real(goValues[first/2]), imag(goValues[first/2])}[first%2]),
			cBits[first], differences, len(cBits))
	} else {
		t.Logf("%s exact (%d complex64 values)", stage, len(goValues))
	}
}

func validAnalysisStageTraceWireFixture() []byte {
	const (
		headerWords     = 7
		sourceHashBytes = 64
		metadataWords   = 21
		floatWords      = 480 + 2*960 + 3 + 1
		phaseBins       = 239
		phaseWords      = 14
	)
	gastBytes := 4 + headerWords*4 + sourceHashBytes + metadataWords*4 + floatWords*4
	data := make([]byte, gastBytes+4+5*4+phaseBins*phaseWords*4)
	copy(data, "GAST")
	putWord := func(offset int, value uint32) {
		binary.LittleEndian.PutUint32(data[offset:], value)
	}
	putWord(4, 1)   // version
	putWord(8, 0)   // selected frame
	putWord(12, 50) // run_analysis calls
	putWord(16, 1)  // selected tonality calls
	putWord(20, 0)  // overflow
	putWord(24, 15) // captured stages
	putWord(28, sourceHashBytes)
	for i := 0; i < sourceHashBytes; i++ {
		data[32+i] = 'a'
	}
	gaph := data[gastBytes:]
	copy(gaph, "GAPH")
	putPhaseWord := func(offset int, value uint32) {
		binary.LittleEndian.PutUint32(gaph[offset:], value)
	}
	putPhaseWord(4, 2)          // version
	putPhaseWord(8, 0)          // selected frame
	putPhaseWord(12, phaseBins) // actual calls
	putPhaseWord(16, phaseBins) // stored calls
	putPhaseWord(20, 0)         // overflow
	for i := 0; i < phaseBins; i++ {
		putPhaseWord(24+i*phaseWords*4, uint32(i+1)) // ordered bin
	}
	return data
}

func TestAnalysisInputFFTStageTraceParserRejectsMalformedWire(t *testing.T) {
	valid := validAnalysisStageTraceWireFixture()
	if _, phase, err := parseLibopusAnalysisStageTrace(valid, 0); err != nil {
		t.Fatalf("valid GAST/GAPH fixture rejected: %v", err)
	} else if len(phase.records) != 239 {
		t.Fatalf("valid GAPH records=%d want 239", len(phase.records))
	}

	tests := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{
			name: "truncated payload",
			mutate: func(data []byte) []byte {
				return data[:len(data)-1]
			},
		},
		{
			name: "unsupported version",
			mutate: func(data []byte) []byte {
				binary.LittleEndian.PutUint32(data[4:], 2)
				return data
			},
		},
		{
			name: "wrong source hash length",
			mutate: func(data []byte) []byte {
				binary.LittleEndian.PutUint32(data[28:], 63)
				return data
			},
		},
		{
			name: "malformed source hash",
			mutate: func(data []byte) []byte {
				data[32] = 'z'
				return data
			},
		},
		{
			name: "trailing bytes",
			mutate: func(data []byte) []byte {
				return append(data, 0)
			},
		},
		{
			name: "wrong magic",
			mutate: func(data []byte) []byte {
				copy(data, "GANO")
				return data
			},
		},
		{
			name: "wrong phase magic",
			mutate: func(data []byte) []byte {
				copy(data[analysisGASTPayloadBytes():], "GATE")
				return data
			},
		},
		{
			name: "unsupported phase version",
			mutate: func(data []byte) []byte {
				binary.LittleEndian.PutUint32(data[analysisGASTPayloadBytes()+4:], 3)
				return data
			},
		},
		{
			name: "phase call count mismatch",
			mutate: func(data []byte) []byte {
				binary.LittleEndian.PutUint32(data[analysisGASTPayloadBytes()+12:], 238)
				return data
			},
		},
		{
			name: "phase frame mismatch",
			mutate: func(data []byte) []byte {
				binary.LittleEndian.PutUint32(data[analysisGASTPayloadBytes()+8:], 1)
				return data
			},
		},
		{
			name: "phase stored count mismatch",
			mutate: func(data []byte) []byte {
				binary.LittleEndian.PutUint32(data[analysisGASTPayloadBytes()+16:], 238)
				return data
			},
		},
		{
			name: "phase overflow",
			mutate: func(data []byte) []byte {
				binary.LittleEndian.PutUint32(data[analysisGASTPayloadBytes()+20:], 1)
				return data
			},
		},
		{
			name: "phase bin order",
			mutate: func(data []byte) []byte {
				binary.LittleEndian.PutUint32(data[analysisGASTPayloadBytes()+24:], 2)
				return data
			},
		},
		{
			name: "non-finite phase value",
			mutate: func(data []byte) []byte {
				// Row word 5 is the first scaled-angle field after the bin and four inputs.
				binary.LittleEndian.PutUint32(data[analysisGASTPayloadBytes()+24+5*4:], 0x7fc00000)
				return data
			},
		},
		{
			name: "non-finite per-bin metric",
			mutate: func(data []byte) []byte {
				// Row word 10 is avg_mod, after the bin and nine phase/state floats.
				binary.LittleEndian.PutUint32(data[analysisGASTPayloadBytes()+24+10*4:], 0x7fc00000)
				return data
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := append([]byte(nil), valid...)
			if _, _, err := parseLibopusAnalysisStageTrace(test.mutate(data), 0); err == nil {
				t.Fatal("malformed GAST payload was accepted")
			}
		})
	}
}

func analysisGASTPayloadBytes() int {
	const (
		fixedHeaderBytes = 7*4 + 64 + 21*4
		floatArrayWords  = 480 + 2*960 + 3 + 1
	)
	return 4 + fixedHeaderBytes + 4*floatArrayWords
}

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

func TestAnalysisMLPCorpusMusicTrace(t *testing.T) {
	libopustest.RequireOracle(t)
	requireCELTTraceV3(t, "analysis MLP corpus music trace")
	if !analysisMLPTraceEnabled {
		t.Fatal("analysis MLP stage trace hook is disabled in this build")
	}

	const (
		fs        = 48000
		channels  = 1
		frameSize = 960
		frames    = 60 // max(1200/20ms, 12), matching TestAnalysisMatchesLibopusLive.
		lsbDepth  = 24
	)
	samples, err := testsignal.GenerateCorpusSignal(
		testsignal.CorpusMusicV1,
		fs,
		frames*frameSize*channels,
		channels,
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
	const baselineHeaderBytes = 12
	baselineBytes := baselineHeaderBytes + frames*recordBytes
	if len(traced) < baselineBytes {
		t.Fatalf("instrumented C output has %d bytes, shorter than GANO prefix %d", len(traced), baselineBytes)
	}
	if !bytes.Equal(traced[:baselineBytes], baseline) {
		t.Fatal("instrumented C output changed ordinary 60-frame GANO result")
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
	oldHook := analysisMLPTraceHook
	t.Cleanup(func() { analysisMLPTraceHook = oldHook })
	var goTrace analysisMLPTraceSnapshot
	var frame0LatestInfo AnalysisInfo
	goCalls := 0
	traceHook := func(snapshot analysisMLPTraceSnapshot) {
		goCalls++
		goTrace = snapshot
	}
	frameSamples := frameSize * channels
	var frame0TraceInfo AnalysisInfo
	for frameIndex := 0; frameIndex < frames; frameIndex++ {
		start := frameIndex * frameSamples
		frame := samples[start : start+frameSamples]
		analysisMLPTraceHook = nil
		plainInfo := plain.RunAnalysis(frame, frameSize, channels)
		analysisMLPTraceHook = traceHook
		tracedInfo := tracedState.RunAnalysis(frame, frameSize, channels)
		analysisMLPTraceHook = nil
		compareGoAnalysisFrame(t, frameIndex, tracedInfo, plainInfo, tracedState, plain)
		if frameIndex == 0 {
			frame0TraceInfo = tracedInfo
			frame0LatestInfo = analysisLatestRawInfo(tracedState)
		}
	}
	if goCalls != 1 || goTrace.Frame != 0 || goTrace.Dense0Calls != 1 || goTrace.GRUCalls != 1 || goTrace.Dense2Calls != 1 {
		t.Fatalf("Go MLP snapshot calls=%d metadata frame=%d dense0=%d GRU=%d dense2=%d",
			goCalls, goTrace.Frame, goTrace.Dense0Calls, goTrace.GRUCalls, goTrace.Dense2Calls)
	}
	if goTrace.Dense0Output != goTrace.GRUInput || goTrace.GRUStateAfter != goTrace.Dense2Input {
		t.Fatal("Go MLP snapshots do not follow the actual dense0→GRU→dense2 chain")
	}
	if got := [2]uint32{math.Float32bits(goTrace.Dense2Output[0]), math.Float32bits(goTrace.Dense2Output[1])}; got != [2]uint32{math.Float32bits(frame0LatestInfo.MusicProb), math.Float32bits(frame0LatestInfo.VADProb)} {
		t.Fatalf("Go dense2 output %08x/%08x is not linked to frame-0 raw analyzer-ring music/VAD %08x/%08x",
			got[0], got[1], math.Float32bits(frame0LatestInfo.MusicProb), math.Float32bits(frame0LatestInfo.VADProb))
	}
	infoDifference := diffAnalysisInfo(analysisInfoToOracle(frame0TraceInfo), cFrame.ret)
	if infoDifference != "" {
		t.Logf("frame-0 postprocessed GANI returned-info difference: %s", infoDifference)
	}

	var firstDifference string
	compareAnalysisMLPArray(t, &firstDifference, "corpus dense0 features", cTrace.dense0Input[:], goTrace.Dense0Input[:])
	compareAnalysisMLPArray(t, &firstDifference, "corpus dense0 output / GRU input", cTrace.dense0Output[:], goTrace.Dense0Output[:])
	compareAnalysisMLPArray(t, &firstDifference, "corpus GRU pre-state", cTrace.gruStateBefore[:], goTrace.GRUStateBefore[:])
	compareAnalysisMLPArray(t, &firstDifference, "corpus GRU post-state", cTrace.gruStateAfter[:], goTrace.GRUStateAfter[:])
	compareAnalysisMLPArray(t, &firstDifference, "corpus dense2 output", cTrace.dense2Output[:], goTrace.Dense2Output[:])
	if firstDifference == "" {
		t.Log("corpus_music_v1 frame-0 MLP stages match bitwise")
	} else {
		t.Logf("first corpus_music_v1 frame-0 MLP difference: %s", firstDifference)
	}
}

const (
	analysisMLPGemmInputMagic  = "GMKI"
	analysisMLPGemmOutputMagic = "GMKO"
	analysisMLPSourceSHA256    = "162884422e9b91d368b695aa5f84d74f9585cbf2b13a45e6bbca65df880db06c"
)

var (
	analysisMLPGemmHelper    libopustest.HelperCache
	analysisMLPGemmAllocSink float32
)

type analysisMLPGemmCase struct {
	name     string
	features [25]float32
	state    [24]float32
}

type analysisMLPGemmResult struct {
	dense0 [32]float32
	state  [24]float32
	dense2 [2]float32
}

func TestAnalysisMLPGEMMMatchesLibopusV3(t *testing.T) {
	requireAnalysisMLPV3Target(t)
	libopustest.RequireOracle(t)
	if !analysisMLPTraceEnabled {
		t.Fatal("analysis MLP trace hook is disabled in this build")
	}

	cases := analysisMLPGemmCases(t)
	want, sourceHash, err := probeAnalysisMLPGemm(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "analysis MLP GEMM", err)
	}
	for i, tc := range cases {
		got := runAnalysisMLPGemm(tc.features, tc.state)
		compareAnalysisMLPGemmOutput(t, tc.name, "dense0", got.dense0[:], want[i].dense0[:])
		compareAnalysisMLPGemmOutput(t, tc.name, "GRU state", got.state[:], want[i].state[:])
		compareAnalysisMLPGemmOutput(t, tc.name, "dense2", got.dense2[:], want[i].dense2[:])
	}
	if t.Failed() {
		t.Fatalf("analysis MLP output differs from selected libopus src/mlp.c (%s)", sourceHash)
	}

	features := cases[1].features
	initialState := cases[1].state
	var dense0 [32]float32
	var state [24]float32
	var dense2 [2]float32
	run := func() {
		state = initialState
		layer0.ComputeDense(dense0[:], features[:])
		layer1.ComputeGRU(state[:], dense0[:])
		layer2.ComputeDense(dense2[:], state[:])
		analysisMLPGemmAllocSink = dense0[0] + state[0] + dense2[0]
	}
	run()
	if allocs := testing.AllocsPerRun(100, run); allocs != 0 {
		t.Fatalf("analysis MLP dense/GRU steady-state allocations=%g, want 0", allocs)
	}
}

func requireAnalysisMLPV3Target(t *testing.T) {
	t.Helper()
	target, err := libopustooling.ResolveLibopusAMD64Target()
	if err == nil && target == "v3" {
		return
	}
	message := "analysis MLP source oracle requires GOPUS_LIBOPUS_AMD64_TARGET=v3"
	if err != nil {
		message += ": " + err.Error()
	} else {
		message += fmt.Sprintf(", got %q", target)
	}
	if libopustest.StrictRefRequired() {
		t.Fatal(message)
	}
	t.Skip(message)
}

func analysisMLPGemmCases(t *testing.T) []analysisMLPGemmCase {
	t.Helper()
	const (
		fs        = 48000
		channels  = 2
		frameSize = 960
	)
	samples, err := testsignal.GenerateEncoderSignalVariant(
		testsignal.EncoderVariantAMMultisineV1, fs, frameSize*channels, channels,
	)
	if err != nil {
		t.Fatalf("generate actual analysis frame: %v", err)
	}
	clampToOpusDemoF32InPlace(samples)
	state := NewTonalityAnalysisState(fs)
	state.SetLSBDepth(24)
	oldHook := analysisMLPTraceHook
	t.Cleanup(func() { analysisMLPTraceHook = oldHook })
	var snapshot analysisMLPTraceSnapshot
	calls := 0
	analysisMLPTraceHook = func(got analysisMLPTraceSnapshot) {
		calls++
		snapshot = got
	}
	state.RunAnalysis(samples, frameSize, channels)
	analysisMLPTraceHook = oldHook
	if calls != 1 || snapshot.Frame != 0 || snapshot.Dense0Calls != 1 || snapshot.GRUCalls != 1 || snapshot.Dense2Calls != 1 {
		t.Fatalf("actual analysis hook calls=%d frame=%d dense0=%d GRU=%d dense2=%d", calls,
			snapshot.Frame, snapshot.Dense0Calls, snapshot.GRUCalls, snapshot.Dense2Calls)
	}
	cases := make([]analysisMLPGemmCase, 0, 9)
	cases = append(cases, analysisMLPGemmCase{
		name:     "actual-frame-0",
		features: snapshot.Dense0Input,
		state:    snapshot.GRUStateBefore,
	})
	for c := 0; c < 8; c++ {
		tc := analysisMLPGemmCase{name: fmt.Sprintf("bounded-f32-%d", c)}
		for i := range tc.features {
			tc.features[i] = float32((i*37+c*19)%127-63) * (1.0 / 32.0)
		}
		for i := range tc.state {
			tc.state[i] = float32((i*13+c*7)%101-50) * (1.0 / 64.0)
		}
		cases = append(cases, tc)
	}
	return cases
}

func runAnalysisMLPGemm(features [25]float32, initialState [24]float32) analysisMLPGemmResult {
	var got analysisMLPGemmResult
	layer0.ComputeDense(got.dense0[:], features[:])
	got.state = initialState
	layer1.ComputeGRU(got.state[:], got.dense0[:])
	layer2.ComputeDense(got.dense2[:], got.state[:])
	return got
}

func compareAnalysisMLPGemmOutput(t *testing.T, caseName, stage string, got, want []float32) {
	t.Helper()
	for i := range want {
		gotBits, wantBits := math.Float32bits(got[i]), math.Float32bits(want[i])
		if gotBits != wantBits {
			t.Errorf("%s %s[%d]=%08x want %08x", caseName, stage, i, gotBits, wantBits)
			return
		}
	}
}

func probeAnalysisMLPGemm(cases []analysisMLPGemmCase) ([]analysisMLPGemmResult, string, error) {
	binPath, sourceHash, err := buildAnalysisMLPGemmHelper()
	if err != nil {
		return nil, "", err
	}
	payload := libopustest.NewOraclePayloadVersion(analysisMLPGemmInputMagic, 1, uint32(len(cases)))
	for _, tc := range cases {
		payload.Float32s(tc.features[:]...)
		payload.Float32s(tc.state[:]...)
	}
	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(), "analysis MLP GEMM", analysisMLPGemmOutputMagic, 1)
	if err != nil {
		return nil, "", err
	}
	count := reader.Count(len(cases))
	if got := string(reader.Bytes(64)); got != sourceHash {
		return nil, "", fmt.Errorf("selected libopus src/mlp.c SHA256=%s want %s", got, sourceHash)
	}
	results := make([]analysisMLPGemmResult, count)
	for i := range results {
		for j := range results[i].dense0 {
			results[i].dense0[j] = reader.Float32()
		}
		for j := range results[i].state {
			results[i].state[j] = reader.Float32()
		}
		for j := range results[i].dense2 {
			results[i].dense2[j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, "", err
	}
	return results, sourceHash, nil
}

func buildAnalysisMLPGemmHelper() (string, string, error) {
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		return "", "", err
	}
	mlpPath := libopustest.RefPath("src", "mlp.c")
	root := filepath.Clean(filepath.Join(filepath.Dir(mlpPath), "..", "..", ".."))
	switch variant {
	case libopustooling.LibopusReferenceScalar:
		if !libopustooling.EnsureLibopusScalar(libopustooling.DefaultVersion, []string{root}) {
			return "", "", fmt.Errorf("ensure selected scalar libopus reference at %s", root)
		}
	case libopustooling.LibopusReferenceSIMD:
		if !libopustooling.EnsureLibopusSIMD(libopustooling.DefaultVersion, []string{root}) {
			return "", "", fmt.Errorf("ensure selected SIMD libopus reference at %s", root)
		}
	default:
		return "", "", fmt.Errorf("analysis MLP oracle selected unsupported reference variant %q", variant)
	}
	mlpSource, err := os.ReadFile(mlpPath)
	if err != nil {
		return "", "", fmt.Errorf("read selected libopus src/mlp.c: %w", err)
	}
	sourceHash := fmt.Sprintf("%x", sha256.Sum256(mlpSource))
	if sourceHash != analysisMLPSourceSHA256 {
		return "", "", fmt.Errorf("selected libopus src/mlp.c SHA256=%s want pinned %s", sourceHash, analysisMLPSourceSHA256)
	}
	config := libopustest.CHelperConfig{
		Label:      "libopus analysis MLP GEMM",
		OutputBase: "gopus_libopus_analysis_mlp_gemm",
		SourceFile: "libopus_analysis_mlp_gemm_kernel.c",
		CFlags: []string{
			"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG",
			fmt.Sprintf("-DGOPUS_MLP_SOURCE_SHA256=%q", sourceHash),
		},
		RefIncludes: []string{"include", "src", "celt", "silk", "silk/float"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	}
	path, err := analysisMLPGemmHelper.CHelperPath(config)
	if err != nil {
		return "", "", err
	}
	return path, sourceHash, nil
}

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

func TestParseAnalysisMLPStageTraceRejectsMalformedGAML(t *testing.T) {
	const wantDenseCalls, wantGRUCalls = 100, 50
	valid := validAnalysisMLPStageTraceWire(wantDenseCalls, wantGRUCalls)
	if _, err := parseAnalysisMLPStageTrace(valid, wantDenseCalls, wantGRUCalls); err != nil {
		t.Fatalf("parse valid GAML fixture: %v", err)
	}

	const headerBytes = 4 + 6*4 + 2*64
	const stageOneOffset = headerBytes
	const stageTwoOffset = stageOneOffset + 5*4 + (25+32)*4
	const stageThreeOffset = stageTwoOffset + 5*4 + (32+24+24)*4
	cases := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{name: "magic", mutate: func(b []byte) []byte { b[0] = 'X'; return b }},
		{name: "version", mutate: func(b []byte) []byte { putAnalysisMLPTraceU32(b, 4, 2); return b }},
		{name: "frame", mutate: func(b []byte) []byte { putAnalysisMLPTraceU32(b, 8, 1); return b }},
		{name: "dense call count", mutate: func(b []byte) []byte { putAnalysisMLPTraceU32(b, 12, 99); return b }},
		{name: "GRU call count", mutate: func(b []byte) []byte { putAnalysisMLPTraceU32(b, 16, 49); return b }},
		{name: "stage count", mutate: func(b []byte) []byte { putAnalysisMLPTraceU32(b, 20, 2); return b }},
		{name: "overflow", mutate: func(b []byte) []byte { putAnalysisMLPTraceU32(b, 24, 1); return b }},
		{name: "analysis source hash", mutate: func(b []byte) []byte { b[28] ^= 1; return b }},
		{name: "MLP source hash", mutate: func(b []byte) []byte { b[92] ^= 1; return b }},
		{name: "dense input width", mutate: func(b []byte) []byte { putAnalysisMLPTraceU32(b, stageOneOffset+4, 24); return b }},
		{name: "GRU state width", mutate: func(b []byte) []byte { putAnalysisMLPTraceU32(b, stageTwoOffset+8, 23); return b }},
		{name: "dense2 output width", mutate: func(b []byte) []byte { putAnalysisMLPTraceU32(b, stageThreeOffset+16, 3); return b }},
		{name: "stage order", mutate: func(b []byte) []byte { putAnalysisMLPTraceU32(b, stageTwoOffset, 1); return b }},
		{name: "nonfinite value", mutate: func(b []byte) []byte { putAnalysisMLPTraceU32(b, stageOneOffset+5*4, 0x7fc00000); return b }},
		{name: "truncated payload", mutate: func(b []byte) []byte { return b[:len(b)-1] }},
		{name: "trailing byte", mutate: func(b []byte) []byte { return append(b, 0) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			malformed := append([]byte(nil), valid...)
			malformed = tc.mutate(malformed)
			if _, err := parseAnalysisMLPStageTrace(malformed, wantDenseCalls, wantGRUCalls); err == nil {
				t.Fatal("malformed GAML payload was accepted")
			}
		})
	}
}

func TestAnalysisMLPStageTraceDriverAnchorsAreUnique(t *testing.T) {
	root := celtQuantTraceRepoRoot(t)
	path := filepath.Join(root, "tools", "csrc", "libopus_analysis_info.c")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pinned GANI driver: %v", err)
	}
	if got := analysisMLPStageTraceSHA256(source); got != libopusAnalysisInfoSourceSHA256 {
		t.Fatalf("GANI driver SHA256=%s want pinned %s", got, libopusAnalysisInfoSourceSHA256)
	}

	instrumented, err := instrumentAnalysisMLPStageDriver(string(source))
	if err != nil {
		t.Fatalf("instrument pinned GANI driver: %v", err)
	}
	flush := strings.Index(instrumented, "  fflush(stdout);\n")
	write := strings.Index(instrumented, "  if (!gopus_analysis_mlp_trace_write()) return 7;\n")
	cleanup := strings.Index(instrumented, "  free(pcm);\n")
	if write < 0 || flush <= write || cleanup <= flush || strings.Count(instrumented, "gopus_analysis_mlp_trace_write()") != 1 {
		t.Fatal("GAML writer must run once after GANO records and before the final flush and driver cleanup")
	}

	const includeAnchor = "#include \"modes.h\"\n"
	duplicateInclude := strings.Replace(string(source), includeAnchor, includeAnchor+includeAnchor, 1)
	if _, err := instrumentAnalysisMLPStageDriver(duplicateInclude); err == nil {
		t.Fatal("duplicated GANI declaration anchor was accepted")
	}
	const endAnchor = "  fflush(stdout);\n  free(pcm);\n  free(st);\n  return 0;"
	duplicateEnd := string(source) + "\n" + endAnchor
	if _, err := instrumentAnalysisMLPStageDriver(duplicateEnd); err == nil {
		t.Fatal("duplicated GANI trailer anchor was accepted")
	}
}

func validAnalysisMLPStageTraceWire(denseCalls, gruCalls uint32) []byte {
	data := []byte("GAML")
	for _, value := range []uint32{1, 0, denseCalls, gruCalls, 3, 0} {
		data = appendAnalysisMLPTraceU32(data, value)
	}
	data = append(data, libopusAnalysisSourceSHA256...)
	data = append(data, libopusMLPSourceSHA256...)
	data = appendAnalysisMLPTraceStage(data, 1, 25, 0, 0, 32, 25+32)
	data = appendAnalysisMLPTraceStage(data, 2, 32, 24, 24, 0, 32+24+24)
	data = appendAnalysisMLPTraceStage(data, 3, 24, 0, 0, 2, 24+2)
	return data
}

func appendAnalysisMLPTraceStage(data []byte, stage, inputs, before, after, outputs, floatCount uint32) []byte {
	for _, value := range []uint32{stage, inputs, before, after, outputs} {
		data = appendAnalysisMLPTraceU32(data, value)
	}
	for range floatCount {
		data = appendAnalysisMLPTraceU32(data, 0)
	}
	return data
}

func appendAnalysisMLPTraceU32(data []byte, value uint32) []byte {
	var word [4]byte
	binary.LittleEndian.PutUint32(word[:], value)
	return append(data, word[:]...)
}

func putAnalysisMLPTraceU32(data []byte, offset int, value uint32) {
	binary.LittleEndian.PutUint32(data[offset:offset+4], value)
}

func analysisMLPStageTraceSHA256(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func TestAnalysisNonzeroHistoryAvgModTrace(t *testing.T) {
	const (
		fs        = 48000
		channels  = 2
		frameSize = 960
		frames    = 50
		lsbDepth  = 24
		frame1    = uint32(1)
		frame2    = uint32(2)
	)
	libopustest.RequireOracle(t)
	requireCELTTraceV3(t, "analysis nonzero-history avg_mod trace")

	samples, err := testsignal.GenerateEncoderSignalVariant(
		testsignal.EncoderVariantAMMultisineV1,
		fs,
		frameSize*channels*frames,
		channels,
	)
	if err != nil {
		t.Fatalf("generate AM multisine analysis input: %v", err)
	}
	samples = quantizeCELTTracePCM(samples)
	payload := libopustest.NewOraclePayloadVersion("GANI", 1,
		fs, channels, frameSize, frames, lsbDepth,
		0, ^uint32(1), 0, uint32(len(samples)),
	)
	payload.Float32s(samples...)
	input := payload.Bytes()
	assertAnalysisAvgModInputSHA256(t, "AM multisine with quantizeCELTTracePCM", input,
		"58fad87922a34dede4b4e576be6b0bdcb414f4db8007b409e302982e4f0c396c")

	baselinePath, err := libopusAnalysisHelper.Path(buildLibopusAnalysisHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "baseline tonality analysis", err)
	}
	baseline, err := libopustest.RunHelper(baselinePath, input)
	if err != nil {
		libopustest.HelperUnavailable(t, "baseline tonality analysis", err)
	}
	if err := validateAnalysisGANO(baseline, frames); err != nil {
		t.Fatalf("baseline GANO structure: %v", err)
	}

	traces := make(map[uint32]libopusAnalysisStageTrace, 2)
	phaseTraces := make(map[uint32]libopusAnalysisPhaseTrace, 2)
	for _, targetFrame := range []uint32{frame1, frame2} {
		helperPath, sourceHash := buildLibopusAnalysisStageTraceHelper(t, targetFrame)
		traced, err := libopustest.RunHelper(helperPath, input)
		if err != nil {
			libopustest.HelperUnavailable(t, fmt.Sprintf("analysis frame-%d stage trace", targetFrame), err)
		}
		if len(traced) < len(baseline) || !bytes.Equal(traced[:len(baseline)], baseline) {
			t.Fatalf("frame-%d source-instrumented helper changed baseline GANO output", targetFrame)
		}
		trace, phase, err := parseLibopusAnalysisStageTrace(traced[len(baseline):], targetFrame)
		if err != nil {
			t.Fatalf("parse frame-%d GAST/GAPH: %v", targetFrame, err)
		}
		validateAnalysisNonzeroHistoryCapture(t, trace, phase, targetFrame, frames, sourceHash)
		traces[targetFrame] = trace
		phaseTraces[targetFrame] = phase
	}

	plainState := NewTonalityAnalysisState(fs)
	plainState.SetLSBDepth(lsbDepth)
	state := NewTonalityAnalysisState(fs)
	state.SetLSBDepth(lsbDepth)
	var goFrame1 analysisNonzeroHistoryGoFrame
	var goFrame2 analysisNonzeroHistoryGoFrame
	var goMetrics [239]analysisPerBinTraceSnapshot
	var goMetricCalls int
	var goMetricOverflow bool
	var frame2CountBefore, frame2CountAfter int32
	var plainFrame2CountBefore, plainFrame2CountAfter int32
	oldPerBinHook := analysisPerBinTraceHook
	t.Cleanup(func() { analysisPerBinTraceHook = oldPerBinHook })

	chunkSize := fs / 50
	chunkCount := (frameSize + chunkSize - 1) / chunkSize
	for frame := 0; frame < frames; frame++ {
		start := frame * frameSize * channels
		framePCM := samples[start : start+frameSize*channels]
		if frame == int(frame2) && (state.Count != int32(frame2) || plainState.Count != int32(frame2)) {
			t.Fatalf("frame-2 pre-run Count traced=%d plain=%d want %d", state.Count, plainState.Count, frame2)
		}
		if frame == int(frame2) {
			frame2CountBefore, plainFrame2CountBefore = state.Count, plainState.Count
		}

		// Keep the observer off for the plain state on every call. It is active
		// only for the selected frame-2 run on the traced state.
		analysisPerBinTraceHook = nil
		plainInfo := plainState.RunAnalysis(framePCM, frameSize, channels)
		if frame == int(frame2) {
			analysisPerBinTraceHook = func(snapshot analysisPerBinTraceSnapshot) {
				if goMetricCalls >= len(goMetrics) || snapshot.Bin != int32(goMetricCalls+1) {
					goMetricOverflow = true
					return
				}
				goMetrics[goMetricCalls] = snapshot
				goMetricCalls++
			}
		}
		tracedInfo := state.RunAnalysis(framePCM, frameSize, channels)
		analysisPerBinTraceHook = nil
		if frame == int(frame2) {
			frame2CountAfter, plainFrame2CountAfter = state.Count, plainState.Count
		}
		compareAnalysisPerBinGoTransparency(t, frame, tracedInfo, plainInfo, state, plainState)

		switch frame {
		case int(frame1):
			goFrame1 = snapshotAnalysisNonzeroHistoryGoFrame(state)
		case int(frame2):
			goFrame2 = snapshotAnalysisNonzeroHistoryGoFrame(state)
		}
	}
	if goMetricOverflow || goMetricCalls != len(goMetrics) {
		t.Fatalf("frame-2 Go per-bin metric calls=%d overflow=%t want 239/false", goMetricCalls, goMetricOverflow)
	}
	if frame2CountBefore != 2 || plainFrame2CountBefore != 2 || frame2CountAfter != 3 || plainFrame2CountAfter != 3 {
		t.Fatalf("frame-2 Count traced=%d->%d plain=%d->%d want 2->3", frame2CountBefore, frame2CountAfter,
			plainFrame2CountBefore, plainFrame2CountAfter)
	}
	if chunkCount != 1 {
		t.Fatalf("selected public frame has %d tonality chunks want 1", chunkCount)
	}

	frame1Trace, frame1Phase := traces[frame1], phaseTraces[frame1]
	compareAnalysisNonzeroHistoryFrame(t, frame1Trace, frame1Phase, goFrame1, frame1)
	frame2Trace, frame2Phase := traces[frame2], phaseTraces[frame2]
	compareAnalysisNonzeroHistoryFrame(t, frame2Trace, frame2Phase, goFrame2, frame2)

	priorNonzero := countNonzeroAnalysisPhaseField(frame1Phase, func(record libopusAnalysisPhaseRecord) uint32 {
		return record.d2AngleState
	})
	if priorNonzero == 0 {
		t.Fatal("frame-1 C phase state has no nonzero d2 history for frame 2")
	}
	cNonzeroAvgMod := countNonzeroAnalysisPhaseField(frame2Phase, func(record libopusAnalysisPhaseRecord) uint32 {
		return record.avgMod
	})
	goNonzeroAvgMod := 0
	for _, metric := range goMetrics {
		if metric.AvgMod != 0 {
			goNonzeroAvgMod++
		}
	}
	if cNonzeroAvgMod == 0 || goNonzeroAvgMod == 0 {
		t.Fatalf("frame-2 avg_mod does not exercise nonzero history: C=%d Go=%d bins", cNonzeroAvgMod, goNonzeroAvgMod)
	}
	t.Logf("frame-1 d2 history is nonzero in %d/239 bins; frame-2 avg_mod is nonzero in C=%d/239 Go=%d/239 bins",
		priorNonzero, cNonzeroAvgMod, goNonzeroAvgMod)
	logAnalysisNonzeroHistoryMetricDifferences(t, frame2Phase, goMetrics[:], frame2)
	logAnalysisNonzeroHistoryAvgModBin40(t, frame1Phase.records[39], frame2Phase.records[39], goMetrics[39])
}

func TestAnalysisNonzeroHistoryAvgModMixed5msTrace(t *testing.T) {
	runAnalysisNonzeroHistoryAvgModTrace(t, testsignal.CorpusMixedV1,
		48000, 2, 240, 240, 11, 24, false,
		"c9a3382a2990a523ec7570337e5165b0704b39a9f3ea2b51feb8f20e2a715603")
}

func TestAnalysisNonzeroHistoryAvgModCastanet16kTrace(t *testing.T) {
	runAnalysisNonzeroHistoryAvgModTrace(t, testsignal.CorpusCastanetTransientV1,
		16000, 1, 320, 60, 2, 24, false,
		"c7006d837316da4decefd30b00c7bb5e2c64d5ad70cff724662f79b1caf6054b")
}

func runAnalysisNonzeroHistoryAvgModTrace(t *testing.T, class string, fs, channels, frameSize, frames, targetFrame, lsbDepth int, clamp bool, wantInputSHA256 string) {
	t.Helper()
	libopustest.RequireOracle(t)
	requireCELTTraceV3(t, "nonzero-history avg_mod trace")

	samples, err := testsignal.GenerateCorpusSignal(class,
		fs, frames*frameSize*channels, channels)
	if err != nil {
		t.Fatalf("generate %s analysis input: %v", class, err)
	}
	if clamp {
		clampToOpusDemoF32InPlace(samples)
	}
	payload := libopustest.NewOraclePayloadVersion("GANI", 1,
		uint32(fs), uint32(channels), uint32(frameSize), uint32(frames), uint32(lsbDepth),
		0, ^uint32(1), 0, uint32(len(samples)))
	payload.Float32s(samples...)
	input := payload.Bytes()
	assertAnalysisAvgModInputSHA256(t, fmt.Sprintf("%s clamp=%t", class, clamp), input, wantInputSHA256)

	baselinePath, err := libopusAnalysisHelper.Path(buildLibopusAnalysisHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "baseline tonality analysis", err)
	}
	baseline, err := libopustest.RunHelper(baselinePath, input)
	if err != nil {
		libopustest.HelperUnavailable(t, "baseline tonality analysis", err)
	}
	if err := validateAnalysisGANO(baseline, frames); err != nil {
		t.Fatalf("baseline GANO structure: %v", err)
	}
	tracePath, sourceHash := buildLibopusAnalysisStageTraceHelper(t, uint32(targetFrame))
	traced, err := libopustest.RunHelper(tracePath, input)
	if err != nil {
		libopustest.HelperUnavailable(t, "analysis phase trace", err)
	}
	if len(traced) < len(baseline) || !bytes.Equal(traced[:len(baseline)], baseline) {
		t.Fatal("source-instrumented analysis helper changed baseline GANO output")
	}
	trace, phaseTrace, err := parseLibopusAnalysisStageTrace(traced[len(baseline):], uint32(targetFrame))
	if err != nil {
		t.Fatalf("parse GAST/GAPH: %v", err)
	}
	if trace.frame != uint32(targetFrame) || trace.runCalls != uint32(frames) || trace.tonalityCalls != 1 ||
		trace.overflow != 0 || trace.stageMask != 15 || trace.sourceHash != sourceHash {
		t.Fatalf("GAST frame=%d run=%d tonality=%d overflow=%d stages=%04b source=%s want=%s",
			trace.frame, trace.runCalls, trace.tonalityCalls, trace.overflow, trace.stageMask,
			trace.sourceHash, sourceHash)
	}
	if phaseTrace.frame != uint32(targetFrame) || phaseTrace.totalCalls != 239 || phaseTrace.storedCalls != 239 ||
		phaseTrace.overflow != 0 || len(phaseTrace.records) != 239 {
		t.Fatalf("GAPH frame=%d calls=%d stored=%d overflow=%d records=%d",
			phaseTrace.frame, phaseTrace.totalCalls, phaseTrace.storedCalls,
			phaseTrace.overflow, len(phaseTrace.records))
	}

	state := NewTonalityAnalysisState(fs)
	state.SetLSBDepth(lsbDepth)
	var metrics [239]analysisPerBinTraceSnapshot
	metricCalls := 0
	oldHook := analysisPerBinTraceHook
	t.Cleanup(func() { analysisPerBinTraceHook = oldHook })
	for frame := 0; frame <= targetFrame; frame++ {
		start := frame * frameSize * channels
		framePCM := samples[start : start+frameSize*channels]
		if frame == targetFrame {
			if state.Count != 2 {
				t.Fatalf("%s frame-%d pre-analysis Count=%d want 2", class, frame, state.Count)
			}
			nonzeroPriorHistory := 0
			for _, value := range state.D2Angle {
				if value != 0 {
					nonzeroPriorHistory++
				}
			}
			if nonzeroPriorHistory == 0 {
				t.Fatalf("%s frame-%d has no nonzero prior d2 history", class, frame)
			}
			analysisPerBinTraceHook = func(snapshot analysisPerBinTraceSnapshot) {
				if metricCalls >= len(metrics) || snapshot.Bin != int32(metricCalls+1) {
					metricCalls = len(metrics) + 1
					return
				}
				metrics[metricCalls] = snapshot
				metricCalls++
			}
		} else {
			analysisPerBinTraceHook = nil
		}
		state.RunAnalysis(framePCM, frameSize, channels)
	}
	analysisPerBinTraceHook = nil
	if metricCalls != len(metrics) {
		t.Fatalf("%s frame-%d Go per-bin trace calls=%d want %d", class, targetFrame, metricCalls, len(metrics))
	}
	if state.Count != 3 {
		t.Fatalf("%s frame-%d post-analysis Count=%d want 3", class, targetFrame, state.Count)
	}
	fftOut := analysisFFTOutAsComplex(state.scratchFFTOut[:])
	compareAnalysisComplexBits(t, "target-frame FFT output", trace.fftOutput, fftOut)
	compareAnalysisPhaseInputs(t, phaseTrace, fftOut)
	compareAnalysisPhaseState(t, phaseTrace, state.Angle, state.DAngle, state.D2Angle)
	compareAnalysisPerBinMetrics(t, phaseTrace, metrics[:])
}

func assertAnalysisAvgModInputSHA256(t *testing.T, label string, input []byte, want string) {
	t.Helper()
	got := fmt.Sprintf("%x", sha256.Sum256(input))
	if got != want {
		t.Fatalf("%s GANI input SHA256=%s want %s", label, got, want)
	}
	t.Logf("%s GANI input SHA256=%s", label, got)
}

type analysisNonzeroHistoryGoFrame struct {
	inmem         [480]float32
	fftInput      []complex64
	fftOutput     []complex64
	angle         [240]float32
	dAngle        [240]float32
	d2Angle       [240]float32
	downmixState  [3]float32
	hpEnergyAccum float32
	memFill       int32
}

func snapshotAnalysisNonzeroHistoryGoFrame(state *TonalityAnalysisState) analysisNonzeroHistoryGoFrame {
	var frame analysisNonzeroHistoryGoFrame
	copy(frame.inmem[:], state.InMem[240:720])
	frame.fftInput = append([]complex64(nil), state.scratchFFTIn[:]...)
	frame.fftOutput = analysisFFTOutAsComplex(state.scratchFFTOut[:])
	frame.angle = state.Angle
	frame.dAngle = state.DAngle
	frame.d2Angle = state.D2Angle
	frame.downmixState = state.DownmixState
	frame.hpEnergyAccum = state.HPEnerAccum
	frame.memFill = state.MemFill
	return frame
}

func validateAnalysisNonzeroHistoryCapture(t *testing.T, trace libopusAnalysisStageTrace, phase libopusAnalysisPhaseTrace, targetFrame uint32, frames int, sourceHash string) {
	t.Helper()
	if trace.frame != targetFrame || trace.runCalls != uint32(frames) || trace.tonalityCalls != 1 ||
		trace.overflow != 0 || trace.stageMask != 15 || trace.sourceHash != sourceHash {
		t.Fatalf("GAST target=%d frame=%d run=%d tonality=%d overflow=%d stages=%04b source=%s want=%s",
			targetFrame, trace.frame, trace.runCalls, trace.tonalityCalls, trace.overflow, trace.stageMask,
			trace.sourceHash, sourceHash)
	}
	wantMetadata := [21]uint32{
		960, 960, 48000, 0, ^uint32(1), 2, 24,
		48000, 480, 0, 0, ^uint32(1), 2, 24,
		240, 240, 480, 480, 480, 3, 1,
	}
	if trace.metadata != wantMetadata {
		t.Fatalf("frame-%d GAST source geometry=%v want %v", targetFrame, trace.metadata, wantMetadata)
	}
	if phase.frame != targetFrame || phase.totalCalls != 239 || phase.storedCalls != 239 ||
		phase.overflow != 0 || len(phase.records) != 239 {
		t.Fatalf("frame-%d GAPH frame=%d calls=%d stored=%d overflow=%d records=%d",
			targetFrame, phase.frame, phase.totalCalls, phase.storedCalls, phase.overflow, len(phase.records))
	}
}

func compareAnalysisNonzeroHistoryFrame(t *testing.T, trace libopusAnalysisStageTrace, phase libopusAnalysisPhaseTrace, goFrame analysisNonzeroHistoryGoFrame, targetFrame uint32) {
	t.Helper()
	if goFrame.memFill != 240 {
		t.Fatalf("frame-%d Go post-run mem_fill=%d want 240", targetFrame, goFrame.memFill)
	}
	compareAnalysisF32Bits(t, fmt.Sprintf("frame-%d downmix/resampler output ring", targetFrame), trace.inmem, goFrame.inmem[:])
	compareAnalysisComplexBits(t, fmt.Sprintf("frame-%d windowed FFT input", targetFrame), trace.fftInput, goFrame.fftInput)
	compareAnalysisComplexBits(t, fmt.Sprintf("frame-%d FFT output", targetFrame), trace.fftOutput, goFrame.fftOutput)
	compareAnalysisPhaseInputs(t, phase, goFrame.fftOutput)
	compareAnalysisPhaseState(t, phase, goFrame.angle, goFrame.dAngle, goFrame.d2Angle)
	compareAnalysisF32Bits(t, fmt.Sprintf("frame-%d downmix state", targetFrame), trace.downmixState[:], goFrame.downmixState[:])
	if got := math.Float32bits(goFrame.hpEnergyAccum); got != trace.hpEnergyAccum {
		t.Fatalf("frame-%d post-run high-pass energy Go=%08x C=%08x", targetFrame, got, trace.hpEnergyAccum)
	}
	t.Logf("frame-%d GAST input/window/FFT and GAPH phase inputs/state match", targetFrame)
}

func countNonzeroAnalysisPhaseField(trace libopusAnalysisPhaseTrace, field func(libopusAnalysisPhaseRecord) uint32) int {
	count := 0
	for _, record := range trace.records {
		if math.Float32frombits(field(record)) != 0 {
			count++
		}
	}
	return count
}

func logAnalysisNonzeroHistoryMetricDifferences(t *testing.T, trace libopusAnalysisPhaseTrace, goMetrics []analysisPerBinTraceSnapshot, frame uint32) {
	t.Helper()
	if len(trace.records) != len(goMetrics) {
		t.Fatalf("frame-%d per-bin metric dimensions C=%d Go=%d", frame, len(trace.records), len(goMetrics))
	}
	fields := []struct {
		name string
		c    func(libopusAnalysisPhaseRecord) uint32
		goAt func(analysisPerBinTraceSnapshot) float32
	}{
		{"avg_mod", func(r libopusAnalysisPhaseRecord) uint32 { return r.avgMod }, func(s analysisPerBinTraceSnapshot) float32 { return s.AvgMod }},
		{"tonality2", func(r libopusAnalysisPhaseRecord) uint32 { return r.tonality2 }, func(s analysisPerBinTraceSnapshot) float32 { return s.Tonality2 }},
		{"raw_tonality", func(r libopusAnalysisPhaseRecord) uint32 { return r.rawTonality }, func(s analysisPerBinTraceSnapshot) float32 { return s.Tonality }},
		{"noisiness", func(r libopusAnalysisPhaseRecord) uint32 { return r.noisiness }, func(s analysisPerBinTraceSnapshot) float32 { return s.Noisiness }},
	}
	for _, field := range fields {
		firstBin := -1
		var firstGo, firstC uint32
		differences := 0
		for i, record := range trace.records {
			if record.bin != uint32(i+1) || goMetrics[i].Bin != int32(i+1) {
				t.Fatalf("frame-%d %s bin order C=%d Go=%d expected=%d", frame, field.name, record.bin, goMetrics[i].Bin, i+1)
			}
			goBits := math.Float32bits(field.goAt(goMetrics[i]))
			cBits := field.c(record)
			if goBits != cBits {
				if firstBin < 0 {
					firstBin, firstGo, firstC = int(record.bin), goBits, cBits
				}
				differences++
			}
		}
		if differences != 0 {
			t.Fatalf("frame-%d per-bin %s first difference bin=%d Go=%08x C=%08x differing bins=%d/239",
				frame, field.name, firstBin, firstGo, firstC, differences)
		}
		t.Logf("frame-%d per-bin %s exact for bins 1..239", frame, field.name)
	}
}

func logAnalysisNonzeroHistoryAvgModBin40(t *testing.T, prior, current libopusAnalysisPhaseRecord, goMetric analysisPerBinTraceSnapshot) {
	t.Helper()
	if prior.bin != 40 || current.bin != 40 || goMetric.Bin != 40 {
		t.Fatalf("avg_mod diagnostic bin selection C-prior=%d C-current=%d Go=%d want 40", prior.bin, current.bin, goMetric.Bin)
	}
	oldAngle := math.Float32frombits(prior.angleState)
	oldDAngle := math.Float32frombits(prior.dAngleState)
	oldD2Angle := math.Float32frombits(prior.d2AngleState)
	angle := math.Float32frombits(current.angle)
	angle2 := math.Float32frombits(current.angle2)

	// These noinline float32 operations model the source's individual C-float
	// rounding points. They are diagnostic models, not captured intermediates
	// and not replacements for either implementation's actual avg_mod.
	dAngleModel := analysisAvgModModelSub32(angle, oldAngle)
	d2AngleModel := analysisAvgModModelSub32(dAngleModel, oldDAngle)
	dAngle2Model := analysisAvgModModelSub32(angle2, angle)
	d2Angle2Model := analysisAvgModModelSub32(dAngle2Model, dAngleModel)
	mod1RootModel := analysisAvgModModelSub32(d2AngleModel, float32(analysisFloat2Int(d2AngleModel)))
	mod2RootModel := analysisAvgModModelSub32(d2Angle2Model, float32(analysisFloat2Int(d2Angle2Model)))
	mod1SquareModel := analysisAvgModModelMul32(mod1RootModel, mod1RootModel)
	mod1FourthModel := analysisAvgModModelMul32(mod1SquareModel, mod1SquareModel)
	mod2SquareModel := analysisAvgModModelMul32(mod2RootModel, mod2RootModel)
	mod2FourthModel := analysisAvgModModelMul32(mod2SquareModel, mod2SquareModel)
	twoMod2Model := analysisAvgModModelMul32(2, mod2FourthModel)
	sumWithOldHistoryModel := analysisAvgModModelAdd32(oldD2Angle, mod1FourthModel)
	sumAllTermsModel := analysisAvgModModelAdd32(sumWithOldHistoryModel, twoMod2Model)
	avgModSourceOrderModel := analysisAvgModModelMul32(0.25, sumAllTermsModel)
	avgModGoFMA32Model := analysisAvgMod32(mod1SquareModel, oldD2Angle, mod2FourthModel)

	t.Logf("frame-2 avg_mod source model bin=40 C-input X1=(%08x,%08x) X2=(%08x,%08x) angle=%08x angle2=%08x",
		current.x1r, current.x1i, current.x2r, current.x2i, current.angle, current.angle2)
	t.Logf("frame-2 avg_mod source model bin=40 prior A=%08x dA=%08x oldD2=%08x; modeled d_angle=%08x d2_angle=%08x d_angle2=%08x d2_angle2=%08x",
		prior.angleState, prior.dAngleState, prior.d2AngleState, math.Float32bits(dAngleModel),
		math.Float32bits(d2AngleModel), math.Float32bits(dAngle2Model), math.Float32bits(d2Angle2Model))
	t.Logf("frame-2 avg_mod source model bin=40 mod1_root=%08x mod1_square=%08x mod1_fourth=%08x mod2_root=%08x mod2_square=%08x mod2_fourth=%08x",
		math.Float32bits(mod1RootModel), math.Float32bits(mod1SquareModel), math.Float32bits(mod1FourthModel),
		math.Float32bits(mod2RootModel), math.Float32bits(mod2SquareModel), math.Float32bits(mod2FourthModel))
	t.Logf("frame-2 avg_mod source-order model bin=40 oldD2+mod1^4=%08x then +2*mod2^4=%08x then *0.25=%08x; actual avg_mod C=%08x Go=%08x",
		math.Float32bits(sumWithOldHistoryModel), math.Float32bits(sumAllTermsModel), math.Float32bits(avgModSourceOrderModel),
		current.avgMod, math.Float32bits(goMetric.AvgMod))
	t.Logf("frame-2 float32 whole-bin FMA model bin=40=%08x; original C avg_mod=%08x",
		math.Float32bits(avgModGoFMA32Model), current.avgMod)
}

//go:noinline
func analysisAvgModModelSub32(a, b float32) float32 {
	return a - b
}

//go:noinline
func analysisAvgModModelAdd32(a, b float32) float32 {
	return a + b
}

//go:noinline
func analysisAvgModModelMul32(a, b float32) float32 {
	return a * b
}

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
