package gopus

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

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
				assertAPIRateQualityFloat32(t, got, want, sampleRate, channels, "SILK api-rate decode")
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

			assertAPIRateQualityFloat32(t, got, want, sampleRate, channels, "high-gain float32 output")
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

					assertAPIRateQualityFloat32(t, got, want, sampleRate, channels, tc.name+" api-rate PLC duration decode")
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

					assertAPIRateQualityFloat32PLC(t, got, want, sampleRate, channels, true, tc.name+" overlong PLC request")
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
				assertAPIRateQualityFloat32(t, got, want, sampleRate, channels, "Hybrid api-rate decode")
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

					assertAPIRateQualityFloat32(t, got, want, sampleRate, channels, tc.name+" api-rate multi-frame decode")
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
				assertAPIRateQualityFloat32(t, got[:n*channels], want, sampleRate, channels, "cold PLC api-rate decode")

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
				assertAPIRateQualityFloat32(t, got[:n*channels], want, sampleRate, channels, "reset cold PLC api-rate decode")
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
