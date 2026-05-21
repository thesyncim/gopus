//go:build gopus_dred || gopus_extra_controls

package gopus

import (
	"fmt"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
	silkpkg "github.com/thesyncim/gopus/silk"
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

	payload := libopustest.NewOraclePayload(libopusDecoderDREDSequenceInputMagic,
		uint32(sampleRate),
		uint32(maxDREDSamples),
		uint32(frameSizeSamples),
		uint32(len(seedPacket)),
		uint32(len(carrierPacket)),
		uint32(len(nextPacket)),
		uint32(len(decoderModelBlob)),
		uint32(len(dredModelBlob)),
		uint32(step0Source),
	)
	payload.I32(int32(step0OffsetSamples))
	payload.U32(uint32(step1Source))
	payload.I32(int32(step1OffsetSamples))
	var nextFlag uint32
	if decodeNextPacket {
		nextFlag = 1
	}
	payload.U32(nextFlag)
	for _, chunk := range [][]byte{
		seedPacket,
		carrierPacket,
		nextPacket,
		decoderModelBlob,
		dredModelBlob,
	} {
		payload.Raw(chunk)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "decoder dred sequence", libopusDecoderDREDSequenceOutputMagic)
	if err != nil {
		return libopusDecoderDREDSequenceInfo{}, err
	}
	info := libopusDecoderDREDSequenceInfo{
		carrierParseRet: int(reader.I32()),
		carrierDredEnd:  int(reader.I32()),
		nextParseRet:    int(reader.I32()),
		nextDredEnd:     int(reader.I32()),
		carrierRet:      int(reader.I32()),
	}
	info.step0.ret = int(reader.I32())
	info.step1.ret = int(reader.I32())
	info.next.ret = int(reader.I32())
	info.channels = int(reader.I32())

	readBits := func(dst []float32) error {
		for i := range dst {
			dst[i] = reader.Float32()
		}
		if err := reader.Err(); err != nil {
			return err
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
		step.ret = int(reader.I32())
		step.state.Blend = int(reader.I32())
		step.state.LossCount = int(reader.I32())
		step.state.AnalysisGap = int(reader.I32())
		step.state.AnalysisPos = int(reader.I32())
		step.state.PredictPos = int(reader.I32())
		step.state.FECReadPos = int(reader.I32())
		step.state.FECFillPos = int(reader.I32())
		step.state.FECSkip = int(reader.I32())
		step.fargan.ContInitialized = reader.I32() != 0
		step.fargan.LastPeriod = int(reader.I32())
		step.celt48k.LastFrameType = int(reader.I32())
		step.celt48k.PLCFill = int(reader.I32())
		step.celt48k.PLCDuration = int(reader.I32())
		step.celt48k.SkipPLC = int(reader.I32())
		step.celt48k.PLCPreemphasisMem = reader.Float32()
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
		step.fargan.DeemphMem = reader.Float32()
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
		step.silk.LagPrev = int(reader.I32())
		step.silk.LastGainIndex = int(reader.I32())
		step.silk.LossCount = int(reader.I32())
		step.silk.PrevSignalType = int(reader.I32())
		for _, dst := range [][]float32{
			step.silk.SMid[:],
			step.silk.OutBuf[:],
			step.silk.SLPCQ14[:],
			step.silk.ExcQ14[:],
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
	if err := reader.ExpectConsumed(); err != nil {
		return libopusDecoderDREDSequenceInfo{}, err
	}
	return info, nil
}

func requireLibopusDREDSequenceParsed(t testing.TB, info libopusDecoderDREDSequenceInfo, label string) {
	t.Helper()
	if info.carrierParseRet <= 0 {
		t.Fatalf("%s libopus DRED carrier parse ret=%d want >0 (dredEnd=%d)", label, info.carrierParseRet, info.carrierDredEnd)
	}
	if info.carrierParseRet < info.carrierDredEnd {
		t.Fatalf("%s libopus DRED carrier parse ret=%d before dredEnd=%d", label, info.carrierParseRet, info.carrierDredEnd)
	}
}

func TestDecoderFirstLossNeuralConcealmentMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParity(t)

	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 0, 0, false)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "first-loss")
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
	assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.step0.silk, silkpkg.BandwidthWideband, "live 16k first-loss sequence oracle silk", 1e-4)
}

func TestDecoderSecondLossNeuralConcealmentMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParity(t)

	if _, err := dec.Decode(nil, pcm); err != nil {
		t.Fatalf("Decode(nil, first) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 1, 2*n, false)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "second-loss")
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
	assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.step1.silk, silkpkg.BandwidthWideband, "live 16k second-loss sequence oracle silk", 1e-4)
}

func TestDecoderFirstLossNeuralConcealment16kFrameSizeMatrixMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParityForFrameSize(t, frameSize)

			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 0, 0, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, fmt.Sprintf("first-loss carrier_%d", frameSize))
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
			assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.step0.silk, silkpkg.BandwidthWideband, "live 16k first-loss frame-size sequence oracle silk", celtTol)
		})
	}
}

func TestDecoderSecondLossNeuralConcealment16kFrameSizeMatrixMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParityForFrameSize(t, frameSize)

			if _, err := dec.Decode(nil, pcm); err != nil {
				t.Fatalf("Decode(nil, first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 1, n, 1, 2*n, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, fmt.Sprintf("second-loss carrier_%d", frameSize))
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
			assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.step1.silk, silkpkg.BandwidthWideband, "live 16k second-loss frame-size sequence oracle silk", celtTol)
		})
	}
}
