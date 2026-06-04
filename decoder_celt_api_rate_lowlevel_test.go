package gopus

import (
	"testing"

	celtpkg "github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestCELTDecoderAPIRateToFloat32MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize48 := range []int{240, 960} {
		for _, packetChannels := range []int{1, 2} {
			packet := encodeAPIRateCELTPacketFrameSize(t, packetChannels, frameSize48)
			toc := ParseTOC(packet[0])
			if toc.Mode != ModeCELT {
				t.Fatalf("packet mode=%v want CELT", toc.Mode)
			}
			payload := firstAPIRateFramePayload(t, packet)
			for _, decoderChannels := range []int{1, 2} {
				for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
					t.Run("frame_"+itoaSmall(frameSize48)+"_pktch_"+itoaSmall(packetChannels)+"_decch_"+itoaSmall(decoderChannels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
						frameSize, err := packetSamplesAtRate(packet, sampleRate)
						if err != nil {
							t.Fatalf("packetSamplesAtRate: %v", err)
						}
						want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, decoderChannels, frameSize, [][]byte{packet})
						if err != nil {
							libopustest.HelperUnavailable(t, "low-level CELT API-rate reference decode", err)
						}

						dec := celtpkg.NewDecoder(decoderChannels)
						if err := dec.SetAPISampleRate(sampleRate); err != nil {
							t.Fatalf("SetAPISampleRate: %v", err)
						}
						dec.SetBandwidth(celtpkg.BandwidthFromOpusConfig(int(toc.Bandwidth)))
						got := make([]float32, frameSize*decoderChannels)
						if err := dec.DecodeFrameWithPacketStereoToFloat32AtAPIRate(payload, frameSize, toc.Stereo, got); err != nil {
							t.Fatalf("DecodeFrameWithPacketStereoToFloat32AtAPIRate: %v", err)
						}
						assertAPIRateQualityFloat32(t, got, want, sampleRate, decoderChannels, "low-level CELT API-rate decode")
					})
				}
			}
		}
	}
}

func TestCELTDecoderAPIRatePLCMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, packetChannels := range []int{1, 2} {
		packet := encodeAPIRateCELTPacket(t, packetChannels)
		toc := ParseTOC(packet[0])
		payload := firstAPIRateFramePayload(t, packet)
		for _, decoderChannels := range []int{1, 2} {
			for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
				t.Run("pktch_"+itoaSmall(packetChannels)+"_decch_"+itoaSmall(decoderChannels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
					frameSize, err := packetSamplesAtRate(packet, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}
					want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, decoderChannels, frameSize, [][]byte{packet, nil})
					if err != nil {
						libopustest.HelperUnavailable(t, "low-level CELT API-rate PLC reference decode", err)
					}

					dec := celtpkg.NewDecoder(decoderChannels)
					if err := dec.SetAPISampleRate(sampleRate); err != nil {
						t.Fatalf("SetAPISampleRate: %v", err)
					}
					dec.SetBandwidth(celtpkg.BandwidthFromOpusConfig(int(toc.Bandwidth)))
					got := make([]float32, 0, len(want))
					frame := make([]float32, frameSize*decoderChannels)
					if err := dec.DecodeFrameWithPacketStereoToFloat32AtAPIRate(payload, frameSize, toc.Stereo, frame); err != nil {
						t.Fatalf("DecodeFrameWithPacketStereoToFloat32AtAPIRate packet: %v", err)
					}
					got = append(got, frame...)
					clear(frame)
					if err := dec.DecodeFrameWithPacketStereoToFloat32AtAPIRate(nil, frameSize, toc.Stereo, frame); err != nil {
						t.Fatalf("DecodeFrameWithPacketStereoToFloat32AtAPIRate PLC: %v", err)
					}
					got = append(got, frame...)
					assertAPIRateQualityFloat32(t, got, want, sampleRate, decoderChannels, "low-level CELT API-rate PLC")
				})
			}
		}
	}
}
