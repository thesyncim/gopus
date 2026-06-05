package gopus

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestDecodeInt16AndFECUseSILKAPIRatePacketDuration(t *testing.T) {
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateSILKPacket(t, channels)
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				want, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}

				intDec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder int16: %v", err)
				}
				pcm16 := make([]int16, want*channels)
				n, err := intDec.DecodeInt16(packet, pcm16)
				if err != nil {
					t.Fatalf("DecodeInt16: %v", err)
				}
				if n != want {
					t.Fatalf("DecodeInt16 samples=%d want %d", n, want)
				}

				fecDec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder fec: %v", err)
				}
				pcm := make([]float32, want*channels)
				n, err = fecDec.DecodeWithFEC(packet, pcm, true)
				if err != nil {
					t.Fatalf("DecodeWithFEC(no LBRR): %v", err)
				}
				if n != want {
					t.Fatalf("DecodeWithFEC(no LBRR) samples=%d want %d", n, want)
				}
			})
		}
	}
}

func TestDecodeWithFECCELTRequestedPLCDurationMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		seedPacket := encodeAPIRateCELTPacket(t, channels)
		recoveryPacket := encodeAPIRateCELTPacketFrameSize(t, channels, 960)
		for _, sampleRate := range []int{8000, 16000, 48000} {
			packetFrameSize, err := packetSamplesAtRate(seedPacket, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			for _, requestedFrameSize := range []int{sampleRate / 25, sampleRate * 3 / 50} {
				t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate)+"_request_"+itoaSmall(requestedFrameSize), func(t *testing.T) {
					steps := []libopusAPIRateDecodeStep{
						{packet: seedPacket},
						{packet: recoveryPacket, fec: true},
					}
					want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, requestedFrameSize, steps)
					if err != nil {
						libopustest.HelperUnavailable(t, "api-rate requested CELT FEC reference decode", err)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got := make([]float32, 0, len(want))
					frame := make([]float32, requestedFrameSize*channels)

					n, err := dec.Decode(seedPacket, frame)
					if err != nil {
						t.Fatalf("Decode seed: %v", err)
					}
					if n != packetFrameSize {
						t.Fatalf("Decode seed samples=%d want %d", n, packetFrameSize)
					}
					got = append(got, frame[:n*channels]...)

					clear(frame)
					n, err = dec.DecodeWithFEC(recoveryPacket, frame, true)
					if err != nil {
						t.Fatalf("DecodeWithFEC recovery: %v", err)
					}
					if n != requestedFrameSize {
						t.Fatalf("DecodeWithFEC samples=%d want requested %d", n, requestedFrameSize)
					}
					got = append(got, frame[:n*channels]...)

					assertAPIRateQualityFloat32(t, got, want, sampleRate, channels, "CELT requested FEC duration")
				})
			}
		}
	}
}

func TestDecodeHybridFECUsesAPIRatePacketDurationForTenMs(t *testing.T) {
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateHybridPacketFrameSize(t, channels, 480)
		toc := ParseTOC(packet[0])
		if toc.Mode != ModeHybrid || toc.FrameSize != 480 {
			t.Fatalf("channels=%d test packet mode=%v frame=%d want Hybrid 10ms", channels, toc.Mode, toc.FrameSize)
		}

		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				want, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				pcm := make([]float32, want*channels)
				n, err := dec.DecodeWithFEC(packet, pcm, true)
				if err != nil {
					t.Fatalf("DecodeWithFEC(no LBRR): %v", err)
				}
				if n != want {
					t.Fatalf("DecodeWithFEC(no LBRR) samples=%d want %d", n, want)
				}
			})
		}
	}
}

func TestDecodeWithFECNoLBRRAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name      string
		packet    func(t *testing.T, channels int) []byte
		tolerance float64
	}{
		{name: "silk_20ms", packet: encodeAPIRateSILKPacket, tolerance: 8e-3},
		{name: "silk_40ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateSILKPacketFrameSize(t, channels, 1920) }, tolerance: 8e-3},
		{name: "silk_60ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateSILKPacketFrameSize(t, channels, 2880) }, tolerance: 8e-3},
		{name: "celt_20ms", packet: encodeAPIRateCELTPacket, tolerance: 3e-3},
		{name: "celt_40ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateCELTPacketFrameSize(t, channels, 1920) }, tolerance: 3e-3},
		{name: "celt_60ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateCELTPacketFrameSize(t, channels, 2880) }, tolerance: 3e-3},
		{name: "hybrid_20ms", packet: encodeAPIRateHybridPacket, tolerance: 1e-2},
		{name: "hybrid_40ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateHybridPacketFrameSize(t, channels, 1920) }, tolerance: 1e-2},
		{name: "hybrid_60ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateHybridPacketFrameSize(t, channels, 2880) }, tolerance: 1e-2},
	} {
		for _, channels := range []int{1, 2} {
			packet := tc.packet(t, channels)
			toc := ParseTOC(packet[0])
			if toc.Mode == ModeSILK || toc.Mode == ModeHybrid {
				firstFrameData, err := extractFirstFramePayload(packet, toc)
				if err != nil {
					t.Fatalf("%s ch=%d extract first frame: %v", tc.name, channels, err)
				}
				if packetHasLBRR(firstFrameData, toc) {
					t.Fatalf("%s ch=%d test packet unexpectedly contains LBRR", tc.name, channels)
				}
			}

			for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
				t.Run(tc.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
					frameSize, err := packetSamplesAtRate(packet, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}

					steps := []libopusAPIRateDecodeStep{
						{packet: packet},
						{packet: packet, fec: true},
					}
					want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, frameSize, steps)
					if err != nil {
						libopustest.HelperUnavailable(t, "api-rate no-LBRR FEC reference decode", err)
					}
					fecFrameSize := len(want)/channels - frameSize
					if fecFrameSize <= 0 {
						t.Fatalf("api-rate no-LBRR FEC reference decoded %d samples after seed, want positive", fecFrameSize)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got := make([]float32, 0, len(want))
					frameCapacity := max(fecFrameSize, frameSize)
					frame := make([]float32, frameCapacity*channels)
					n, err := dec.Decode(packet, frame)
					if err != nil {
						t.Fatalf("Decode seed: %v", err)
					}
					if n != frameSize {
						t.Fatalf("Decode seed samples=%d want %d", n, frameSize)
					}
					got = append(got, frame[:n*channels]...)

					n, err = dec.DecodeWithFEC(packet, frame, true)
					if err != nil {
						t.Fatalf("DecodeWithFEC(no LBRR): %v", err)
					}
					if n != fecFrameSize {
						t.Fatalf("DecodeWithFEC(no LBRR) samples=%d want %d", n, fecFrameSize)
					}
					got = append(got, frame[:n*channels]...)

					assertAPIRateQualityFloat32(t, got, want, sampleRate, channels, tc.name+" api-rate no-LBRR FEC decode")
				})
			}
		}
	}
}

func TestDecodeWithFECNoLBRRRequestedDurationMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name      string
		seed      func(t *testing.T, channels int) []byte
		recovery  func(t *testing.T, channels int) []byte
		tolerance float64
	}{
		{name: "celt_to_silk", seed: encodeAPIRateCELTPacket, recovery: encodeAPIRateSILKPacket, tolerance: 1e-2},
		{name: "silk_to_hybrid", seed: encodeAPIRateSILKPacket, recovery: encodeAPIRateHybridPacket, tolerance: 1.2e-2},
	} {
		for _, channels := range []int{1, 2} {
			seedPacket := tc.seed(t, channels)
			recoveryPacket := tc.recovery(t, channels)
			toc := ParseTOC(recoveryPacket[0])
			if toc.Mode != ModeSILK && toc.Mode != ModeHybrid {
				t.Fatalf("%s recovery mode=%v want SILK or Hybrid", tc.name, toc.Mode)
			}
			firstFrameData, err := extractFirstFramePayload(recoveryPacket, toc)
			if err != nil {
				t.Fatalf("%s ch=%d extract first frame: %v", tc.name, channels, err)
			}
			if packetHasLBRR(firstFrameData, toc) {
				t.Fatalf("%s ch=%d recovery packet unexpectedly contains LBRR", tc.name, channels)
			}

			for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
				packetFrameSize, err := packetSamplesAtRate(recoveryPacket, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}
				for _, requestedFrameSize := range []int{sampleRate / 25, sampleRate * 3 / 50} {
					t.Run(tc.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate)+"_request_"+itoaSmall(requestedFrameSize), func(t *testing.T) {
						if requestedFrameSize <= packetFrameSize {
							t.Fatalf("requestedFrameSize=%d want > packetFrameSize=%d", requestedFrameSize, packetFrameSize)
						}
						steps := []libopusAPIRateDecodeStep{
							{packet: seedPacket},
							{packet: recoveryPacket, fec: true},
							{packet: recoveryPacket},
						}
						want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, requestedFrameSize, steps)
						if err != nil {
							libopustest.HelperUnavailable(t, "api-rate requested no-LBRR reference decode", err)
						}

						dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
						if err != nil {
							t.Fatalf("NewDecoder: %v", err)
						}
						got := make([]float32, 0, len(want))
						frame := make([]float32, requestedFrameSize*channels)

						seedSamples, err := packetSamplesAtRate(seedPacket, sampleRate)
						if err != nil {
							t.Fatalf("seed packetSamplesAtRate: %v", err)
						}
						n, err := dec.Decode(seedPacket, frame)
						if err != nil {
							t.Fatalf("Decode seed: %v", err)
						}
						if n != seedSamples {
							t.Fatalf("Decode seed samples=%d want %d", n, seedSamples)
						}
						got = append(got, frame[:n*channels]...)

						clear(frame)
						n, err = dec.DecodeWithFEC(recoveryPacket, frame, true)
						if err != nil {
							t.Fatalf("DecodeWithFEC(no LBRR): %v", err)
						}
						if n != requestedFrameSize {
							t.Fatalf("DecodeWithFEC(no LBRR) samples=%d want requested %d", n, requestedFrameSize)
						}
						got = append(got, frame[:n*channels]...)

						clear(frame)
						n, err = dec.Decode(recoveryPacket, frame)
						if err != nil {
							t.Fatalf("Decode recovery packet: %v", err)
						}
						if n != packetFrameSize {
							t.Fatalf("Decode recovery packet samples=%d want %d", n, packetFrameSize)
						}
						got = append(got, frame[:n*channels]...)

						// The middle FEC frame requests a longer duration than the
						// 20 ms packet with no LBRR available, so 20-40 ms of this
						// stream is packet-loss concealment. opus_compare's Q is not a
						// valid metric on extrapolated concealment (a silk_to_hybrid
						// transition scores Q<0 even though gopus matches libopus to
						// <3e-3 abs, corr~=0.99999, rms~=1.0), so gate on the trusted
						// near-exact corr/RMS bar and log Q. Steady-state per-mode Q is
						// covered by TestDecoderParityLibopusMatrix.
						assertAPIRateQualityFloat32PLC(t, got, want, sampleRate, channels, true, tc.name+" requested no-LBRR duration")
					})
				}
			}
		}
	}
}

func TestDecodeWithFECNilAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range apiRatePLCDurationCases() {
		for _, channels := range []int{1, 2} {
			packet := tc.packet(t, channels)
			for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
				t.Run(tc.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
					frameSize, err := packetSamplesAtRate(packet, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}

					steps := []libopusAPIRateDecodeStep{
						{packet: packet},
						{fec: true},
					}
					want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, frameSize, steps)
					if err != nil {
						libopustest.HelperUnavailable(t, "api-rate nil FEC reference decode", err)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got := make([]float32, 0, len(want))
					frame := make([]float32, frameSize*channels)
					n, err := dec.Decode(packet, frame)
					if err != nil {
						t.Fatalf("Decode seed: %v", err)
					}
					if n != frameSize {
						t.Fatalf("Decode seed samples=%d want %d", n, frameSize)
					}
					got = append(got, frame[:n*channels]...)

					n, err = dec.DecodeWithFEC(nil, frame, true)
					if err != nil {
						t.Fatalf("DecodeWithFEC(nil): %v", err)
					}
					if n != frameSize {
						t.Fatalf("DecodeWithFEC(nil) samples=%d want %d", n, frameSize)
					}
					got = append(got, frame[:n*channels]...)

					assertAPIRateQualityFloat32(t, got, want, sampleRate, channels, tc.name+" api-rate nil FEC decode")
				})
			}
		}
	}
}

func TestDecodeWithFECOverlongNoLBRRRequestMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		seedPacket := encodeAPIRateCELTPacket(t, channels)
		recoveryPacket := encodeAPIRateCELTPacketFrameSize(t, channels, 960)
		const sampleRate = 48000
		packetFrameSize, err := packetSamplesAtRate(seedPacket, sampleRate)
		if err != nil {
			t.Fatalf("packetSamplesAtRate: %v", err)
		}
		requestedFrameSize := overlongAPIRateRequestedFrameSize(sampleRate)

		t.Run("ch_"+itoaSmall(channels), func(t *testing.T) {
			steps := []libopusAPIRateDecodeStep{
				{packet: seedPacket},
				{packet: recoveryPacket, fec: true},
			}
			want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, requestedFrameSize, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "api-rate overlong no-LBRR FEC reference decode", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			got := make([]float32, 0, len(want))
			frame := make([]float32, requestedFrameSize*channels)

			n, err := dec.Decode(seedPacket, frame)
			if err != nil {
				t.Fatalf("Decode seed: %v", err)
			}
			if n != packetFrameSize {
				t.Fatalf("Decode seed samples=%d want %d", n, packetFrameSize)
			}
			got = append(got, frame[:n*channels]...)

			clear(frame)
			n, err = dec.DecodeWithFEC(recoveryPacket, frame, true)
			if err != nil {
				t.Fatalf("DecodeWithFEC no-LBRR: %v", err)
			}
			if n != requestedFrameSize {
				t.Fatalf("DecodeWithFEC no-LBRR samples=%d want %d", n, requestedFrameSize)
			}
			got = append(got, frame[:n*channels]...)

			assertAPIRateQualityFloat32(t, got, want, sampleRate, channels, "CELT overlong no-LBRR FEC request")
		})
	}
}

func TestDecodeWithFECInvalidRequestedFrameSizeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireLibopusAPIRateRefdecodeHelper(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateSILKPacket(t, channels)
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			for _, frameSize := range invalidAPIRateRequestedFrameSizes(sampleRate) {
				t.Run("nil_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate)+"_request_"+itoaSmall(frameSize), func(t *testing.T) {
					steps := []libopusAPIRateDecodeStep{{fec: true}}
					if _, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, frameSize, steps); err == nil {
						t.Fatalf("libopus DecodeWithFEC(nil) accepted frame_size=%d", frameSize)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					n, err := dec.DecodeWithFEC(nil, make([]float32, frameSize*channels), true)
					if n != 0 || err != ErrInvalidFrameSize {
						t.Fatalf("DecodeWithFEC(nil) = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
					}
				})
				t.Run("packet_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate)+"_request_"+itoaSmall(frameSize), func(t *testing.T) {
					steps := []libopusAPIRateDecodeStep{{packet: packet, fec: true}}
					if _, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, frameSize, steps); err == nil {
						t.Fatalf("libopus DecodeWithFEC(packet) accepted frame_size=%d", frameSize)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					n, err := dec.DecodeWithFEC(packet, make([]float32, frameSize*channels), true)
					if n != 0 || err != ErrInvalidFrameSize {
						t.Fatalf("DecodeWithFEC(packet) = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
					}
				})
			}
		}
	}
}

func TestDecodeWithFECLBRRAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name      string
		mode      EncoderMode
		wantMode  Mode
		bandwidth Bandwidth
		bitrate   int
		tolerance float64
		channels  []int
	}{
		{name: "silk_mb_stereo", mode: EncoderModeSILK, wantMode: ModeSILK, bandwidth: BandwidthMediumband, bitrate: 18000, tolerance: 8e-3, channels: []int{2}},
		{name: "silk_wb", mode: EncoderModeSILK, wantMode: ModeSILK, bandwidth: BandwidthWideband, bitrate: 24000, tolerance: 8e-3},
		{name: "hybrid", mode: EncoderModeHybrid, wantMode: ModeHybrid, bandwidth: BandwidthFullband, bitrate: 64000, tolerance: 1.2e-2},
	} {
		channelsSet := tc.channels
		if len(channelsSet) == 0 {
			channelsSet = []int{1, 2}
		}
		for _, channels := range channelsSet {
			seedPacket, recoveryPacket := encodeAPIRateFECSequence(t, tc.mode, tc.wantMode, tc.bandwidth, tc.bitrate, channels, 960)
			for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
				t.Run(tc.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
					frameSize, err := packetSamplesAtRate(recoveryPacket, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}

					steps := []libopusAPIRateDecodeStep{
						{packet: seedPacket},
						{packet: recoveryPacket, fec: true},
						{packet: recoveryPacket},
					}
					want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, frameSize, steps)
					if err != nil {
						libopustest.HelperUnavailable(t, "api-rate FEC reference decode", err)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got := make([]float32, 0, len(want))
					frame := make([]float32, frameSize*channels)
					n, err := dec.Decode(seedPacket, frame)
					if err != nil {
						t.Fatalf("Decode seed: %v", err)
					}
					if n != frameSize {
						t.Fatalf("Decode seed samples=%d want %d", n, frameSize)
					}
					got = append(got, frame[:n*channels]...)

					n, err = dec.DecodeWithFEC(recoveryPacket, frame, true)
					if err != nil {
						t.Fatalf("DecodeWithFEC recovery: %v", err)
					}
					if n != frameSize {
						t.Fatalf("DecodeWithFEC samples=%d want %d", n, frameSize)
					}
					got = append(got, frame[:n*channels]...)

					n, err = dec.Decode(recoveryPacket, frame)
					if err != nil {
						t.Fatalf("Decode recovery packet: %v", err)
					}
					if n != frameSize {
						t.Fatalf("Decode recovery packet samples=%d want %d", n, frameSize)
					}
					got = append(got, frame[:n*channels]...)

					assertAPIRateQualityFloat32(t, got, want, sampleRate, channels, tc.name+" api-rate FEC decode")
				})
			}
		}
	}
}

func TestDecodeWithFECSILKFinalRangeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		seedPacket, recoveryPacket := encodeAPIRateFECSequence(t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, channels, 960)
		for _, sampleRate := range []int{8000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				frameSize, err := packetSamplesAtRate(recoveryPacket, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}

				steps := []libopusAPIRateDecodeStep{
					{packet: seedPacket},
					{packet: recoveryPacket, fec: true},
				}
				_, ranges, err := decodeWithLibopusReferenceAPIRateFloat32StepsRanges(sampleRate, channels, frameSize, steps)
				if err != nil {
					libopustest.HelperUnavailable(t, "api-rate final range reference decode", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				frame := make([]float32, frameSize*channels)
				if _, err := dec.Decode(seedPacket, frame); err != nil {
					t.Fatalf("Decode seed: %v", err)
				}
				if got := dec.FinalRange(); got != ranges[0] {
					t.Fatalf("Decode seed FinalRange()=0x%08x want 0x%08x", got, ranges[0])
				}
				clear(frame)
				n, err := dec.DecodeWithFEC(recoveryPacket, frame, true)
				if err != nil {
					t.Fatalf("DecodeWithFEC recovery: %v", err)
				}
				if n != frameSize {
					t.Fatalf("DecodeWithFEC samples=%d want %d", n, frameSize)
				}
				if got := dec.FinalRange(); got != ranges[1] {
					t.Fatalf("DecodeWithFEC FinalRange()=0x%08x want 0x%08x", got, ranges[1])
				}
			})
		}
	}
}

func TestDecodeWithFECLBRRRequestedDurationMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name      string
		mode      EncoderMode
		wantMode  Mode
		bandwidth Bandwidth
		bitrate   int
		tolerance float64
	}{
		{name: "silk_wb", mode: EncoderModeSILK, wantMode: ModeSILK, bandwidth: BandwidthWideband, bitrate: 24000, tolerance: 8e-3},
		{name: "hybrid", mode: EncoderModeHybrid, wantMode: ModeHybrid, bandwidth: BandwidthFullband, bitrate: 64000, tolerance: 1.2e-2},
	} {
		for _, channels := range []int{1, 2} {
			seedPacket, recoveryPacket := encodeAPIRateFECSequence(t, tc.mode, tc.wantMode, tc.bandwidth, tc.bitrate, channels, 960)
			for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
				packetFrameSize, err := packetSamplesAtRate(recoveryPacket, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}
				for _, requestedFrameSize := range []int{sampleRate / 25, sampleRate * 3 / 50} {
					t.Run(tc.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate)+"_request_"+itoaSmall(requestedFrameSize), func(t *testing.T) {
						steps := []libopusAPIRateDecodeStep{
							{packet: seedPacket},
							{packet: recoveryPacket, fec: true},
							{packet: recoveryPacket},
						}
						want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, requestedFrameSize, steps)
						if err != nil {
							libopustest.HelperUnavailable(t, "api-rate requested LBRR reference decode", err)
						}

						dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
						if err != nil {
							t.Fatalf("NewDecoder: %v", err)
						}
						got := make([]float32, 0, len(want))
						frame := make([]float32, requestedFrameSize*channels)

						n, err := dec.Decode(seedPacket, frame)
						if err != nil {
							t.Fatalf("Decode seed: %v", err)
						}
						if n != packetFrameSize {
							t.Fatalf("Decode seed samples=%d want %d", n, packetFrameSize)
						}
						got = append(got, frame[:n*channels]...)

						clear(frame)
						n, err = dec.DecodeWithFEC(recoveryPacket, frame, true)
						if err != nil {
							t.Fatalf("DecodeWithFEC recovery: %v", err)
						}
						if n != requestedFrameSize {
							t.Fatalf("DecodeWithFEC samples=%d want requested %d", n, requestedFrameSize)
						}
						got = append(got, frame[:n*channels]...)

						clear(frame)
						n, err = dec.Decode(recoveryPacket, frame)
						if err != nil {
							t.Fatalf("Decode recovery packet: %v", err)
						}
						if n != packetFrameSize {
							t.Fatalf("Decode recovery packet samples=%d want %d", n, packetFrameSize)
						}
						got = append(got, frame[:n*channels]...)

						assertAPIRateQualityFloat32(t, got, want, sampleRate, channels, tc.name+" requested LBRR duration")
					})
				}
			}
		}
	}
}

func TestDecodeWithFECNilAfterLBRRAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name      string
		mode      EncoderMode
		wantMode  Mode
		bandwidth Bandwidth
		bitrate   int
		tolerance float64
		channels  []int
	}{
		{name: "silk_wb", mode: EncoderModeSILK, wantMode: ModeSILK, bandwidth: BandwidthWideband, bitrate: 24000, tolerance: 8e-3},
		{name: "hybrid", mode: EncoderModeHybrid, wantMode: ModeHybrid, bandwidth: BandwidthFullband, bitrate: 64000, tolerance: 1.2e-2},
	} {
		channelsSet := tc.channels
		if len(channelsSet) == 0 {
			channelsSet = []int{1, 2}
		}
		for _, channels := range channelsSet {
			seedPacket, recoveryPacket := encodeAPIRateFECSequence(t, tc.mode, tc.wantMode, tc.bandwidth, tc.bitrate, channels, 960)
			for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
				t.Run(tc.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
					frameSize, err := packetSamplesAtRate(recoveryPacket, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}

					steps := []libopusAPIRateDecodeStep{
						{packet: seedPacket},
						{packet: recoveryPacket},
						{fec: true},
					}
					want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, frameSize, steps)
					if err != nil {
						libopustest.HelperUnavailable(t, "api-rate nil FEC after LBRR reference decode", err)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got := make([]float32, 0, len(want))
					frame := make([]float32, frameSize*channels)
					for i, packet := range [][]byte{seedPacket, recoveryPacket} {
						n, err := dec.Decode(packet, frame)
						if err != nil {
							t.Fatalf("Decode packet[%d]: %v", i, err)
						}
						if n != frameSize {
							t.Fatalf("Decode packet[%d] samples=%d want %d", i, n, frameSize)
						}
						got = append(got, frame[:n*channels]...)
					}

					n, err := dec.DecodeWithFEC(nil, frame, true)
					if err != nil {
						t.Fatalf("DecodeWithFEC(nil): %v", err)
					}
					if n != frameSize {
						t.Fatalf("DecodeWithFEC(nil) samples=%d want %d", n, frameSize)
					}
					got = append(got, frame[:n*channels]...)

					assertAPIRateQualityFloat32(t, got, want, sampleRate, channels, tc.name+" api-rate nil FEC after LBRR decode")
				})
			}
		}
	}
}
