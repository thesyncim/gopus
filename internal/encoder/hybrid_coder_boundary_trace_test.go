//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext

package encoder

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
)

const (
	hybridBoundaryFrameSize   = 960
	hybridBoundaryChannels    = 2
	hybridBoundaryBitrate     = 96000
	hybridBoundaryFrames      = 50
	hybridBoundaryCapacity    = 4000
	hybridBoundaryPacketBytes = 240
	hybridBoundaryAudioApp    = 0
	hybridBoundaryFullband    = 1105
)

var hybridCoderBoundaryOracleCache libopustest.HelperCache

// TestHybridCBRActualCoderBoundaryDiagnostic snapshots the same live range
// coder after SILK and at the actual CELT call boundary for the selected
// Hybrid CBR fixture frame. It also proves that tracing preserves each full
// packet stream and final range before comparing the stage snapshots.
func TestHybridCBRActualCoderBoundaryDiagnostic(t *testing.T) {
	libopustest.RequireOracle(t)
	requireCELTTraceV3(t, "Hybrid CBR coder-boundary")

	pcm, err := testsignal.GenerateEncoderSignalVariant(
		testsignal.EncoderVariantAMMultisineV1,
		48000,
		hybridBoundaryFrameSize*hybridBoundaryChannels*hybridBoundaryFrames,
		hybridBoundaryChannels,
	)
	if err != nil {
		t.Fatalf("generate Hybrid CBR input: %v", err)
	}
	pcm = quantizeCELTTracePCM(pcm)
	input := celtTraceCBRInputFor(pcm, hybridBoundaryAudioApp, hybridBoundaryFullband,
		hybridBoundaryChannels, hybridBoundaryBitrate, hybridBoundaryFrameSize,
		hybridBoundaryFrames, 10)
	inputHash := sha256.Sum256(input)
	t.Logf("fixture=AMMultisineV1 input=Hybrid-FB-20ms-stereo-96k frames=%d frame_size=%d channels=%d bitrate=%d complexity=10 max_data_bytes=%d GCBR_sha256=%s target_frame=%d",
		hybridBoundaryFrames, hybridBoundaryFrameSize, hybridBoundaryChannels, hybridBoundaryBitrate,
		hybridBoundaryCapacity, hex.EncodeToString(inputHash[:]), hybridCoderBoundaryTraceTargetFrame)

	ordinaryPath := buildCELTTraceOracle(t, false)
	ordinaryBytes, err := libopustest.RunHelper(ordinaryPath, input)
	if err != nil {
		t.Fatalf("run ordinary Hybrid CBR oracle: %v", err)
	}
	ordinary, err := parseCELTTraceCBRPrefix(ordinaryBytes)
	if err != nil {
		t.Fatalf("parse ordinary Hybrid CBR output: %v", err)
	}
	if len(ordinary.Packets) != hybridBoundaryFrames || len(ordinary.FinalRanges) != hybridBoundaryFrames {
		t.Fatalf("ordinary C returned packets=%d ranges=%d, want %d", len(ordinary.Packets), len(ordinary.FinalRanges), hybridBoundaryFrames)
	}
	for frame, packet := range ordinary.Packets {
		if len(packet) != hybridBoundaryPacketBytes {
			t.Fatalf("ordinary C CBR frame %d has %d bytes, want %d", frame, len(packet), hybridBoundaryPacketBytes)
		}
	}
	targetCTOC := ordinary.Packets[hybridCoderBoundaryTraceTargetFrame][0]
	targetCConfig := targetCTOC >> 3
	targetCMode := hybridCoderBoundaryTOCMode(targetCConfig)
	t.Logf("selected ordinary C frame %d TOC=0x%02x config=%d mode=%s",
		hybridCoderBoundaryTraceTargetFrame, targetCTOC, targetCConfig, targetCMode)

	tracePath := buildHybridCoderBoundaryOracle(t)
	traceBytes, err := libopustest.RunHelper(tracePath, input)
	if err != nil {
		t.Fatalf("run traced Hybrid CBR oracle: %v", err)
	}
	tracePrefix, tracePayload, err := splitCELTTraceOutput(traceBytes)
	if err != nil {
		t.Fatalf("split traced Hybrid CBR output: %v", err)
	}
	traced, err := parseCELTTraceCBRPrefix(tracePrefix)
	if err != nil {
		t.Fatalf("parse traced Hybrid CBR output: %v", err)
	}
	if !sameCELTTraceCBROutput(ordinary, traced) {
		t.Fatalf("C boundary wrappers changed packets or final ranges for the %d-frame stream", hybridBoundaryFrames)
	}
	cTrace, err := parseHybridCoderBoundaryTrace(tracePayload)
	if err != nil {
		t.Fatalf("parse C coder-boundary trace: %v", err)
	}
	t.Logf("C GCHB header before record validation: %s", cTrace.header())
	if !strings.Contains(ordinary.LibopusVersion, libopustooling.DefaultVersion) {
		t.Fatalf("C oracle version=%q does not identify pinned libopus %q", ordinary.LibopusVersion, libopustooling.DefaultVersion)
	}
	t.Logf("C oracle=%q archmask=0x%x features=0x%x selected_arch=%d calls(frame/SILK/CELT)=%d/%d/%d target frame=%d public=%d SILK=%d coder=%d CELT=%d shared=%d",
		ordinary.LibopusVersion, ordinary.ArchMask, ordinary.BuildFeatures, ordinary.SelectedArch,
		cTrace.FrameCalls, cTrace.SILKCalls, cTrace.CELTCalls,
		hybridCoderBoundaryTraceTargetFrame, cTrace.TargetPublicCalls,
		cTrace.TargetSILKCalls, cTrace.TargetSILKRangeCalls,
		cTrace.TargetCELTCalls, cTrace.TargetSharedCELTCalls)

	tracedGo := newHybridCoderBoundaryEncoder()
	goTrace := beginHybridCoderBoundaryTraceForTesting(uint32(hybridCoderBoundaryTraceTargetFrame))
	tracedPackets, tracedRanges := make([][]byte, 0, hybridBoundaryFrames), make([]uint32, 0, hybridBoundaryFrames)
	for frame := range hybridBoundaryFrames {
		setHybridCoderBoundaryTraceFrameForTesting(uint32(frame))
		packet, encodeErr := encodeHybridCoderBoundaryFrame(tracedGo, pcm, frame)
		if encodeErr != nil {
			endHybridCoderBoundaryTraceForTesting(goTrace)
			t.Fatalf("encode traced Go Hybrid frame %d: %v", frame, encodeErr)
		}
		tracedPackets = append(tracedPackets, packet)
		tracedRanges = append(tracedRanges, tracedGo.FinalRange())
	}
	endHybridCoderBoundaryTraceForTesting(goTrace)
	if len(tracedPackets[hybridCoderBoundaryTraceTargetFrame]) == 0 {
		t.Fatalf("traced Go frame %d has no packet", hybridCoderBoundaryTraceTargetFrame)
	}
	targetGoTOC := tracedPackets[hybridCoderBoundaryTraceTargetFrame][0]
	targetGoConfig := targetGoTOC >> 3
	targetGoMode := hybridCoderBoundaryTOCMode(targetGoConfig)
	t.Logf("selected traced Go frame %d TOC=0x%02x config=%d mode=%s boundary-records=%d",
		hybridCoderBoundaryTraceTargetFrame, targetGoTOC, targetGoConfig, targetGoMode, goTrace.recordCount)
	if targetCMode != "Hybrid" || targetGoMode != "Hybrid" {
		t.Fatalf("selected frame %d is not an actual Hybrid frame on both paths: C=%s/config=%d Go=%s/config=%d; C GCHB: %s",
			hybridCoderBoundaryTraceTargetFrame, targetCMode, targetCConfig,
			targetGoMode, targetGoConfig, cTrace.header())
	}
	if cTrace.TargetFrame != hybridCoderBoundaryTraceTargetFrame || cTrace.FrameCalls != hybridBoundaryFrames ||
		cTrace.TargetPublicCalls != 1 || cTrace.TargetSILKCalls < cTrace.TargetSILKRangeCalls ||
		cTrace.TargetSILKRangeCalls != 1 || cTrace.TargetCELTCalls < cTrace.TargetSharedCELTCalls ||
		cTrace.TargetSharedCELTCalls != 1 ||
		len(cTrace.Records) != 3 || cTrace.Overflow != 0 || !cTrace.SameEC || !cTrace.SameBuffer {
		t.Fatalf("C did not capture the selected live Hybrid calls exactly once: %+v", cTrace.header())
	}
	for i, record := range cTrace.Records {
		if record.Frame != hybridCoderBoundaryTraceTargetFrame || record.Stage != uint32(i+1) || record.CallIndex != 1 {
			t.Fatalf("C boundary record %d has frame/stage/call=%d/%d/%d, want %d/%d/1",
				i, record.Frame, record.Stage, record.CallIndex, hybridCoderBoundaryTraceTargetFrame, i+1)
		}
	}
	assertHybridCoderBoundaryParserRejectsInvalidState(t, tracePayload)
	if goTrace.overflow || goTrace.recordCount != 3 {
		t.Fatalf("Go coder-boundary trace overflow=%t records=%d, want three ordered stages", goTrace.overflow, goTrace.recordCount)
	}
	for i, record := range goTrace.records {
		if record.Frame != hybridCoderBoundaryTraceTargetFrame || record.Stage != uint32(i+1) || record.Coder == nil {
			t.Fatalf("Go boundary record %d is frame/stage/coder=%d/%d/%p, want selected frame stage %d", i,
				record.Frame, record.Stage, record.Coder, i+1)
		}
		if i > 0 && record.Coder != goTrace.records[0].Coder {
			t.Fatalf("Go range-coder identity changed between stages %d and %d", i, i+1)
		}
	}

	plainGo := newHybridCoderBoundaryEncoder()
	plainPackets, plainRanges := make([][]byte, 0, hybridBoundaryFrames), make([]uint32, 0, hybridBoundaryFrames)
	for frame := range hybridBoundaryFrames {
		packet, encodeErr := encodeHybridCoderBoundaryFrame(plainGo, pcm, frame)
		if encodeErr != nil {
			t.Fatalf("encode plain Go Hybrid frame %d: %v", frame, encodeErr)
		}
		plainPackets = append(plainPackets, packet)
		plainRanges = append(plainRanges, plainGo.FinalRange())
	}
	if !sameHybridCoderBoundaryStream(plainPackets, plainRanges, tracedPackets, tracedRanges) {
		t.Fatal("Go boundary trace changed one or more packets or final ranges")
	}
	for frame, packet := range tracedPackets {
		if len(packet) != hybridBoundaryPacketBytes {
			t.Fatalf("traced Go CBR frame %d has %d bytes, want %d", frame, len(packet), hybridBoundaryPacketBytes)
		}
	}
	if len(tracedPackets[hybridCoderBoundaryTraceTargetFrame]) != len(ordinary.Packets[hybridCoderBoundaryTraceTargetFrame]) {
		t.Fatalf("selected packet size differs: Go=%d C=%d",
			len(tracedPackets[hybridCoderBoundaryTraceTargetFrame]), len(ordinary.Packets[hybridCoderBoundaryTraceTargetFrame]))
	}

	t.Logf("trace transparency: all %d C and Go packets and final ranges match their untraced controls",
		hybridBoundaryFrames)
	earliestBoundary := ""
	for i, goRecord := range goTrace.records {
		cRecord := cTrace.Records[i]
		difference := firstHybridCoderBoundaryDifference(goRecord.State, cRecord.State)
		if difference == "" {
			t.Logf("selected frame %d boundary %s: coder state and written bytes match", hybridCoderBoundaryTraceTargetFrame, hybridCoderBoundaryStageName(goRecord.Stage))
		} else {
			boundary := hybridCoderBoundaryStageName(goRecord.Stage)
			t.Logf("selected frame %d C/Go state difference at %s: %s", hybridCoderBoundaryTraceTargetFrame, boundary, difference)
			if earliestBoundary == "" {
				earliestBoundary = boundary + ": " + difference
			}
		}
	}
	if earliestBoundary == "" {
		t.Logf("selected frame %d coder state and written bytes match at all three boundaries", hybridCoderBoundaryTraceTargetFrame)
	} else {
		t.Logf("selected frame %d earliest C/Go divergence: %s", hybridCoderBoundaryTraceTargetFrame, earliestBoundary)
	}
	packetDiff := firstCELTTraceByteDifference(tracedPackets[hybridCoderBoundaryTraceTargetFrame], ordinary.Packets[hybridCoderBoundaryTraceTargetFrame])
	t.Logf("selected frame %d packet byte diff=%d Go range=0x%08x C range=0x%08x",
		hybridCoderBoundaryTraceTargetFrame, packetDiff,
		tracedRanges[hybridCoderBoundaryTraceTargetFrame], ordinary.FinalRanges[hybridCoderBoundaryTraceTargetFrame])
}

func newHybridCoderBoundaryEncoder() *Encoder {
	e := NewEncoder(48000, hybridBoundaryChannels)
	e.SetMode(ModeAuto)
	e.SetRestrictedSilkApplication(false)
	e.SetLowDelay(false)
	e.SetBandwidth(types.BandwidthFullband)
	e.SetBitrate(hybridBoundaryBitrate)
	e.SetBitrateMode(ModeCBR)
	e.SetComplexity(10)
	return e
}

func encodeHybridCoderBoundaryFrame(e *Encoder, pcm []float32, frame int) ([]byte, error) {
	samples := hybridBoundaryFrameSize * hybridBoundaryChannels
	start := frame * samples
	end := start + samples
	packet, err := e.EncodeFloat32WithAnalysisMaxBytes(pcm[start:end], hybridBoundaryFrameSize,
		pcm[start:end], hybridBoundaryCapacity)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), packet...), nil
}

func sameHybridCoderBoundaryStream(aPackets [][]byte, aRanges []uint32, bPackets [][]byte, bRanges []uint32) bool {
	if len(aPackets) != hybridBoundaryFrames || len(bPackets) != hybridBoundaryFrames ||
		len(aRanges) != hybridBoundaryFrames || len(bRanges) != hybridBoundaryFrames {
		return false
	}
	for i := range aPackets {
		if !bytes.Equal(aPackets[i], bPackets[i]) || aRanges[i] != bRanges[i] {
			return false
		}
	}
	return true
}

func buildHybridCoderBoundaryOracle(t *testing.T) string {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve Hybrid coder-boundary test source path")
	}
	wrapper := filepath.Join(filepath.Dir(testFile), "..", "..", "tools", "csrc", "libopus_hybrid_coder_boundary_trace.c")
	linkMap := filepath.Join(t.TempDir(), "hybrid-coder-boundary.map")
	config := libopustest.CHelperConfig{
		Label:      "Hybrid CBR actual coder-boundary trace",
		OutputBase: fmt.Sprintf("gopus_libopus_hybrid_coder_boundary_%d", hybridCoderBoundaryTraceTargetFrame),
		SourceFile: "libopus_cbr_encode_packets.c",
		CFlags: []string{
			"-DHAVE_CONFIG_H",
			fmt.Sprintf("-DGOPUS_HYBRID_TRACE_FRAME=%d", hybridCoderBoundaryTraceTargetFrame),
		},
		RefIncludes: []string{"celt", "silk", "src"},
		Sources:     []string{wrapper},
		LDFlags: []string{
			"-Wl,--wrap=opus_encode_float",
			"-Wl,--wrap=silk_Encode",
			"-Wl,--wrap=celt_encode_with_ec",
			"-Wl,-Map," + linkMap,
		},
	}
	path, err := hybridCoderBoundaryOracleCache.Path(func() (string, error) {
		helper, buildErr := libopustest.BuildPublicAPIHelper(config)
		if buildErr != nil {
			return "", buildErr
		}
		data, readErr := os.ReadFile(linkMap)
		if readErr != nil {
			return "", fmt.Errorf("read Hybrid coder-boundary link map: %w", readErr)
		}
		for _, symbol := range []string{"__wrap_opus_encode_float", "__wrap_silk_Encode", "__wrap_celt_encode_with_ec"} {
			if !bytes.Contains(data, []byte(symbol)) {
				return "", fmt.Errorf("Hybrid coder-boundary link map omits wrapper %s", symbol)
			}
		}
		if err := os.WriteFile(helper+".map", data, 0o600); err != nil {
			return "", fmt.Errorf("preserve Hybrid coder-boundary link map: %w", err)
		}
		return helper, nil
	})
	if err != nil {
		libopustest.HelperUnavailable(t, config.Label, err)
	}
	return path
}

type hybridCoderBoundaryCTrace struct {
	TargetFrame           uint32
	FrameCalls            uint32
	SILKCalls             uint32
	CELTCalls             uint32
	TargetPublicCalls     uint32
	TargetSILKCalls       uint32
	TargetSILKRangeCalls  uint32
	TargetCELTCalls       uint32
	TargetSharedCELTCalls uint32
	Overflow              uint32
	SameEC                bool
	SameBuffer            bool
	Records               []hybridCoderBoundaryCRecord
}

type hybridCoderBoundaryCRecord struct {
	Frame     uint32
	Stage     uint32
	CallIndex uint32
	State     hybridCoderBoundaryState
}

type hybridCoderBoundaryState struct {
	Storage    uint32
	Offs       uint32
	EndOffs    uint32
	EndWindow  uint32
	Range      uint32
	Value      uint32
	Extension  uint32
	NBitsTotal int32
	NEndBits   int32
	Remainder  int32
	Error      int32
	Tell       int32
	TellFrac   int32
	Forward    []byte
	Backward   []byte
}

func (trace hybridCoderBoundaryCTrace) header() string {
	return fmt.Sprintf("target=%d frame-calls=%d silk-calls=%d celt-calls=%d selected-calls=%d/%d(coder=%d)/%d(shared=%d) records=%d overflow=%d same-ec=%t same-buffer=%t",
		trace.TargetFrame, trace.FrameCalls, trace.SILKCalls, trace.CELTCalls,
		trace.TargetPublicCalls, trace.TargetSILKCalls, trace.TargetSILKRangeCalls,
		trace.TargetCELTCalls, trace.TargetSharedCELTCalls,
		len(trace.Records), trace.Overflow, trace.SameEC, trace.SameBuffer)
}

func parseHybridCoderBoundaryTrace(data []byte) (hybridCoderBoundaryCTrace, error) {
	var trace hybridCoderBoundaryCTrace
	if len(data) < 60 || string(data[:4]) != "GCHB" || binary.LittleEndian.Uint32(data[4:8]) != 1 {
		return trace, fmt.Errorf("invalid GCHB version-1 header")
	}
	reader := hybridCoderBoundaryReader{data: data, off: 8}
	var err error
	if trace.TargetFrame, err = reader.u32(); err != nil {
		return trace, err
	}
	if trace.FrameCalls, err = reader.u32(); err != nil {
		return trace, err
	}
	if trace.SILKCalls, err = reader.u32(); err != nil {
		return trace, err
	}
	if trace.CELTCalls, err = reader.u32(); err != nil {
		return trace, err
	}
	if trace.TargetPublicCalls, err = reader.u32(); err != nil {
		return trace, err
	}
	if trace.TargetSILKCalls, err = reader.u32(); err != nil {
		return trace, err
	}
	if trace.TargetSILKRangeCalls, err = reader.u32(); err != nil {
		return trace, err
	}
	if trace.TargetCELTCalls, err = reader.u32(); err != nil {
		return trace, err
	}
	if trace.TargetSharedCELTCalls, err = reader.u32(); err != nil {
		return trace, err
	}
	recordCount, err := reader.u32()
	if err != nil {
		return trace, err
	}
	if trace.Overflow, err = reader.u32(); err != nil {
		return trace, err
	}
	sameEC, err := reader.u32()
	if err != nil {
		return trace, err
	}
	sameBuffer, err := reader.u32()
	if err != nil {
		return trace, err
	}
	if sameEC > 1 || sameBuffer > 1 || recordCount > 3 {
		return trace, fmt.Errorf("invalid GCHB flags/count: same_ec=%d same_buffer=%d records=%d", sameEC, sameBuffer, recordCount)
	}
	trace.SameEC, trace.SameBuffer = sameEC == 1, sameBuffer == 1
	trace.Records = make([]hybridCoderBoundaryCRecord, 0, recordCount)
	for i := uint32(0); i < recordCount; i++ {
		var record hybridCoderBoundaryCRecord
		if record.Frame, err = reader.u32(); err != nil {
			return trace, err
		}
		if record.Stage, err = reader.u32(); err != nil {
			return trace, err
		}
		if record.CallIndex, err = reader.u32(); err != nil {
			return trace, err
		}
		fields := []*uint32{
			&record.State.Storage, &record.State.Offs, &record.State.EndOffs, &record.State.EndWindow,
			&record.State.Range, &record.State.Value, &record.State.Extension,
		}
		for _, field := range fields {
			if *field, err = reader.u32(); err != nil {
				return trace, err
			}
		}
		signed := []*int32{&record.State.NBitsTotal, &record.State.NEndBits, &record.State.Remainder,
			&record.State.Error, &record.State.Tell, &record.State.TellFrac}
		for _, field := range signed {
			value, readErr := reader.u32()
			if readErr != nil {
				return trace, readErr
			}
			*field = int32(value)
		}
		forwardLen, readErr := reader.u32()
		if readErr != nil {
			return trace, readErr
		}
		backwardLen, readErr := reader.u32()
		if readErr != nil {
			return trace, readErr
		}
		if forwardLen > 4096 || backwardLen > 4096 || forwardLen > uint32(len(data)-reader.off) {
			return trace, fmt.Errorf("invalid GCHB byte lengths record=%d forward=%d backward=%d", i, forwardLen, backwardLen)
		}
		record.State.Forward, err = reader.bytes(forwardLen)
		if err != nil {
			return trace, err
		}
		record.State.Backward, err = reader.bytes(backwardLen)
		if err != nil {
			return trace, err
		}
		if record.State.Offs != forwardLen || record.State.EndOffs != backwardLen ||
			uint64(record.State.Offs)+uint64(record.State.EndOffs) > uint64(record.State.Storage) {
			return trace, fmt.Errorf("invalid GCHB cursor/byte lengths in record %d", i)
		}
		if record.State.Storage > 4096 || record.State.Range == 0 {
			return trace, fmt.Errorf("invalid GCHB coder state in record %d: storage=%d range=%d",
				i, record.State.Storage, record.State.Range)
		}
		trace.Records = append(trace.Records, record)
	}
	if reader.off != len(data) {
		return trace, fmt.Errorf("GCHB trace has %d trailing bytes", len(data)-reader.off)
	}
	return trace, nil
}

func assertHybridCoderBoundaryParserRejectsInvalidState(t *testing.T, validTrace []byte) {
	t.Helper()
	const firstRecord = 60
	if len(validTrace) < firstRecord+72 {
		t.Fatalf("valid GCHB trace has no complete record for malformed-state checks: %d bytes", len(validTrace))
	}
	for _, invalid := range []struct {
		name   string
		offset int
		value  uint32
	}{
		{name: "storage exceeds capture bound", offset: firstRecord + 12, value: 4097},
		{name: "zero range", offset: firstRecord + 28, value: 0},
	} {
		malformed := append([]byte(nil), validTrace...)
		binary.LittleEndian.PutUint32(malformed[invalid.offset:invalid.offset+4], invalid.value)
		if _, err := parseHybridCoderBoundaryTrace(malformed); err == nil {
			t.Fatalf("GCHB parser accepted %s", invalid.name)
		}
	}
}

type hybridCoderBoundaryReader struct {
	data []byte
	off  int
}

func (reader *hybridCoderBoundaryReader) u32() (uint32, error) {
	if reader.off < 0 || reader.off+4 > len(reader.data) {
		return 0, fmt.Errorf("truncated GCHB u32 at offset %d", reader.off)
	}
	value := binary.LittleEndian.Uint32(reader.data[reader.off:])
	reader.off += 4
	return value, nil
}

func (reader *hybridCoderBoundaryReader) bytes(count uint32) ([]byte, error) {
	if uint64(count) > uint64(len(reader.data)-reader.off) {
		return nil, fmt.Errorf("truncated GCHB byte range at offset %d length %d", reader.off, count)
	}
	start := reader.off
	reader.off += int(count)
	return append([]byte(nil), reader.data[start:reader.off]...), nil
}

func firstHybridCoderBoundaryDifference(goState rangecoding.BoundaryTraceSnapshot, cState hybridCoderBoundaryState) string {
	fields := []struct {
		name string
		got  uint32
		want uint32
	}{
		{"storage", goState.Storage, cState.Storage},
		{"offs", goState.Offs, cState.Offs},
		{"end_offs", goState.EndOffs, cState.EndOffs},
		{"end_window", goState.EndWindow, cState.EndWindow},
		{"range", goState.Range, cState.Range},
		{"val", goState.Value, cState.Value},
		{"ext", goState.Extension, cState.Extension},
		{"nbits_total", uint32(goState.NBitsTotal), uint32(cState.NBitsTotal)},
		{"nend_bits", uint32(goState.NEndBits), uint32(cState.NEndBits)},
		{"rem", uint32(goState.Remainder), uint32(cState.Remainder)},
		{"error", uint32(goState.Error), uint32(cState.Error)},
		{"tell", uint32(goState.Tell), uint32(cState.Tell)},
		{"tell_frac", uint32(goState.TellFrac), uint32(cState.TellFrac)},
	}
	for _, field := range fields {
		if field.got != field.want {
			return fmt.Sprintf("%s Go=0x%08x C=0x%08x", field.name, field.got, field.want)
		}
	}
	if goState.ForwardLen != uint32(len(cState.Forward)) {
		return fmt.Sprintf("forward length Go=%d C=%d", goState.ForwardLen, len(cState.Forward))
	}
	forward := goState.Forward[:goState.ForwardLen]
	if !bytes.Equal(forward, cState.Forward) {
		index := firstHybridCoderTraceByteDifference(forward, cState.Forward)
		return fmt.Sprintf("forward byte[%d] Go=0x%02x C=0x%02x", index, forward[index], cState.Forward[index])
	}
	if goState.BackwardLen != uint32(len(cState.Backward)) {
		return fmt.Sprintf("backward length Go=%d C=%d", goState.BackwardLen, len(cState.Backward))
	}
	backward := goState.Backward[:goState.BackwardLen]
	if !bytes.Equal(backward, cState.Backward) {
		index := firstHybridCoderTraceByteDifference(backward, cState.Backward)
		return fmt.Sprintf("backward byte[%d] Go=0x%02x C=0x%02x", index, backward[index], cState.Backward[index])
	}
	return ""
}

func firstHybridCoderTraceByteDifference(a, b []byte) int {
	limit := min(len(a), len(b))
	for i := 0; i < limit; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return limit
}

func hybridCoderBoundaryStageName(stage uint32) string {
	switch stage {
	case hybridCoderBoundarySILKExit:
		return "SILK exit"
	case hybridCoderBoundaryCELTEntry:
		return "CELT entry"
	case hybridCoderBoundaryCELTExit:
		return "CELT exit"
	default:
		return fmt.Sprintf("stage %d", stage)
	}
}

func hybridCoderBoundaryTOCMode(config uint8) string {
	switch {
	case config <= 11:
		return "SILK"
	case config <= 15:
		return "Hybrid"
	default:
		return "CELT"
	}
}
