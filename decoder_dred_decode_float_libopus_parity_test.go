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

	libopusPitchDNNModelBlobHelperOnce sync.Once
	libopusPitchDNNModelBlobHelperPath string
	libopusPitchDNNModelBlobHelperErr  error

	libopusPLCModelBlobHelperOnce sync.Once
	libopusPLCModelBlobHelperPath string
	libopusPLCModelBlobHelperErr  error

	libopusFARGANModelBlobHelperOnce sync.Once
	libopusFARGANModelBlobHelperPath string
	libopusFARGANModelBlobHelperErr  error
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

func getLibopusPitchDNNModelBlobHelperPath() (string, error) {
	libopusPitchDNNModelBlobHelperOnce.Do(func() {
		libopusPitchDNNModelBlobHelperPath, libopusPitchDNNModelBlobHelperErr = buildLibopusDREDHelper("libopus_pitchdnn_model_blob.c", "gopus_libopus_pitchdnn_model_blob", true)
	})
	if libopusPitchDNNModelBlobHelperErr != nil {
		return "", libopusPitchDNNModelBlobHelperErr
	}
	return libopusPitchDNNModelBlobHelperPath, nil
}

func getLibopusPLCModelBlobHelperPath() (string, error) {
	libopusPLCModelBlobHelperOnce.Do(func() {
		libopusPLCModelBlobHelperPath, libopusPLCModelBlobHelperErr = buildLibopusDREDHelper("libopus_plc_model_blob.c", "gopus_libopus_plc_model_blob", true)
	})
	if libopusPLCModelBlobHelperErr != nil {
		return "", libopusPLCModelBlobHelperErr
	}
	return libopusPLCModelBlobHelperPath, nil
}

func getLibopusFARGANModelBlobHelperPath() (string, error) {
	libopusFARGANModelBlobHelperOnce.Do(func() {
		libopusFARGANModelBlobHelperPath, libopusFARGANModelBlobHelperErr = buildLibopusDREDHelper("libopus_fargan_model_blob.c", "gopus_libopus_fargan_model_blob", true)
	})
	if libopusFARGANModelBlobHelperErr != nil {
		return "", libopusFARGANModelBlobHelperErr
	}
	return libopusFARGANModelBlobHelperPath, nil
}

func runModelBlobHelper(binPath string) ([]byte, error) {
	cmd := exec.Command(binPath)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run model blob helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return stdout.Bytes(), nil
}

func probeLibopusDecoderNeuralModelBlob() ([]byte, error) {
	pitchPath, err := getLibopusPitchDNNModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	plcPath, err := getLibopusPLCModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	farganPath, err := getLibopusFARGANModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	pitchBlob, err := runModelBlobHelper(pitchPath)
	if err != nil {
		return nil, err
	}
	plcBlob, err := runModelBlobHelper(plcPath)
	if err != nil {
		return nil, err
	}
	farganBlob, err := runModelBlobHelper(farganPath)
	if err != nil {
		return nil, err
	}
	blob := make([]byte, 0, len(pitchBlob)+len(plcBlob)+len(farganBlob))
	blob = append(blob, pitchBlob...)
	blob = append(blob, plcBlob...)
	blob = append(blob, farganBlob...)
	return blob, nil
}

func requireLibopusDecoderNeuralModelBlob(t *testing.T) []byte {
	t.Helper()
	blob, err := probeLibopusDecoderNeuralModelBlob()
	if err != nil {
		t.Skipf("libopus decoder neural model helper unavailable: %v", err)
	}
	return blob
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
	const headerSize = 92
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
	assertFloat32ApproxEqual(t, got.Features[:], want.Features[:], label+" features", 1e-4)
	assertFloat32ApproxEqual(t, got.Cont[:], want.Cont[:], label+" continuity", 1e-4)
	assertFloat32ApproxEqual(t, got.PCM[:], want.PCM[:], label+" pcm history", 1e-4)
	assertFloat32ApproxEqual(t, got.PLCNet.GRU1[:], want.PLCNet.GRU1[:], label+" plc net gru1", 1e-4)
	assertFloat32ApproxEqual(t, got.PLCNet.GRU2[:], want.PLCNet.GRU2[:], label+" plc net gru2", 1e-4)
	assertFloat32ApproxEqual(t, got.PLCBak[0].GRU1[:], want.PLCBak[0].GRU1[:], label+" plc bak0 gru1", 1e-4)
	assertFloat32ApproxEqual(t, got.PLCBak[0].GRU2[:], want.PLCBak[0].GRU2[:], label+" plc bak0 gru2", 1e-4)
	assertFloat32ApproxEqual(t, got.PLCBak[1].GRU1[:], want.PLCBak[1].GRU1[:], label+" plc bak1 gru1", 1e-4)
	assertFloat32ApproxEqual(t, got.PLCBak[1].GRU2[:], want.PLCBak[1].GRU2[:], label+" plc bak1 gru2", 1e-4)
}

func assertDecoderDREDFARGANStateApproxEqual(t *testing.T, got, want lpcnetplc.FARGANSnapshot, label string) {
	t.Helper()
	if got.ContInitialized != want.ContInitialized || got.LastPeriod != want.LastPeriod {
		t.Fatalf("%s header=%+v want %+v", label, got, want)
	}
	if math.Abs(float64(got.DeemphMem-want.DeemphMem)) > 1e-4 {
		t.Fatalf("%s deemph=%f want %f", label, got.DeemphMem, want.DeemphMem)
	}
	assertFloat32ApproxEqual(t, got.PitchBuf[:], want.PitchBuf[:], label+" pitch", 1e-4)
	assertFloat32ApproxEqual(t, got.CondConv1State[:], want.CondConv1State[:], label+" cond", 1e-4)
	assertFloat32ApproxEqual(t, got.FWC0Mem[:], want.FWC0Mem[:], label+" fwc0", 1e-4)
	assertFloat32ApproxEqual(t, got.GRU1State[:], want.GRU1State[:], label+" gru1", 1e-4)
	assertFloat32ApproxEqual(t, got.GRU2State[:], want.GRU2State[:], label+" gru2", 1e-4)
	assertFloat32ApproxEqual(t, got.GRU3State[:], want.GRU3State[:], label+" gru3", 1e-4)
}

func assertDecoderDREDCELT48kBridgeApproxEqual(t *testing.T, dec *Decoder, want libopusDecoderDREDCELTSnapshot, label string) {
	t.Helper()
	var plcState celt.PLCStateSnapshot
	var preemphMem [2]float32
	if dec.celtDecoder != nil {
		plcState = dec.celtDecoder.SnapshotPLCState()
		preemphMem = dec.celtDecoder.SnapshotPreemphasisState()
	}
	if plcState.LastFrameType != want.LastFrameType || plcState.PLCDuration != want.PLCDuration || plcState.SkipPLC != (want.SkipPLC != 0) {
		t.Fatalf("%s celt plc state=%+v want {LastFrameType:%d PLCDuration:%d SkipPLC:%t}", label, plcState, want.LastFrameType, want.PLCDuration, want.SkipPLC != 0)
	}
	assertFloat32ApproxEqual(t, preemphMem[:], want.PreemphMem[:], label+" celt preemph_memD", 1e-4)
	state := requireDecoderDREDState(t, dec)
	if state.dredPLCFill != want.PLCFill {
		t.Fatalf("%s fill=%d want %d (lastFrameType=%d plcDuration=%d skipPLC=%d preemph=%f)", label, state.dredPLCFill, want.PLCFill, want.LastFrameType, want.PLCDuration, want.SkipPLC, want.PLCPreemphasisMem)
	}
	if math.Abs(float64(state.dredPLCPreemphMem-want.PLCPreemphasisMem)) > 1e-4 {
		t.Fatalf("%s preemph=%f want %f", label, state.dredPLCPreemphMem, want.PLCPreemphasisMem)
	}
	wantNeural := want.LastFrameType == libopusCELTFramePLCNeural || want.LastFrameType == libopusCELTFrameDRED
	if state.dredLastNeural != wantNeural {
		t.Fatalf("%s lastNeural=%v want %v (lastFrameType=%d)", label, state.dredLastNeural, wantNeural, want.LastFrameType)
	}
	assertFloat32ApproxEqual(t, state.dredPLCPCM[:], want.PLCPCM[:], label+" plc pcm", 1e-4)
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
	if state := requireDecoderDREDState(t, dec); state != nil {
		snap.PLCFill = state.dredPLCFill
		snap.PLCPreemphasisMem = state.dredPLCPreemphMem
		snap.PLCPCM = state.dredPLCPCM
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

func TestDecoderCachedHybridDREDDecodeMatrixMatchesExplicitDREDOracle(t *testing.T) {
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

			dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
			if n != tc.frameSize {
				t.Skipf("cached hybrid parity requires frame=%d packet, got frame=%d", tc.frameSize, n)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(packetInfo.packet, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
			}
			if want.parseRet < 0 {
				t.Skipf("libopus cached hybrid DRED parse failed: %d", want.parseRet)
			}
			if want.ret != n {
				t.Fatalf("libopus cached hybrid decoder DRED decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil)=%d want %d", got, n)
			}

			assertFloat32ApproxEqual(t, pcm[:got], want.pcm[:got], "cached hybrid explicit oracle pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "cached hybrid explicit oracle plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "cached hybrid explicit oracle fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "cached hybrid explicit oracle celt")
		})
	}
}

func TestDecoderCachedHybridDREDThenNextPacketMatchesExplicitDREDOracle(t *testing.T) {
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
			pcm := make([]float32, dec.maxPacketSamples)
			if _, err := dec.Decode(nil, pcm); err != nil {
				t.Fatalf("Decode(nil) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloat(packetInfo.packet, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
			}
			if want.parseRet < 0 {
				t.Skipf("libopus cached hybrid DRED parse failed: %d", want.parseRet)
			}
			if want.ret != n {
				t.Fatalf("libopus cached hybrid decoder DRED decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus cached hybrid decoder follow-up ret=%d want >0", want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.nextRet)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.nextPCM[:gotNext], "cached hybrid next packet pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "cached hybrid next packet plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "cached hybrid next packet fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "cached hybrid next packet celt")
		})
	}
}

func TestDecoderCachedHybridDREDSecondLossMatchesExplicitDREDOracle(t *testing.T) {
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

			dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.Decode(nil, pcm0); err != nil {
				t.Fatalf("Decode(nil, first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(packetInfo.packet, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
			if err != nil {
				t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
			}
			if want.parseRet < 0 {
				t.Skipf("libopus cached hybrid DRED parse failed: %d", want.parseRet)
			}
			if want.warmupRet != n {
				t.Fatalf("libopus cached hybrid decoder warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus cached hybrid decoder second ret=%d want %d", want.ret, n)
			}

			pcm1 := make([]float32, dec.maxPacketSamples)
			got, err := dec.Decode(nil, pcm1)
			if err != nil {
				t.Fatalf("Decode(nil, second) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil, second)=%d want %d", got, n)
			}

			assertFloat32ApproxEqual(t, pcm1[:got], want.pcm[:got], "cached hybrid second-loss pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "cached hybrid second-loss plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "cached hybrid second-loss fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "cached hybrid second-loss celt")
		})
	}
}

func TestDecoderCachedHybridSecondLossThenNextPacketMatchesExplicitDREDOracle(t *testing.T) {
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
			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.Decode(nil, pcm0); err != nil {
				t.Fatalf("Decode(nil, first) error: %v", err)
			}
			pcm1 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.Decode(nil, pcm1); err != nil {
				t.Fatalf("Decode(nil, second) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloat(packetInfo.packet, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
			if err != nil {
				t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
			}
			if want.parseRet < 0 {
				t.Skipf("libopus cached hybrid DRED parse failed: %d", want.parseRet)
			}
			if want.warmupRet != n {
				t.Fatalf("libopus cached hybrid decoder warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus cached hybrid decoder second ret=%d want %d", want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus cached hybrid decoder follow-up ret=%d want >0", want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.nextRet)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.nextPCM[:gotNext], "cached hybrid second-loss next packet pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "cached hybrid second-loss next packet plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "cached hybrid second-loss next packet fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "cached hybrid second-loss next packet celt")
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

func TestDecoderExplicitDREDDecodeMatchesCachedPath(t *testing.T) {
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		t.Skipf("libopus dred model helper unavailable: %v", err)
	}
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)

	explicitDec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
	explicitPCM := make([]float32, explicitDec.maxPacketSamples*explicitDec.channels)
	gotExplicit, err := explicitDec.decodeExplicitDREDFloat(dred, n, explicitPCM, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if gotExplicit != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", gotExplicit, n)
	}

	cachedDec, err := NewDecoder(DefaultDecoderConfig(packetInfo.sampleRate, 1))
	if err != nil {
		t.Fatalf("NewDecoder(cached) error: %v", err)
	}
	if err := cachedDec.SetDNNBlob(decoderBlob); err != nil {
		t.Fatalf("SetDNNBlob(cached) error: %v", err)
	}
	blob, err := dnnblob.Clone(modelBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone(real model) error: %v", err)
	}
	if err := blob.ValidateDREDDecoderControl(); err != nil {
		t.Fatalf("ValidateDREDDecoderControl(real model) error: %v", err)
	}
	cachedDec.setDREDDecoderBlob(blob)
	cachedSeed := make([]float32, cachedDec.maxPacketSamples)
	if _, err := cachedDec.Decode(seedPacket, cachedSeed); err != nil {
		t.Fatalf("Decode(cached seed packet) error: %v", err)
	}
	cachedDec.maybeCacheDREDPayload(packetInfo.packet)
	cachedPCM := make([]float32, cachedDec.maxPacketSamples)
	gotCached, err := cachedDec.Decode(nil, cachedPCM)
	if err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}
	if gotCached != n {
		t.Fatalf("Decode(nil)=%d want %d", gotCached, n)
	}

	assertFloat32ApproxEqual(t, explicitPCM[:n], cachedPCM[:n], "explicit vs cached pcm", 1e-4)
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, explicitDec).dredPLC.Snapshot(), requireDecoderDREDState(t, cachedDec).dredPLC.Snapshot(), "explicit vs cached plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, explicitDec).dredFARGAN.Snapshot(), requireDecoderDREDState(t, cachedDec).dredFARGAN.Snapshot(), "explicit vs cached fargan")
}

func TestDecoderExplicitDREDFirstConcealFrameBootstraps48kRuntime(t *testing.T) {
	dec, dred, packetInfo, _, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n < lpcnetplc.FrameSize {
		t.Skipf("48 kHz bootstrap regression requires 48 kHz packet and >=%d samples, got sampleRate=%d frame=%d", lpcnetplc.FrameSize, packetInfo.sampleRate, n)
	}
	if got := dec.primeDREDCELTEntryHistory(dec.prevMode); got == 0 {
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
	if got := dec.primeDREDCELTEntryHistory(dec.prevMode); got == 0 {
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
	if got := dec.primeDREDCELTEntryHistory(dec.prevMode); got == 0 {
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
	if got := dec.primeDREDCELTEntryHistory(dec.prevMode); got == 0 {
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
		if !requireDecoderDREDState(t, dec).dredPLC.ConcealFrameFloat(&requireDecoderDREDState(t, dec).dredPredictor, &requireDecoderDREDState(t, dec).dredFARGAN, frame[:]) {
			t.Fatalf("ConcealFrameFloat returned false at frame %d (plc=%+v fargan=%+v)", i, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), requireDecoderDREDState(t, dec).dredFARGAN.Snapshot())
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

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, dec.sampleRate, -1, n, n)
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
}

func TestDecoderExplicitDREDDecode16kMatchesCachedPath(t *testing.T) {
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		t.Skipf("libopus dred model helper unavailable: %v", err)
	}
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)

	explicitDec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState16k(t)
	explicitPCM := make([]float32, explicitDec.maxPacketSamples)
	gotExplicit, err := explicitDec.decodeExplicitDREDFloat(dred, n, explicitPCM, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if gotExplicit != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", gotExplicit, n)
	}

	cachedDec, err := NewDecoder(DefaultDecoderConfig(explicitDec.sampleRate, 1))
	if err != nil {
		t.Fatalf("NewDecoder(cached) error: %v", err)
	}
	if err := cachedDec.SetDNNBlob(decoderBlob); err != nil {
		t.Fatalf("SetDNNBlob(cached) error: %v", err)
	}
	blob, err := dnnblob.Clone(modelBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone(real model) error: %v", err)
	}
	if err := blob.ValidateDREDDecoderControl(); err != nil {
		t.Fatalf("ValidateDREDDecoderControl(real model) error: %v", err)
	}
	cachedDec.setDREDDecoderBlob(blob)
	cachedSeed := make([]float32, cachedDec.maxPacketSamples)
	if _, err := cachedDec.Decode(seedPacket, cachedSeed); err != nil {
		t.Fatalf("Decode(cached seed packet) error: %v", err)
	}
	cachedDec.maybeCacheDREDPayload(packetInfo.packet)
	cachedPCM := make([]float32, cachedDec.maxPacketSamples)
	gotCached, err := cachedDec.Decode(nil, cachedPCM)
	if err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}
	if gotCached != n {
		t.Fatalf("Decode(nil)=%d want %d", gotCached, n)
	}

	assertFloat32ApproxEqual(t, explicitPCM[:n], cachedPCM[:n], "explicit 16k vs cached pcm", 1e-4)
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, explicitDec).dredPLC.Snapshot(), requireDecoderDREDState(t, cachedDec).dredPLC.Snapshot(), "explicit 16k vs cached plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, explicitDec).dredFARGAN.Snapshot(), requireDecoderDREDState(t, cachedDec).dredFARGAN.Snapshot(), "explicit 16k vs cached fargan")
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

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, dec.sampleRate, n, 2*n, n)
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

	want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, dec.sampleRate, -1, n, n)
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
}

func TestDecoderExplicitDREDDecode16kFrameSizeMatrixMatchesLibopus(t *testing.T) {
	for _, frameSize := range []int{480, 960} {
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState16kForFrameSize(t, frameSize)

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, dec.sampleRate, -1, n, n)
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

	want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, dec.sampleRate, n, 2*n, n)
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

func TestDecoderExplicitSecondLossThenNextPacketMatchesCachedPath(t *testing.T) {
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		t.Skipf("libopus dred model helper unavailable: %v", err)
	}
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)

	explicitDec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
	nextPacket := makeValidMonoCELTPacketForDREDTest(t)

	explicitPCM0 := make([]float32, explicitDec.maxPacketSamples)
	if _, err := explicitDec.decodeExplicitDREDFloat(dred, n, explicitPCM0, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}
	explicitPCM1 := make([]float32, explicitDec.maxPacketSamples)
	if _, err := explicitDec.decodeExplicitDREDFloat(dred, 2*n, explicitPCM1, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
	}
	explicitNext := make([]float32, explicitDec.maxPacketSamples)
	gotExplicitNext, err := explicitDec.Decode(nextPacket, explicitNext)
	if err != nil {
		t.Fatalf("Decode(explicit next packet) error: %v", err)
	}

	cachedDec, err := NewDecoder(DefaultDecoderConfig(packetInfo.sampleRate, 1))
	if err != nil {
		t.Fatalf("NewDecoder(cached) error: %v", err)
	}
	if err := cachedDec.SetDNNBlob(decoderBlob); err != nil {
		t.Fatalf("SetDNNBlob(cached) error: %v", err)
	}
	blob, err := dnnblob.Clone(modelBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone(real model) error: %v", err)
	}
	if err := blob.ValidateDREDDecoderControl(); err != nil {
		t.Fatalf("ValidateDREDDecoderControl(real model) error: %v", err)
	}
	cachedDec.setDREDDecoderBlob(blob)
	cachedSeed := make([]float32, cachedDec.maxPacketSamples)
	if _, err := cachedDec.Decode(seedPacket, cachedSeed); err != nil {
		t.Fatalf("Decode(cached seed packet) error: %v", err)
	}
	cachedDec.maybeCacheDREDPayload(packetInfo.packet)
	cachedPCM0 := make([]float32, cachedDec.maxPacketSamples)
	if _, err := cachedDec.Decode(nil, cachedPCM0); err != nil {
		t.Fatalf("Decode(nil, first) error: %v", err)
	}
	cachedPCM1 := make([]float32, cachedDec.maxPacketSamples)
	if _, err := cachedDec.Decode(nil, cachedPCM1); err != nil {
		t.Fatalf("Decode(nil, second) error: %v", err)
	}
	cachedNext := make([]float32, cachedDec.maxPacketSamples)
	gotCachedNext, err := cachedDec.Decode(nextPacket, cachedNext)
	if err != nil {
		t.Fatalf("Decode(cached next packet) error: %v", err)
	}

	if gotExplicitNext != gotCachedNext {
		t.Fatalf("next packet samples explicit=%d cached=%d", gotExplicitNext, gotCachedNext)
	}
	assertFloat32ApproxEqual(t, explicitNext[:gotExplicitNext], cachedNext[:gotCachedNext], "explicit second-loss next packet vs cached pcm", 1e-4)
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, explicitDec).dredPLC.Snapshot(), requireDecoderDREDState(t, cachedDec).dredPLC.Snapshot(), "explicit second-loss next packet vs cached plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, explicitDec).dredFARGAN.Snapshot(), requireDecoderDREDState(t, cachedDec).dredFARGAN.Snapshot(), "explicit second-loss next packet vs cached fargan")
	assertDecoderDREDCELT48kBridgeApproxEqual(t, explicitDec, snapshotDecoderDREDCELT48kForTest(t, cachedDec), "explicit second-loss next packet vs cached celt")
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

			assertFloat32ApproxEqual(t, pcm[:n], want.pcm[:n], "offset matrix pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, localDec).dredPLC.Snapshot(), want.state, "offset matrix plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, localDec).dredFARGAN.Snapshot(), want.fargan, "offset matrix fargan")
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

func TestDecoderExplicitSecondLossThenNextPacketFrameSizeMatrixMatchesCachedPath(t *testing.T) {
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		t.Skipf("libopus dred model helper unavailable: %v", err)
	}
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)

	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			explicitDec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForFrameSize(t, frameSize)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz explicit second-loss follow-up cached parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, frameSize)

			explicitPCM0 := make([]float32, explicitDec.maxPacketSamples)
			if _, err := explicitDec.decodeExplicitDREDFloat(dred, n, explicitPCM0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}
			explicitPCM1 := make([]float32, explicitDec.maxPacketSamples)
			if _, err := explicitDec.decodeExplicitDREDFloat(dred, 2*n, explicitPCM1, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}
			explicitNext := make([]float32, explicitDec.maxPacketSamples)
			gotExplicitNext, err := explicitDec.Decode(nextPacket, explicitNext)
			if err != nil {
				t.Fatalf("Decode(explicit next packet) error: %v", err)
			}

			cachedDec, err := NewDecoder(DefaultDecoderConfig(packetInfo.sampleRate, 1))
			if err != nil {
				t.Fatalf("NewDecoder(cached) error: %v", err)
			}
			if err := cachedDec.SetDNNBlob(decoderBlob); err != nil {
				t.Fatalf("SetDNNBlob(cached) error: %v", err)
			}
			blob, err := dnnblob.Clone(modelBlob)
			if err != nil {
				t.Fatalf("dnnblob.Clone(real model) error: %v", err)
			}
			if err := blob.ValidateDREDDecoderControl(); err != nil {
				t.Fatalf("ValidateDREDDecoderControl(real model) error: %v", err)
			}
			cachedDec.setDREDDecoderBlob(blob)
			cachedSeed := make([]float32, cachedDec.maxPacketSamples)
			if _, err := cachedDec.Decode(seedPacket, cachedSeed); err != nil {
				t.Fatalf("Decode(cached seed packet) error: %v", err)
			}
			cachedDec.maybeCacheDREDPayload(packetInfo.packet)
			cachedPCM0 := make([]float32, cachedDec.maxPacketSamples)
			if _, err := cachedDec.Decode(nil, cachedPCM0); err != nil {
				t.Fatalf("Decode(nil, first) error: %v", err)
			}
			cachedPCM1 := make([]float32, cachedDec.maxPacketSamples)
			if _, err := cachedDec.Decode(nil, cachedPCM1); err != nil {
				t.Fatalf("Decode(nil, second) error: %v", err)
			}
			cachedNext := make([]float32, cachedDec.maxPacketSamples)
			gotCachedNext, err := cachedDec.Decode(nextPacket, cachedNext)
			if err != nil {
				t.Fatalf("Decode(cached next packet) error: %v", err)
			}

			if gotExplicitNext != gotCachedNext {
				t.Fatalf("next packet samples explicit=%d cached=%d", gotExplicitNext, gotCachedNext)
			}
			assertFloat32ApproxEqual(t, explicitNext[:gotExplicitNext], cachedNext[:gotCachedNext], "second-loss follow-up frame size matrix cached pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, explicitDec).dredPLC.Snapshot(), requireDecoderDREDState(t, cachedDec).dredPLC.Snapshot(), "second-loss follow-up frame size matrix cached plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, explicitDec).dredFARGAN.Snapshot(), requireDecoderDREDState(t, cachedDec).dredFARGAN.Snapshot(), "second-loss follow-up frame size matrix cached fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, explicitDec, snapshotDecoderDREDCELT48kForTest(t, cachedDec), "second-loss follow-up frame size matrix cached celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeSecondLossMatchesCachedPath(t *testing.T) {
	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		t.Skipf("libopus dred model helper unavailable: %v", err)
	}
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)

	explicitDec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
	explicitPCM0 := make([]float32, explicitDec.maxPacketSamples)
	if _, err := explicitDec.decodeExplicitDREDFloat(dred, n, explicitPCM0, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}
	explicitPCM1 := make([]float32, explicitDec.maxPacketSamples)
	gotExplicit, err := explicitDec.decodeExplicitDREDFloat(dred, 2*n, explicitPCM1, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
	}
	if gotExplicit != n {
		t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", gotExplicit, n)
	}

	cachedDec, err := NewDecoder(DefaultDecoderConfig(packetInfo.sampleRate, 1))
	if err != nil {
		t.Fatalf("NewDecoder(cached) error: %v", err)
	}
	if err := cachedDec.SetDNNBlob(decoderBlob); err != nil {
		t.Fatalf("SetDNNBlob(cached) error: %v", err)
	}
	blob, err := dnnblob.Clone(modelBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone(real model) error: %v", err)
	}
	if err := blob.ValidateDREDDecoderControl(); err != nil {
		t.Fatalf("ValidateDREDDecoderControl(real model) error: %v", err)
	}
	cachedDec.setDREDDecoderBlob(blob)
	cachedSeed := make([]float32, cachedDec.maxPacketSamples)
	if _, err := cachedDec.Decode(seedPacket, cachedSeed); err != nil {
		t.Fatalf("Decode(cached seed packet) error: %v", err)
	}
	cachedDec.maybeCacheDREDPayload(packetInfo.packet)
	cachedPCM0 := make([]float32, cachedDec.maxPacketSamples)
	if _, err := cachedDec.Decode(nil, cachedPCM0); err != nil {
		t.Fatalf("Decode(nil, first) error: %v", err)
	}
	cachedPCM1 := make([]float32, cachedDec.maxPacketSamples)
	gotCached, err := cachedDec.Decode(nil, cachedPCM1)
	if err != nil {
		t.Fatalf("Decode(nil, second) error: %v", err)
	}
	if gotCached != n {
		t.Fatalf("Decode(nil, second)=%d want %d", gotCached, n)
	}

	assertFloat32ApproxEqual(t, explicitPCM1[:n], cachedPCM1[:n], "explicit second vs cached second pcm", 1e-4)
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, explicitDec).dredPLC.Snapshot(), requireDecoderDREDState(t, cachedDec).dredPLC.Snapshot(), "explicit second vs cached second plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, explicitDec).dredFARGAN.Snapshot(), requireDecoderDREDState(t, cachedDec).dredFARGAN.Snapshot(), "explicit second vs cached second fargan")
}
