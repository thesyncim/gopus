package gopus

import (
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/qualitycompare"
	"math"
	"testing"
)

func TestDecodeGainLinearMatchesLibopusCELTExp2(t *testing.T) {
	libopustest.RequireOracle(t)
	gains := []int{-32768, -8192, -4096, -256, 256, 4096, 8192, 32767}
	inputs := make([]float32, len(gains))
	for i, gain := range gains {
		inputs[i] = float32(6.48814081e-4) * float32(gain)
	}
	want, err := libopustest.ProbeCELTMath(libopustest.CELTMathModeExp2, inputs)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT exp2 decode gain", err)
	}
	for i, gain := range gains {
		got := decodeGainLinear(gain)
		if math.Float32bits(got) != math.Float32bits(want[i]) {
			t.Fatalf("decodeGainLinear(%d)=%08x(%g) want %08x(%g)", gain, math.Float32bits(got), got, math.Float32bits(want[i]), want[i])
		}
	}
}

func TestDecodeCELTUsesAPIRatePacketDuration(t *testing.T) {
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateCELTPacket(t, channels)
		toc := ParseTOC(packet[0])
		if toc.Mode != ModeCELT {
			t.Fatalf("channels=%d test packet mode=%v want CELT", channels, toc.Mode)
		}
		if toc.Stereo != (channels == 2) {
			t.Fatalf("channels=%d packet stereo=%v", channels, toc.Stereo)
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
				n, err := dec.Decode(packet, pcm)
				if err != nil {
					t.Fatalf("Decode: %v", err)
				}
				if n != want {
					t.Fatalf("Decode samples=%d want %d", n, want)
				}

				plc := make([]float32, want*channels)
				n, err = dec.Decode(nil, plc)
				if err != nil {
					t.Fatalf("Decode PLC: %v", err)
				}
				if n != want {
					t.Fatalf("Decode PLC samples=%d want %d", n, want)
				}

				intDec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder int16: %v", err)
				}
				pcm16 := make([]int16, want*channels)
				n, err = intDec.DecodeInt16(packet, pcm16)
				if err != nil {
					t.Fatalf("DecodeInt16: %v", err)
				}
				if n != want {
					t.Fatalf("DecodeInt16 samples=%d want %d", n, want)
				}
			})
		}
	}
}

func TestDecodeCELTAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateCELTPacket(t, channels)
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				frameSize, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}

				sequence := [][]byte{packet, nil}
				want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, frameSize, sequence)
				if err != nil {
					libopustest.HelperUnavailable(t, "api-rate reference decode", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]float32, 0, len(want))
				frame := make([]float32, frameSize*channels)
				for i, pkt := range sequence {
					n, err := dec.Decode(pkt, frame)
					if err != nil {
						t.Fatalf("Decode sequence[%d]: %v", i, err)
					}
					if n != frameSize {
						t.Fatalf("Decode sequence[%d] samples=%d want %d", i, n, frameSize)
					}
					got = append(got, frame[:n*channels]...)
				}
				assertAPIRateQualityFloat32(t, got, want, sampleRate, channels, "CELT api-rate decode")
				assertAPIRateFloat32BitsExact(t, got, want, "CELT api-rate decode")
			})
		}
	}
}

func TestDecodeCELTRequestedPLCDurationMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateCELTPacket(t, channels)
		for _, sampleRate := range []int{8000, 16000, 48000} {
			packetFrameSize, err := packetSamplesAtRate(packet, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			for _, requestedFrameSize := range []int{sampleRate / 25, sampleRate * 3 / 50} {
				t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate)+"_request_"+itoaSmall(requestedFrameSize), func(t *testing.T) {
					sequence := [][]byte{packet, nil}
					want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, requestedFrameSize, sequence)
					if err != nil {
						libopustest.HelperUnavailable(t, "api-rate requested CELT PLC reference decode", err)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got := make([]float32, 0, len(want))
					frame := make([]float32, requestedFrameSize*channels)

					n, err := dec.Decode(packet, frame)
					if err != nil {
						t.Fatalf("Decode packet: %v", err)
					}
					if n != packetFrameSize {
						t.Fatalf("Decode packet samples=%d want %d", n, packetFrameSize)
					}
					got = append(got, frame[:n*channels]...)

					clear(frame)
					n, err = dec.Decode(nil, frame)
					if err != nil {
						t.Fatalf("Decode(nil): %v", err)
					}
					if n != requestedFrameSize {
						t.Fatalf("Decode(nil) samples=%d want requested %d", n, requestedFrameSize)
					}
					got = append(got, frame[:n*channels]...)

					assertAPIRateQualityFloat32PLC(t, got, want, sampleRate, channels, true, "CELT requested PLC duration")
					assertAPIRateFloat32BitsExact(t, got, want, "CELT requested PLC duration")
				})
			}
		}
	}
}

func TestDecodeInt16CELTRequestedPLCDurationMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateCELTPacket(t, channels)
		for _, sampleRate := range []int{8000, 16000, 48000} {
			packetFrameSize, err := packetSamplesAtRate(packet, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			for _, requestedFrameSize := range []int{sampleRate / 25, sampleRate * 3 / 50} {
				t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate)+"_request_"+itoaSmall(requestedFrameSize), func(t *testing.T) {
					sequence := [][]byte{packet, nil}
					want, err := decodeWithLibopusReferenceAPIRateInt16(sampleRate, channels, requestedFrameSize, sequence)
					if err != nil {
						libopustest.HelperUnavailable(t, "api-rate requested CELT int16 PLC reference decode", err)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got := make([]int16, 0, len(want))
					frame := make([]int16, requestedFrameSize*channels)

					n, err := dec.DecodeInt16(packet, frame)
					if err != nil {
						t.Fatalf("DecodeInt16 packet: %v", err)
					}
					if n != packetFrameSize {
						t.Fatalf("DecodeInt16 packet samples=%d want %d", n, packetFrameSize)
					}
					got = append(got, frame[:n*channels]...)

					clear(frame)
					n, err = dec.DecodeInt16(nil, frame)
					if err != nil {
						t.Fatalf("DecodeInt16(nil): %v", err)
					}
					if n != requestedFrameSize {
						t.Fatalf("DecodeInt16(nil) samples=%d want requested %d", n, requestedFrameSize)
					}
					got = append(got, frame[:n*channels]...)

					assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, true, "CELT requested int16 PLC duration")
					assertAPIRateInt16Exact(t, got, want, "CELT requested int16 PLC duration")
				})
			}
		}
	}
}

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

					assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, "CELT requested FEC duration")
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

					assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, tc.name+" api-rate no-LBRR FEC decode")
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
						assertSelectedPublicAPIRateFloat32PLC(t, got, want, sampleRate, channels, true, tc.name+" requested no-LBRR duration")
					})
				}
			}
		}
	}
}

// opus_decode_frame treats a payload of at most one byte as PLC even when the
// enclosing decode_fec call supplies a packet. The requested prefix and the
// empty recovery frame must follow the selected C decoder's state sequence.
func TestDecodeWithFECEmptyPayloadRequestedDurationMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		channels   = 2
		requested  = 5760
	)
	seed := encodeAPIRateSILKPacket(t, channels)
	for _, toc := range []byte{0x70, 0x58, 0x40} {
		t.Run("toc_"+itoaSmall(int(toc)), func(t *testing.T) {
			packet := []byte{toc}
			steps := []libopusAPIRateDecodeStep{{packet: seed}, {packet: packet, fec: true}}
			want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, requested, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "api-rate empty-payload FEC reference decode", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatal(err)
			}
			frame := make([]float32, requested*channels)
			seedSamples, err := dec.Decode(seed, frame)
			if err != nil {
				t.Fatalf("Decode seed: %v", err)
			}
			got := append([]float32(nil), frame[:seedSamples*channels]...)
			clear(frame)
			n, err := dec.DecodeWithFEC(packet, frame, true)
			if err != nil || n != requested {
				t.Fatalf("DecodeWithFEC empty packet=(%d,%v), want %d", n, err, requested)
			}
			got = append(got, frame[:n*channels]...)
			assertSelectedPublicAPIRateFloat32PLC(t, got, want, sampleRate, channels, true, "empty-payload FEC")
		})
	}
}

func TestDecodeWithFECNoLBRRPacketWarmZeroAllocs(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 2
		requested  = 1920
	)
	seed := encodeAPIRateSILKPacket(t, channels)
	recovery := encodeAPIRateHybridPacket(t, channels)
	if PacketHasLBRR(recovery) {
		t.Fatal("recovery packet unexpectedly carries LBRR")
	}
	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]float32, requested*channels)
	if _, err := dec.Decode(seed, frame); err != nil {
		t.Fatalf("Decode seed: %v", err)
	}
	for range 4 {
		if n, err := dec.DecodeWithFEC(recovery, frame, true); err != nil || n != requested {
			t.Fatalf("warm DecodeWithFEC=(%d,%v), want %d", n, err, requested)
		}
	}
	allocs := testing.AllocsPerRun(20, func() {
		if n, err := dec.DecodeWithFEC(recovery, frame, true); err != nil || n != requested {
			t.Fatalf("DecodeWithFEC=(%d,%v), want %d", n, err, requested)
		}
	})
	if allocs != 0 {
		t.Fatalf("warm DecodeWithFEC allocations=%g, want 0", allocs)
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

					assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, tc.name+" api-rate nil FEC decode")
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

			assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, "CELT overlong no-LBRR FEC request")
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

func TestDecodeWithFECInvalidRequestedSizePrecedesPacketParsing(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate = 48000
	const channels = 1
	const frameSize = sampleRate/400 + 1
	packet := []byte{0x03} // Code-3 packet missing its frame-count byte.

	want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, []libopustest.DecodeDiffCase{{
		Packet: packet, Format: libopustest.DecodeDiffFormatFloat32,
		FrameSize: frameSize, DecodeFEC: true,
	}})
	if err != nil {
		t.Fatalf("probe libopus invalid FEC request: %v", err)
	}
	if len(want) != 1 || want[0].Code != -1 {
		t.Fatalf("libopus result=%+v, want OPUS_BAD_ARG (-1) before parsing malformed packet", want)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	got, err := dec.DecodeWithFEC(packet, make([]float32, frameSize*channels), true)
	if got != 0 || err != ErrInvalidFrameSize {
		t.Fatalf("DecodeWithFEC(invalid size, malformed packet)=(%d,%v), want (0,%v)", got, err, ErrInvalidFrameSize)
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

					assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, tc.name+" api-rate FEC decode")
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

						assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, tc.name+" requested LBRR duration")
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

					assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, tc.name+" api-rate nil FEC after LBRR decode")
				})
			}
		}
	}
}

func TestDecodeInt16PLCNoSoftClipMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		frameSize  = 960
		gainQ8     = 8192
	)
	for _, channels := range []int{1, 2} {
		t.Run("ch_"+itoaSmall(channels), func(t *testing.T) {
			packet := encodeAPIRateCELTPacket(t, channels)
			steps := []libopusAPIRateDecodeStep{{packet: packet}, {}}
			want, err := decodeWithLibopusReferenceAPIRateInt16StepsGain(sampleRate, channels, frameSize, gainQ8, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "api-rate int16 PLC reference decode", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			if err := dec.SetGain(gainQ8); err != nil {
				t.Fatalf("SetGain(%d): %v", gainQ8, err)
			}

			got := make([]int16, 0, len(want))
			frame := make([]int16, frameSize*channels)
			n, err := dec.DecodeInt16(packet, frame)
			if err != nil {
				t.Fatalf("DecodeInt16 packet: %v", err)
			}
			if n != frameSize {
				t.Fatalf("DecodeInt16 packet samples=%d want %d", n, frameSize)
			}
			got = append(got, frame[:n*channels]...)

			clear(frame)
			n, err = dec.DecodeInt16(nil, frame)
			if err != nil {
				t.Fatalf("DecodeInt16(nil): %v", err)
			}
			if n != frameSize {
				t.Fatalf("DecodeInt16(nil) samples=%d want %d", n, frameSize)
			}
			got = append(got, frame[:n*channels]...)

			assertAPIRateQualityInt16(t, got, want, sampleRate, channels, "high-gain int16 PLC")
			assertAPIRateInt16Exact(t, got, want, "high-gain int16 PLC")
		})
	}
}

func TestDecodeInt16OverlongPLCRequestAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateCELTPacketFrameSize(t, channels, 960)
		const sampleRate = 48000
		packetFrameSize, err := packetSamplesAtRate(packet, sampleRate)
		if err != nil {
			t.Fatalf("packetSamplesAtRate: %v", err)
		}
		requestedFrameSize := overlongAPIRateRequestedFrameSize(sampleRate)

		t.Run("ch_"+itoaSmall(channels), func(t *testing.T) {
			sequence := [][]byte{packet, nil}
			want, err := decodeWithLibopusReferenceAPIRateInt16(sampleRate, channels, requestedFrameSize, sequence)
			if err != nil {
				libopustest.HelperUnavailable(t, "api-rate overlong int16 PLC reference decode", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			got := make([]int16, 0, len(want))
			frame := make([]int16, requestedFrameSize*channels)
			n, err := dec.DecodeInt16(packet, frame)
			if err != nil {
				t.Fatalf("DecodeInt16 packet: %v", err)
			}
			if n != packetFrameSize {
				t.Fatalf("DecodeInt16 packet samples=%d want %d", n, packetFrameSize)
			}
			got = append(got, frame[:n*channels]...)

			clear(frame)
			n, err = dec.DecodeInt16(nil, frame)
			if err != nil {
				t.Fatalf("DecodeInt16 nil: %v", err)
			}
			if n != requestedFrameSize {
				t.Fatalf("DecodeInt16 nil samples=%d want %d", n, requestedFrameSize)
			}
			got = append(got, frame[:n*channels]...)

			assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, true, "CELT overlong int16 PLC request")
			assertAPIRateInt16Exact(t, got, want, "CELT overlong int16 PLC request")
		})
	}
}

func TestDecodeInt16APIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name   string
		packet func(t *testing.T, channels int) []byte
	}{
		{name: "silk", packet: encodeAPIRateSILKPacket},
		{name: "celt", packet: encodeAPIRateCELTPacket},
		{name: "hybrid", packet: encodeAPIRateHybridPacket},
	} {
		for _, channels := range []int{1, 2} {
			packet := tc.packet(t, channels)
			for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
				t.Run(tc.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
					frameSize, err := packetSamplesAtRate(packet, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}
					sequence := [][]byte{packet, nil}
					want, err := decodeWithLibopusReferenceAPIRateInt16(sampleRate, channels, frameSize, sequence)
					if err != nil {
						libopustest.HelperUnavailable(t, "api-rate int16 reference decode", err)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got := make([]int16, 0, len(want))
					frame := make([]int16, frameSize*channels)
					for i, pkt := range sequence {
						n, err := dec.DecodeInt16(pkt, frame)
						if err != nil {
							t.Fatalf("DecodeInt16 sequence[%d]: %v", i, err)
						}
						if n != frameSize {
							t.Fatalf("DecodeInt16 sequence[%d] samples=%d want %d", i, n, frameSize)
						}
						got = append(got, frame[:n*channels]...)
					}
					assertAPIRateQualityInt16(t, got, want, sampleRate, channels, tc.name+" api-rate int16 decode")
					assertAPIRateInt16Exact(t, got, want, tc.name+" api-rate int16 decode")
				})
			}
		}
	}
}

func TestDecodeInt16PacketAfterShortPLCAPIRateMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateCELTPacket(t, channels)
		for _, sampleRate := range []int{16000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				frameSize, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}
				shortFrameSize := sampleRate / 400
				steps := []libopusAPIRateDecodeStep{
					{packet: packet, frameSize: frameSize},
					{frameSize: shortFrameSize},
					{packet: packet, frameSize: frameSize},
				}
				want, err := decodeWithLibopusReferenceAPIRateInt16VariableSteps(sampleRate, channels, frameSize, steps)
				if err != nil {
					libopustest.HelperUnavailable(t, "api-rate int16 variable reference decode", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]int16, 0, len(want))
				for i, step := range steps {
					frame := make([]int16, step.frameSize*channels)
					n, err := dec.DecodeInt16(step.packet, frame)
					if err != nil {
						t.Fatalf("DecodeInt16 sequence[%d]: %v", i, err)
					}
					if n != step.frameSize {
						t.Fatalf("DecodeInt16 sequence[%d] samples=%d want %d", i, n, step.frameSize)
					}
					got = append(got, frame[:n*channels]...)
				}
				assertAPIRateQualityInt16(t, got, want, sampleRate, channels, "packet-short-plc-packet int16 decode")
				assertAPIRateInt16Exact(t, got, want, "packet-short-plc-packet int16 decode")
			})
		}
	}
}

func TestDecodeInt16InvalidRequestedPLCFrameSizeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireLibopusAPIRateRefdecodeHelper(t)
	for _, channels := range []int{1, 2} {
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			for _, frameSize := range invalidAPIRateRequestedFrameSizes(sampleRate) {
				t.Run("int16_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate)+"_request_"+itoaSmall(frameSize), func(t *testing.T) {
					if _, err := decodeWithLibopusReferenceAPIRateInt16(sampleRate, channels, frameSize, [][]byte{nil}); err == nil {
						t.Fatalf("libopus DecodeInt16(nil) accepted frame_size=%d", frameSize)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					n, err := dec.DecodeInt16(nil, make([]int16, frameSize*channels))
					if n != 0 || err != ErrInvalidFrameSize {
						t.Fatalf("DecodeInt16(nil) = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
					}
				})
			}
		}
	}
}

func TestDecodeSILKUsesAPIRatePacketDuration(t *testing.T) {
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateSILKPacket(t, channels)
		toc := ParseTOC(packet[0])
		if toc.Mode != ModeSILK {
			t.Fatalf("channels=%d test packet mode=%v want SILK", channels, toc.Mode)
		}
		if toc.Stereo != (channels == 2) {
			t.Fatalf("channels=%d packet stereo=%v", channels, toc.Stereo)
		}
		if first, err := extractFirstFramePayload(packet, toc); err != nil {
			t.Fatalf("channels=%d extract first frame: %v", channels, err)
		} else if packetHasLBRR(first, toc) {
			t.Fatalf("channels=%d test packet unexpectedly contains LBRR", channels)
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
				n, err := dec.Decode(packet, pcm)
				if err != nil {
					t.Fatalf("Decode: %v", err)
				}
				if n != want {
					t.Fatalf("Decode samples=%d want %d", n, want)
				}

				smallDec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder small: %v", err)
				}
				if _, err := smallDec.Decode(packet, make([]float32, want*channels-1)); err != ErrBufferTooSmall {
					t.Fatalf("Decode small-buffer error=%v want %v", err, ErrBufferTooSmall)
				}

				plc := make([]float32, want*channels)
				n, err = dec.Decode(nil, plc)
				if err != nil {
					t.Fatalf("Decode PLC: %v", err)
				}
				if n != want {
					t.Fatalf("Decode PLC samples=%d want %d", n, want)
				}
			})
		}
	}
}

func TestDecodeSILKAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateSILKPacket(t, channels)
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				frameSize, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}

				sequence := [][]byte{packet, nil}
				want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, frameSize, sequence)
				if err != nil {
					libopustest.HelperUnavailable(t, "api-rate SILK reference decode", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]float32, 0, len(want))
				frame := make([]float32, frameSize*channels)
				for i, pkt := range sequence {
					n, err := dec.Decode(pkt, frame)
					if err != nil {
						t.Fatalf("Decode sequence[%d]: %v", i, err)
					}
					if n != frameSize {
						t.Fatalf("Decode sequence[%d] samples=%d want %d", i, n, frameSize)
					}
					got = append(got, frame[:n*channels]...)
				}
				assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, "SILK api-rate decode")
			})
		}
	}
}

func TestDecodeOutputGainFloat32MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		frameSize  = 960
		gainQ8     = 8192
	)
	for _, channels := range []int{1, 2} {
		t.Run("ch_"+itoaSmall(channels), func(t *testing.T) {
			packet := encodeAPIRateCELTPacket(t, channels)
			steps := []libopusAPIRateDecodeStep{{packet: packet}, {}}
			want, err := decodeWithLibopusReferenceAPIRateFloat32StepsGain(sampleRate, channels, frameSize, gainQ8, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "api-rate float32 output gain reference decode", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			if err := dec.SetGain(gainQ8); err != nil {
				t.Fatalf("SetGain(%d): %v", gainQ8, err)
			}

			got := make([]float32, 0, len(want))
			frame := make([]float32, frameSize*channels)
			n, err := dec.Decode(packet, frame)
			if err != nil {
				t.Fatalf("Decode packet: %v", err)
			}
			got = append(got, frame[:n*channels]...)
			clear(frame)
			n, err = dec.Decode(nil, frame)
			if err != nil {
				t.Fatalf("Decode(nil): %v", err)
			}
			got = append(got, frame[:n*channels]...)

			assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, "high-gain float32 output")
		})
	}
}

func TestDecodeHybridUsesAPIRatePacketDuration(t *testing.T) {
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateHybridPacket(t, channels)
		toc := ParseTOC(packet[0])
		if toc.Mode != ModeHybrid {
			t.Fatalf("channels=%d test packet mode=%v want Hybrid", channels, toc.Mode)
		}
		if toc.Stereo != (channels == 2) {
			t.Fatalf("channels=%d packet stereo=%v", channels, toc.Stereo)
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
				n, err := dec.Decode(packet, pcm)
				if err != nil {
					t.Fatalf("Decode: %v", err)
				}
				if n != want {
					t.Fatalf("Decode samples=%d want %d", n, want)
				}

				plc := make([]float32, want*channels)
				n, err = dec.Decode(nil, plc)
				if err != nil {
					t.Fatalf("Decode PLC: %v", err)
				}
				if n != want {
					t.Fatalf("Decode PLC samples=%d want %d", n, want)
				}

				intDec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder int16: %v", err)
				}
				pcm16 := make([]int16, want*channels)
				n, err = intDec.DecodeInt16(packet, pcm16)
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
				fecPCM := make([]float32, want*channels)
				n, err = fecDec.DecodeWithFEC(packet, fecPCM, true)
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

func TestDecodePLCDurationAPIRatePCMMatchesLibopus(t *testing.T) {
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

					sequence := [][]byte{packet, nil}
					want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, frameSize, sequence)
					if err != nil {
						libopustest.HelperUnavailable(t, "api-rate PLC duration reference decode", err)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got := make([]float32, 0, len(want))
					frame := make([]float32, frameSize*channels)
					for i, pkt := range sequence {
						n, err := dec.Decode(pkt, frame)
						if err != nil {
							t.Fatalf("Decode sequence[%d]: %v", i, err)
						}
						if n != frameSize {
							t.Fatalf("Decode sequence[%d] samples=%d want %d", i, n, frameSize)
						}
						got = append(got, frame[:n*channels]...)
					}

					assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, tc.name+" api-rate PLC duration decode")
				})
			}
		}
	}
}

func TestDecodeOverlongPLCRequestAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []apiRatePacketParityCase{
		{name: "silk_20ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateSILKPacketFrameSize(t, channels, 960) }, tolerance: 8e-3},
		{name: "celt_20ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateCELTPacketFrameSize(t, channels, 960) }, tolerance: 3e-3},
		{name: "hybrid_20ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateHybridPacketFrameSize(t, channels, 960) }, tolerance: 1e-2},
	} {
		for _, channels := range []int{1, 2} {
			packet := tc.packet(t, channels)
			for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
				t.Run(tc.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
					packetFrameSize, err := packetSamplesAtRate(packet, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}
					requestedFrameSize := overlongAPIRateRequestedFrameSize(sampleRate)
					if requestedFrameSize <= sampleRate*3/25 {
						t.Fatalf("requestedFrameSize=%d not over one 120ms chunk", requestedFrameSize)
					}

					sequence := [][]byte{packet, nil}
					want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, requestedFrameSize, sequence)
					if err != nil {
						libopustest.HelperUnavailable(t, "api-rate overlong PLC reference decode", err)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got := make([]float32, 0, len(want))
					frame := make([]float32, requestedFrameSize*channels)
					n, err := dec.Decode(packet, frame)
					if err != nil {
						t.Fatalf("Decode packet: %v", err)
					}
					if n != packetFrameSize {
						t.Fatalf("Decode packet samples=%d want %d", n, packetFrameSize)
					}
					got = append(got, frame[:n*channels]...)

					clear(frame)
					n, err = dec.Decode(nil, frame)
					if err != nil {
						t.Fatalf("Decode nil: %v", err)
					}
					if n != requestedFrameSize {
						t.Fatalf("Decode nil samples=%d want %d", n, requestedFrameSize)
					}
					got = append(got, frame[:n*channels]...)

					assertSelectedPublicAPIRateFloat32PLC(t, got, want, sampleRate, channels, true, tc.name+" overlong PLC request")
				})
			}
		}
	}
}

func TestDecodeHybridAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateHybridPacket(t, channels)
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				frameSize, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}

				sequence := [][]byte{packet, nil}
				want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, frameSize, sequence)
				if err != nil {
					libopustest.HelperUnavailable(t, "api-rate reference decode", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]float32, 0, len(want))
				frame := make([]float32, frameSize*channels)
				for i, pkt := range sequence {
					n, err := dec.Decode(pkt, frame)
					if err != nil {
						t.Fatalf("Decode sequence[%d]: %v", i, err)
					}
					if n != frameSize {
						t.Fatalf("Decode sequence[%d] samples=%d want %d", i, n, frameSize)
					}
					got = append(got, frame[:n*channels]...)
				}
				assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, "Hybrid api-rate decode")
			})
		}
	}
}

func TestDecodeMultiFrameAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []apiRatePacketParityCase{
		{
			name: "silk_code1_two_equal",
			packet: func(t *testing.T, channels int) []byte {
				packet := encodeAPIRateSILKPacketFrameSize(t, channels, 960)
				frame := firstAPIRateFramePayload(t, packet)
				return buildAPIRateMultiFramePacket(t, packet, [][]byte{frame, frame}, 1)
			},
			tolerance: 8e-3,
		},
		{
			name: "hybrid_code1_two_equal",
			packet: func(t *testing.T, channels int) []byte {
				packet := encodeAPIRateHybridPacketFrameSize(t, channels, 960)
				frame := firstAPIRateFramePayload(t, packet)
				return buildAPIRateMultiFramePacket(t, packet, [][]byte{frame, frame}, 1)
			},
			tolerance: 1e-2,
		},
		{
			name: "celt_code1_two_equal",
			packet: func(t *testing.T, channels int) []byte {
				packet := encodeAPIRateCELTPacketFrameSizeVariant(t, channels, 480, 64000, 0)
				frame := firstAPIRateFramePayload(t, packet)
				return buildAPIRateMultiFramePacket(t, packet, [][]byte{frame, frame}, 1)
			},
			tolerance: 3e-3,
		},
		{
			name: "celt_code2_two_vbr",
			packet: func(t *testing.T, channels int) []byte {
				packets := encodeAPIRateCELTPacketVariants(t, channels, 480, []int{64000, 96000}, 2)
				frames := firstAPIRateFramePayloads(t, packets)
				return buildAPIRateMultiFramePacket(t, packets[0], frames, 2)
			},
			tolerance: 3e-3,
		},
		{
			name: "celt_code3_three_cbr",
			packet: func(t *testing.T, channels int) []byte {
				packet := encodeAPIRateCELTPacketFrameSizeVariant(t, channels, 480, 64000, 1)
				frame := firstAPIRateFramePayload(t, packet)
				return buildAPIRateMultiFramePacket(t, packet, [][]byte{frame, frame, frame}, 3)
			},
			tolerance: 3e-3,
		},
		{
			name: "celt_code3_three_vbr",
			packet: func(t *testing.T, channels int) []byte {
				packets := encodeAPIRateCELTPacketVariants(t, channels, 480, []int{64000, 96000, 128000}, 3)
				frames := firstAPIRateFramePayloads(t, packets)
				return buildAPIRateMultiFramePacket(t, packets[0], frames, 3)
			},
			tolerance: 3e-3,
		},
	} {
		for _, channels := range []int{1, 2} {
			packet := tc.packet(t, channels)
			for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
				t.Run(tc.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
					frameSize, err := packetSamplesAtRate(packet, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}

					sequence := [][]byte{packet, nil}
					want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, frameSize, sequence)
					if err != nil {
						libopustest.HelperUnavailable(t, "api-rate multi-frame reference decode", err)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got := make([]float32, 0, len(want))
					frame := make([]float32, frameSize*channels)
					for i, pkt := range sequence {
						n, err := dec.Decode(pkt, frame)
						if err != nil {
							t.Fatalf("Decode sequence[%d]: %v", i, err)
						}
						if n != frameSize {
							t.Fatalf("Decode sequence[%d] samples=%d want %d", i, n, frameSize)
						}
						got = append(got, frame[:n*channels]...)
					}

					assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, tc.name+" api-rate multi-frame decode")
				})
			}
		}
	}
}

func TestDecodeColdPLCAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				frameSize := sampleRate / 50
				want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, frameSize, [][]byte{nil})
				if err != nil {
					libopustest.HelperUnavailable(t, "api-rate cold PLC reference decode", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]float32, frameSize*channels)
				n, err := dec.Decode(nil, got)
				if err != nil {
					t.Fatalf("Decode(nil): %v", err)
				}
				if n != frameSize {
					t.Fatalf("Decode(nil) samples=%d want %d", n, frameSize)
				}
				if dec.LastPacketDuration() != frameSize {
					t.Fatalf("LastPacketDuration()=%d want %d", dec.LastPacketDuration(), frameSize)
				}
				assertSelectedPublicAPIRateFloat32(t, got[:n*channels], want, sampleRate, channels, "cold PLC api-rate decode")

				dec.Reset()
				if dec.LastPacketDuration() != 0 {
					t.Fatalf("LastPacketDuration() after Reset=%d want 0", dec.LastPacketDuration())
				}
				clear(got)
				n, err = dec.Decode(nil, got)
				if err != nil {
					t.Fatalf("Decode(nil) after Reset: %v", err)
				}
				if n != frameSize {
					t.Fatalf("Decode(nil) after Reset samples=%d want %d", n, frameSize)
				}
				assertSelectedPublicAPIRateFloat32(t, got[:n*channels], want, sampleRate, channels, "reset cold PLC api-rate decode")
			})
		}
	}
}

func TestDecodeInvalidRequestedPLCFrameSizeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireLibopusAPIRateRefdecodeHelper(t)
	for _, channels := range []int{1, 2} {
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			for _, frameSize := range invalidAPIRateRequestedFrameSizes(sampleRate) {
				t.Run("float_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate)+"_request_"+itoaSmall(frameSize), func(t *testing.T) {
					if _, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, frameSize, [][]byte{nil}); err == nil {
						t.Fatalf("libopus Decode(nil) accepted frame_size=%d", frameSize)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					n, err := dec.Decode(nil, make([]float32, frameSize*channels))
					if n != 0 || err != ErrInvalidFrameSize {
						t.Fatalf("Decode(nil) = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
					}
				})
			}
		}
	}
}

func TestDecodeRejectsNonChannelMultipleRequestedFrameBuffer(t *testing.T) {
	const channels = 2
	packet := encodeAPIRateSILKPacket(t, channels)
	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		frameSize := sampleRate / 50
		sampleCount := frameSize*channels + 1
		t.Run("fs_"+itoaSmall(sampleRate), func(t *testing.T) {
			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			n, err := dec.Decode(nil, make([]float32, sampleCount))
			if n != 0 || err != ErrInvalidFrameSize {
				t.Fatalf("Decode(nil) = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
			}

			intDec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder int16: %v", err)
			}
			n, err = intDec.DecodeInt16(nil, make([]int16, sampleCount))
			if n != 0 || err != ErrInvalidFrameSize {
				t.Fatalf("DecodeInt16(nil) = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
			}

			fecDec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder FEC: %v", err)
			}
			n, err = fecDec.DecodeWithFEC(nil, make([]float32, sampleCount), true)
			if n != 0 || err != ErrInvalidFrameSize {
				t.Fatalf("DecodeWithFEC(nil) = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
			}

			packetFECDec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder packet FEC: %v", err)
			}
			n, err = packetFECDec.DecodeWithFEC(packet, make([]float32, sampleCount), true)
			if n != 0 || err != ErrInvalidFrameSize {
				t.Fatalf("DecodeWithFEC(packet) = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
			}
		})
	}
}

var apiRateSubRateBar = qualitycompare.QualityBar{
	MinQ:    math.Inf(-1), // opus_compare N/A here; Q is logged, not gated.
	MinCorr: 0.997,
	RMSLo:   0.98,
	RMSHi:   1.02,
	Desc:    "near-exact vs libopus (sub-48k/short/PLC-dominated: corr/RMS, opus_compare N/A)",
}

// opusCompareApplies reports whether opus_compare (and thus the Q gate) is valid
// for an api-rate decoded stream: 48 kHz with at least 480 samples per channel.
func opusCompareApplies(sampleRate, channels, totalSamples int) bool {
	return sampleRate == 48000 && channels > 0 && totalSamples/channels >= 480
}

// assertAPIRateQualityFloat32 checks the quality metrics for a decoded PCM
// stream with no PLC-dominated tail.
func assertAPIRateQualityFloat32(t *testing.T, got, want []float32, sampleRate, channels int, label string) {
	t.Helper()
	assertAPIRateQualityFloat32PLC(t, got, want, sampleRate, channels, false, label)
}

// assertAPIRateQualityFloat32PLC checks decoded-PCM quality metrics. got/want are interleaved
// 48 kHz-or-lower PCM that are sample-aligned vs libopus. plcDominated must be
// true when the requested output is mostly packet-loss concealment (a short real
// frame followed by a longer PLC tail), in which case the quality check uses
// correlation and RMS instead of opus_compare Q (see file header).
func assertAPIRateQualityFloat32PLC(t *testing.T, got, want []float32, sampleRate, channels int, plcDominated bool, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	if opusCompareApplies(sampleRate, channels, len(got)) && !plcDominated {
		// 48 kHz, >=10 ms, real content: opus_compare applies. Check its quality
		// bar and log the measured metrics.
		cmp, err := qualitycompare.CompareDecodedFloat32(got, want, sampleRate, channels, 0)
		if err != nil {
			t.Fatalf("%s CompareDecodedFloat32: %v", label, err)
		}
		qualitycompare.AssertQuality(t, cmp, qualitycompare.QualityBarNearExact, label)
		return
	}
	// Sub-48 kHz, short, or PLC-dominated output uses the rate-independent
	// waveform corr/RMS metrics. For 48 kHz PLC-dominated streams, also measure
	// Q for the log; it does not affect the quality bar.
	corr, rms := apiRateWaveformCorrelationRMS(got, want)
	q := 0.0
	if opusCompareApplies(sampleRate, channels, len(got)) {
		if cmp, err := qualitycompare.CompareDecodedFloat32(got, want, sampleRate, channels, 0); err == nil {
			q = cmp.Q
		}
	}
	cmp := qualitycompare.QualityComparison{Q: q, BestDelay: 0, Corr: corr, RMSRatio: rms}
	qualitycompare.AssertQuality(t, cmp, apiRateSubRateBar, label)
}

// apiRateWaveformCorrelationRMS mirrors qualitycompare's internal (unexported)
// waveform diagnostics: Pearson correlation and RMS(got)/RMS(want) over the
// common prefix. Used only for the sub-48k/short gate where opus_compare cannot
// run; the 48k path uses CompareDecodedFloat32 directly.
func apiRateWaveformCorrelationRMS(a, b []float32) (corr, rmsRatio float64) {
	n := min(len(b), len(a))
	if n == 0 {
		return 0, 0
	}
	var sumA, sumB, sumASq, sumBSq float64
	for i := range n {
		fa, fb := float64(a[i]), float64(b[i])
		sumA += fa
		sumB += fb
		sumASq += fa * fa
		sumBSq += fb * fb
	}
	meanA, meanB := sumA/float64(n), sumB/float64(n)
	var varA, varB, cov float64
	for i := range n {
		da, db := float64(a[i])-meanA, float64(b[i])-meanB
		cov += da * db
		varA += da * da
		varB += db * db
	}
	if varA > 0 && varB > 0 {
		corr = cov / math.Sqrt(varA*varB)
	} else if varA == 0 && varB == 0 {
		corr = 1
	}
	rmsA := math.Sqrt(sumASq / float64(n))
	rmsB := math.Sqrt(sumBSq / float64(n))
	if rmsB > 0 {
		rmsRatio = rmsA / rmsB
	} else if rmsA == 0 {
		rmsRatio = 1
	}
	return corr, rmsRatio
}

// assertAPIRateQualityInt16 converts both int16 streams to float32, scaled by
// 1/32768, and checks their waveform quality. Call assertAPIRateInt16Exact when
// the test also requires exact sample equality.
func assertAPIRateQualityInt16(t *testing.T, got, want []int16, sampleRate, channels int, label string) {
	t.Helper()
	assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, false, label)
}

func assertAPIRateQualityInt16PLC(t *testing.T, got, want []int16, sampleRate, channels int, plcDominated bool, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	gotF := int16SliceToFloat32(got)
	wantF := int16SliceToFloat32(want)
	assertAPIRateQualityFloat32PLC(t, gotF, wantF, sampleRate, channels, plcDominated, label)
}

func int16SliceToFloat32(in []int16) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v) / 32768.0
	}
	return out
}

// assertAPIRateInt16Exact compares integer PCM samples with the selected C
// reference. Quality metrics alone do not establish sample equality.
func assertAPIRateInt16Exact(t *testing.T, got, want []int16, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s sample[%d]=%d want %d", label, i, got[i], want[i])
		}
	}
}

func assertAPIRateFloat32BitsExact(t *testing.T, got, want []float32, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range want {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s sample[%d]=%08x want %08x", label, i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}

// The public API-rate reference helper selects the active libopus feature and
// instruction lane, then checks both quality metrics and every output bit.
// Call this only when want comes from decodeWithLibopusReferenceAPIRateFloat32.
func assertSelectedPublicAPIRateFloat32(t *testing.T, got, want []float32, sampleRate, channels int, label string) {
	t.Helper()
	assertSelectedPublicAPIRateFloat32PLC(t, got, want, sampleRate, channels, false, label)
}

func assertSelectedPublicAPIRateFloat32PLC(t *testing.T, got, want []float32, sampleRate, channels int, plcDominated bool, label string) {
	t.Helper()
	assertAPIRateQualityFloat32PLC(t, got, want, sampleRate, channels, plcDominated, label)
	assertAPIRateFloat32BitsExact(t, got, want, label)
}

const (
	libopusRefdecodeSingleFormatFloat32 = uint32(0)
	libopusRefdecodeSingleFormatInt16   = uint32(1)
)

var libopusAPIRateRefdecodeHelper libopustest.HelperCache

type apiRatePacketParityCase struct {
	name      string
	packet    func(t *testing.T, channels int) []byte
	tolerance float64
}

func apiRatePLCDurationCases() []apiRatePacketParityCase {
	return []apiRatePacketParityCase{
		{name: "silk_10ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateSILKPacketFrameSize(t, channels, 480) }, tolerance: 8e-3},
		{name: "silk_40ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateSILKPacketFrameSize(t, channels, 1920) }, tolerance: 8e-3},
		{name: "silk_60ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateSILKPacketFrameSize(t, channels, 2880) }, tolerance: 8e-3},
		{name: "celt_2p5ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateCELTPacketFrameSize(t, channels, 120) }, tolerance: 3e-3},
		{name: "celt_5ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateCELTPacketFrameSize(t, channels, 240) }, tolerance: 3e-3},
		{name: "celt_10ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateCELTPacketFrameSize(t, channels, 480) }, tolerance: 3e-3},
		{name: "celt_40ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateCELTPacketFrameSize(t, channels, 1920) }, tolerance: 3e-3},
		{name: "celt_60ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateCELTPacketFrameSize(t, channels, 2880) }, tolerance: 3e-3},
		{name: "hybrid_10ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateHybridPacketFrameSize(t, channels, 480) }, tolerance: 1e-2},
		{name: "hybrid_40ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateHybridPacketFrameSize(t, channels, 1920) }, tolerance: 1e-2},
		{name: "hybrid_60ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateHybridPacketFrameSize(t, channels, 2880) }, tolerance: 1e-2},
	}
}

func invalidAPIRateRequestedFrameSizes(sampleRate int) []int {
	quantum := sampleRate / 400
	return []int{quantum + 1, sampleRate/50 - 1, sampleRate/50 + 1}
}

func overlongAPIRateRequestedFrameSize(sampleRate int) int {
	return sampleRate / 5 // 200 ms, larger than one 120 ms internal PLC chunk.
}

func encodeAPIRateSILKPacket(t *testing.T, channels int) []byte {
	t.Helper()
	return encodeAPIRateSILKPacketFrameSize(t, channels, 960)
}

func encodeAPIRateSILKPacketFrameSize(t *testing.T, channels, frameSize int) []byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationRestrictedSilk,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthWideband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetInBandFEC(InBandFECDisabled); err != nil {
		t.Fatalf("SetInBandFEC: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	pcm := make([]float32, frameSize*channels)
	for i := range frameSize {
		left := 0.22 * float32(math.Sin(2*math.Pi*440*float64(i)/sampleRate))
		pcm[i*channels] = left
		if channels == 2 {
			pcm[i*channels+1] = 0.18 * float32(math.Sin(2*math.Pi*660*float64(i)/sampleRate))
		}
	}
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return packet
}

func encodeAPIRateCELTPacket(t *testing.T, channels int) []byte {
	t.Helper()
	return encodeAPIRateCELTPacketFrameSize(t, channels, 960)
}

func encodeAPIRateCELTPacketFrameSize(t *testing.T, channels, frameSize int) []byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationRestrictedCelt,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(128000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	pcm := make([]float32, frameSize*channels)
	for i := range frameSize {
		left := 0.28 * float32(math.Sin(2*math.Pi*1200*float64(i)/sampleRate))
		pcm[i*channels] = left
		if channels == 2 {
			pcm[i*channels+1] = 0.19 * float32(math.Sin(2*math.Pi*1900*float64(i)/sampleRate))
		}
	}
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return packet
}

func encodeAPIRateCELTPacketFrameSizeVariant(t *testing.T, channels, frameSize, bitrate, variant int) []byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationRestrictedCelt,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetBitrateMode(BitrateModeVBR); err != nil {
		t.Fatalf("SetBitrateMode(VBR): %v", err)
	}
	enc.SetVBRConstraint(false)
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	pcm := make([]float32, frameSize*channels)
	for i := range frameSize {
		tm := float64(variant*frameSize+i) / sampleRate
		left := 0.28*float32(math.Sin(2*math.Pi*(1200+float64(variant)*137)*tm+float64(variant)*0.11)) +
			0.05*float32(math.Sin(2*math.Pi*(2300+float64(variant)*91)*tm+0.23))
		pcm[i*channels] = left
		if channels == 2 {
			pcm[i*channels+1] = 0.19*float32(math.Sin(2*math.Pi*(1900+float64(variant)*151)*tm+0.31)) +
				0.04*float32(math.Sin(2*math.Pi*(3100+float64(variant)*73)*tm+0.07))
		}
	}
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return packet
}

func encodeAPIRateCELTPacketVariants(t *testing.T, channels, frameSize int, bitrates []int, count int) [][]byte {
	t.Helper()
	packets := make([][]byte, 0, 16)
	for variant := range 16 {
		bitrate := bitrates[variant%len(bitrates)]
		packet := encodeAPIRateCELTPacketFrameSizeVariant(t, channels, frameSize, bitrate, variant)
		if len(packets) > 0 && packet[0]&0xFC != packets[0][0]&0xFC {
			t.Fatalf("variant TOC base=0x%02x want 0x%02x", packet[0]&0xFC, packets[0][0]&0xFC)
		}
		packets = append(packets, packet)
		for start := 0; start+count <= len(packets); start++ {
			window := packets[start : start+count]
			if apiRatePacketsHaveUnequalPayloadSizes(window) {
				return append([][]byte(nil), window...)
			}
		}
	}
	t.Fatalf("failed to generate %d CELT VBR frames with unequal payload sizes", count)
	return nil
}

func encodeAPIRateHybridPacket(t *testing.T, channels int) []byte {
	t.Helper()
	return encodeAPIRateHybridPacketFrameSize(t, channels, 960)
}

func encodeAPIRateHybridPacketFrameSize(t *testing.T, channels, frameSize int) []byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationVoIP,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetMode(EncoderModeHybrid); err != nil {
		t.Fatalf("SetMode(Hybrid): %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(64000 * channels); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetInBandFEC(InBandFECDisabled); err != nil {
		t.Fatalf("SetInBandFEC: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	pcm := make([]float32, frameSize*channels)
	for i := range frameSize {
		tm := float64(i) / sampleRate
		pcm[i*channels] = 0.24*float32(math.Sin(2*math.Pi*220*tm)) +
			0.12*float32(math.Sin(2*math.Pi*1300*tm+0.17))
		if channels == 2 {
			pcm[i*channels+1] = 0.21*float32(math.Sin(2*math.Pi*330*tm+0.09)) +
				0.10*float32(math.Sin(2*math.Pi*1700*tm+0.31))
		}
	}
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return packet
}

func encodeAPIRateFECSequence(t *testing.T, mode EncoderMode, wantMode Mode, bandwidth Bandwidth, bitrate, channels, frameSize int) ([]byte, []byte) {
	t.Helper()
	const sampleRate = 48000
	app := ApplicationVoIP
	if mode == EncoderModeSILK {
		app = ApplicationRestrictedSilk
	}
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: app,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetMode(mode); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(bandwidth); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(bitrate * channels); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetSignal(SignalVoice); err != nil {
		t.Fatalf("SetSignal: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(20); err != nil {
		t.Fatalf("SetPacketLoss: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	packets := make([][]byte, 0, 12)
	for frameIndex := range 12 {
		pcm := make([]float32, frameSize*channels)
		for i := range frameSize {
			tm := float64(frameIndex*frameSize+i) / sampleRate
			pcm[i*channels] = 0.38*float32(math.Sin(2*math.Pi*220*tm)) +
				0.14*float32(math.Sin(2*math.Pi*440*tm+0.11))
			if channels == 2 {
				pcm[i*channels+1] = 0.33*float32(math.Sin(2*math.Pi*330*tm+0.07)) +
					0.12*float32(math.Sin(2*math.Pi*660*tm+0.19))
			}
		}
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Encode frame %d: %v", frameIndex, err)
		}
		if len(packet) == 0 {
			t.Fatalf("Encode frame %d produced no packet", frameIndex)
		}
		toc := ParseTOC(packet[0])
		if toc.Mode != wantMode {
			t.Fatalf("Encode frame %d mode=%v want %v", frameIndex, toc.Mode, wantMode)
		}
		packets = append(packets, append([]byte(nil), packet...))
		if len(packets) >= 3 && packetHasInBandFEC(t, packet) {
			return packets[len(packets)-3], packet
		}
	}
	t.Fatalf("failed to generate %v packet carrying LBRR", wantMode)
	return nil, nil
}

func firstAPIRateFramePayload(t *testing.T, packet []byte) []byte {
	t.Helper()
	_, frames, err := parsePacketFrames(packet)
	if err != nil {
		t.Fatalf("parsePacketFrames: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("single-frame packet has %d frames", len(frames))
	}
	return append([]byte(nil), frames[0]...)
}

func firstAPIRateFramePayloads(t *testing.T, packets [][]byte) [][]byte {
	t.Helper()
	frames := make([][]byte, len(packets))
	for i, packet := range packets {
		frames[i] = firstAPIRateFramePayload(t, packet)
	}
	return frames
}

func buildAPIRateMultiFramePacket(t *testing.T, basePacket []byte, frames [][]byte, wantFrameCode byte) []byte {
	t.Helper()
	data := make([]byte, maxPacketBytesPerStream)
	n, err := buildRepacketizedPacketWithOptions(basePacket[0]&0xFC, frames, data, 0, false, nil)
	if err != nil {
		t.Fatalf("buildRepacketizedPacketWithOptions: %v", err)
	}
	packet := append([]byte(nil), data[:n]...)
	info, parsed, err := parsePacketFrames(packet)
	if err != nil {
		t.Fatalf("parse repacketized packet: %v", err)
	}
	if info.TOC.FrameCode != wantFrameCode {
		t.Fatalf("frame code=%d want %d", info.TOC.FrameCode, wantFrameCode)
	}
	if len(parsed) != len(frames) {
		t.Fatalf("frame count=%d want %d", len(parsed), len(frames))
	}
	return packet
}

func apiRatePacketsHaveUnequalPayloadSizes(packets [][]byte) bool {
	if len(packets) < 2 {
		return false
	}
	firstLen := len(firstPacketPayloadForSizeCheck(packets[0]))
	for _, packet := range packets[1:] {
		if len(firstPacketPayloadForSizeCheck(packet)) != firstLen {
			return true
		}
	}
	return false
}

func firstPacketPayloadForSizeCheck(packet []byte) []byte {
	if len(packet) == 0 {
		return nil
	}
	if packet[0]&0x03 == 0 {
		return packet[1:]
	}
	_, frames, err := parsePacketFrames(packet)
	if err != nil || len(frames) == 0 {
		return nil
	}
	return frames[0]
}

type libopusAPIRateDecodeStep struct {
	packet    []byte
	frameSize int
	fec       bool
}

func getLibopusAPIRateRefdecodeHelperPath() (string, error) {
	return apiRateReferenceHelperPath()
}

func requireLibopusAPIRateRefdecodeHelper(t *testing.T) {
	t.Helper()
	if _, err := getLibopusAPIRateRefdecodeHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "api-rate reference decode", err)
	}
}

func decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, frameSize int, packets [][]byte) ([]float32, error) {
	steps := make([]libopusAPIRateDecodeStep, len(packets))
	for i, packet := range packets {
		steps[i] = libopusAPIRateDecodeStep{packet: packet}
	}
	return decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, frameSize, steps)
}

func decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, frameSize int, steps []libopusAPIRateDecodeStep) ([]float32, error) {
	return decodeWithLibopusReferenceAPIRateFloat32StepsGain(sampleRate, channels, frameSize, 0, steps)
}

func decodeWithLibopusReferenceAPIRateFloat32StepsGain(sampleRate, channels, frameSize, gainQ8 int, steps []libopusAPIRateDecodeStep) ([]float32, error) {
	binPath, err := getLibopusAPIRateRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 5, libopusRefdecodeSingleFormatFloat32, uint32(sampleRate), uint32(int32(gainQ8)), uint32(channels), uint32(frameSize), uint32(len(steps)))
	for _, step := range steps {
		if step.fec {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		payload.U32(uint32(len(step.packet)))
		payload.Raw(step.packet)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "api-rate reference decode", "GOSO")
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 4)
	decoded := make([]float32, nSamples)
	for i := range decoded {
		decoded[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return decoded, nil
}

func decodeWithLibopusReferenceAPIRateFloat32StepsRanges(sampleRate, channels, frameSize int, steps []libopusAPIRateDecodeStep) ([]float32, []uint32, error) {
	return decodeWithLibopusReferenceAPIRateFloat32StepsGainRanges(sampleRate, channels, frameSize, 0, steps)
}

func decodeWithLibopusReferenceAPIRateFloat32StepsGainRanges(sampleRate, channels, frameSize, gainQ8 int, steps []libopusAPIRateDecodeStep) ([]float32, []uint32, error) {
	binPath, err := getLibopusAPIRateRefdecodeHelperPath()
	if err != nil {
		return nil, nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 6, libopusRefdecodeSingleFormatFloat32, uint32(sampleRate), uint32(int32(gainQ8)), uint32(channels), uint32(frameSize), uint32(len(steps)))
	for _, step := range steps {
		if step.fec {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		payload.U32(uint32(len(step.packet)))
		payload.Raw(step.packet)
	}
	data, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return nil, nil, err
	}
	reader, version, err := libopustest.NewOracleReaderVersion("api-rate reference decode ranges", "GOSO", data)
	if err != nil {
		return nil, nil, err
	}
	if version != 2 {
		return nil, nil, fmt.Errorf("api-rate reference decode ranges helper version=%d want 2", version)
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples*4 + 4 + len(steps)*4)
	decoded := make([]float32, nSamples)
	for i := range decoded {
		decoded[i] = reader.Float32()
	}
	rangeCount := reader.Count(len(steps))
	ranges := make([]uint32, rangeCount)
	for i := range ranges {
		ranges[i] = reader.U32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, nil, err
	}
	return decoded, ranges, nil
}

func decodeWithLibopusReferenceAPIRateInt16(sampleRate, channels, frameSize int, packets [][]byte) ([]int16, error) {
	steps := make([]libopusAPIRateDecodeStep, len(packets))
	for i, packet := range packets {
		steps[i] = libopusAPIRateDecodeStep{packet: packet}
	}
	return decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize, steps)
}

func decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize int, steps []libopusAPIRateDecodeStep) ([]int16, error) {
	return decodeWithLibopusReferenceAPIRateInt16StepsGain(sampleRate, channels, frameSize, 0, steps)
}

func decodeWithLibopusReferenceAPIRateInt16StepsGain(sampleRate, channels, frameSize, gainQ8 int, steps []libopusAPIRateDecodeStep) ([]int16, error) {
	binPath, err := getLibopusAPIRateRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 5, libopusRefdecodeSingleFormatInt16, uint32(sampleRate), uint32(int32(gainQ8)), uint32(channels), uint32(frameSize), uint32(len(steps)))
	for _, step := range steps {
		if step.fec {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		payload.U32(uint32(len(step.packet)))
		payload.Raw(step.packet)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "api-rate reference decode", "GOSO")
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 2)
	decoded := make([]int16, nSamples)
	for i := range decoded {
		decoded[i] = reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return decoded, nil
}

func decodeWithLibopusReferenceAPIRateInt16VariableSteps(sampleRate, channels, maxFrameSize int, steps []libopusAPIRateDecodeStep) ([]int16, error) {
	binPath, err := getLibopusAPIRateRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 7, libopusRefdecodeSingleFormatInt16, uint32(sampleRate), 0, uint32(channels), uint32(maxFrameSize), uint32(len(steps)))
	for _, step := range steps {
		if step.fec {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		frameSize := step.frameSize
		if frameSize == 0 {
			frameSize = maxFrameSize
		}
		payload.U32(uint32(frameSize))
		payload.U32(uint32(len(step.packet)))
		payload.Raw(step.packet)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "api-rate reference decode", "GOSO")
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 2)
	decoded := make([]int16, nSamples)
	for i := range decoded {
		decoded[i] = reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return decoded, nil
}
