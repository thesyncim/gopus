//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os/exec"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
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

var (
	libopusDecoderDREDDecodeFloatHelperOnce sync.Once
	libopusDecoderDREDDecodeFloatHelperPath string
	libopusDecoderDREDDecodeFloatHelperErr  error
)

func getLibopusDecoderDREDDecodeFloatHelperPath() (string, error) {
	libopusDecoderDREDDecodeFloatHelperOnce.Do(func() {
		libopusDecoderDREDDecodeFloatHelperPath, libopusDecoderDREDDecodeFloatHelperErr = buildLibopusDREDHelper("libopus_decoder_dred_decode_float_info.c", "gopus_libopus_decoder_dred_decode_float", true)
	})
	if libopusDecoderDREDDecodeFloatHelperErr != nil {
		return "", libopusDecoderDREDDecodeFloatHelperErr
	}
	return libopusDecoderDREDDecodeFloatHelperPath, nil
}

func probeLibopusDecoderDREDDecodeFloat(seedPacket, packet []byte, maxDREDSamples, sampleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples int) (libopusDecoderDREDDecodeFloatInfo, error) {
	return probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packet, nil, maxDREDSamples, sampleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples)
}

func probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packet, nextPacket []byte, maxDREDSamples, sampleRate, warmupDREDOffsetSamples, dredOffsetSamples, frameSizeSamples int) (libopusDecoderDREDDecodeFloatInfo, error) {
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

	var payload bytes.Buffer
	payload.WriteString(libopusDecoderDREDDecodeFloatInputMagic)
	for _, v := range []uint32{
		6,
		uint32(sampleRate),
		uint32(maxDREDSamples),
		uint32(warmupDREDOffsetSamples),
		uint32(dredOffsetSamples),
		uint32(frameSizeSamples),
		uint32(len(seedPacket)),
		uint32(len(packet)),
		uint32(len(nextPacket)),
		uint32(len(decoderModelBlob)),
		uint32(len(dredModelBlob)),
	} {
		if err := binary.Write(&payload, binary.LittleEndian, v); err != nil {
			return libopusDecoderDREDDecodeFloatInfo{}, fmt.Errorf("encode decoder dred decode helper header: %w", err)
		}
	}
	if _, err := payload.Write(seedPacket); err != nil {
		return libopusDecoderDREDDecodeFloatInfo{}, fmt.Errorf("encode decoder dred decode helper seed packet: %w", err)
	}
	if _, err := payload.Write(packet); err != nil {
		return libopusDecoderDREDDecodeFloatInfo{}, fmt.Errorf("encode decoder dred decode helper packet: %w", err)
	}
	if _, err := payload.Write(nextPacket); err != nil {
		return libopusDecoderDREDDecodeFloatInfo{}, fmt.Errorf("encode decoder dred decode helper next packet: %w", err)
	}
	if _, err := payload.Write(decoderModelBlob); err != nil {
		return libopusDecoderDREDDecodeFloatInfo{}, fmt.Errorf("encode decoder dred decode helper decoder model blob: %w", err)
	}
	if _, err := payload.Write(dredModelBlob); err != nil {
		return libopusDecoderDREDDecodeFloatInfo{}, fmt.Errorf("encode decoder dred decode helper dred model blob: %w", err)
	}

	cmd := exec.Command(binPath)
	cmd.Stdin = bytes.NewReader(payload.Bytes())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return libopusDecoderDREDDecodeFloatInfo{}, fmt.Errorf("run decoder dred decode helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}

	out := stdout.Bytes()
	const headerSize = 108
	if len(out) < headerSize || string(out[:4]) != libopusDecoderDREDDecodeFloatOutputMagic {
		return libopusDecoderDREDDecodeFloatInfo{}, fmt.Errorf("unexpected decoder dred decode helper output")
	}
	info := libopusDecoderDREDDecodeFloatInfo{
		parseRet:  int(int32(binary.LittleEndian.Uint32(out[8:12]))),
		dredEnd:   int(int32(binary.LittleEndian.Uint32(out[12:16]))),
		warmupRet: int(int32(binary.LittleEndian.Uint32(out[16:20]))),
		ret:       int(int32(binary.LittleEndian.Uint32(out[20:24]))),
		nextRet:   int(int32(binary.LittleEndian.Uint32(out[24:28]))),
		channels:  int(int32(binary.LittleEndian.Uint32(out[28:32]))),
	}
	info.state.Blend = int(int32(binary.LittleEndian.Uint32(out[32:36])))
	info.state.LossCount = int(int32(binary.LittleEndian.Uint32(out[36:40])))
	info.state.AnalysisGap = int(int32(binary.LittleEndian.Uint32(out[40:44])))
	info.state.AnalysisPos = int(int32(binary.LittleEndian.Uint32(out[44:48])))
	info.state.PredictPos = int(int32(binary.LittleEndian.Uint32(out[48:52])))
	info.state.FECReadPos = int(int32(binary.LittleEndian.Uint32(out[52:56])))
	info.state.FECFillPos = int(int32(binary.LittleEndian.Uint32(out[56:60])))
	info.state.FECSkip = int(int32(binary.LittleEndian.Uint32(out[60:64])))
	info.fargan.ContInitialized = int32(binary.LittleEndian.Uint32(out[64:68])) != 0
	info.fargan.LastPeriod = int(int32(binary.LittleEndian.Uint32(out[68:72])))
	info.celt48k.LastFrameType = int(int32(binary.LittleEndian.Uint32(out[72:76])))
	info.celt48k.PLCFill = int(int32(binary.LittleEndian.Uint32(out[76:80])))
	info.celt48k.PLCDuration = int(int32(binary.LittleEndian.Uint32(out[80:84])))
	info.celt48k.SkipPLC = int(int32(binary.LittleEndian.Uint32(out[84:88])))
	info.celt48k.PLCPreemphasisMem = math.Float32frombits(binary.LittleEndian.Uint32(out[88:92]))
	info.silk.LagPrev = int(int32(binary.LittleEndian.Uint32(out[92:96])))
	info.silk.LastGainIndex = int(int32(binary.LittleEndian.Uint32(out[96:100])))
	info.silk.LossCount = int(int32(binary.LittleEndian.Uint32(out[100:104])))
	info.silk.PrevSignalType = int(int32(binary.LittleEndian.Uint32(out[104:108])))
	offset := headerSize
	readBits := func(dst []float32) error {
		for i := range dst {
			if offset+4 > len(out) {
				return fmt.Errorf("truncated decoder dred decode helper payload")
			}
			dst[i] = math.Float32frombits(binary.LittleEndian.Uint32(out[offset : offset+4]))
			offset += 4
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
	if offset+4 > len(out) {
		return libopusDecoderDREDDecodeFloatInfo{}, fmt.Errorf("truncated decoder dred decode helper deemph")
	}
	info.fargan.DeemphMem = math.Float32frombits(binary.LittleEndian.Uint32(out[offset : offset+4]))
	offset += 4
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
	if offset+4*(2+1+len(info.celt48k.WarmupPLCUpdate)) <= len(out) {
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
	return info, nil
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
		t.Skipf("libopus dred packet helper unavailable: %v", err)
	}
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		t.Skipf("libopus dred model helper unavailable: %v", err)
	}
	channels := 1
	toc := ParseTOC(packetInfo.packet[0])
	if toc.Stereo {
		channels = 2
	}
	if channels != 1 {
		t.Skipf("explicit DRED decode parity requires mono packet, got sampleRate=%d channels=%d", packetInfo.sampleRate, channels)
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
	if _, _, err := standalone.Parse(dred, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, true); err != nil {
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

	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		t.Skipf("libopus dred model helper unavailable: %v", err)
	}

	channels := 1
	toc := ParseTOC(packetInfo.packet[0])
	if toc.Stereo {
		channels = 2
	}
	if channels != 1 {
		t.Skipf("cached DRED decode parity requires mono packet, got sampleRate=%d channels=%d", packetInfo.sampleRate, channels)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(packetInfo.sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
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
		t.Skipf("%s libopus decoder DRED sequence helper unavailable: %v", label, err)
	}
	if want.carrierParseRet < 0 {
		t.Skipf("%s libopus cached DRED sequence parse failed: %d", label, want.carrierParseRet)
	}
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
		t.Skipf("%s libopus decoder DRED sequence helper unavailable: %v", label, err)
	}
	if want.carrierParseRet < 0 {
		t.Skipf("%s libopus cached DRED sequence parse failed: %d", label, want.carrierParseRet)
	}
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
	if frameSize >= 960 {
		// A 20 ms 48 kHz loss makes the libopus neural PLC synthesize three
		// consecutive 16 kHz FARGAN frames. Keep the seam pinned by PCM, PLC
		// lifecycle, CELT bridge, and FARGAN headers; the private recurrent
		// vectors are numerically sensitive after repeated synthesis.
		return 5e-3, 1e-2, 9e-2, 5e-3
	}
	return pcmTol, plcTol, farganTol, celtTol
}

func TestDecoderCachedDREDDecodeMatrixMatchesLiveSequenceOracle(t *testing.T) {
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthFullband,
			})
			if err != nil {
				t.Skipf("libopus dred packet helper unavailable: %v", err)
			}
			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertDecoderCachedDREDFirstLossMatchesLiveSequenceOracleWithTolerances(t, "cached CELT", packetInfo, pcmTol, plcTol, farganTol, celtTol)
		})
	}
}

func TestDecoderCachedDREDThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthFullband,
			})
			if err != nil {
				t.Skipf("libopus dred packet helper unavailable: %v", err)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, frameSize)

			dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("cached CELT live-sequence parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 0, 0, true)
			if err != nil {
				t.Skipf("libopus decoder DRED sequence helper unavailable: %v", err)
			}
			if want.carrierParseRet < 0 {
				t.Skipf("libopus cached CELT DRED sequence parse failed: %d", want.carrierParseRet)
			}
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

func TestDecoderCachedDREDSecondLossMatchesLiveSequenceOracle(t *testing.T) {
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthFullband,
			})
			if err != nil {
				t.Skipf("libopus dred packet helper unavailable: %v", err)
			}
			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertDecoderCachedDREDSecondLossMatchesLiveSequenceOracleWithTolerances(t, "cached CELT", packetInfo, pcmTol, plcTol, farganTol, celtTol)
		})
	}
}

func TestDecoderCachedDREDSecondLossThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthFullband,
			})
			if err != nil {
				t.Skipf("libopus dred packet helper unavailable: %v", err)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, frameSize)

			dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("cached CELT second-loss live-sequence parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 1, 2*n, true)
			if err != nil {
				t.Skipf("libopus decoder DRED sequence helper unavailable: %v", err)
			}
			if want.carrierParseRet < 0 {
				t.Skipf("libopus cached CELT DRED sequence parse failed: %d", want.carrierParseRet)
			}
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

func TestDecoderExplicitHybridDREDDecodeMatrixMatchesLibopus(t *testing.T) {
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
				t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
			}
			if want.parseRet < 0 {
				t.Skipf("libopus decoder hybrid DRED parse failed: %d", want.parseRet)
			}
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

func TestDecoderExplicitHybridDREDDecodeThenNextPacketMatchesLibopus(t *testing.T) {
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
				t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
			}
			if want.parseRet < 0 {
				t.Skipf("libopus decoder hybrid DRED parse failed: %d", want.parseRet)
			}
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

func cachedHybridLiveSequenceTolerances(_ Bandwidth, frameSize int) (pcmTol, plcTol, farganTol, celtTol float64) {
	pcmTol, plcTol, farganTol, celtTol = decoderDREDLiveSequenceTolerances(frameSize)
	return pcmTol, plcTol, farganTol, celtTol
}

func TestDecoderCachedHybridDREDDecodeMatrixMatchesLiveSequenceOracle(t *testing.T) {
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
				t.Skipf("libopus dred packet helper unavailable: %v", err)
			}
			pcmTol, plcTol, farganTol, celtTol := cachedHybridLiveSequenceTolerances(tc.bandwidth, tc.frameSize)
			assertDecoderCachedDREDFirstLossMatchesLiveSequenceOracleWithTolerances(t, "cached hybrid", packetInfo, pcmTol, plcTol, farganTol, celtTol)
		})
	}
}

func TestDecoderCachedHybridDREDThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
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
				t.Skipf("libopus dred packet helper unavailable: %v", err)
			}
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 0, 0, true)
			if err != nil {
				t.Skipf("libopus decoder DRED sequence helper unavailable: %v", err)
			}
			if want.carrierParseRet < 0 {
				t.Skipf("libopus cached hybrid DRED sequence parse failed: %d", want.carrierParseRet)
			}
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
				t.Skipf("libopus dred packet helper unavailable: %v", err)
			}
			pcmTol, plcTol, farganTol, celtTol := cachedHybridLiveSequenceTolerances(tc.bandwidth, tc.frameSize)
			assertDecoderCachedDREDSecondLossMatchesLiveSequenceOracleWithTolerances(t, "cached hybrid", packetInfo, pcmTol, plcTol, farganTol, celtTol)
		})
	}
}

func TestDecoderCachedHybridSecondLossThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
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
				t.Skipf("libopus dred packet helper unavailable: %v", err)
			}
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 1, 2*n, true)
			if err != nil {
				t.Skipf("libopus decoder DRED sequence helper unavailable: %v", err)
			}
			if want.carrierParseRet < 0 {
				t.Skipf("libopus cached hybrid DRED sequence parse failed: %d", want.carrierParseRet)
			}
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

func TestDecoderExplicitDREDWarmup48kStateMatchesLibopus(t *testing.T) {
	dec, _, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n <= 0 {
		t.Skipf("48 kHz warmup parity requires 48 kHz packet, got sampleRate=%d frame=%d", packetInfo.sampleRate, n)
	}
	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
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
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
	}
	if want.parseRet < 0 {
		t.Skipf("libopus decoder DRED parse failed: %d", want.parseRet)
	}
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

func TestDecoderExplicitDREDDecode16kMatchesLibopus(t *testing.T) {
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState16k(t)

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
	}
	if want.parseRet < 0 {
		t.Skipf("libopus decoder DRED parse failed: %d", want.parseRet)
	}
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

	assertFloat32ApproxEqual(t, pcm[:n], want.pcm[:n], "explicit 16k libopus pcm", 1e-4)
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k libopus plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k libopus fargan")
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "explicit 16k libopus celt")
}

func TestDecoderExplicitDREDCELT48kBridgeMatchesLibopusFirstLoss(t *testing.T) {
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n <= 0 {
		t.Skipf("48 kHz explicit bridge parity requires 48 kHz packet, got sampleRate=%d frame=%d", packetInfo.sampleRate, n)
	}

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
	}
	if want.parseRet < 0 || want.ret != n {
		t.Skipf("libopus decoder DRED decode not available: parse=%d ret=%d", want.parseRet, want.ret)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "explicit first libopus celt")
}

func TestDecoderExplicitDREDDecodeSecondLossMatchesLibopus(t *testing.T) {
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)

	pcm0 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
	if err != nil {
		t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
	}
	if want.parseRet < 0 {
		t.Skipf("libopus decoder DRED parse failed: %d", want.parseRet)
	}
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

func TestDecoderExplicitDREDDecodeSecondLoss16kMatchesLibopus(t *testing.T) {
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState16k(t)

	pcm0 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
	if err != nil {
		t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
	}
	if want.parseRet < 0 {
		t.Skipf("libopus decoder DRED parse failed: %d", want.parseRet)
	}
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

	assertFloat32ApproxEqual(t, pcm[:n], want.pcm[:n], "explicit 16k second libopus pcm", 1e-4)
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k second libopus plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k second libopus fargan")
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "explicit 16k second libopus celt")
}

func TestDecoderExplicitDREDCELT48kBridgeMatchesLibopusSecondLoss(t *testing.T) {
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
		t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
	}
	if want.parseRet < 0 || want.ret != n {
		t.Skipf("libopus decoder DRED second decode not available: parse=%d ret=%d", want.parseRet, want.ret)
	}

	pcm1 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
	}
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "explicit second libopus celt")
}

func TestDecoderExplicitDREDDecodeThenNextPacketMatchesLibopus(t *testing.T) {
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
		t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
	}
	if want.parseRet < 0 {
		t.Skipf("libopus decoder DRED parse failed: %d", want.parseRet)
	}
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
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState16k(t)
	nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, 480)

	lossPCM := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
	}
	if want.parseRet < 0 {
		t.Skipf("libopus decoder DRED parse failed: %d", want.parseRet)
	}
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

	assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.nextPCM[:gotNext], "explicit 16k next packet pcm", 1e-4)
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k next packet plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k next packet fargan")
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "explicit 16k next packet celt")
}

func TestDecoderExplicitDREDDecode16kFrameSizeMatrixMatchesLibopus(t *testing.T) {
	for _, frameSize := range []int{480, 960} {
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState16kForFrameSize(t, frameSize)

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
			}
			if want.parseRet < 0 {
				t.Skipf("libopus decoder DRED parse failed: %d", want.parseRet)
			}
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

			assertFloat32ApproxEqual(t, pcm[:n], want.pcm[:n], "explicit 16k frame-size libopus pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k frame-size libopus plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k frame-size libopus fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "explicit 16k frame-size libopus celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeThenNextPacket16kFrameSizeMatrixMatchesLibopus(t *testing.T) {
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState16kForFrameSize(t, frameSize)
			nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, frameSize)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
			}
			if want.parseRet < 0 {
				t.Skipf("libopus decoder DRED parse failed: %d", want.parseRet)
			}
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

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.nextPCM[:gotNext], "explicit 16k follow-up frame-size pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k follow-up frame-size plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k follow-up frame-size fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "explicit 16k follow-up frame-size celt")
		})
	}
}

func TestDecoderExplicitSecondLossThenNextPacket16kMatchesLibopus(t *testing.T) {
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

	want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
	if err != nil {
		t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
	}
	if want.parseRet < 0 {
		t.Skipf("libopus decoder DRED parse failed: %d", want.parseRet)
	}
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

	assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.nextPCM[:gotNext], "explicit 16k second-loss next packet pcm", 1e-4)
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k second-loss next packet plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k second-loss next packet fargan")
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "explicit 16k second-loss next packet celt")
}

func TestDecoderExplicitSecondLossThenNextPacketMatchesLibopus(t *testing.T) {
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
		t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
	}
	if want.parseRet < 0 {
		t.Skipf("libopus decoder DRED parse failed: %d", want.parseRet)
	}
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
				t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
			}
			if want.parseRet < 0 {
				t.Skipf("libopus decoder DRED parse failed: %d", want.parseRet)
			}
			if want.ret != n {
				t.Fatalf("libopus decoder DRED decode ret=%d want %d", want.ret, n)
			}

			localDec, err := NewDecoder(DefaultDecoderConfig(packetInfo.sampleRate, 1))
			if err != nil {
				t.Fatalf("NewDecoder error: %v", err)
			}
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

func TestDecoderExplicitDREDDecodeFrameSizeMatrixMatchesLibopus(t *testing.T) {
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForFrameSize(t, frameSize)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz explicit frame-size parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
			}
			if want.parseRet < 0 {
				t.Skipf("libopus decoder DRED parse failed: %d", want.parseRet)
			}
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

func TestDecoderExplicitDREDDecodeSecondLossFrameSizeMatrixMatchesLibopus(t *testing.T) {
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
				t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
			}
			if want.parseRet < 0 {
				t.Skipf("libopus decoder DRED parse failed: %d", want.parseRet)
			}
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

func TestDecoderExplicitDREDDecodeThenNextPacketFrameSizeMatrixMatchesLibopus(t *testing.T) {
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
				t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
			}
			if want.parseRet < 0 {
				t.Skipf("libopus decoder DRED parse failed: %d", want.parseRet)
			}
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

func TestDecoderExplicitSecondLossThenNextPacketFrameSizeMatrixMatchesLibopus(t *testing.T) {
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
				t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
			}
			if want.parseRet < 0 {
				t.Skipf("libopus decoder DRED parse failed: %d", want.parseRet)
			}
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
