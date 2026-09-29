//go:build linux && amd64 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext

package encoder

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
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
	if cTrace.BandCalls == 0 || cTrace.LogCalls == 0 || cTrace.NormalizationCalls == 0 || cTrace.CoarseCalls == 0 || cTrace.QuantCalls == 0 {
		t.Fatalf("C wrappers did not cover every target boundary: %+v", cTrace.counts())
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
		len(goTrace.BandQuantize) != cTrace.QuantCalls {
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
	if cTrace.BandCalls == 0 || cTrace.LogCalls == 0 || cTrace.NormalizationCalls == 0 || cTrace.CoarseCalls == 0 || cTrace.QuantCalls == 0 {
		t.Fatalf("C wrappers did not cover every late-frame target boundary: %+v", cTrace.counts())
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
	if len(goTrace.BandStages) != cTrace.BandCalls || len(goTrace.BandStages) != cTrace.LogCalls ||
		len(goTrace.Normalizations) != cTrace.NormalizationCalls || len(goTrace.CoarseEnergy) != cTrace.CoarseCalls ||
		len(goTrace.BandQuantize) != cTrace.QuantCalls {
		t.Fatalf("late-frame Go/C stage call counts differ: Go bands=%d normalize=%d coarse=%d quant=%d; C %s",
			len(goTrace.BandStages), len(goTrace.Normalizations), len(goTrace.CoarseEnergy), len(goTrace.BandQuantize), cTrace.counts())
	}
	if err := validateCELTTraceShapes(goTrace, cTrace); err != nil {
		t.Fatalf("invalid late-frame Go/C CELT trace shapes: %v", err)
	}
	if err := validateCELTTraceExpectedDimensions(goTrace, cTrace, celtLateFrameSize, celtTraceBandCount, celtLateChannels, celtTraceActive/2, 0); err != nil {
		t.Fatalf("unexpected late-frame Go/C CELT trace dimensions: %v", err)
	}
	t.Logf("late-frame result: frame=%d Go packet bytes=%d C bytes=%d first packet byte diff=%d Go range=%08x C range=%08x",
		celtLateTraceFrame, len(goPacket), len(ordinary.Packets[celtLateTraceFrame]), firstCELTTraceByteDifference(goPacket, ordinary.Packets[celtLateTraceFrame]), goEncoder.FinalRange(), ordinary.FinalRanges[celtLateTraceFrame])
	t.Log("late-frame stage-bit comparisons are diagnostic and do not establish packet parity")
	logCELTTraceDifferences(t, goTrace, cTrace)
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
	if trace {
		if traceFrame == 0 {
			config.OutputBase = "gopus_libopus_cbr_celt_first_frame_trace_wrapped"
		} else {
			config.OutputBase = fmt.Sprintf("gopus_libopus_cbr_celt_late_frame_%d_trace_wrapped", traceFrame)
		}
		config.CFlags = append(config.CFlags, "-DGOPUS_CELT_TRACE", fmt.Sprintf("-DGOPUS_CELT_TRACE_FRAME=%d", traceFrame))
		config.LDFlags = []string{
			"-Wl,--wrap=compute_band_energies",
			"-Wl,--wrap=amp2Log2",
			"-Wl,--wrap=normalise_bands",
			"-Wl,--wrap=quant_coarse_energy",
			"-Wl,--wrap=quant_all_bands",
		}
	}
	cache := &celtTraceOracleCache
	if trace && traceFrame == 0 {
		cache = &celtTraceWrappedOracleCache
	} else if trace {
		cache = &celtLateTraceOracleCache
	}
	path, err := cache.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(config)
	})
	if err != nil {
		libopustest.HelperUnavailable(t, config.Label, err)
	}
	return path
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

type celtCBRStageTrace struct {
	TraceFrame                                                       uint32
	Overflow                                                         uint32
	BandCalls, LogCalls, NormalizationCalls, CoarseCalls, QuantCalls int
	Bands                                                            []celtCBRStageBand
	Logs                                                             []celtCBRStageLog
	Normalizations                                                   []celtCBRStageNorm
	Coarse                                                           []celtCBRStageCoarse
	Quant                                                            []celtCBRStageQuant
}

type celtCBRStageLog struct {
	Bands      int
	Channels   int
	Amplitudes []float32
	LogEnergy  []float32
}

func validateCELTTraceShapes(goTrace celt.EncodeStageTrace, cTrace celtCBRStageTrace) error {
	if len(goTrace.BandStages) != cTrace.BandCalls || len(goTrace.BandStages) != len(cTrace.Bands) ||
		len(goTrace.BandStages) != cTrace.LogCalls || len(goTrace.BandStages) != len(cTrace.Logs) ||
		len(goTrace.Normalizations) != cTrace.NormalizationCalls || len(goTrace.Normalizations) != len(cTrace.Normalizations) ||
		len(goTrace.CoarseEnergy) != cTrace.CoarseCalls || len(goTrace.CoarseEnergy) != len(cTrace.Coarse) ||
		len(goTrace.BandQuantize) != cTrace.QuantCalls || len(goTrace.BandQuantize) != len(cTrace.Quant) {
		return fmt.Errorf("stage counts differ: Go bands/logs/norm/coarse/quant=%d/%d/%d/%d/%d, C=%s",
			len(goTrace.BandStages), len(cTrace.Logs), len(goTrace.Normalizations), len(goTrace.CoarseEnergy), len(goTrace.BandQuantize), cTrace.counts())
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
	return fmt.Sprintf("bands=%d/%d logs=%d/%d normalize=%d/%d coarse=%d/%d quant=%d/%d overflow=%d",
		trace.BandCalls, len(trace.Bands), trace.LogCalls, len(trace.Logs),
		trace.NormalizationCalls, len(trace.Normalizations), trace.CoarseCalls, len(trace.Coarse),
		trace.QuantCalls, len(trace.Quant), trace.Overflow)
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
	if len(data) < 12 || string(data[:4]) != "GCET" || binary.LittleEndian.Uint32(data[4:8]) != 2 {
		return result, fmt.Errorf("invalid GCET v2 stage trace")
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
		index, gotBits, wantBits := firstCELTTraceFloatDifference(got, want)
		if index < 0 {
			t.Logf("%s: match (%d float32 values)", label, len(got))
			return
		}
		if first == "" {
			first = label
		}
		t.Logf("%s: first difference at %d Go=0x%08x C=0x%08x lengths Go=%d C=%d", label, index, gotBits, wantBits, len(got), len(want))
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
