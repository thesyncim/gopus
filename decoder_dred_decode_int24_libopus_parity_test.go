//go:build gopus_dred || gopus_extra_controls

package gopus

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestDecodeDREDInt24TracksDecodeDREDFloat verifies that Decoder.DecodeDREDInt24
// produces output that tracks Decoder.DecodeDRED via the same int24 conversion
// used by DecodeInt24 — matching libopus opus_decoder_dred_decode24()'s pattern
// of running opus_decode_native() in float mode then applying RES2INT24 to each
// sample (src/opus_decoder.c:1659-1663).
//
// Two decoders are seeded identically and advance through the same DRED parse
// and process steps; DecodeDRED produces the float reference, DecodeDREDInt24
// must equal float32ToInt24Slice(floatPCM) sample-for-sample.
func TestDecodeDREDInt24TracksDecodeDREDFloat(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{960, 480} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			decFloat, dredFloat, _, _, n := prepareExplicitDREDDecodeParityStateForFrameSize(t, frameSize)
			decInt24, dredInt24, _, _, _ := prepareExplicitDREDDecodeParityStateForFrameSize(t, frameSize)

			channels := decFloat.Channels()
			needed := n * channels

			pcmFloat := make([]float32, needed)
			gotFloat, err := decFloat.DecodeDRED(dredFloat, n, pcmFloat, n)
			if err != nil {
				t.Fatalf("DecodeDRED float error: %v", err)
			}
			if gotFloat != n {
				t.Fatalf("DecodeDRED float returned %d want %d", gotFloat, n)
			}

			pcmInt24 := make([]int32, needed)
			gotInt24, err := decInt24.DecodeDREDInt24(dredInt24, n, pcmInt24, n)
			if err != nil {
				t.Fatalf("DecodeDREDInt24 error: %v", err)
			}
			if gotInt24 != n {
				t.Fatalf("DecodeDREDInt24 returned %d want %d", gotInt24, n)
			}

			wantInt24 := make([]int32, needed)
			for i := 0; i < gotFloat*channels; i++ {
				wantInt24[i] = float32ToInt24(pcmFloat[i])
			}
			for i := 0; i < gotFloat*channels; i++ {
				if pcmInt24[i] != wantInt24[i] {
					t.Fatalf("pcmInt24[%d]=%d want float32ToInt24(%g)=%d",
						i, pcmInt24[i], pcmFloat[i], wantInt24[i])
				}
			}
		})
	}
}

// TestDecodeDREDInt24OracleQuality verifies that Decoder.DecodeDREDInt24
// output — converted back to float32 via int24/8388608 — meets the same
// near-exact quality bar against the libopus opus_decoder_dred_decode_float
// oracle as the float DRED decode tests.
//
// This mirrors the oracle-based CELT DRED float tests. The oracle PCM comes
// from the SourceCarrierDRED sequence (opus_decoder_dred_decode_float called
// in the same decoder context as after decoding the carrier packet), and
// assertDecodedPCMQuality verifies the decoded audio is near-exact.
//
// The int24 quantisation ≤1 LSB rounding is sub-perceptual: it cannot affect
// the quality score at the near-exact bar.
func TestDecodeDREDInt24OracleQuality(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{960, 480} {
		frameSize := frameSize
		t.Run(fmt.Sprintf("frame_size_%d", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthFullband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "dred packet", err)
			}

			dec, n := prepareCachedDREDDecodeParityStateForPacket(t, packetInfo)
			dred := parseCarrierDREDForExplicitDecode(t, packetInfo.sampleRate, packetInfo)

			// Oracle: opus_decoder_dred_decode_float in carrier-DRED context.
			want, err := probeLibopusDecoderDREDSequence(
				nil, packetInfo.packet, nil,
				packetInfo.maxDREDSamples, packetInfo.sampleRate,
				n, libopusDecoderDREDSequenceSourceCarrierDRED,
				n, libopusDecoderDREDSequenceSourceNone, 0, false,
			)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "DRED int24 oracle quality")
			if want.step0.ret != n {
				t.Fatalf("oracle DRED sequence step0.ret=%d want %d", want.step0.ret, n)
			}

			channels := dec.Channels()
			pcmInt24 := make([]int32, n*channels)
			got, err := dec.DecodeDREDInt24(dred, n, pcmInt24, n)
			if err != nil {
				t.Fatalf("DecodeDREDInt24 error: %v", err)
			}
			if got != n {
				t.Fatalf("DecodeDREDInt24 returned %d want %d", got, n)
			}

			// Convert int24 output back to float32 for quality comparison.
			gotFloat := int32Int24ToFloat32(pcmInt24[:got*channels])
			label := fmt.Sprintf("DRED int24 oracle quality frame_size=%d", frameSize)
			assertDecodedPCMQuality(t, gotFloat, want.step0.pcm[:got*channels], packetInfo.sampleRate, channels, label)
		})
	}
}

// TestDecodeDREDInt24BufferTooSmall verifies that DecodeDREDInt24 returns
// ErrBufferTooSmall when the output buffer is too small.
func TestDecodeDREDInt24BufferTooSmall(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, _, _, n := prepareExplicitDREDDecodeParityState(t)
	channels := dec.Channels()

	if _, err := dec.DecodeDREDInt24(dred, n, make([]int32, n*channels-1), n); err != ErrBufferTooSmall {
		t.Fatalf("DecodeDREDInt24(short buf) error=%v want %v", err, ErrBufferTooSmall)
	}
}

// TestDecodeDREDInt24InvalidFrameSize verifies that DecodeDREDInt24 returns
// ErrInvalidArgument for a zero frame size.
func TestDecodeDREDInt24InvalidFrameSize(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, _, _, n := prepareExplicitDREDDecodeParityState(t)
	channels := dec.Channels()

	if _, err := dec.DecodeDREDInt24(dred, n, make([]int32, n*channels), 0); err != ErrInvalidArgument {
		t.Fatalf("DecodeDREDInt24(frameSize=0) error=%v want %v", err, ErrInvalidArgument)
	}
}
