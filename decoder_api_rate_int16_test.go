package gopus

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

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
			if celtIntegerPLCActive {
				t.Skip("48k CELT PLC routes to the integer decoder under gopus_fixedpoint (vs float oracle); see TestDecoderFixedPointCELTPLCParity")
			}
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
					if celtIntegerPLCActive && tc.name == "celt" && sampleRate == 48000 {
						t.Skip("48k CELT PLC (sequence ends in a lost frame) routes to the integer decoder under gopus_fixedpoint (vs float oracle); see TestDecoderFixedPointCELTPLCParity")
					}
					if hybridIntegerPLCActive && tc.name == "hybrid" && sampleRate == 48000 {
						t.Skip("48k Hybrid PLC (sequence ends in a lost frame) routes to the integer decoder under gopus_fixedpoint (vs float oracle); see TestDecodeDifferentialFixedPointPLC")
					}
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
				})
			}
		}
	}
}

func TestDecodeInt16PacketAfterShortPLCAPIRateMatchesLibopus(t *testing.T) {
	if celtIntegerPLCActive {
		t.Skip("CELT decode + PLC route to the integer FIXED_POINT decoder under gopus_fixedpoint, diverging from this float oracle; bit-exact CELT PLC is gated by TestDecoderFixedPointCELTPLCParity")
	}
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
