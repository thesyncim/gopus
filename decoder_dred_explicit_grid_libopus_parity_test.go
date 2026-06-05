//go:build gopus_dred || gopus_osce

package gopus

// Explicit-DRED-decode parity grid: API-rate × mode cells that were thin or
// missing from the existing decode-float file.
//
// Cells covered here (all use the decodeExplicitDREDFloat / DecodeDRED path,
// gated on the probeLibopusDecoderDREDDecode* oracles):
//
//   CELT  – 16 kHz decoder rate (mono + stereo): first-loss via
//            decodeCachedCarrierDREDViaExplicit, mirroring the existing 8/12/24
//            cases in TestDecoderCachedCELTDREDAPIRateMatchesLiveSequenceOracle.
//
//   Hybrid – 16 kHz decoder rate explicit decode: first-loss, second-loss,
//            then-next-packet, second-loss-then-next-packet.  These four
//            scenarios mirror the existing hybridDREDAPIRateCases() battery
//            (8/12/24 kHz) extended to 16 kHz; they use decodeExplicitDREDFloat
//            directly so the internal hybrid-at-16k path is gated bit-exactly.
//
//   SILK  – explicit decodeExplicitDREDFloat API-rate matrix (8/12/24 kHz,
//            first-loss and second-loss).  TestDecoderPublicSILKDREDAPIRateMatchesLibopus
//            already covers these rates via the public DecodeDRED wrapper; the
//            tests below gate the internal path (decodeExplicitDREDFloat) for
//            correctness independent of the public-API wrapper.

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	silkpkg "github.com/thesyncim/gopus/internal/silk"
)

// TestDecoderCachedCELTDRED16kExplicitMatchesLiveSequenceOracle gates the
// CELT explicit-decode path at the 16 kHz decoder API rate against the
// SourceCarrierDRED live-sequence oracle.  The existing
// TestDecoderCachedCELTDREDAPIRateMatchesLiveSequenceOracle covers 8/12/24 kHz;
// 16 kHz is tested here via decodeCachedCarrierDREDViaExplicit (the same entry
// point that backs the public DecodeDRED at 16 kHz).
func TestDecoderCachedCELTDRED16kExplicitMatchesLiveSequenceOracle(t *testing.T) {
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
		t.Fatalf("CELT 16k DRED packet TOC=%+v, want CELT FB", toc)
	}

	const decoderRate = 16000
	dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, decoderRate, packetInfo)
	wantFrame, err := packetSamplesAtRate(packetInfo.packet, decoderRate)
	if err != nil {
		t.Fatalf("packetSamplesAtRate: %v", err)
	}
	if n != wantFrame {
		t.Fatalf("CELT 16k warmup samples=%d want %d", n, wantFrame)
	}

	dred := parseCarrierDREDForExplicitDecode(t, decoderRate, packetInfo)
	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, decoderRate)
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, false)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "CELT 16k first-loss")
	if want.step0.ret != n {
		t.Fatalf("libopus CELT 16k first-loss ret=%d want %d", want.step0.ret, n)
	}

	_, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(packetInfo.sampleRate / 50)
	pcm := make([]float32, n*dec.Channels())
	got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
	if got != n {
		t.Fatalf("explicit DRED decode=%d want %d", got, n)
	}

	assertDecodedPCMQuality(t, pcm[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], decoderRate, dec.Channels(), "CELT 16k explicit live-sequence pcm")
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "CELT 16k explicit live-sequence plc", plcTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "CELT 16k explicit live-sequence fargan", farganTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, "CELT 16k explicit live-sequence celt", celtTol)
}

// TestDecoderCachedStereoCELTDRED16kExplicitMatchesLiveSequenceOracle gates the
// stereo CELT explicit-decode path at the 16 kHz decoder API rate.  The
// TestDecoderCachedStereoCELTDREDAPIRateMatchesLiveSequenceOracle already covers
// 16 kHz stereo, but this test targets the same cell via the sequence oracle so
// it closes the mono/stereo 16 kHz CELT explicit-decode row.
func TestDecoderCachedStereoCELTDRED16kExplicitMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     960,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo CELT 16k dred packet", err)
	}
	toc := ParseTOC(packetInfo.packet[0])
	if toc.Mode != ModeCELT || toc.Bandwidth != BandwidthFullband || !toc.Stereo {
		t.Fatalf("stereo CELT 16k DRED packet TOC=%+v, want stereo CELT FB", toc)
	}

	const decoderRate = 16000
	const wantChannels = 2
	dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, decoderRate, packetInfo, wantChannels)
	wantFrame, err := packetSamplesAtRate(packetInfo.packet, decoderRate)
	if err != nil {
		t.Fatalf("packetSamplesAtRate: %v", err)
	}
	if n != wantFrame {
		t.Fatalf("stereo CELT 16k warmup samples=%d want %d", n, wantFrame)
	}

	dred := parseCarrierDREDForExplicitDecode(t, decoderRate, packetInfo)
	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, decoderRate)
	want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceCarrierDRED, n, libopusDecoderDREDSequenceSourceNone, 0, false)
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo CELT 16k decoder DRED sequence", err)
	}
	requireLibopusDREDSequenceParsed(t, want, "stereo CELT 16k first-loss")
	if want.channels != wantChannels {
		t.Fatalf("libopus stereo CELT 16k DRED channels=%d want %d", want.channels, wantChannels)
	}
	if want.step0.ret != n {
		t.Fatalf("libopus stereo CELT 16k first-loss ret=%d want %d", want.step0.ret, n)
	}

	_, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(packetInfo.sampleRate / 50)
	pcm := make([]float32, n*dec.Channels())
	got := decodeCachedCarrierDREDViaExplicit(t, dec, dred, n, pcm, n)
	if got != n {
		t.Fatalf("stereo CELT 16k explicit DRED decode=%d want %d", got, n)
	}

	assertDecodedPCMQuality(t, pcm[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], decoderRate, dec.Channels(), "stereo CELT 16k explicit live-sequence pcm")
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "stereo CELT 16k explicit live-sequence plc", plcTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "stereo CELT 16k explicit live-sequence fargan", farganTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, "stereo CELT 16k explicit live-sequence celt", celtTol)
}

// hybrid16kExplicitDREDCases returns the 16 kHz decoder-rate hybrid test cases
// for the explicit decodeExplicitDREDFloat battery. These are the 16 kHz
// equivalents of hybridDREDAPIRateCases() and close the "16 kHz hybrid explicit
// decode" cell in the DRED parity matrix.
func hybrid16kExplicitDREDCases() []hybridDREDAPIRateCase {
	return []hybridDREDAPIRateCase{
		{name: "16k_swb_10ms", sampleRate: 16000, bandwidth: BandwidthSuperwideband, frameSize: 480},
		{name: "16k_swb_20ms", sampleRate: 16000, bandwidth: BandwidthSuperwideband, frameSize: 960},
		{name: "16k_fb_10ms", sampleRate: 16000, bandwidth: BandwidthFullband, frameSize: 480},
		{name: "16k_fb_20ms", sampleRate: 16000, bandwidth: BandwidthFullband, frameSize: 960},
	}
}

// TestDecoderExplicitHybridDRED16kAPIRateFirstLossMatchesLibopus gates the
// first-loss explicit DRED decode at a 16 kHz decoder API rate for Hybrid
// SWB/FB carriers. Mirrors TestDecoderExplicitHybridDREDDecodeAPIRateMatrixMatchesLibopus
// (which covers 8/12/24 kHz) extended to 16 kHz.
//
// libopus reference: opus_decoder_dred_decode_float (opus_decoder.c) called with
// a 16 kHz decoder via probeLibopusDecoderDREDDecodeFloatForDecoder.
func TestDecoderExplicitHybridDRED16kAPIRateFirstLossMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range hybrid16kExplicitDREDCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, tc.sampleRate, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, tc.sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("16k hybrid explicit frame=%d got %d want %d", tc.frameSize, n, wantFrame)
			}

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, tc.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, fmt.Sprintf("libopus decoder 16k hybrid %s DRED", tc.name))
			if want.ret != n {
				t.Fatalf("libopus 16k hybrid decoder DRED decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMQuality(t, pcm[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "16k hybrid API-rate explicit first-loss pcm")
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "16k hybrid API-rate explicit first-loss plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid API-rate explicit first-loss fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "16k hybrid API-rate explicit first-loss celt")
		})
	}
}

// TestDecoderExplicitHybridDRED16kAPIRateThenNextPacketMatchesLibopus gates the
// then-next-packet path at 16 kHz for Hybrid explicit DRED.
func TestDecoderExplicitHybridDRED16kAPIRateThenNextPacketMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range hybrid16kExplicitDREDCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, tc.sampleRate, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, tc.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, fmt.Sprintf("libopus 16k hybrid %s next-packet DRED", tc.name))
			if want.ret != n {
				t.Fatalf("libopus 16k hybrid decoder DRED decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus 16k hybrid decoder follow-up ret=%d want >0", want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.nextRet)
			}

			assertDecodedPCMQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "16k hybrid API-rate explicit next-packet pcm")
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "16k hybrid API-rate explicit next-packet plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid API-rate explicit next-packet fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "16k hybrid API-rate explicit next-packet celt")
		})
	}
}

// TestDecoderExplicitHybridDRED16kAPIRateSecondLossMatchesLibopus gates the
// second-loss explicit DRED decode at 16 kHz for Hybrid carriers.
func TestDecoderExplicitHybridDRED16kAPIRateSecondLossMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range hybrid16kExplicitDREDCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, tc.sampleRate, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, tc.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, fmt.Sprintf("libopus 16k hybrid %s second-loss DRED", tc.name))
			if want.warmupRet != n {
				t.Fatalf("libopus 16k hybrid decoder DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus 16k hybrid decoder DRED second ret=%d want %d", want.ret, n)
			}

			pcm1 := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
			}

			assertDecodedPCMQuality(t, pcm1[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "16k hybrid API-rate explicit second-loss pcm")
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "16k hybrid API-rate explicit second-loss plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid API-rate explicit second-loss fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "16k hybrid API-rate explicit second-loss celt")
		})
	}
}

// TestDecoderExplicitHybridDRED16kAPIRateSecondLossThenNextPacketMatchesLibopus
// gates the second-loss-then-next-packet path at 16 kHz for Hybrid explicit DRED.
func TestDecoderExplicitHybridDRED16kAPIRateSecondLossThenNextPacketMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range hybrid16kExplicitDREDCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, tc.sampleRate, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}
			pcm1 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, tc.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, fmt.Sprintf("libopus 16k hybrid %s second-loss+next DRED", tc.name))
			if want.warmupRet != n {
				t.Fatalf("libopus 16k hybrid decoder DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus 16k hybrid decoder DRED second ret=%d want %d", want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus 16k hybrid decoder second-loss follow-up ret=%d want >0", want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) after second loss error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next hybrid packet) after second loss=%d want %d", gotNext, want.nextRet)
			}

			assertDecodedPCMQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "16k hybrid API-rate explicit second-loss+next pcm")
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "16k hybrid API-rate explicit second-loss+next plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid API-rate explicit second-loss+next fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "16k hybrid API-rate explicit second-loss+next celt")
		})
	}
}

// silkExplicitDREDAPIRateCases returns the sub-48 kHz SILK decoder-rate cases
// for the explicit decodeExplicitDREDFloat battery.
func silkExplicitDREDAPIRateCases() []struct {
	name       string
	sampleRate int
} {
	return []struct {
		name       string
		sampleRate int
	}{
		{name: "8k", sampleRate: 8000},
		{name: "12k", sampleRate: 12000},
		{name: "24k", sampleRate: 24000},
	}
}

// TestDecoderExplicitSILKDREDAPIRateFirstLossMatchesLibopus gates the internal
// decodeExplicitDREDFloat path (not the public DecodeDRED wrapper) for SILK
// carriers at 8/12/24 kHz decoder API rates.
//
// TestDecoderPublicSILKDREDAPIRateMatchesLibopus covers the same rates via the
// public DecodeDRED API; these tests gate the internal path independently.
// libopus reference: opus_decoder_dred_decode_float (opus_decoder.c).
func TestDecoderExplicitSILKDREDAPIRateFirstLossMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range silkExplicitDREDAPIRateCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(
				t, tc.sampleRate, libopusDREDPacketConfig{
					FrameSize: 960,
					ForceMode: ModeSILK,
					Bandwidth: BandwidthWideband,
				})
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, tc.sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("SILK %s explicit frame=%d want %d", tc.name, n, wantFrame)
			}

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, tc.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder SILK API-rate DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, fmt.Sprintf("libopus SILK %s explicit DRED", tc.name))
			if want.ret != n {
				t.Fatalf("libopus SILK %s explicit DRED ret=%d want %d", tc.name, want.ret, n)
			}
			if want.channels != 1 {
				t.Fatalf("libopus SILK %s explicit DRED channels=%d want 1", tc.name, want.channels)
			}

			pcm := make([]float32, n*dec.Channels())
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMQuality(t, pcm[:got*dec.Channels()], want.pcm[:got*dec.Channels()], dec.SampleRate(), dec.Channels(), fmt.Sprintf("SILK %s explicit API-rate pcm", tc.name))
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, fmt.Sprintf("SILK %s explicit API-rate plc", tc.name))
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, fmt.Sprintf("SILK %s explicit API-rate fargan", tc.name))
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, fmt.Sprintf("SILK %s explicit API-rate celt", tc.name))
			assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.silk, silkpkg.BandwidthWideband, fmt.Sprintf("SILK %s explicit API-rate silk", tc.name), 1e-4)
		})
	}
}

// TestDecoderExplicitSILKDREDAPIRateSecondLossMatchesLibopus gates the
// second-loss explicit decodeExplicitDREDFloat path for SILK at 8/12/24/48 kHz
// decoder rates. The existing TestDecoderExplicitSILKDREDDecodeMatchesLibopus
// and TestDecoderExplicit16kSILKDREDDecodeMatchesLibopus cover 48 kHz and
// 16 kHz respectively; this test extends the matrix to all API rates with two
// consecutive losses.
//
// libopus reference: opus_decoder_dred_decode_float called twice
// (warmup_dred_offset=n, dred_offset=2n).
func TestDecoderExplicitSILKDREDAPIRateSecondLossMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	// All five API rates: 8/12/16/24/48 kHz.
	tests := []struct {
		name       string
		sampleRate int
	}{
		{name: "8k", sampleRate: 8000},
		{name: "12k", sampleRate: 12000},
		{name: "16k", sampleRate: 16000},
		{name: "24k", sampleRate: 24000},
		{name: "48k", sampleRate: 48000},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(
				t, tc.sampleRate, libopusDREDPacketConfig{
					FrameSize: 960,
					ForceMode: ModeSILK,
					Bandwidth: BandwidthWideband,
				})
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, tc.sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("SILK %s second-loss frame=%d want %d", tc.name, n, wantFrame)
			}

			// First loss (warmup).
			pcm0 := make([]float32, n*dec.Channels())
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			// Oracle: warmup at offset n, decode at offset 2n.
			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, tc.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder SILK API-rate second-loss DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, fmt.Sprintf("libopus SILK %s second-loss explicit DRED", tc.name))
			if want.warmupRet != n {
				t.Fatalf("libopus SILK %s second-loss warmup ret=%d want %d", tc.name, want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus SILK %s second-loss ret=%d want %d", tc.name, want.ret, n)
			}
			if want.channels != 1 {
				t.Fatalf("libopus SILK %s second-loss channels=%d want 1", tc.name, want.channels)
			}

			// Second loss.
			pcm1 := make([]float32, n*dec.Channels())
			got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
			}

			assertDecodedPCMQuality(t, pcm1[:got*dec.Channels()], want.pcm[:got*dec.Channels()], dec.SampleRate(), dec.Channels(), fmt.Sprintf("SILK %s second-loss explicit pcm", tc.name))
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, fmt.Sprintf("SILK %s second-loss explicit plc", tc.name))
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, fmt.Sprintf("SILK %s second-loss explicit fargan", tc.name))
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, fmt.Sprintf("SILK %s second-loss explicit celt", tc.name))
			assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.silk, silkpkg.BandwidthWideband, fmt.Sprintf("SILK %s second-loss explicit silk", tc.name), 1e-4)
		})
	}
}
