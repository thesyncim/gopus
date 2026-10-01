//go:build gopus_dred || gopus_osce

package gopus

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
	silkpkg "github.com/thesyncim/gopus/internal/silk"
)

func newDREDHistoryDecoder(t *testing.T, complexity int, loadModel bool) *Decoder {
	t.Helper()
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if err := dec.SetComplexity(complexity); err != nil {
		t.Fatalf("SetComplexity(%d): %v", complexity, err)
	}
	if loadModel {
		prepareDREDHistoryOSCEModel(t, dec)
		if err := dec.SetDNNBlob(dredHistoryDecoderModelBlob(t)); err != nil {
			t.Fatalf("SetDNNBlob: %v", err)
		}
	}
	return dec
}

func probeDREDHistoryCarrier(t *testing.T, seedPacket []byte, packetInfo libopusDREDPacket, options libopusDecoderDREDSequenceOptions) libopusDecoderDREDSequenceInfo {
	t.Helper()
	n := ParseTOC(packetInfo.packet[0]).FrameSize
	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, 48000)
	want, err := probeLibopusDecoderDREDSequenceWithOptions(
		seedPacket, packetInfo.packet, nil,
		maxDRED, oracleRate, n,
		libopusDecoderDREDSequenceSourceCarrierDRED, n,
		libopusDecoderDREDSequenceSourceNone, 0, false,
		libopusDecoderDREDSequenceSampleFormatInt24,
		libopusDecoderDREDSequenceSampleFormatFloat32,
		libopusDecoderDREDSequenceSampleFormatFloat32,
		true, options,
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED history sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "DRED history sequence")
	if want.carrierRet != n || want.step0.ret != n || want.channels != 1 {
		t.Fatalf("DRED history C returns carrier/recovery/channels=%d/%d/%d want %d/%d/1", want.carrierRet, want.step0.ret, want.channels, n, n)
	}
	return want
}

func assertDREDHistoryInt24MatchesC(t *testing.T, dec *Decoder, dred *DRED, packetInfo libopusDREDPacket, want libopusDecoderDREDSequenceInfo, label string) {
	t.Helper()
	n := ParseTOC(packetInfo.packet[0]).FrameSize
	pcm := make([]int32, n)
	got, err := dec.DecodeDREDInt24(dred, n, pcm, n)
	if err != nil || got != want.step0.ret {
		t.Fatalf("%s DecodeDREDInt24=(%d,%v) C=%d", label, got, err, want.step0.ret)
	}
	state := requireDecoderDREDState(t, dec)
	assertDecoderDREDPLCStateBitsMatch(t, state.dredPLC.Snapshot(), want.step0.state, label+" PLC")
	assertDecoderDREDFARGANStateBitsMatch(t, state.dredFARGAN.Snapshot(), want.step0.fargan, label+" FARGAN")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, label+" CELT")
	if got := dec.FinalRange(); got != want.step0.finalRange {
		t.Fatalf("%s final range=%08x C=%08x", label, got, want.step0.finalRange)
	}
	assertDecoderDREDSILKStateBitsMatch(t, dec, want.step0.silk, silkpkg.BandwidthWideband, label+" SILK")
	assertInt24ParitySelectedCExact(t, pcm[:got], want.step0.pcm24, 48000, 1, label+" PCM")
}

func TestDREDHistoryWrapResetAndLateModelLoadMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960, ForceMode: ModeSILK, Bandwidth: BandwidthWideband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "SILK DRED carrier", err)
	}
	seedPacket := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, 960, BandwidthWideband)
	seedRepeats := 8
	carrierSamples := ParseTOC(packetInfo.packet[0]).FrameSize
	wrapPos := (seedRepeats*320 + carrierSamples*16000/48000) % lpcnetplc.PLCBufSize
	for _, tc := range []struct {
		name               string
		resetAfterSeed     bool
		loadModelAfterSeed bool
		wantHistoryFill    int
		wantHistoryPos     int
	}{
		{name: "ring_wrap", wantHistoryFill: lpcnetplc.PLCBufSize, wantHistoryPos: wrapPos},
		{name: "reset", resetAfterSeed: true, wantHistoryFill: 320, wantHistoryPos: 320},
		{name: "model_loaded_after_seed", loadModelAfterSeed: true, wantHistoryFill: lpcnetplc.PLCBufSize, wantHistoryPos: wrapPos},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := libopusDecoderDREDSequenceOptions{
				seedRepeats:        seedRepeats,
				complexity:         10,
				resetAfterSeed:     tc.resetAfterSeed,
				loadModelAfterSeed: tc.loadModelAfterSeed,
			}
			if tc.loadModelAfterSeed {
				options.useWeightsFile = true
				options.decoderModelBlob = dredHistoryDecoderModelBlob(t)
			}
			want := probeDREDHistoryCarrier(t, seedPacket, packetInfo, options)
			dec := newDREDHistoryDecoder(t, 10, !tc.loadModelAfterSeed)
			seedPCM := make([]float32, dec.maxPacketSamples)
			for i := 0; i < seedRepeats; i++ {
				if got, err := dec.Decode(seedPacket, seedPCM); err != nil || got != 960 {
					t.Fatalf("Decode(seed %d)=(%d,%v) want (960,nil)", i, got, err)
				}
			}
			if tc.loadModelAfterSeed {
				if err := dec.SetDNNBlob(dredHistoryDecoderModelBlob(t)); err != nil {
					t.Fatalf("SetDNNBlob after seed: %v", err)
				}
			}
			if tc.resetAfterSeed {
				dec.Reset()
				if dec.rawSILKHistoryFill != 0 || dec.rawSILKHistoryPos != 0 || dec.pcmHistorySynced {
					t.Fatalf("Reset left retained history fill=%d pos=%d synced=%v", dec.rawSILKHistoryFill, dec.rawSILKHistoryPos, dec.pcmHistorySynced)
				}
			}
			carrierPCM := make([]float32, dec.maxPacketSamples)
			if got, err := dec.Decode(packetInfo.packet, carrierPCM); err != nil || got != carrierSamples {
				t.Fatalf("Decode(carrier)=(%d,%v) want (%d,nil)", got, err, carrierSamples)
			}
			if tc.loadModelAfterSeed {
				maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, 48000)
				postCarrier, err := probeLibopusDecoderDREDSequenceWithOptions(
					seedPacket, packetInfo.packet, nil,
					maxDRED, oracleRate, carrierSamples,
					libopusDecoderDREDSequenceSourceNone, 0,
					libopusDecoderDREDSequenceSourceNone, 0, false,
					libopusDecoderDREDSequenceSampleFormatFloat32,
					libopusDecoderDREDSequenceSampleFormatFloat32,
					libopusDecoderDREDSequenceSampleFormatFloat32,
					true, libopusDecoderDREDSequenceOptions{
						seedRepeats:        seedRepeats,
						complexity:         options.complexity,
						loadModelAfterSeed: true,
						useWeightsFile:     true,
						decoderModelBlob:   options.decoderModelBlob,
					},
				)
				if err != nil {
					t.Fatalf("post-carrier C probe: %v", err)
				}
				requireLibopusDREDSequenceParsed(t, postCarrier, "late-model post-carrier history")
				if !dec.ensureDREDNeuralConcealmentRuntime() || !dec.refreshDREDHistoryFromSILKDecoder() {
					t.Fatal("failed to replay retained SILK history after late model load")
				}
				state := requireDecoderDREDState(t, dec)
				plcSnapshot := state.dredPLC.Snapshot()
				assertDecoderDREDPLCStateBitsMatch(t, plcSnapshot, postCarrier.step0.state, "late-model post-carrier PLC")
			}
			if dec.rawSILKHistoryFill != tc.wantHistoryFill || dec.rawSILKHistoryPos != tc.wantHistoryPos {
				t.Fatalf("retained history fill/pos=%d/%d want %d/%d", dec.rawSILKHistoryFill, dec.rawSILKHistoryPos, tc.wantHistoryFill, tc.wantHistoryPos)
			}
			dred := parseCarrierDREDForExplicitDecode(t, 48000, packetInfo)
			assertDREDHistoryInt24MatchesC(t, dec, dred, packetInfo, want, tc.name)
		})
	}
}

func TestDREDHistoryModeTransitionsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name        string
		seedMode    Mode
		seedBW      Bandwidth
		carrierMode Mode
		carrierBW   Bandwidth
	}{
		{name: "silk_to_celt", seedMode: ModeSILK, seedBW: BandwidthWideband, carrierMode: ModeCELT, carrierBW: BandwidthFullband},
		{name: "celt_to_silk", seedMode: ModeCELT, seedBW: BandwidthFullband, carrierMode: ModeSILK, carrierBW: BandwidthWideband},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: 960, ForceMode: tc.carrierMode, Bandwidth: tc.carrierBW,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, tc.name+" DRED carrier", err)
			}
			seedPacket := makeValidMonoPacketForModeBandwidthFrameSizeForDREDTest(t, tc.seedMode, tc.seedBW, 960)
			want := probeDREDHistoryCarrier(t, seedPacket, packetInfo, libopusDecoderDREDSequenceOptions{seedRepeats: 1, complexity: 10})
			dec := newDREDHistoryDecoder(t, 10, true)
			pcm := make([]float32, dec.maxPacketSamples)
			if got, err := dec.Decode(seedPacket, pcm); err != nil || got != 960 {
				t.Fatalf("Decode(seed)=(%d,%v) want (960,nil)", got, err)
			}
			if got, err := dec.Decode(packetInfo.packet, pcm); err != nil || got != 960 {
				t.Fatalf("Decode(carrier)=(%d,%v) want (960,nil)", got, err)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, 48000)
			postCarrier, err := probeLibopusDecoderDREDSequenceWithOptions(
				seedPacket, packetInfo.packet, nil,
				maxDRED, oracleRate, 960,
				libopusDecoderDREDSequenceSourceNone, 0,
				libopusDecoderDREDSequenceSourceNone, 0, false,
				libopusDecoderDREDSequenceSampleFormatFloat32,
				libopusDecoderDREDSequenceSampleFormatFloat32,
				libopusDecoderDREDSequenceSampleFormatFloat32,
				true, libopusDecoderDREDSequenceOptions{seedRepeats: 1, complexity: 10},
			)
			if err != nil {
				t.Fatalf("post-carrier C probe: %v", err)
			}
			requireLibopusDREDSequenceParsed(t, postCarrier, "post-carrier DRED history")
			if postCarrier.carrierRet != 960 {
				t.Fatalf("post-carrier C return=%d want 960", postCarrier.carrierRet)
			}
			state := requireDecoderDREDState(t, dec)
			assertDecoderDREDPLCStateBitsMatch(t, state.dredPLC.Snapshot(), postCarrier.step0.state, tc.name+" post-carrier PLC")
			assertDecoderDREDFARGANStateBitsMatch(t, state.dredFARGAN.Snapshot(), postCarrier.step0.fargan, tc.name+" post-carrier FARGAN")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, postCarrier.step0.celt48k, tc.name+" post-carrier CELT")
			assertDecoderDREDSILKStateBitsMatch(t, dec, postCarrier.step0.silk, silkpkg.BandwidthWideband, tc.name+" post-carrier SILK")
			if got := dec.FinalRange(); got != postCarrier.step0.finalRange {
				t.Fatalf("%s post-carrier final range=%08x C=%08x", tc.name, got, postCarrier.step0.finalRange)
			}
			dred := parseCarrierDREDForExplicitDecode(t, 48000, packetInfo)
			assertDREDHistoryInt24MatchesC(t, dec, dred, packetInfo, want, fmt.Sprintf("%s explicit DRED", tc.name))
		})
	}
}

func TestDREDHistoryClassicalLossCapturesPreCNGPCMMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960, ForceMode: ModeSILK, Bandwidth: BandwidthWideband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "SILK DRED carrier", err)
	}
	seedPacket := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, 960, BandwidthWideband)
	n := ParseTOC(packetInfo.packet[0]).FrameSize
	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, 48000)
	want, err := probeLibopusDecoderDREDSequenceWithOptions(
		seedPacket, packetInfo.packet, nil,
		maxDRED, oracleRate, n,
		libopusDecoderDREDSequenceSourceLost, n,
		libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, false,
		libopusDecoderDREDSequenceSampleFormatFloat32,
		libopusDecoderDREDSequenceSampleFormatFloat32,
		libopusDecoderDREDSequenceSampleFormatFloat32,
		true,
		libopusDecoderDREDSequenceOptions{seedRepeats: 1, complexity: 0},
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "classical SILK loss sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "classical SILK loss sequence")
	if want.carrierRet != n || want.step0.ret != n || want.step1.ret != n {
		t.Fatalf("classical C carrier/loss/recovery=%d/%d/%d want %d/%d/%d", want.carrierRet, want.step0.ret, want.step1.ret, n, n, n)
	}

	dec := newDREDHistoryDecoder(t, 0, true)
	pcm := make([]float32, dec.maxPacketSamples)
	if got, err := dec.Decode(seedPacket, pcm); err != nil || got != n {
		t.Fatalf("Decode(seed)=(%d,%v) want (%d,nil)", got, err, n)
	}
	if got, err := dec.Decode(packetInfo.packet, pcm); err != nil || got != n {
		t.Fatalf("Decode(carrier)=(%d,%v) want (%d,nil)", got, err, n)
	}
	lostPCM := make([]float32, n)
	if got, err := dec.Decode(nil, lostPCM); err != nil || got != n {
		t.Fatalf("Decode(nil)=(%d,%v) want (%d,nil)", got, err, n)
	}
	assertFloat32BitsEqual(t, lostPCM, want.step0.pcm, "classical pre-CNG loss PCM")
	if dec.rawSILKHistoryFill != 3*320 {
		t.Fatalf("raw SILK history after seed/carrier/loss=%d want %d", dec.rawSILKHistoryFill, 3*320)
	}
	dred := parseCarrierDREDForExplicitDecode(t, 48000, packetInfo)
	recoveryPCM := make([]float32, n)
	if got, err := dec.DecodeDRED(dred, 2*n, recoveryPCM, n); err != nil || got != want.step1.ret {
		t.Fatalf("DecodeDRED after classical loss=(%d,%v) C=%d", got, err, want.step1.ret)
	}
	assertFloat32BitsEqual(t, recoveryPCM, want.step1.pcm, "classical loss DRED recovery PCM")
	state := requireDecoderDREDState(t, dec)
	assertDecoderDREDPLCStateBitsMatch(t, state.dredPLC.Snapshot(), want.step1.state, "classical loss DRED PLC")
	assertDecoderDREDFARGANStateBitsMatch(t, state.dredFARGAN.Snapshot(), want.step1.fargan, "classical loss DRED FARGAN")
	assertDecoderDREDSILKStateBitsMatch(t, dec, want.step1.silk, silkpkg.BandwidthWideband, "classical loss DRED SILK")
	if got := dec.FinalRange(); got != want.step1.finalRange {
		t.Fatalf("classical loss DRED final range=%08x C=%08x", got, want.step1.finalRange)
	}
}

func TestDREDHistoryUnloadedClassicalLossIsExcludedAfterLateModelLoadMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960, ForceMode: ModeSILK, Bandwidth: BandwidthWideband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "SILK DRED carrier", err)
	}
	seedPacket := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, 960, BandwidthWideband)
	n := ParseTOC(packetInfo.packet[0]).FrameSize
	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, 48000)
	options := libopusDecoderDREDSequenceOptions{
		seedRepeats:         1,
		complexity:          10,
		loadModelAfterSeed:  true,
		useWeightsFile:      true,
		lossBeforeModelLoad: true,
		decoderModelBlob:    dredHistoryDecoderModelBlob(t),
	}
	want, err := probeLibopusDecoderDREDSequenceWithOptions(
		seedPacket, packetInfo.packet, nil,
		maxDRED, oracleRate, n,
		libopusDecoderDREDSequenceSourceLost, n,
		libopusDecoderDREDSequenceSourceNone, 0, false,
		libopusDecoderDREDSequenceSampleFormatFloat32,
		libopusDecoderDREDSequenceSampleFormatFloat32,
		libopusDecoderDREDSequenceSampleFormatFloat32,
		true, options,
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "pre-load classical loss sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "pre-load classical loss sequence")
	if want.carrierRet != n || want.step0.ret != n {
		t.Fatalf("dynamic C carrier/neural-loss returns %d/%d want %d/%d", want.carrierRet, want.step0.ret, n, n)
	}

	dec := newDREDHistoryDecoder(t, 10, false)
	pcm := make([]float32, dec.maxPacketSamples)
	if got, err := dec.Decode(seedPacket, pcm); err != nil || got != n {
		t.Fatalf("Decode(seed)=(%d,%v) want (%d,nil)", got, err, n)
	}
	unloadedLoss := make([]float32, n)
	if got, err := dec.Decode(nil, unloadedLoss); err != nil || got != n {
		t.Fatalf("Decode(nil before model)=(%d,%v) want (%d,nil)", got, err, n)
	}
	if dec.rawSILKHistoryFill != 320 {
		t.Fatalf("pre-model classical loss entered retained history: fill=%d want seed-only 320", dec.rawSILKHistoryFill)
	}
	if err := dec.SetDNNBlob(dredHistoryDecoderModelBlob(t)); err != nil {
		t.Fatalf("SetDNNBlob after classical loss: %v", err)
	}
	if got, err := dec.Decode(packetInfo.packet, pcm); err != nil || got != n {
		t.Fatalf("Decode(carrier)=(%d,%v) want (%d,nil)", got, err, n)
	}
	neuralLoss := make([]float32, n)
	if got, err := dec.Decode(nil, neuralLoss); err != nil || got != want.step0.ret {
		t.Fatalf("Decode(nil after model)=(%d,%v) C=%d", got, err, want.step0.ret)
	}
	assertFloat32BitsEqual(t, neuralLoss, want.step0.pcm, "late-model neural loss PCM")
	state := requireDecoderDREDState(t, dec)
	assertDecoderDREDPLCStateBitsMatch(t, state.dredPLC.Snapshot(), want.step0.state, "late-model neural loss PLC")
	assertDecoderDREDFARGANStateBitsMatch(t, state.dredFARGAN.Snapshot(), want.step0.fargan, "late-model neural loss FARGAN")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, "late-model neural loss CELT")
	assertDecoderDREDSILKStateBitsMatch(t, dec, want.step0.silk, silkpkg.BandwidthWideband, "late-model neural loss SILK")
	if got := dec.FinalRange(); got != want.step0.finalRange {
		t.Fatalf("late-model neural loss final range=%08x C=%08x", got, want.step0.finalRange)
	}
}
