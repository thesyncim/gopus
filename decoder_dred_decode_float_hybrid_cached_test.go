//go:build gopus_dred || gopus_extra_controls

package gopus

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

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
			dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, true)
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

			pcm := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			_, plcTol, farganTol, celtTol := cachedHybridLiveSequenceTolerances(tc.bandwidth, tc.frameSize)
			assertDecodedPCMQuality(t, pcm[:got], want.step0.pcm[:got], dec.SampleRate(), dec.Channels(), "cached hybrid live-sequence first-loss pcm")

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.next.ret)
			}

			assertDecodedPCMQuality(t, nextPCM[:gotNext], want.next.pcm[:gotNext], dec.SampleRate(), dec.Channels(), "cached hybrid next packet live-sequence pcm")
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
			dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, true)
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
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm0, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			_, plcTol, farganTol, celtTol := cachedHybridLiveSequenceTolerances(tc.bandwidth, tc.frameSize)
			assertDecodedPCMQuality(t, pcm0[:got], want.step0.pcm[:got], dec.SampleRate(), dec.Channels(), "cached hybrid live-sequence warmup pcm")

			pcm1 := make([]float32, dec.maxPacketSamples)
			got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			assertDecodedPCMQuality(t, pcm1[:got], want.step1.pcm[:got], dec.SampleRate(), dec.Channels(), "cached hybrid live-sequence second-loss pcm")

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.next.ret)
			}

			assertDecodedPCMQuality(t, nextPCM[:gotNext], want.next.pcm[:gotNext], dec.SampleRate(), dec.Channels(), "cached hybrid second-loss next packet live-sequence pcm")
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
			dred := parseCarrierDREDForExplicitDecode(t, 16000, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, 16000)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("16 kHz cached hybrid warmup samples=%d want %d", n, wantFrame)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, 16000)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "16k cached hybrid first-loss "+tc.name)
			if want.step0.ret != n {
				t.Fatalf("libopus 16k cached hybrid decoder first-loss ret=%d want %d", want.step0.ret, n)
			}

			_, plcTol, farganTol, celtTol := cachedHybridLiveSequenceTolerances(tc.bandwidth, tc.frameSize)
			pcm := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}

			assertDecodedPCMQuality(t, pcm[:got], want.step0.pcm[:got], dec.SampleRate(), dec.Channels(), "16k cached hybrid first-loss live-sequence pcm")
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "16k cached hybrid first-loss live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "16k cached hybrid first-loss live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, "16k cached hybrid first-loss live-sequence celt", celtTol)
		})
	}
}

func TestDecoderCachedHybridDREDAPIRateDecodeMatrixMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range hybridDREDAPIRateCases() {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, tc.sampleRate, packetInfo)
			dred := parseCarrierDREDForExplicitDecode(t, tc.sampleRate, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, tc.sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("%d Hz cached hybrid warmup samples=%d want %d", tc.sampleRate, n, wantFrame)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, tc.sampleRate)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "API-rate cached hybrid first-loss "+tc.name)
			if want.step0.ret != n {
				t.Fatalf("libopus %d Hz cached hybrid decoder first-loss ret=%d want %d", tc.sampleRate, want.step0.ret, n)
			}

			_, plcTol, farganTol, celtTol := cachedHybridLiveSequenceTolerances(tc.bandwidth, tc.frameSize)
			pcm := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}

			assertDecodedPCMQuality(t, pcm[:got], want.step0.pcm[:got], dec.SampleRate(), dec.Channels(), "API-rate cached hybrid first-loss live-sequence pcm")
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "API-rate cached hybrid first-loss live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "API-rate cached hybrid first-loss live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, "API-rate cached hybrid first-loss live-sequence celt", celtTol)
		})
	}
}

func TestDecoderCachedHybridDREDAPIRateThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range hybridDREDAPIRateCases() {
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

			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, tc.sampleRate, packetInfo)
			dred := parseCarrierDREDForExplicitDecode(t, tc.sampleRate, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, tc.sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("%d Hz cached hybrid warmup samples=%d want %d", tc.sampleRate, n, wantFrame)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, tc.sampleRate)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "API-rate cached hybrid first-loss next-packet "+tc.name)
			if want.step0.ret != n {
				t.Fatalf("libopus %d Hz cached hybrid decoder first-loss ret=%d want %d", tc.sampleRate, want.step0.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus %d Hz cached hybrid decoder follow-up ret=%d want >0", tc.sampleRate, want.next.ret)
			}

			pcm := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			_, plcTol, farganTol, celtTol := cachedHybridLiveSequenceTolerances(tc.bandwidth, tc.frameSize)
			assertDecodedPCMQuality(t, pcm[:got], want.step0.pcm[:got], dec.SampleRate(), dec.Channels(), "API-rate cached hybrid live-sequence first-loss pcm")

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.next.ret)
			}

			assertDecodedPCMQuality(t, nextPCM[:gotNext], want.next.pcm[:gotNext], dec.SampleRate(), dec.Channels(), "API-rate cached hybrid next packet live-sequence pcm")
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "API-rate cached hybrid next packet live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "API-rate cached hybrid next packet live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.next.celt48k, "API-rate cached hybrid next packet live-sequence celt", celtTol)
		})
	}
}

func TestDecoderCachedHybridDREDAPIRateSecondLossMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range hybridDREDAPIRateCases() {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, tc.sampleRate, packetInfo)
			dred := parseCarrierDREDForExplicitDecode(t, tc.sampleRate, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, tc.sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("%d Hz cached hybrid warmup samples=%d want %d", tc.sampleRate, n, wantFrame)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, tc.sampleRate)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "API-rate cached hybrid second-loss "+tc.name)
			if want.step0.ret != n {
				t.Fatalf("libopus %d Hz cached hybrid decoder first warmup ret=%d want %d", tc.sampleRate, want.step0.ret, n)
			}
			if want.step1.ret != n {
				t.Fatalf("libopus %d Hz cached hybrid decoder second-loss ret=%d want %d", tc.sampleRate, want.step1.ret, n)
			}

			_, plcTol, farganTol, celtTol := cachedHybridLiveSequenceTolerances(tc.bandwidth, tc.frameSize)

			pcm0 := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm0, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			assertDecodedPCMQuality(t, pcm0[:got], want.step0.pcm[:got], dec.SampleRate(), dec.Channels(), "API-rate cached hybrid warmup live-sequence pcm")
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "API-rate cached hybrid warmup live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "API-rate cached hybrid warmup live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, "API-rate cached hybrid warmup live-sequence celt", celtTol)

			pcm1 := make([]float32, n*dec.Channels())
			got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			assertDecodedPCMQuality(t, pcm1[:got], want.step1.pcm[:got], dec.SampleRate(), dec.Channels(), "API-rate cached hybrid second-loss live-sequence pcm")
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step1.state, "API-rate cached hybrid second-loss live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step1.fargan, "API-rate cached hybrid second-loss live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step1.celt48k, "API-rate cached hybrid second-loss live-sequence celt", celtTol)
		})
	}
}

func TestDecoderCachedHybridDREDAPIRateSecondLossThenNextPacketMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range hybridDREDAPIRateCases() {
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

			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, tc.sampleRate, packetInfo)
			dred := parseCarrierDREDForExplicitDecode(t, tc.sampleRate, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, tc.sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("%d Hz cached hybrid warmup samples=%d want %d", tc.sampleRate, n, wantFrame)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, tc.sampleRate)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, true)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "API-rate cached hybrid second-loss next-packet "+tc.name)
			if want.step0.ret != n {
				t.Fatalf("libopus %d Hz cached hybrid decoder first warmup ret=%d want %d", tc.sampleRate, want.step0.ret, n)
			}
			if want.step1.ret != n {
				t.Fatalf("libopus %d Hz cached hybrid decoder second-loss ret=%d want %d", tc.sampleRate, want.step1.ret, n)
			}
			if want.next.ret <= 0 {
				t.Fatalf("libopus %d Hz cached hybrid decoder follow-up ret=%d want >0", tc.sampleRate, want.next.ret)
			}

			_, plcTol, farganTol, celtTol := cachedHybridLiveSequenceTolerances(tc.bandwidth, tc.frameSize)

			pcm0 := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm0, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			assertDecodedPCMQuality(t, pcm0[:got], want.step0.pcm[:got], dec.SampleRate(), dec.Channels(), "API-rate cached hybrid live-sequence warmup pcm")

			pcm1 := make([]float32, n*dec.Channels())
			got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			assertDecodedPCMQuality(t, pcm1[:got], want.step1.pcm[:got], dec.SampleRate(), dec.Channels(), "API-rate cached hybrid live-sequence second-loss pcm")

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.next.ret)
			}

			assertDecodedPCMQuality(t, nextPCM[:gotNext], want.next.pcm[:gotNext], dec.SampleRate(), dec.Channels(), "API-rate cached hybrid second-loss next packet live-sequence pcm")
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "API-rate cached hybrid second-loss next packet live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "API-rate cached hybrid second-loss next packet live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.next.celt48k, "API-rate cached hybrid second-loss next packet live-sequence celt", celtTol)
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
			dred := parseCarrierDREDForExplicitDecode(t, 16000, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, 16000)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("16 kHz cached hybrid warmup samples=%d want %d", n, wantFrame)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, 16000)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, true)
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

			pcm := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			_, plcTol, farganTol, celtTol := cachedHybridLiveSequenceTolerances(tc.bandwidth, tc.frameSize)
			assertDecodedPCMQuality(t, pcm[:got], want.step0.pcm[:got], dec.SampleRate(), dec.Channels(), "16k cached hybrid live-sequence first-loss pcm")

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.next.ret)
			}

			assertDecodedPCMQuality(t, nextPCM[:gotNext], want.next.pcm[:gotNext], dec.SampleRate(), dec.Channels(), "16k cached hybrid next packet live-sequence pcm")
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
			dred := parseCarrierDREDForExplicitDecode(t, 16000, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, 16000)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("16 kHz cached hybrid warmup samples=%d want %d", n, wantFrame)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, 16000)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, false)
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

			_, plcTol, farganTol, celtTol := cachedHybridLiveSequenceTolerances(tc.bandwidth, tc.frameSize)

			pcm0 := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm0, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			assertDecodedPCMQuality(t, pcm0[:got], want.step0.pcm[:got], dec.SampleRate(), dec.Channels(), "16k cached hybrid warmup live-sequence pcm")
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "16k cached hybrid warmup live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "16k cached hybrid warmup live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, "16k cached hybrid warmup live-sequence celt", celtTol)

			pcm1 := make([]float32, n*dec.Channels())
			got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			assertDecodedPCMQuality(t, pcm1[:got], want.step1.pcm[:got], dec.SampleRate(), dec.Channels(), "16k cached hybrid second-loss live-sequence pcm")
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
			dred := parseCarrierDREDForExplicitDecode(t, 16000, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, 16000)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("16 kHz cached hybrid warmup samples=%d want %d", n, wantFrame)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, 16000)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nextPacket, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceCarrierDRED, 2*n, true)
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

			_, plcTol, farganTol, celtTol := cachedHybridLiveSequenceTolerances(tc.bandwidth, tc.frameSize)

			pcm0 := make([]float32, n*dec.Channels())
			got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm0, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			assertDecodedPCMQuality(t, pcm0[:got], want.step0.pcm[:got], dec.SampleRate(), dec.Channels(), "16k cached hybrid live-sequence warmup pcm")

			pcm1 := make([]float32, n*dec.Channels())
			got = decodeCachedCarrierDREDViaExplicit(t, dec, dred, 2*n, pcm1, n)
			if got != n {
				t.Fatalf("explicit DRED decode=%d want %d", got, n)
			}
			assertDecodedPCMQuality(t, pcm1[:got], want.step1.pcm[:got], dec.SampleRate(), dec.Channels(), "16k cached hybrid live-sequence second-loss pcm")

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.next.ret {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.next.ret)
			}

			assertDecodedPCMQuality(t, nextPCM[:gotNext], want.next.pcm[:gotNext], dec.SampleRate(), dec.Channels(), "16k cached hybrid second-loss next packet live-sequence pcm")
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.next.state, "16k cached hybrid second-loss next packet live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.next.fargan, "16k cached hybrid second-loss next packet live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.next.celt48k, "16k cached hybrid second-loss next packet live-sequence celt", celtTol)
		})
	}
}
