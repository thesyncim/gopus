//go:build gopus_dred || gopus_extra_controls

package gopus

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
	silkpkg "github.com/thesyncim/gopus/silk"
)

const (
	libopusDecoderDREDDecodeFloatInputMagic  = "GDDI"
	libopusDecoderDREDDecodeFloatOutputMagic = "GDDO"
	libopusCELTFramePLCNeural                = 4
	libopusCELTFrameDRED                     = 5
)

type libopusDecoderDREDDecodeFloatInfo struct {
	parseRet  int
	dredEnd   int
	warmupRet int
	ret       int
	nextRet   int
	channels  int
	state     lpcnetplc.StateSnapshot
	fargan    lpcnetplc.FARGANSnapshot
	celt48k   libopusDecoderDREDCELTSnapshot
	silk      libopusDecoderDREDSILKSnapshot
	pcm       []float32
	nextPCM   []float32
}

type libopusDecoderDREDCELTSnapshot struct {
	LastFrameType     int
	PLCFill           int
	PLCDuration       int
	SkipPLC           int
	PLCPreemphasisMem float32
	PreemphMem        [2]float32
	PLCPCM            [4 * lpcnetplc.FrameSize]float32
	WarmupPreemphMem  [2]float32
	WarmupPLCPreemph  float32
	WarmupPLCUpdate   [4 * lpcnetplc.FrameSize]float32
}

var libopusDecoderDREDDecodeFloatHelper libopustest.HelperCache

func getLibopusDecoderDREDDecodeFloatHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusDecoderDREDDecodeFloatHelper, "libopus_decoder_dred_decode_float_info.c", "gopus_libopus_decoder_dred_decode_float", true)
}

func probeLibopusDecoderDREDDecodeFloat(seedPacket, packet []byte, maxDREDSamples, sampleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples int) (libopusDecoderDREDDecodeFloatInfo, error) {
	return probeLibopusDecoderDREDDecodeFloatWithGain(seedPacket, packet, maxDREDSamples, sampleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples, 0)
}

func probeLibopusDecoderDREDDecodeFloatWithGain(seedPacket, packet []byte, maxDREDSamples, sampleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples, gain int) (libopusDecoderDREDDecodeFloatInfo, error) {
	return probeLibopusDecoderDREDDecodeAndNextFloatWithGain(seedPacket, packet, nil, maxDREDSamples, sampleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples, gain)
}

func probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packet, nextPacket []byte, maxDREDSamples, sampleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples int) (libopusDecoderDREDDecodeFloatInfo, error) {
	return probeLibopusDecoderDREDDecodeAndNextFloatWithGain(seedPacket, packet, nextPacket, maxDREDSamples, sampleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples, 0)
}

func probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket []byte, packetInfo libopusDREDPacket, decoderSampleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples int) (libopusDecoderDREDDecodeFloatInfo, error) {
	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, decoderSampleRate)
	return probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, maxDRED, oracleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples)
}

func probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket []byte, packetInfo libopusDREDPacket, nextPacket []byte, decoderSampleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples int) (libopusDecoderDREDDecodeFloatInfo, error) {
	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, decoderSampleRate)
	return probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, maxDRED, oracleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples)
}

func probeLibopusDecoderDREDDecodeAndNextFloatWithGain(seedPacket, packet, nextPacket []byte, maxDREDSamples, sampleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples, gain int) (libopusDecoderDREDDecodeFloatInfo, error) {
	binPath, err := getLibopusDecoderDREDDecodeFloatHelperPath()
	if err != nil {
		return libopusDecoderDREDDecodeFloatInfo{}, err
	}
	decoderModelBlob, err := probeLibopusDecoderNeuralModelBlob()
	if err != nil {
		return libopusDecoderDREDDecodeFloatInfo{}, err
	}
	dredModelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		return libopusDecoderDREDDecodeFloatInfo{}, err
	}

	payload := libopustest.NewOraclePayloadVersion(libopusDecoderDREDDecodeFloatInputMagic, 7,
		uint32(sampleRate),
		uint32(maxDREDSamples),
		uint32(warmupDREDOffsetSamples),
		uint32(dredOffsetSamples),
		uint32(frameSizeSamples),
	)
	payload.I32(int32(gain))
	payload.U32s(
		uint32(len(seedPacket)),
		uint32(len(packet)),
		uint32(len(nextPacket)),
		uint32(len(decoderModelBlob)),
		uint32(len(dredModelBlob)),
	)
	for _, chunk := range [][]byte{
		seedPacket,
		packet,
		nextPacket,
		decoderModelBlob,
		dredModelBlob,
	} {
		payload.Raw(chunk)
	}

	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(), "decoder dred decode", libopusDecoderDREDDecodeFloatOutputMagic, 5)
	if err != nil {
		return libopusDecoderDREDDecodeFloatInfo{}, err
	}
	info := libopusDecoderDREDDecodeFloatInfo{
		parseRet:  int(reader.I32()),
		dredEnd:   int(reader.I32()),
		warmupRet: int(reader.I32()),
		ret:       int(reader.I32()),
		nextRet:   int(reader.I32()),
		channels:  int(reader.I32()),
	}
	info.state.Blend = int(reader.I32())
	info.state.LossCount = int(reader.I32())
	info.state.AnalysisGap = int(reader.I32())
	info.state.AnalysisPos = int(reader.I32())
	info.state.PredictPos = int(reader.I32())
	info.state.FECReadPos = int(reader.I32())
	info.state.FECFillPos = int(reader.I32())
	info.state.FECSkip = int(reader.I32())
	info.fargan.ContInitialized = reader.I32() != 0
	info.fargan.LastPeriod = int(reader.I32())
	info.celt48k.LastFrameType = int(reader.I32())
	info.celt48k.PLCFill = int(reader.I32())
	info.celt48k.PLCDuration = int(reader.I32())
	info.celt48k.SkipPLC = int(reader.I32())
	info.celt48k.PLCPreemphasisMem = reader.Float32()
	info.silk.LagPrev = int(reader.I32())
	info.silk.LastGainIndex = int(reader.I32())
	info.silk.LossCount = int(reader.I32())
	info.silk.PrevSignalType = int(reader.I32())
	readBits := func(dst []float32) error {
		for i := range dst {
			dst[i] = reader.Float32()
		}
		if err := reader.Err(); err != nil {
			return err
		}
		return nil
	}
	if info.ret > 0 && info.channels > 0 {
		info.pcm = make([]float32, info.ret*info.channels)
		if err := readBits(info.pcm); err != nil {
			return libopusDecoderDREDDecodeFloatInfo{}, err
		}
	}
	if info.nextRet > 0 && info.channels > 0 {
		info.nextPCM = make([]float32, info.nextRet*info.channels)
		if err := readBits(info.nextPCM); err != nil {
			return libopusDecoderDREDDecodeFloatInfo{}, err
		}
	}
	for _, dst := range [][]float32{
		info.state.Features[:],
		info.state.Cont[:],
		info.state.PCM[:],
		info.state.PLCNet.GRU1[:],
		info.state.PLCNet.GRU2[:],
		info.state.PLCBak[0].GRU1[:],
		info.state.PLCBak[0].GRU2[:],
		info.state.PLCBak[1].GRU1[:],
		info.state.PLCBak[1].GRU2[:],
	} {
		if err := readBits(dst); err != nil {
			return libopusDecoderDREDDecodeFloatInfo{}, err
		}
	}
	info.fargan.DeemphMem = reader.Float32()
	for _, dst := range [][]float32{
		info.fargan.PitchBuf[:],
		info.fargan.CondConv1State[:],
		info.fargan.FWC0Mem[:],
		info.fargan.GRU1State[:],
		info.fargan.GRU2State[:],
		info.fargan.GRU3State[:],
	} {
		if err := readBits(dst); err != nil {
			return libopusDecoderDREDDecodeFloatInfo{}, err
		}
	}
	for _, dst := range [][]float32{
		info.celt48k.PreemphMem[:],
		info.celt48k.PLCPCM[:],
	} {
		if err := readBits(dst); err != nil {
			return libopusDecoderDREDDecodeFloatInfo{}, err
		}
	}
	for _, dst := range [][]float32{
		info.silk.SMid[:],
		info.silk.OutBuf[:],
		info.silk.SLPCQ14[:],
		info.silk.ExcQ14[:],
		info.silk.ResamplerIIR[:],
		info.silk.ResamplerFIR[:],
		info.silk.ResamplerDelay[:],
	} {
		if err := readBits(dst); err != nil {
			return libopusDecoderDREDDecodeFloatInfo{}, err
		}
	}
	if reader.Remaining() >= 4*(2+1+len(info.celt48k.WarmupPLCUpdate)) {
		warmupPLCPreemph := []float32{0}
		for _, dst := range [][]float32{
			info.celt48k.WarmupPreemphMem[:],
			warmupPLCPreemph,
			info.celt48k.WarmupPLCUpdate[:],
		} {
			if err := readBits(dst); err != nil {
				return libopusDecoderDREDDecodeFloatInfo{}, err
			}
		}
		info.celt48k.WarmupPLCPreemph = warmupPLCPreemph[0]
	}
	if err := reader.ExpectConsumed(); err != nil {
		return libopusDecoderDREDDecodeFloatInfo{}, err
	}
	return info, nil
}

func requireLibopusDREDDecodeParsed(t testing.TB, info libopusDecoderDREDDecodeFloatInfo, label string) {
	t.Helper()
	if info.parseRet <= 0 {
		t.Fatalf("%s libopus DRED parse ret=%d want >0 (dredEnd=%d)", label, info.parseRet, info.dredEnd)
	}
	if info.parseRet < info.dredEnd {
		t.Fatalf("%s libopus DRED parse ret=%d before dredEnd=%d", label, info.parseRet, info.dredEnd)
	}
}

func assertDecoderDREDPLCStateApproxEqual(t *testing.T, got, want lpcnetplc.StateSnapshot, label string) {
	t.Helper()
	assertDecoderDREDPLCStateApproxEqualWithin(t, got, want, label, 1e-4)
}

func assertDecoderDREDPLCStateApproxEqualWithin(t *testing.T, got, want lpcnetplc.StateSnapshot, label string, tol float64) {
	t.Helper()
	if got.Blend != want.Blend ||
		got.LossCount != want.LossCount ||
		got.AnalysisGap != want.AnalysisGap ||
		got.AnalysisPos != want.AnalysisPos ||
		got.PredictPos != want.PredictPos ||
		got.FECReadPos != want.FECReadPos ||
		got.FECFillPos != want.FECFillPos ||
		got.FECSkip != want.FECSkip {
		t.Fatalf("%s header=%+v want %+v", label, got, want)
	}
	assertFloat32ApproxEqual(t, got.Features[:lpcnetplc.NumFeatures], want.Features[:lpcnetplc.NumFeatures], label+" features", tol)
	assertFloat32ApproxEqual(t, got.Cont[:], want.Cont[:], label+" continuity", tol)
	assertFloat32ApproxEqual(t, got.PCM[:], want.PCM[:], label+" pcm history", tol)
	assertFloat32ApproxEqual(t, got.PLCNet.GRU1[:], want.PLCNet.GRU1[:], label+" plc net gru1", tol)
	assertFloat32ApproxEqual(t, got.PLCNet.GRU2[:], want.PLCNet.GRU2[:], label+" plc net gru2", tol)
	assertFloat32ApproxEqual(t, got.PLCBak[0].GRU1[:], want.PLCBak[0].GRU1[:], label+" plc bak0 gru1", tol)
	assertFloat32ApproxEqual(t, got.PLCBak[0].GRU2[:], want.PLCBak[0].GRU2[:], label+" plc bak0 gru2", tol)
	assertFloat32ApproxEqual(t, got.PLCBak[1].GRU1[:], want.PLCBak[1].GRU1[:], label+" plc bak1 gru1", tol)
	assertFloat32ApproxEqual(t, got.PLCBak[1].GRU2[:], want.PLCBak[1].GRU2[:], label+" plc bak1 gru2", tol)
}

func assertDecoderDREDFARGANStateApproxEqual(t *testing.T, got, want lpcnetplc.FARGANSnapshot, label string) {
	t.Helper()
	assertDecoderDREDFARGANStateApproxEqualWithin(t, got, want, label, 1e-4)
}

func assertDecoderDREDFARGANStateApproxEqualWithin(t *testing.T, got, want lpcnetplc.FARGANSnapshot, label string, tol float64) {
	t.Helper()
	if got.ContInitialized != want.ContInitialized || got.LastPeriod != want.LastPeriod {
		t.Fatalf("%s header=%+v want %+v", label, got, want)
	}
	if math.Abs(float64(got.DeemphMem-want.DeemphMem)) > tol {
		t.Fatalf("%s deemph=%f want %f", label, got.DeemphMem, want.DeemphMem)
	}
	assertFloat32ApproxEqual(t, got.PitchBuf[:], want.PitchBuf[:], label+" pitch", tol)
	assertFloat32ApproxEqual(t, got.CondConv1State[:], want.CondConv1State[:], label+" cond", tol)
	assertFloat32ApproxEqual(t, got.FWC0Mem[:], want.FWC0Mem[:], label+" fwc0", tol)
	assertFloat32ApproxEqual(t, got.GRU1State[:], want.GRU1State[:], label+" gru1", tol)
	assertFloat32ApproxEqual(t, got.GRU2State[:], want.GRU2State[:], label+" gru2", tol)
	assertFloat32ApproxEqual(t, got.GRU3State[:], want.GRU3State[:], label+" gru3", tol)
}

func assertDecoderDREDCELT48kBridgeApproxEqual(t *testing.T, dec *Decoder, want libopusDecoderDREDCELTSnapshot, label string) {
	t.Helper()
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want, label, 1e-4)
}

func assertDecoderDREDCELT48kBridgeApproxEqualWithin(t *testing.T, dec *Decoder, want libopusDecoderDREDCELTSnapshot, label string, tol float64) {
	t.Helper()
	var plcState celt.PLCStateSnapshot
	var preemphMem [2]float32
	var plcFill int
	var plcPreemphasisMem float32
	var lastNeural bool
	var plcPCM [4 * lpcnetplc.FrameSize]float32
	if dec.celtDecoder != nil {
		plcState = dec.celtDecoder.SnapshotPLCState()
		preemphMem = dec.celtDecoder.SnapshotPreemphasisState()
	}
	if bridge := dec.dred48kBridgeState(); bridge != nil {
		plcFill = bridge.dredPLCFill
		plcPreemphasisMem = bridge.dredPLCPreemphMem
		lastNeural = bridge.dredLastNeural
		plcPCM = bridge.dredPLCPCM
	}
	if plcState.LastFrameType != want.LastFrameType || plcState.PLCDuration != want.PLCDuration || plcState.SkipPLC != (want.SkipPLC != 0) {
		t.Fatalf("%s celt plc state=%+v want {LastFrameType:%d PLCDuration:%d SkipPLC:%t}", label, plcState, want.LastFrameType, want.PLCDuration, want.SkipPLC != 0)
	}
	assertFloat32ApproxEqual(t, preemphMem[:], want.PreemphMem[:], label+" celt preemph_memD", tol)
	if plcFill != want.PLCFill {
		t.Fatalf("%s fill=%d want %d (lastFrameType=%d plcDuration=%d skipPLC=%d preemph=%f)", label, plcFill, want.PLCFill, want.LastFrameType, want.PLCDuration, want.SkipPLC, want.PLCPreemphasisMem)
	}
	if math.Abs(float64(plcPreemphasisMem-want.PLCPreemphasisMem)) > tol {
		t.Fatalf("%s preemph=%f want %f", label, plcPreemphasisMem, want.PLCPreemphasisMem)
	}
	wantNeural := want.LastFrameType == libopusCELTFramePLCNeural || want.LastFrameType == libopusCELTFrameDRED
	if lastNeural != wantNeural {
		t.Fatalf("%s lastNeural=%v want %v (lastFrameType=%d)", label, lastNeural, wantNeural, want.LastFrameType)
	}
	assertFloat32ApproxEqual(t, plcPCM[:], want.PLCPCM[:], label+" plc pcm", tol)
}

func assertDecoderDREDSILKStateApproxEqualWithin(t *testing.T, dec *Decoder, want libopusDecoderDREDSILKSnapshot, bandwidth silkpkg.Bandwidth, label string, tol float64) {
	t.Helper()
	if dec == nil || dec.silkDecoder == nil {
		t.Fatalf("%s missing SILK decoder", label)
	}
	got := dec.silkDecoder.SnapshotDecoderState(bandwidth, 0)
	if got.LagPrev != want.LagPrev ||
		got.LastGainIndex != want.LastGainIndex ||
		got.LossCount != want.LossCount ||
		got.PrevSignalType != want.PrevSignalType {
		t.Fatalf("%s header={LagPrev:%d LastGainIndex:%d LossCount:%d PrevSignalType:%d} want {LagPrev:%d LastGainIndex:%d LossCount:%d PrevSignalType:%d}",
			label,
			got.LagPrev, got.LastGainIndex, got.LossCount, got.PrevSignalType,
			want.LagPrev, want.LastGainIndex, want.LossCount, want.PrevSignalType)
	}
	assertFloat32ApproxEqual(t, got.SMid[:], want.SMid[:], label+" smid", tol)
	assertFloat32ApproxEqual(t, got.OutBuf[:], want.OutBuf[:], label+" outbuf", tol)
	assertFloat32ApproxEqual(t, got.SLPCQ14[:], want.SLPCQ14[:], label+" slpc_q14", tol)
	assertFloat32ApproxEqual(t, got.ExcQ14[:], want.ExcQ14[:], label+" exc_q14", tol)
	assertFloat32ApproxEqual(t, got.ResamplerIIR[:], want.ResamplerIIR[:], label+" resampler iir", tol)
	assertFloat32ApproxEqual(t, got.ResamplerFIR[:], want.ResamplerFIR[:], label+" resampler fir", tol)
	assertFloat32ApproxEqual(t, got.ResamplerDelay[:], want.ResamplerDelay[:], label+" resampler delay", tol)
}

func snapshotDecoderDREDCELT48kForTest(t *testing.T, dec *Decoder) libopusDecoderDREDCELTSnapshot {
	t.Helper()
	var snap libopusDecoderDREDCELTSnapshot
	if dec == nil || dec.celtDecoder == nil {
		return snap
	}
	plcState := dec.celtDecoder.SnapshotPLCState()
	preemphMem := dec.celtDecoder.SnapshotPreemphasisState()
	snap.LastFrameType = plcState.LastFrameType
	snap.PLCDuration = plcState.PLCDuration
	if plcState.SkipPLC {
		snap.SkipPLC = 1
	}
	snap.PreemphMem = preemphMem
	if bridge := dec.dred48kBridgeState(); bridge != nil {
		snap.PLCFill = bridge.dredPLCFill
		snap.PLCPreemphasisMem = bridge.dredPLCPreemphMem
		snap.PLCPCM = bridge.dredPLCPCM
	}
	return snap
}

func prepareExplicitDREDDecodeParityState(t *testing.T) (*Decoder, *DRED, libopusDREDPacket, []byte, int) {
	return prepareExplicitDREDDecodeParityStateForFrameSize(t, 960)
}

func prepareExplicitDREDDecodeParityStateForFrameSize(t *testing.T, frameSize int) (*Decoder, *DRED, libopusDREDPacket, []byte, int) {
	return prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
		FrameSize: frameSize,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
}

func prepareExplicitDREDDecodeParityState16k(t *testing.T) (*Decoder, *DRED, libopusDREDPacket, []byte, int) {
	return prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
		FrameSize: 480,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
}

func prepareExplicitDREDDecodeParityState16kForFrameSize(t *testing.T, frameSize int) (*Decoder, *DRED, libopusDREDPacket, []byte, int) {
	return prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
		FrameSize: frameSize,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
}

func prepareExplicitDREDDecodeParityStateForDecoderRateAndFrameSize(t *testing.T, decoderSampleRate, frameSize int) (*Decoder, *DRED, libopusDREDPacket, []byte, int) {
	return prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, decoderSampleRate, libopusDREDPacketConfig{
		FrameSize: frameSize,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
}

func prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t *testing.T, decoderSampleRate int, packetCfg libopusDREDPacketConfig) (*Decoder, *DRED, libopusDREDPacket, []byte, int) {
	t.Helper()

	packetInfo, err := emitLibopusDREDPacketWithConfig(packetCfg)
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred model", err)
	}
	channels := 1
	toc := ParseTOC(packetInfo.packet[0])
	if toc.Stereo {
		channels = 2
	}
	if channels < 1 || channels > 2 {
		t.Skipf("explicit DRED decode parity requires mono or stereo packet, got sampleRate=%d channels=%d", packetInfo.sampleRate, channels)
	}
	if packetCfg.ForceMode != toc.Mode {
		t.Skipf("explicit DRED decode parity requires mode=%v packet, got mode=%v", packetCfg.ForceMode, toc.Mode)
	}
	if packetCfg.Bandwidth != toc.Bandwidth {
		t.Skipf("explicit DRED decode parity requires bandwidth=%v packet, got bandwidth=%v", packetCfg.Bandwidth, toc.Bandwidth)
	}
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	seedPacket := makeValidMonoPacketForModeBandwidthFrameSizeForDREDTest(t, toc.Mode, toc.Bandwidth, toc.FrameSize)

	dec, err := NewDecoder(DefaultDecoderConfig(decoderSampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	setDecoderComplexityForLibopusDREDParityTest(t, dec)
	if err := dec.SetDNNBlob(decoderBlob); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}

	seedPCM := make([]float32, dec.maxPacketSamples*channels)
	n, err := dec.Decode(seedPacket, seedPCM)
	if err != nil {
		t.Fatalf("Decode(seed packet) error: %v", err)
	}
	if n <= 0 {
		t.Skip("carrier packet returned no audio")
	}

	standalone := NewDREDDecoder()
	if err := standalone.SetDNNBlob(modelBlob); err != nil {
		t.Fatalf("standalone SetDNNBlob(real model) error: %v", err)
	}
	dred := NewDRED()
	maxDRED, parseRate := libopusDREDRequestForDecoder(packetInfo, decoderSampleRate)
	if _, _, err := standalone.Parse(dred, packetInfo.packet, maxDRED, parseRate, true); err != nil {
		t.Fatalf("standalone Parse error: %v", err)
	}
	if err := standalone.Process(dred, dred); err != nil {
		t.Fatalf("standalone Process error: %v", err)
	}
	if !dred.Processed() {
		t.Fatal("standalone DRED did not reach processed state")
	}
	return dec, dred, packetInfo, seedPacket, n
}

func prepareCachedDREDDecodeParityStateForPacket(t *testing.T, packetInfo libopusDREDPacket) (*Decoder, int) {
	t.Helper()
	return prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, packetInfo.sampleRate, packetInfo, 1)
}

func prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t *testing.T, decoderSampleRate int, packetInfo libopusDREDPacket) (*Decoder, int) {
	t.Helper()
	return prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, decoderSampleRate, packetInfo, 1)
}

func prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t *testing.T, decoderSampleRate int, packetInfo libopusDREDPacket, wantChannels int) (*Decoder, int) {
	t.Helper()

	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred model", err)
	}

	channels := 1
	toc := ParseTOC(packetInfo.packet[0])
	if toc.Stereo {
		channels = 2
	}
	if wantChannels > 0 && channels != wantChannels {
		t.Skipf("cached DRED decode parity requires %d-channel packet, got sampleRate=%d channels=%d", wantChannels, packetInfo.sampleRate, channels)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(decoderSampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	setDecoderComplexityForLibopusDREDParityTest(t, dec)
	if err := dec.SetDNNBlob(decoderBlob); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	blob, err := dnnblob.Clone(modelBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone(real model) error: %v", err)
	}
	if err := blob.ValidateDREDDecoderControl(); err != nil {
		t.Fatalf("ValidateDREDDecoderControl(real model) error: %v", err)
	}
	dec.setDREDDecoderBlob(blob)

	pcm := make([]float32, dec.maxPacketSamples*channels)
	n, err := dec.Decode(packetInfo.packet, pcm)
	if err != nil {
		t.Fatalf("Decode(DRED packet) error: %v", err)
	}
	if n <= 0 {
		t.Fatal("Decode(DRED packet) returned no audio")
	}
	if state := requireDecoderDREDState(t, dec); state.dredCache.Empty() || state.dredDecoded.NbLatents <= 0 {
		t.Fatal("Decode(DRED packet) did not retain processed DRED state")
	}
	return dec, n
}

func assertDecoderCachedDREDFirstLossMatchesLiveSequenceOracle(t *testing.T, label string, packetInfo libopusDREDPacket) {
	t.Helper()
	assertDecoderCachedDREDFirstLossMatchesLiveSequenceOracleWithTolerances(t, label, packetInfo, 1e-4, 1e-4, 1e-4, 1e-4)
}

func assertDecoderCachedDREDFirstLossMatchesLiveSequenceOracleWithTolerances(t *testing.T, label string, packetInfo libopusDREDPacket, pcmTol, plcTol, farganTol, celtTol float64) {
	t.Helper()

	dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 0, 0, false)
	if err != nil {
		libopustest.HelperUnavailable(t, label+" decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, label+" cached first-loss")
	if want.step0.ret != n {
		t.Fatalf("%s libopus cached decoder first-loss ret=%d want %d", label, want.step0.ret, n)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	got, err := dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("%s Decode(nil) error: %v", label, err)
	}
	if got != n {
		t.Fatalf("%s Decode(nil)=%d want %d", label, got, n)
	}

	assertFloat32ApproxEqual(t, pcm[:got], want.step0.pcm[:got], label+" first-loss live-sequence pcm", pcmTol)
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, label+" first-loss live-sequence plc", plcTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, label+" first-loss live-sequence fargan", farganTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, label+" first-loss live-sequence celt", celtTol)
}

func assertDecoderCachedDREDSecondLossMatchesLiveSequenceOracle(t *testing.T, label string, packetInfo libopusDREDPacket) {
	t.Helper()
	assertDecoderCachedDREDSecondLossMatchesLiveSequenceOracleWithTolerances(t, label, packetInfo, 1e-4, 1e-4, 1e-4, 1e-4)
}

func assertDecoderCachedDREDSecondLossMatchesLiveSequenceOracleWithTolerances(t *testing.T, label string, packetInfo libopusDREDPacket, pcmTol, plcTol, farganTol, celtTol float64) {
	t.Helper()

	dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 1, 2*n, false)
	if err != nil {
		libopustest.HelperUnavailable(t, label+" decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, label+" cached second-loss")
	if want.step0.ret != n {
		t.Fatalf("%s libopus cached decoder first warmup ret=%d want %d", label, want.step0.ret, n)
	}
	if want.step1.ret != n {
		t.Fatalf("%s libopus cached decoder second-loss ret=%d want %d", label, want.step1.ret, n)
	}

	pcm0 := make([]float32, dec.maxPacketSamples)
	got, err := dec.Decode(nil, pcm0)
	if err != nil {
		t.Fatalf("%s Decode(nil, first) error: %v", label, err)
	}
	if got != n {
		t.Fatalf("%s Decode(nil, first)=%d want %d", label, got, n)
	}
	assertFloat32ApproxEqual(t, pcm0[:got], want.step0.pcm[:got], label+" warmup live-sequence pcm", pcmTol)
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, label+" warmup live-sequence plc", plcTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, label+" warmup live-sequence fargan", farganTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, label+" warmup live-sequence celt", celtTol)

	pcm1 := make([]float32, dec.maxPacketSamples)
	got, err = dec.Decode(nil, pcm1)
	if err != nil {
		t.Fatalf("%s Decode(nil, second) error: %v", label, err)
	}
	if got != n {
		t.Fatalf("%s Decode(nil, second)=%d want %d", label, got, n)
	}
	assertFloat32ApproxEqual(t, pcm1[:got], want.step1.pcm[:got], label+" second-loss live-sequence pcm", pcmTol)
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step1.state, label+" second-loss live-sequence plc", plcTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step1.fargan, label+" second-loss live-sequence fargan", farganTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step1.celt48k, label+" second-loss live-sequence celt", celtTol)
}

func decoderDREDLiveSequenceTolerances(frameSize int) (pcmTol, plcTol, farganTol, celtTol float64) {
	pcmTol, plcTol, farganTol, celtTol = 1e-4, 1e-4, 1e-4, 1e-4
	if frameSize >= 480 {
		pcmTol, plcTol, farganTol, celtTol = 1e-2, 1e-1, 2.5e-1, 3e-2
	}
	if frameSize >= 960 {
		// A 20 ms 48 kHz loss synthesizes three 16 kHz FARGAN frames.
		// Keep the sensitive recurrent state envelope, but pin PLC history tighter.
		return 5e-3, 6e-3, 9e-2, 5e-3
	}
	return pcmTol, plcTol, farganTol, celtTol
}

func TestDecoderCachedDREDDecodeMatrixMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthFullband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertDecoderCachedDREDFirstLossMatchesLiveSequenceOracleWithTolerances(t, "cached CELT", packetInfo, pcmTol, plcTol, farganTol, celtTol)
		})
	}
}

func TestDecoderCachedSILKDREDDecodeMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name              string
		decoderSampleRate int
	}{
		{name: "decoder_8000", decoderSampleRate: 8000},
		{name: "decoder_12000", decoderSampleRate: 12000},
		{name: "decoder_16000", decoderSampleRate: 16000},
		{name: "decoder_24000", decoderSampleRate: 24000},
		{name: "decoder_48000", decoderSampleRate: 48000},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			const frameSize = 960
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeSILK,
				Bandwidth: BandwidthWideband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			toc := ParseTOC(packetInfo.packet[0])
			if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband {
				t.Fatalf("cached SILK DRED test packet TOC=%+v, want SILK WB", toc)
			}

			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, tc.decoderSampleRate, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, tc.decoderSampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("cached SILK warmup samples=%d want %d at %d Hz", n, wantFrame, tc.decoderSampleRate)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, tc.decoderSampleRate)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, 1, n, 0, 0, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "cached SILK first-loss")
			if want.step0.ret != n {
				t.Fatalf("libopus cached SILK decoder first-loss ret=%d want %d", want.step0.ret, n)
			}
			if want.channels != 1 {
				t.Fatalf("libopus cached SILK decoder channels=%d want 1", want.channels)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil)=%d want %d", got, n)
			}

			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertFloat32ApproxEqual(t, pcm[:got], want.step0.pcm[:got], "cached SILK live-sequence first-loss pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "cached SILK live-sequence first-loss plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "cached SILK live-sequence first-loss fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, "cached SILK live-sequence first-loss celt", celtTol)
			assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.step0.silk, silkpkg.BandwidthWideband, "cached SILK live-sequence first-loss silk", max(celtTol, 1))

			decSecond, nSecond := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, tc.decoderSampleRate, packetInfo)
			if nSecond != wantFrame {
				t.Fatalf("cached SILK second-loss warmup samples=%d want %d at %d Hz", nSecond, wantFrame, tc.decoderSampleRate)
			}
			wantSecond, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, nSecond, 1, nSecond, 1, 2*nSecond, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, wantSecond, "cached SILK second-loss")
			if wantSecond.step1.ret != nSecond {
				t.Fatalf("libopus cached SILK decoder second-loss ret=%d want %d", wantSecond.step1.ret, nSecond)
			}

			pcm0 := make([]float32, decSecond.maxPacketSamples)
			got0, err := decSecond.Decode(nil, pcm0)
			if err != nil {
				t.Fatalf("Decode(nil, warmup) error: %v", err)
			}
			if got0 != nSecond {
				t.Fatalf("Decode(nil, warmup)=%d want %d", got0, nSecond)
			}
			pcm1 := make([]float32, decSecond.maxPacketSamples)
			got1, err := decSecond.Decode(nil, pcm1)
			if err != nil {
				t.Fatalf("Decode(nil, second) error: %v", err)
			}
			if got1 != nSecond {
				t.Fatalf("Decode(nil, second)=%d want %d", got1, nSecond)
			}
			assertFloat32ApproxEqual(t, pcm1[:got1], wantSecond.step1.pcm[:got1], "cached SILK live-sequence second-loss pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, decSecond).dredPLC.Snapshot(), wantSecond.step1.state, "cached SILK live-sequence second-loss plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, decSecond).dredFARGAN.Snapshot(), wantSecond.step1.fargan, "cached SILK live-sequence second-loss fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, decSecond, wantSecond.step1.celt48k, "cached SILK live-sequence second-loss celt", celtTol)
			assertDecoderDREDSILKStateApproxEqualWithin(t, decSecond, wantSecond.step1.silk, silkpkg.BandwidthWideband, "cached SILK live-sequence second-loss silk", max(celtTol, 16))
		})
	}
}

func TestDecoderCachedSILKDREDDecodeWithFECFallbackMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, sampleRate := range []int{8000, 16000, 48000} {
		sampleRate := sampleRate
		t.Run(fmt.Sprintf("decoder_%d", sampleRate), func(t *testing.T) {
			const frameSize = 960
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeSILK,
				Bandwidth: BandwidthWideband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			toc := ParseTOC(packetInfo.packet[0])
			if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband {
				t.Fatalf("cached SILK DRED FEC-fallback packet TOC=%+v, want SILK WB", toc)
			}
			firstFrameData, err := extractFirstFramePayload(packetInfo.packet, toc)
			if err != nil {
				t.Fatalf("extractFirstFramePayload: %v", err)
			}
			if packetHasLBRR(firstFrameData, toc) {
				t.Skip("cached SILK DRED FEC-fallback fixture unexpectedly carries LBRR")
			}

			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, sampleRate, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("cached SILK warmup samples=%d want %d at %d Hz", n, wantFrame, sampleRate)
			}

			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, sampleRate)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, 1, n, 0, 0, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "cached SILK FEC-fallback first-loss")
			if want.step0.ret != n {
				t.Fatalf("libopus cached SILK FEC-fallback first-loss ret=%d want %d", want.step0.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.DecodeWithFEC(packetInfo.packet, pcm, true)
			if err != nil {
				t.Fatalf("DecodeWithFEC(no LBRR) error: %v", err)
			}
			if got != n {
				t.Fatalf("DecodeWithFEC(no LBRR)=%d want %d", got, n)
			}

			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertFloat32ApproxEqual(t, pcm[:got], want.step0.pcm[:got], "cached SILK FEC-fallback live-sequence pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "cached SILK FEC-fallback live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "cached SILK FEC-fallback live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, "cached SILK FEC-fallback live-sequence celt", celtTol)
			assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.step0.silk, silkpkg.BandwidthWideband, "cached SILK FEC-fallback live-sequence silk", max(celtTol, 1))
		})
	}
}

func TestDecoderCachedStereoSILKDREDAPIRateMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 960
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     frameSize,
		ForceMode:     ModeSILK,
		Bandwidth:     BandwidthWideband,
		Channels:      2,
		ForceChannels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo SILK DRED packet", err)
	}
	if toc := ParseTOC(packetInfo.packet[0]); toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband || !toc.Stereo {
		t.Fatalf("cached stereo SILK DRED packet TOC=%+v, want stereo SILK WB", toc)
	}

	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		sampleRate := sampleRate
		t.Run(fmt.Sprintf("decoder_%d", sampleRate), func(t *testing.T) {
			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, sampleRate, packetInfo, 2)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("cached stereo SILK warmup samples=%d want %d at %d Hz", n, wantFrame, sampleRate)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, sampleRate)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, 1, n, 1, 2*n, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "stereo SILK decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "cached stereo SILK second-loss")
			if want.channels != 2 {
				t.Fatalf("libopus cached stereo SILK DRED channels=%d want 2", want.channels)
			}
			if want.step0.ret != n || want.step1.ret != n {
				t.Fatalf("libopus cached stereo SILK DRED ret=(%d,%d) want (%d,%d)", want.step0.ret, want.step1.ret, n, n)
			}

			pcm0 := make([]float32, dec.maxPacketSamples*dec.channels)
			got, err := dec.Decode(nil, pcm0)
			if err != nil {
				t.Fatalf("Decode(nil, first) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil, first)=%d want %d", got, n)
			}
			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(n)
			assertInterleavedStereoApproxDuplicated(t, pcm0[:got*dec.channels], got, "cached stereo SILK first loss", 1e-2)
			assertInterleavedStereoApproxDuplicated(t, want.step0.pcm, got, "libopus cached stereo SILK first loss", 1e-2)
			assertFloat32ApproxEqual(t, pcm0[:got*dec.channels], want.step0.pcm[:got*dec.channels], "cached stereo SILK first-loss live-sequence pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "cached stereo SILK first-loss live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "cached stereo SILK first-loss live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, "cached stereo SILK first-loss live-sequence celt", celtTol)
			assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.step0.silk, silkpkg.BandwidthWideband, "cached stereo SILK first-loss live-sequence silk", max(celtTol, 1))

			pcm1 := make([]float32, dec.maxPacketSamples*dec.channels)
			got, err = dec.Decode(nil, pcm1)
			if err != nil {
				t.Fatalf("Decode(nil, second) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil, second)=%d want %d", got, n)
			}
			assertInterleavedStereoApproxDuplicated(t, pcm1[:got*dec.channels], got, "cached stereo SILK second loss", 1e-2)
			assertInterleavedStereoApproxDuplicated(t, want.step1.pcm, got, "libopus cached stereo SILK second loss", 1e-2)
			assertFloat32ApproxEqual(t, pcm1[:got*dec.channels], want.step1.pcm[:got*dec.channels], "cached stereo SILK second-loss live-sequence pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step1.state, "cached stereo SILK second-loss live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step1.fargan, "cached stereo SILK second-loss live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step1.celt48k, "cached stereo SILK second-loss live-sequence celt", celtTol)
			assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.step1.silk, silkpkg.BandwidthWideband, "cached stereo SILK second-loss live-sequence silk", max(celtTol, 8))
		})
	}
}

func TestDecoderCachedStereoDREDDecodeMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 960
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     frameSize,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	if toc := ParseTOC(packetInfo.packet[0]); !toc.Stereo {
		t.Fatalf("cached stereo DRED parity forced mono packet, got TOC=%#x", packetInfo.packet[0])
	}

	dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, packetInfo.sampleRate, packetInfo, 2)
	if packetInfo.sampleRate != 48000 || n != frameSize {
		t.Skipf("cached stereo CELT live-sequence parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
	}
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 0, 0, false)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "cached stereo first-loss")
	if want.channels != 2 {
		t.Fatalf("libopus cached stereo DRED channels=%d want 2", want.channels)
	}
	if want.step0.ret != n {
		t.Fatalf("libopus cached stereo decoder first-loss ret=%d want %d", want.step0.ret, n)
	}

	pcm := make([]float32, dec.maxPacketSamples*dec.channels)
	got, err := dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}
	if got != n {
		t.Fatalf("Decode(nil)=%d want %d", got, n)
	}
	for i := 0; i < got; i++ {
		if d := math.Abs(float64(pcm[2*i] - pcm[2*i+1])); d != 0 {
			t.Fatalf("cached stereo DRED PCM not L=R duplicated at sample %d: |L-R|=%g", i, d)
		}
		if d := math.Abs(float64(want.step0.pcm[2*i] - want.step0.pcm[2*i+1])); d != 0 {
			t.Fatalf("libopus cached stereo DRED PCM not L=R duplicated at sample %d: |L-R|=%g", i, d)
		}
	}

	const stereoDREDStateTol = 1e-4
	const stereoDREDPCMTol = 1e-4
	assertFloat32ApproxEqual(t, pcm[:got*dec.channels], want.step0.pcm[:got*dec.channels], "cached stereo live-sequence first-loss pcm", stereoDREDPCMTol)
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "cached stereo live-sequence first-loss plc", stereoDREDStateTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "cached stereo live-sequence first-loss fargan", stereoDREDStateTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, "cached stereo live-sequence first-loss celt", stereoDREDPCMTol)
}

func TestDecoderCachedStereoDREDSecondLossMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 960
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     frameSize,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	if toc := ParseTOC(packetInfo.packet[0]); !toc.Stereo {
		t.Fatalf("cached stereo DRED parity forced mono packet, got TOC=%#x", packetInfo.packet[0])
	}

	dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, packetInfo.sampleRate, packetInfo, 2)
	if packetInfo.sampleRate != 48000 || n != frameSize {
		t.Skipf("cached stereo CELT second-loss parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
	}
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 1, 2*n, false)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "cached stereo second-loss")
	if want.channels != 2 {
		t.Fatalf("libopus cached stereo DRED channels=%d want 2", want.channels)
	}
	if want.step0.ret != n || want.step1.ret != n {
		t.Fatalf("libopus cached stereo DRED ret=(%d,%d) want (%d,%d)", want.step0.ret, want.step1.ret, n, n)
	}

	pcm0 := make([]float32, dec.maxPacketSamples*dec.channels)
	got, err := dec.Decode(nil, pcm0)
	if err != nil {
		t.Fatalf("Decode(nil, first) error: %v", err)
	}
	if got != n {
		t.Fatalf("Decode(nil, first)=%d want %d", got, n)
	}
	assertInterleavedStereoDuplicated(t, pcm0, got, "cached stereo first loss")
	assertInterleavedStereoDuplicated(t, want.step0.pcm, got, "libopus cached stereo first loss")

	pcm1 := make([]float32, dec.maxPacketSamples*dec.channels)
	got, err = dec.Decode(nil, pcm1)
	if err != nil {
		t.Fatalf("Decode(nil, second) error: %v", err)
	}
	if got != n {
		t.Fatalf("Decode(nil, second)=%d want %d", got, n)
	}
	assertInterleavedStereoDuplicated(t, pcm1, got, "cached stereo second loss")
	assertInterleavedStereoDuplicated(t, want.step1.pcm, got, "libopus cached stereo second loss")

	const stereoDREDStateTol = 1e-4
	const stereoDREDPCMTol = 1e-4
	assertFloat32ApproxEqual(t, pcm1[:got*dec.channels], want.step1.pcm[:got*dec.channels], "cached stereo live-sequence second-loss pcm", stereoDREDPCMTol)
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step1.state, "cached stereo live-sequence second-loss plc", stereoDREDStateTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step1.fargan, "cached stereo live-sequence second-loss fargan", stereoDREDStateTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step1.celt48k, "cached stereo live-sequence second-loss celt", stereoDREDPCMTol)
}

func TestDecoderCachedStereoDRED16kCELTMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		frameSize48k      = 960
		decoderSampleRate = 16000
	)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     frameSize48k,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	if toc := ParseTOC(packetInfo.packet[0]); !toc.Stereo || toc.Mode != ModeCELT {
		t.Fatalf("cached stereo 16k DRED parity packet mismatch: stereo=%t mode=%v", toc.Stereo, toc.Mode)
	}

	wantFrame, err := packetSamplesAtRate(packetInfo.packet, decoderSampleRate)
	if err != nil {
		t.Fatalf("packetSamplesAtRate: %v", err)
	}
	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, decoderSampleRate)
	pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(wantFrame)

	for _, tc := range []struct {
		name        string
		step1Source int
	}{
		{name: "first_loss"},
		{name: "second_loss", step1Source: 1},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, decoderSampleRate, packetInfo, 2)
			if n != wantFrame {
				t.Fatalf("cached stereo 16k warmup samples=%d want %d", n, wantFrame)
			}
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, 1, n, tc.step1Source, 2*n, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "cached stereo 16k "+tc.name)
			if want.channels != 2 {
				t.Fatalf("libopus cached stereo 16k DRED channels=%d want 2", want.channels)
			}
			if want.step0.ret != n {
				t.Fatalf("libopus cached stereo 16k first-loss ret=%d want %d", want.step0.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples*dec.channels)
			got, err := dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil, first) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil, first)=%d want %d", got, n)
			}
			comparePCM := pcm[:got*dec.channels]
			compareState := want.step0
			compareLabel := "cached stereo 16k first-loss"

			if tc.step1Source != 0 {
				pcm1 := make([]float32, dec.maxPacketSamples*dec.channels)
				got, err = dec.Decode(nil, pcm1)
				if err != nil {
					t.Fatalf("Decode(nil, second) error: %v", err)
				}
				if got != n {
					t.Fatalf("Decode(nil, second)=%d want %d", got, n)
				}
				if want.step1.ret != n {
					t.Fatalf("libopus cached stereo 16k second-loss ret=%d want %d", want.step1.ret, n)
				}
				comparePCM = pcm1[:got*dec.channels]
				compareState = want.step1
				compareLabel = "cached stereo 16k second-loss"
			}

			assertInterleavedStereoApproxDuplicated(t, comparePCM, n, compareLabel, 1e-2)
			assertInterleavedStereoApproxDuplicated(t, compareState.pcm, n, compareLabel+" libopus", 1e-2)
			assertFloat32ApproxEqual(t, comparePCM, compareState.pcm[:n*dec.channels], compareLabel+" live-sequence pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), compareState.state, compareLabel+" live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), compareState.fargan, compareLabel+" live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, compareState.celt48k, compareLabel+" live-sequence celt", celtTol)
		})
	}
}

func TestDecoderCachedStereoDREDThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 960
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     frameSize,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	if toc := ParseTOC(packetInfo.packet[0]); !toc.Stereo {
		t.Fatalf("cached stereo DRED parity forced mono packet, got TOC=%#x", packetInfo.packet[0])
	}
	nextPacket := makeValidCELTPacketForDREDTest(t)

	dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, packetInfo.sampleRate, packetInfo, 2)
	if packetInfo.sampleRate != 48000 || n != frameSize {
		t.Skipf("cached stereo CELT follow-up parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
	}
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 0, 0, true)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "cached stereo first-loss next-packet")
	if want.channels != 2 {
		t.Fatalf("libopus cached stereo DRED channels=%d want 2", want.channels)
	}
	if want.step0.ret != n {
		t.Fatalf("libopus cached stereo first-loss ret=%d want %d", want.step0.ret, n)
	}
	if want.next.ret <= 0 {
		t.Fatalf("libopus cached stereo follow-up ret=%d want >0", want.next.ret)
	}

	pcm := make([]float32, dec.maxPacketSamples*dec.channels)
	got, err := dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}
	if got != n {
		t.Fatalf("Decode(nil)=%d want %d", got, n)
	}
	assertInterleavedStereoDuplicated(t, pcm, got, "cached stereo first loss")
	assertInterleavedStereoDuplicated(t, want.step0.pcm, got, "libopus cached stereo first loss")

	nextPCM := make([]float32, dec.maxPacketSamples*dec.channels)
	gotNext, err := dec.Decode(nextPacket, nextPCM)
	if err != nil {
		t.Fatalf("Decode(next stereo CELT packet) error: %v", err)
	}
	if gotNext != want.next.ret {
		t.Fatalf("Decode(next stereo CELT packet)=%d want %d", gotNext, want.next.ret)
	}

	const stereoDREDStateTol = 1e-4
	const stereoDREDPCMTol = 1e-4
	assertFloat32ApproxEqual(t, nextPCM[:gotNext*dec.channels], want.next.pcm[:gotNext*dec.channels], "cached stereo next packet live-sequence pcm", stereoDREDPCMTol)
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "cached stereo next packet live-sequence plc", stereoDREDStateTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "cached stereo next packet live-sequence fargan", stereoDREDStateTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.next.celt48k, "cached stereo next packet live-sequence celt", stereoDREDPCMTol)
}

func TestDecoderCachedStereoDREDSecondLossThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 960
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     frameSize,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	if toc := ParseTOC(packetInfo.packet[0]); !toc.Stereo {
		t.Fatalf("cached stereo DRED parity forced mono packet, got TOC=%#x", packetInfo.packet[0])
	}
	nextPacket := makeValidCELTPacketForDREDTest(t)

	dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, packetInfo.sampleRate, packetInfo, 2)
	if packetInfo.sampleRate != 48000 || n != frameSize {
		t.Skipf("cached stereo CELT second-loss follow-up parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
	}
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 1, 2*n, true)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "cached stereo second-loss next-packet")
	if want.channels != 2 {
		t.Fatalf("libopus cached stereo DRED channels=%d want 2", want.channels)
	}
	if want.step0.ret != n || want.step1.ret != n {
		t.Fatalf("libopus cached stereo DRED ret=(%d,%d) want (%d,%d)", want.step0.ret, want.step1.ret, n, n)
	}
	if want.next.ret <= 0 {
		t.Fatalf("libopus cached stereo follow-up ret=%d want >0", want.next.ret)
	}

	pcm0 := make([]float32, dec.maxPacketSamples*dec.channels)
	got, err := dec.Decode(nil, pcm0)
	if err != nil {
		t.Fatalf("Decode(nil, first) error: %v", err)
	}
	if got != n {
		t.Fatalf("Decode(nil, first)=%d want %d", got, n)
	}
	assertInterleavedStereoDuplicated(t, pcm0, got, "cached stereo first loss")
	assertInterleavedStereoDuplicated(t, want.step0.pcm, got, "libopus cached stereo first loss")

	pcm1 := make([]float32, dec.maxPacketSamples*dec.channels)
	got, err = dec.Decode(nil, pcm1)
	if err != nil {
		t.Fatalf("Decode(nil, second) error: %v", err)
	}
	if got != n {
		t.Fatalf("Decode(nil, second)=%d want %d", got, n)
	}
	assertInterleavedStereoDuplicated(t, pcm1, got, "cached stereo second loss")
	assertInterleavedStereoDuplicated(t, want.step1.pcm, got, "libopus cached stereo second loss")

	nextPCM := make([]float32, dec.maxPacketSamples*dec.channels)
	gotNext, err := dec.Decode(nextPacket, nextPCM)
	if err != nil {
		t.Fatalf("Decode(next stereo CELT packet) error: %v", err)
	}
	if gotNext != want.next.ret {
		t.Fatalf("Decode(next stereo CELT packet)=%d want %d", gotNext, want.next.ret)
	}

	const stereoDREDStateTol = 1e-4
	const stereoDREDPCMTol = 1e-4
	assertFloat32ApproxEqual(t, nextPCM[:gotNext*dec.channels], want.next.pcm[:gotNext*dec.channels], "cached stereo second-loss next packet live-sequence pcm", stereoDREDPCMTol)
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "cached stereo second-loss next packet live-sequence plc", stereoDREDStateTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "cached stereo second-loss next packet live-sequence fargan", stereoDREDStateTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.next.celt48k, "cached stereo second-loss next packet live-sequence celt", stereoDREDPCMTol)
}

func assertInterleavedStereoDuplicated(t *testing.T, pcm []float32, samples int, label string) {
	t.Helper()
	if len(pcm) < samples*2 {
		t.Fatalf("%s PCM length=%d too short for %d stereo samples", label, len(pcm), samples)
	}
	for i := 0; i < samples; i++ {
		if d := math.Abs(float64(pcm[2*i] - pcm[2*i+1])); d != 0 {
			t.Fatalf("%s PCM not L=R duplicated at sample %d: |L-R|=%g", label, i, d)
		}
	}
}

func assertInterleavedStereoApproxDuplicated(t *testing.T, pcm []float32, samples int, label string, tol float64) {
	t.Helper()
	if len(pcm) < samples*2 {
		t.Fatalf("%s PCM length=%d too short for %d stereo samples", label, len(pcm), samples)
	}
	var maxDrift float64
	for i := 0; i < samples; i++ {
		d := math.Abs(float64(pcm[2*i] - pcm[2*i+1]))
		if d > maxDrift {
			maxDrift = d
		}
		if d > tol {
			t.Fatalf("%s PCM not L=R duplicated at sample %d: |L-R|=%g max=%g tol=%g", label, i, d, maxDrift, tol)
		}
	}
}

type cachedStereoDREDLiveFlow int

const (
	cachedStereoDREDFirstLoss cachedStereoDREDLiveFlow = iota
	cachedStereoDREDSecondLoss
	cachedStereoDREDFirstLossThenNext
	cachedStereoDREDSecondLossThenNext
)

func (f cachedStereoDREDLiveFlow) name() string {
	switch f {
	case cachedStereoDREDFirstLoss:
		return "first_loss"
	case cachedStereoDREDSecondLoss:
		return "second_loss"
	case cachedStereoDREDFirstLossThenNext:
		return "first_loss_then_next"
	case cachedStereoDREDSecondLossThenNext:
		return "second_loss_then_next"
	default:
		return "unknown"
	}
}

func assertDecoderCachedStereoDREDLiveSequenceMatchesLibopus(t *testing.T, label string, packetCfg libopusDREDPacketConfig, nextPacket []byte, flow cachedStereoDREDLiveFlow) {
	t.Helper()
	packetCfg.Channels = 2
	packetCfg.ForceChannels = 2
	packetInfo, err := emitLibopusDREDPacketWithConfig(packetCfg)
	if err != nil {
		libopustest.HelperUnavailable(t, label+" dred packet", err)
	}
	toc := ParseTOC(packetInfo.packet[0])
	if !toc.Stereo || toc.Mode != packetCfg.ForceMode || toc.Bandwidth != packetCfg.Bandwidth || toc.FrameSize != packetCfg.FrameSize {
		t.Fatalf("%s forced stereo %v/%v/%d packet mismatch: stereo=%t mode=%v bandwidth=%v frame=%d", label, packetCfg.ForceMode, packetCfg.Bandwidth, packetCfg.FrameSize, toc.Stereo, toc.Mode, toc.Bandwidth, toc.FrameSize)
	}

	dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, packetInfo.sampleRate, packetInfo, 2)
	if packetInfo.sampleRate != 48000 || n != packetCfg.FrameSize {
		t.Skipf("%s cached stereo live-sequence parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", label, packetCfg.FrameSize, packetInfo.sampleRate, n)
	}

	step1Source := 0
	decodeNext := false
	switch flow {
	case cachedStereoDREDSecondLoss:
		step1Source = 1
	case cachedStereoDREDFirstLossThenNext:
		decodeNext = true
	case cachedStereoDREDSecondLossThenNext:
		step1Source = 1
		decodeNext = true
	}
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, step1Source, 2*n, decodeNext)
	if err != nil {
		libopustest.HelperUnavailable(t, label+" decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, label+" cached stereo first-loss")
	if want.channels != 2 {
		t.Fatalf("%s libopus cached stereo DRED channels=%d want 2", label, want.channels)
	}
	if want.step0.ret != n {
		t.Fatalf("%s libopus cached stereo first-loss ret=%d want %d", label, want.step0.ret, n)
	}
	if step1Source != 0 && want.step1.ret != n {
		t.Fatalf("%s libopus cached stereo second-loss ret=%d want %d", label, want.step1.ret, n)
	}
	if decodeNext && want.next.ret <= 0 {
		t.Fatalf("%s libopus cached stereo follow-up ret=%d want >0", label, want.next.ret)
	}

	const stereoStateTol = 3e-3
	const stereoPCMTol = 1e-4
	const stereoCELTTol = 3e-3
	const duplicateTol = 1e-2

	pcm0 := make([]float32, dec.maxPacketSamples*dec.channels)
	got, err := dec.Decode(nil, pcm0)
	if err != nil {
		t.Fatalf("%s Decode(nil, first) error: %v", label, err)
	}
	if got != n {
		t.Fatalf("%s Decode(nil, first)=%d want %d", label, got, n)
	}
	assertInterleavedStereoApproxDuplicated(t, pcm0, got, label+" first loss", duplicateTol)
	assertInterleavedStereoApproxDuplicated(t, want.step0.pcm, got, label+" libopus first loss", duplicateTol)

	comparePCM := pcm0[:got*dec.channels]
	compareSamples := got
	compareState := want.step0
	compareLabel := label + " first-loss"

	if step1Source != 0 {
		pcm1 := make([]float32, dec.maxPacketSamples*dec.channels)
		got, err = dec.Decode(nil, pcm1)
		if err != nil {
			t.Fatalf("%s Decode(nil, second) error: %v", label, err)
		}
		if got != n {
			t.Fatalf("%s Decode(nil, second)=%d want %d", label, got, n)
		}
		assertInterleavedStereoApproxDuplicated(t, pcm1, got, label+" second loss", duplicateTol)
		assertInterleavedStereoApproxDuplicated(t, want.step1.pcm, got, label+" libopus second loss", duplicateTol)
		comparePCM = pcm1[:got*dec.channels]
		compareSamples = got
		compareState = want.step1
		compareLabel = label + " second-loss"
	}

	if decodeNext {
		nextPCM := make([]float32, dec.maxPacketSamples*dec.channels)
		gotNext, err := dec.Decode(nextPacket, nextPCM)
		if err != nil {
			t.Fatalf("%s Decode(next packet) error: %v", label, err)
		}
		if gotNext != want.next.ret {
			t.Fatalf("%s Decode(next packet)=%d want %d", label, gotNext, want.next.ret)
		}
		comparePCM = nextPCM[:gotNext*dec.channels]
		compareSamples = gotNext
		compareState = want.next
		compareLabel = label + " next-packet"
	}

	assertFloat32ApproxEqual(t, comparePCM, compareState.pcm[:compareSamples*dec.channels], compareLabel+" live-sequence pcm", stereoPCMTol)
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), compareState.state, compareLabel+" live-sequence plc", stereoStateTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), compareState.fargan, compareLabel+" live-sequence fargan", stereoStateTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, compareState.celt48k, compareLabel+" live-sequence celt", stereoCELTTol)
}

func TestDecoderCachedStereoDREDCELTMatrixMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "wb_2_5ms", bandwidth: BandwidthWideband, frameSize: 120},
		{name: "wb_5ms", bandwidth: BandwidthWideband, frameSize: 240},
		{name: "wb_10ms", bandwidth: BandwidthWideband, frameSize: 480},
		{name: "wb_20ms", bandwidth: BandwidthWideband, frameSize: 960},
		{name: "swb_2_5ms", bandwidth: BandwidthSuperwideband, frameSize: 120},
		{name: "swb_5ms", bandwidth: BandwidthSuperwideband, frameSize: 240},
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_2_5ms", bandwidth: BandwidthFullband, frameSize: 120},
		{name: "fb_5ms", bandwidth: BandwidthFullband, frameSize: 240},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}
	flows := []cachedStereoDREDLiveFlow{
		cachedStereoDREDFirstLoss,
		cachedStereoDREDSecondLoss,
		cachedStereoDREDFirstLossThenNext,
		cachedStereoDREDSecondLossThenNext,
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			nextPacket := makeValidStereoCELTPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)
			for _, flow := range flows {
				flow := flow
				t.Run(flow.name(), func(t *testing.T) {
					assertDecoderCachedStereoDREDLiveSequenceMatchesLibopus(t, "cached stereo CELT "+tc.name+" "+flow.name(), libopusDREDPacketConfig{
						FrameSize: tc.frameSize,
						ForceMode: ModeCELT,
						Bandwidth: tc.bandwidth,
					}, nextPacket, flow)
				})
			}
		})
	}
}

func TestDecoderCachedStereoDREDHybridMatrixMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}
	flows := []cachedStereoDREDLiveFlow{
		cachedStereoDREDFirstLoss,
		cachedStereoDREDSecondLoss,
		cachedStereoDREDFirstLossThenNext,
		cachedStereoDREDSecondLossThenNext,
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			nextPacket := makeValidStereoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)
			for _, flow := range flows {
				flow := flow
				t.Run(flow.name(), func(t *testing.T) {
					assertDecoderCachedStereoDREDLiveSequenceMatchesLibopus(t, "cached stereo Hybrid "+tc.name+" "+flow.name(), libopusDREDPacketConfig{
						FrameSize: tc.frameSize,
						ForceMode: ModeHybrid,
						Bandwidth: tc.bandwidth,
					}, nextPacket, flow)
				})
			}
		})
	}
}

func TestDecoderCachedDREDDecodeCELTSuperwidebandMatrixMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertDecoderCachedDREDFirstLossMatchesLiveSequenceOracleWithTolerances(t, "cached CELT SWB", packetInfo, pcmTol, plcTol, farganTol, celtTol)
		})
	}
}

func TestDecoderCachedDREDThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthFullband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, frameSize)

			dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("cached CELT live-sequence parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 0, 0, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, fmt.Sprintf("cached CELT first-loss next-packet frame_size_%d", frameSize))
			if want.step0.ret != n {
				t.Fatalf("libopus cached CELT decoder first-loss ret=%d want %d", want.step0.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus cached CELT decoder follow-up ret=%d want >0", want.next.ret)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil)=%d want %d", got, n)
			}
			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertFloat32ApproxEqual(t, pcm[:got], want.step0.pcm[:got], "cached CELT live-sequence first-loss pcm", pcmTol)

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next CELT packet)=%d want %d", gotNext, want.next.ret)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.next.pcm[:gotNext], "cached CELT next packet live-sequence pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "cached CELT next packet live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "cached CELT next packet live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.next.celt48k, "cached CELT next packet live-sequence celt", celtTol)
		})
	}
}

func TestDecoderCachedDREDThenNextPacketCELTSuperwidebandMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthSuperwideband)

			dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("cached CELT SWB live-sequence parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 0, 0, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, fmt.Sprintf("cached CELT SWB first-loss next-packet frame_size_%d", frameSize))
			if want.step0.ret != n {
				t.Fatalf("libopus cached CELT SWB decoder first-loss ret=%d want %d", want.step0.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus cached CELT SWB decoder follow-up ret=%d want >0", want.next.ret)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil)=%d want %d", got, n)
			}
			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertFloat32ApproxEqual(t, pcm[:got], want.step0.pcm[:got], "cached CELT SWB live-sequence first-loss pcm", pcmTol)

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT SWB packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next CELT SWB packet)=%d want %d", gotNext, want.next.ret)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.next.pcm[:gotNext], "cached CELT SWB next packet live-sequence pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "cached CELT SWB next packet live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "cached CELT SWB next packet live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.next.celt48k, "cached CELT SWB next packet live-sequence celt", celtTol)
		})
	}
}

func TestDecoderCachedDREDSecondLossMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthFullband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertDecoderCachedDREDSecondLossMatchesLiveSequenceOracleWithTolerances(t, "cached CELT", packetInfo, pcmTol, plcTol, farganTol, celtTol)
		})
	}
}

func TestDecoderCachedDREDSecondLossCELTSuperwidebandMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertDecoderCachedDREDSecondLossMatchesLiveSequenceOracleWithTolerances(t, "cached CELT SWB", packetInfo, pcmTol, plcTol, farganTol, celtTol)
		})
	}
}

func TestDecoderCachedDREDSecondLossThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthFullband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, frameSize)

			dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("cached CELT second-loss live-sequence parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 1, 2*n, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, fmt.Sprintf("cached CELT second-loss next-packet frame_size_%d", frameSize))
			if want.step0.ret != n {
				t.Fatalf("libopus cached CELT decoder first warmup ret=%d want %d", want.step0.ret, n)
			}
			if want.step1.ret != n {
				t.Fatalf("libopus cached CELT decoder second-loss ret=%d want %d", want.step1.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus cached CELT decoder follow-up ret=%d want >0", want.next.ret)
			}

			pcm0 := make([]float32, dec.maxPacketSamples)
			got, err := dec.Decode(nil, pcm0)
			if err != nil {
				t.Fatalf("Decode(nil, first) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil, first)=%d want %d", got, n)
			}
			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertFloat32ApproxEqual(t, pcm0[:got], want.step0.pcm[:got], "cached CELT live-sequence warmup pcm", pcmTol)

			pcm1 := make([]float32, dec.maxPacketSamples)
			got, err = dec.Decode(nil, pcm1)
			if err != nil {
				t.Fatalf("Decode(nil, second) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil, second)=%d want %d", got, n)
			}
			assertFloat32ApproxEqual(t, pcm1[:got], want.step1.pcm[:got], "cached CELT live-sequence second-loss pcm", pcmTol)

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next CELT packet)=%d want %d", gotNext, want.next.ret)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.next.pcm[:gotNext], "cached CELT second-loss next packet live-sequence pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "cached CELT second-loss next packet live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "cached CELT second-loss next packet live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.next.celt48k, "cached CELT second-loss next packet live-sequence celt", celtTol)
		})
	}
}

func TestDecoderCachedDREDSecondLossThenNextPacketCELTSuperwidebandMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthSuperwideband)

			dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("cached CELT SWB second-loss live-sequence parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 1, 2*n, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, fmt.Sprintf("cached CELT SWB second-loss next-packet frame_size_%d", frameSize))
			if want.step0.ret != n {
				t.Fatalf("libopus cached CELT SWB decoder first warmup ret=%d want %d", want.step0.ret, n)
			}
			if want.step1.ret != n {
				t.Fatalf("libopus cached CELT SWB decoder second-loss ret=%d want %d", want.step1.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus cached CELT SWB decoder follow-up ret=%d want >0", want.next.ret)
			}

			pcm0 := make([]float32, dec.maxPacketSamples)
			got, err := dec.Decode(nil, pcm0)
			if err != nil {
				t.Fatalf("Decode(nil, first) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil, first)=%d want %d", got, n)
			}
			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertFloat32ApproxEqual(t, pcm0[:got], want.step0.pcm[:got], "cached CELT SWB live-sequence warmup pcm", pcmTol)

			pcm1 := make([]float32, dec.maxPacketSamples)
			got, err = dec.Decode(nil, pcm1)
			if err != nil {
				t.Fatalf("Decode(nil, second) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil, second)=%d want %d", got, n)
			}
			assertFloat32ApproxEqual(t, pcm1[:got], want.step1.pcm[:got], "cached CELT SWB live-sequence second-loss pcm", pcmTol)

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT SWB packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next CELT SWB packet)=%d want %d", gotNext, want.next.ret)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.next.pcm[:gotNext], "cached CELT SWB second-loss next packet live-sequence pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "cached CELT SWB second-loss next packet live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "cached CELT SWB second-loss next packet live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.next.celt48k, "cached CELT SWB second-loss next packet live-sequence celt", celtTol)
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			if packetInfo.sampleRate != 48000 || n != tc.frameSize {
				t.Skipf("hybrid explicit parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", tc.frameSize, packetInfo.sampleRate, n)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder hybrid DRED")
			if want.ret != n {
				t.Fatalf("libopus hybrid decoder DRED decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertFloat32ApproxEqual(t, pcm[:got], want.pcm[:got], "hybrid explicit libopus pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "hybrid explicit libopus plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "hybrid explicit libopus fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "hybrid explicit libopus celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecode16kMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, 16000)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("16 kHz hybrid explicit frame=%d got %d want %d", tc.frameSize, n, wantFrame)
			}

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder 16k hybrid DRED")
			if want.ret != n {
				t.Fatalf("libopus 16k hybrid decoder DRED decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertFloat32ApproxEqual(t, pcm[:got], want.pcm[:got], "16k hybrid explicit libopus pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "16k hybrid explicit libopus plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid explicit libopus fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "16k hybrid explicit libopus celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeThenNextPacketMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder hybrid DRED")
			if want.ret != n {
				t.Fatalf("libopus hybrid decoder DRED decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus hybrid decoder follow-up ret=%d want >0", want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.nextRet)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.nextPCM[:gotNext], "hybrid explicit next packet pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "hybrid explicit next packet plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "hybrid explicit next packet fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "hybrid explicit next packet celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeThenNextPacket16kMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder 16k hybrid DRED")
			if want.ret != n {
				t.Fatalf("libopus 16k hybrid decoder DRED decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus 16k hybrid decoder follow-up ret=%d want >0", want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.nextRet)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.nextPCM[:gotNext], "16k hybrid explicit next packet pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "16k hybrid explicit next packet plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid explicit next packet fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "16k hybrid explicit next packet celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeSecondLossMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder hybrid DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus hybrid decoder DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus hybrid decoder DRED second ret=%d want %d", want.ret, n)
			}

			pcm1 := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
			}

			assertFloat32ApproxEqual(t, pcm1[:got], want.pcm[:got], "hybrid explicit second loss pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "hybrid explicit second loss plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "hybrid explicit second loss fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "hybrid explicit second loss celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeSecondLoss16kMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder 16k hybrid DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus 16k hybrid decoder DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus 16k hybrid decoder DRED second ret=%d want %d", want.ret, n)
			}

			pcm1 := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
			}

			assertFloat32ApproxEqual(t, pcm1[:got], want.pcm[:got], "16k hybrid explicit second loss pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "16k hybrid explicit second loss plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid explicit second loss fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "16k hybrid explicit second loss celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeSecondLossThenNextPacketMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}
			pcm1 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder hybrid DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus hybrid decoder DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus hybrid decoder DRED second ret=%d want %d", want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus hybrid decoder second-loss follow-up ret=%d want >0", want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) after second loss error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next hybrid packet) after second loss=%d want %d", gotNext, want.nextRet)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.nextPCM[:gotNext], "hybrid explicit second-loss follow-up pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "hybrid explicit second-loss follow-up plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "hybrid explicit second-loss follow-up fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "hybrid explicit second-loss follow-up celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeSecondLossThenNextPacket16kMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}
			pcm1 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, 16000, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder 16k hybrid DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus 16k hybrid decoder DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus 16k hybrid decoder DRED second ret=%d want %d", want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus 16k hybrid decoder second-loss follow-up ret=%d want >0", want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) after second loss error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next hybrid packet) after second loss=%d want %d", gotNext, want.nextRet)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.nextPCM[:gotNext], "16k hybrid explicit second-loss follow-up pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "16k hybrid explicit second-loss follow-up plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid explicit second-loss follow-up fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "16k hybrid explicit second-loss follow-up celt")
		})
	}
}

func cachedHybridLiveSequenceTolerances(_ Bandwidth, frameSize int) (pcmTol, plcTol, farganTol, celtTol float64) {
	pcmTol, plcTol, farganTol, celtTol = decoderDREDLiveSequenceTolerances(frameSize)
	return pcmTol, plcTol, farganTol, celtTol
}

func TestDecoderCachedHybridDREDDecodeMatrixMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			pcmTol, plcTol, farganTol, celtTol := cachedHybridLiveSequenceTolerances(tc.bandwidth, tc.frameSize)
			assertDecoderCachedDREDFirstLossMatchesLiveSequenceOracleWithTolerances(t, "cached hybrid", packetInfo, pcmTol, plcTol, farganTol, celtTol)
		})
	}
}

func TestDecoderCachedHybridDREDThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 0, 0, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "cached hybrid first-loss next-packet "+tc.name)
			if want.step0.ret != n {
				t.Fatalf("libopus cached hybrid decoder first-loss ret=%d want %d", want.step0.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus cached hybrid decoder follow-up ret=%d want >0", want.next.ret)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil)=%d want %d", got, n)
			}
			pcmTol, plcTol, farganTol, celtTol := cachedHybridLiveSequenceTolerances(tc.bandwidth, tc.frameSize)
			assertFloat32ApproxEqual(t, pcm[:got], want.step0.pcm[:got], "cached hybrid live-sequence first-loss pcm", pcmTol)

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.next.ret)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.next.pcm[:gotNext], "cached hybrid next packet live-sequence pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "cached hybrid next packet live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "cached hybrid next packet live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.next.celt48k, "cached hybrid next packet live-sequence celt", celtTol)
		})
	}
}

func TestDecoderCachedHybridDREDSecondLossMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			pcmTol, plcTol, farganTol, celtTol := cachedHybridLiveSequenceTolerances(tc.bandwidth, tc.frameSize)
			assertDecoderCachedDREDSecondLossMatchesLiveSequenceOracleWithTolerances(t, "cached hybrid", packetInfo, pcmTol, plcTol, farganTol, celtTol)
		})
	}
}

func TestDecoderCachedHybridSecondLossThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 1, 2*n, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "cached hybrid second-loss next-packet "+tc.name)
			if want.step0.ret != n {
				t.Fatalf("libopus cached hybrid decoder first warmup ret=%d want %d", want.step0.ret, n)
			}
			if want.step1.ret != n {
				t.Fatalf("libopus cached hybrid decoder second-loss ret=%d want %d", want.step1.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus cached hybrid decoder follow-up ret=%d want >0", want.next.ret)
			}

			pcm0 := make([]float32, dec.maxPacketSamples)
			got, err := dec.Decode(nil, pcm0)
			if err != nil {
				t.Fatalf("Decode(nil, first) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil, first)=%d want %d", got, n)
			}
			pcmTol, plcTol, farganTol, celtTol := cachedHybridLiveSequenceTolerances(tc.bandwidth, tc.frameSize)
			assertFloat32ApproxEqual(t, pcm0[:got], want.step0.pcm[:got], "cached hybrid live-sequence warmup pcm", pcmTol)

			pcm1 := make([]float32, dec.maxPacketSamples)
			got, err = dec.Decode(nil, pcm1)
			if err != nil {
				t.Fatalf("Decode(nil, second) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil, second)=%d want %d", got, n)
			}
			assertFloat32ApproxEqual(t, pcm1[:got], want.step1.pcm[:got], "cached hybrid live-sequence second-loss pcm", pcmTol)

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.next.ret)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.next.pcm[:gotNext], "cached hybrid second-loss next packet live-sequence pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "cached hybrid second-loss next packet live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "cached hybrid second-loss next packet live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.next.celt48k, "cached hybrid second-loss next packet live-sequence celt", celtTol)
		})
	}
}

func TestDecoderCachedHybridDRED16kDecodeMatrixMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, 16000, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, 16000)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("16 kHz cached hybrid warmup samples=%d want %d", n, wantFrame)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, 16000)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, 1, n, 0, 0, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "16k cached hybrid first-loss "+tc.name)
			if want.step0.ret != n {
				t.Fatalf("libopus 16k cached hybrid decoder first-loss ret=%d want %d", want.step0.ret, n)
			}

			pcmTol, plcTol, farganTol, celtTol := cachedHybridLiveSequenceTolerances(tc.bandwidth, tc.frameSize)
			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil)=%d want %d", got, n)
			}

			assertFloat32ApproxEqual(t, pcm[:got], want.step0.pcm[:got], "16k cached hybrid first-loss live-sequence pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "16k cached hybrid first-loss live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "16k cached hybrid first-loss live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, "16k cached hybrid first-loss live-sequence celt", celtTol)
		})
	}
}

func TestDecoderCachedHybridDRED16kThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, 16000, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, 16000)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("16 kHz cached hybrid warmup samples=%d want %d", n, wantFrame)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, 16000)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, maxDRED, oracleRate, n, 1, n, 0, 0, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "16k cached hybrid first-loss next-packet "+tc.name)
			if want.step0.ret != n {
				t.Fatalf("libopus 16k cached hybrid decoder first-loss ret=%d want %d", want.step0.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus 16k cached hybrid decoder follow-up ret=%d want >0", want.next.ret)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil)=%d want %d", got, n)
			}
			pcmTol, plcTol, farganTol, celtTol := cachedHybridLiveSequenceTolerances(tc.bandwidth, tc.frameSize)
			assertFloat32ApproxEqual(t, pcm[:got], want.step0.pcm[:got], "16k cached hybrid live-sequence first-loss pcm", pcmTol)

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.next.ret)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.next.pcm[:gotNext], "16k cached hybrid next packet live-sequence pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "16k cached hybrid next packet live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "16k cached hybrid next packet live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.next.celt48k, "16k cached hybrid next packet live-sequence celt", celtTol)
		})
	}
}

func TestDecoderCachedHybridDRED16kSecondLossMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, 16000, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, 16000)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("16 kHz cached hybrid warmup samples=%d want %d", n, wantFrame)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, 16000)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, 1, n, 1, 2*n, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "16k cached hybrid second-loss "+tc.name)
			if want.step0.ret != n {
				t.Fatalf("libopus 16k cached hybrid decoder first warmup ret=%d want %d", want.step0.ret, n)
			}
			if want.step1.ret != n {
				t.Fatalf("libopus 16k cached hybrid decoder second-loss ret=%d want %d", want.step1.ret, n)
			}

			pcmTol, plcTol, farganTol, celtTol := cachedHybridLiveSequenceTolerances(tc.bandwidth, tc.frameSize)

			pcm0 := make([]float32, dec.maxPacketSamples)
			got, err := dec.Decode(nil, pcm0)
			if err != nil {
				t.Fatalf("Decode(nil, first) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil, first)=%d want %d", got, n)
			}
			assertFloat32ApproxEqual(t, pcm0[:got], want.step0.pcm[:got], "16k cached hybrid warmup live-sequence pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "16k cached hybrid warmup live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "16k cached hybrid warmup live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, "16k cached hybrid warmup live-sequence celt", celtTol)

			pcm1 := make([]float32, dec.maxPacketSamples)
			got, err = dec.Decode(nil, pcm1)
			if err != nil {
				t.Fatalf("Decode(nil, second) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil, second)=%d want %d", got, n)
			}
			assertFloat32ApproxEqual(t, pcm1[:got], want.step1.pcm[:got], "16k cached hybrid second-loss live-sequence pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step1.state, "16k cached hybrid second-loss live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step1.fargan, "16k cached hybrid second-loss live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step1.celt48k, "16k cached hybrid second-loss live-sequence celt", celtTol)
		})
	}
}

func TestDecoderCachedHybridDRED16kSecondLossThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, 16000, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, 16000)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("16 kHz cached hybrid warmup samples=%d want %d", n, wantFrame)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, 16000)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, maxDRED, oracleRate, n, 1, n, 1, 2*n, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "16k cached hybrid second-loss next-packet "+tc.name)
			if want.step0.ret != n {
				t.Fatalf("libopus 16k cached hybrid decoder first warmup ret=%d want %d", want.step0.ret, n)
			}
			if want.step1.ret != n {
				t.Fatalf("libopus 16k cached hybrid decoder second-loss ret=%d want %d", want.step1.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus 16k cached hybrid decoder follow-up ret=%d want >0", want.next.ret)
			}

			pcmTol, plcTol, farganTol, celtTol := cachedHybridLiveSequenceTolerances(tc.bandwidth, tc.frameSize)

			pcm0 := make([]float32, dec.maxPacketSamples)
			got, err := dec.Decode(nil, pcm0)
			if err != nil {
				t.Fatalf("Decode(nil, first) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil, first)=%d want %d", got, n)
			}
			assertFloat32ApproxEqual(t, pcm0[:got], want.step0.pcm[:got], "16k cached hybrid live-sequence warmup pcm", pcmTol)

			pcm1 := make([]float32, dec.maxPacketSamples)
			got, err = dec.Decode(nil, pcm1)
			if err != nil {
				t.Fatalf("Decode(nil, second) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil, second)=%d want %d", got, n)
			}
			assertFloat32ApproxEqual(t, pcm1[:got], want.step1.pcm[:got], "16k cached hybrid live-sequence second-loss pcm", pcmTol)

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.next.ret)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.next.pcm[:gotNext], "16k cached hybrid second-loss next packet live-sequence pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "16k cached hybrid second-loss next packet live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "16k cached hybrid second-loss next packet live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.next.celt48k, "16k cached hybrid second-loss next packet live-sequence celt", celtTol)
		})
	}
}

func TestDecoderExplicitDREDWarmup48kStateMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, _, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n <= 0 {
		t.Skipf("48 kHz warmup parity requires 48 kHz packet, got sampleRate=%d frame=%d", packetInfo.sampleRate, n)
	}
	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	if dec.celtDecoder == nil {
		t.Fatal("decoder missing CELT state after seed packet")
	}
	gotPreemph := dec.celtDecoder.SnapshotPreemphasisState()
	assertFloat32ApproxEqual(t, gotPreemph[:], want.celt48k.WarmupPreemphMem[:], "warmup celt preemph_memD", 1e-4)
	var gotPLCUpdate [4 * lpcnetplc.FrameSize]float32
	_, gotPLCPreemph := dec.celtDecoder.FillPLCUpdate16kMonoWithPreemphasisMem(gotPLCUpdate[:])
	assertFloat32ApproxEqual(t, []float32{gotPLCPreemph}, []float32{want.celt48k.WarmupPLCPreemph}, "warmup plc_preemphasis_mem", 1e-4)
	assertFloat32ApproxEqual(t, gotPLCUpdate[:], want.celt48k.WarmupPLCUpdate[:], "warmup plc_update", 1e-4)
}

// The ordinary cached Decode(nil) path follows libopus FRAME_PLC_NEURAL,
// while the explicit DRED API follows FRAME_DRED. These legacy equality tests
// remain as disabled scaffolding until they are rewritten against the separate
// live-sequence and explicit libopus oracles.

func TestDecoderExplicitDREDFirstConcealFrameBootstraps48kRuntime(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, _, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n < lpcnetplc.FrameSize {
		t.Skipf("48 kHz bootstrap regression requires 48 kHz packet and >=%d samples, got sampleRate=%d frame=%d", lpcnetplc.FrameSize, packetInfo.sampleRate, n)
	}
	if got := dec.primeDREDCELTEntryHistory(dec.prevMode, true); got == 0 {
		t.Fatal("primeDREDCELTEntryHistory() returned 0")
	}
	window := dec.queueExplicitDREDRecovery(dred, n, n)
	if window.NeededFeatureFrames == 0 {
		t.Fatal("queueExplicitDREDRecovery produced empty window")
	}
	var frame [lpcnetplc.FrameSize]float32
	if !requireDecoderDREDState(t, dec).dredPLC.GenerateConcealedFrameFloatWithAnalysis(&requireDecoderDREDState(t, dec).dredAnalysis, &requireDecoderDREDState(t, dec).dredPredictor, &requireDecoderDREDState(t, dec).dredFARGAN, frame[:]) {
		t.Fatal("ConcealFrameFloatWithAnalysis returned false after 48 kHz bootstrap")
	}
}

func TestDecoderExplicitDREDThreeConcealFramesBootstraps48kRuntime(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, _, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n < 3*lpcnetplc.FrameSize {
		t.Skipf("48 kHz triple-frame regression requires 48 kHz packet and >=%d samples, got sampleRate=%d frame=%d", 3*lpcnetplc.FrameSize, packetInfo.sampleRate, n)
	}
	if got := dec.primeDREDCELTEntryHistory(dec.prevMode, true); got == 0 {
		t.Fatal("primeDREDCELTEntryHistory() returned 0")
	}
	window := dec.queueExplicitDREDRecovery(dred, n, n)
	if window.NeededFeatureFrames == 0 {
		t.Fatal("queueExplicitDREDRecovery produced empty window")
	}
	var frame [lpcnetplc.FrameSize]float32
	for i := 0; i < 3; i++ {
		if !requireDecoderDREDState(t, dec).dredPLC.GenerateConcealedFrameFloatWithAnalysis(&requireDecoderDREDState(t, dec).dredAnalysis, &requireDecoderDREDState(t, dec).dredPredictor, &requireDecoderDREDState(t, dec).dredFARGAN, frame[:]) {
			t.Fatalf("GenerateConcealedFrameFloatWithAnalysis returned false at frame %d (plc=%+v fargan=%+v)", i, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), requireDecoderDREDState(t, dec).dredFARGAN.Snapshot())
		}
	}
}

func TestDecoderExplicitDREDThreeConcealFramesManualStep48kRuntime(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, _, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n < 3*lpcnetplc.FrameSize {
		t.Skipf("48 kHz manual-step regression requires 48 kHz packet and >=%d samples, got sampleRate=%d frame=%d", 3*lpcnetplc.FrameSize, packetInfo.sampleRate, n)
	}
	if got := dec.primeDREDCELTEntryHistory(dec.prevMode, true); got == 0 {
		t.Fatal("primeDREDCELTEntryHistory() returned 0")
	}
	window := dec.queueExplicitDREDRecovery(dred, n, n)
	if window.NeededFeatureFrames == 0 {
		t.Fatal("queueExplicitDREDRecovery produced empty window")
	}
	if !requireDecoderDREDState(t, dec).dredPLC.PrimeFirstLossWithAnalysis(&requireDecoderDREDState(t, dec).dredAnalysis, &requireDecoderDREDState(t, dec).dredPredictor, &requireDecoderDREDState(t, dec).dredFARGAN) {
		t.Fatal("PrimeFirstLossWithAnalysis returned false")
	}
	var (
		frame    [lpcnetplc.FrameSize]float32
		features [lpcnetplc.NumFeatures]float32
	)
	for i := 0; i < 3; i++ {
		requireDecoderDREDState(t, dec).dredPLC.ConcealmentFeatureStep(&requireDecoderDREDState(t, dec).dredPredictor)
		if got := requireDecoderDREDState(t, dec).dredPLC.FillCurrentFeatures(features[:]); got != len(features) {
			t.Fatalf("FillCurrentFeatures()=%d want %d", got, len(features))
		}
		if got := requireDecoderDREDState(t, dec).dredFARGAN.Synthesize(frame[:], features[:]); got != lpcnetplc.FrameSize {
			t.Fatalf("Synthesize()=%d want %d at frame %d (fargan=%+v)", got, lpcnetplc.FrameSize, i, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot())
		}
		requireDecoderDREDState(t, dec).dredPLC.QueueFeatures(features[:])
		requireDecoderDREDState(t, dec).dredPLC.FinishConcealedFrameFloat(frame[:])
	}
}

func TestDecoderExplicitDREDThreeConcealFramesMixedHelpers48kRuntime(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, _, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n < 3*lpcnetplc.FrameSize {
		t.Skipf("48 kHz mixed-helper regression requires 48 kHz packet and >=%d samples, got sampleRate=%d frame=%d", 3*lpcnetplc.FrameSize, packetInfo.sampleRate, n)
	}
	if got := dec.primeDREDCELTEntryHistory(dec.prevMode, true); got == 0 {
		t.Fatal("primeDREDCELTEntryHistory() returned 0")
	}
	window := dec.queueExplicitDREDRecovery(dred, n, n)
	if window.NeededFeatureFrames == 0 {
		t.Fatal("queueExplicitDREDRecovery produced empty window")
	}
	var frame [lpcnetplc.FrameSize]float32
	if !requireDecoderDREDState(t, dec).dredPLC.ConcealFrameFloatWithAnalysis(&requireDecoderDREDState(t, dec).dredAnalysis, &requireDecoderDREDState(t, dec).dredPredictor, &requireDecoderDREDState(t, dec).dredFARGAN, frame[:]) {
		t.Fatal("ConcealFrameFloatWithAnalysis(first) returned false")
	}
	for i := 1; i < 3; i++ {
		st := requireDecoderDREDState(t, dec)
		before := st.dredPLC.Snapshot()
		gotFEC := st.dredPLC.ConcealFrameFloat(&st.dredPredictor, &st.dredFARGAN, frame[:])
		wantFEC := before.FECReadPos != before.FECFillPos && before.FECSkip == 0
		if gotFEC != wantFEC {
			t.Fatalf("ConcealFrameFloat gotFEC=%v want %v at frame %d (fecRead=%d fecFill=%d fecSkip=%d)", gotFEC, wantFEC, i, before.FECReadPos, before.FECFillPos, before.FECSkip)
		}
		after := st.dredPLC.Snapshot()
		if after.AnalysisPos >= before.AnalysisPos {
			t.Fatalf("ConcealFrameFloat did not advance at frame %d (analysisPos=%d want <%d)", i, after.AnalysisPos, before.AnalysisPos)
		}
	}
}

func TestDecoderExplicitDREDDecodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder DRED decode ret=%d want %d", want.ret, n)
	}
	if want.channels != 1 {
		t.Fatalf("libopus decoder DRED decode channels=%d want 1", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
	}

	assertFloat32ApproxEqual(t, pcm[:n], want.pcm[:n], "explicit libopus pcm", 1e-4)
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit libopus plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit libopus fargan")
}

// TestDecoderExplicitStereoDREDDecodeMatchesLibopus exercises the stereo DRED
// runtime against libopus. The neural signal is mono, while CELT stereo state
// and overlap crossfade still follow celt_decode_lost().
func TestDecoderExplicitStereoDREDDecodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
		FrameSize:     960,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if dec.channels != 2 {
		t.Fatalf("stereo explicit DRED parity got decoder channels=%d, want 2", dec.channels)
	}

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder stereo DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder stereo DRED decode ret=%d want %d", want.ret, n)
	}
	if want.channels != 2 {
		t.Fatalf("libopus decoder stereo DRED decode channels=%d want 2", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples*dec.channels)
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
	}

	// This forced-stereo carrier yields the libopus mono neural duplicate
	// shape, so both sides should stay bit-exact L=R for this fixture.
	for i := 0; i < n; i++ {
		if d := math.Abs(float64(pcm[2*i] - pcm[2*i+1])); d != 0 {
			t.Fatalf("stereo DRED PCM not L=R duplicated at sample %d: |L-R|=%g", i, d)
		}
		if d := math.Abs(float64(want.pcm[2*i] - want.pcm[2*i+1])); d != 0 {
			t.Fatalf("libopus stereo DRED PCM not L=R duplicated at sample %d: |L-R|=%g", i, d)
		}
	}

	const stereoDREDStateTol = 1e-4
	const stereoDREDPCMTol = 1e-4
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit stereo libopus plc", stereoDREDStateTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit stereo libopus fargan", stereoDREDStateTol)
	assertFloat32ApproxEqual(t, pcm[:n*dec.channels], want.pcm[:n*dec.channels], "explicit stereo libopus pcm", stereoDREDPCMTol)
}

// TestDecoderExplicitStereoDRED16kDecodeMatchesLibopus covers the same CELT
// stereo DRED path through the 16 kHz decoder API.
func TestDecoderExplicitStereoDRED16kDecodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	// Force stereo at the libopus encoder control layer so this exercises a
	// real 16 kHz stereo carrier instead of the encoder's auto mono choice.
	probeInfo, probeErr := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     480,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if probeErr != nil {
		libopustest.HelperUnavailable(t, "dred packet", probeErr)
	}
	if !ParseTOC(probeInfo.packet[0]).Stereo {
		t.Fatalf("libopus dred emit helper produced mono TOC at 480-sample CELT FB despite forced channels (toc=0x%02x)", probeInfo.packet[0])
	}

	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
		FrameSize:     480,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if dec.channels != 2 {
		t.Fatalf("stereo explicit DRED 16k parity got decoder channels=%d, want 2", dec.channels)
	}

	want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder stereo 16k DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder stereo 16k DRED decode ret=%d want %d", want.ret, n)
	}
	if want.channels != 2 {
		t.Fatalf("libopus decoder stereo 16k DRED decode channels=%d want 2", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples*dec.channels)
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
	}

	// L=R interleaved invariant: both gopus and libopus must produce
	// L=R-duplicated stereo PCM for DRED concealment at 16 kHz, same as the
	// 48 kHz stereo case.
	for i := 0; i < n; i++ {
		if d := math.Abs(float64(pcm[2*i] - pcm[2*i+1])); d != 0 {
			t.Fatalf("gopus stereo 16k DRED PCM not L=R duplicated at sample %d: |L-R|=%g", i, d)
		}
		if d := math.Abs(float64(want.pcm[2*i] - want.pcm[2*i+1])); d != 0 {
			t.Fatalf("libopus stereo 16k DRED PCM not L=R duplicated at sample %d: |L-R|=%g", i, d)
		}
	}

	pcmTol, plcTol, farganTol, _ := decoderDREDLiveSequenceTolerances(480)
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit stereo 16k libopus plc", plcTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit stereo 16k libopus fargan", farganTol)
	assertFloat32ApproxEqual(t, pcm[:n*dec.channels], want.pcm[:n*dec.channels], "explicit stereo 16k libopus pcm", pcmTol)
}

// TestDecoderExplicitStereoHybridDRED16kDecodeMatchesLibopus exercises the
// stereo DRED runtime path at 16 kHz against a Hybrid SWB carrier packet
// (10 ms / 480 samples) instead of CELT FB. Libopus leaves tiny L/R drift on
// this forced Hybrid seam, so the duplicate-shape check is numerical.
func TestDecoderExplicitStereoHybridDRED16kDecodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	// Force stereo at the libopus encoder control layer so this exercises a
	// real 16 kHz stereo carrier instead of the encoder's auto mono choice.
	probeInfo, probeErr := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     480,
		ForceMode:     ModeHybrid,
		Bandwidth:     BandwidthSuperwideband,
		Channels:      2,
		ForceChannels: 2,
	})
	if probeErr != nil {
		libopustest.HelperUnavailable(t, "dred packet", probeErr)
	}
	if !ParseTOC(probeInfo.packet[0]).Stereo {
		t.Fatalf("libopus dred emit helper produced mono TOC at 480-sample Hybrid SWB despite forced channels (toc=0x%02x)", probeInfo.packet[0])
	}

	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
		FrameSize:     480,
		ForceMode:     ModeHybrid,
		Bandwidth:     BandwidthSuperwideband,
		Channels:      2,
		ForceChannels: 2,
	})
	if dec.channels != 2 {
		t.Fatalf("stereo explicit Hybrid DRED 16k parity got decoder channels=%d, want 2", dec.channels)
	}

	want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder stereo Hybrid 16k DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder stereo Hybrid 16k DRED decode ret=%d want %d", want.ret, n)
	}
	if want.channels != 2 {
		t.Fatalf("libopus decoder stereo Hybrid 16k DRED decode channels=%d want 2", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples*dec.channels)
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
	}

	const stereoHybridDuplicateTol = 3e-3
	for i := 0; i < n; i++ {
		if d := math.Abs(float64(pcm[2*i] - pcm[2*i+1])); d > stereoHybridDuplicateTol {
			t.Fatalf("gopus stereo Hybrid 16k DRED PCM not duplicated at sample %d: |L-R|=%g", i, d)
		}
		if d := math.Abs(float64(want.pcm[2*i] - want.pcm[2*i+1])); d > stereoHybridDuplicateTol {
			t.Fatalf("libopus stereo Hybrid 16k DRED PCM not duplicated at sample %d: |L-R|=%g", i, d)
		}
	}

	const stereoDREDStateTol = 1e-4
	const stereoDREDPCMTol = 1e-4
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit stereo Hybrid 16k libopus plc", stereoDREDStateTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit stereo Hybrid 16k libopus fargan", stereoDREDStateTol)
	assertFloat32ApproxEqual(t, pcm[:n*dec.channels], want.pcm[:n*dec.channels], "explicit stereo Hybrid 16k libopus pcm", stereoDREDPCMTol)
}

func TestDecoderExplicit16kHybridDREDDecodeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
		frameSize int
	}{
		{name: "swb_10ms", bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "swb_20ms", bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "fb_10ms", bandwidth: BandwidthFullband, frameSize: 480},
		{name: "fb_20ms", bandwidth: BandwidthFullband, frameSize: 960},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder 16k hybrid DRED")
			if want.ret != n {
				t.Fatalf("libopus 16k hybrid decoder DRED decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertFloat32ApproxEqual(t, pcm[:got], want.pcm[:got], "16k hybrid explicit libopus pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "16k hybrid explicit libopus plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid explicit libopus fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "16k hybrid explicit libopus celt")
		})
	}
}

func TestDecoderExplicitDREDDecode16kMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState16k(t)

	want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.ret != n {
		t.Fatalf("libopus 16k decoder DRED decode ret=%d want %d", want.ret, n)
	}
	if want.channels != 1 {
		t.Fatalf("libopus 16k decoder DRED decode channels=%d want 1", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
	}

	pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(480)
	assertFloat32ApproxEqual(t, pcm[:n], want.pcm[:n], "explicit 16k libopus pcm", pcmTol)
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k libopus plc", plcTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k libopus fargan", farganTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.celt48k, "explicit 16k libopus celt", celtTol)
}

func TestDecoderPublicDecodeDRED16kMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		cfg       libopusDREDPacketConfig
		pcmTol    float64
		plcTol    float64
		farganTol float64
		celtTol   float64
	}{
		{
			name: "celt_fb_mono",
			cfg: libopusDREDPacketConfig{
				FrameSize: 480,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthFullband,
			},
		},
		{
			name: "celt_fb_stereo",
			cfg: libopusDREDPacketConfig{
				FrameSize:     480,
				ForceMode:     ModeCELT,
				Bandwidth:     BandwidthFullband,
				Channels:      2,
				ForceChannels: 2,
			},
		},
		{
			name: "hybrid_swb_mono",
			cfg: libopusDREDPacketConfig{
				FrameSize: 960,
				ForceMode: ModeHybrid,
				Bandwidth: BandwidthSuperwideband,
			},
			pcmTol:    1e-4,
			plcTol:    1e-4,
			farganTol: 1e-4,
			celtTol:   1e-4,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, tc.cfg)
			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus public decoder DRED")
			if want.ret != n {
				t.Fatalf("libopus public 16k decoder DRED decode ret=%d want %d", want.ret, n)
			}
			if want.channels != dec.channels {
				t.Fatalf("libopus public 16k decoder DRED channels=%d want %d", want.channels, dec.channels)
			}

			pcm := make([]float32, dec.maxPacketSamples*dec.channels)
			got, err := dec.DecodeDRED(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("DecodeDRED error: %v", err)
			}
			if got != n {
				t.Fatalf("DecodeDRED=%d want %d", got, n)
			}
			if gotDuration := dec.LastPacketDuration(); gotDuration != n {
				t.Fatalf("LastPacketDuration()=%d want API-rate frame %d", gotDuration, n)
			}

			pcmTol, plcTol, farganTol, celtTol := tc.pcmTol, tc.plcTol, tc.farganTol, tc.celtTol
			if pcmTol == 0 {
				pcmTol, plcTol, farganTol, celtTol = decoderDREDLiveSequenceTolerances(tc.cfg.FrameSize)
			}
			gotPCM := pcm[:got*dec.channels]
			wantPCM := want.pcm[:got*dec.channels]
			if dec.channels == 2 {
				assertInterleavedStereoApproxDuplicated(t, gotPCM, got, "public 16k DecodeDRED", 1e-2)
				assertInterleavedStereoApproxDuplicated(t, wantPCM, got, "libopus public 16k DecodeDRED", 1e-2)
			}
			assertFloat32ApproxEqual(t, gotPCM, wantPCM, "public 16k DecodeDRED pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "public 16k DecodeDRED plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "public 16k DecodeDRED fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.celt48k, "public 16k DecodeDRED celt", celtTol)
		})
	}
}

func TestDecoderExplicitDREDCELT48kBridgeMatchesLibopusFirstLoss(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n <= 0 {
		t.Skipf("48 kHz explicit bridge parity requires 48 kHz packet, got sampleRate=%d frame=%d", packetInfo.sampleRate, n)
	}

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder DRED decode ret=%d want %d", want.ret, n)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "explicit first libopus celt")
}

func TestDecoderExplicitDREDDecodeSecondLossMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)

	pcm0 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.warmupRet != n {
		t.Fatalf("libopus decoder DRED warmup ret=%d want %d", want.warmupRet, n)
	}
	if want.ret != n {
		t.Fatalf("libopus decoder DRED second ret=%d want %d", want.ret, n)
	}
	if want.channels != 1 {
		t.Fatalf("libopus decoder DRED second channels=%d want 1", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
	}

	assertFloat32ApproxEqual(t, pcm[:n], want.pcm[:n], "explicit second libopus pcm", 1e-4)
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit second libopus plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit second libopus fargan")
}

func TestDecoderExplicitDREDDecodeSecondLossGainTransitionMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
	const gainA = 256
	const gainB = -512

	if err := dec.SetGain(gainA); err != nil {
		t.Fatalf("SetGain(%d) error: %v", gainA, err)
	}
	wantFirst, err := probeLibopusDecoderDREDDecodeFloatWithGain(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n, gainA)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, wantFirst, "libopus decoder DRED")
	if wantFirst.ret != n {
		t.Fatalf("libopus decoder DRED first ret=%d want %d", wantFirst.ret, n)
	}

	pcm0 := make([]float32, dec.maxPacketSamples)
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat(first)=%d want %d", got, n)
	}

	assertFloat32ApproxEqual(t, pcm0[:n], wantFirst.pcm[:n], "explicit second-loss gain transition first libopus pcm", 1e-4)
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), wantFirst.state, "explicit second-loss gain transition first libopus plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), wantFirst.fargan, "explicit second-loss gain transition first libopus fargan")

	if err := dec.SetGain(gainB); err != nil {
		t.Fatalf("SetGain(%d) error: %v", gainB, err)
	}
	wantSecond, err := probeLibopusDecoderDREDDecodeFloatWithGain(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n, gainB)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, wantSecond, "libopus decoder DRED")
	if wantSecond.warmupRet != n {
		t.Fatalf("libopus decoder DRED warmup ret=%d want %d", wantSecond.warmupRet, n)
	}
	if wantSecond.ret != n {
		t.Fatalf("libopus decoder DRED second ret=%d want %d", wantSecond.ret, n)
	}

	pcm1 := make([]float32, dec.maxPacketSamples)
	got, err = dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
	}

	assertFloat32ApproxEqual(t, pcm1[:n], wantSecond.pcm[:n], "explicit second-loss gain transition second libopus pcm", 1e-4)
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), wantSecond.state, "explicit second-loss gain transition second libopus plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), wantSecond.fargan, "explicit second-loss gain transition second libopus fargan")
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, wantSecond.celt48k, "explicit second-loss gain transition second libopus celt")
}

func TestDecoderExplicitDREDDecodeSecondLoss16kMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState16k(t)

	pcm0 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, n, 2*n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.warmupRet != n {
		t.Fatalf("libopus 16k decoder DRED warmup ret=%d want %d", want.warmupRet, n)
	}
	if want.ret != n {
		t.Fatalf("libopus 16k decoder DRED second ret=%d want %d", want.ret, n)
	}
	if want.channels != 1 {
		t.Fatalf("libopus 16k decoder DRED second channels=%d want 1", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
	}

	pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(480)
	assertFloat32ApproxEqual(t, pcm[:n], want.pcm[:n], "explicit 16k second libopus pcm", pcmTol)
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k second libopus plc", plcTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k second libopus fargan", farganTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.celt48k, "explicit 16k second libopus celt", celtTol)
}

func TestDecoderExplicitDREDCELT48kBridgeMatchesLibopusSecondLoss(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n <= 0 {
		t.Skipf("48 kHz explicit bridge parity requires 48 kHz packet, got sampleRate=%d frame=%d", packetInfo.sampleRate, n)
	}
	pcm0 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED second")
	if want.ret != n {
		t.Fatalf("libopus decoder DRED second decode ret=%d want %d", want.ret, n)
	}

	pcm1 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
	}
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "explicit second libopus celt")
}

func TestDecoderExplicitDREDDecodeThenNextPacketMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n <= 0 {
		t.Skipf("48 kHz explicit follow-up parity requires 48 kHz packet, got sampleRate=%d frame=%d", packetInfo.sampleRate, n)
	}
	nextPacket := makeValidMonoCELTPacketForDREDTest(t)

	lossPCM := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder DRED decode ret=%d want %d", want.ret, n)
	}
	if want.nextRet <= 0 {
		t.Fatalf("libopus decoder follow-up ret=%d want >0", want.nextRet)
	}

	nextPCM := make([]float32, dec.maxPacketSamples)
	gotNext, err := dec.Decode(nextPacket, nextPCM)
	if err != nil {
		t.Fatalf("Decode(next packet) error: %v", err)
	}
	if gotNext != want.nextRet {
		t.Fatalf("Decode(next packet)=%d want %d", gotNext, want.nextRet)
	}

	assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.nextPCM[:gotNext], "explicit next packet pcm", 1e-4)
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit next packet plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit next packet fargan")
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "explicit next packet celt")
}

func TestDecoderExplicitDREDDecodeThenNextPacket16kMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState16k(t)
	nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, 480)

	lossPCM := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, 16000, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.ret != n {
		t.Fatalf("libopus 16k decoder DRED decode ret=%d want %d", want.ret, n)
	}
	if want.nextRet <= 0 {
		t.Fatalf("libopus 16k decoder follow-up ret=%d want >0", want.nextRet)
	}

	nextPCM := make([]float32, dec.maxPacketSamples)
	gotNext, err := dec.Decode(nextPacket, nextPCM)
	if err != nil {
		t.Fatalf("Decode(next packet) error: %v", err)
	}
	if gotNext != want.nextRet {
		t.Fatalf("Decode(next packet)=%d want %d", gotNext, want.nextRet)
	}

	pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(480)
	assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.nextPCM[:gotNext], "explicit 16k next packet pcm", pcmTol)
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k next packet plc", plcTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k next packet fargan", farganTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.celt48k, "explicit 16k next packet celt", celtTol)
}

func TestDecoderExplicitDREDDecode16kFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState16kForFrameSize(t, frameSize)

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
			if want.ret != n {
				t.Fatalf("libopus 16k frame-size decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertFloat32ApproxEqual(t, pcm[:n], want.pcm[:n], "explicit 16k frame-size libopus pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k frame-size libopus plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k frame-size libopus fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.celt48k, "explicit 16k frame-size libopus celt", celtTol)
		})
	}
}

func TestDecoderExplicitDREDDecodeThenNextPacket16kFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState16kForFrameSize(t, frameSize)
			nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, frameSize)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
			if want.ret != n {
				t.Fatalf("libopus 16k follow-up frame-size decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus 16k follow-up frame-size next ret=%d want >0", want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next packet) error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next packet)=%d want %d", gotNext, want.nextRet)
			}

			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.nextPCM[:gotNext], "explicit 16k follow-up frame-size pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k follow-up frame-size plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k follow-up frame-size fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.celt48k, "explicit 16k follow-up frame-size celt", celtTol)
		})
	}
}

func TestDecoderExplicitDREDDecode16kCELTSuperwidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT SWB DRED")
			if want.ret != n {
				t.Fatalf("libopus 16k CELT SWB frame-size decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertFloat32ApproxEqual(t, pcm[:n], want.pcm[:n], "explicit 16k celt swb frame-size libopus pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k celt swb frame-size libopus plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k celt swb frame-size libopus fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.celt48k, "explicit 16k celt swb frame-size libopus celt", celtTol)
		})
	}
}

func TestDecoderExplicitDREDDecodeThenNextPacket16kCELTSuperwidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthSuperwideband)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT SWB DRED")
			if want.ret != n {
				t.Fatalf("libopus 16k CELT SWB follow-up frame-size decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus 16k CELT SWB follow-up frame-size next ret=%d want >0", want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT SWB packet) error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next CELT SWB packet)=%d want %d", gotNext, want.nextRet)
			}

			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.nextPCM[:gotNext], "explicit 16k celt swb follow-up frame-size pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k celt swb follow-up frame-size plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k celt swb follow-up frame-size fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.celt48k, "explicit 16k celt swb follow-up frame-size celt", celtTol)
		})
	}
}

func TestDecoderExplicitDREDDecode16kCELTWidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT WB DRED")
			if want.ret != n {
				t.Fatalf("libopus 16k CELT WB frame-size decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertFloat32ApproxEqual(t, pcm[:n], want.pcm[:n], "explicit 16k celt wb frame-size libopus pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k celt wb frame-size libopus plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k celt wb frame-size libopus fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.celt48k, "explicit 16k celt wb frame-size libopus celt", celtTol)
		})
	}
}

func TestDecoderExplicitDREDDecodeThenNextPacket16kCELTWidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT WB DRED")
			if want.ret != n {
				t.Fatalf("libopus 16k CELT WB follow-up frame-size decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus 16k CELT WB follow-up frame-size next ret=%d want >0", want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT WB packet) error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next CELT WB packet)=%d want %d", gotNext, want.nextRet)
			}

			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.nextPCM[:gotNext], "explicit 16k celt wb follow-up frame-size pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k celt wb follow-up frame-size plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k celt wb follow-up frame-size fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.celt48k, "explicit 16k celt wb follow-up frame-size celt", celtTol)
		})
	}
}

func TestDecoderExplicitSecondLossThenNextPacket16kMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState16k(t)
	nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, 480)

	pcm0 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}
	pcm1 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, 16000, n, 2*n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.warmupRet != n {
		t.Fatalf("libopus 16k decoder warmup ret=%d want %d", want.warmupRet, n)
	}
	if want.ret != n {
		t.Fatalf("libopus 16k decoder second ret=%d want %d", want.ret, n)
	}
	if want.nextRet <= 0 {
		t.Fatalf("libopus 16k decoder follow-up ret=%d want >0", want.nextRet)
	}

	nextPCM := make([]float32, dec.maxPacketSamples)
	gotNext, err := dec.Decode(nextPacket, nextPCM)
	if err != nil {
		t.Fatalf("Decode(next packet) error: %v", err)
	}
	if gotNext != want.nextRet {
		t.Fatalf("Decode(next packet)=%d want %d", gotNext, want.nextRet)
	}

	pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(480)
	assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.nextPCM[:gotNext], "explicit 16k second-loss next packet pcm", pcmTol)
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k second-loss next packet plc", plcTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k second-loss next packet fargan", farganTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.celt48k, "explicit 16k second-loss next packet celt", celtTol)
}

func TestDecoderExplicitSecondLossThenNextPacketMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n <= 0 {
		t.Skipf("48 kHz explicit second-loss follow-up parity requires 48 kHz packet, got sampleRate=%d frame=%d", packetInfo.sampleRate, n)
	}
	nextPacket := makeValidMonoCELTPacketForDREDTest(t)

	pcm0 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}
	pcm1 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.warmupRet != n {
		t.Fatalf("libopus decoder DRED warmup ret=%d want %d", want.warmupRet, n)
	}
	if want.ret != n {
		t.Fatalf("libopus decoder DRED second ret=%d want %d", want.ret, n)
	}
	if want.nextRet <= 0 {
		t.Fatalf("libopus decoder second-loss follow-up ret=%d want >0", want.nextRet)
	}

	nextPCM := make([]float32, dec.maxPacketSamples)
	gotNext, err := dec.Decode(nextPacket, nextPCM)
	if err != nil {
		t.Fatalf("Decode(next packet) after second loss error: %v", err)
	}
	if gotNext != want.nextRet {
		t.Fatalf("Decode(next packet) after second loss=%d want %d", gotNext, want.nextRet)
	}

	assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.nextPCM[:gotNext], "explicit second-loss next packet pcm", 1e-4)
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit second-loss next packet plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit second-loss next packet fargan")
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "explicit second-loss next packet celt")
}

func TestDecoderExplicitDREDDecodeOffsetMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	_, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
	boundary := -dred.Parsed().Header.OffsetSamples(packetInfo.sampleRate)

	tests := []struct {
		name       string
		dredOffset int
	}{
		{name: "before_first_feature_boundary", dredOffset: boundary - 1},
		{name: "at_first_feature_boundary", dredOffset: boundary},
		{name: "end_of_first_feature_frame", dredOffset: boundary + n - 1},
		{name: "at_second_feature_boundary", dredOffset: boundary + n},
		{name: "late_offset", dredOffset: boundary + 2*n},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, tc.dredOffset, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder DRED decode ret=%d want %d", want.ret, n)
			}

			localDec, err := NewDecoder(DefaultDecoderConfig(packetInfo.sampleRate, 1))
			if err != nil {
				t.Fatalf("NewDecoder error: %v", err)
			}
			setDecoderComplexityForLibopusDREDParityTest(t, localDec)
			if err := localDec.SetDNNBlob(decoderBlob); err != nil {
				t.Fatalf("SetDNNBlob error: %v", err)
			}
			seedPCM := make([]float32, localDec.maxPacketSamples)
			if _, err := localDec.Decode(seedPacket, seedPCM); err != nil {
				t.Fatalf("Decode(seed packet) error: %v", err)
			}
			localDRED := NewDRED()
			*localDRED = *dred
			pcm := make([]float32, localDec.maxPacketSamples)
			got, err := localDec.decodeExplicitDREDFloat(localDRED, tc.dredOffset, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			pcmTol, plcTol, farganTol := 1e-4, 1e-4, 1e-4
			if tc.dredOffset == boundary {
				// The exact first-feature boundary lands on a FARGAN frame edge;
				// keep the branch pinned while allowing the same tiny DNN drift
				// already covered by the internal libopus neural parity tests.
				pcmTol, plcTol, farganTol = 1.5e-4, 1e-2, 5e-2
			}
			assertFloat32ApproxEqual(t, pcm[:n], want.pcm[:n], "offset matrix pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, localDec).dredPLC.Snapshot(), want.state, "offset matrix plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, localDec).dredFARGAN.Snapshot(), want.fargan, "offset matrix fargan", farganTol)
		})
	}
}

func TestDecoderExplicitDREDDecodeOffsetMatrixCELTSuperwidebandMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	_, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthSuperwideband,
	})
	boundary := -dred.Parsed().Header.OffsetSamples(packetInfo.sampleRate)

	tests := []struct {
		name       string
		dredOffset int
	}{
		{name: "before_first_feature_boundary", dredOffset: boundary - 1},
		{name: "at_first_feature_boundary", dredOffset: boundary},
		{name: "end_of_first_feature_frame", dredOffset: boundary + n - 1},
		{name: "at_second_feature_boundary", dredOffset: boundary + n},
		{name: "late_offset", dredOffset: boundary + 2*n},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, tc.dredOffset, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT SWB DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder CELT SWB DRED decode ret=%d want %d", want.ret, n)
			}

			localDec, err := NewDecoder(DefaultDecoderConfig(packetInfo.sampleRate, 1))
			if err != nil {
				t.Fatalf("NewDecoder error: %v", err)
			}
			setDecoderComplexityForLibopusDREDParityTest(t, localDec)
			if err := localDec.SetDNNBlob(decoderBlob); err != nil {
				t.Fatalf("SetDNNBlob error: %v", err)
			}
			seedPCM := make([]float32, localDec.maxPacketSamples)
			if _, err := localDec.Decode(seedPacket, seedPCM); err != nil {
				t.Fatalf("Decode(seed packet) error: %v", err)
			}
			localDRED := NewDRED()
			*localDRED = *dred
			pcm := make([]float32, localDec.maxPacketSamples)
			got, err := localDec.decodeExplicitDREDFloat(localDRED, tc.dredOffset, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			pcmTol, plcTol, farganTol := 1e-4, 1e-4, 1e-4
			if tc.dredOffset == boundary {
				// The exact first-feature boundary lands on a FARGAN frame edge;
				// keep the branch pinned while allowing the same tiny DNN drift
				// already covered by the internal libopus neural parity tests.
				pcmTol, plcTol, farganTol = 1.5e-4, 1e-2, 5e-2
			}
			assertFloat32ApproxEqual(t, pcm[:n], want.pcm[:n], "celt swb offset matrix pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, localDec).dredPLC.Snapshot(), want.state, "celt swb offset matrix plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, localDec).dredFARGAN.Snapshot(), want.fargan, "celt swb offset matrix fargan", farganTol)
		})
	}
}

func TestDecoderExplicitDREDDecodeOffsetMatrixHybridSuperwidebandMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	_, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthSuperwideband,
	})
	boundary := -dred.Parsed().Header.OffsetSamples(packetInfo.sampleRate)

	tests := []struct {
		name       string
		dredOffset int
	}{
		{name: "before_first_feature_boundary", dredOffset: boundary - 1},
		{name: "at_first_feature_boundary", dredOffset: boundary},
		{name: "end_of_first_feature_frame", dredOffset: boundary + n - 1},
		{name: "at_second_feature_boundary", dredOffset: boundary + n},
		{name: "late_offset", dredOffset: boundary + 2*n},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, tc.dredOffset, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder hybrid SWB DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder hybrid SWB DRED decode ret=%d want %d", want.ret, n)
			}

			localDec, err := NewDecoder(DefaultDecoderConfig(packetInfo.sampleRate, 1))
			if err != nil {
				t.Fatalf("NewDecoder error: %v", err)
			}
			setDecoderComplexityForLibopusDREDParityTest(t, localDec)
			if err := localDec.SetDNNBlob(decoderBlob); err != nil {
				t.Fatalf("SetDNNBlob error: %v", err)
			}
			seedPCM := make([]float32, localDec.maxPacketSamples)
			if _, err := localDec.Decode(seedPacket, seedPCM); err != nil {
				t.Fatalf("Decode(seed packet) error: %v", err)
			}
			localDRED := NewDRED()
			*localDRED = *dred
			pcm := make([]float32, localDec.maxPacketSamples)
			got, err := localDec.decodeExplicitDREDFloat(localDRED, tc.dredOffset, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			pcmTol, plcTol, farganTol := 1e-4, 1e-4, 1e-4
			if tc.dredOffset == boundary {
				// The exact first-feature boundary lands on a FARGAN frame edge;
				// keep the branch pinned while allowing the same tiny DNN drift
				// already covered by the internal libopus neural parity tests.
				pcmTol, plcTol, farganTol = 1.5e-4, 1e-2, 5e-2
			}
			assertFloat32ApproxEqual(t, pcm[:n], want.pcm[:n], "hybrid swb offset matrix pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, localDec).dredPLC.Snapshot(), want.state, "hybrid swb offset matrix plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, localDec).dredFARGAN.Snapshot(), want.fargan, "hybrid swb offset matrix fargan", farganTol)
		})
	}
}

func TestDecoderExplicitDREDDecodeOffsetMatrixHybridFullbandMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	_, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
	})
	boundary := -dred.Parsed().Header.OffsetSamples(packetInfo.sampleRate)

	tests := []struct {
		name       string
		dredOffset int
	}{
		{name: "before_first_feature_boundary", dredOffset: boundary - 1},
		{name: "at_first_feature_boundary", dredOffset: boundary},
		{name: "end_of_first_feature_frame", dredOffset: boundary + n - 1},
		{name: "at_second_feature_boundary", dredOffset: boundary + n},
		{name: "late_offset", dredOffset: boundary + 2*n},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, tc.dredOffset, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder hybrid FB DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder hybrid FB DRED decode ret=%d want %d", want.ret, n)
			}

			localDec, err := NewDecoder(DefaultDecoderConfig(packetInfo.sampleRate, 1))
			if err != nil {
				t.Fatalf("NewDecoder error: %v", err)
			}
			setDecoderComplexityForLibopusDREDParityTest(t, localDec)
			if err := localDec.SetDNNBlob(decoderBlob); err != nil {
				t.Fatalf("SetDNNBlob error: %v", err)
			}
			seedPCM := make([]float32, localDec.maxPacketSamples)
			if _, err := localDec.Decode(seedPacket, seedPCM); err != nil {
				t.Fatalf("Decode(seed packet) error: %v", err)
			}
			localDRED := NewDRED()
			*localDRED = *dred
			pcm := make([]float32, localDec.maxPacketSamples)
			got, err := localDec.decodeExplicitDREDFloat(localDRED, tc.dredOffset, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			pcmTol, plcTol, farganTol := 1e-4, 1e-4, 1e-4
			if tc.dredOffset == boundary {
				// The exact first-feature boundary lands on a FARGAN frame edge;
				// keep the branch pinned while allowing the same tiny DNN drift
				// already covered by the internal libopus neural parity tests.
				pcmTol, plcTol, farganTol = 1.5e-4, 1e-2, 5e-2
			}
			assertFloat32ApproxEqual(t, pcm[:n], want.pcm[:n], "hybrid fb offset matrix pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, localDec).dredPLC.Snapshot(), want.state, "hybrid fb offset matrix plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, localDec).dredFARGAN.Snapshot(), want.fargan, "hybrid fb offset matrix fargan", farganTol)
		})
	}
}

func TestDecoderExplicitDREDDecodeOffsetMatrix16kHybridMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
	}{
		{name: "swb", bandwidth: BandwidthSuperwideband},
		{name: "fb", bandwidth: BandwidthFullband},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, dred, packetInfo, _, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: 960,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, 16000)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("16 kHz hybrid offset frame=%d want %d", n, wantFrame)
			}
			boundary := -dred.Parsed().Header.OffsetSamples(16000)

			offsets := []struct {
				name       string
				dredOffset int
			}{
				{name: "before_first_feature_boundary", dredOffset: boundary - 1},
				{name: "at_first_feature_boundary", dredOffset: boundary},
				{name: "end_of_first_feature_frame", dredOffset: boundary + n - 1},
				{name: "at_second_feature_boundary", dredOffset: boundary + n},
				{name: "late_offset", dredOffset: boundary + 2*n},
			}

			for _, offset := range offsets {
				offset := offset
				t.Run(offset.name, func(t *testing.T) {
					localDec, localDRED, localPacketInfo, seedPacket, localN := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
						FrameSize: 960,
						ForceMode: ModeHybrid,
						Bandwidth: tc.bandwidth,
					})
					if localPacketInfo.sampleRate != packetInfo.sampleRate || localN != n {
						t.Fatalf("local 16 kHz hybrid packet changed: sampleRate=%d frame=%d want sampleRate=%d frame=%d", localPacketInfo.sampleRate, localN, packetInfo.sampleRate, n)
					}
					want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, localPacketInfo, 16000, -1, offset.dredOffset, n)
					if err != nil {
						libopustest.HelperUnavailable(t, "decoder DRED decode", err)
					}
					requireLibopusDREDDecodeParsed(t, want, "libopus decoder 16k hybrid DRED")
					if want.ret != n {
						t.Fatalf("libopus decoder 16k hybrid DRED decode ret=%d want %d", want.ret, n)
					}

					pcm := make([]float32, localDec.maxPacketSamples)
					got, err := localDec.decodeExplicitDREDFloat(localDRED, offset.dredOffset, pcm, n)
					if err != nil {
						t.Fatalf("decodeExplicitDREDFloat error: %v", err)
					}
					if got != n {
						t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
					}

					pcmTol, plcTol, farganTol := 1e-4, 1e-4, 1e-4
					if offset.dredOffset == boundary {
						pcmTol, plcTol, farganTol = 1.5e-4, 1e-2, 5e-2
					}
					assertFloat32ApproxEqual(t, pcm[:n], want.pcm[:n], "16k hybrid offset matrix pcm", pcmTol)
					assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, localDec).dredPLC.Snapshot(), want.state, "16k hybrid offset matrix plc", plcTol)
					assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, localDec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid offset matrix fargan", farganTol)
				})
			}
		})
	}
}

func TestDecoderExplicitDREDDecodeFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForFrameSize(t, frameSize)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz explicit frame-size parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder DRED decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertFloat32ApproxEqual(t, pcm[:got], want.pcm[:got], "frame size matrix pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "frame size matrix plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "frame size matrix fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "frame size matrix celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeCELTSuperwidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz CELT SWB frame-size parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT SWB DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder CELT SWB DRED decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertFloat32ApproxEqual(t, pcm[:got], want.pcm[:got], "celt swb frame size matrix pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "celt swb frame size matrix plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "celt swb frame size matrix fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "celt swb frame size matrix celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeCELTWidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz CELT WB frame-size parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT WB DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder CELT WB DRED decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertFloat32ApproxEqual(t, pcm[:got], want.pcm[:got], "celt wb frame size matrix pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "celt wb frame size matrix plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "celt wb frame size matrix fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "celt wb frame size matrix celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeSecondLossFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForFrameSize(t, frameSize)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz explicit second-loss frame-size parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus decoder DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus decoder DRED second ret=%d want %d", want.ret, n)
			}

			pcm1 := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
			}

			assertFloat32ApproxEqual(t, pcm1[:got], want.pcm[:got], "second loss frame size matrix pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "second loss frame size matrix plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "second loss frame size matrix fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "second loss frame size matrix celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeSecondLossCELTSuperwidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz CELT SWB second-loss parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT SWB DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus decoder CELT SWB DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus decoder CELT SWB DRED second ret=%d want %d", want.ret, n)
			}

			pcm1 := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
			}

			assertFloat32ApproxEqual(t, pcm1[:got], want.pcm[:got], "celt swb second loss frame size matrix pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "celt swb second loss frame size matrix plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "celt swb second loss frame size matrix fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "celt swb second loss frame size matrix celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeThenNextPacketFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForFrameSize(t, frameSize)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz explicit follow-up frame-size parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, frameSize)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder DRED decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet != n {
				t.Fatalf("libopus decoder follow-up ret=%d want %d", want.nextRet, n)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next packet) error: %v", err)
			}
			if gotNext != n {
				t.Fatalf("Decode(next packet)=%d want %d", gotNext, n)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.nextPCM[:gotNext], "follow-up frame size matrix pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "follow-up frame size matrix plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "follow-up frame size matrix fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "follow-up frame size matrix celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeThenNextPacketCELTSuperwidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz CELT SWB follow-up parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthSuperwideband)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT SWB DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder CELT SWB DRED decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet != n {
				t.Fatalf("libopus decoder CELT SWB follow-up ret=%d want %d", want.nextRet, n)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT SWB packet) error: %v", err)
			}
			if gotNext != n {
				t.Fatalf("Decode(next CELT SWB packet)=%d want %d", gotNext, n)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.nextPCM[:gotNext], "celt swb follow-up pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "celt swb follow-up plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "celt swb follow-up fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "celt swb follow-up celt")
		})
	}
}

func TestDecoderExplicitSecondLossThenNextPacketFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForFrameSize(t, frameSize)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz explicit second-loss follow-up frame-size parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, frameSize)

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}
			pcm1 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus decoder DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus decoder DRED second ret=%d want %d", want.ret, n)
			}
			if want.nextRet != n {
				t.Fatalf("libopus decoder second-loss follow-up ret=%d want %d", want.nextRet, n)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next packet) after second loss error: %v", err)
			}
			if gotNext != n {
				t.Fatalf("Decode(next packet) after second loss=%d want %d", gotNext, n)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.nextPCM[:gotNext], "second-loss follow-up frame size matrix pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "second-loss follow-up frame size matrix plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "second-loss follow-up frame size matrix fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "second-loss follow-up frame size matrix celt")
		})
	}
}

func TestDecoderExplicitSecondLossThenNextPacketCELTSuperwidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz CELT SWB second-loss follow-up parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthSuperwideband)

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}
			pcm1 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT SWB DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus decoder CELT SWB DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus decoder CELT SWB DRED second ret=%d want %d", want.ret, n)
			}
			if want.nextRet != n {
				t.Fatalf("libopus decoder CELT SWB second-loss follow-up ret=%d want %d", want.nextRet, n)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT SWB packet) after second loss error: %v", err)
			}
			if gotNext != n {
				t.Fatalf("Decode(next CELT SWB packet) after second loss=%d want %d", gotNext, n)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.nextPCM[:gotNext], "celt swb second-loss follow-up pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "celt swb second-loss follow-up plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "celt swb second-loss follow-up fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "celt swb second-loss follow-up celt")
		})
	}
}

func TestDecoderCachedDREDDecodeCELTWidebandMatrixMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertDecoderCachedDREDFirstLossMatchesLiveSequenceOracleWithTolerances(t, "cached CELT WB", packetInfo, pcmTol, plcTol, farganTol, celtTol)
		})
	}
}

func TestDecoderCachedDREDThenNextPacketCELTWidebandMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)

			dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("cached CELT WB live-sequence parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 0, 0, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, fmt.Sprintf("cached CELT WB first-loss next-packet frame_size_%d", frameSize))
			if want.step0.ret != n {
				t.Fatalf("libopus cached CELT WB decoder first-loss ret=%d want %d", want.step0.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus cached CELT WB decoder follow-up ret=%d want >0", want.next.ret)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil)=%d want %d", got, n)
			}
			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertFloat32ApproxEqual(t, pcm[:got], want.step0.pcm[:got], "cached CELT WB live-sequence first-loss pcm", pcmTol)

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT WB packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next CELT WB packet)=%d want %d", gotNext, want.next.ret)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.next.pcm[:gotNext], "cached CELT WB next packet live-sequence pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "cached CELT WB next packet live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "cached CELT WB next packet live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.next.celt48k, "cached CELT WB next packet live-sequence celt", celtTol)
		})
	}
}

func TestDecoderCachedDREDSecondLossCELTWidebandMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertDecoderCachedDREDSecondLossMatchesLiveSequenceOracleWithTolerances(t, "cached CELT WB", packetInfo, pcmTol, plcTol, farganTol, celtTol)
		})
	}
}

func TestDecoderCachedDREDSecondLossThenNextPacketCELTWidebandMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)

			dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("cached CELT WB second-loss live-sequence parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 1, 2*n, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, fmt.Sprintf("cached CELT WB second-loss next-packet frame_size_%d", frameSize))
			if want.step0.ret != n {
				t.Fatalf("libopus cached CELT WB decoder first warmup ret=%d want %d", want.step0.ret, n)
			}
			if want.step1.ret != n {
				t.Fatalf("libopus cached CELT WB decoder second-loss ret=%d want %d", want.step1.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus cached CELT WB decoder follow-up ret=%d want >0", want.next.ret)
			}

			pcm0 := make([]float32, dec.maxPacketSamples)
			got, err := dec.Decode(nil, pcm0)
			if err != nil {
				t.Fatalf("Decode(nil, first) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil, first)=%d want %d", got, n)
			}
			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertFloat32ApproxEqual(t, pcm0[:got], want.step0.pcm[:got], "cached CELT WB live-sequence warmup pcm", pcmTol)

			pcm1 := make([]float32, dec.maxPacketSamples)
			got, err = dec.Decode(nil, pcm1)
			if err != nil {
				t.Fatalf("Decode(nil, second) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil, second)=%d want %d", got, n)
			}
			assertFloat32ApproxEqual(t, pcm1[:got], want.step1.pcm[:got], "cached CELT WB live-sequence second-loss pcm", pcmTol)

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT WB packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next CELT WB packet)=%d want %d", gotNext, want.next.ret)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.next.pcm[:gotNext], "cached CELT WB second-loss next packet live-sequence pcm", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "cached CELT WB second-loss next packet live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "cached CELT WB second-loss next packet live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.next.celt48k, "cached CELT WB second-loss next packet live-sequence celt", celtTol)
		})
	}
}

func TestDecoderExplicitDREDDecodeSecondLossCELTWidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz CELT WB second-loss parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT WB DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus decoder CELT WB DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus decoder CELT WB DRED second ret=%d want %d", want.ret, n)
			}

			pcm1 := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
			}

			assertFloat32ApproxEqual(t, pcm1[:got], want.pcm[:got], "celt wb second loss frame size matrix pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "celt wb second loss frame size matrix plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "celt wb second loss frame size matrix fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "celt wb second loss frame size matrix celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeThenNextPacketCELTWidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz CELT WB follow-up parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT WB DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder CELT WB DRED decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet != n {
				t.Fatalf("libopus decoder CELT WB follow-up ret=%d want %d", want.nextRet, n)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT WB packet) error: %v", err)
			}
			if gotNext != n {
				t.Fatalf("Decode(next CELT WB packet)=%d want %d", gotNext, n)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.nextPCM[:gotNext], "celt wb follow-up pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "celt wb follow-up plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "celt wb follow-up fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "celt wb follow-up celt")
		})
	}
}

func TestDecoderExplicitSecondLossThenNextPacketCELTWidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz CELT WB second-loss follow-up parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}
			pcm1 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT WB DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus decoder CELT WB DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus decoder CELT WB DRED second ret=%d want %d", want.ret, n)
			}
			if want.nextRet != n {
				t.Fatalf("libopus decoder CELT WB second-loss follow-up ret=%d want %d", want.nextRet, n)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT WB packet) after second loss error: %v", err)
			}
			if gotNext != n {
				t.Fatalf("Decode(next CELT WB packet) after second loss=%d want %d", gotNext, n)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.nextPCM[:gotNext], "celt wb second-loss follow-up pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "celt wb second-loss follow-up plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "celt wb second-loss follow-up fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "celt wb second-loss follow-up celt")
		})
	}
}

// TestDecoderExplicitSILKDREDDecodeMatchesLibopus exercises the SILK-only
// explicit DRED decode path against libopus. libopus routes SILK-only DRED
// through silk_Decode(lost_flag=1) with FEC features queued in lpcnet, where
// the SILK DeepPLC hook produces 16 kHz neural concealment and SILK upsamples
// to the API rate. The gopus equivalent (decodeExplicitSILKDREDFloat) installs
// the same DeepPLC hook around a standard PLC chunk decode after priming the
// LPCNet/FARGAN entry history from the prior SILK native lowband.
func TestDecoderExplicitSILKDREDDecodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(
		t, 48000, libopusDREDPacketConfig{
			FrameSize: 960,
			ForceMode: ModeSILK,
			Bandwidth: BandwidthWideband,
		})

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder SILK DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder SILK DRED decode ret=%d want %d", want.ret, n)
	}
	if want.channels != 1 {
		t.Fatalf("libopus decoder SILK DRED decode channels=%d want 1", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
	}

	assertFloat32ApproxEqual(t, pcm[:n], want.pcm[:n], "explicit silk libopus pcm", 1e-4)
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit silk libopus plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit silk libopus fargan")
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "explicit silk libopus celt")
	assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.silk, silkpkg.BandwidthWideband, "explicit silk libopus silk", 1e-4)
}

// TestDecoderExplicit16kSILKDREDDecodeMatchesLibopus mirrors the 48 kHz SILK
// explicit DRED parity test at a 16 kHz decoder rate. SILK runs internally at
// 16 kHz so the 16 kHz API path skips the SILK->API upsampler entirely; the
// DeepPLC neural lowband is emitted at 16 kHz directly to the caller buffer.
// libopus's opus_decoder_dred_decode_float supports this path at any internal
// SR including 16 kHz.
func TestDecoderExplicit16kSILKDREDDecodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(
		t, 16000, libopusDREDPacketConfig{
			FrameSize: 960,
			ForceMode: ModeSILK,
			Bandwidth: BandwidthWideband,
		})

	want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder 16k SILK DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder 16k SILK DRED decode ret=%d want %d", want.ret, n)
	}
	if want.channels != 1 {
		t.Fatalf("libopus decoder 16k SILK DRED decode channels=%d want 1", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
	}

	assertFloat32ApproxEqual(t, pcm[:n], want.pcm[:n], "explicit 16k silk libopus pcm", 1e-4)
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k silk libopus plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k silk libopus fargan")
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "explicit 16k silk libopus celt")
	assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.silk, silkpkg.BandwidthWideband, "explicit 16k silk libopus silk", 1e-4)
}

// TestDecoderExplicitSILKDREDDecodeStereoMatchesLibopus mirrors
// TestDecoderExplicitSILKDREDDecodeMatchesLibopus for the stereo SILK DRED
// runtime path. libopus routes DRED through one lpcnet state on SILK channel 0
// and exposes duplicated L=R PCM on the API side for this fixture.
func TestDecoderExplicitSILKDREDDecodeStereoMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	probeInfo, probeErr := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     960,
		ForceMode:     ModeSILK,
		Bandwidth:     BandwidthWideband,
		Channels:      2,
		ForceChannels: 2,
	})
	if probeErr != nil {
		libopustest.HelperUnavailable(t, "dred packet", probeErr)
	}
	if !ParseTOC(probeInfo.packet[0]).Stereo {
		t.Fatalf("forced stereo SILK DRED packet produced mono TOC at 960-sample WB (toc=0x%02x)", probeInfo.packet[0])
	}

	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(
		t, 48000, libopusDREDPacketConfig{
			FrameSize:     960,
			ForceMode:     ModeSILK,
			Bandwidth:     BandwidthWideband,
			Channels:      2,
			ForceChannels: 2,
		})
	if dec.channels != 2 {
		t.Fatalf("stereo explicit SILK DRED parity got decoder channels=%d, want 2", dec.channels)
	}

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder stereo SILK DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder stereo SILK DRED decode ret=%d want %d", want.ret, n)
	}
	if want.channels != 2 {
		t.Fatalf("libopus decoder stereo SILK DRED decode channels=%d want 2", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples*dec.channels)
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
	}

	for i := 0; i < n; i++ {
		if d := math.Abs(float64(pcm[2*i] - pcm[2*i+1])); d != 0 {
			t.Fatalf("gopus stereo SILK DRED PCM not L=R duplicated at sample %d: |L-R|=%g", i, d)
		}
		if d := math.Abs(float64(want.pcm[2*i] - want.pcm[2*i+1])); d != 0 {
			t.Fatalf("libopus stereo SILK DRED PCM not L=R duplicated at sample %d: |L-R|=%g", i, d)
		}
	}

	const stereoDREDStateTol = 1e-4
	const stereoDREDPCMTol = 1e-4
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit stereo SILK libopus plc", stereoDREDStateTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit stereo SILK libopus fargan", stereoDREDStateTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.celt48k, "explicit stereo SILK libopus celt", stereoDREDStateTol)
	assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.silk, silkpkg.BandwidthWideband, "explicit stereo SILK libopus silk", stereoDREDStateTol)
	assertFloat32ApproxEqual(t, pcm[:n*dec.channels], want.pcm[:n*dec.channels], "explicit stereo SILK libopus pcm", stereoDREDPCMTol)
}
