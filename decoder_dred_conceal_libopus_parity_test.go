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

func prepareDecoderForNeuralConcealmentParity(t *testing.T) (*Decoder, []float32, libopusDREDPacket, int) {
	t.Helper()
	return prepareDecoderForNeuralConcealmentParityForFrameSize(t, 480)
}

func prepareDecoderForNeuralConcealmentParityForFrameSize(t *testing.T, frameSize int) (*Decoder, []float32, libopusDREDPacket, int) {
	t.Helper()

	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		t.Skipf("libopus dred model helper unavailable: %v", err)
	}
	packetInfo, err := emitLibopusDREDPacketWithFrameSize(frameSize)
	if err != nil {
		t.Skipf("libopus dred packet helper unavailable: %v", err)
	}

	channels := 1
	if ParseTOC(packetInfo.packet[0]).Stereo {
		channels = 2
	}
	if channels != 1 {
		t.Skipf("conceal parity test requires mono packet, got sampleRate=%d channels=%d", packetInfo.sampleRate, channels)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(16000, channels))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(requireLibopusDecoderNeuralModelBlob(t)); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	setDREDDecoderBlobFromBytesForTest(t, dec, modelBlob)

	pcm := make([]float32, dec.maxPacketSamples*channels)
	n, err := dec.Decode(packetInfo.packet, pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if n <= 0 {
		t.Fatal("Decode returned no audio")
	}
	if state := requireDecoderDREDState(t, dec); state.dredCache.Empty() || state.dredDecoded.NbLatents <= 0 {
		t.Fatal("Decode did not retain processed DRED state")
	}
	return dec, pcm, packetInfo, n
}

func setDREDDecoderBlobFromBytesForTest(t *testing.T, dec *Decoder, modelBlob []byte) {
	t.Helper()

	blob, err := dnnblob.Clone(modelBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone(real model) error: %v", err)
	}
	if err := blob.ValidateDREDDecoderControl(); err != nil {
		t.Fatalf("ValidateDREDDecoderControl(real model) error: %v", err)
	}
	dec.setDREDDecoderBlob(blob)
}

func TestDecoderFirstLossNeuralConcealmentMatchesExplicitDREDOracle(t *testing.T) {
	dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParity(t)

	want, err := probeLibopusDecoderDREDDecodeFloat(packetInfo.packet, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		t.Skipf("libopus decoder DRED decode helper unavailable: %v", err)
	}
	if want.parseRet < 0 {
		t.Skipf("libopus decoder DRED parse failed: %d", want.parseRet)
	}
	if want.ret != n {
		t.Fatalf("libopus decoder DRED decode ret=%d want %d", want.ret, n)
	}

	gotN, err := dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}
	if gotN != n {
		t.Fatalf("Decode(nil)=%d want %d", gotN, n)
	}

	assertFloat32ApproxEqual(t, pcm[:n], want.pcm[:n], "concealed pcm", 1e-4)
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "live 16k first-loss explicit oracle plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "live 16k first-loss explicit oracle fargan")
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "live 16k first-loss explicit oracle celt")
}

func TestDecoderSecondLossNeuralConcealmentMatchesExplicitDREDOracle(t *testing.T) {
	dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParity(t)

	if _, err := dec.Decode(nil, pcm); err != nil {
		t.Fatalf("Decode(nil, first) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeFloat(packetInfo.packet, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
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

	gotN, err := dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("Decode(nil, second) error: %v", err)
	}
	if gotN != n {
		t.Fatalf("Decode(nil, second)=%d want %d", gotN, n)
	}

	assertFloat32ApproxEqual(t, pcm[:n], want.pcm[:n], "second concealed pcm", 1e-4)
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "live 16k second-loss explicit oracle plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "live 16k second-loss explicit oracle fargan")
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "live 16k second-loss explicit oracle celt")
}

func TestDecoderFirstLossThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParity(t)
	nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, 480)

	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 0, 0, true)
	if err != nil {
		t.Skipf("libopus decoder DRED sequence helper unavailable: %v", err)
	}
	if want.carrierParseRet < 0 {
		t.Skipf("libopus decoder DRED sequence carrier parse failed: %d", want.carrierParseRet)
	}
	if want.step0.ret != n {
		t.Fatalf("libopus decoder DRED first-loss ret=%d want %d", want.step0.ret, n)
	}
	if want.next.ret <= 0 {
		t.Fatalf("libopus decoder DRED next ret=%d want >0", want.next.ret)
	}

	gotN, err := dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}
	if gotN != n {
		t.Fatalf("Decode(nil)=%d want %d", gotN, n)
	}
	assertFloat32ApproxEqual(t, pcm[:n], want.step0.pcm[:n], "first-loss live-sequence pcm", 1e-4)

	nextPCM := make([]float32, dec.maxPacketSamples)
	gotNext, err := dec.Decode(nextPacket, nextPCM)
	if err != nil {
		t.Fatalf("Decode(next packet) error: %v", err)
	}
	if gotNext != want.next.ret {
		t.Fatalf("Decode(next packet)=%d want %d", gotNext, want.next.ret)
	}

	assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.next.pcm[:gotNext], "first-loss next packet live-sequence pcm", 1e-4)
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "first-loss next packet live-sequence plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "first-loss next packet live-sequence fargan")
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.next.celt48k, "first-loss next packet live-sequence celt")
}

func TestDecoderSecondLossThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParity(t)
	nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, 480)

	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 1, 2*n, true)
	if err != nil {
		t.Skipf("libopus decoder DRED sequence helper unavailable: %v", err)
	}
	if want.carrierParseRet < 0 {
		t.Skipf("libopus decoder DRED sequence carrier parse failed: %d", want.carrierParseRet)
	}
	if want.step0.ret != n {
		t.Fatalf("libopus decoder DRED first warmup ret=%d want %d", want.step0.ret, n)
	}
	if want.step1.ret != n {
		t.Fatalf("libopus decoder DRED second-loss ret=%d want %d", want.step1.ret, n)
	}
	if want.next.ret <= 0 {
		t.Fatalf("libopus decoder DRED next ret=%d want >0", want.next.ret)
	}

	gotN, err := dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("Decode(nil, first) error: %v", err)
	}
	if gotN != n {
		t.Fatalf("Decode(nil, first)=%d want %d", gotN, n)
	}
	assertFloat32ApproxEqual(t, pcm[:n], want.step0.pcm[:n], "second-loss warmup live-sequence pcm", 1e-4)

	gotN, err = dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("Decode(nil, second) error: %v", err)
	}
	if gotN != n {
		t.Fatalf("Decode(nil, second)=%d want %d", gotN, n)
	}
	assertFloat32ApproxEqual(t, pcm[:n], want.step1.pcm[:n], "second-loss live-sequence pcm", 1e-4)

	nextPCM := make([]float32, dec.maxPacketSamples)
	gotNext, err := dec.Decode(nextPacket, nextPCM)
	if err != nil {
		t.Fatalf("Decode(next packet) error: %v", err)
	}
	if gotNext != want.next.ret {
		t.Fatalf("Decode(next packet)=%d want %d", gotNext, want.next.ret)
	}

	assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.next.pcm[:gotNext], "second-loss next packet live-sequence pcm", 1e-4)
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "second-loss next packet live-sequence plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "second-loss next packet live-sequence fargan")
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.next.celt48k, "second-loss next packet live-sequence celt")
}

func TestDecoderFirstLossThenNextPacket16kFrameSizeMatrixMatchesLiveSequenceOracle(t *testing.T) {
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParityForFrameSize(t, frameSize)
			nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, frameSize)

			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 0, 0, true)
			if err != nil {
				t.Skipf("libopus decoder DRED sequence helper unavailable: %v", err)
			}
			if want.carrierParseRet < 0 {
				t.Skipf("libopus decoder DRED sequence carrier parse failed: %d", want.carrierParseRet)
			}
			if want.step0.ret != n {
				t.Fatalf("libopus decoder DRED first-loss ret=%d want %d", want.step0.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus decoder DRED next ret=%d want >0", want.next.ret)
			}

			gotN, err := dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil) error: %v", err)
			}
			if gotN != n {
				t.Fatalf("Decode(nil)=%d want %d", gotN, n)
			}
			assertFloat32ApproxEqual(t, pcm[:n], want.step0.pcm[:n], "first-loss frame-size live-sequence pcm", 1e-4)

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next packet)=%d want %d", gotNext, want.next.ret)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.next.pcm[:gotNext], "first-loss frame-size next packet live-sequence pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "first-loss frame-size next packet live-sequence plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "first-loss frame-size next packet live-sequence fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.next.celt48k, "first-loss frame-size next packet live-sequence celt")
		})
	}
}

func TestDecoderSecondLossThenNextPacket16kFrameSizeMatrixMatchesLiveSequenceOracle(t *testing.T) {
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParityForFrameSize(t, frameSize)
			nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, frameSize)

			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 1, 2*n, true)
			if err != nil {
				t.Skipf("libopus decoder DRED sequence helper unavailable: %v", err)
			}
			if want.carrierParseRet < 0 {
				t.Skipf("libopus decoder DRED sequence carrier parse failed: %d", want.carrierParseRet)
			}
			if want.step0.ret != n {
				t.Fatalf("libopus decoder DRED first warmup ret=%d want %d", want.step0.ret, n)
			}
			if want.step1.ret != n {
				t.Fatalf("libopus decoder DRED second-loss ret=%d want %d", want.step1.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus decoder DRED next ret=%d want >0", want.next.ret)
			}

			gotN, err := dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil, first) error: %v", err)
			}
			if gotN != n {
				t.Fatalf("Decode(nil, first)=%d want %d", gotN, n)
			}
			assertFloat32ApproxEqual(t, pcm[:n], want.step0.pcm[:n], "second-loss frame-size warmup live-sequence pcm", 1e-4)

			gotN, err = dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil, second) error: %v", err)
			}
			if gotN != n {
				t.Fatalf("Decode(nil, second)=%d want %d", gotN, n)
			}
			assertFloat32ApproxEqual(t, pcm[:n], want.step1.pcm[:n], "second-loss frame-size live-sequence pcm", 1e-4)

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next packet)=%d want %d", gotNext, want.next.ret)
			}

			assertFloat32ApproxEqual(t, nextPCM[:gotNext], want.next.pcm[:gotNext], "second-loss frame-size next packet live-sequence pcm", 1e-4)
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "second-loss frame-size next packet live-sequence plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "second-loss frame-size next packet live-sequence fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.next.celt48k, "second-loss frame-size next packet live-sequence celt")
		})
	}
}
