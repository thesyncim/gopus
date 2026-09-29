//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext

package encoder

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
)

const (
	vbrTraceFrameSize = 240
	vbrTraceChannels  = 2
	vbrTraceFrames    = 2
	vbrTraceBitrate   = 128000
)

var encodeDiffCELTVBREntropyTraceOracle libopustest.HelperCache

// TestEncodeDiffCELTVBRFrame1Trace records the first divergence for the exact
// scalar public VBR fuzz case while proving that C and Go tracing leave both
// frames' packets and final ranges unchanged.
func TestEncodeDiffCELTVBRFrame1Trace(t *testing.T) {
	libopustest.RequireOracle(t)
	requireCELTTraceV3(t, "public VBR frame-1")

	pcm, err := testsignal.GenerateCorpusSignal(
		testsignal.CorpusBellClusterV1,
		48000,
		vbrTraceFrameSize*vbrTraceChannels*vbrTraceFrames,
		vbrTraceChannels,
	)
	if err != nil {
		t.Fatalf("generate BellCluster V1 PCM: %v", err)
	}
	params := libopustest.EncodeDiffParams{
		SampleRate:    48000,
		Channels:      vbrTraceChannels,
		Application:   libopustest.EncodeDiffApplicationAudio,
		ForceMode:     libopustest.EncodeDiffForceModeCELTOnly,
		Bandwidth:     libopustest.EncodeDiffBandwidthFullband,
		MaxBandwidth:  libopustest.EncodeDiffBandwidthFullband,
		Bitrate:       vbrTraceBitrate,
		Complexity:    10,
		Signal:        libopustest.EncodeDiffSignalMusic,
		VBR:           true,
		VBRConstraint: true,
		ForceChannels: vbrTraceChannels,
		FrameSize:     vbrTraceFrameSize,
		FrameCount:    vbrTraceFrames,
		PCM:           pcm,
	}

	ordinaryC, err := libopustest.ProbeEncodeDiff(params)
	if err != nil {
		libopustest.HelperUnavailable(t, "ordinary public VBR encode oracle", err)
		return
	}
	if len(ordinaryC) != vbrTraceFrames {
		t.Fatalf("ordinary public C oracle returned %d records, want %d", len(ordinaryC), vbrTraceFrames)
	}

	tracePath := buildCELTVBREntropyTraceOracle(t)
	traceBytes, err := libopustest.RunHelper(tracePath, encodeDiffCELTVBRInput(params))
	if err != nil {
		t.Fatalf("run traced public VBR oracle: %v", err)
	}
	tracedC, trailerOffset, err := parseEncodeDiffCELTVBRPrefix(traceBytes)
	if err != nil {
		t.Fatalf("parse traced public VBR packet prefix: %v", err)
	}
	if !sameEncodeDiffRecords(ordinaryC, tracedC) {
		t.Fatalf("C trace wrappers changed the two-frame public stream: ordinary=%s traced=%s",
			formatEncodeDiffRecords(ordinaryC), formatEncodeDiffRecords(tracedC))
	}

	stageBytes, err := scanCELTVBRStageTrace(traceBytes[trailerOffset:])
	if err != nil {
		t.Fatalf("scan C CELT stage trace: %v", err)
	}
	cTrace, err := parseCELTEncodeTrace(traceBytes[trailerOffset : trailerOffset+stageBytes])
	if err != nil {
		t.Fatalf("parse C CELT stage trace: %v", err)
	}
	if cTrace.TraceFrame != 1 || cTrace.Overflow != 0 {
		t.Fatalf("C stage trace selected frame=%d overflow=%d, want frame 1 and no overflow", cTrace.TraceFrame, cTrace.Overflow)
	}
	if cTrace.BandCalls == 0 || cTrace.LogCalls == 0 || cTrace.NormalizationCalls == 0 || cTrace.CoarseCalls == 0 || cTrace.QuantCalls == 0 {
		t.Fatalf("C wrappers did not cover every CELT stage: %s", cTrace.counts())
	}
	if cTrace.MDCTCalls == 0 {
		t.Fatalf("C MDCT wrapper did not capture frame 1: %s", cTrace.counts())
	}
	for i, band := range cTrace.Bands {
		if band.FrameCoeffs != vbrTraceFrameSize || band.Bands != celtTraceBandCount || band.Channels != vbrTraceChannels || band.LM != 1 {
			t.Fatalf("C VBR band stage %d dimensions are frame=%d bands=%d channels=%d LM=%d",
				i, band.FrameCoeffs, band.Bands, band.Channels, band.LM)
		}
	}

	goTraced := newCELTVBRTraceEncoder()
	goPlain := newCELTVBRTraceEncoder()
	tracedGo := make([]libopustest.EncodeDiffRecord, vbrTraceFrames)
	plainGo := make([]libopustest.EncodeDiffRecord, vbrTraceFrames)
	for frame := range vbrTraceFrames {
		framePCM := pcm[frame*vbrTraceFrameSize*vbrTraceChannels : (frame+1)*vbrTraceFrameSize*vbrTraceChannels]
		if frame == 1 {
			if goTraced.celtEncoder == nil {
				t.Fatal("Go public CELT encoder was not initialized after frame 0")
			}
			goTraced.celtEncoder.EnableEncodeStageTraceForTesting()
		}
		tracedPacket, tracedErr := goTraced.EncodeFloat32WithAnalysisMaxBytes(framePCM, vbrTraceFrameSize, framePCM, 4000)
		if tracedErr != nil {
			t.Fatalf("encode traced Go VBR frame %d: %v", frame, tracedErr)
		}
		plainPacket, plainErr := goPlain.EncodeFloat32WithAnalysisMaxBytes(framePCM, vbrTraceFrameSize, framePCM, 4000)
		if plainErr != nil {
			t.Fatalf("encode plain Go VBR frame %d: %v", frame, plainErr)
		}
		tracedGo[frame] = libopustest.EncodeDiffRecord{Ret: len(tracedPacket), FinalRange: goTraced.FinalRange(), Packet: append([]byte(nil), tracedPacket...)}
		plainGo[frame] = libopustest.EncodeDiffRecord{Ret: len(plainPacket), FinalRange: goPlain.FinalRange(), Packet: append([]byte(nil), plainPacket...)}
	}
	if !sameEncodeDiffRecords(tracedGo, plainGo) {
		t.Fatalf("Go stage trace changed the two-frame public stream: traced=%s plain=%s",
			formatEncodeDiffRecords(tracedGo), formatEncodeDiffRecords(plainGo))
	}
	goTrace := goTraced.celtEncoder.EncodeStageTraceForTesting()
	// C records the logical output stride B; Go's stride-less MDCT helper uses the equivalent b+i*B layout.
	if err := validateCELTTraceShapes(goTrace, cTrace); err != nil {
		t.Fatalf("invalid Go/C CELT stage trace shapes: %v", err)
	}
	if err := validateCELTTraceExpectedDimensions(goTrace, cTrace, vbrTraceFrameSize, celtTraceBandCount, vbrTraceChannels, celtTraceActive, 1); err != nil {
		t.Fatalf("unexpected Go/C public VBR frame-1 CELT dimensions: %v", err)
	}

	entropyOffset := trailerOffset + stageBytes
	entropyTrace, err := parseCELTVBREntropyTrace(traceBytes[entropyOffset:])
	if err != nil {
		t.Fatalf("parse C frame-1 entropy trace: %v", err)
	}
	if entropyTrace.Frame != 1 || entropyTrace.Overflow != 0 || entropyTrace.RawCalls == 0 || entropyTrace.DoneCalls == 0 {
		t.Fatalf("C entropy trace incomplete: %+v", entropyTrace.summary())
	}

	t.Logf("public VBR frame 0: Go bytes=%d C bytes=%d first byte diff=%d Go range=%08x C range=%08x",
		len(tracedGo[0].Packet), len(ordinaryC[0].Packet), firstCELTTraceByteDifference(tracedGo[0].Packet, ordinaryC[0].Packet), tracedGo[0].FinalRange, ordinaryC[0].FinalRange)
	frame1Diff := firstCELTTraceByteDifference(tracedGo[1].Packet, ordinaryC[1].Packet)
	t.Logf("public VBR frame 1: Go bytes=%d C bytes=%d first byte diff=%d Go range=%08x C range=%08x",
		len(tracedGo[1].Packet), len(ordinaryC[1].Packet), frame1Diff, tracedGo[1].FinalRange, ordinaryC[1].FinalRange)
	t.Logf("C frame-1 entropy trace: %s", entropyTrace.summary())
	if frame1Diff >= 0 {
		logCELTVBREntropyByteClass(t, frame1Diff, ordinaryC[1].Packet, entropyTrace)
	}
	t.Log("CELT stage-bit comparisons are diagnostic; packet/range transparency checks are strict")
	logCELTTraceDifferences(t, goTrace, cTrace)
}

func buildCELTVBREntropyTraceOracle(t *testing.T) string {
	t.Helper()
	config := libopustest.CHelperConfig{
		Label:       "public VBR CELT stage and entropy trace",
		OutputBase:  "gopus_libopus_public_vbr_celt_entropy_trace",
		SourceFile:  "libopus_encode_diff_celt_entropy_trace.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk", "src"},
		LDFlags: []string{
			"-Wl,--wrap=opus_encode_float",
			"-Wl,--wrap=compute_band_energies",
			"-Wl,--wrap=amp2Log2",
			"-Wl,--wrap=normalise_bands",
			"-Wl,--wrap=quant_coarse_energy",
			"-Wl,--wrap=quant_all_bands",
			"-Wl,--wrap=clt_mdct_forward_c",
			"-Wl,--wrap=ec_enc_bits",
			"-Wl,--wrap=ec_enc_done",
		},
	}
	path, err := encodeDiffCELTVBREntropyTraceOracle.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(config)
	})
	if err != nil {
		libopustest.HelperUnavailable(t, config.Label, err)
	}
	return path
}

func newCELTVBRTraceEncoder() *Encoder {
	e := NewEncoder(48000, vbrTraceChannels)
	e.SetFrameSize(vbrTraceFrameSize)
	e.SetMode(ModeCELT)
	e.SetBandwidth(types.BandwidthFullband)
	e.SetMaxBandwidth(types.BandwidthFullband)
	e.SetBitrate(vbrTraceBitrate)
	e.SetBitrateMode(ModeCVBR)
	e.SetComplexity(10)
	e.SetSignalType(types.SignalMusic)
	e.SetForceChannels(vbrTraceChannels)
	e.SetFEC(false)
	e.SetDTX(false)
	return e
}

func encodeDiffCELTVBRInput(params libopustest.EncodeDiffParams) []byte {
	nSamples := len(params.PCM)
	payload := libopustest.NewOraclePayloadVersion("GEDI", 1)
	values := [...]uint32{
		uint32(params.SampleRate), uint32(params.Channels), uint32(params.Application),
		uint32(params.ForceMode), uint32(params.Bandwidth), uint32(params.MaxBandwidth),
		uint32(params.Bitrate), uint32(params.Complexity), params.Signal,
		boolToU32(params.VBR), boolToU32(params.VBRConstraint), uint32(params.ForceChannels),
		uint32(params.InbandFEC), uint32(params.PacketLoss), boolToU32(params.DTX),
		uint32(params.LSBDepth), boolToU32(params.PredictionDisabled), boolToU32(params.PhaseInvDisabled),
		uint32(params.FrameSize), uint32(params.FrameCount), uint32(nSamples),
	}
	payload.U32s(values[:]...)
	payload.Float32s(params.PCM...)
	return payload.Bytes()
}

func boolToU32(value bool) uint32 {
	if value {
		return 1
	}
	return 0
}

func parseEncodeDiffCELTVBRPrefix(data []byte) ([]libopustest.EncodeDiffRecord, int, error) {
	if len(data) < 12 || string(data[:4]) != "GEDO" || binary.LittleEndian.Uint32(data[4:8]) != 1 {
		return nil, 0, fmt.Errorf("invalid GEDO v1 header")
	}
	count := binary.LittleEndian.Uint32(data[8:12])
	if count != vbrTraceFrames {
		return nil, 0, fmt.Errorf("traced C record count=%d, want %d", count, vbrTraceFrames)
	}
	off := 12
	records := make([]libopustest.EncodeDiffRecord, count)
	for i := range records {
		if off+12 > len(data) {
			return nil, 0, fmt.Errorf("truncated GEDO record %d", i)
		}
		ret := int(int32(binary.LittleEndian.Uint32(data[off:])))
		finalRange := binary.LittleEndian.Uint32(data[off+4:])
		packetLen := uint64(binary.LittleEndian.Uint32(data[off+8:]))
		off += 12
		if packetLen > uint64(len(data)-off) {
			return nil, 0, fmt.Errorf("truncated GEDO packet %d length %d", i, packetLen)
		}
		packet := append([]byte(nil), data[off:off+int(packetLen)]...)
		off += int(packetLen)
		if packetLen > 0 {
			padding := (4 - int(packetLen)%4) % 4
			if off+padding > len(data) {
				return nil, 0, fmt.Errorf("truncated GEDO padding %d", i)
			}
			off += padding
		}
		if ret > 0 && ret != len(packet) {
			return nil, 0, fmt.Errorf("GEDO record %d ret=%d packet length=%d", i, ret, len(packet))
		}
		records[i] = libopustest.EncodeDiffRecord{Ret: ret, FinalRange: finalRange, Packet: packet}
	}
	if off+4 > len(data) || string(data[off:off+4]) != "GCET" {
		return nil, 0, fmt.Errorf("missing GCET stage trace at offset %d", off)
	}
	return records, off, nil
}

func sameEncodeDiffRecords(a, b []libopustest.EncodeDiffRecord) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Ret != b[i].Ret || a[i].FinalRange != b[i].FinalRange || !bytes.Equal(a[i].Packet, b[i].Packet) {
			return false
		}
	}
	return true
}

func formatEncodeDiffRecords(records []libopustest.EncodeDiffRecord) string {
	var result string
	for i, record := range records {
		if i != 0 {
			result += "; "
		}
		result += fmt.Sprintf("f%d(len=%d range=%08x)", i, len(record.Packet), record.FinalRange)
	}
	return result
}

func scanCELTVBRStageTrace(data []byte) (int, error) {
	if len(data) < 16 || string(data[:4]) != "GCET" || binary.LittleEndian.Uint32(data[4:8]) != 3 {
		return 0, fmt.Errorf("invalid GCET v3 header")
	}
	off := 8
	read := func() (uint32, error) {
		if off+4 > len(data) {
			return 0, fmt.Errorf("truncated GCET u32 at %d", off)
		}
		value := binary.LittleEndian.Uint32(data[off:])
		off += 4
		return value, nil
	}
	skipFloats := func(count uint64) error {
		if count > 4096 || count*4 > uint64(len(data)-off) {
			return fmt.Errorf("invalid GCET float count %d at %d", count, off)
		}
		off += int(count * 4)
		return nil
	}
	if _, err := read(); err != nil { // trace frame
		return 0, err
	}
	if _, err := read(); err != nil { // overflow
		return 0, err
	}
	readCount := func() (uint32, error) {
		total, err := read()
		if err != nil {
			return 0, err
		}
		stored, err := read()
		if err != nil {
			return 0, err
		}
		if total > celtVBRTraceMaxCalls || stored != total {
			return 0, fmt.Errorf("invalid GCET count total=%d stored=%d", total, stored)
		}
		return total, nil
	}
	bandCalls, err := readCount()
	if err != nil {
		return 0, err
	}
	for range bandCalls {
		coeffs, err := read()
		if err != nil {
			return 0, err
		}
		bands, err := read()
		if err != nil {
			return 0, err
		}
		channels, err := read()
		if err != nil {
			return 0, err
		}
		if _, err = read(); err != nil {
			return 0, err
		} // LM
		if err = skipFloats((uint64(coeffs) + uint64(bands)) * uint64(channels)); err != nil {
			return 0, err
		}
	}
	logCalls, err := readCount()
	if err != nil {
		return 0, err
	}
	for range logCalls {
		bands, err := read()
		if err != nil {
			return 0, err
		}
		channels, err := read()
		if err = skipFloats(2 * uint64(bands) * uint64(channels)); err != nil {
			return 0, err
		}
	}
	normCalls, err := readCount()
	if err != nil {
		return 0, err
	}
	for range normCalls {
		active, err := read()
		if err != nil {
			return 0, err
		}
		bands, err := read()
		if err != nil {
			return 0, err
		}
		channels, err := read()
		if err = skipFloats(uint64(active+bands) * uint64(channels)); err != nil {
			return 0, err
		}
	}
	coarseCalls, err := readCount()
	if err != nil {
		return 0, err
	}
	for range coarseCalls {
		bands, err := read()
		if err != nil {
			return 0, err
		}
		channels, err := read()
		if err != nil {
			return 0, err
		}
		if _, err = read(); err != nil {
			return 0, err
		} // byte budget
		if err = skipFloats(3 * uint64(bands) * uint64(channels)); err != nil {
			return 0, err
		}
	}
	quantCalls, err := readCount()
	if err != nil {
		return 0, err
	}
	for range quantCalls {
		active, err := read()
		if err != nil {
			return 0, err
		}
		bands, err := read()
		if err != nil {
			return 0, err
		}
		channels, err := read()
		if err = skipFloats((uint64(bands) + 2*uint64(active)) * uint64(channels)); err != nil {
			return 0, err
		}
	}
	mdctCalls, err := readCount()
	if err != nil {
		return 0, err
	}
	for range mdctCalls {
		var counts [12]uint32
		for i := range counts {
			if counts[i], err = read(); err != nil {
				return 0, err
			}
		}
		if err = skipFloats(uint64(counts[9])); err != nil {
			return 0, err
		}
		if err = skipFloats(uint64(counts[10])); err != nil {
			return 0, err
		}
		if err = skipFloats(uint64(counts[11])); err != nil {
			return 0, err
		}
	}
	return off, nil
}

const celtVBRTraceMaxCalls = 8

type celtVBREntropySnapshot struct {
	Storage, Offs, EndOffs, EndWindow uint32
	NEndBits, NBitsTotal              int32
	Range, Value, Ext                 uint32
	Rem, Error                        int32
}

type celtVBREntropyRawCall struct {
	Value, Bits uint32
	Before      celtVBREntropySnapshot
	After       celtVBREntropySnapshot
}

type celtVBREntropyTrace struct {
	Frame, Overflow       uint32
	RawCalls, DoneCalls   uint32
	Raw                   []celtVBREntropyRawCall
	DoneBefore, DoneAfter []celtVBREntropySnapshot
}

func parseCELTVBREntropyTrace(data []byte) (celtVBREntropyTrace, error) {
	var result celtVBREntropyTrace
	if len(data) < 32 || string(data[:4]) != "GENT" || binary.LittleEndian.Uint32(data[4:8]) != 1 {
		return result, fmt.Errorf("invalid GENT v1 header")
	}
	off := 8
	read := func() (uint32, error) {
		if off+4 > len(data) {
			return 0, fmt.Errorf("truncated GENT u32 at %d", off)
		}
		value := binary.LittleEndian.Uint32(data[off:])
		off += 4
		return value, nil
	}
	readSnapshot := func() (celtVBREntropySnapshot, error) {
		var values [11]uint32
		for i := range values {
			value, err := read()
			if err != nil {
				return celtVBREntropySnapshot{}, err
			}
			values[i] = value
		}
		return celtVBREntropySnapshot{
			Storage: values[0], Offs: values[1], EndOffs: values[2], EndWindow: values[3],
			NEndBits: int32(values[4]), NBitsTotal: int32(values[5]), Range: values[6], Value: values[7],
			Ext: values[8], Rem: int32(values[9]), Error: int32(values[10]),
		}, nil
	}
	var err error
	if result.Frame, err = read(); err != nil {
		return result, err
	}
	if result.Overflow, err = read(); err != nil {
		return result, err
	}
	rawCalls, err := read()
	if err != nil {
		return result, err
	}
	storedRaw, err := read()
	if err != nil {
		return result, err
	}
	doneCalls, err := read()
	if err != nil {
		return result, err
	}
	storedDone, err := read()
	if err != nil {
		return result, err
	}
	if rawCalls > 4096 || storedRaw != rawCalls || doneCalls > 4 || storedDone != doneCalls {
		return result, fmt.Errorf("invalid GENT call counts raw=%d/%d done=%d/%d", rawCalls, storedRaw, doneCalls, storedDone)
	}
	result.RawCalls, result.DoneCalls = rawCalls, doneCalls
	result.Raw = make([]celtVBREntropyRawCall, storedRaw)
	for i := range result.Raw {
		if result.Raw[i].Value, err = read(); err != nil {
			return result, err
		}
		if result.Raw[i].Bits, err = read(); err != nil {
			return result, err
		}
		if result.Raw[i].Before, err = readSnapshot(); err != nil {
			return result, err
		}
		if result.Raw[i].After, err = readSnapshot(); err != nil {
			return result, err
		}
	}
	result.DoneBefore = make([]celtVBREntropySnapshot, storedDone)
	result.DoneAfter = make([]celtVBREntropySnapshot, storedDone)
	for i := range result.DoneBefore {
		if result.DoneBefore[i], err = readSnapshot(); err != nil {
			return result, err
		}
		if result.DoneAfter[i], err = readSnapshot(); err != nil {
			return result, err
		}
	}
	if off != len(data) {
		return result, fmt.Errorf("GENT trailer has %d trailing bytes", len(data)-off)
	}
	return result, nil
}

func (trace celtVBREntropyTrace) summary() string {
	if len(trace.DoneAfter) == 0 {
		return fmt.Sprintf("frame=%d raw-calls=%d done-calls=%d overflow=%d", trace.Frame, trace.RawCalls, trace.DoneCalls, trace.Overflow)
	}
	done := trace.DoneAfter[len(trace.DoneAfter)-1]
	return fmt.Sprintf("frame=%d raw-calls=%d done-calls=%d storage=%d range-bytes=%d raw-bytes=%d nend-bits=%d end-window=%08x coder-rng=%08x",
		trace.Frame, trace.RawCalls, trace.DoneCalls, done.Storage, done.Offs, done.EndOffs, done.NEndBits, done.EndWindow, done.Range)
}

func logCELTVBREntropyByteClass(t *testing.T, diff int, packet []byte, trace celtVBREntropyTrace) {
	t.Helper()
	if len(trace.DoneAfter) == 0 {
		t.Logf("packet byte %d cannot be placed in C entropy segments: no ec_enc_done snapshot", diff)
		return
	}
	state := trace.DoneAfter[len(trace.DoneAfter)-1]
	if uint64(state.Storage) > uint64(len(packet)) || state.Offs > state.Storage || state.EndOffs > state.Storage || state.Offs+state.EndOffs > state.Storage {
		t.Logf("packet byte %d cannot be classified by C entropy extents: packet=%d snapshot=%+v", diff, len(packet), state)
		return
	}
	packetPrefix := len(packet) - int(state.Storage)
	rangeStart, rangeEnd := packetPrefix, packetPrefix+int(state.Offs)
	rawStart, rawEnd := len(packet)-int(state.EndOffs), len(packet)
	switch {
	case diff >= rangeStart && diff < rangeEnd:
		t.Logf("packet byte %d falls in libopus C range-coded bytes [%d,%d)", diff, rangeStart, rangeEnd)
	case diff >= rawStart && diff < rawEnd:
		t.Logf("packet byte %d falls in libopus C raw-bit tail [%d,%d)", diff, rawStart, rawEnd)
	default:
		t.Logf("packet byte %d falls between/outside captured C entropy extents: range=[%d,%d) raw=[%d,%d)", diff, rangeStart, rangeEnd, rawStart, rawEnd)
	}
}
