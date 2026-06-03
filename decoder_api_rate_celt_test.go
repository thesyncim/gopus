package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
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
					if celtIntegerPLCActive && sampleRate == 48000 {
						t.Skip("48k CELT PLC routes to the integer decoder under gopus_fixedpoint (vs float oracle); see TestDecoderFixedPointCELTPLCParity")
					}
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
				})
			}
		}
	}
}
