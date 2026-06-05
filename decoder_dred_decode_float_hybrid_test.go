//go:build gopus_dred || gopus_osce

package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestDecoderExplicitHybridDREDDecodeMatrixMatchesLibopus(t *testing.T) {
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
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			if packetInfo.sampleRate != 48000 || n != tc.frameSize {
				t.Skipf("hybrid explicit parity requires 48 kHz frame=%d packet, got sampleRate=%d frame=%d", tc.frameSize, packetInfo.sampleRate, n)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder hybrid DRED")
			if want.ret != n {
				t.Fatalf("libopus hybrid decoder DRED decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMQuality(t, pcm[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "hybrid explicit libopus pcm")
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "hybrid explicit libopus plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "hybrid explicit libopus fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "hybrid explicit libopus celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecode16kMatrixMatchesLibopus(t *testing.T) {
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
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, 16000)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("16 kHz hybrid explicit frame=%d got %d want %d", tc.frameSize, n, wantFrame)
			}

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder 16k hybrid DRED")
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

			assertDecodedPCMQuality(t, pcm[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "16k hybrid explicit libopus pcm")
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "16k hybrid explicit libopus plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid explicit libopus fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "16k hybrid explicit libopus celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeAPIRateMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range hybridDREDAPIRateCases() {
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
				t.Fatalf("%d Hz hybrid explicit frame=%d got %d want %d", tc.sampleRate, tc.frameSize, n, wantFrame)
			}

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, tc.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder API-rate hybrid DRED")
			if want.ret != n {
				t.Fatalf("libopus %d Hz hybrid decoder DRED decode ret=%d want %d", tc.sampleRate, want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMQuality(t, pcm[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "API-rate hybrid explicit libopus pcm")
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "API-rate hybrid explicit libopus plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "API-rate hybrid explicit libopus fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "API-rate hybrid explicit libopus celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeThenNextPacketAPIRateMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range hybridDREDAPIRateCases() {
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
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder API-rate hybrid DRED")
			if want.ret != n {
				t.Fatalf("libopus %d Hz hybrid decoder DRED decode ret=%d want %d", tc.sampleRate, want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus %d Hz hybrid decoder follow-up ret=%d want >0", tc.sampleRate, want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.nextRet)
			}

			assertDecodedPCMQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "API-rate hybrid explicit next packet pcm")
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "API-rate hybrid explicit next packet plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "API-rate hybrid explicit next packet fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "API-rate hybrid explicit next packet celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeSecondLossAPIRateMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range hybridDREDAPIRateCases() {
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
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder API-rate hybrid DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus %d Hz hybrid decoder DRED warmup ret=%d want %d", tc.sampleRate, want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus %d Hz hybrid decoder DRED second ret=%d want %d", tc.sampleRate, want.ret, n)
			}

			pcm1 := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
			}

			assertDecodedPCMQuality(t, pcm1[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "API-rate hybrid explicit second loss pcm")
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "API-rate hybrid explicit second loss plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "API-rate hybrid explicit second loss fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "API-rate hybrid explicit second loss celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeSecondLossThenNextPacketAPIRateMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range hybridDREDAPIRateCases() {
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
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder API-rate hybrid DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus %d Hz hybrid decoder DRED warmup ret=%d want %d", tc.sampleRate, want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus %d Hz hybrid decoder DRED second ret=%d want %d", tc.sampleRate, want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus %d Hz hybrid decoder second-loss follow-up ret=%d want >0", tc.sampleRate, want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) after second loss error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next hybrid packet) after second loss=%d want %d", gotNext, want.nextRet)
			}

			assertDecodedPCMQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "API-rate hybrid explicit second-loss follow-up pcm")
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "API-rate hybrid explicit second-loss follow-up plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "API-rate hybrid explicit second-loss follow-up fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "API-rate hybrid explicit second-loss follow-up celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeThenNextPacketMatchesLibopus(t *testing.T) {
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
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder hybrid DRED")
			if want.ret != n {
				t.Fatalf("libopus hybrid decoder DRED decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus hybrid decoder follow-up ret=%d want >0", want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next hybrid packet)=%d want %d", gotNext, want.nextRet)
			}

			assertDecodedPCMQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "hybrid explicit next packet pcm")
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "hybrid explicit next packet plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "hybrid explicit next packet fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "hybrid explicit next packet celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeThenNextPacket16kMatchesLibopus(t *testing.T) {
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
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			nextPacket := makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, tc.frameSize, tc.bandwidth)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder 16k hybrid DRED")
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

			assertDecodedPCMQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "16k hybrid explicit next packet pcm")
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "16k hybrid explicit next packet plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid explicit next packet fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "16k hybrid explicit next packet celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeSecondLossMatrixMatchesLibopus(t *testing.T) {
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
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder hybrid DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus hybrid decoder DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus hybrid decoder DRED second ret=%d want %d", want.ret, n)
			}

			pcm1 := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
			}

			assertDecodedPCMQuality(t, pcm1[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "hybrid explicit second loss pcm")
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "hybrid explicit second loss plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "hybrid explicit second loss fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "hybrid explicit second loss celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeSecondLoss16kMatrixMatchesLibopus(t *testing.T) {
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
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder 16k hybrid DRED")
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

			assertDecodedPCMQuality(t, pcm1[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "16k hybrid explicit second loss pcm")
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "16k hybrid explicit second loss plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid explicit second loss fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "16k hybrid explicit second loss celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeSecondLossThenNextPacketMatrixMatchesLibopus(t *testing.T) {
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
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
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

			want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder hybrid DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus hybrid decoder DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus hybrid decoder DRED second ret=%d want %d", want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus hybrid decoder second-loss follow-up ret=%d want >0", want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next hybrid packet) after second loss error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next hybrid packet) after second loss=%d want %d", gotNext, want.nextRet)
			}

			assertDecodedPCMQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "hybrid explicit second-loss follow-up pcm")
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "hybrid explicit second-loss follow-up plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "hybrid explicit second-loss follow-up fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "hybrid explicit second-loss follow-up celt")
		})
	}
}

func TestDecoderExplicitHybridDREDDecodeSecondLossThenNextPacket16kMatrixMatchesLibopus(t *testing.T) {
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
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
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

			want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, 16000, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder 16k hybrid DRED")
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

			assertDecodedPCMQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "16k hybrid explicit second-loss follow-up pcm")
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "16k hybrid explicit second-loss follow-up plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid explicit second-loss follow-up fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "16k hybrid explicit second-loss follow-up celt")
		})
	}
}

// TestDecoderExplicitStereoHybridDRED16kDecodeMatchesLibopus exercises the
// stereo DRED runtime path at 16 kHz against a Hybrid SWB carrier packet
// (10 ms / 480 samples) instead of CELT FB. Libopus leaves tiny L/R drift on
// this forced Hybrid seam, so the duplicate-shape check is numerical.
func TestDecoderExplicitStereoHybridDRED16kDecodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	// Force stereo at the libopus encoder control layer so this exercises a
	// real 16 kHz stereo carrier instead of the encoder's auto mono choice.
	probeInfo, probeErr := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     480,
		ForceMode:     ModeHybrid,
		Bandwidth:     BandwidthSuperwideband,
		Channels:      2,
		ForceChannels: 2,
	})
	if probeErr != nil {
		libopustest.HelperUnavailable(t, "dred packet", probeErr)
	}
	if !ParseTOC(probeInfo.packet[0]).Stereo {
		t.Fatalf("libopus dred emit helper produced mono TOC at 480-sample Hybrid SWB despite forced channels (toc=0x%02x)", probeInfo.packet[0])
	}

	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
		FrameSize:     480,
		ForceMode:     ModeHybrid,
		Bandwidth:     BandwidthSuperwideband,
		Channels:      2,
		ForceChannels: 2,
	})
	if dec.Channels() != 2 {
		t.Fatalf("stereo explicit Hybrid DRED 16k parity got decoder channels=%d, want 2", dec.Channels())
	}

	want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder stereo Hybrid 16k DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder stereo Hybrid 16k DRED decode ret=%d want %d", want.ret, n)
	}
	if want.channels != 2 {
		t.Fatalf("libopus decoder stereo Hybrid 16k DRED decode channels=%d want 2", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
	}

	const stereoHybridDuplicateTol = 3e-3
	for i := 0; i < n; i++ {
		if d := math.Abs(float64(pcm[2*i] - pcm[2*i+1])); d > stereoHybridDuplicateTol {
			t.Fatalf("gopus stereo Hybrid 16k DRED PCM not duplicated at sample %d: |L-R|=%g", i, d)
		}
		if d := math.Abs(float64(want.pcm[2*i] - want.pcm[2*i+1])); d > stereoHybridDuplicateTol {
			t.Fatalf("libopus stereo Hybrid 16k DRED PCM not duplicated at sample %d: |L-R|=%g", i, d)
		}
	}

	const stereoDREDStateTol = 1e-4
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit stereo Hybrid 16k libopus plc", stereoDREDStateTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit stereo Hybrid 16k libopus fargan", stereoDREDStateTol)
	assertDecodedPCMQuality(t, pcm[:n*dec.Channels()], want.pcm[:n*dec.Channels()], dec.SampleRate(), dec.Channels(), "explicit stereo Hybrid 16k libopus pcm")
}

func TestDecoderExplicit16kHybridDREDDecodeMatrixMatchesLibopus(t *testing.T) {
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
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder 16k hybrid DRED")
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

			assertDecodedPCMQuality(t, pcm[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "16k hybrid explicit libopus pcm")
			assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "16k hybrid explicit libopus plc")
			assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid explicit libopus fargan")
			assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "16k hybrid explicit libopus celt")
		})
	}
}

func TestDecoderPublicDecodeDREDHybridAPIRateMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name       string
		sampleRate int
		cfg        libopusDREDPacketConfig
	}{
		{
			name:       "8k_hybrid_swb_mono",
			sampleRate: 8000,
			cfg: libopusDREDPacketConfig{
				FrameSize: 960,
				ForceMode: ModeHybrid,
				Bandwidth: BandwidthSuperwideband,
			},
		},
		{
			name:       "12k_hybrid_swb_mono",
			sampleRate: 12000,
			cfg: libopusDREDPacketConfig{
				FrameSize: 960,
				ForceMode: ModeHybrid,
				Bandwidth: BandwidthSuperwideband,
			},
		},
		{
			name:       "24k_hybrid_swb_mono",
			sampleRate: 24000,
			cfg: libopusDREDPacketConfig{
				FrameSize: 960,
				ForceMode: ModeHybrid,
				Bandwidth: BandwidthSuperwideband,
			},
		},
		{
			name:       "24k_hybrid_swb_stereo",
			sampleRate: 24000,
			cfg: libopusDREDPacketConfig{
				FrameSize:     960,
				ForceMode:     ModeHybrid,
				Bandwidth:     BandwidthSuperwideband,
				Channels:      2,
				ForceChannels: 2,
			},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, tc.sampleRate, tc.cfg)
			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, tc.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus public hybrid API-rate DRED")
			if want.ret != n {
				t.Fatalf("libopus public hybrid API-rate DRED ret=%d want %d", want.ret, n)
			}
			if want.channels != dec.Channels() {
				t.Fatalf("libopus public hybrid API-rate DRED channels=%d want %d", want.channels, dec.Channels())
			}

			pcm := make([]float32, n*dec.Channels())
			got, err := dec.DecodeDRED(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("DecodeDRED error: %v", err)
			}
			if got != n {
				t.Fatalf("DecodeDRED=%d want %d", got, n)
			}

			gotPCM := pcm[:got*dec.Channels()]
			wantPCM := want.pcm[:got*dec.Channels()]
			assertDecodedPCMQuality(t, gotPCM, wantPCM, dec.SampleRate(), dec.Channels(), "public hybrid API-rate DecodeDRED pcm")
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "public hybrid API-rate DecodeDRED plc", 1e-4)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "public hybrid API-rate DecodeDRED fargan", 1e-4)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.celt48k, "public hybrid API-rate DecodeDRED celt", 1e-4)
		})
	}
}

func TestDecoderExplicitDREDDecodeOffsetMatrixHybridSuperwidebandMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	_, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthSuperwideband,
	})
	boundary := -dred.Parsed().Header.OffsetSamples(packetInfo.sampleRate)

	tests := []struct {
		name       string
		dredOffset int
	}{
		{name: "before_first_feature_boundary", dredOffset: boundary - 1},
		{name: "at_first_feature_boundary", dredOffset: boundary},
		{name: "end_of_first_feature_frame", dredOffset: boundary + n - 1},
		{name: "at_second_feature_boundary", dredOffset: boundary + n},
		{name: "late_offset", dredOffset: boundary + 2*n},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, tc.dredOffset, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder hybrid SWB DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder hybrid SWB DRED decode ret=%d want %d", want.ret, n)
			}

			localDec, err := NewDecoder(DefaultDecoderConfig(packetInfo.sampleRate, 1))
			if err != nil {
				t.Fatalf("NewDecoder error: %v", err)
			}
			setDecoderComplexityForLibopusDREDParityTest(t, localDec)
			if err := localDec.SetDNNBlob(decoderBlob); err != nil {
				t.Fatalf("SetDNNBlob error: %v", err)
			}
			seedPCM := make([]float32, localDec.maxPacketSamples)
			if _, err := localDec.Decode(seedPacket, seedPCM); err != nil {
				t.Fatalf("Decode(seed packet) error: %v", err)
			}
			localDRED := NewDRED()
			*localDRED = *dred
			pcm := make([]float32, localDec.maxPacketSamples)
			got, err := localDec.decodeExplicitDREDFloat(localDRED, tc.dredOffset, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			_, plcTol, farganTol := 1e-4, 1e-4, 1e-4
			if tc.dredOffset == boundary {
				// The exact first-feature boundary lands on a FARGAN frame edge;
				// keep the branch pinned while allowing the same tiny DNN drift
				// already covered by the internal libopus neural parity tests.
				_, plcTol, farganTol = 1.5e-4, 1e-2, 5e-2
			}
			assertDecodedPCMQuality(t, pcm[:n], want.pcm[:n], localDec.SampleRate(), localDec.Channels(), "hybrid swb offset matrix pcm")
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, localDec).dredPLC.Snapshot(), want.state, "hybrid swb offset matrix plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, localDec).dredFARGAN.Snapshot(), want.fargan, "hybrid swb offset matrix fargan", farganTol)
		})
	}
}

func TestDecoderExplicitDREDDecodeOffsetMatrixHybridFullbandMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	_, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
	})
	boundary := -dred.Parsed().Header.OffsetSamples(packetInfo.sampleRate)

	tests := []struct {
		name       string
		dredOffset int
	}{
		{name: "before_first_feature_boundary", dredOffset: boundary - 1},
		{name: "at_first_feature_boundary", dredOffset: boundary},
		{name: "end_of_first_feature_frame", dredOffset: boundary + n - 1},
		{name: "at_second_feature_boundary", dredOffset: boundary + n},
		{name: "late_offset", dredOffset: boundary + 2*n},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, tc.dredOffset, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder hybrid FB DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder hybrid FB DRED decode ret=%d want %d", want.ret, n)
			}

			localDec, err := NewDecoder(DefaultDecoderConfig(packetInfo.sampleRate, 1))
			if err != nil {
				t.Fatalf("NewDecoder error: %v", err)
			}
			setDecoderComplexityForLibopusDREDParityTest(t, localDec)
			if err := localDec.SetDNNBlob(decoderBlob); err != nil {
				t.Fatalf("SetDNNBlob error: %v", err)
			}
			seedPCM := make([]float32, localDec.maxPacketSamples)
			if _, err := localDec.Decode(seedPacket, seedPCM); err != nil {
				t.Fatalf("Decode(seed packet) error: %v", err)
			}
			localDRED := NewDRED()
			*localDRED = *dred
			pcm := make([]float32, localDec.maxPacketSamples)
			got, err := localDec.decodeExplicitDREDFloat(localDRED, tc.dredOffset, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			_, plcTol, farganTol := 1e-4, 1e-4, 1e-4
			if tc.dredOffset == boundary {
				// The exact first-feature boundary lands on a FARGAN frame edge;
				// keep the branch pinned while allowing the same tiny DNN drift
				// already covered by the internal libopus neural parity tests.
				_, plcTol, farganTol = 1.5e-4, 1e-2, 5e-2
			}
			assertDecodedPCMQuality(t, pcm[:n], want.pcm[:n], localDec.SampleRate(), localDec.Channels(), "hybrid fb offset matrix pcm")
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, localDec).dredPLC.Snapshot(), want.state, "hybrid fb offset matrix plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, localDec).dredFARGAN.Snapshot(), want.fargan, "hybrid fb offset matrix fargan", farganTol)
		})
	}
}

func TestDecoderExplicitDREDDecodeOffsetMatrix16kHybridMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		bandwidth Bandwidth
	}{
		{name: "swb", bandwidth: BandwidthSuperwideband},
		{name: "fb", bandwidth: BandwidthFullband},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, dred, packetInfo, _, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: 960,
				ForceMode: ModeHybrid,
				Bandwidth: tc.bandwidth,
			})
			wantFrame, err := packetSamplesAtRate(packetInfo.packet, 16000)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			if n != wantFrame {
				t.Fatalf("16 kHz hybrid offset frame=%d want %d", n, wantFrame)
			}
			boundary := -dred.Parsed().Header.OffsetSamples(16000)

			offsets := []struct {
				name       string
				dredOffset int
			}{
				{name: "before_first_feature_boundary", dredOffset: boundary - 1},
				{name: "at_first_feature_boundary", dredOffset: boundary},
				{name: "end_of_first_feature_frame", dredOffset: boundary + n - 1},
				{name: "at_second_feature_boundary", dredOffset: boundary + n},
				{name: "late_offset", dredOffset: boundary + 2*n},
			}

			for _, offset := range offsets {
				offset := offset
				t.Run(offset.name, func(t *testing.T) {
					localDec, localDRED, localPacketInfo, seedPacket, localN := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
						FrameSize: 960,
						ForceMode: ModeHybrid,
						Bandwidth: tc.bandwidth,
					})
					if localPacketInfo.sampleRate != packetInfo.sampleRate || localN != n {
						t.Fatalf("local 16 kHz hybrid packet changed: sampleRate=%d frame=%d want sampleRate=%d frame=%d", localPacketInfo.sampleRate, localN, packetInfo.sampleRate, n)
					}
					want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, localPacketInfo, 16000, -1, offset.dredOffset, n)
					if err != nil {
						libopustest.HelperUnavailable(t, "decoder DRED decode", err)
					}
					requireLibopusDREDDecodeParsed(t, want, "libopus decoder 16k hybrid DRED")
					if want.ret != n {
						t.Fatalf("libopus decoder 16k hybrid DRED decode ret=%d want %d", want.ret, n)
					}

					pcm := make([]float32, localDec.maxPacketSamples)
					got, err := localDec.decodeExplicitDREDFloat(localDRED, offset.dredOffset, pcm, n)
					if err != nil {
						t.Fatalf("decodeExplicitDREDFloat error: %v", err)
					}
					if got != n {
						t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
					}

					_, plcTol, farganTol := 1e-4, 1e-4, 1e-4
					if offset.dredOffset == boundary {
						_, plcTol, farganTol = 1.5e-4, 1e-2, 5e-2
					}
					assertDecodedPCMQuality(t, pcm[:n], want.pcm[:n], localDec.SampleRate(), localDec.Channels(), "16k hybrid offset matrix pcm")
					assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, localDec).dredPLC.Snapshot(), want.state, "16k hybrid offset matrix plc", plcTol)
					assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, localDec).dredFARGAN.Snapshot(), want.fargan, "16k hybrid offset matrix fargan", farganTol)
				})
			}
		})
	}
}
