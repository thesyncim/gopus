//go:build gopus_dred || gopus_extra_controls

package gopus

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	silkpkg "github.com/thesyncim/gopus/internal/silk"
)

func TestDecoderSILKDecodeNilWithCachedDREDLossesMatchLiveLostSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name              string
		decoderSampleRate int
	}{
		{name: "decoder_8000", decoderSampleRate: 8000},
		{name: "decoder_12000", decoderSampleRate: 12000},
		{name: "decoder_16000", decoderSampleRate: 16000},
		{name: "decoder_24000", decoderSampleRate: 24000},
		{name: "decoder_48000", decoderSampleRate: 48000},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			const frameSize = 960
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeSILK,
				Bandwidth: BandwidthWideband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			toc := ParseTOC(packetInfo.packet[0])
			if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband {
				t.Fatalf("cached SILK DRED test packet TOC=%+v, want SILK WB", toc)
			}

			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, tc.decoderSampleRate, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, tc.decoderSampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("cached SILK warmup samples=%d want %d at %d Hz", n, wantFrame, tc.decoderSampleRate)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, tc.decoderSampleRate)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceLost, n, libopusDecoderDREDSequenceSourceNone, 0, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "cached SILK first-loss")
			if want.step0.ret != n {
				t.Fatalf("libopus cached SILK decoder first-loss ret=%d want %d", want.step0.ret, n)
			}
			if want.channels != 1 {
				t.Fatalf("libopus cached SILK decoder channels=%d want 1", want.channels)
			}

			pcm := make([]float32, n*dec.Channels())
			got, err := dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil)=%d want %d", got, n)
			}

			_, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertDecodedPCMQuality(t, pcm[:got], want.step0.pcm[:got], tc.decoderSampleRate, dec.Channels(), "cached SILK live-sequence first-loss pcm")
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "cached SILK live-sequence first-loss plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "cached SILK live-sequence first-loss fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, "cached SILK live-sequence first-loss celt", celtTol)
			assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.step0.silk, silkpkg.BandwidthWideband, "cached SILK live-sequence first-loss silk", max(celtTol, 1))

			decSecond, nSecond := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, tc.decoderSampleRate, packetInfo)
			if nSecond != wantFrame {
				t.Fatalf("cached SILK second-loss warmup samples=%d want %d at %d Hz", nSecond, wantFrame, tc.decoderSampleRate)
			}
			wantSecond, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, nSecond, libopusDecoderDREDSequenceSourceLost, nSecond, libopusDecoderDREDSequenceSourceLost, 2*nSecond, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, wantSecond, "cached SILK second-loss")
			if wantSecond.step1.ret != nSecond {
				t.Fatalf("libopus cached SILK decoder second-loss ret=%d want %d", wantSecond.step1.ret, nSecond)
			}

			pcm0 := make([]float32, nSecond*decSecond.Channels())
			got0, err := decSecond.Decode(nil, pcm0)
			if err != nil {
				t.Fatalf("Decode(nil, warmup) error: %v", err)
			}
			if got0 != nSecond {
				t.Fatalf("Decode(nil, warmup)=%d want %d", got0, nSecond)
			}
			pcm1 := make([]float32, nSecond*decSecond.Channels())
			got1, err := decSecond.Decode(nil, pcm1)
			if err != nil {
				t.Fatalf("Decode(nil, second) error: %v", err)
			}
			if got1 != nSecond {
				t.Fatalf("Decode(nil, second)=%d want %d", got1, nSecond)
			}
			assertDecodedPCMQuality(t, pcm1[:got1], wantSecond.step1.pcm[:got1], tc.decoderSampleRate, decSecond.Channels(), "cached SILK live-sequence second-loss pcm")
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, decSecond).dredPLC.Snapshot(), wantSecond.step1.state, "cached SILK live-sequence second-loss plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, decSecond).dredFARGAN.Snapshot(), wantSecond.step1.fargan, "cached SILK live-sequence second-loss fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, decSecond, wantSecond.step1.celt48k, "cached SILK live-sequence second-loss celt", celtTol)
			assertDecoderDREDSILKStateApproxEqualWithin(t, decSecond, wantSecond.step1.silk, silkpkg.BandwidthWideband, "cached SILK live-sequence second-loss silk", max(celtTol, 16))
		})
	}
}

func TestDecoderSILKDecodeNilWithCachedDREDMatchesLiveLostOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 960
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: frameSize,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "SILK DRED packet", err)
	}
	toc := ParseTOC(packetInfo.packet[0])
	if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband {
		t.Fatalf("SILK DRED loss test packet TOC=%+v, want SILK WB", toc)
	}

	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		sampleRate := sampleRate
		t.Run(fmt.Sprintf("decoder_%d", sampleRate), func(t *testing.T) {
			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, sampleRate, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("SILK warmup samples=%d want %d at %d Hz", n, wantFrame, sampleRate)
			}

			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, sampleRate)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceLost, n, libopusDecoderDREDSequenceSourceNone, 0, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder SILK lost sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "SILK public lost decode")
			if want.step0.ret != n {
				t.Fatalf("libopus SILK public lost ret=%d want %d", want.step0.ret, n)
			}
			if want.channels != 1 {
				t.Fatalf("libopus SILK public lost channels=%d want 1", want.channels)
			}

			pcm := make([]float32, n*dec.Channels())
			got, err := dec.Decode(nil, pcm)
			if err != nil {
				t.Fatalf("Decode(nil) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil)=%d want %d", got, n)
			}

			state := requireDecoderDREDState(t, dec)
			if state.dredRecovery != 0 {
				t.Fatalf("SILK public lost dredRecovery=%d want 0", state.dredRecovery)
			}
			if fill := state.dredPLC.FECFillPos(); fill != 0 {
				t.Fatalf("SILK public lost FECFillPos=%d want 0", fill)
			}
			if skip := state.dredPLC.FECSkip(); skip != 0 {
				t.Fatalf("SILK public lost FECSkip=%d want 0", skip)
			}

			_, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertDecodedPCMQuality(t, pcm[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], sampleRate, dec.Channels(), "SILK public lost pcm")
			assertDecoderDREDPLCStateApproxEqualWithin(t, state.dredPLC.Snapshot(), want.step0.state, "SILK public lost plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, state.dredFARGAN.Snapshot(), want.step0.fargan, "SILK public lost fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, "SILK public lost celt", celtTol)
			assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.step0.silk, silkpkg.BandwidthWideband, "SILK public lost silk", max(celtTol, 16))
		})
	}
}

func TestDecoderSILKDecodeNilWithCachedDREDRequestedPLCDurationMatchesLiveLostSequenceOracle(t *testing.T) {
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
			libopustest.HelperUnavailable(t, "dred packet", err)
		}
		if toc := ParseTOC(packetInfo.packet[0]); toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband || toc.Stereo != (channels == 2) {
			t.Fatalf("cached SILK DRED requested PLC packet TOC=%+v, want channels=%d SILK WB", toc, channels)
		}

		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			sampleRate := sampleRate
			t.Run(fmt.Sprintf("channels_%d_decoder_%d", channels, sampleRate), func(t *testing.T) {
				maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, sampleRate)
				for _, requested := range []int{sampleRate / 25, sampleRate * 3 / 50} {
					t.Run(fmt.Sprintf("request_%d", requested), func(t *testing.T) {
						dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, sampleRate, packetInfo, channels)
						packetFrame, err := packetSamplesAtRate(packetInfo.packet, sampleRate)
						if err != nil {
							t.Fatalf("packetSamplesAtRate: %v", err)
						}
						if n != packetFrame {
							t.Fatalf("cached SILK warmup samples=%d want %d at %d Hz", n, packetFrame, sampleRate)
						}

						want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, requested, libopusDecoderDREDSequenceSourceLost, requested, libopusDecoderDREDSequenceSourceNone, 0, false)
						if err != nil {
							libopustest.HelperUnavailable(t, "decoder DRED requested PLC sequence", err)
						}
						requireLibopusDREDSequenceParsed(t, want, "cached SILK requested PLC")
						if want.channels != channels {
							t.Fatalf("libopus cached SILK requested PLC channels=%d want %d", want.channels, channels)
						}
						if want.step0.ret != requested {
							t.Fatalf("libopus cached SILK requested PLC ret=%d want %d", want.step0.ret, requested)
						}

						pcm := make([]float32, requested*dec.Channels())
						got, err := dec.Decode(nil, pcm)
						if err != nil {
							t.Fatalf("Decode(nil) error: %v", err)
						}
						if got != requested {
							t.Fatalf("Decode(nil)=%d want %d", got, requested)
						}

						_, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(requested)
						assertDecodedPCMQuality(t, pcm[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], sampleRate, dec.Channels(), "cached SILK requested PLC live-sequence pcm")
						assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "cached SILK requested PLC live-sequence plc", plcTol)
						assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "cached SILK requested PLC live-sequence fargan", farganTol)
						assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, "cached SILK requested PLC live-sequence celt", celtTol)
						assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.step0.silk, silkpkg.BandwidthWideband, "cached SILK requested PLC live-sequence silk", max(celtTol, 16))
					})
				}
			})
		}
	}
}

func TestDecoderSILKDecodeWithFECNoLBRRWithCachedDREDRequestedDurationMatchesLiveLostSequenceOracle(t *testing.T) {
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
			libopustest.HelperUnavailable(t, "dred packet", err)
		}
		toc := ParseTOC(packetInfo.packet[0])
		if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband || toc.Stereo != (channels == 2) {
			t.Fatalf("cached SILK DRED FEC-fallback requested packet TOC=%+v, want channels=%d SILK WB", toc, channels)
		}
		firstFrameData, err := extractFirstFramePayload(packetInfo.packet, toc)
		if err != nil {
			t.Fatalf("extractFirstFramePayload: %v", err)
		}
		if packetHasLBRR(firstFrameData, toc) {
			t.Skip("cached SILK DRED FEC-fallback fixture unexpectedly carries LBRR")
		}

		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			sampleRate := sampleRate
			t.Run(fmt.Sprintf("channels_%d_decoder_%d", channels, sampleRate), func(t *testing.T) {
				maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, sampleRate)
				for _, requested := range []int{sampleRate / 25, sampleRate * 3 / 50} {
					t.Run(fmt.Sprintf("request_%d", requested), func(t *testing.T) {
						dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacketWithChannels(t, sampleRate, packetInfo, channels)
						packetFrame, err := packetSamplesAtRate(packetInfo.packet, sampleRate)
						if err != nil {
							t.Fatalf("packetSamplesAtRate: %v", err)
						}
						if n != packetFrame {
							t.Fatalf("cached SILK warmup samples=%d want %d at %d Hz", n, packetFrame, sampleRate)
						}

						want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, requested, libopusDecoderDREDSequenceSourceLost, requested, libopusDecoderDREDSequenceSourceNone, 0, false)
						if err != nil {
							libopustest.HelperUnavailable(t, "decoder DRED requested FEC-fallback sequence", err)
						}
						requireLibopusDREDSequenceParsed(t, want, "cached SILK requested FEC-fallback")
						if want.channels != channels {
							t.Fatalf("libopus cached SILK requested FEC-fallback channels=%d want %d", want.channels, channels)
						}
						if want.step0.ret != requested {
							t.Fatalf("libopus cached SILK requested FEC-fallback ret=%d want %d", want.step0.ret, requested)
						}

						pcm := make([]float32, requested*dec.Channels())
						got, err := dec.DecodeWithFEC(packetInfo.packet, pcm, true)
						if err != nil {
							t.Fatalf("DecodeWithFEC(no LBRR) error: %v", err)
						}
						if got != requested {
							t.Fatalf("DecodeWithFEC(no LBRR)=%d want %d", got, requested)
						}

						_, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(requested)
						assertDecodedPCMQuality(t, pcm[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], sampleRate, dec.Channels(), "cached SILK requested FEC-fallback live-sequence pcm")
						assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "cached SILK requested FEC-fallback live-sequence plc", plcTol)
						assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "cached SILK requested FEC-fallback live-sequence fargan", farganTol)
						assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, "cached SILK requested FEC-fallback live-sequence celt", celtTol)
						assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.step0.silk, silkpkg.BandwidthWideband, "cached SILK requested FEC-fallback live-sequence silk", max(celtTol, 16))
					})
				}
			})
		}
	}
}

func TestDecoderSILKDecodeWithFECNoLBRRWithCachedDREDLossesMatchLiveLostSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		sampleRate := sampleRate
		t.Run(fmt.Sprintf("decoder_%d", sampleRate), func(t *testing.T) {
			const frameSize = 960
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeSILK,
				Bandwidth: BandwidthWideband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}
			toc := ParseTOC(packetInfo.packet[0])
			if toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband {
				t.Fatalf("cached SILK DRED FEC-fallback packet TOC=%+v, want SILK WB", toc)
			}
			firstFrameData, err := extractFirstFramePayload(packetInfo.packet, toc)
			if err != nil {
				t.Fatalf("extractFirstFramePayload: %v", err)
			}
			if packetHasLBRR(firstFrameData, toc) {
				t.Skip("cached SILK DRED FEC-fallback fixture unexpectedly carries LBRR")
			}
			dec, n := prepareCachedDREDDecodeParityStateForDecoderRateAndPacket(t, sampleRate, packetInfo)
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("cached SILK warmup samples=%d want %d at %d Hz", n, wantFrame, sampleRate)
			}

			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, sampleRate)
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceLost, n, libopusDecoderDREDSequenceSourceLost, 2*n, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "cached SILK FEC-fallback first-loss")
			if want.step0.ret != n {
				t.Fatalf("libopus cached SILK FEC-fallback first-loss ret=%d want %d", want.step0.ret, n)
			}
			if want.step1.ret != n {
				t.Fatalf("libopus cached SILK FEC-fallback second-loss ret=%d want %d", want.step1.ret, n)
			}

			pcm := make([]float32, n*dec.Channels())
			got, err := dec.DecodeWithFEC(packetInfo.packet, pcm, true)
			if err != nil {
				t.Fatalf("DecodeWithFEC(no LBRR) error: %v", err)
			}
			if got != n {
				t.Fatalf("DecodeWithFEC(no LBRR)=%d want %d", got, n)
			}

			_, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(frameSize)
			assertDecodedPCMQuality(t, pcm[:got], want.step0.pcm[:got], sampleRate, dec.Channels(), "cached SILK FEC-fallback live-sequence pcm")
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "cached SILK FEC-fallback live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "cached SILK FEC-fallback live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, "cached SILK FEC-fallback live-sequence celt", celtTol)
			assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.step0.silk, silkpkg.BandwidthWideband, "cached SILK FEC-fallback live-sequence silk", max(celtTol, 1))

			pcmSecond := make([]float32, n*dec.Channels())
			gotSecond, err := dec.Decode(nil, pcmSecond)
			if err != nil {
				t.Fatalf("Decode(nil, second) error: %v", err)
			}
			if gotSecond != n {
				t.Fatalf("Decode(nil, second)=%d want %d", gotSecond, n)
			}
			assertDecodedPCMQuality(t, pcmSecond[:gotSecond], want.step1.pcm[:gotSecond], sampleRate, dec.Channels(), "cached SILK FEC-fallback second-loss live-sequence pcm")
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step1.state, "cached SILK FEC-fallback second-loss live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step1.fargan, "cached SILK FEC-fallback second-loss live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step1.celt48k, "cached SILK FEC-fallback second-loss live-sequence celt", celtTol)
			assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.step1.silk, silkpkg.BandwidthWideband, "cached SILK FEC-fallback second-loss live-sequence silk", max(celtTol, 16))
		})
	}
}

func TestDecoderCachedStereoSILKDREDAPIRateMatchesLiveSequenceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 960
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     frameSize,
		ForceMode:     ModeSILK,
		Bandwidth:     BandwidthWideband,
		Channels:      2,
		ForceChannels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo SILK DRED packet", err)
	}
	if toc := ParseTOC(packetInfo.packet[0]); toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband || !toc.Stereo {
		t.Fatalf("cached stereo SILK DRED packet TOC=%+v, want stereo SILK WB", toc)
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
				t.Fatalf("cached stereo SILK warmup samples=%d want %d at %d Hz", n, wantFrame, sampleRate)
			}
			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, sampleRate)
			// A public packet-loss Decode(nil) does not consume cached DRED:
			// libopus gates lpcnet_plc_fec_add on dred!=NULL (opus_decoder.c:736),
			// and opus_decode(NULL) passes dred==NULL, so SILK loss runs plain PLC.
			// This matches the mono SILK siblings and commit cc04ecf0; this stereo
			// case was overlooked there and still used the stale CarrierDRED source.
			want, err := probeLibopusDecoderDREDSequence(nil, packetInfo.packet, nil, maxDRED, oracleRate, n, libopusDecoderDREDSequenceSourceLost, n, libopusDecoderDREDSequenceSourceLost, 2*n, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "stereo SILK decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "cached stereo SILK second-loss")
			if want.channels != 2 {
				t.Fatalf("libopus cached stereo SILK DRED channels=%d want 2", want.channels)
			}
			if want.step0.ret != n || want.step1.ret != n {
				t.Fatalf("libopus cached stereo SILK DRED ret=(%d,%d) want (%d,%d)", want.step0.ret, want.step1.ret, n, n)
			}

			pcm0 := make([]float32, n*dec.Channels())
			got, err := dec.Decode(nil, pcm0)
			if err != nil {
				t.Fatalf("Decode(nil, first) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil, first)=%d want %d", got, n)
			}
			_, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(n)
			assertInterleavedStereoApproxDuplicated(t, pcm0[:got*dec.Channels()], got, "cached stereo SILK first loss", 1e-2)
			assertInterleavedStereoApproxDuplicated(t, want.step0.pcm, got, "libopus cached stereo SILK first loss", 1e-2)
			assertDecodedPCMQuality(t, pcm0[:got*dec.Channels()], want.step0.pcm[:got*dec.Channels()], sampleRate, dec.Channels(), "cached stereo SILK first-loss live-sequence pcm")
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step0.state, "cached stereo SILK first-loss live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step0.fargan, "cached stereo SILK first-loss live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step0.celt48k, "cached stereo SILK first-loss live-sequence celt", celtTol)
			assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.step0.silk, silkpkg.BandwidthWideband, "cached stereo SILK first-loss live-sequence silk", max(celtTol, 1))

			pcm1 := make([]float32, n*dec.Channels())
			got, err = dec.Decode(nil, pcm1)
			if err != nil {
				t.Fatalf("Decode(nil, second) error: %v", err)
			}
			if got != n {
				t.Fatalf("Decode(nil, second)=%d want %d", got, n)
			}
			assertInterleavedStereoApproxDuplicated(t, pcm1[:got*dec.Channels()], got, "cached stereo SILK second loss", 1e-2)
			assertInterleavedStereoApproxDuplicated(t, want.step1.pcm, got, "libopus cached stereo SILK second loss", 1e-2)
			assertDecodedPCMQuality(t, pcm1[:got*dec.Channels()], want.step1.pcm[:got*dec.Channels()], sampleRate, dec.Channels(), "cached stereo SILK second-loss live-sequence pcm")
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.step1.state, "cached stereo SILK second-loss live-sequence plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.step1.fargan, "cached stereo SILK second-loss live-sequence fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.step1.celt48k, "cached stereo SILK second-loss live-sequence celt", celtTol)
			assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.step1.silk, silkpkg.BandwidthWideband, "cached stereo SILK second-loss live-sequence silk", max(celtTol, 8))
		})
	}
}

// TestDecoderExplicitSILKDREDDecodeMatchesLibopus exercises the SILK-only
// explicit DRED decode path against libopus. libopus routes SILK-only DRED
// through silk_Decode(lost_flag=1) with FEC features queued in lpcnet, where
// the SILK DeepPLC hook produces 16 kHz neural concealment and SILK upsamples
// to the API rate. The gopus equivalent (decodeExplicitSILKDREDFloat) installs
// the same DeepPLC hook around a standard PLC chunk decode after priming the
// LPCNet/FARGAN entry history from the prior SILK native lowband.
func TestDecoderExplicitSILKDREDDecodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(
		t, 48000, libopusDREDPacketConfig{
			FrameSize: 960,
			ForceMode: ModeSILK,
			Bandwidth: BandwidthWideband,
		})

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder SILK DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder SILK DRED decode ret=%d want %d", want.ret, n)
	}
	if want.channels != 1 {
		t.Fatalf("libopus decoder SILK DRED decode channels=%d want 1", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
	}

	assertDecodedPCMQuality(t, pcm[:n], want.pcm[:n], dec.SampleRate(), dec.Channels(), "explicit silk libopus pcm")
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit silk libopus plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit silk libopus fargan")
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "explicit silk libopus celt")
	assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.silk, silkpkg.BandwidthWideband, "explicit silk libopus silk", 1e-4)
}

// TestDecoderExplicit16kSILKDREDDecodeMatchesLibopus mirrors the 48 kHz SILK
// explicit DRED parity test at a 16 kHz decoder rate. SILK runs internally at
// 16 kHz so the 16 kHz API path skips the SILK->API upsampler entirely; the
// DeepPLC neural lowband is emitted at 16 kHz directly to the caller buffer.
// libopus's opus_decoder_dred_decode_float supports this path at any internal
// SR including 16 kHz.
func TestDecoderExplicit16kSILKDREDDecodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(
		t, 16000, libopusDREDPacketConfig{
			FrameSize: 960,
			ForceMode: ModeSILK,
			Bandwidth: BandwidthWideband,
		})

	want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder 16k SILK DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder 16k SILK DRED decode ret=%d want %d", want.ret, n)
	}
	if want.channels != 1 {
		t.Fatalf("libopus decoder 16k SILK DRED decode channels=%d want 1", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
	}

	assertDecodedPCMQuality(t, pcm[:n], want.pcm[:n], dec.SampleRate(), dec.Channels(), "explicit 16k silk libopus pcm")
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k silk libopus plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k silk libopus fargan")
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "explicit 16k silk libopus celt")
	assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.silk, silkpkg.BandwidthWideband, "explicit 16k silk libopus silk", 1e-4)
}

func TestDecoderPublicSILKDREDAPIRateMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name       string
		sampleRate int
		cfg        libopusDREDPacketConfig
		pcmTol     float64
		stateTol   float64
	}{
		{
			name:       "8k_mono",
			sampleRate: 8000,
			cfg: libopusDREDPacketConfig{
				FrameSize: 960,
				ForceMode: ModeSILK,
				Bandwidth: BandwidthWideband,
			},
			pcmTol:   1e-4,
			stateTol: 1e-4,
		},
		{
			name:       "12k_mono",
			sampleRate: 12000,
			cfg: libopusDREDPacketConfig{
				FrameSize: 960,
				ForceMode: ModeSILK,
				Bandwidth: BandwidthWideband,
			},
			pcmTol:   1e-4,
			stateTol: 1e-4,
		},
		{
			name:       "24k_mono",
			sampleRate: 24000,
			cfg: libopusDREDPacketConfig{
				FrameSize: 960,
				ForceMode: ModeSILK,
				Bandwidth: BandwidthWideband,
			},
			pcmTol:   1e-4,
			stateTol: 1e-4,
		},
		{
			name:       "24k_stereo",
			sampleRate: 24000,
			cfg: libopusDREDPacketConfig{
				FrameSize:     960,
				ForceMode:     ModeSILK,
				Bandwidth:     BandwidthWideband,
				Channels:      2,
				ForceChannels: 2,
			},
			pcmTol:   1e-4,
			stateTol: 1e-4,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, tc.sampleRate, tc.cfg)
			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, tc.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder SILK API-rate DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus public SILK API-rate DRED")
			if want.ret != n {
				t.Fatalf("libopus public SILK API-rate DRED ret=%d want %d", want.ret, n)
			}
			if want.channels != dec.Channels() {
				t.Fatalf("libopus public SILK API-rate DRED channels=%d want %d", want.channels, dec.Channels())
			}

			pcm := make([]float32, n*dec.Channels())
			got, err := dec.DecodeDRED(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("DecodeDRED error: %v", err)
			}
			if got != n {
				t.Fatalf("DecodeDRED=%d want %d", got, n)
			}

			assertDecodedPCMQuality(t, pcm[:got*dec.Channels()], want.pcm[:got*dec.Channels()], dec.SampleRate(), dec.Channels(), "public SILK API-rate DecodeDRED pcm")
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "public SILK API-rate DecodeDRED plc", tc.stateTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "public SILK API-rate DecodeDRED fargan", tc.stateTol)
			assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.silk, silkpkg.BandwidthWideband, "public SILK API-rate DecodeDRED silk", tc.stateTol)
		})
	}
}

// TestDecoderExplicitSILKDREDDecodeStereoMatchesLibopus mirrors
// TestDecoderExplicitSILKDREDDecodeMatchesLibopus for the stereo SILK DRED
// runtime path. libopus routes DRED through one lpcnet state on SILK channel 0
// and exposes duplicated L=R PCM on the API side for this fixture.
func TestDecoderExplicitSILKDREDDecodeStereoMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	probeInfo, probeErr := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     960,
		ForceMode:     ModeSILK,
		Bandwidth:     BandwidthWideband,
		Channels:      2,
		ForceChannels: 2,
	})
	if probeErr != nil {
		libopustest.HelperUnavailable(t, "dred packet", probeErr)
	}
	if !ParseTOC(probeInfo.packet[0]).Stereo {
		t.Fatalf("forced stereo SILK DRED packet produced mono TOC at 960-sample WB (toc=0x%02x)", probeInfo.packet[0])
	}

	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(
		t, 48000, libopusDREDPacketConfig{
			FrameSize:     960,
			ForceMode:     ModeSILK,
			Bandwidth:     BandwidthWideband,
			Channels:      2,
			ForceChannels: 2,
		})
	if dec.Channels() != 2 {
		t.Fatalf("stereo explicit SILK DRED parity got decoder channels=%d, want 2", dec.Channels())
	}

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder stereo SILK DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder stereo SILK DRED decode ret=%d want %d", want.ret, n)
	}
	if want.channels != 2 {
		t.Fatalf("libopus decoder stereo SILK DRED decode channels=%d want 2", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
	}

	for i := 0; i < n; i++ {
		if d := math.Abs(float64(pcm[2*i] - pcm[2*i+1])); d != 0 {
			t.Fatalf("gopus stereo SILK DRED PCM not L=R duplicated at sample %d: |L-R|=%g", i, d)
		}
		if d := math.Abs(float64(want.pcm[2*i] - want.pcm[2*i+1])); d != 0 {
			t.Fatalf("libopus stereo SILK DRED PCM not L=R duplicated at sample %d: |L-R|=%g", i, d)
		}
	}

	const stereoDREDStateTol = 1e-4
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit stereo SILK libopus plc", stereoDREDStateTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit stereo SILK libopus fargan", stereoDREDStateTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.celt48k, "explicit stereo SILK libopus celt", stereoDREDStateTol)
	assertDecoderDREDSILKStateApproxEqualWithin(t, dec, want.silk, silkpkg.BandwidthWideband, "explicit stereo SILK libopus silk", stereoDREDStateTol)
	assertDecodedPCMQuality(t, pcm[:n*dec.Channels()], want.pcm[:n*dec.Channels()], dec.SampleRate(), dec.Channels(), "explicit stereo SILK libopus pcm")
}
