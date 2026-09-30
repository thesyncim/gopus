//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext

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

const libopusAnalysisSourceSHA256 = "d2442fc330fc2576d30df080d68be4aca8803df15edcfcfa48078c13ebbc3ff8"
const libopusAnalysisInfoSourceSHA256 = "68a03d38b330425339597d56b992022d0f21c2b12799aa1853ee4ae93376cd6f"

var libopusAnalysisStageHelper libopustest.HelperCache

type libopusAnalysisStageTrace struct {
	frame, runCalls, tonalityCalls, overflow, stageMask uint32
	sourceHash                                          string
	metadata                                            [21]uint32
	inmem, fftInput, fftOutput                          []uint32
	downmixState                                        [3]uint32
	hpEnergyAccum                                       uint32
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
	tracePath, sourceHash := buildLibopusAnalysisStageTraceHelper(t)
	traced, err := libopustest.RunHelper(tracePath, input)
	if err != nil {
		libopustest.HelperUnavailable(t, "analysis input/FFT stage trace", err)
	}
	if len(traced) < len(baseline) || !bytes.Equal(traced[:len(baseline)], baseline) {
		t.Fatal("GAST source-instrumented analysis helper changed the baseline GANO output")
	}
	trace, err := parseLibopusAnalysisStageTrace(traced[len(baseline):])
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

	state := NewTonalityAnalysisState(fs)
	state.SetLSBDepth(lsbDepth)
	framePCM := samples[:frameSize*channels]
	_ = state.RunAnalysis(framePCM, frameSize, channels)
	if state.MemFill != 240 {
		t.Fatalf("Go post-run mem_fill=%d want 240", state.MemFill)
	}
	compareAnalysisF32Bits(t, "downmix/resampler output ring", trace.inmem, state.InMem[240:720])
	compareAnalysisComplexBits(t, "windowed FFT input", trace.fftInput, state.scratchFFTIn[:])
	compareAnalysisComplexBits(t, "FFT output", trace.fftOutput, state.scratchFFTOut[:])
	compareAnalysisF32Bits(t, "downmix state", trace.downmixState[:], state.DownmixState[:])
	if got := math.Float32bits(state.HPEnerAccum); got != trace.hpEnergyAccum {
		t.Fatalf("post-run high-pass energy Go=%08x C=%08x", got, trace.hpEnergyAccum)
	} else {
		t.Logf("post-run high-pass energy matches: %08x", got)
	}
}

func buildLibopusAnalysisStageTraceHelper(t *testing.T) (string, string) {
	t.Helper()
	path, err := libopusAnalysisStageHelper.Path(func() (string, error) {
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
			OutputBase: "gopus_libopus_analysis_stage_trace_f0",
			SourceFile: filepath.Join(csrc, "libopus_analysis_stage_trace_main.c"),
			CFlags: []string{
				"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG",
				"-DGOPUS_ANALYSIS_STAGE_TRACE_FRAME=0",
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

func parseLibopusAnalysisStageTrace(data []byte) (libopusAnalysisStageTrace, error) {
	var trace libopusAnalysisStageTrace
	if len(data) < 4 || string(data[:4]) != "GAST" {
		return trace, fmt.Errorf("missing GAST magic")
	}
	const (
		fixedHeaderBytes = 7*4 + 64 + 21*4
		floatArrayWords  = 480 + 2*960 + 3 + 1
	)
	if len(data) != 4+fixedHeaderBytes+4*floatArrayWords {
		return trace, fmt.Errorf("GAST byte length=%d want %d", len(data), 4+fixedHeaderBytes+4*floatArrayWords)
	}
	offset := 4
	readU32 := func() uint32 {
		value := binary.LittleEndian.Uint32(data[offset:])
		offset += 4
		return value
	}
	version := readU32()
	if version != 1 {
		return trace, fmt.Errorf("GAST version=%d want 1", version)
	}
	trace.frame = readU32()
	trace.runCalls = readU32()
	trace.tonalityCalls = readU32()
	trace.overflow = readU32()
	trace.stageMask = readU32()
	hashLen := readU32()
	if hashLen != 64 || offset+int(hashLen) > len(data) {
		return trace, fmt.Errorf("GAST source hash length=%d want 64", hashLen)
	}
	trace.sourceHash = string(data[offset : offset+int(hashLen)])
	if _, err := hex.DecodeString(trace.sourceHash); err != nil {
		return trace, fmt.Errorf("GAST source hash is malformed: %w", err)
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
	return trace, nil
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
