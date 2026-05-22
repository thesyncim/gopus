//go:build gopus_dred || gopus_extra_controls

package gopus

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
	silkpkg "github.com/thesyncim/gopus/silk"
)

const (
	libopusDecoderDREDSequenceInputMagic  = "GDSI"
	libopusDecoderDREDSequenceOutputMagic = "GDSO"

	libopusDecoderDREDSequenceSampleFormatFloat32 = uint32(0)
	libopusDecoderDREDSequenceSampleFormatInt16   = uint32(1)
)

type libopusDecoderDREDSequenceStepInfo struct {
	ret     int
	state   lpcnetplc.StateSnapshot
	fargan  lpcnetplc.FARGANSnapshot
	celt48k libopusDecoderDREDCELTSnapshot
	silk    libopusDecoderDREDSILKSnapshot
	pcm     []float32
	pcm16   []int16
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

var libopusDecoderDREDSequenceHelper libopustest.HelperCache

func getLibopusDecoderDREDSequenceHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(&libopusDecoderDREDSequenceHelper, "libopus_decoder_dred_sequence_info.c", "gopus_libopus_decoder_dred_sequence", true)
}

func probeLibopusDecoderDREDSequence(seedPacket, carrierPacket, nextPacket []byte, maxDREDSamples, sampleRate, frameSizeSamples, step0Source, step0OffsetSamples, step1Source, step1OffsetSamples int, decodeNextPacket bool) (libopusDecoderDREDSequenceInfo, error) {
	return probeLibopusDecoderDREDSequenceWithSampleFormat(seedPacket, carrierPacket, nextPacket, maxDREDSamples, sampleRate, frameSizeSamples, step0Source, step0OffsetSamples, step1Source, step1OffsetSamples, decodeNextPacket, libopusDecoderDREDSequenceSampleFormatFloat32)
}

func probeLibopusDecoderDREDSequenceInt16(seedPacket, carrierPacket, nextPacket []byte, maxDREDSamples, sampleRate, frameSizeSamples, step0Source, step0OffsetSamples, step1Source, step1OffsetSamples int, decodeNextPacket bool) (libopusDecoderDREDSequenceInfo, error) {
	return probeLibopusDecoderDREDSequenceWithSampleFormat(seedPacket, carrierPacket, nextPacket, maxDREDSamples, sampleRate, frameSizeSamples, step0Source, step0OffsetSamples, step1Source, step1OffsetSamples, decodeNextPacket, libopusDecoderDREDSequenceSampleFormatInt16)
}

func probeLibopusDecoderDREDSequenceWithSampleFormat(seedPacket, carrierPacket, nextPacket []byte, maxDREDSamples, sampleRate, frameSizeSamples, step0Source, step0OffsetSamples, step1Source, step1OffsetSamples int, decodeNextPacket bool, sampleFormat uint32) (libopusDecoderDREDSequenceInfo, error) {
	binPath, err := getLibopusDecoderDREDSequenceHelperPath()
	if err != nil {
		return libopusDecoderDREDSequenceInfo{}, err
	}
	if sampleFormat != libopusDecoderDREDSequenceSampleFormatFloat32 && sampleFormat != libopusDecoderDREDSequenceSampleFormatInt16 {
		return libopusDecoderDREDSequenceInfo{}, fmt.Errorf("invalid decoder DRED sequence sample format %d", sampleFormat)
	}
	decoderModelBlob, err := probeLibopusDecoderNeuralModelBlob()
	if err != nil {
		return libopusDecoderDREDSequenceInfo{}, err
	}
	dredModelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		return libopusDecoderDREDSequenceInfo{}, err
	}

	payloadVersion := uint32(1)
	if sampleFormat != libopusDecoderDREDSequenceSampleFormatFloat32 {
		payloadVersion = 2
	}
	payload := libopustest.NewOraclePayloadVersion(libopusDecoderDREDSequenceInputMagic, payloadVersion,
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
	if payloadVersion >= 2 {
		payload.U32(sampleFormat)
	}
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
	readPCM16 := func(ret int) ([]int16, error) {
		if ret <= 0 || info.channels <= 0 {
			return nil, nil
		}
		dst := make([]int16, ret*info.channels)
		for i := range dst {
			dst[i] = reader.I16()
		}
		if err := reader.Err(); err != nil {
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

	if sampleFormat == libopusDecoderDREDSequenceSampleFormatInt16 {
		if info.step0.pcm16, err = readPCM16(info.step0.ret); err != nil {
			return libopusDecoderDREDSequenceInfo{}, err
		}
		if info.step1.pcm16, err = readPCM16(info.step1.ret); err != nil {
			return libopusDecoderDREDSequenceInfo{}, err
		}
		if info.next.pcm16, err = readPCM16(info.next.ret); err != nil {
			return libopusDecoderDREDSequenceInfo{}, err
		}
	} else {
		if info.step0.pcm, err = readPCM(info.step0.ret); err != nil {
			return libopusDecoderDREDSequenceInfo{}, err
		}
		if info.step1.pcm, err = readPCM(info.step1.ret); err != nil {
			return libopusDecoderDREDSequenceInfo{}, err
		}
		if info.next.pcm, err = readPCM(info.next.ret); err != nil {
			return libopusDecoderDREDSequenceInfo{}, err
		}
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

func prepareCachedDREDDecodeInt16ParityStateForDecoderRateAndPacketWithChannels(t *testing.T, decoderSampleRate int, packetInfo libopusDREDPacket, wantChannels int) (*Decoder, int) {
	t.Helper()

	modelBlob, err := probeLibopusDREDModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "dred model", err)
	}
	toc := ParseTOC(packetInfo.packet[0])
	channels := 1
	if toc.Stereo {
		channels = 2
	}
	if wantChannels > 0 && channels != wantChannels {
		t.Skipf("cached DRED int16 parity requires %d-channel packet, got sampleRate=%d channels=%d", wantChannels, packetInfo.sampleRate, channels)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(decoderSampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	setDecoderComplexityForLibopusDREDParityTest(t, dec)
	if err := dec.SetDNNBlob(requireLibopusDecoderNeuralModelBlob(t)); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	setDREDDecoderBlobFromBytesForTest(t, dec, modelBlob)

	pcm := make([]int16, dec.maxPacketSamples*channels)
	n, err := dec.DecodeInt16(packetInfo.packet, pcm)
	if err != nil {
		t.Fatalf("DecodeInt16(DRED packet) error: %v", err)
	}
	if n <= 0 {
		t.Fatal("DecodeInt16(DRED packet) returned no audio")
	}
	if state := requireDecoderDREDState(t, dec); state.dredCache.Empty() || state.dredDecoded.NbLatents <= 0 {
		t.Fatal("DecodeInt16(DRED packet) did not retain processed DRED state")
	}
	return dec, n
}

func assertDecoderCachedDREDDecodeInt16LossesMatchLiveSequenceOracle(t *testing.T, label string, decoderSampleRate int, packetInfo libopusDREDPacket, wantChannels, maxDiff int) {
	t.Helper()

	dec, n := prepareCachedDREDDecodeInt16ParityStateForDecoderRateAndPacketWithChannels(t, decoderSampleRate, packetInfo, wantChannels)
	wantFrame, err := packetSamplesAtRate(packetInfo.packet, decoderSampleRate)
	if err != nil {
		t.Fatalf("%s packetSamplesAtRate: %v", label, err)
	}
	if n != wantFrame {
		t.Fatalf("%s warmup samples=%d want %d at %d Hz", label, n, wantFrame, decoderSampleRate)
	}

	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, decoderSampleRate)
	want, err := probeLibopusDecoderDREDSequenceInt16(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, 1, n, 1, 2*n, false)
	if err != nil {
		libopustest.HelperUnavailable(t, label+" decoder DRED int16 sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, label+" int16 cached losses")
	if want.channels != wantChannels {
		t.Fatalf("%s libopus channels=%d want %d", label, want.channels, wantChannels)
	}
	if want.step0.ret != n || want.step1.ret != n {
		t.Fatalf("%s libopus cached int16 ret=(%d,%d) want (%d,%d)", label, want.step0.ret, want.step1.ret, n, n)
	}

	pcm0 := make([]int16, n*dec.channels)
	got0, err := dec.DecodeInt16(nil, pcm0)
	if err != nil {
		t.Fatalf("%s DecodeInt16(nil, first) error: %v", label, err)
	}
	if got0 != n {
		t.Fatalf("%s DecodeInt16(nil, first)=%d want %d", label, got0, n)
	}
	assertInt16WithinLSB(t, pcm0[:got0*dec.channels], want.step0.pcm16[:got0*dec.channels], maxDiff, label+" cached first-loss int16")

	pcm1 := make([]int16, n*dec.channels)
	got1, err := dec.DecodeInt16(nil, pcm1)
	if err != nil {
		t.Fatalf("%s DecodeInt16(nil, second) error: %v", label, err)
	}
	if got1 != n {
		t.Fatalf("%s DecodeInt16(nil, second)=%d want %d", label, got1, n)
	}
	assertInt16WithinLSB(t, pcm1[:got1*dec.channels], want.step1.pcm16[:got1*dec.channels], maxDiff, label+" cached second-loss int16")
}

func assertInt16WithinLSB(t *testing.T, got, want []int16, maxDiff int, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	worstIdx := -1
	worstDiff := 0
	for i := range got {
		diff := int(got[i]) - int(want[i])
		if diff < 0 {
			diff = -diff
		}
		if diff > worstDiff {
			worstIdx = i
			worstDiff = diff
		}
	}
	if worstDiff > maxDiff {
		t.Fatalf("%s[%d]=%d want %d (max diff=%d > %d)", label, worstIdx, got[worstIdx], want[worstIdx], worstDiff, maxDiff)
	}
}

func TestDecoderCachedSILKDREDDecodeInt16MatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 960
	for _, channels := range []int{1, 2} {
		channels := channels
		packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
			FrameSize:     frameSize,
			ForceMode:     ModeSILK,
			Bandwidth:     BandwidthWideband,
			Channels:      channels,
			ForceChannels: channels,
		})
		if err != nil {
			libopustest.HelperUnavailable(t, "SILK DRED int16 packet", err)
		}
		toc := ParseTOC(packetInfo.packet[0])
		if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband || toc.Stereo != (channels == 2) {
			t.Fatalf("SILK DRED int16 packet TOC=%+v, want channels=%d SILK WB", toc, channels)
		}
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			sampleRate := sampleRate
			t.Run(fmt.Sprintf("channels_%d_decoder_%d", channels, sampleRate), func(t *testing.T) {
				label := fmt.Sprintf("cached SILK int16 channels=%d decoder=%d", channels, sampleRate)
				assertDecoderCachedDREDDecodeInt16LossesMatchLiveSequenceOracle(t, label, sampleRate, packetInfo, channels, 1)
			})
		}
	}
}

func TestDecoderCachedSILKDREDDecodeInt16RequestedPLCDurationMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 960
	for _, channels := range []int{1} {
		channels := channels
		packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
			FrameSize:     frameSize,
			ForceMode:     ModeSILK,
			Bandwidth:     BandwidthWideband,
			Channels:      channels,
			ForceChannels: channels,
		})
		if err != nil {
			libopustest.HelperUnavailable(t, "SILK DRED int16 requested packet", err)
		}
		toc := ParseTOC(packetInfo.packet[0])
		if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband || toc.Stereo != (channels == 2) {
			t.Fatalf("SILK DRED int16 requested packet TOC=%+v, want channels=%d SILK WB", toc, channels)
		}
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			sampleRate := sampleRate
			for _, requested := range []int{sampleRate / 25, sampleRate * 3 / 50} {
				requested := requested
				t.Run(fmt.Sprintf("channels_%d_decoder_%d_request_%d", channels, sampleRate, requested), func(t *testing.T) {
					dec, n := prepareCachedDREDDecodeInt16ParityStateForDecoderRateAndPacketWithChannels(t, sampleRate, packetInfo, channels)
					packetFrame, err := packetSamplesAtRate(packetInfo.packet, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}
					if n != packetFrame {
						t.Fatalf("cached SILK int16 requested warmup samples=%d want %d at %d Hz", n, packetFrame, sampleRate)
					}

					maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, sampleRate)
					want, err := probeLibopusDecoderDREDSequenceInt16(nil, packetInfo.packet, nil, maxDRED, oracleRate, requested, 1, requested, 1, 2*requested, false)
					if err != nil {
						libopustest.HelperUnavailable(t, "cached SILK int16 requested decoder DRED sequence", err)
					}
					requireLibopusDREDSequenceParsed(t, want, "cached SILK int16 requested losses")
					if want.channels != channels {
						t.Fatalf("libopus cached SILK int16 requested channels=%d want %d", want.channels, channels)
					}
					if want.step0.ret != requested || want.step1.ret != requested {
						t.Fatalf("libopus cached SILK int16 requested ret=(%d,%d) want (%d,%d)", want.step0.ret, want.step1.ret, requested, requested)
					}

					pcm0 := make([]int16, requested*dec.channels)
					got0, err := dec.DecodeInt16(nil, pcm0)
					if err != nil {
						t.Fatalf("DecodeInt16(nil, requested first) error: %v", err)
					}
					if got0 != requested {
						t.Fatalf("DecodeInt16(nil, requested first)=%d want %d", got0, requested)
					}
					assertInt16WithinLSB(t, pcm0[:got0*dec.channels], want.step0.pcm16[:got0*dec.channels], 1, "cached SILK int16 requested first-loss")

					pcm1 := make([]int16, requested*dec.channels)
					got1, err := dec.DecodeInt16(nil, pcm1)
					if err != nil {
						t.Fatalf("DecodeInt16(nil, requested second) error: %v", err)
					}
					if got1 != requested {
						t.Fatalf("DecodeInt16(nil, requested second)=%d want %d", got1, requested)
					}
					assertInt16WithinLSB(t, pcm1[:got1*dec.channels], want.step1.pcm16[:got1*dec.channels], 1, "cached SILK int16 requested second-loss")
				})
			}
		}
	}
}

func TestDecoderCachedCELTDREDDecodeInt16TracksLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 960
	for _, channels := range []int{1, 2} {
		channels := channels
		packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
			FrameSize:     frameSize,
			ForceMode:     ModeCELT,
			Bandwidth:     BandwidthFullband,
			Channels:      channels,
			ForceChannels: channels,
		})
		if err != nil {
			libopustest.HelperUnavailable(t, "CELT DRED int16 packet", err)
		}
		toc := ParseTOC(packetInfo.packet[0])
		if toc.Mode != ModeCELT || toc.Bandwidth != BandwidthFullband || toc.Stereo != (channels == 2) {
			t.Fatalf("CELT DRED int16 packet TOC=%+v, want channels=%d CELT FB", toc, channels)
		}
		t.Run(fmt.Sprintf("channels_%d_decoder_48000", channels), func(t *testing.T) {
			label := fmt.Sprintf("cached CELT int16 channels=%d decoder=48000", channels)
			assertDecoderCachedDREDDecodeInt16LossesMatchLiveSequenceOracle(t, label, 48000, packetInfo, channels, 192)
		})
	}
}

func TestDecoderFirstLossNeuralConcealmentMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParity(t)

	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, dec.SampleRate())
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, 1, n, 0, 0, false)
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

	frameSize48 := n * 48000 / dec.SampleRate()
	pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize48)
	assertFloat32ApproxEqual(t, pcm[:n], want.step0.pcm[:n], "concealed pcm live-sequence oracle", pcmTol)
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "live 16k first-loss sequence oracle plc", plcTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "live 16k first-loss sequence oracle fargan", farganTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, "live 16k first-loss sequence oracle celt", celtTol)
	assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.step0.silk, silkpkg.BandwidthWideband, "live 16k first-loss sequence oracle silk", celtTol)
}

func TestDecoderSecondLossNeuralConcealmentMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParity(t)

	if _, err := dec.Decode(nil, pcm); err != nil {
		t.Fatalf("Decode(nil, first) error: %v", err)
	}

	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, dec.SampleRate())
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, 1, n, 1, 2*n, false)
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

	frameSize48 := n * 48000 / dec.SampleRate()
	pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize48)
	assertFloat32ApproxEqual(t, pcm[:n], want.step1.pcm[:n], "second concealed pcm live-sequence oracle", pcmTol)
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step1.state, "live 16k second-loss sequence oracle plc", plcTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step1.fargan, "live 16k second-loss sequence oracle fargan", farganTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step1.celt48k, "live 16k second-loss sequence oracle celt", celtTol)
	assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.step1.silk, silkpkg.BandwidthWideband, "live 16k second-loss sequence oracle silk", celtTol)
}

func TestDecoderFirstLossNeuralConcealment16kFrameSizeMatrixMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, pcm, packetInfo, n := prepareDecoderForNeuralConcealmentParityForFrameSize(t, frameSize)

			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, dec.SampleRate())
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, 1, n, 0, 0, false)
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

			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, dec.SampleRate())
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, 1, n, 1, 2*n, false)
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
