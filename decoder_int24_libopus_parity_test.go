package gopus

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// libopusRefdecodeSingleFormatInt24 selects opus_decode24() in the single-stream
// C helper (SAMPLE_FORMAT_INT24 = 2, libopus_refdecode_single.c).
const libopusRefdecodeSingleFormatInt24 = uint32(2)

// decodeWithLibopusReferenceAPIRateInt24 drives libopus opus_decode24() through
// the refdecode_single C helper for a sequence of packets, mirroring the pattern
// used by decodeWithLibopusReferenceAPIRateInt16.
func decodeWithLibopusReferenceAPIRateInt24(sampleRate, channels, frameSize int, packets [][]byte) ([]int32, error) {
	steps := make([]libopusAPIRateDecodeStep, len(packets))
	for i, pkt := range packets {
		steps[i] = libopusAPIRateDecodeStep{packet: pkt}
	}
	return decodeWithLibopusReferenceAPIRateInt24Steps(sampleRate, channels, frameSize, steps)
}

func decodeWithLibopusReferenceAPIRateInt24Steps(sampleRate, channels, frameSize int, steps []libopusAPIRateDecodeStep) ([]int32, error) {
	binPath, err := getLibopusAPIRateRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	// Protocol version 5: magic GOSI + version + format + sample_rate + gain +
	// channels + frame_size + packet_count.
	payload := libopustest.NewOraclePayloadVersion("GOSI", 5,
		libopusRefdecodeSingleFormatInt24,
		uint32(sampleRate),
		0, // gain
		uint32(channels),
		uint32(frameSize),
		uint32(len(steps)),
	)
	for _, step := range steps {
		if step.fec {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		payload.U32(uint32(len(step.packet)))
		payload.Raw(step.packet)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "api-rate reference decode int24", "GOSO")
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 4)
	decoded := make([]int32, nSamples)
	for i := range decoded {
		decoded[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return decoded, nil
}

// int32Int24ToFloat32 converts an int32 slice of right-justified 24-bit samples
// to float32 by dividing by 8388608 (= 2^23), matching libopus INT24TORES for
// float builds: (1.f/32768.f/256.f) * a.
func int32Int24ToFloat32(in []int32) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v) / 8388608.0
	}
	return out
}

// assertInt24ParityNearExact converts both int32 int24 streams to float32 and
// applies the trusted near-exact quality bar, matching the approach used for
// int16 parity (assertAPIRateQualityInt16). This correctly absorbs the
// documented darwin/arm64 1-ULP float drift that produces ≤1 LSB int24
// divergence on CELT/Hybrid modes.
func assertInt24ParityNearExact(t *testing.T, got, want []int32, sampleRate, channels int, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	assertAPIRateQualityFloat32(t, int32Int24ToFloat32(got), int32Int24ToFloat32(want), sampleRate, channels, label)
}

// TestDecodeInt24SILKAPIRatePCMMatchesLibopus verifies that Decoder.DecodeInt24
// produces bit-exact output vs libopus opus_decode24() for SILK packets.
// SILK float32 decode is bit-exact vs libopus on all architectures, so the
// int24 conversion is also bit-exact.
func TestDecodeInt24SILKAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateSILKPacket(t, channels)
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				frameSize, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}

				sequence := [][]byte{packet}
				want, err := decodeWithLibopusReferenceAPIRateInt24(sampleRate, channels, frameSize, sequence)
				if err != nil {
					libopustest.HelperUnavailable(t, "api-rate SILK reference decode int24", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]int32, frameSize*channels)
				n, err := dec.DecodeInt24(packet, got)
				if err != nil {
					t.Fatalf("DecodeInt24: %v", err)
				}
				if n != frameSize {
					t.Fatalf("DecodeInt24 samples=%d want %d", n, frameSize)
				}
				got = got[:n*channels]
				// SILK: bit-exact on all architectures.
				if len(got) != len(want) {
					t.Fatalf("DecodeInt24 len=%d want %d", len(got), len(want))
				}
				for i := range got {
					if got[i] != want[i] {
						t.Fatalf("DecodeInt24[%d]=%d want %d", i, got[i], want[i])
					}
				}
			})
		}
	}
}

// TestDecodeInt24CELTAPIRatePCMMatchesLibopus verifies that Decoder.DecodeInt24
// produces near-exact output vs libopus opus_decode24() for CELT packets.
//
// The ≤1 LSB tolerance absorbs the documented darwin/arm64 1-ULP float drift
// in the CELT path; CI (amd64) is bit-exact. This matches the trusted
// near-exact bar used for the float32/int16 CELT decode tests.
func TestDecodeInt24CELTAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateCELTPacket(t, channels)
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				frameSize, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}

				sequence := [][]byte{packet}
				want, err := decodeWithLibopusReferenceAPIRateInt24(sampleRate, channels, frameSize, sequence)
				if err != nil {
					libopustest.HelperUnavailable(t, "api-rate CELT reference decode int24", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]int32, frameSize*channels)
				n, err := dec.DecodeInt24(packet, got)
				if err != nil {
					t.Fatalf("DecodeInt24: %v", err)
				}
				if n != frameSize {
					t.Fatalf("DecodeInt24 samples=%d want %d", n, frameSize)
				}
				got = got[:n*channels]
				// CELT: ≤1 LSB tolerance for the arm64 1-ULP float drift.
				assertInt24ParityNearExact(t, got, want, sampleRate, channels, "CELT int24 decode")
			})
		}
	}
}

// TestDecodeInt24HybridAPIRatePCMMatchesLibopus verifies Decoder.DecodeInt24
// for Hybrid (SILK+CELT) packets vs libopus opus_decode24().
func TestDecodeInt24HybridAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateHybridPacket(t, channels)
		for _, sampleRate := range []int{16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				frameSize, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}

				sequence := [][]byte{packet}
				want, err := decodeWithLibopusReferenceAPIRateInt24(sampleRate, channels, frameSize, sequence)
				if err != nil {
					libopustest.HelperUnavailable(t, "api-rate Hybrid reference decode int24", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]int32, frameSize*channels)
				n, err := dec.DecodeInt24(packet, got)
				if err != nil {
					t.Fatalf("DecodeInt24: %v", err)
				}
				if n != frameSize {
					t.Fatalf("DecodeInt24 samples=%d want %d", n, frameSize)
				}
				got = got[:n*channels]
				// Hybrid: ≤1 LSB tolerance for the arm64 1-ULP float drift.
				assertInt24ParityNearExact(t, got, want, sampleRate, channels, "Hybrid int24 decode")
			})
		}
	}
}

// TestDecodeInt24TracksFloat32Decode verifies that Decoder.DecodeInt24 output
// is consistent with Decoder.Decode (float32) output via the int24 conversion,
// confirming that DecodeInt24 shares the same decode path and differs only in
// the final sample format conversion.
func TestDecodeInt24TracksFloat32Decode(t *testing.T) {
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateCELTPacket(t, channels)
		const sampleRate = 48000
		frameSize, err := packetSamplesAtRate(packet, sampleRate)
		if err != nil {
			t.Fatalf("packetSamplesAtRate: %v", err)
		}
		t.Run("ch_"+itoaSmall(channels), func(t *testing.T) {
			decF := mustNewTestDecoder(t, sampleRate, channels)
			pcmF := make([]float32, frameSize*channels)
			nF, err := decF.Decode(packet, pcmF)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}

			dec24 := mustNewTestDecoder(t, sampleRate, channels)
			pcm24 := make([]int32, frameSize*channels)
			n24, err := dec24.DecodeInt24(packet, pcm24)
			if err != nil {
				t.Fatalf("DecodeInt24: %v", err)
			}

			if nF != n24 {
				t.Fatalf("sample count mismatch: float32=%d int24=%d", nF, n24)
			}
			for i := 0; i < nF*channels; i++ {
				want := float32ToInt24(pcmF[i])
				if pcm24[i] != want {
					t.Fatalf("pcm24[%d]=%d want float32ToInt24(%g)=%d", i, pcm24[i], pcmF[i], want)
				}
			}
		})
	}
}

// TestDecodeInt24SliceMatchesDecodeInt24 verifies that DecodeInt24Slice returns
// the same samples as DecodeInt24 with a pre-allocated buffer.
func TestDecodeInt24SliceMatchesDecodeInt24(t *testing.T) {
	packet := encodeAPIRateCELTPacket(t, 1)
	const sampleRate = 48000
	frameSize, err := packetSamplesAtRate(packet, sampleRate)
	if err != nil {
		t.Fatalf("packetSamplesAtRate: %v", err)
	}

	dec1 := mustNewTestDecoder(t, sampleRate, 1)
	buf := make([]int32, frameSize)
	n, err := dec1.DecodeInt24(packet, buf)
	if err != nil {
		t.Fatalf("DecodeInt24: %v", err)
	}
	want := buf[:n]

	dec2 := mustNewTestDecoder(t, sampleRate, 1)
	got, err := dec2.DecodeInt24Slice(packet)
	if err != nil {
		t.Fatalf("DecodeInt24Slice: %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("DecodeInt24Slice len=%d want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("sample[%d]=%d want %d", i, got[i], want[i])
		}
	}
}

// TestDecodeInt24InvalidFrameSize verifies that DecodeInt24 returns
// ErrBufferTooSmall when the output buffer is too small.
func TestDecodeInt24InvalidFrameSize(t *testing.T) {
	packet := encodeAPIRateCELTPacket(t, 1)
	dec := mustNewTestDecoder(t, 48000, 1)

	if _, err := dec.DecodeInt24(packet, nil); err != ErrBufferTooSmall {
		t.Fatalf("DecodeInt24(nil pcm) error=%v want %v", err, ErrBufferTooSmall)
	}
	if _, err := dec.DecodeInt24(packet, make([]int32, 1)); err != ErrBufferTooSmall {
		t.Fatalf("DecodeInt24(short pcm) error=%v want %v", err, ErrBufferTooSmall)
	}
}

// TestDecodeInt24PLCMatchesLibopus verifies that Decoder.DecodeInt24 with
// a nil packet (PLC) matches libopus opus_decode24(NULL, ...) within the
// trusted near-exact bar.
func TestDecodeInt24PLCMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateCELTPacket(t, channels)
		const sampleRate = 48000
		frameSize, err := packetSamplesAtRate(packet, sampleRate)
		if err != nil {
			t.Fatalf("packetSamplesAtRate: %v", err)
		}
		t.Run("ch_"+itoaSmall(channels), func(t *testing.T) {
			sequence := [][]byte{packet, nil}
			want, err := decodeWithLibopusReferenceAPIRateInt24(sampleRate, channels, frameSize, sequence)
			if err != nil {
				libopustest.HelperUnavailable(t, "api-rate CELT reference decode int24 PLC", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			got := make([]int32, 0, len(want))
			buf := make([]int32, frameSize*channels)
			for i, pkt := range sequence {
				n, err := dec.DecodeInt24(pkt, buf)
				if err != nil {
					t.Fatalf("DecodeInt24 seq[%d]: %v", i, err)
				}
				got = append(got, buf[:n*channels]...)
			}
			assertInt24ParityNearExact(t, got, want, sampleRate, channels, "CELT int24 PLC decode")
		})
	}
}
