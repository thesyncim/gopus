//go:build linux && amd64 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext

package encoder

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
)

const (
	celtTraceFrameSize = 240
	celtTraceChannels  = 2
	celtTraceBitrate   = 128000
	celtTraceAppCELT   = 3
	celtTraceBWFULL    = 1105
	celtTraceBandCount = 21
	celtTraceActive    = 200
	celtLateTraceFrame = 95
	celtLateFrameSize  = 120
	celtLateChannels   = 1
	celtLateBitrate    = 64000
	celtLateFrames     = 400
)

var (
	celtTraceOracleCache        libopustest.HelperCache
	celtTraceWrappedOracleCache libopustest.HelperCache
	celtLateTraceOracleCache    libopustest.HelperCache
)

func TestCELTFirstFrameStageDiagnostic(t *testing.T) {
	libopustest.RequireOracle(t)
	requireCELTTraceV3(t, "first-frame")
	testCELTPrefilterNoopValidatorRejectsMalformedEvidence(t)

	pcm, err := testsignal.GenerateEncoderSignalVariant(
		testsignal.EncoderVariantAMMultisineV1,
		48000,
		(48000/celtTraceFrameSize)*celtTraceFrameSize*celtTraceChannels,
		celtTraceChannels,
	)
	if err != nil {
		t.Fatalf("generate CBR parity signal: %v", err)
	}
	frameSamples := celtTraceFrameSize * celtTraceChannels
	frame := quantizeCELTTracePCM(pcm[:frameSamples])
	input := celtTraceCBRInput(frame)

	ordinaryPath := buildCELTTraceOracle(t, false)
	ordinaryBytes, err := libopustest.RunHelper(ordinaryPath, input)
	if err != nil {
		t.Fatalf("run ordinary selected CBR oracle: %v", err)
	}
	ordinary, err := parseCELTTraceCBRPrefix(ordinaryBytes)
	if err != nil {
		t.Fatalf("parse ordinary selected CBR oracle: %v", err)
	}
	if len(ordinary.Packets) != 1 || len(ordinary.FinalRanges) != 1 {
		t.Fatalf("ordinary selected CBR oracle returned packets=%d ranges=%d, want one frame", len(ordinary.Packets), len(ordinary.FinalRanges))
	}
	if !strings.Contains(ordinary.LibopusVersion, libopustooling.DefaultVersion) {
		t.Fatalf("ordinary selected CBR oracle reports version %q, want %s", ordinary.LibopusVersion, libopustooling.DefaultVersion)
	}

	tracePath := buildCELTTraceOracle(t, true)
	traceBytes, err := libopustest.RunHelper(tracePath, input)
	if err != nil {
		t.Fatalf("run traced selected CBR oracle: %v", err)
	}
	tracePrefix, tracePayload, err := splitCELTTraceOutput(traceBytes)
	if err != nil {
		t.Fatalf("split traced selected CBR oracle output: %v", err)
	}
	traced, err := parseCELTTraceCBRPrefix(tracePrefix)
	if err != nil {
		t.Fatalf("parse traced selected CBR oracle: %v", err)
	}
	if !sameCELTTraceCBROutput(ordinary, traced) {
		t.Fatalf("trace wrappers changed the C oracle output: ordinary packet=%x range=%08x, traced packet=%x range=%08x",
			ordinary.Packets[0], ordinary.FinalRanges[0], traced.Packets[0], traced.FinalRanges[0])
	}
	cTrace, err := parseCELTEncodeTrace(tracePayload)
	if err != nil {
		t.Fatalf("parse traced C stage payload: %v", err)
	}
	if cTrace.Overflow != 0 {
		t.Fatalf("C stage trace overflowed its bounded capture: flags=%d", cTrace.Overflow)
	}
	if cTrace.TraceFrame != 0 {
		t.Fatalf("C stage trace captured frame %d, want frame 0", cTrace.TraceFrame)
	}
	if cTrace.BandCalls == 0 || cTrace.LogCalls == 0 || cTrace.NormalizationCalls == 0 || cTrace.CoarseCalls == 0 || cTrace.QuantCalls == 0 || cTrace.PreemphasisCalls == 0 {
		t.Fatalf("C wrappers did not cover every target boundary: %+v", cTrace.counts())
	}
	if cTrace.MDCTCalls == 0 {
		t.Fatalf("C MDCT wrapper did not capture the selected first frame: %s", cTrace.counts())
	}

	goEncoder := newCELTTraceEncoder()
	goEncoder.ensureCELTEncoder()
	goEncoder.celtEncoder.EnableEncodeStageTraceForTesting()
	goPacket, err := goEncoder.Encode(frame, celtTraceFrameSize)
	if err != nil {
		t.Fatalf("encode first Go CELT frame: %v", err)
	}
	goTrace := goEncoder.celtEncoder.EncodeStageTraceForTesting()
	plainEncoder := newCELTTraceEncoder()
	plainPacket, err := plainEncoder.Encode(frame, celtTraceFrameSize)
	if err != nil {
		t.Fatalf("encode first untraced Go CELT frame: %v", err)
	}
	if !bytes.Equal(goPacket, plainPacket) || goEncoder.FinalRange() != plainEncoder.FinalRange() {
		t.Fatalf("Go trace hooks changed the result: traced packet=%x range=%08x, untraced packet=%x range=%08x",
			goPacket, goEncoder.FinalRange(), plainPacket, plainEncoder.FinalRange())
	}

	t.Logf("first-frame result: Go packet bytes=%d C bytes=%d first packet byte diff=%d Go range=%08x C range=%08x",
		len(goPacket), len(ordinary.Packets[0]), firstCELTTraceByteDifference(goPacket, ordinary.Packets[0]), goEncoder.FinalRange(), ordinary.FinalRanges[0])
	if len(goTrace.BandStages) != cTrace.BandCalls || len(goTrace.BandStages) != cTrace.LogCalls ||
		len(goTrace.Normalizations) != cTrace.NormalizationCalls || len(goTrace.CoarseEnergy) != cTrace.CoarseCalls ||
		len(goTrace.BandQuantize) != cTrace.QuantCalls || len(goTrace.MDCTCalls) != cTrace.MDCTCalls {
		t.Fatalf("Go/C stage call counts differ: Go bands=%d normalize=%d coarse=%d quant=%d; C %s",
			len(goTrace.BandStages), len(goTrace.Normalizations), len(goTrace.CoarseEnergy), len(goTrace.BandQuantize), cTrace.counts())
	}
	if err := validateCELTTraceShapes(goTrace, cTrace); err != nil {
		t.Fatalf("invalid Go/C CELT trace shapes: %v", err)
	}
	if err := validateCELTTraceExpectedDimensions(goTrace, cTrace, celtTraceFrameSize, celtTraceBandCount, celtTraceChannels, celtTraceActive, 1); err != nil {
		t.Fatalf("unexpected first-frame Go/C CELT trace dimensions: %v", err)
	}
	t.Log("stage-bit comparisons are diagnostic and do not establish packet parity")
	logCELTTraceDifferences(t, goTrace, cTrace)
}

func testCELTPrefilterNoopValidatorRejectsMalformedEvidence(t *testing.T) {
	t.Helper()
	noops, cTrace := celTPrefilterNoopFixture()
	if err := validateCELTPrefilterNoop(noops, cTrace); err != nil {
		t.Fatalf("valid zero-gain identity trace was rejected: %v", err)
	}
	tinyNoops, _ := celTPrefilterNoopFixture()
	for channel := range tinyNoops {
		clear(tinyNoops[channel].Frame)
		clear(tinyNoops[channel].Input)
		tinyNoops[channel].Frame[0] = math.SmallestNonzeroFloat32
		tinyNoops[channel].Input[0] = math.SmallestNonzeroFloat32
	}
	if celtTraceNoopWouldCancel(tinyNoops) {
		t.Fatal("subnormal input incorrectly predicts C cancellation after its 0.01 threshold rounds to zero")
	}
	overflowNoops, _ := celTPrefilterNoopFixture()
	for channel := range overflowNoops {
		for i := range overflowNoops[channel].Frame {
			overflowNoops[channel].Frame[i] = math.MaxFloat32
			overflowNoops[channel].Input[i] = math.MaxFloat32
		}
	}
	if celtTraceNoopWouldCancel(overflowNoops) {
		t.Fatal("finite inputs whose ABS32 sum overflows incorrectly predict C cancellation from Inf-Inf")
	}
	cases := []struct {
		name   string
		mutate func(*celtCBRStageTrace)
	}{
		{name: "nonzero gain", mutate: func(trace *celtCBRStageTrace) { trace.Prefilter[0].Gain1 = 0.125 }},
		{name: "unmatched history", mutate: func(trace *celtCBRStageTrace) {
			trace.Prefilter[0].History[0] = math.Float32frombits(math.Float32bits(trace.Prefilter[0].History[0]) ^ 1)
		}},
		{name: "wrong geometry", mutate: func(trace *celtCBRStageTrace) { trace.Prefilter[0].N-- }},
		{name: "nonidentity output", mutate: func(trace *celtCBRStageTrace) {
			trace.Prefilter[0].Output[0] = math.Float32frombits(math.Float32bits(trace.Prefilter[0].Output[0]) ^ 1)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, malformed := celTPrefilterNoopFixture()
			tc.mutate(&malformed)
			if err := validateCELTPrefilterNoop(noops, malformed); err == nil {
				t.Fatal("malformed C no-op evidence was accepted")
			}
		})
	}
}

func celTPrefilterNoopFixture() ([]celt.EncodePrefilterNoopTrace, celtCBRStageTrace) {
	const channels, frameSize, overlap = 2, 240, 120
	noops := make([]celt.EncodePrefilterNoopTrace, channels)
	cTrace := celtCBRStageTrace{Preemphasis: make([]celtCBRStagePreemphasis, channels), PrefilterCalls: channels * 2}
	for channel := range channels {
		history := make([]float32, 1024)
		input := make([]float32, frameSize)
		window := make([]float32, overlap)
		for i := range history {
			history[i] = float32(channel+1) * float32(i+1) / 4096
		}
		for i := range input {
			input[i] = float32(channel+1) * float32(i+1) / 2048
		}
		for i := range window {
			window[i] = float32(i+1) / float32(overlap+1)
		}
		noops[channel] = celt.EncodePrefilterNoopTrace{
			Channel: channel, FrameSize: frameSize, Overlap: overlap, T0: 48, T1: 96,
			Gain0: 0, Gain1: 0, Tapset0: 1, Tapset1: 2,
			History: history, Input: append([]float32(nil), input...), Frame: append([]float32(nil), input...), Window: window,
		}
	}
	for i := range cTrace.PrefilterCalls {
		channel, n := i%channels, frameSize
		if i >= channels {
			channel, n = i-channels, overlap
		}
		goNoop := noops[channel]
		values := append([]float32(nil), goNoop.Input[:n]...)
		cTrace.Prefilter = append(cTrace.Prefilter, celtCBRStagePrefilter{
			T0: goNoop.T0, T1: goNoop.T1, N: n, Tapset0: goNoop.Tapset0, Tapset1: goNoop.Tapset1,
			Overlap: overlap, WindowNil: false, Gain0: 0, Gain1: 0,
			History: append([]float32(nil), goNoop.History...), Input: values,
			Window: append([]float32(nil), goNoop.Window...), Output: append([]float32(nil), values...),
		})
	}
	return noops, cTrace
}

func TestCELTLateCBRFrameStageDiagnostic(t *testing.T) {
	libopustest.RequireOracle(t)
	requireCELTTraceV3(t, "late-frame")

	pcm, err := testsignal.GenerateEncoderSignalVariant(
		testsignal.EncoderVariantAMMultisineV1,
		48000,
		celtLateFrameSize*celtLateChannels*celtLateFrames,
		celtLateChannels,
	)
	if err != nil {
		t.Fatalf("generate late-frame CBR parity signal: %v", err)
	}
	pcm = quantizeCELTTracePCM(pcm)
	input := celtTraceCBRInputFor(pcm, celtTraceAppCELT, celtTraceBWFULL, celtLateChannels,
		celtLateBitrate, celtLateFrameSize, celtLateFrames, 10)

	ordinaryPath := buildCELTTraceOracle(t, false)
	ordinaryBytes, err := libopustest.RunHelper(ordinaryPath, input)
	if err != nil {
		t.Fatalf("run ordinary full-stream CBR oracle: %v", err)
	}
	ordinary, err := parseCELTTraceCBRPrefix(ordinaryBytes)
	if err != nil {
		t.Fatalf("parse ordinary full-stream CBR oracle: %v", err)
	}
	if len(ordinary.Packets) != celtLateFrames || len(ordinary.FinalRanges) != celtLateFrames {
		t.Fatalf("ordinary C oracle returned packets=%d ranges=%d, want %d frames", len(ordinary.Packets), len(ordinary.FinalRanges), celtLateFrames)
	}
	if !strings.Contains(ordinary.LibopusVersion, libopustooling.DefaultVersion) {
		t.Fatalf("ordinary C oracle reports version %q, want %s", ordinary.LibopusVersion, libopustooling.DefaultVersion)
	}
	t.Logf("late-frame C oracle: version=%q archmask=0x%x features=0x%x selected_arch=%d frames=%d",
		ordinary.LibopusVersion, ordinary.ArchMask, ordinary.BuildFeatures, ordinary.SelectedArch, len(ordinary.Packets))

	tracePath := buildCELTTraceOracleAtFrame(t, true, celtLateTraceFrame)
	traceBytes, err := libopustest.RunHelper(tracePath, input)
	if err != nil {
		t.Fatalf("run late-frame traced CBR oracle: %v", err)
	}
	tracePrefix, tracePayload, err := splitCELTTraceOutput(traceBytes)
	if err != nil {
		t.Fatalf("split late-frame traced CBR oracle output: %v", err)
	}
	traced, err := parseCELTTraceCBRPrefix(tracePrefix)
	if err != nil {
		t.Fatalf("parse late-frame traced CBR oracle: %v", err)
	}
	if len(traced.Packets) != celtLateFrames || len(traced.FinalRanges) != celtLateFrames {
		t.Fatalf("traced C oracle returned packets=%d ranges=%d, want %d frames", len(traced.Packets), len(traced.FinalRanges), celtLateFrames)
	}
	if !sameCELTTraceCBROutput(ordinary, traced) {
		for frame := range min(len(ordinary.Packets), len(traced.Packets)) {
			byteDiff := firstCELTTraceByteDifference(ordinary.Packets[frame], traced.Packets[frame])
			if byteDiff >= 0 || ordinary.FinalRanges[frame] != traced.FinalRanges[frame] {
				t.Fatalf("C stage wrappers changed full-stream output at frame %d: packet byte diff=%d ordinary range=%08x traced range=%08x",
					frame, byteDiff, ordinary.FinalRanges[frame], traced.FinalRanges[frame])
			}
		}
		t.Fatal("C stage wrappers changed CBR stream metadata or frame count")
	}

	cTrace, err := parseCELTEncodeTrace(tracePayload)
	if err != nil {
		t.Fatalf("parse late-frame C stage trace: %v", err)
	}
	if cTrace.TraceFrame != celtLateTraceFrame {
		t.Fatalf("C stage trace captured frame %d, want %d", cTrace.TraceFrame, celtLateTraceFrame)
	}
	if cTrace.Overflow != 0 {
		t.Fatalf("late-frame C stage trace overflowed its bounded capture: flags=%d", cTrace.Overflow)
	}
	if cTrace.BandCalls == 0 || cTrace.LogCalls == 0 || cTrace.NormalizationCalls == 0 || cTrace.CoarseCalls == 0 || cTrace.QuantCalls == 0 || cTrace.PreemphasisCalls == 0 {
		t.Fatalf("C wrappers did not cover every late-frame target boundary: %+v", cTrace.counts())
	}
	if cTrace.MDCTCalls == 0 {
		t.Fatalf("C MDCT wrapper did not capture the selected late frame: %s", cTrace.counts())
	}

	goEncoder := newCELTTraceEncoderConfig(celtLateChannels, celtLateBitrate)
	plainEncoder := newCELTTraceEncoderConfig(celtLateChannels, celtLateBitrate)
	frameSamples := celtLateFrameSize * celtLateChannels
	var goPacket []byte
	for frame := range celtLateTraceFrame + 1 {
		framePCM := pcm[frame*frameSamples : (frame+1)*frameSamples]
		if frame == celtLateTraceFrame {
			goEncoder.celtEncoder.EnableEncodeStageTraceForTesting()
		}
		goPacket, err = goEncoder.Encode(framePCM, celtLateFrameSize)
		if err != nil {
			t.Fatalf("encode Go CELT frame %d: %v", frame, err)
		}
		plainPacket, plainErr := plainEncoder.Encode(framePCM, celtLateFrameSize)
		if plainErr != nil {
			t.Fatalf("encode untraced Go CELT frame %d: %v", frame, plainErr)
		}
		if !bytes.Equal(goPacket, plainPacket) || goEncoder.FinalRange() != plainEncoder.FinalRange() {
			t.Fatalf("Go trace altered encoding at frame %d: traced packet=%x range=%08x plain packet=%x range=%08x",
				frame, goPacket, goEncoder.FinalRange(), plainPacket, plainEncoder.FinalRange())
		}
	}
	goTrace := goEncoder.celtEncoder.EncodeStageTraceForTesting()
	// Emit preemphasis and actual prefilter inputs/history before the strict
	// control-shape gate so a period mismatch cannot hide an upstream data gap.
	logCELTTraceDifferences(t, goTrace, cTrace)
	if len(goTrace.BandStages) != cTrace.BandCalls || len(goTrace.BandStages) != cTrace.LogCalls ||
		len(goTrace.Normalizations) != cTrace.NormalizationCalls || len(goTrace.CoarseEnergy) != cTrace.CoarseCalls ||
		len(goTrace.BandQuantize) != cTrace.QuantCalls || len(goTrace.MDCTCalls) != cTrace.MDCTCalls {
		t.Fatalf("late-frame Go/C stage call counts differ: Go bands=%d normalize=%d coarse=%d quant=%d; C %s",
			len(goTrace.BandStages), len(goTrace.Normalizations), len(goTrace.CoarseEnergy), len(goTrace.BandQuantize), cTrace.counts())
	}
	if err := validateCELTTraceShapes(goTrace, cTrace); err != nil {
		t.Fatalf("invalid late-frame Go/C CELT trace shapes: %v", err)
	}
	if err := validateCELTTraceExpectedDimensions(goTrace, cTrace, celtLateFrameSize, celtTraceBandCount, celtLateChannels, celtTraceActive/2, 0); err != nil {
		t.Fatalf("unexpected late-frame Go/C CELT trace dimensions: %v", err)
	}
	if err := validateCELTMDCTTraceExpectedDimensions(goTrace, cTrace, celtLateFrameSize, celtLateChannels, 240, 3, 3); err != nil {
		t.Fatalf("unexpected late-frame Go/C MDCT trace dimensions: %v", err)
	}
	t.Logf("late-frame result: frame=%d Go packet bytes=%d C bytes=%d first packet byte diff=%d Go range=%08x C range=%08x",
		celtLateTraceFrame, len(goPacket), len(ordinary.Packets[celtLateTraceFrame]), firstCELTTraceByteDifference(goPacket, ordinary.Packets[celtLateTraceFrame]), goEncoder.FinalRange(), ordinary.FinalRanges[celtLateTraceFrame])
	t.Log("late-frame stage-bit comparisons are diagnostic and do not establish packet parity")
}

func requireCELTTraceV3(t *testing.T, label string) {
	t.Helper()
	if target := os.Getenv("GOPUS_LIBOPUS_AMD64_TARGET"); target != "v3" {
		message := fmt.Sprintf("%s CELT trace requires GOPUS_LIBOPUS_AMD64_TARGET=v3, got %q", label, target)
		if libopustest.StrictRefRequired() {
			t.Fatal(message)
		}
		t.Skip(message)
	}
}

func newCELTTraceEncoder() *Encoder {
	return newCELTTraceEncoderConfig(celtTraceChannels, celtTraceBitrate)
}

func newCELTTraceEncoderConfig(channels int, bitrate int) *Encoder {
	encoder := NewEncoder(48000, channels)
	encoder.SetMode(ModeCELT)
	encoder.SetRestrictedSilkApplication(false)
	encoder.SetLowDelay(true)
	encoder.SetBandwidth(types.BandwidthFullband)
	encoder.SetBitrate(bitrate)
	encoder.SetBitrateMode(ModeCBR)
	encoder.SetComplexity(10)
	return encoder
}

func buildCELTTraceOracle(t *testing.T, trace bool) string {
	return buildCELTTraceOracleAtFrame(t, trace, 0)
}

func buildCELTTraceOracleAtFrame(t *testing.T, trace bool, traceFrame int) string {
	t.Helper()
	flags := []string{"-DHAVE_CONFIG_H"}
	config := libopustest.CHelperConfig{
		Label:      fmt.Sprintf("CELT CBR stage trace frame %d", traceFrame),
		OutputBase: fmt.Sprintf("gopus_libopus_cbr_celt_stage_trace_frame_%d", traceFrame),
		SourceFile: "libopus_cbr_encode_packets.c",
		CFlags:     flags,
	}
	linkMapPath := ""
	if trace {
		if traceFrame == 0 {
			config.OutputBase = "gopus_libopus_cbr_celt_first_frame_trace_wrapped"
		} else {
			config.OutputBase = fmt.Sprintf("gopus_libopus_cbr_celt_late_frame_%d_trace_wrapped", traceFrame)
		}
		instrumentedSource := writeCELTPreemphasisTraceSource(t)
		config.CFlags = append(config.CFlags,
			"-O3", "-DNDEBUG", "-DGOPUS_CELT_TRACE", fmt.Sprintf("-DGOPUS_CELT_TRACE_FRAME=%d", traceFrame))
		config.RefIncludes = []string{"celt", "silk", "src"}
		config.Sources = []string{instrumentedSource}
		linkMapPath = filepath.Join(t.TempDir(), config.OutputBase+".map")
		config.LDFlags = []string{
			"-Wl,--wrap=comb_filter",
			"-Wl,--wrap=compute_band_energies",
			"-Wl,--wrap=amp2Log2",
			"-Wl,--wrap=normalise_bands",
			"-Wl,--wrap=quant_coarse_energy",
			"-Wl,--wrap=quant_all_bands",
			"-Wl,--wrap=clt_mdct_forward_c",
			"-Wl,-Map," + linkMapPath,
		}
	}
	cache := &celtTraceOracleCache
	if trace && traceFrame == 0 {
		cache = &celtTraceWrappedOracleCache
	} else if trace {
		cache = &celtLateTraceOracleCache
	}
	path, err := cache.Path(func() (string, error) {
		helperPath, err := libopustest.BuildPublicAPIHelper(config)
		if err != nil {
			return "", err
		}
		if trace {
			if err := validateCELTTraceLinkMap(linkMapPath); err != nil {
				return "", err
			}
		}
		return helperPath, nil
	})
	if err != nil {
		libopustest.HelperUnavailable(t, config.Label, err)
	}
	return path
}

func writeCELTPreemphasisTraceSource(t *testing.T) string {
	t.Helper()
	// celt_preemphasis is called in the same translation unit that defines it,
	// so linker --wrap does not observe this call. Build a content-hashed copy of
	// the paired pinned source with hooks around the one unchanged call site.
	sourcePath := libopustest.RefPath("celt", "celt_encoder.c")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read pinned libopus CELT encoder source %s: %v", sourcePath, err)
	}
	const originalCall = "      celt_preemphasis(pcm+c, in+c*(N+overlap)+overlap, N, CC, st->upsample,\n                  mode->preemph, st->preemph_memE+c, need_clip);"
	if count := strings.Count(string(source), originalCall); count != 1 {
		t.Fatalf("pinned CELT source has %d selected preemphasis call sites, want exactly 1", count)
	}
	const beforeAndAfter = `      {
         extern int gopus_celt_preemphasis_trace_begin(int channel, const opus_res *pcmp,
             celt_sig *inp, int N, int CC, int upsample, const opus_val16 *coef,
             celt_sig *mem, int clip);
         extern void gopus_celt_preemphasis_trace_end(int call, const celt_sig *inp,
             int N, const celt_sig *mem);
         int gopus_trace_call = gopus_celt_preemphasis_trace_begin(
             c, pcm+c, in+c*(N+overlap)+overlap, N, CC, st->upsample,
             mode->preemph, st->preemph_memE+c, need_clip);
` + originalCall + `
         gopus_celt_preemphasis_trace_end(gopus_trace_call,
             in+c*(N+overlap)+overlap, N, st->preemph_memE+c);
      }`
	instrumented := strings.Replace(string(source), originalCall, beforeAndAfter, 1)
	if count := strings.Count(instrumented, originalCall); count != 1 {
		t.Fatalf("instrumented CELT source retains %d original preemphasis calls, want exactly 1", count)
	}
	path := filepath.Join(t.TempDir(), "celt_encoder_preemphasis_trace.c")
	if err := os.WriteFile(path, []byte(instrumented), 0o600); err != nil {
		t.Fatalf("write instrumented pinned CELT encoder source: %v", err)
	}
	return path
}

func validateCELTTraceLinkMap(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read CELT trace link map %s: %w", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "libopus.a(celt_encoder.o)") {
			return fmt.Errorf("instrumented CELT trace linked the uninstrumented archive member: %s", strings.TrimSpace(line))
		}
	}
	return nil
}

func quantizeCELTTracePCM(pcm []float32) []float32 {
	quantized := make([]float32, len(pcm))
	for i, sample := range pcm {
		quantized[i] = float32(math.Floor(0.5+float64(sample)*8388608.0) / 8388608.0)
	}
	return quantized
}

func celtTraceCBRInput(pcm []float32) []byte {
	return celtTraceCBRInputFor(pcm, celtTraceAppCELT, celtTraceBWFULL, celtTraceChannels,
		celtTraceBitrate, celtTraceFrameSize, 1, 10)
}

func celtTraceCBRInputFor(pcm []float32, app, bandwidth, channels int, bitrate, frameSize, numFrames, complexity uint32) []byte {
	const headerBytes = 4 + 8*4
	data := make([]byte, headerBytes+len(pcm)*4)
	copy(data, "GCBR")
	values := [...]uint32{
		1, uint32(app), uint32(bandwidth), uint32(channels),
		bitrate, frameSize, complexity, numFrames,
	}
	for i, value := range values {
		binary.LittleEndian.PutUint32(data[4+i*4:], value)
	}
	for i, sample := range pcm {
		binary.LittleEndian.PutUint32(data[headerBytes+i*4:], math.Float32bits(sample))
	}
	return data
}

type celtTraceCBROutput struct {
	LibopusVersion string
	ArchMask       uint32
	BuildFeatures  uint32
	SelectedArch   uint32
	Packets        [][]byte
	FinalRanges    []uint32
}

func parseCELTTraceCBRPrefix(data []byte) (celtTraceCBROutput, error) {
	var result celtTraceCBROutput
	if len(data) < 8 || string(data[:4]) != "GCBO" || binary.LittleEndian.Uint32(data[4:8]) != 2 {
		return result, fmt.Errorf("invalid GCBO v2 prefix")
	}
	off := 8
	versionLen, ok := celtTraceReadU32(data, &off)
	if !ok || versionLen == 0 || versionLen > 128 || int(versionLen) > len(data)-off {
		return result, fmt.Errorf("invalid CBR version string")
	}
	result.LibopusVersion = string(data[off : off+int(versionLen)])
	off += int(versionLen)
	if off+16 > len(data) {
		return result, fmt.Errorf("truncated CBR metadata")
	}
	result.ArchMask = binary.LittleEndian.Uint32(data[off:])
	result.BuildFeatures = binary.LittleEndian.Uint32(data[off+4:])
	result.SelectedArch = binary.LittleEndian.Uint32(data[off+8:])
	frames := int(binary.LittleEndian.Uint32(data[off+12:]))
	off += 16
	if frames < 0 || frames > 4096 {
		return result, fmt.Errorf("invalid CBR frame count %d", frames)
	}
	result.Packets = make([][]byte, 0, frames)
	result.FinalRanges = make([]uint32, 0, frames)
	for frame := range frames {
		if off+8 > len(data) {
			return result, fmt.Errorf("truncated CBR frame %d", frame)
		}
		packetLen := uint64(binary.LittleEndian.Uint32(data[off:]))
		result.FinalRanges = append(result.FinalRanges, binary.LittleEndian.Uint32(data[off+4:]))
		off += 8
		if packetLen > uint64(len(data)-off) {
			return result, fmt.Errorf("truncated CBR packet %d", frame)
		}
		end := off + int(packetLen)
		result.Packets = append(result.Packets, append([]byte(nil), data[off:end]...))
		off = end
	}
	if off != len(data) {
		return result, fmt.Errorf("CBR prefix has %d trailing bytes", len(data)-off)
	}
	return result, nil
}

func splitCELTTraceOutput(data []byte) ([]byte, []byte, error) {
	if len(data) < 8 || string(data[:4]) != "GCBO" {
		return nil, nil, fmt.Errorf("traced CBR output has no GCBO header")
	}
	off := 8
	versionLen, ok := celtTraceReadU32(data, &off)
	if !ok || versionLen == 0 || versionLen > 128 || int(versionLen) > len(data)-off {
		return nil, nil, fmt.Errorf("invalid traced CBR version string")
	}
	off += int(versionLen)
	if off+16 > len(data) {
		return nil, nil, fmt.Errorf("truncated traced CBR metadata")
	}
	frames := int(binary.LittleEndian.Uint32(data[off+12:]))
	off += 16
	if frames < 0 || frames > 4096 {
		return nil, nil, fmt.Errorf("invalid traced CBR frame count %d", frames)
	}
	for frame := range frames {
		if off+8 > len(data) {
			return nil, nil, fmt.Errorf("truncated traced CBR frame %d", frame)
		}
		packetLen := uint64(binary.LittleEndian.Uint32(data[off:]))
		off += 8
		if packetLen > uint64(len(data)-off) {
			return nil, nil, fmt.Errorf("truncated traced CBR packet %d", frame)
		}
		off += int(packetLen)
	}
	if off > len(data) {
		return nil, nil, fmt.Errorf("traced CBR packet prefix exceeds output")
	}
	return data[:off], data[off:], nil
}

func celtTraceReadU32(data []byte, off *int) (uint32, bool) {
	if *off < 0 || *off+4 > len(data) {
		return 0, false
	}
	value := binary.LittleEndian.Uint32(data[*off:])
	*off += 4
	return value, true
}

func sameCELTTraceCBROutput(a, b celtTraceCBROutput) bool {
	if a.LibopusVersion != b.LibopusVersion || a.ArchMask != b.ArchMask || a.BuildFeatures != b.BuildFeatures ||
		a.SelectedArch != b.SelectedArch || len(a.Packets) != len(b.Packets) || len(a.FinalRanges) != len(b.FinalRanges) {
		return false
	}
	for i := range a.Packets {
		if !bytes.Equal(a.Packets[i], b.Packets[i]) || a.FinalRanges[i] != b.FinalRanges[i] {
			return false
		}
	}
	return true
}

type celtCBRStageBand struct {
	FrameCoeffs int
	Bands       int
	Channels    int
	LM          int
	Spectrum    []float32
	Amplitudes  []float32
	LogEnergy   []float32
}

type celtCBRStageNorm struct {
	ActiveCoeffs int
	Bands        int
	Channels     int
	BandEnergy   []float32
	Normalized   []float32
}

type celtCBRStageCoarse struct {
	Bands       int
	Channels    int
	BudgetBytes int
	Input       []float32
	Quantized   []float32
	Error       []float32
}

type celtCBRStageQuant struct {
	ActiveCoeffs int
	Bands        int
	Channels     int
	BandEnergy   []float32
	Input        []float32
	Output       []float32
}

type celtCBRStageMDCT struct {
	LookupN    int
	MaxShift   int
	TransformN int
	Shift      int
	Stride     int
	Overlap    int
	Arch       int
	FFTSize    int
	FFTScale   float32
	Input      []float32
	Window     []float32
	Trig       []float32
}

type celtCBRStagePreemphasis struct {
	Channel      int
	Channels     int
	FrameSize    int
	Upsample     int
	Flags        uint32
	StateBefore  float32
	StateAfter   float32
	Coefficients [4]float32
	Input        []float32
	Output       []float32
}

type celtCBRStagePrefilter struct {
	T0        int32
	T1        int32
	N         int
	Tapset0   int32
	Tapset1   int32
	Overlap   int
	Arch      int
	WindowNil bool
	Gain0     float32
	Gain1     float32
	History   []float32
	Input     []float32
	Window    []float32
	Output    []float32
}

type celtCBRStageTrace struct {
	TraceFrame                                                                  uint32
	Overflow                                                                    uint32
	BandCalls, LogCalls, NormalizationCalls, CoarseCalls, QuantCalls, MDCTCalls int
	PreemphasisCalls, PrefilterCalls                                            int
	Bands                                                                       []celtCBRStageBand
	Logs                                                                        []celtCBRStageLog
	Normalizations                                                              []celtCBRStageNorm
	Coarse                                                                      []celtCBRStageCoarse
	Quant                                                                       []celtCBRStageQuant
	MDCT                                                                        []celtCBRStageMDCT
	Preemphasis                                                                 []celtCBRStagePreemphasis
	Prefilter                                                                   []celtCBRStagePrefilter
}

type celtCBRStageLog struct {
	Bands      int
	Channels   int
	Amplitudes []float32
	LogEnergy  []float32
}

func validateCELTTraceShapes(goTrace celt.EncodeStageTrace, cTrace celtCBRStageTrace) error {
	if goTrace.StageOverflow {
		return fmt.Errorf("Go preemphasis/prefilter trace exceeded its bounded capture")
	}
	if len(goTrace.PrefilterNoop) > 0 {
		if len(goTrace.PrefilterComb) != 0 {
			return fmt.Errorf("Go captured both comb calls and a no-op prefilter boundary")
		}
	} else if len(goTrace.PrefilterComb) != cTrace.PrefilterCalls || len(goTrace.PrefilterComb) != len(cTrace.Prefilter) {
		return fmt.Errorf("prefilter call counts differ: Go comb=%d no-op boundaries=%d; C raw/stored=%d/%d",
			len(goTrace.PrefilterComb), len(goTrace.PrefilterNoop), cTrace.PrefilterCalls, len(cTrace.Prefilter))
	}
	if len(goTrace.BandStages) != cTrace.BandCalls || len(goTrace.BandStages) != len(cTrace.Bands) ||
		len(goTrace.BandStages) != cTrace.LogCalls || len(goTrace.BandStages) != len(cTrace.Logs) ||
		len(goTrace.Normalizations) != cTrace.NormalizationCalls || len(goTrace.Normalizations) != len(cTrace.Normalizations) ||
		len(goTrace.CoarseEnergy) != cTrace.CoarseCalls || len(goTrace.CoarseEnergy) != len(cTrace.Coarse) ||
		len(goTrace.BandQuantize) != cTrace.QuantCalls || len(goTrace.BandQuantize) != len(cTrace.Quant) ||
		len(goTrace.Preemphasis) != cTrace.PreemphasisCalls || len(goTrace.Preemphasis) != len(cTrace.Preemphasis) {
		return fmt.Errorf("stage counts differ: Go band/norm/coarse/quant/MDCT/preemphasis/prefilter/noop=%d/%d/%d/%d/%d/%d/%d/%d; C=%s",
			len(goTrace.BandStages), len(goTrace.Normalizations), len(goTrace.CoarseEnergy), len(goTrace.BandQuantize),
			len(goTrace.MDCTCalls), len(goTrace.Preemphasis), len(goTrace.PrefilterComb), len(goTrace.PrefilterNoop), cTrace.counts())
	}
	for i, got := range goTrace.BandStages {
		want, log := cTrace.Bands[i], cTrace.Logs[i]
		if got.FrameCoeffs != want.FrameCoeffs || got.Bands != want.Bands || got.Channels != want.Channels || got.LM != want.LM ||
			log.Bands != got.Bands || log.Channels != got.Channels {
			return fmt.Errorf("band/log stage %d dimensions differ: Go=(%d,%d,%d,LM%d) C-band=(%d,%d,%d,LM%d) C-log=(%d,%d)",
				i, got.FrameCoeffs, got.Bands, got.Channels, got.LM, want.FrameCoeffs, want.Bands, want.Channels, want.LM, log.Bands, log.Channels)
		}
		bandCount := got.Bands * got.Channels
		if len(got.Spectrum) != got.FrameCoeffs*got.Channels || len(want.Spectrum) != got.FrameCoeffs*got.Channels ||
			len(got.LogEnergy) != bandCount || len(log.LogEnergy) != bandCount || len(log.Amplitudes) != bandCount ||
			len(want.Amplitudes) != bandCount {
			return fmt.Errorf("band/log stage %d has malformed payload lengths: Go spectrum/log/amplitude=%d/%d/%d C spectrum/amplitude/log=%d/%d/%d",
				i, len(got.Spectrum), len(got.LogEnergy), len(got.Amplitudes), len(want.Spectrum), len(want.Amplitudes), len(log.LogEnergy))
		}
		if got.HasAmplitudes {
			if len(got.Amplitudes) != bandCount {
				return fmt.Errorf("band stage %d marks amplitudes available with length %d, want %d", i, len(got.Amplitudes), bandCount)
			}
		} else if len(got.Amplitudes) != 0 {
			return fmt.Errorf("band stage %d marks amplitudes unavailable but captured %d values", i, len(got.Amplitudes))
		}
	}
	for i, got := range goTrace.Normalizations {
		want := cTrace.Normalizations[i]
		if got.ActiveCoeffs != want.ActiveCoeffs || got.Bands != want.Bands || got.Channels != want.Channels {
			return fmt.Errorf("normalization stage %d dimensions differ: Go=(%d,%d,%d) C=(%d,%d,%d)",
				i, got.ActiveCoeffs, got.Bands, got.Channels, want.ActiveCoeffs, want.Bands, want.Channels)
		}
		if len(got.BandEnergy) != got.Bands*got.Channels || len(want.BandEnergy) != got.Bands*got.Channels ||
			len(got.Normalized) != got.ActiveCoeffs*got.Channels || len(want.Normalized) != got.ActiveCoeffs*got.Channels {
			return fmt.Errorf("normalization stage %d has malformed Go/C band-energy or normalized lengths", i)
		}
	}
	for i, got := range goTrace.CoarseEnergy {
		want := cTrace.Coarse[i]
		if got.Bands != want.Bands || got.Channels != want.Channels || got.BudgetBytes != want.BudgetBytes {
			return fmt.Errorf("coarse stage %d dimensions/budget differ: Go=(%d,%d,%d) C=(%d,%d,%d)",
				i, got.Bands, got.Channels, got.BudgetBytes, want.Bands, want.Channels, want.BudgetBytes)
		}
		count := got.Bands * got.Channels
		if len(got.Input) != count || len(got.Quantized) != count || len(got.Error) != count ||
			len(want.Input) != count || len(want.Quantized) != count || len(want.Error) != count {
			return fmt.Errorf("coarse stage %d has malformed Go/C payload lengths", i)
		}
	}
	for i, got := range goTrace.BandQuantize {
		want := cTrace.Quant[i]
		if got.ActiveCoeffs != want.ActiveCoeffs || got.Bands != want.Bands || got.Channels != want.Channels {
			return fmt.Errorf("quantization stage %d dimensions differ: Go=(%d,%d,%d) C=(%d,%d,%d)",
				i, got.ActiveCoeffs, got.Bands, got.Channels, want.ActiveCoeffs, want.Bands, want.Channels)
		}
		if len(got.BandEnergy) != got.Bands*got.Channels || len(want.BandEnergy) != got.Bands*got.Channels ||
			len(got.Input) != got.ActiveCoeffs*got.Channels || len(got.Output) != got.ActiveCoeffs*got.Channels ||
			len(want.Input) != got.ActiveCoeffs*got.Channels || len(want.Output) != got.ActiveCoeffs*got.Channels {
			return fmt.Errorf("quantization stage %d has malformed Go/C payload lengths", i)
		}
	}
	if err := validateCELTMDCTTraceShapes(goTrace, cTrace); err != nil {
		return err
	}
	for i, got := range goTrace.Preemphasis {
		want := cTrace.Preemphasis[i]
		if got.Channel != want.Channel || got.Channels != want.Channels || got.FrameSize != want.FrameSize ||
			got.Upsample != want.Upsample || got.Clip != (want.Flags&1 != 0) ||
			len(got.Input) != len(want.Input) || len(got.Output) != got.FrameSize || len(want.Output) != want.FrameSize {
			return fmt.Errorf("preemphasis call %d dimensions differ: Go=(ch%d/%d frame%d up%d clip=%t input/output=%d/%d) C=(ch%d/%d frame%d up%d clip=%t input/output=%d/%d)",
				i, got.Channel, got.Channels, got.FrameSize, got.Upsample, got.Clip, len(got.Input), len(got.Output),
				want.Channel, want.Channels, want.FrameSize, want.Upsample, want.Flags&1 != 0, len(want.Input), len(want.Output))
		}
	}
	if len(goTrace.Preemphasis) > 0 {
		channels := goTrace.Preemphasis[0].Channels
		if len(goTrace.Preemphasis) != channels {
			return fmt.Errorf("preemphasis captured %d channel calls, want %d", len(goTrace.Preemphasis), channels)
		}
		for channel, got := range goTrace.Preemphasis {
			if got.Channel != channel || got.Channels != channels || cTrace.Preemphasis[channel].Channel != channel {
				return fmt.Errorf("preemphasis channel call %d is out of order: Go=%d/%d C=%d", channel, got.Channel, got.Channels, cTrace.Preemphasis[channel].Channel)
			}
		}
	}
	for i, got := range goTrace.PrefilterComb {
		want := cTrace.Prefilter[i]
		if got.T0 != want.T0 || got.T1 != want.T1 || got.N != want.N || got.Tapset0 != want.Tapset0 ||
			got.Tapset1 != want.Tapset1 || got.Overlap != want.Overlap || got.WindowNil != want.WindowNil ||
			got.Start < 1024 || len(got.History) != 1024 || len(want.History) != 1024 ||
			len(got.Input) != got.N || len(want.Input) != got.N || len(got.Output) != got.N || len(want.Output) != got.N ||
			len(got.Window) != len(want.Window) {
			return fmt.Errorf("prefilter comb call %d dimensions/controls differ: Go=(ch%d start%d t=%d/%d n%d taps=%d/%d ov%d hist/input/window/output=%d/%d/%d/%d) C=(t=%d/%d n%d taps=%d/%d ov%d hist/input/window/output=%d/%d/%d/%d arch=%d)",
				i, got.Channel, got.Start, got.T0, got.T1, got.N, got.Tapset0, got.Tapset1, got.Overlap,
				len(got.History), len(got.Input), len(got.Window), len(got.Output),
				want.T0, want.T1, want.N, want.Tapset0, want.Tapset1, want.Overlap,
				len(want.History), len(want.Input), len(want.Window), len(want.Output), want.Arch)
		}
	}
	if len(goTrace.PrefilterNoop) > 0 {
		if err := validateCELTPrefilterNoop(goTrace.PrefilterNoop, cTrace); err != nil {
			return err
		}
	}
	return nil
}

func validateCELTPrefilterNoop(noops []celt.EncodePrefilterNoopTrace, cTrace celtCBRStageTrace) error {
	if len(noops) == 0 {
		return fmt.Errorf("missing Go no-op prefilter boundary")
	}
	channels := len(noops)
	if channels > 2 || channels != len(cTrace.Preemphasis) {
		return fmt.Errorf("Go no-op prefilter channels=%d, want %d from preemphasis trace", channels, len(cTrace.Preemphasis))
	}
	first := noops[0]
	if first.FrameSize <= 0 || first.Overlap < 0 || first.Overlap > first.FrameSize || first.Offset < 0 || first.Offset > first.FrameSize ||
		first.Offset > len(first.History) || first.Offset+first.Overlap > first.FrameSize || len(first.History) == 0 || len(first.Window) != first.Overlap {
		return fmt.Errorf("invalid Go no-op prefilter geometry frame=%d overlap=%d offset=%d history=%d",
			first.FrameSize, first.Overlap, first.Offset, len(first.History))
	}
	if first.Gain0 != 0 || first.Gain1 != 0 {
		return fmt.Errorf("Go no-op prefilter boundary has nonzero gains %08x/%08x",
			math.Float32bits(first.Gain0), math.Float32bits(first.Gain1))
	}
	if len(first.Input) != first.FrameSize || len(first.Frame) != first.FrameSize ||
		!sameCELTTraceFloat32Bits(first.Input, first.Frame) {
		return fmt.Errorf("Go no-op prefilter input/current frame do not match exactly")
	}
	for channel, got := range noops {
		if got.Channel != channel || got.FrameSize != first.FrameSize || got.Overlap != first.Overlap || got.Offset != first.Offset ||
			got.T0 != first.T0 || got.T1 != first.T1 || got.Tapset0 != first.Tapset0 || got.Tapset1 != first.Tapset1 ||
			math.Float32bits(got.Gain0) != math.Float32bits(first.Gain0) || math.Float32bits(got.Gain1) != math.Float32bits(first.Gain1) ||
			len(got.History) != len(first.History) || len(got.Input) != first.FrameSize || len(got.Frame) != first.FrameSize || len(got.Window) != first.Overlap {
			return fmt.Errorf("Go no-op prefilter channel %d does not share bounded frame geometry/controls", channel)
		}
		if got.Gain0 != 0 || got.Gain1 != 0 {
			return fmt.Errorf("Go no-op prefilter channel %d has nonzero gains", channel)
		}
		if !sameCELTTraceFloat32Bits(got.Input, got.Frame) {
			return fmt.Errorf("Go no-op prefilter channel %d input/current frame do not match exactly", channel)
		}
	}
	basePerChannel := 1
	if first.Offset > 0 {
		basePerChannel++
	}
	baseCalls := channels * basePerChannel
	wantCancel := celtTraceNoopWouldCancel(noops)
	wantCalls := baseCalls
	if wantCancel {
		wantCalls += channels
	}
	if len(cTrace.Prefilter) != wantCalls || cTrace.PrefilterCalls != wantCalls {
		return fmt.Errorf("Go no-op boundaries predict %d exact C comb calls (cancel=%t), got raw/stored=%d/%d",
			wantCalls, wantCancel, cTrace.PrefilterCalls, len(cTrace.Prefilter))
	}
	for i, got := range cTrace.Prefilter {
		channel, phase := 0, 0
		cancel := i >= baseCalls
		if cancel {
			channel = i - baseCalls
			phase = 2
		} else {
			channel = i / basePerChannel
			phase = i % basePerChannel
		}
		want := noops[channel]
		start, n, t0, t1, tapset0, tapset1, overlap, windowNil, gain0, gain1 := 0, 0, want.T0, want.T1, want.Tapset0, want.Tapset1, want.Overlap, false, want.Gain0, want.Gain1
		switch {
		case cancel:
			start, n, gain1 = want.Offset, want.Overlap, 0
		case want.Offset > 0 && phase == 0:
			start, n, t1, tapset1, overlap, windowNil, gain1 = 0, want.Offset, want.T0, want.Tapset0, 0, true, want.Gain0
		case want.Offset > 0:
			start, n = want.Offset, want.FrameSize-want.Offset
		default:
			start, n = 0, want.FrameSize
		}
		if got.Gain0 != 0 || got.Gain1 != 0 {
			return fmt.Errorf("C comb call %d has nonzero gain; Go no-op bypass is not equivalent (%08x/%08x)",
				i, math.Float32bits(got.Gain0), math.Float32bits(got.Gain1))
		}
		if got.N != n || got.T0 != t0 || got.T1 != t1 || got.Tapset0 != tapset0 || got.Tapset1 != tapset1 ||
			got.Overlap != overlap || got.WindowNil != windowNil || got.Gain0 != gain0 || got.Gain1 != gain1 {
			return fmt.Errorf("C zero-gain comb call %d geometry/controls differ: n=%d t=%d/%d taps=%d/%d overlap=%d nilwindow=%t gains=%08x/%08x",
				i, got.N, got.T0, got.T1, got.Tapset0, got.Tapset1, got.Overlap, got.WindowNil,
				math.Float32bits(got.Gain0), math.Float32bits(got.Gain1))
		}
		if got.Overlap < 0 || got.Overlap > want.Overlap || len(got.History) != len(want.History) ||
			len(got.Input) != n || len(got.Output) != n {
			return fmt.Errorf("C zero-gain comb call %d malformed geometry/history/input/output lengths=%d/%d/%d/%d",
				i, got.Overlap, len(got.History), len(got.Input), len(got.Output))
		}
		for j, value := range got.History {
			var expected float32
			if j+start < len(want.History) {
				expected = want.History[j+start]
			} else {
				expected = want.Input[j+start-len(want.History)]
			}
			if math.Float32bits(value) != math.Float32bits(expected) {
				return fmt.Errorf("C zero-gain comb call %d history differs at %d: Go=0x%08x C=0x%08x",
					i, j, math.Float32bits(expected), math.Float32bits(value))
			}
		}
		expectedInput := want.Input[start : start+n]
		expectedFrame := want.Frame[start : start+n]
		if !sameCELTTraceFloat32Bits(got.Input, expectedInput) {
			return fmt.Errorf("C zero-gain comb call %d source input differs from Go frame at offset %d", i, start)
		}
		if !sameCELTTraceFloat32Bits(got.Output, expectedInput) || !sameCELTTraceFloat32Bits(got.Output, expectedFrame) {
			return fmt.Errorf("C zero-gain comb call %d output is not the exact copied Go frame at offset %d", i, start)
		}
		if windowNil {
			if len(got.Window) != 0 {
				return fmt.Errorf("C zero-gain comb call %d has %d unexpected window values", i, len(got.Window))
			}
		} else if len(got.Window) != len(want.Window) || !sameCELTTraceFloat32Bits(got.Window, want.Window) {
			return fmt.Errorf("C zero-gain comb call %d window differs from the selected Go mode table", i)
		}
	}
	return nil
}

func celtTraceAbs32Sum(values []float32) float32 {
	var sum float32
	for _, value := range values {
		if value < 0 {
			value = -value
		}
		sum += value
	}
	return sum
}

func celtTraceNoopWouldCancel(noops []celt.EncodePrefilterNoopTrace) bool {
	if len(noops) != 2 {
		return false
	}
	before0 := celtTraceAbs32Sum(noops[0].Frame)
	before1 := celtTraceAbs32Sum(noops[1].Frame)
	gain := noops[0].Gain1
	quarterGain := float32(0.25) * gain
	threshold0 := quarterGain*before0 + float32(0.01)*before1
	threshold1 := quarterGain*before1 + float32(0.01)*before0
	// A zero-gain C comb call copies its input. Compute both branch deltas
	// explicitly so the source's Inf-Inf behavior remains NaN when a finite
	// input vector overflows its serial ABS32 sum.
	after0, after1 := before0, before1
	increase0, increase1 := after0-before0, after1-before1
	decrease0, decrease1 := before0-after0, before1-after1
	if increase0 > threshold0 || increase1 > threshold1 {
		return true
	}
	return decrease0 < threshold0 && decrease1 < threshold1
}

func sameCELTTraceFloat32Bits(got, want []float32) bool {
	index, _, _ := firstCELTTraceFloatDifference(got, want)
	return index < 0
}

func validateCELTMDCTTraceShapes(goTrace celt.EncodeStageTrace, cTrace celtCBRStageTrace) error {
	if goTrace.MDCTOverflow {
		return fmt.Errorf("Go MDCT trace exceeded its bounded capture")
	}
	if len(goTrace.MDCTCalls) != cTrace.MDCTCalls || len(goTrace.MDCTCalls) != len(cTrace.MDCT) {
		return fmt.Errorf("MDCT call counts differ: Go=%d C=%d/%d", len(goTrace.MDCTCalls), cTrace.MDCTCalls, len(cTrace.MDCT))
	}
	for i, got := range goTrace.MDCTCalls {
		want := cTrace.MDCT[i]
		if got.TransformN != want.TransformN || got.Shift != want.Shift || got.LookupN != want.LookupN ||
			got.MaxShift != want.MaxShift || got.Stride != want.Stride || got.Overlap != want.Overlap || got.FFTSize != want.FFTSize {
			return fmt.Errorf("MDCT call %d geometry differs: Go=(lookup=%d maxshift=%d n=%d shift=%d stride=%d overlap=%d fft=%d) C=(lookup=%d maxshift=%d n=%d shift=%d stride=%d overlap=%d fft=%d)",
				i, got.LookupN, got.MaxShift, got.TransformN, got.Shift, got.Stride, got.Overlap, got.FFTSize,
				want.LookupN, want.MaxShift, want.TransformN, want.Shift, want.Stride, want.Overlap, want.FFTSize)
		}
		if got.TransformN <= 0 || got.TransformN%4 != 0 || got.FFTSize != got.TransformN/4 ||
			got.LookupN != got.TransformN<<got.Shift || got.Shift < 0 || got.Shift > got.MaxShift || got.Stride <= 0 || got.Overlap < 0 {
			return fmt.Errorf("MDCT call %d has invalid Go geometry: %+v", i, got)
		}
		if len(got.Input) != got.TransformN/2+got.Overlap || len(want.Input) != want.TransformN/2+want.Overlap ||
			len(got.Window) != got.Overlap || len(want.Window) != want.Overlap ||
			len(got.Trig) != got.TransformN/2 || len(want.Trig) != want.TransformN/2 {
			return fmt.Errorf("MDCT call %d payload lengths differ from transform shape: Go input/window/trig=%d/%d/%d C=%d/%d/%d",
				i, len(got.Input), len(got.Window), len(got.Trig), len(want.Input), len(want.Window), len(want.Trig))
		}
	}
	return nil
}

func validateCELTMDCTTraceExpectedDimensions(goTrace celt.EncodeStageTrace, cTrace celtCBRStageTrace, overlap, channels, transformN, maxShift, shift int) error {
	if err := validateCELTMDCTTraceShapes(goTrace, cTrace); err != nil {
		return err
	}
	if len(goTrace.MDCTCalls) != channels {
		return fmt.Errorf("captured %d MDCT calls, want %d channels", len(goTrace.MDCTCalls), channels)
	}
	for i, got := range goTrace.MDCTCalls {
		want := cTrace.MDCT[i]
		if got.Channel != i || got.Block != 0 || got.TransformN != transformN || got.MaxShift != maxShift || got.Shift != shift ||
			got.Stride != 1 || got.Overlap != overlap || got.FFTSize != transformN/4 || got.LookupN != transformN<<shift {
			return fmt.Errorf("MDCT call %d is Go=(channel=%d block=%d lookup=%d maxshift=%d n=%d shift=%d stride=%d overlap=%d fft=%d) C=(lookup=%d maxshift=%d n=%d shift=%d stride=%d overlap=%d fft=%d)",
				i, got.Channel, got.Block, got.LookupN, got.MaxShift, got.TransformN, got.Shift, got.Stride, got.Overlap, got.FFTSize,
				want.LookupN, want.MaxShift, want.TransformN, want.Shift, want.Stride, want.Overlap, want.FFTSize)
		}
	}
	return nil
}

func validateCELTTraceExpectedDimensions(goTrace celt.EncodeStageTrace, cTrace celtCBRStageTrace, frameCoeffs, bands, channels, active, lm int) error {
	for i, got := range goTrace.BandStages {
		if got.FrameCoeffs != frameCoeffs || got.Bands != bands || got.Channels != channels || got.LM != lm {
			return fmt.Errorf("Go band stage %d dimensions are frame=%d bands=%d channels=%d LM=%d, want %d/%d/%d/LM%d",
				i, got.FrameCoeffs, got.Bands, got.Channels, got.LM, frameCoeffs, bands, channels, lm)
		}
		want := cTrace.Bands[i]
		if want.FrameCoeffs != frameCoeffs || want.Bands != bands || want.Channels != channels || want.LM != lm {
			return fmt.Errorf("C band stage %d dimensions are frame=%d bands=%d channels=%d LM=%d, want %d/%d/%d/LM%d",
				i, want.FrameCoeffs, want.Bands, want.Channels, want.LM, frameCoeffs, bands, channels, lm)
		}
	}
	for i, got := range goTrace.Normalizations {
		if got.ActiveCoeffs != active || got.Bands != bands || got.Channels != channels {
			return fmt.Errorf("Go normalization stage %d dimensions are active=%d bands=%d channels=%d, want %d/%d/%d",
				i, got.ActiveCoeffs, got.Bands, got.Channels, active, bands, channels)
		}
		want := cTrace.Normalizations[i]
		if want.ActiveCoeffs != active || want.Bands != bands || want.Channels != channels {
			return fmt.Errorf("C normalization stage %d dimensions are active=%d bands=%d channels=%d, want %d/%d/%d",
				i, want.ActiveCoeffs, want.Bands, want.Channels, active, bands, channels)
		}
	}
	for i, got := range goTrace.CoarseEnergy {
		if got.Bands != bands || got.Channels != channels {
			return fmt.Errorf("Go coarse stage %d dimensions are bands=%d channels=%d, want %d/%d", i, got.Bands, got.Channels, bands, channels)
		}
		want := cTrace.Coarse[i]
		if want.Bands != bands || want.Channels != channels {
			return fmt.Errorf("C coarse stage %d dimensions are bands=%d channels=%d, want %d/%d", i, want.Bands, want.Channels, bands, channels)
		}
	}
	for i, got := range goTrace.BandQuantize {
		if got.ActiveCoeffs != active || got.Bands != bands || got.Channels != channels {
			return fmt.Errorf("Go quantization stage %d dimensions are active=%d bands=%d channels=%d, want %d/%d/%d",
				i, got.ActiveCoeffs, got.Bands, got.Channels, active, bands, channels)
		}
		want := cTrace.Quant[i]
		if want.ActiveCoeffs != active || want.Bands != bands || want.Channels != channels {
			return fmt.Errorf("C quantization stage %d dimensions are active=%d bands=%d channels=%d, want %d/%d/%d",
				i, want.ActiveCoeffs, want.Bands, want.Channels, active, bands, channels)
		}
	}
	return nil
}

func (trace celtCBRStageTrace) counts() string {
	return fmt.Sprintf("bands=%d/%d logs=%d/%d normalize=%d/%d coarse=%d/%d quant=%d/%d mdct=%d/%d preemphasis=%d/%d prefilter=%d/%d overflow=%d",
		trace.BandCalls, len(trace.Bands), trace.LogCalls, len(trace.Logs),
		trace.NormalizationCalls, len(trace.Normalizations), trace.CoarseCalls, len(trace.Coarse),
		trace.QuantCalls, len(trace.Quant), trace.MDCTCalls, len(trace.MDCT),
		trace.PreemphasisCalls, len(trace.Preemphasis), trace.PrefilterCalls, len(trace.Prefilter), trace.Overflow)
}

type celtTraceReader struct {
	data []byte
	off  int
}

func (reader *celtTraceReader) u32() (uint32, error) {
	value, ok := celtTraceReadU32(reader.data, &reader.off)
	if !ok {
		return 0, fmt.Errorf("truncated trace u32 at offset %d", reader.off)
	}
	return value, nil
}

func (reader *celtTraceReader) floats(count int) ([]float32, error) {
	if count < 0 || count > 4096 || count > (len(reader.data)-reader.off)/4 {
		return nil, fmt.Errorf("invalid trace float count %d at offset %d", count, reader.off)
	}
	values := make([]float32, count)
	for i := range count {
		values[i] = math.Float32frombits(binary.LittleEndian.Uint32(reader.data[reader.off+i*4:]))
	}
	reader.off += count * 4
	return values, nil
}

func parseCELTEncodeTrace(data []byte) (celtCBRStageTrace, error) {
	var result celtCBRStageTrace
	if len(data) < 12 || string(data[:4]) != "GCET" {
		return result, fmt.Errorf("invalid GCET stage trace header")
	}
	if version := binary.LittleEndian.Uint32(data[4:8]); version != 4 {
		return result, fmt.Errorf("invalid GCET v4 stage trace version %d", version)
	}
	reader := celtTraceReader{data: data, off: 8}
	var err error
	if result.TraceFrame, err = reader.u32(); err != nil {
		return result, err
	}
	if result.Overflow, err = reader.u32(); err != nil {
		return result, err
	}
	readCounts := func() (int, int, error) {
		total, readErr := reader.u32()
		if readErr != nil {
			return 0, 0, readErr
		}
		stored, readErr := reader.u32()
		if readErr != nil {
			return 0, 0, readErr
		}
		if total > 8 || stored > total || stored != total {
			return 0, 0, fmt.Errorf("invalid C stage call counts total=%d stored=%d", total, stored)
		}
		return int(total), int(stored), nil
	}
	traceFloatCount := func(a, b uint32) (int, error) {
		count := uint64(a) * uint64(b)
		if count > 4096 {
			return 0, fmt.Errorf("C stage array too large: %d × %d", a, b)
		}
		return int(count), nil
	}
	if result.BandCalls, _, err = readCounts(); err != nil {
		return result, err
	}
	result.Bands = make([]celtCBRStageBand, 0, result.BandCalls)
	for range result.BandCalls {
		frameCoeffs, readErr := reader.u32()
		if readErr != nil {
			return result, readErr
		}
		bands, readErr := reader.u32()
		if readErr != nil {
			return result, readErr
		}
		channels, readErr := reader.u32()
		if readErr != nil {
			return result, readErr
		}
		lm, readErr := reader.u32()
		if readErr != nil {
			return result, readErr
		}
		spectrumCount, readErr := traceFloatCount(frameCoeffs, channels)
		if readErr != nil {
			return result, readErr
		}
		bandCount, readErr := traceFloatCount(bands, channels)
		if readErr != nil {
			return result, readErr
		}
		spectrum, readErr := reader.floats(spectrumCount)
		if readErr != nil {
			return result, readErr
		}
		amplitudes, readErr := reader.floats(bandCount)
		if readErr != nil {
			return result, readErr
		}
		result.Bands = append(result.Bands, celtCBRStageBand{
			FrameCoeffs: int(frameCoeffs), Bands: int(bands), Channels: int(channels), LM: int(lm),
			Spectrum: spectrum, Amplitudes: amplitudes,
		})
	}
	if result.LogCalls, _, err = readCounts(); err != nil {
		return result, err
	}
	result.Logs = make([]celtCBRStageLog, 0, result.LogCalls)
	for range result.LogCalls {
		bands, readErr := reader.u32()
		if readErr != nil {
			return result, readErr
		}
		channels, readErr := reader.u32()
		if readErr != nil {
			return result, readErr
		}
		count, readErr := traceFloatCount(bands, channels)
		if readErr != nil {
			return result, readErr
		}
		amplitudes, readErr := reader.floats(count)
		if readErr != nil {
			return result, readErr
		}
		logEnergy, readErr := reader.floats(count)
		if readErr != nil {
			return result, readErr
		}
		result.Logs = append(result.Logs, celtCBRStageLog{Bands: int(bands), Channels: int(channels), Amplitudes: amplitudes, LogEnergy: logEnergy})
	}
	if result.NormalizationCalls, _, err = readCounts(); err != nil {
		return result, err
	}
	result.Normalizations = make([]celtCBRStageNorm, 0, result.NormalizationCalls)
	for range result.NormalizationCalls {
		active, readErr := reader.u32()
		if readErr != nil {
			return result, readErr
		}
		bands, readErr := reader.u32()
		if readErr != nil {
			return result, readErr
		}
		channels, readErr := reader.u32()
		if readErr != nil {
			return result, readErr
		}
		bandCount, readErr := traceFloatCount(bands, channels)
		if readErr != nil {
			return result, readErr
		}
		coeffCount, readErr := traceFloatCount(active, channels)
		if readErr != nil {
			return result, readErr
		}
		bandEnergy, readErr := reader.floats(bandCount)
		if readErr != nil {
			return result, readErr
		}
		normalized, readErr := reader.floats(coeffCount)
		if readErr != nil {
			return result, readErr
		}
		result.Normalizations = append(result.Normalizations, celtCBRStageNorm{
			ActiveCoeffs: int(active), Bands: int(bands), Channels: int(channels),
			BandEnergy: bandEnergy, Normalized: normalized,
		})
	}
	if result.CoarseCalls, _, err = readCounts(); err != nil {
		return result, err
	}
	result.Coarse = make([]celtCBRStageCoarse, 0, result.CoarseCalls)
	for range result.CoarseCalls {
		bands, readErr := reader.u32()
		if readErr != nil {
			return result, readErr
		}
		channels, readErr := reader.u32()
		if readErr != nil {
			return result, readErr
		}
		budget, readErr := reader.u32()
		if readErr != nil {
			return result, readErr
		}
		count, readErr := traceFloatCount(bands, channels)
		if readErr != nil {
			return result, readErr
		}
		input, readErr := reader.floats(count)
		if readErr != nil {
			return result, readErr
		}
		quantized, readErr := reader.floats(count)
		if readErr != nil {
			return result, readErr
		}
		errorValues, readErr := reader.floats(count)
		if readErr != nil {
			return result, readErr
		}
		result.Coarse = append(result.Coarse, celtCBRStageCoarse{
			Bands: int(bands), Channels: int(channels), BudgetBytes: int(budget),
			Input: input, Quantized: quantized, Error: errorValues,
		})
	}
	if result.QuantCalls, _, err = readCounts(); err != nil {
		return result, err
	}
	result.Quant = make([]celtCBRStageQuant, 0, result.QuantCalls)
	for range result.QuantCalls {
		active, readErr := reader.u32()
		if readErr != nil {
			return result, readErr
		}
		bands, readErr := reader.u32()
		if readErr != nil {
			return result, readErr
		}
		channels, readErr := reader.u32()
		if readErr != nil {
			return result, readErr
		}
		bandCount, readErr := traceFloatCount(bands, channels)
		if readErr != nil {
			return result, readErr
		}
		coeffCount, readErr := traceFloatCount(active, channels)
		if readErr != nil {
			return result, readErr
		}
		bandEnergy, readErr := reader.floats(bandCount)
		if readErr != nil {
			return result, readErr
		}
		input, readErr := reader.floats(coeffCount)
		if readErr != nil {
			return result, readErr
		}
		output, readErr := reader.floats(coeffCount)
		if readErr != nil {
			return result, readErr
		}
		result.Quant = append(result.Quant, celtCBRStageQuant{
			ActiveCoeffs: int(active), Bands: int(bands), Channels: int(channels),
			BandEnergy: bandEnergy, Input: input, Output: output,
		})
	}
	if result.MDCTCalls, _, err = readCounts(); err != nil {
		return result, err
	}
	result.MDCT = make([]celtCBRStageMDCT, 0, result.MDCTCalls)
	for range result.MDCTCalls {
		values := make([]uint32, 12)
		for i := range values {
			if values[i], err = reader.u32(); err != nil {
				return result, err
			}
		}
		input, readErr := reader.floats(int(values[9]))
		if readErr != nil {
			return result, readErr
		}
		window, readErr := reader.floats(int(values[10]))
		if readErr != nil {
			return result, readErr
		}
		trig, readErr := reader.floats(int(values[11]))
		if readErr != nil {
			return result, readErr
		}
		result.MDCT = append(result.MDCT, celtCBRStageMDCT{
			LookupN: int(values[0]), MaxShift: int(values[1]), TransformN: int(values[2]), Shift: int(values[3]),
			Stride: int(values[4]), Overlap: int(values[5]), Arch: int(values[6]), FFTSize: int(values[7]),
			FFTScale: math.Float32frombits(values[8]), Input: input, Window: window, Trig: trig,
		})
	}
	if result.PreemphasisCalls, _, err = readCounts(); err != nil {
		return result, err
	}
	result.Preemphasis = make([]celtCBRStagePreemphasis, 0, result.PreemphasisCalls)
	for range result.PreemphasisCalls {
		values := make([]uint32, 7)
		for i := range values {
			if values[i], err = reader.u32(); err != nil {
				return result, err
			}
		}
		inputCount, readErr := traceFloatCount(values[4], 1)
		if readErr != nil {
			return result, readErr
		}
		outputCount, readErr := traceFloatCount(values[5], 1)
		if readErr != nil {
			return result, readErr
		}
		if values[6]&^uint32(1) != 0 {
			return result, fmt.Errorf("invalid C preemphasis flags %#x", values[6])
		}
		stateBeforeBits, readErr := reader.u32()
		if readErr != nil {
			return result, readErr
		}
		stateAfterBits, readErr := reader.u32()
		if readErr != nil {
			return result, readErr
		}
		coefficients, readErr := reader.floats(4)
		if readErr != nil {
			return result, readErr
		}
		input, readErr := reader.floats(inputCount)
		if readErr != nil {
			return result, readErr
		}
		output, readErr := reader.floats(outputCount)
		if readErr != nil {
			return result, readErr
		}
		if values[1] == 0 || values[0] >= values[1] || values[3] == 0 || values[2] != values[5] ||
			values[2]%values[3] != 0 || values[4] != values[2]/values[3] {
			return result, fmt.Errorf("invalid C preemphasis dimensions channel=%d channels=%d frame=%d upsample=%d input=%d output=%d",
				values[0], values[1], values[2], values[3], values[4], values[5])
		}
		result.Preemphasis = append(result.Preemphasis, celtCBRStagePreemphasis{
			Channel: int(values[0]), Channels: int(values[1]), FrameSize: int(values[2]), Upsample: int(values[3]),
			Flags: values[6], StateBefore: math.Float32frombits(stateBeforeBits), StateAfter: math.Float32frombits(stateAfterBits),
			Coefficients: [4]float32{coefficients[0], coefficients[1], coefficients[2], coefficients[3]},
			Input:        input, Output: output,
		})
	}
	if result.PrefilterCalls, _, err = readCounts(); err != nil {
		return result, err
	}
	result.Prefilter = make([]celtCBRStagePrefilter, 0, result.PrefilterCalls)
	for range result.PrefilterCalls {
		values := make([]uint32, 12)
		for i := range values {
			if values[i], err = reader.u32(); err != nil {
				return result, err
			}
		}
		gain0Bits, readErr := reader.u32()
		if readErr != nil {
			return result, readErr
		}
		gain1Bits, readErr := reader.u32()
		if readErr != nil {
			return result, readErr
		}
		historyCount, readErr := traceFloatCount(values[8], 1)
		if readErr != nil {
			return result, readErr
		}
		inputCount, readErr := traceFloatCount(values[9], 1)
		if readErr != nil {
			return result, readErr
		}
		windowCount, readErr := traceFloatCount(values[10], 1)
		if readErr != nil {
			return result, readErr
		}
		outputCount, readErr := traceFloatCount(values[11], 1)
		if readErr != nil {
			return result, readErr
		}
		if values[7] > 1 || values[8] != 1024 || values[2] != values[9] || values[2] != values[11] ||
			(values[7] == 0 && values[10] != values[5]) || (values[7] == 1 && values[10] != 0) {
			return result, fmt.Errorf("invalid C prefilter dimensions n=%d overlap=%d history/input/window/output=%d/%d/%d/%d",
				values[2], values[5], values[8], values[9], values[10], values[11])
		}
		history, readErr := reader.floats(historyCount)
		if readErr != nil {
			return result, readErr
		}
		input, readErr := reader.floats(inputCount)
		if readErr != nil {
			return result, readErr
		}
		window, readErr := reader.floats(windowCount)
		if readErr != nil {
			return result, readErr
		}
		output, readErr := reader.floats(outputCount)
		if readErr != nil {
			return result, readErr
		}
		result.Prefilter = append(result.Prefilter, celtCBRStagePrefilter{
			T0: int32(values[0]), T1: int32(values[1]), N: int(values[2]), Tapset0: int32(values[3]),
			Tapset1: int32(values[4]), Overlap: int(values[5]), Arch: int(values[6]), WindowNil: values[7] == 1,
			Gain0: math.Float32frombits(gain0Bits), Gain1: math.Float32frombits(gain1Bits),
			History: history, Input: input, Window: window, Output: output,
		})
	}
	if reader.off != len(reader.data) {
		return result, fmt.Errorf("CELT stage trace has %d trailing bytes", len(reader.data)-reader.off)
	}
	return result, nil
}

func firstCELTTraceFloatDifference(got, want []float32) (index int, gotBits, wantBits uint32) {
	limit := min(len(got), len(want))
	for i := 0; i < limit; i++ {
		gotBits, wantBits = math.Float32bits(got[i]), math.Float32bits(want[i])
		if gotBits != wantBits {
			return i, gotBits, wantBits
		}
	}
	if len(got) != len(want) {
		return limit, uint32(len(got)), uint32(len(want))
	}
	return -1, 0, 0
}

func celtTraceFloat32Distance(bits uint32) uint32 {
	if bits&0x80000000 != 0 {
		return ^bits
	}
	return bits | 0x80000000
}

func celtTraceFloat32Stats(got, want []float32) (first int, gotBits, wantBits uint32, differing int, maxULP uint64) {
	first = -1
	limit := min(len(got), len(want))
	for i := 0; i < limit; i++ {
		gotBits, wantBits = math.Float32bits(got[i]), math.Float32bits(want[i])
		if gotBits == wantBits {
			continue
		}
		if first < 0 {
			first, gotBits, wantBits = i, gotBits, wantBits
		}
		differing++
		gotOrder, wantOrder := uint64(celtTraceFloat32Distance(math.Float32bits(got[i]))), uint64(celtTraceFloat32Distance(math.Float32bits(want[i])))
		var ulp uint64
		if gotOrder >= wantOrder {
			ulp = gotOrder - wantOrder
		} else {
			ulp = wantOrder - gotOrder
		}
		if ulp > maxULP {
			maxULP = ulp
		}
	}
	if len(got) != len(want) {
		differing += max(len(got), len(want)) - limit
		if first < 0 {
			first = limit
			gotBits, wantBits = uint32(len(got)), uint32(len(want))
		}
	}
	return first, gotBits, wantBits, differing, maxULP
}

func firstCELTTraceByteDifference(got, want []byte) int {
	limit := min(len(got), len(want))
	for i := 0; i < limit; i++ {
		if got[i] != want[i] {
			return i
		}
	}
	if len(got) != len(want) {
		return limit
	}
	return -1
}

func logCELTTraceDifferences(t *testing.T, goTrace celt.EncodeStageTrace, cTrace celtCBRStageTrace) {
	t.Helper()
	first := ""
	compare := func(label string, got, want []float32) {
		index, gotBits, wantBits, differing, maxULP := celtTraceFloat32Stats(got, want)
		if index < 0 {
			t.Logf("%s: match (%d float32 values)", label, len(got))
			return
		}
		if first == "" {
			first = label
		}
		t.Logf("%s: first difference at %d Go=0x%08x C=0x%08x differing=%d maxULP=%d lengths Go=%d C=%d",
			label, index, gotBits, wantBits, differing, maxULP, len(got), len(want))
	}
	if len(goTrace.Preemphasis) != len(cTrace.Preemphasis) || len(goTrace.PrefilterComb) != len(cTrace.Prefilter) || len(goTrace.PrefilterNoop) > 0 {
		t.Logf("preemphasis/prefilter counts Go PE=%d comb=%d noop-boundaries=%d C PE=%d raw-comb=%d",
			len(goTrace.Preemphasis), len(goTrace.PrefilterComb), len(goTrace.PrefilterNoop), len(cTrace.Preemphasis), cTrace.PrefilterCalls)
	}
	for i := 0; i < min(len(goTrace.Preemphasis), len(cTrace.Preemphasis)); i++ {
		got, want := goTrace.Preemphasis[i], cTrace.Preemphasis[i]
		label := fmt.Sprintf("preemphasis call %d channel %d", i, got.Channel)
		if got.Channel != want.Channel || got.Channels != want.Channels || got.FrameSize != want.FrameSize ||
			got.Upsample != want.Upsample || got.Clip != (want.Flags&1 != 0) {
			if first == "" {
				first = label + " controls"
			}
			t.Logf("%s controls Go=(ch%d/%d frame%d up%d clip=%t) C=(ch%d/%d frame%d up%d clip=%t)",
				label, got.Channel, got.Channels, got.FrameSize, got.Upsample, got.Clip,
				want.Channel, want.Channels, want.FrameSize, want.Upsample, want.Flags&1 != 0)
		}
		compare(label+" raw PCM input", got.Input, want.Input)
		compare(label+" preemphasis output", got.Output, want.Output)
		for coefficient := range got.Coefficients {
			if math.Float32bits(got.Coefficients[coefficient]) != math.Float32bits(want.Coefficients[coefficient]) {
				if first == "" {
					first = fmt.Sprintf("%s coefficient %d", label, coefficient)
				}
				t.Logf("%s coefficient %d Go=0x%08x C=0x%08x", label, coefficient,
					math.Float32bits(got.Coefficients[coefficient]), math.Float32bits(want.Coefficients[coefficient]))
			}
		}
		if math.Float32bits(got.StateBefore) != math.Float32bits(want.StateBefore) ||
			math.Float32bits(got.StateAfter) != math.Float32bits(want.StateAfter) {
			if first == "" {
				first = label + " carry"
			}
			t.Logf("%s carry Go before/after=%08x/%08x C=%08x/%08x", label,
				math.Float32bits(got.StateBefore), math.Float32bits(got.StateAfter),
				math.Float32bits(want.StateBefore), math.Float32bits(want.StateAfter))
		}
	}
	for i := 0; i < min(len(goTrace.PrefilterComb), len(cTrace.Prefilter)); i++ {
		got, want := goTrace.PrefilterComb[i], cTrace.Prefilter[i]
		label := fmt.Sprintf("prefilter comb call %d channel %d", i, got.Channel)
		compare(label+" history[-1024:]", got.History, want.History)
		compare(label+" frame input", got.Input, want.Input)
		if got.T0 != want.T0 || got.T1 != want.T1 || got.N != want.N || got.Tapset0 != want.Tapset0 ||
			got.Tapset1 != want.Tapset1 || got.Overlap != want.Overlap || got.WindowNil != want.WindowNil ||
			math.Float32bits(got.Gain0) != math.Float32bits(want.Gain0) || math.Float32bits(got.Gain1) != math.Float32bits(want.Gain1) {
			if first == "" {
				first = label + " controls"
			}
			t.Logf("%s controls Go=(start%d t=%d/%d n%d gain=%08x/%08x taps=%d/%d ov%d nilwindow=%t) C=(t=%d/%d n%d gain=%08x/%08x taps=%d/%d ov%d nilwindow=%t arch=%d)",
				label, got.Start, got.T0, got.T1, got.N, math.Float32bits(got.Gain0), math.Float32bits(got.Gain1), got.Tapset0, got.Tapset1, got.Overlap, got.WindowNil,
				want.T0, want.T1, want.N, math.Float32bits(want.Gain0), math.Float32bits(want.Gain1), want.Tapset0, want.Tapset1, want.Overlap, want.WindowNil, want.Arch)
		}
		compare(label+" window", got.Window, want.Window)
		compare(label+" output", got.Output, want.Output)
	}
	if len(goTrace.MDCTCalls) != len(cTrace.MDCT) {
		t.Logf("MDCT diagnostic call counts Go=%d C=%d", len(goTrace.MDCTCalls), len(cTrace.MDCT))
	}
	for i := 0; i < min(len(goTrace.MDCTCalls), len(cTrace.MDCT)); i++ {
		got, want := goTrace.MDCTCalls[i], cTrace.MDCT[i]
		if got.FFTScale != want.FFTScale {
			t.Logf("MDCT call %d scale Go=0x%08x C=0x%08x; Go channel=%d block=%d C arch=%d",
				i, math.Float32bits(got.FFTScale), math.Float32bits(want.FFTScale), got.Channel, got.Block, want.Arch)
		}
		compare(fmt.Sprintf("MDCT input call %d channel %d block %d", i, got.Channel, got.Block), got.Input, want.Input)
		compare(fmt.Sprintf("MDCT window call %d channel %d block %d", i, got.Channel, got.Block), got.Window, want.Window)
		compare(fmt.Sprintf("MDCT trig call %d channel %d block %d", i, got.Channel, got.Block), got.Trig, want.Trig)
	}
	for i := range goTrace.BandStages {
		got, want := goTrace.BandStages[i], cTrace.Bands[i]
		if got.FrameCoeffs != want.FrameCoeffs || got.Bands != want.Bands || got.Channels != want.Channels || got.LM != want.LM {
			if first == "" {
				first = fmt.Sprintf("band stage dimensions call %d", i)
			}
			t.Logf("band call %d dimensions Go=(%d,%d,%d,LM%d) C=(%d,%d,%d,LM%d)", i,
				got.FrameCoeffs, got.Bands, got.Channels, got.LM, want.FrameCoeffs, want.Bands, want.Channels, want.LM)
		}
		compare(fmt.Sprintf("MDCT spectrum call %d", i), got.Spectrum, want.Spectrum)
		if got.HasAmplitudes {
			compare(fmt.Sprintf("band amplitudes call %d", i), got.Amplitudes, want.Amplitudes)
		} else {
			t.Logf("band amplitudes call %d: unavailable at this Go stage boundary", i)
		}
		compare(fmt.Sprintf("band log energy call %d", i), got.LogEnergy, cTrace.Logs[i].LogEnergy)
	}
	for i := range goTrace.Normalizations {
		got, want := goTrace.Normalizations[i], cTrace.Normalizations[i]
		if got.ActiveCoeffs != want.ActiveCoeffs || got.Bands != want.Bands || got.Channels != want.Channels {
			if first == "" {
				first = fmt.Sprintf("normalization dimensions call %d", i)
			}
			t.Logf("normalization call %d dimensions Go=(%d,%d,%d) C=(%d,%d,%d)", i,
				got.ActiveCoeffs, got.Bands, got.Channels, want.ActiveCoeffs, want.Bands, want.Channels)
		}
		compare(fmt.Sprintf("normalization band energy call %d", i), got.BandEnergy, want.BandEnergy)
		compare(fmt.Sprintf("normalized coefficients call %d", i), got.Normalized, want.Normalized)
	}
	for i := range goTrace.CoarseEnergy {
		got, want := goTrace.CoarseEnergy[i], cTrace.Coarse[i]
		if got.BudgetBytes != want.BudgetBytes {
			if first == "" {
				first = fmt.Sprintf("coarse budget call %d", i)
			}
			t.Logf("coarse call %d budget Go=%d C=%d", i, got.BudgetBytes, want.BudgetBytes)
		}
		compare(fmt.Sprintf("coarse input call %d", i), got.Input, want.Input)
		compare(fmt.Sprintf("coarse quantized energy call %d", i), got.Quantized, want.Quantized)
		compare(fmt.Sprintf("coarse error call %d", i), got.Error, want.Error)
	}
	for i := range goTrace.BandQuantize {
		got, want := goTrace.BandQuantize[i], cTrace.Quant[i]
		if got.ActiveCoeffs != want.ActiveCoeffs || got.Bands != want.Bands || got.Channels != want.Channels {
			if first == "" {
				first = fmt.Sprintf("band quantization dimensions call %d", i)
			}
			t.Logf("quant call %d dimensions Go=(%d,%d,%d) C=(%d,%d,%d)", i,
				got.ActiveCoeffs, got.Bands, got.Channels, want.ActiveCoeffs, want.Bands, want.Channels)
		}
		compare(fmt.Sprintf("quant input band energy call %d", i), got.BandEnergy, want.BandEnergy)
		compare(fmt.Sprintf("quant input coefficients call %d", i), got.Input, want.Input)
		compare(fmt.Sprintf("quant reconstructed coefficients call %d", i), got.Output, want.Output)
	}
	if first == "" {
		t.Log("first-divergence trace: all captured CELT stages match; mismatch lies after the captured stages or in uncaptured state")
	} else {
		t.Logf("first-divergence trace begins at %s", first)
	}
}
