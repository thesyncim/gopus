//go:build gopus_dred || gopus_osce

package gopus

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestDecoderExplicitDREDDecode16kFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState16kForFrameSize(t, frameSize)

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
			if want.ret != n {
				t.Fatalf("libopus 16k frame-size decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:n], want.pcm[:n], dec.SampleRate(), dec.Channels(), "explicit 16k frame-size libopus pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k frame-size libopus plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k frame-size libopus fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "explicit 16k frame-size libopus celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeThenNextPacket16kFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState16kForFrameSize(t, frameSize)
			nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, frameSize)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
			if want.ret != n {
				t.Fatalf("libopus 16k follow-up frame-size decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus 16k follow-up frame-size next ret=%d want >0", want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next packet) error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next packet)=%d want %d", gotNext, want.nextRet)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "explicit 16k follow-up frame-size pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k follow-up frame-size plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k follow-up frame-size fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "explicit 16k follow-up frame-size celt")
		})
	}
}

func TestDecoderExplicitDREDDecode16kCELTSuperwidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT SWB DRED")
			if want.ret != n {
				t.Fatalf("libopus 16k CELT SWB frame-size decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:n], want.pcm[:n], dec.SampleRate(), dec.Channels(), "explicit 16k celt swb frame-size libopus pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k celt swb frame-size libopus plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k celt swb frame-size libopus fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "explicit 16k celt swb frame-size libopus celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeThenNextPacket16kCELTSuperwidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthSuperwideband)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT SWB DRED")
			if want.ret != n {
				t.Fatalf("libopus 16k CELT SWB follow-up frame-size decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus 16k CELT SWB follow-up frame-size next ret=%d want >0", want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT SWB packet) error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next CELT SWB packet)=%d want %d", gotNext, want.nextRet)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "explicit 16k celt swb follow-up frame-size pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k celt swb follow-up frame-size plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k celt swb follow-up frame-size fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "explicit 16k celt swb follow-up frame-size celt")
		})
	}
}

func TestDecoderExplicitDREDDecode16kCELTWidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})

			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT WB DRED")
			if want.ret != n {
				t.Fatalf("libopus 16k CELT WB frame-size decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:n], want.pcm[:n], dec.SampleRate(), dec.Channels(), "explicit 16k celt wb frame-size libopus pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k celt wb frame-size libopus plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k celt wb frame-size libopus fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "explicit 16k celt wb frame-size libopus celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeThenNextPacket16kCELTWidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("carrier_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT WB DRED")
			if want.ret != n {
				t.Fatalf("libopus 16k CELT WB follow-up frame-size decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet <= 0 {
				t.Fatalf("libopus 16k CELT WB follow-up frame-size next ret=%d want >0", want.nextRet)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT WB packet) error: %v", err)
			}
			if gotNext != want.nextRet {
				t.Fatalf("Decode(next CELT WB packet)=%d want %d", gotNext, want.nextRet)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "explicit 16k celt wb follow-up frame-size pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k celt wb follow-up frame-size plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k celt wb follow-up frame-size fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "explicit 16k celt wb follow-up frame-size celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeOffsetMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	_, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
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
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder DRED decode ret=%d want %d", want.ret, n)
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

			assertDecodedPCMExactAndQuality(t, pcm[:n], want.pcm[:n], localDec.SampleRate(), localDec.Channels(), "offset matrix pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, localDec).dredPLC.Snapshot(), want.state, "offset matrix plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, localDec).dredFARGAN.Snapshot(), want.fargan, "offset matrix fargan")
		})
	}
}

func TestDecoderExplicitDREDDecodeOffsetMatrixCELTSuperwidebandMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	decoderBlob := requireLibopusDecoderNeuralModelBlob(t)
	_, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeCELT,
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
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT SWB DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder CELT SWB DRED decode ret=%d want %d", want.ret, n)
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

			assertDecodedPCMExactAndQuality(t, pcm[:n], want.pcm[:n], localDec.SampleRate(), localDec.Channels(), "celt swb offset matrix pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, localDec).dredPLC.Snapshot(), want.state, "celt swb offset matrix plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, localDec).dredFARGAN.Snapshot(), want.fargan, "celt swb offset matrix fargan")
		})
	}
}

func TestDecoderExplicitDREDDecodeFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForFrameSize(t, frameSize)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz explicit frame-size parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder DRED decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "frame size matrix pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "frame size matrix plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "frame size matrix fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "frame size matrix celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeCELTSuperwidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz CELT SWB frame-size parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT SWB DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder CELT SWB DRED decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "celt swb frame size matrix pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "celt swb frame size matrix plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "celt swb frame size matrix fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "celt swb frame size matrix celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeCELTWidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz CELT WB frame-size parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT WB DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder CELT WB DRED decode ret=%d want %d", want.ret, n)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "celt wb frame size matrix pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "celt wb frame size matrix plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "celt wb frame size matrix fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "celt wb frame size matrix celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeSecondLossFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForFrameSize(t, frameSize)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz explicit second-loss frame-size parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus decoder DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus decoder DRED second ret=%d want %d", want.ret, n)
			}

			pcm1 := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm1[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "second loss frame size matrix pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "second loss frame size matrix plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "second loss frame size matrix fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "second loss frame size matrix celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeSecondLossCELTSuperwidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz CELT SWB second-loss parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT SWB DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus decoder CELT SWB DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus decoder CELT SWB DRED second ret=%d want %d", want.ret, n)
			}

			pcm1 := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm1[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "celt swb second loss frame size matrix pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "celt swb second loss frame size matrix plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "celt swb second loss frame size matrix fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "celt swb second loss frame size matrix celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeThenNextPacketFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForFrameSize(t, frameSize)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz explicit follow-up frame-size parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, frameSize)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder DRED decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet != n {
				t.Fatalf("libopus decoder follow-up ret=%d want %d", want.nextRet, n)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next packet) error: %v", err)
			}
			if gotNext != n {
				t.Fatalf("Decode(next packet)=%d want %d", gotNext, n)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "follow-up frame size matrix pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "follow-up frame size matrix plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "follow-up frame size matrix fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "follow-up frame size matrix celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeThenNextPacketCELTSuperwidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz CELT SWB follow-up parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthSuperwideband)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT SWB DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder CELT SWB DRED decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet != n {
				t.Fatalf("libopus decoder CELT SWB follow-up ret=%d want %d", want.nextRet, n)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT SWB packet) error: %v", err)
			}
			if gotNext != n {
				t.Fatalf("Decode(next CELT SWB packet)=%d want %d", gotNext, n)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "celt swb follow-up pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "celt swb follow-up plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "celt swb follow-up fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "celt swb follow-up celt")
		})
	}
}

func TestDecoderExplicitSecondLossThenNextPacketFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForFrameSize(t, frameSize)
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz explicit second-loss follow-up frame-size parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, frameSize)

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
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus decoder DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus decoder DRED second ret=%d want %d", want.ret, n)
			}
			if want.nextRet != n {
				t.Fatalf("libopus decoder second-loss follow-up ret=%d want %d", want.nextRet, n)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next packet) after second loss error: %v", err)
			}
			if gotNext != n {
				t.Fatalf("Decode(next packet) after second loss=%d want %d", gotNext, n)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "second-loss follow-up frame size matrix pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "second-loss follow-up frame size matrix plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "second-loss follow-up frame size matrix fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "second-loss follow-up frame size matrix celt")
		})
	}
}

func TestDecoderExplicitSecondLossThenNextPacketCELTSuperwidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthSuperwideband,
			})
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz CELT SWB second-loss follow-up parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthSuperwideband)

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
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT SWB DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus decoder CELT SWB DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus decoder CELT SWB DRED second ret=%d want %d", want.ret, n)
			}
			if want.nextRet != n {
				t.Fatalf("libopus decoder CELT SWB second-loss follow-up ret=%d want %d", want.nextRet, n)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT SWB packet) after second loss error: %v", err)
			}
			if gotNext != n {
				t.Fatalf("Decode(next CELT SWB packet) after second loss=%d want %d", gotNext, n)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "celt swb second-loss follow-up pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "celt swb second-loss follow-up plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "celt swb second-loss follow-up fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "celt swb second-loss follow-up celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeSecondLossCELTWidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz CELT WB second-loss parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}

			pcm0 := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT WB DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus decoder CELT WB DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus decoder CELT WB DRED second ret=%d want %d", want.ret, n)
			}

			pcm1 := make([]float32, dec.maxPacketSamples)
			got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n)
			if err != nil {
				t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
			}
			if got != n {
				t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
			}

			assertDecodedPCMExactAndQuality(t, pcm1[:got], want.pcm[:got], dec.SampleRate(), dec.Channels(), "celt wb second loss frame size matrix pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "celt wb second loss frame size matrix plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "celt wb second loss frame size matrix fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "celt wb second loss frame size matrix celt")
		})
	}
}

func TestDecoderExplicitDREDDecodeThenNextPacketCELTWidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz CELT WB follow-up parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)

			lossPCM := make([]float32, dec.maxPacketSamples)
			if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
				t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
			}

			want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT WB DRED")
			if want.ret != n {
				t.Fatalf("libopus decoder CELT WB DRED decode ret=%d want %d", want.ret, n)
			}
			if want.nextRet != n {
				t.Fatalf("libopus decoder CELT WB follow-up ret=%d want %d", want.nextRet, n)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT WB packet) error: %v", err)
			}
			if gotNext != n {
				t.Fatalf("Decode(next CELT WB packet)=%d want %d", gotNext, n)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "celt wb follow-up pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "celt wb follow-up plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "celt wb follow-up fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "celt wb follow-up celt")
		})
	}
}

func TestDecoderExplicitSecondLossThenNextPacketCELTWidebandFrameSizeMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{120, 240, 480, 960} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthWideband,
			})
			if packetInfo.sampleRate != 48000 || n != frameSize {
				t.Skipf("48 kHz CELT WB second-loss follow-up parity requires frame=%d packet, got sampleRate=%d frame=%d", frameSize, packetInfo.sampleRate, n)
			}
			nextPacket := makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, frameSize, BandwidthWideband)

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
			requireLibopusDREDDecodeParsed(t, want, "libopus decoder CELT WB DRED")
			if want.warmupRet != n {
				t.Fatalf("libopus decoder CELT WB DRED warmup ret=%d want %d", want.warmupRet, n)
			}
			if want.ret != n {
				t.Fatalf("libopus decoder CELT WB DRED second ret=%d want %d", want.ret, n)
			}
			if want.nextRet != n {
				t.Fatalf("libopus decoder CELT WB second-loss follow-up ret=%d want %d", want.nextRet, n)
			}

			nextPCM := make([]float32, dec.maxPacketSamples)
			gotNext, err := dec.Decode(nextPacket, nextPCM)
			if err != nil {
				t.Fatalf("Decode(next CELT WB packet) after second loss error: %v", err)
			}
			if gotNext != n {
				t.Fatalf("Decode(next CELT WB packet) after second loss=%d want %d", gotNext, n)
			}

			assertDecodedPCMExactAndQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "celt wb second-loss follow-up pcm")
			assertDecoderDREDPLCStateBitsMatch(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "celt wb second-loss follow-up plc")
			assertDecoderDREDFARGANStateBitsMatch(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "celt wb second-loss follow-up fargan")
			assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.celt48k, "celt wb second-loss follow-up celt")
		})
	}
}
