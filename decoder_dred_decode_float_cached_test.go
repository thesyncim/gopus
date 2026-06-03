//go:build gopus_dred || gopus_extra_controls

package gopus

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

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

func TestDecoderCachedCELTDREDAPIRateMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}
	toc := ParseTOC(packetInfo.packet[0])
	if toc.Mode != ModeCELT || toc.Bandwidth != BandwidthFullband {
		t.Fatalf("cached CELT DRED API-rate packet TOC=%+v, want CELT FB", toc)
	}

	for _, sampleRate := range []int{8000, 12000, 24000} {
		sampleRate := sampleRate
		t.Run(fmt.Sprintf("decoder_%d", sampleRate), func(t *testing.T) {
			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, sampleRate, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("cached CELT warmup samples=%d want %d at %d Hz", n, wantFrame, sampleRate)
			}

			dred := parseCarrierDREDForExplicitDecode(t, sampleRate, packetInfo)
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, sampleRate)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "cached CELT API-rate first-loss")
			if want.step0.ret != n {
				t.Fatalf("libopus cached CELT API-rate first-loss ret=%d want %d", want.step0.ret, n)
			}

			// Explicit DRED-decode path (SourceCarrierDRED oracle): a public
			// Decode(nil) runs plain PLC and consumes no cached DRED
			// (opus_decoder.c:736); recovery is verified through the explicit
			// entry point per cc04ecf0's reconciliation.
			pcm := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}

			_, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(packetInfo.sampleRate / 50)
			assertDecodedPCMQuality(t, pcm[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], sampleRate, dec.Channels(), "cached CELT API-rate live-sequence first-loss pcm")
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "cached CELT API-rate live-sequence first-loss plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "cached CELT API-rate live-sequence first-loss fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, "cached CELT API-rate live-sequence first-loss celt", celtTol)
		})
	}
}

func TestDecoderCachedStereoCELTDREDAPIRateMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     960,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo CELT DRED API-rate packet", err)
	}
	toc := ParseTOC(packetInfo.packet[0])
	if toc.Mode != ModeCELT || toc.Bandwidth != BandwidthFullband || !toc.Stereo {
		t.Fatalf("cached stereo CELT DRED API-rate packet TOC=%+v, want stereo CELT FB", toc)
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
				t.Fatalf("cached stereo CELT warmup samples=%d want %d at %d Hz", n, wantFrame, sampleRate)
			}

			dred := parseCarrierDREDForExplicitDecode(t, sampleRate, packetInfo)
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, sampleRate)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "stereo CELT decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "cached stereo CELT API-rate first-loss")
			if want.channels != 2 {
				t.Fatalf("libopus cached stereo CELT DRED channels=%d want 2", want.channels)
			}
			if want.step0.ret != n {
				t.Fatalf("libopus cached stereo CELT API-rate first-loss ret=%d want %d", want.step0.ret, n)
			}

			// Explicit DRED-decode path (SourceCarrierDRED oracle, opus_decoder.c:736).
			pcm := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}

			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(packetInfo.sampleRate / 50)
			if sampleRate == 8000 {
				pcmTol = max(pcmTol, 1.5e-2)
				plcTol = max(plcTol, 2e-2)
				farganTol = max(farganTol, 3e-1)
				celtTol = max(celtTol, 1.5e-2)
			}
			assertDecodedPCMQuality(t, pcm[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], sampleRate, dec.Channels(), "cached stereo CELT API-rate live-sequence first-loss pcm")
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "cached stereo CELT API-rate live-sequence first-loss plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "cached stereo CELT API-rate live-sequence first-loss fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, "cached stereo CELT API-rate live-sequence first-loss celt", celtTol)
		})
	}
}

func TestDecoderCachedCELTDREDRequestedPLCDurationMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "dred packet", err)
	}

	for _, sampleRate := range []int{8000, 48000} {
		sampleRate := sampleRate
		t.Run(fmt.Sprintf("decoder_%d", sampleRate), func(t *testing.T) {
			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, sampleRate, packetInfo)
			dred := parseCarrierDREDForExplicitDecode(t, sampleRate, packetInfo)
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, sampleRate)
			// DRED recovery is requested through the explicit DRED-decode path
			// (SourceCarrierDRED oracle = opus_decoder_dred_decode_float,
			// opus_decoder.c:1625). The carrier DRED covers one frame, so the
			// requested recovery duration is the carrier frame. A public
			// Decode(nil) of a longer duration runs PLAIN PLC and consumes no
			// cached DRED (opus_decoder.c:736 gates the FEC feed on dred!=NULL);
			// that invariant is asserted separately below.
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED requested-duration sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "cached CELT requested-duration first-loss")
			if want.step0.ret != n {
				t.Fatalf("libopus cached CELT requested-duration first-loss ret=%d want %d", want.step0.ret, n)
			}

			pcm := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}

			pcmTol, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(packetInfo.sampleRate / 50)
			pcmTol = max(pcmTol, 4e-2)
			plcTol = max(plcTol, 8e-3)
			farganTol = max(farganTol, 1.1e-1)
			celtTol = max(celtTol, 8e-3)
			assertDecodedPCMQuality(t, pcm[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], sampleRate, dec.Channels(), "cached CELT requested-duration live-sequence first-loss pcm")
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "cached CELT requested-duration live-sequence first-loss plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "cached CELT requested-duration live-sequence first-loss fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, "cached CELT requested-duration live-sequence first-loss celt", celtTol)

			// Verify a public packet-loss Decode(nil) over a longer requested
			// duration applies no cached DRED (plain PLC), matching
			// opus_decode(NULL,...) with dred==NULL (opus_decoder.c:736).
			lossDec, lossN := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, sampleRate, packetInfo)
			lossPCM := make([]float32, 2*lossN*lossDec.Channels())
			gotLoss, err := lossDec.Decode(nil, lossPCM)
			if err != nil {
				t.Fatalf("Decode(nil) error: %v", err)
			}
			if gotLoss != 2*lossN {
				t.Fatalf("Decode(nil)=%d want %d", gotLoss, 2*lossN)
			}
			lossState := requireDecoderDREDState(t, lossDec)
			if lossState.dredRecovery != 0 {
				t.Fatalf("public lost dredRecovery=%d want 0", lossState.dredRecovery)
			}
			if fill := lossState.dredPLC.FECFillPos(); fill != 0 {
				t.Fatalf("public lost FECFillPos=%d want 0", fill)
			}
			if skip := lossState.dredPLC.FECSkip(); skip != 0 {
				t.Fatalf("public lost FECSkip=%d want 0", skip)
			}
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
	dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
	if packetInfo.sampleRate != 48000 || n != frameSize {
		t.Skipf("cached stereo CELT live-sequence parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
	}
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, false)
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

	pcm := make([]float32, n*dec.Channels())
	got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
	if got != n {
		t.Fatalf("explicit DRED decode=%d want %d", got, n)
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
	assertDecodedPCMQuality(t, pcm[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], packetInfo.sampleRate, dec.Channels(), "cached stereo live-sequence first-loss pcm")
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
	dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
	if packetInfo.sampleRate != 48000 || n != frameSize {
		t.Skipf("cached stereo CELT second-loss parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
	}
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, false)
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

	pcm0 := make([]float32, n*dec.Channels())
	got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm0, n)
	if got != n {
		t.Fatalf("explicit DRED decode=%d want %d", got, n)
	}
	assertInterleavedStereoDuplicated(t, pcm0, got, "cached stereo first loss")
	assertInterleavedStereoDuplicated(t, want.step0.pcm, got, "libopus cached stereo first loss")

	pcm1 := make([]float32, n*dec.Channels())
	got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
	if got != n {
		t.Fatalf("explicit DRED decode=%d want %d", got, n)
	}
	assertInterleavedStereoDuplicated(t, pcm1, got, "cached stereo second loss")
	assertInterleavedStereoDuplicated(t, want.step1.pcm, got, "libopus cached stereo second loss")

	_, stereoDREDStateTol, stereoDREDFARGANTol, stereoDREDCELTTol := decoderDREDLiveSequenceTolerances(frameSize)
	assertDecodedPCMQuality(t, pcm1[:got*dec.Channels()], want.step1.pcm[:got*dec.Channels()], packetInfo.sampleRate, dec.Channels(), "cached stereo live-sequence second-loss pcm")
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step1.state, "cached stereo live-sequence second-loss plc", stereoDREDStateTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step1.fargan, "cached stereo live-sequence second-loss fargan", stereoDREDFARGANTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step1.celt48k, "cached stereo live-sequence second-loss celt", stereoDREDCELTTol)
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
	_, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize48k)

	for _, tc := range []struct {
		name        string
		step1Source int
	}{
		{name: "first_loss"},
		{name: "second_loss", step1Source: libopusDecoderDREDSequenceSourceCarrierDRED},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, decoderSampleRate, packetInfo, 2)
			dred := parseCarrierDREDForExplicitDecode(t, decoderSampleRate, packetInfo)
			if n != wantFrame {
				t.Fatalf("cached stereo 16k warmup samples=%d want %d", n, wantFrame)
			}
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, tc.step1Source, 2*n, false)
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

			pcm := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			comparePCM := pcm[:got*dec.Channels()]
			compareState := want.step0
			compareLabel := "cached stereo 16k first-loss"

			if tc.step1Source != 0 {
				pcm1 := make([]float32, n*dec.Channels())
				got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
				if got != n {
					t.Fatalf("explicit DRED decode=%d want %d", got, n)
				}
				if want.step1.ret != n {
					t.Fatalf("libopus cached stereo 16k second-loss ret=%d want %d", want.step1.ret, n)
				}
				comparePCM = pcm1[:got*dec.Channels()]
				compareState = want.step1
				compareLabel = "cached stereo 16k second-loss"
			}

			assertInterleavedStereoApproxDuplicated(t, comparePCM, n, compareLabel, 1e-2)
			assertInterleavedStereoApproxDuplicated(t, compareState.pcm, n, compareLabel+" libopus", 1e-2)
			assertDecodedPCMQuality(t, comparePCM, compareState.pcm[:n*dec.Channels()], decoderSampleRate, dec.Channels(), compareLabel+" live-sequence pcm")
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
	dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
	if packetInfo.sampleRate != 48000 || n != frameSize {
		t.Skipf("cached stereo CELT follow-up parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
	}
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, true)
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

	pcm := make([]float32, n*dec.Channels())
	got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
	if got != n {
		t.Fatalf("explicit DRED decode=%d want %d", got, n)
	}
	assertInterleavedStereoDuplicated(t, pcm, got, "cached stereo first loss")
	assertInterleavedStereoDuplicated(t, want.step0.pcm, got, "libopus cached stereo first loss")

	nextPCM := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
	gotNext, err := dec.Decode(nextPacket, nextPCM)
	if err != nil {
		t.Fatalf("Decode(next stereo CELT packet) error: %v", err)
	}
	if gotNext != want.next.ret {
		t.Fatalf("Decode(next stereo CELT packet)=%d want %d", gotNext, want.next.ret)
	}

	const stereoDREDStateTol = 1e-4
	const stereoDREDPCMTol = 1e-4
	assertDecodedPCMQuality(t, nextPCM[:gotNext*dec.Channels()], want.next.pcm[:gotNext*dec.Channels()], packetInfo.sampleRate, dec.Channels(), "cached stereo next packet live-sequence pcm")
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
	dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
	if packetInfo.sampleRate != 48000 || n != frameSize {
		t.Skipf("cached stereo CELT second-loss follow-up parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
	}
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, true)
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

	pcm0 := make([]float32, n*dec.Channels())
	got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm0, n)
	if got != n {
		t.Fatalf("explicit DRED decode=%d want %d", got, n)
	}
	assertInterleavedStereoDuplicated(t, pcm0, got, "cached stereo first loss")
	assertInterleavedStereoDuplicated(t, want.step0.pcm, got, "libopus cached stereo first loss")

	pcm1 := make([]float32, n*dec.Channels())
	got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
	if got != n {
		t.Fatalf("explicit DRED decode=%d want %d", got, n)
	}
	assertInterleavedStereoDuplicated(t, pcm1, got, "cached stereo second loss")
	assertInterleavedStereoDuplicated(t, want.step1.pcm, got, "libopus cached stereo second loss")

	nextPCM := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
	gotNext, err := dec.Decode(nextPacket, nextPCM)
	if err != nil {
		t.Fatalf("Decode(next stereo CELT packet) error: %v", err)
	}
	if gotNext != want.next.ret {
		t.Fatalf("Decode(next stereo CELT packet)=%d want %d", gotNext, want.next.ret)
	}

	const stereoDREDStateTol = 1e-4
	const stereoDREDPCMTol = 1e-4
	assertDecodedPCMQuality(t, nextPCM[:gotNext*dec.Channels()], want.next.pcm[:gotNext*dec.Channels()], packetInfo.sampleRate, dec.Channels(), "cached stereo second-loss next packet live-sequence pcm")
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "cached stereo second-loss next packet live-sequence plc", stereoDREDStateTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "cached stereo second-loss next packet live-sequence fargan", stereoDREDStateTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.next.celt48k, "cached stereo second-loss next packet live-sequence celt", stereoDREDPCMTol)
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
			dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, true)
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

			// Explicit DRED-decode path (SourceCarrierDRED oracle, opus_decoder.c:736),
			// then decode the next real packet.
			pcm := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			_, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertDecodedPCMQuality(t, pcm[:got], want.step0.pcm[:got], packetInfo.sampleRate, dec.Channels(), "cached CELT live-sequence first-loss pcm")

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next CELT packet)=%d want %d", gotNext, want.next.ret)
			}

			assertDecodedPCMQuality(t, nextPCM[:gotNext], want.next.pcm[:gotNext], packetInfo.sampleRate, dec.Channels(), "cached CELT next packet live-sequence pcm")
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
			dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("cached CELT SWB live-sequence parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, true)
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

			pcm := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			_, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertDecodedPCMQuality(t, pcm[:got], want.step0.pcm[:got], packetInfo.sampleRate, dec.Channels(), "cached CELT SWB live-sequence first-loss pcm")

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT SWB packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next CELT SWB packet)=%d want %d", gotNext, want.next.ret)
			}

			assertDecodedPCMQuality(t, nextPCM[:gotNext], want.next.pcm[:gotNext], packetInfo.sampleRate, dec.Channels(), "cached CELT SWB next packet live-sequence pcm")
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
			dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, true)
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

			// Explicit DRED-decode path (SourceCarrierDRED oracle, opus_decoder.c:736),
			// two recoveries then the next real packet.
			pcm0 := make([]float32, dec.maxPacketSamples)
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm0, n)
			if got != n {
				t.Fatalf("explicit DRED decode(first)=%d want %d", got, n)
			}
			_, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertDecodedPCMQuality(t, pcm0[:got], want.step0.pcm[:got], packetInfo.sampleRate, dec.Channels(), "cached CELT live-sequence warmup pcm")

			pcm1 := make([]float32, dec.maxPacketSamples)
			got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
			if got != n {
				t.Fatalf("explicit DRED decode(second)=%d want %d", got, n)
			}
			assertDecodedPCMQuality(t, pcm1[:got], want.step1.pcm[:got], packetInfo.sampleRate, dec.Channels(), "cached CELT live-sequence second-loss pcm")

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next CELT packet)=%d want %d", gotNext, want.next.ret)
			}

			assertDecodedPCMQuality(t, nextPCM[:gotNext], want.next.pcm[:gotNext], packetInfo.sampleRate, dec.Channels(), "cached CELT second-loss next packet live-sequence pcm")
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
			dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("cached CELT SWB second-loss live-sequence parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, true)
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
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm0, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			_, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertDecodedPCMQuality(t, pcm0[:got], want.step0.pcm[:got], packetInfo.sampleRate, dec.Channels(), "cached CELT SWB live-sequence warmup pcm")

			pcm1 := make([]float32, dec.maxPacketSamples)
			got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			assertDecodedPCMQuality(t, pcm1[:got], want.step1.pcm[:got], packetInfo.sampleRate, dec.Channels(), "cached CELT SWB live-sequence second-loss pcm")

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT SWB packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next CELT SWB packet)=%d want %d", gotNext, want.next.ret)
			}

			assertDecodedPCMQuality(t, nextPCM[:gotNext], want.next.pcm[:gotNext], packetInfo.sampleRate, dec.Channels(), "cached CELT SWB second-loss next packet live-sequence pcm")
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "cached CELT SWB second-loss next packet live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "cached CELT SWB second-loss next packet live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.next.celt48k, "cached CELT SWB second-loss next packet live-sequence celt", celtTol)
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
			dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("cached CELT WB live-sequence parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, true)
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

			pcm := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			_, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertDecodedPCMQuality(t, pcm[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], dec.SampleRate(), dec.Channels(), "cached CELT WB live-sequence first-loss pcm")

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT WB packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next CELT WB packet)=%d want %d", gotNext, want.next.ret)
			}

			assertDecodedPCMQuality(t, nextPCM[:gotNext], want.next.pcm[:gotNext], dec.SampleRate(), dec.Channels(), "cached CELT WB next packet live-sequence pcm")
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
			dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("cached CELT WB second-loss live-sequence parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, true)
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

			pcm0 := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm0, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			_, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertDecodedPCMQuality(t, pcm0[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], dec.SampleRate(), dec.Channels(), "cached CELT WB live-sequence warmup pcm")

			pcm1 := make([]float32, n*dec.Channels())
			got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			assertDecodedPCMQuality(t, pcm1[:got*dec.Channels()], want.step1.pcm[:got*dec.Channels()], dec.SampleRate(), dec.Channels(), "cached CELT WB live-sequence second-loss pcm")

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT WB packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next CELT WB packet)=%d want %d", gotNext, want.next.ret)
			}

			assertDecodedPCMQuality(t, nextPCM[:gotNext], want.next.pcm[:gotNext], dec.SampleRate(), dec.Channels(), "cached CELT WB second-loss next packet live-sequence pcm")
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "cached CELT WB second-loss next packet live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "cached CELT WB second-loss next packet live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.next.celt48k, "cached CELT WB second-loss next packet live-sequence celt", celtTol)
		})
	}
}
