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

	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

const (
	libopusDecoderDREDSequenceInputMagic  = "GDSI"
	libopusDecoderDREDSequenceOutputMagic = "GDSO"
)

type libopusDecoderDREDSequenceStepInfo struct {
	ret     int
	state   lpcnetplc.StateSnapshot
	fargan  lpcnetplc.FARGANSnapshot
	celt48k libopusDecoderDREDCELTSnapshot
	silk    libopusDecoderDREDSILKSnapshot
	pcm     []float32
}

type libopusDecoderDREDSILKSnapshot struct {
	LagPrev        int
	LastGainIndex  int
	LossCount      int
	PrevSignalType int
	SMid           [2]float32
	OutBuf         [480]float32
	SLPCQ14        [16]float32
	ExcQ14         [320]float32
	ResamplerIIR   [6]float32
	ResamplerFIR   [8]float32
	ResamplerDelay [96]float32
}

type libopusDecoderDREDSequenceInfo struct {
	carrierParseRet int
	carrierDredEnd  int
	nextParseRet    int
	nextDredEnd     int
	carrierRet      int
	channels        int
	step0           libopusDecoderDREDSequenceStepInfo
	step1           libopusDecoderDREDSequenceStepInfo
	next            libopusDecoderDREDSequenceStepInfo
}

var (
	libopusDecoderDREDSequenceHelperOnce sync.Once
	libopusDecoderDREDSequenceHelperPath string
	libopusDecoderDREDSequenceHelperErr  error
)

func getLibopusDecoderDREDSequenceHelperPath() (string, error) {
	libopusDecoderDREDSequenceHelperOnce.Do(func() {
		libopusDecoderDREDSequenceHelperPath, libopusDecoderDREDSequenceHelperErr = buildLibopusDREDHelper("libopus_decoder_dred_sequence_info.c", "gopus_libopus_decoder_dred_sequence", true)
	})
	if libopusDecoderDREDSequenceHelperErr != nil {
		return "", libopusDecoderDREDSequenceHelperErr
	}
	return libopusDecoderDREDSequenceHelperPath, nil
}

func probeLibopusDecoderDREDSequence(seedPacket, carrierPacket, nextPacket []byte, maxDREDSamples, sampleRate, frameSizeSamples, step0Source, step0OffsetSamples, step1Source, step1OffsetSamples int, decodeNextPacket bool) (libopusDecoderDREDSequenceInfo, error) {
	binPath, err := getLibopusDecoderDREDSequenceHelperPath()
	if err != nil {
		return libopusDecoderDREDSequenceInfo{}, err
	}
	decoderModelBlob, err := probeLibopusDecoderNeuralModelBlob()
	if err != nil {
		return libopusDecoderDREDSequenceInfo{}, err
	}
	dredModelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		return libopusDecoderDREDSequenceInfo{}, err
	}

	var payload bytes.Buffer
	payload.WriteString(libopusDecoderDREDSequenceInputMagic)
	for _, v := range []uint32{
		1,
		uint32(sampleRate),
		uint32(maxDREDSamples),
		uint32(frameSizeSamples),
		uint32(len(seedPacket)),
		uint32(len(carrierPacket)),
		uint32(len(nextPacket)),
		uint32(len(decoderModelBlob)),
		uint32(len(dredModelBlob)),
		uint32(step0Source),
	} {
		if err := binary.Write(&payload, binary.LittleEndian, v); err != nil {
			return libopusDecoderDREDSequenceInfo{}, fmt.Errorf("encode decoder dred sequence helper header: %w", err)
		}
	}
	if err := binary.Write(&payload, binary.LittleEndian, int32(step0OffsetSamples)); err != nil {
		return libopusDecoderDREDSequenceInfo{}, fmt.Errorf("encode decoder dred sequence helper step0 offset: %w", err)
	}
	if err := binary.Write(&payload, binary.LittleEndian, uint32(step1Source)); err != nil {
		return libopusDecoderDREDSequenceInfo{}, fmt.Errorf("encode decoder dred sequence helper step1 source: %w", err)
	}
	if err := binary.Write(&payload, binary.LittleEndian, int32(step1OffsetSamples)); err != nil {
		return libopusDecoderDREDSequenceInfo{}, fmt.Errorf("encode decoder dred sequence helper step1 offset: %w", err)
	}
	var nextFlag uint32
	if decodeNextPacket {
		nextFlag = 1
	}
	if err := binary.Write(&payload, binary.LittleEndian, nextFlag); err != nil {
		return libopusDecoderDREDSequenceInfo{}, fmt.Errorf("encode decoder dred sequence helper decode-next flag: %w", err)
	}
	for _, chunk := range [][]byte{
		seedPacket,
		carrierPacket,
		nextPacket,
		decoderModelBlob,
		dredModelBlob,
	} {
		if _, err := payload.Write(chunk); err != nil {
			return libopusDecoderDREDSequenceInfo{}, fmt.Errorf("encode decoder dred sequence helper payload: %w", err)
		}
	}

	cmd := exec.Command(binPath)
	cmd.Stdin = bytes.NewReader(payload.Bytes())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return libopusDecoderDREDSequenceInfo{}, fmt.Errorf("run decoder dred sequence helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}

	out := stdout.Bytes()
	const headerSize = 44
	if len(out) < headerSize || string(out[:4]) != libopusDecoderDREDSequenceOutputMagic {
		return libopusDecoderDREDSequenceInfo{}, fmt.Errorf("unexpected decoder dred sequence helper output")
	}
	info := libopusDecoderDREDSequenceInfo{
		carrierParseRet: int(int32(binary.LittleEndian.Uint32(out[8:12]))),
		carrierDredEnd:  int(int32(binary.LittleEndian.Uint32(out[12:16]))),
		nextParseRet:    int(int32(binary.LittleEndian.Uint32(out[16:20]))),
		nextDredEnd:     int(int32(binary.LittleEndian.Uint32(out[20:24]))),
		carrierRet:      int(int32(binary.LittleEndian.Uint32(out[24:28]))),
		channels:        int(int32(binary.LittleEndian.Uint32(out[40:44]))),
	}
	info.step0.ret = int(int32(binary.LittleEndian.Uint32(out[28:32])))
	info.step1.ret = int(int32(binary.LittleEndian.Uint32(out[32:36])))
	info.next.ret = int(int32(binary.LittleEndian.Uint32(out[36:40])))

	offset := headerSize
	readBits := func(dst []float32) error {
		for i := range dst {
			if offset+4 > len(out) {
				return fmt.Errorf("truncated decoder dred sequence helper payload")
			}
			dst[i] = math.Float32frombits(binary.LittleEndian.Uint32(out[offset : offset+4]))
			offset += 4
		}
		return nil
	}
	readPCM := func(ret int) ([]float32, error) {
		if ret <= 0 || info.channels <= 0 {
			return nil, nil
		}
		dst := make([]float32, ret*info.channels)
		if err := readBits(dst); err != nil {
			return nil, err
		}
		return dst, nil
	}
	parseSnapshot := func(step *libopusDecoderDREDSequenceStepInfo) error {
		if offset+64 > len(out) {
			return fmt.Errorf("truncated decoder dred sequence helper snapshot header")
		}
		step.ret = int(int32(binary.LittleEndian.Uint32(out[offset : offset+4])))
		offset += 4
		step.state.Blend = int(int32(binary.LittleEndian.Uint32(out[offset : offset+4])))
		offset += 4
		step.state.LossCount = int(int32(binary.LittleEndian.Uint32(out[offset : offset+4])))
		offset += 4
		step.state.AnalysisGap = int(int32(binary.LittleEndian.Uint32(out[offset : offset+4])))
		offset += 4
		step.state.AnalysisPos = int(int32(binary.LittleEndian.Uint32(out[offset : offset+4])))
		offset += 4
		step.state.PredictPos = int(int32(binary.LittleEndian.Uint32(out[offset : offset+4])))
		offset += 4
		step.state.FECReadPos = int(int32(binary.LittleEndian.Uint32(out[offset : offset+4])))
		offset += 4
		step.state.FECFillPos = int(int32(binary.LittleEndian.Uint32(out[offset : offset+4])))
		offset += 4
		step.state.FECSkip = int(int32(binary.LittleEndian.Uint32(out[offset : offset+4])))
		offset += 4
		step.fargan.ContInitialized = int32(binary.LittleEndian.Uint32(out[offset:offset+4])) != 0
		offset += 4
		step.fargan.LastPeriod = int(int32(binary.LittleEndian.Uint32(out[offset : offset+4])))
		offset += 4
		step.celt48k.LastFrameType = int(int32(binary.LittleEndian.Uint32(out[offset : offset+4])))
		offset += 4
		step.celt48k.PLCFill = int(int32(binary.LittleEndian.Uint32(out[offset : offset+4])))
		offset += 4
		step.celt48k.PLCDuration = int(int32(binary.LittleEndian.Uint32(out[offset : offset+4])))
		offset += 4
		step.celt48k.SkipPLC = int(int32(binary.LittleEndian.Uint32(out[offset : offset+4])))
		offset += 4
		step.celt48k.PLCPreemphasisMem = math.Float32frombits(binary.LittleEndian.Uint32(out[offset : offset+4]))
		offset += 4
		for _, dst := range [][]float32{
			step.state.Features[:],
			step.state.Cont[:],
			step.state.PCM[:],
			step.state.PLCNet.GRU1[:],
			step.state.PLCNet.GRU2[:],
			step.state.PLCBak[0].GRU1[:],
			step.state.PLCBak[0].GRU2[:],
			step.state.PLCBak[1].GRU1[:],
			step.state.PLCBak[1].GRU2[:],
		} {
			if err := readBits(dst); err != nil {
				return err
			}
		}
		if offset+4 > len(out) {
			return fmt.Errorf("truncated decoder dred sequence helper deemph")
		}
		step.fargan.DeemphMem = math.Float32frombits(binary.LittleEndian.Uint32(out[offset : offset+4]))
		offset += 4
		for _, dst := range [][]float32{
			step.fargan.PitchBuf[:],
			step.fargan.CondConv1State[:],
			step.fargan.FWC0Mem[:],
			step.fargan.GRU1State[:],
			step.fargan.GRU2State[:],
			step.fargan.GRU3State[:],
			step.celt48k.PreemphMem[:],
			step.celt48k.PLCPCM[:],
		} {
			if err := readBits(dst); err != nil {
				return err
			}
		}
		if offset+16 > len(out) {
			return fmt.Errorf("truncated decoder dred sequence helper silk header")
		}
		step.silk.LagPrev = int(int32(binary.LittleEndian.Uint32(out[offset : offset+4])))
		offset += 4
		step.silk.LastGainIndex = int(int32(binary.LittleEndian.Uint32(out[offset : offset+4])))
		offset += 4
		step.silk.LossCount = int(int32(binary.LittleEndian.Uint32(out[offset : offset+4])))
		offset += 4
		step.silk.PrevSignalType = int(int32(binary.LittleEndian.Uint32(out[offset : offset+4])))
		offset += 4
		for _, dst := range [][]float32{
			step.silk.SMid[:],
			step.silk.OutBuf[:],
			step.silk.ResamplerIIR[:],
			step.silk.ResamplerFIR[:],
			step.silk.ResamplerDelay[:],
		} {
			if err := readBits(dst); err != nil {
				return err
			}
		}
		return nil
	}

	if info.step0.pcm, err = readPCM(info.step0.ret); err != nil {
		return libopusDecoderDREDSequenceInfo{}, err
	}
	if info.step1.pcm, err = readPCM(info.step1.ret); err != nil {
		return libopusDecoderDREDSequenceInfo{}, err
	}
	if info.next.pcm, err = readPCM(info.next.ret); err != nil {
		return libopusDecoderDREDSequenceInfo{}, err
	}
	for _, step := range []*libopusDecoderDREDSequenceStepInfo{&info.step0, &info.step1, &info.next} {
		if err := parseSnapshot(step); err != nil {
			return libopusDecoderDREDSequenceInfo{}, err
		}
	}
	return info, nil
}

func TestDecoderFirstLossNeuralConcealmentMatchesLiveSequenceOracle(t *testing.T) {
	dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParity(t)

	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 0, 0, false)
	if err != nil {
		t.Skipf("libopus decoder DRED sequence helper unavailable: %v", err)
	}
	if want.carrierParseRet < 0 {
		t.Skipf("libopus decoder DRED sequence carrier parse failed: %d", want.carrierParseRet)
	}
	if want.step0.ret != n {
		t.Fatalf("libopus decoder DRED first-loss ret=%d want %d", want.step0.ret, n)
	}

	gotN, err := dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}
	if gotN != n {
		t.Fatalf("Decode(nil)=%d want %d", gotN, n)
	}

	assertFloat32ApproxEqual(t, pcm[:n], want.step0.pcm[:n], "concealed pcm live-sequence oracle", 1e-4)
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "live 16k first-loss sequence oracle plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "live 16k first-loss sequence oracle fargan")
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.step0.celt48k, "live 16k first-loss sequence oracle celt")
}

func TestDecoderSecondLossNeuralConcealmentMatchesLiveSequenceOracle(t *testing.T) {
	dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParity(t)

	if _, err := dec.Decode(nil, pcm); err != nil {
		t.Fatalf("Decode(nil, first) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 1, 2*n, false)
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

	gotN, err := dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("Decode(nil, second) error: %v", err)
	}
	if gotN != n {
		t.Fatalf("Decode(nil, second)=%d want %d", gotN, n)
	}

	assertFloat32ApproxEqual(t, pcm[:n], want.step1.pcm[:n], "second concealed pcm live-sequence oracle", 1e-4)
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step1.state, "live 16k second-loss sequence oracle plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step1.fargan, "live 16k second-loss sequence oracle fargan")
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.step1.celt48k, "live 16k second-loss sequence oracle celt")
}

func TestDecoderFirstLossNeuralConcealment16kFrameSizeMatrixMatchesLiveSequenceOracle(t *testing.T) {
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParityForFrameSize(t, frameSize)

			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 0, 0, false)
			if err != nil {
				t.Skipf("libopus decoder DRED sequence helper unavailable: %v", err)
			}
			if want.carrierParseRet < 0 {
				t.Skipf("libopus decoder DRED sequence carrier parse failed: %d", want.carrierParseRet)
			}
			if want.step0.ret != n {
				t.Fatalf("libopus decoder DRED first-loss ret=%d want %d", want.step0.ret, n)
			}

			gotN, err := dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil) error: %v", err)
			}
			if gotN != n {
				t.Fatalf("Decode(nil)=%d want %d", gotN, n)
			}

			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertFloat32ApproxEqual(t, pcm[:n], want.step0.pcm[:n], "concealed frame-size pcm live-sequence oracle", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "live 16k first-loss frame-size sequence oracle plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "live 16k first-loss frame-size sequence oracle fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, "live 16k first-loss frame-size sequence oracle celt", celtTol)
		})
	}
}

func TestDecoderSecondLossNeuralConcealment16kFrameSizeMatrixMatchesLiveSequenceOracle(t *testing.T) {
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParityForFrameSize(t, frameSize)

			if _, err := dec.Decode(nil, pcm); err != nil {
				t.Fatalf("Decode(nil, first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 1, 2*n, false)
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

			gotN, err := dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil, second) error: %v", err)
			}
			if gotN != n {
				t.Fatalf("Decode(nil, second)=%d want %d", gotN, n)
			}

			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertFloat32ApproxEqual(t, pcm[:n], want.step1.pcm[:n], "second concealed frame-size pcm live-sequence oracle", pcmTol)
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step1.state, "live 16k second-loss frame-size sequence oracle plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step1.fargan, "live 16k second-loss frame-size sequence oracle fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step1.celt48k, "live 16k second-loss frame-size sequence oracle celt", celtTol)
		})
	}
}
