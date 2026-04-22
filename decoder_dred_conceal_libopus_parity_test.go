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

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

const (
	libopusPLCConcealInputMagic  = "GPCI"
	libopusPLCConcealOutputMagic = "GPCO"
)

type libopusDecoderPLCConcealResult struct {
	GotFEC bool
	Frame  [lpcnetplc.FrameSize]float32
	State  lpcnetplc.StateSnapshot
	FARGAN lpcnetplc.FARGANSnapshot
}

var (
	libopusPLCConcealHelperOnce sync.Once
	libopusPLCConcealHelperPath string
	libopusPLCConcealHelperErr  error
)

func getLibopusPLCConcealHelperPath() (string, error) {
	libopusPLCConcealHelperOnce.Do(func() {
		libopusPLCConcealHelperPath, libopusPLCConcealHelperErr = buildLibopusDREDHelper("libopus_plc_conceal_info.c", "gopus_libopus_plc_conceal", true)
	})
	if libopusPLCConcealHelperErr != nil {
		return "", libopusPLCConcealHelperErr
	}
	return libopusPLCConcealHelperPath, nil
}

func probeLibopusDecoderPLCConceal(state lpcnetplc.StateSnapshot, fargan lpcnetplc.FARGANSnapshot, fec0, fec1 []float32) (libopusDecoderPLCConcealResult, error) {
	return probeLibopusDecoderPLCConcealQueue(state, fargan, [][]float32{fec0, fec1})
}

func probeLibopusDecoderPLCConcealQueue(state lpcnetplc.StateSnapshot, fargan lpcnetplc.FARGANSnapshot, queue [][]float32) (libopusDecoderPLCConcealResult, error) {
	binPath, err := getLibopusPLCConcealHelperPath()
	if err != nil {
		return libopusDecoderPLCConcealResult{}, err
	}
	for _, features := range queue {
		if len(features) != lpcnetplc.NumFeatures {
			return libopusDecoderPLCConcealResult{}, fmt.Errorf("invalid conceal helper FEC size")
		}
	}

	var payload bytes.Buffer
	payload.WriteString(libopusPLCConcealInputMagic)
	if err := binary.Write(&payload, binary.LittleEndian, uint32(2)); err != nil {
		return libopusDecoderPLCConcealResult{}, fmt.Errorf("encode plc conceal version: %w", err)
	}
	for _, v := range []int32{
		int32(state.Blend),
		int32(state.LossCount),
		int32(state.AnalysisGap),
		int32(state.AnalysisPos),
		int32(state.PredictPos),
		int32(state.FECReadPos),
		int32(state.FECFillPos),
		int32(state.FECSkip),
	} {
		if err := binary.Write(&payload, binary.LittleEndian, v); err != nil {
			return libopusDecoderPLCConcealResult{}, fmt.Errorf("encode plc conceal header: %w", err)
		}
	}
	writeBits := func(values []float32) error {
		for _, v := range values {
			if err := binary.Write(&payload, binary.LittleEndian, math.Float32bits(v)); err != nil {
				return err
			}
		}
		return nil
	}
	for _, values := range [][]float32{
		state.Features[:],
		state.Cont[:],
		state.PCM[:],
		state.PLCNet.GRU1[:],
		state.PLCNet.GRU2[:],
		state.PLCBak[0].GRU1[:],
		state.PLCBak[0].GRU2[:],
		state.PLCBak[1].GRU1[:],
		state.PLCBak[1].GRU2[:],
	} {
		if err := writeBits(values); err != nil {
			return libopusDecoderPLCConcealResult{}, fmt.Errorf("encode plc conceal state: %w", err)
		}
	}
	var contInitialized int32
	if fargan.ContInitialized {
		contInitialized = 1
	}
	if err := binary.Write(&payload, binary.LittleEndian, contInitialized); err != nil {
		return libopusDecoderPLCConcealResult{}, fmt.Errorf("encode plc conceal fargan flag: %w", err)
	}
	if err := binary.Write(&payload, binary.LittleEndian, int32(fargan.LastPeriod)); err != nil {
		return libopusDecoderPLCConcealResult{}, fmt.Errorf("encode plc conceal fargan last period: %w", err)
	}
	if err := binary.Write(&payload, binary.LittleEndian, int32(len(queue))); err != nil {
		return libopusDecoderPLCConcealResult{}, fmt.Errorf("encode plc conceal queue count: %w", err)
	}
	for _, values := range [][]float32{
		{fargan.DeemphMem},
		fargan.PitchBuf[:],
		fargan.CondConv1State[:],
		fargan.FWC0Mem[:],
		fargan.GRU1State[:],
		fargan.GRU2State[:],
		fargan.GRU3State[:],
	} {
		if err := writeBits(values); err != nil {
			return libopusDecoderPLCConcealResult{}, fmt.Errorf("encode plc conceal fargan payload: %w", err)
		}
	}
	for _, features := range queue {
		if err := writeBits(features); err != nil {
			return libopusDecoderPLCConcealResult{}, fmt.Errorf("encode plc conceal queue payload: %w", err)
		}
	}

	cmd := exec.Command(binPath)
	cmd.Stdin = bytes.NewReader(payload.Bytes())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return libopusDecoderPLCConcealResult{}, fmt.Errorf("run plc conceal helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}

	data := stdout.Bytes()
	const header = 48
	if len(data) < header || string(data[:4]) != libopusPLCConcealOutputMagic {
		return libopusDecoderPLCConcealResult{}, fmt.Errorf("unexpected plc conceal helper output")
	}
	result := libopusDecoderPLCConcealResult{
		GotFEC: data[8] != 0,
	}
	result.State.Blend = int(int32(binary.LittleEndian.Uint32(data[12:16])))
	result.State.LossCount = int(int32(binary.LittleEndian.Uint32(data[16:20])))
	result.State.AnalysisGap = int(int32(binary.LittleEndian.Uint32(data[20:24])))
	result.State.AnalysisPos = int(int32(binary.LittleEndian.Uint32(data[24:28])))
	result.State.PredictPos = int(int32(binary.LittleEndian.Uint32(data[28:32])))
	result.State.FECReadPos = int(int32(binary.LittleEndian.Uint32(data[32:36])))
	result.State.FECSkip = int(int32(binary.LittleEndian.Uint32(data[36:40])))
	result.FARGAN.ContInitialized = int32(binary.LittleEndian.Uint32(data[40:44])) != 0
	result.FARGAN.LastPeriod = int(int32(binary.LittleEndian.Uint32(data[44:48])))

	offset := header
	readBits := func(dst []float32) error {
		for i := range dst {
			if len(data) < offset+4 {
				return fmt.Errorf("truncated plc conceal helper output")
			}
			dst[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[offset : offset+4]))
			offset += 4
		}
		return nil
	}
	if err := readBits(result.Frame[:]); err != nil {
		return libopusDecoderPLCConcealResult{}, err
	}
	if err := readBits(result.State.Features[:]); err != nil {
		return libopusDecoderPLCConcealResult{}, err
	}
	if err := readBits(result.State.Cont[:]); err != nil {
		return libopusDecoderPLCConcealResult{}, err
	}
	if err := readBits(result.State.PCM[:]); err != nil {
		return libopusDecoderPLCConcealResult{}, err
	}
	if err := readBits(result.State.PLCNet.GRU1[:]); err != nil {
		return libopusDecoderPLCConcealResult{}, err
	}
	if err := readBits(result.State.PLCNet.GRU2[:]); err != nil {
		return libopusDecoderPLCConcealResult{}, err
	}
	if err := readBits(result.State.PLCBak[0].GRU1[:]); err != nil {
		return libopusDecoderPLCConcealResult{}, err
	}
	if err := readBits(result.State.PLCBak[0].GRU2[:]); err != nil {
		return libopusDecoderPLCConcealResult{}, err
	}
	if err := readBits(result.State.PLCBak[1].GRU1[:]); err != nil {
		return libopusDecoderPLCConcealResult{}, err
	}
	if err := readBits(result.State.PLCBak[1].GRU2[:]); err != nil {
		return libopusDecoderPLCConcealResult{}, err
	}
	var deemph [1]float32
	if err := readBits(deemph[:]); err != nil {
		return libopusDecoderPLCConcealResult{}, err
	}
	result.FARGAN.DeemphMem = deemph[0]
	if err := readBits(result.FARGAN.PitchBuf[:]); err != nil {
		return libopusDecoderPLCConcealResult{}, err
	}
	if err := readBits(result.FARGAN.CondConv1State[:]); err != nil {
		return libopusDecoderPLCConcealResult{}, err
	}
	if err := readBits(result.FARGAN.FWC0Mem[:]); err != nil {
		return libopusDecoderPLCConcealResult{}, err
	}
	if err := readBits(result.FARGAN.GRU1State[:]); err != nil {
		return libopusDecoderPLCConcealResult{}, err
	}
	if err := readBits(result.FARGAN.GRU2State[:]); err != nil {
		return libopusDecoderPLCConcealResult{}, err
	}
	if err := readBits(result.FARGAN.GRU3State[:]); err != nil {
		return libopusDecoderPLCConcealResult{}, err
	}
	return result, nil
}

func prepareDecoderForNeuralConcealmentParity(t *testing.T) (*Decoder, []float32, int, [lpcnetplc.NumFeatures]float32, [lpcnetplc.NumFeatures]float32) {
	t.Helper()

	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		t.Skipf("libopus dred model helper unavailable: %v", err)
	}
	packetInfo, err := emitLibopusDREDPacket()
	if err != nil {
		t.Skipf("libopus dred packet helper unavailable: %v", err)
	}

	channels := 1
	if ParseTOC(packetInfo.packet[0]).Stereo {
		channels = 2
	}
	if packetInfo.sampleRate != 16000 || channels != 1 {
		t.Skipf("conceal parity test requires 16 kHz mono packet, got sampleRate=%d channels=%d", packetInfo.sampleRate, channels)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(packetInfo.sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
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
		t.Fatalf("Decode error: %v", err)
	}
	if n != lpcnetplc.FrameSize {
		t.Skipf("conceal parity helper is single-frame only, got frame size %d", n)
	}
	if got := dec.primeDREDCELTEntryHistory(dec.prevMode); got != lpcnetplc.FrameSize {
		t.Fatalf("primeDREDCELTEntryHistory()=%d want %d", got, lpcnetplc.FrameSize)
	}
	window := dec.queueActiveDREDRecovery(n)
	if window.NeededFeatureFrames == 0 {
		t.Fatal("queueActiveDREDRecovery produced empty window")
	}
	var fec0 [lpcnetplc.NumFeatures]float32
	var fec1 [lpcnetplc.NumFeatures]float32
	_ = requireDecoderDREDState(t, dec).dredPLC.FillQueuedFeatures(0, fec0[:])
	_ = requireDecoderDREDState(t, dec).dredPLC.FillQueuedFeatures(1, fec1[:])
	return dec, pcm, n, fec0, fec1
}

func TestDecoderFirstLossNeuralConcealmentMatchesLibopus(t *testing.T) {
	dec, pcm, n, fec0, fec1 := prepareDecoderForNeuralConcealmentParity(t)

	want, err := probeLibopusDecoderPLCConceal(requireDecoderDREDState(t, dec).dredPLC.Snapshot(), requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), fec0[:], fec1[:])
	if err != nil {
		t.Skipf("libopus plc conceal helper unavailable: %v", err)
	}

	gotN, err := dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}
	if gotN != n {
		t.Fatalf("Decode(nil)=%d want %d", gotN, n)
	}

	assertFloat32ApproxEqual(t, pcm[:n], want.Frame[:], "concealed pcm", 1e-4)

	gotState := requireDecoderDREDState(t, dec).dredPLC.Snapshot()
	if gotState.Blend != want.State.Blend ||
		gotState.LossCount != want.State.LossCount ||
		gotState.AnalysisGap != want.State.AnalysisGap ||
		gotState.AnalysisPos != want.State.AnalysisPos ||
		gotState.PredictPos != want.State.PredictPos ||
		gotState.FECFillPos != want.State.FECFillPos ||
		gotState.FECReadPos != want.State.FECReadPos ||
		gotState.FECSkip != want.State.FECSkip {
		t.Fatalf("state header=%+v want %+v", gotState, want.State)
	}
	assertFloat32ApproxEqual(t, gotState.Features[:], want.State.Features[:], "state features", 1e-4)
	assertFloat32ApproxEqual(t, gotState.Cont[:], want.State.Cont[:], "state continuity", 1e-4)
	assertFloat32ApproxEqual(t, gotState.PCM[:], want.State.PCM[:], "state pcm", 1e-4)
	assertFloat32ApproxEqual(t, gotState.PLCNet.GRU1[:], want.State.PLCNet.GRU1[:], "state plc_net gru1", 1e-4)
	assertFloat32ApproxEqual(t, gotState.PLCNet.GRU2[:], want.State.PLCNet.GRU2[:], "state plc_net gru2", 1e-4)
	assertFloat32ApproxEqual(t, gotState.PLCBak[0].GRU1[:], want.State.PLCBak[0].GRU1[:], "state plc_bak0 gru1", 1e-4)
	assertFloat32ApproxEqual(t, gotState.PLCBak[0].GRU2[:], want.State.PLCBak[0].GRU2[:], "state plc_bak0 gru2", 1e-4)
	assertFloat32ApproxEqual(t, gotState.PLCBak[1].GRU1[:], want.State.PLCBak[1].GRU1[:], "state plc_bak1 gru1", 1e-4)
	assertFloat32ApproxEqual(t, gotState.PLCBak[1].GRU2[:], want.State.PLCBak[1].GRU2[:], "state plc_bak1 gru2", 1e-4)

	gotFARGAN := requireDecoderDREDState(t, dec).dredFARGAN.Snapshot()
	if gotFARGAN.ContInitialized != want.FARGAN.ContInitialized || gotFARGAN.LastPeriod != want.FARGAN.LastPeriod {
		t.Fatalf("fargan header=%+v want %+v", gotFARGAN, want.FARGAN)
	}
	assertFloat32ApproxEqual(t, []float32{gotFARGAN.DeemphMem}, []float32{want.FARGAN.DeemphMem}, "fargan deemph", 1e-4)
	assertFloat32ApproxEqual(t, gotFARGAN.PitchBuf[:], want.FARGAN.PitchBuf[:], "fargan pitch", 1e-4)
	assertFloat32ApproxEqual(t, gotFARGAN.CondConv1State[:], want.FARGAN.CondConv1State[:], "fargan cond", 1e-4)
	assertFloat32ApproxEqual(t, gotFARGAN.FWC0Mem[:], want.FARGAN.FWC0Mem[:], "fargan fwc0", 1e-4)
	assertFloat32ApproxEqual(t, gotFARGAN.GRU1State[:], want.FARGAN.GRU1State[:], "fargan gru1", 1e-4)
	assertFloat32ApproxEqual(t, gotFARGAN.GRU2State[:], want.FARGAN.GRU2State[:], "fargan gru2", 1e-4)
	assertFloat32ApproxEqual(t, gotFARGAN.GRU3State[:], want.FARGAN.GRU3State[:], "fargan gru3", 1e-4)
}

func TestDecoderSecondLossNeuralConcealmentMatchesLibopus(t *testing.T) {
	dec, pcm, n, fec0, fec1 := prepareDecoderForNeuralConcealmentParity(t)

	if _, err := dec.Decode(nil, pcm); err != nil {
		t.Fatalf("Decode(nil, first) error: %v", err)
	}
	window := dec.queueActiveDREDRecovery(n)
	if window.NeededFeatureFrames == 0 {
		t.Fatal("second-loss queueActiveDREDRecovery produced empty window")
	}

	want, err := probeLibopusDecoderPLCConceal(requireDecoderDREDState(t, dec).dredPLC.Snapshot(), requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), fec0[:], fec1[:])
	if err != nil {
		t.Skipf("libopus plc conceal helper unavailable: %v", err)
	}

	gotN, err := dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("Decode(nil, second) error: %v", err)
	}
	if gotN != n {
		t.Fatalf("Decode(nil, second)=%d want %d", gotN, n)
	}

	assertFloat32ApproxEqual(t, pcm[:n], want.Frame[:], "second concealed pcm", 1e-4)

	gotState := requireDecoderDREDState(t, dec).dredPLC.Snapshot()
	if gotState.Blend != want.State.Blend ||
		gotState.LossCount != want.State.LossCount ||
		gotState.AnalysisGap != want.State.AnalysisGap ||
		gotState.AnalysisPos != want.State.AnalysisPos ||
		gotState.PredictPos != want.State.PredictPos ||
		gotState.FECFillPos != want.State.FECFillPos ||
		gotState.FECReadPos != want.State.FECReadPos ||
		gotState.FECSkip != want.State.FECSkip {
		t.Fatalf("second state header=%+v want %+v", gotState, want.State)
	}
	assertFloat32ApproxEqual(t, gotState.Features[:], want.State.Features[:], "second state features", 1e-4)
	assertFloat32ApproxEqual(t, gotState.Cont[:], want.State.Cont[:], "second state continuity", 1e-4)
	assertFloat32ApproxEqual(t, gotState.PCM[:], want.State.PCM[:], "second state pcm", 1e-4)
	assertFloat32ApproxEqual(t, gotState.PLCNet.GRU1[:], want.State.PLCNet.GRU1[:], "second state plc_net gru1", 1e-4)
	assertFloat32ApproxEqual(t, gotState.PLCNet.GRU2[:], want.State.PLCNet.GRU2[:], "second state plc_net gru2", 1e-4)
	assertFloat32ApproxEqual(t, gotState.PLCBak[0].GRU1[:], want.State.PLCBak[0].GRU1[:], "second state plc_bak0 gru1", 1e-4)
	assertFloat32ApproxEqual(t, gotState.PLCBak[0].GRU2[:], want.State.PLCBak[0].GRU2[:], "second state plc_bak0 gru2", 1e-4)
	assertFloat32ApproxEqual(t, gotState.PLCBak[1].GRU1[:], want.State.PLCBak[1].GRU1[:], "second state plc_bak1 gru1", 1e-4)
	assertFloat32ApproxEqual(t, gotState.PLCBak[1].GRU2[:], want.State.PLCBak[1].GRU2[:], "second state plc_bak1 gru2", 1e-4)

	gotFARGAN := requireDecoderDREDState(t, dec).dredFARGAN.Snapshot()
	if gotFARGAN.ContInitialized != want.FARGAN.ContInitialized || gotFARGAN.LastPeriod != want.FARGAN.LastPeriod {
		t.Fatalf("second fargan header=%+v want %+v", gotFARGAN, want.FARGAN)
	}
	assertFloat32ApproxEqual(t, []float32{gotFARGAN.DeemphMem}, []float32{want.FARGAN.DeemphMem}, "second fargan deemph", 1e-4)
	assertFloat32ApproxEqual(t, gotFARGAN.PitchBuf[:], want.FARGAN.PitchBuf[:], "second fargan pitch", 1e-4)
	assertFloat32ApproxEqual(t, gotFARGAN.CondConv1State[:], want.FARGAN.CondConv1State[:], "second fargan cond", 1e-4)
	assertFloat32ApproxEqual(t, gotFARGAN.FWC0Mem[:], want.FARGAN.FWC0Mem[:], "second fargan fwc0", 1e-4)
	assertFloat32ApproxEqual(t, gotFARGAN.GRU1State[:], want.FARGAN.GRU1State[:], "second fargan gru1", 1e-4)
	assertFloat32ApproxEqual(t, gotFARGAN.GRU2State[:], want.FARGAN.GRU2State[:], "second fargan gru2", 1e-4)
	assertFloat32ApproxEqual(t, gotFARGAN.GRU3State[:], want.FARGAN.GRU3State[:], "second fargan gru3", 1e-4)
}
