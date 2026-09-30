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
	tracePath, sourceHash := buildLibopusAnalysisStageTraceHelper(t)
	traced, err := libopustest.RunHelper(tracePath, input)
	if err != nil {
		libopustest.HelperUnavailable(t, "analysis input/FFT stage trace", err)
	}
	if len(traced) < len(baseline) || !bytes.Equal(traced[:len(baseline)], baseline) {
		t.Fatal("GAST source-instrumented analysis helper changed the baseline GANO output")
	}
	trace, phaseTrace, err := parseLibopusAnalysisStageTrace(traced[len(baseline):])
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
	frame0FFTOutput := state.scratchFFTOut
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

func parseLibopusAnalysisStageTrace(data []byte) (libopusAnalysisStageTrace, libopusAnalysisPhaseTrace, error) {
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
	phase, err := parseLibopusAnalysisPhaseTrace(data[gastBytes:])
	if err != nil {
		return trace, phase, err
	}
	return trace, phase, nil
}

func parseLibopusAnalysisPhaseTrace(data []byte) (libopusAnalysisPhaseTrace, error) {
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
	if phase.frame != 0 || phase.totalCalls != phaseBins || phase.storedCalls != phaseBins || phase.overflow != 0 {
		return phase, fmt.Errorf("GAPH frame=%d calls=%d stored=%d overflow=%d", phase.frame,
			phase.totalCalls, phase.storedCalls, phase.overflow)
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
