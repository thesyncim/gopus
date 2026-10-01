//go:build gopus_dred || gopus_osce

package gopus

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	silkpkg "github.com/thesyncim/gopus/internal/silk"
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

// TestDecodeDREDInt24MatchesLibopusInt24Reference compares the public Go
// int24 output with opus_decoder_dred_decode24() from the selected C build.
func TestDecodeDREDInt24MatchesLibopusInt24Reference(t *testing.T) {
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

			// The helper runs the carrier decode and DRED decode through opus_decode24
			// and opus_decoder_dred_decode24 on the same persistent decoder state.
			want, err := probeLibopusDecoderDREDSequenceInt24(
				nil, packetInfo.packet, nil,
				packetInfo.maxDREDSamples, packetInfo.sampleRate,
				n, libopusDecoderDREDSequenceSourceCarrierDRED,
				n, libopusDecoderDREDSequenceSourceNone, 0, false,
			)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED sequence", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "DRED int24 reference")
			if want.step0.ret != n {
				t.Fatalf("oracle DRED sequence step0.ret=%d want %d", want.step0.ret, n)
			}

			channels := dec.Channels()
			needed := n * channels
			pcmInt24 := make([]int32, needed)
			got, err := dec.DecodeDREDInt24(dred, n, pcmInt24, n)
			if err != nil {
				t.Fatalf("DecodeDREDInt24 error: %v", err)
			}
			if got != n {
				t.Fatalf("DecodeDREDInt24 returned %d want %d", got, n)
			}

			if len(want.step0.pcm24) != got*channels {
				t.Fatalf("libopus int24 sample count=%d want %d", len(want.step0.pcm24), got*channels)
			}

			label := fmt.Sprintf("DRED int24 reference frame_size=%d", frameSize)
			assertInt24ParitySelectedCExact(t, pcmInt24[:got*channels], want.step0.pcm24, packetInfo.sampleRate, channels, label)
		})
	}
}

// TestDecodeDREDInt24MatrixMatchesLibopusInt24Reference extends the int24 DRED
// recovery parity coverage from the single CELT-FB fixture exercised by
// TestDecodeDREDInt24MatchesLibopusInt24Reference to the full DRED mode/rate/
// channel matrix already gated for the float DRED decode path: SILK-WB, CELT-FB
// and Hybrid-SWB/FB, mono and stereo, at 48 kHz and 16 kHz decoder rates.
//
// For each mode, rate, and channel combination, the same-format C helper decodes
// the seed with opus_decode24(), parses the carrier DRED payload, and calls
// opus_decoder_dred_decode24() without decoding the carrier. This matches the
// explicit Go DRED path exercised here, which starts from the seeded decoder.
func TestDecodeDREDInt24MatrixMatchesLibopusInt24Reference(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name         string
		decoderRate  int
		cfg          libopusDREDPacketConfig
		wantChannels int
	}{
		{
			name:        "celt_fb_mono_48k_20ms",
			decoderRate: 48000,
			cfg:         libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeCELT, Bandwidth: BandwidthFullband},
		},
		{
			name:        "celt_fb_mono_48k_10ms",
			decoderRate: 48000,
			cfg:         libopusDREDPacketConfig{FrameSize: 480, ForceMode: ModeCELT, Bandwidth: BandwidthFullband},
		},
		{
			name:         "celt_fb_stereo_48k_20ms",
			decoderRate:  48000,
			cfg:          libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeCELT, Bandwidth: BandwidthFullband, Channels: 2, ForceChannels: 2},
			wantChannels: 2,
		},
		{
			name:        "celt_fb_mono_16k_20ms",
			decoderRate: 16000,
			cfg:         libopusDREDPacketConfig{FrameSize: 480, ForceMode: ModeCELT, Bandwidth: BandwidthFullband},
		},
		{
			name:         "celt_fb_stereo_16k_20ms",
			decoderRate:  16000,
			cfg:          libopusDREDPacketConfig{FrameSize: 480, ForceMode: ModeCELT, Bandwidth: BandwidthFullband, Channels: 2, ForceChannels: 2},
			wantChannels: 2,
		},
		{
			name:        "silk_wb_mono_48k_20ms",
			decoderRate: 48000,
			cfg:         libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeSILK, Bandwidth: BandwidthWideband},
		},
		{
			name:        "silk_wb_mono_16k_20ms",
			decoderRate: 16000,
			cfg:         libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeSILK, Bandwidth: BandwidthWideband},
		},
		{
			name:         "silk_wb_stereo_48k_20ms",
			decoderRate:  48000,
			cfg:          libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeSILK, Bandwidth: BandwidthWideband, Channels: 2, ForceChannels: 2},
			wantChannels: 2,
		},
		{
			name:        "hybrid_swb_mono_48k_20ms",
			decoderRate: 48000,
			cfg:         libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeHybrid, Bandwidth: BandwidthSuperwideband},
		},
		{
			name:        "hybrid_fb_mono_48k_20ms",
			decoderRate: 48000,
			cfg:         libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeHybrid, Bandwidth: BandwidthFullband},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, tc.decoderRate, tc.cfg)

			wantChannels := tc.wantChannels
			if wantChannels == 0 {
				wantChannels = 1
			}
			if dec.Channels() != wantChannels {
				t.Fatalf("int24 DRED matrix got decoder channels=%d, want %d", dec.Channels(), wantChannels)
			}

			maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, tc.decoderRate)
			want, err := probeLibopusDecoderDREDSequenceInt24WithoutCarrierDecode(
				seedPacket, packetInfo.packet, nil,
				maxDRED, oracleRate, n,
				libopusDecoderDREDSequenceSourceCarrierDRED, n,
				libopusDecoderDREDSequenceSourceNone, 0, false,
			)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED int24 decode", err)
			}
			requireLibopusDREDSequenceParsed(t, want, "libopus int24 DRED matrix")
			if want.step0.ret != n {
				t.Fatalf("libopus int24 DRED matrix decode ret=%d want %d", want.step0.ret, n)
			}
			if want.channels != wantChannels {
				t.Fatalf("libopus int24 DRED matrix channels=%d want %d", want.channels, wantChannels)
			}

			channels := dec.Channels()
			needed := n * channels
			pcmInt24 := make([]int32, needed)
			got, err := dec.DecodeDREDInt24(dred, n, pcmInt24, n)
			if err != nil {
				t.Fatalf("DecodeDREDInt24 error: %v", err)
			}
			if got != n {
				t.Fatalf("DecodeDREDInt24 returned %d want %d", got, n)
			}

			label := "DRED int24 matrix reference " + tc.name
			if len(want.step0.pcm24) != got*channels {
				t.Fatalf("%s sample count=%d want %d", label, len(want.step0.pcm24), got*channels)
			}
			for i, sample := range pcmInt24[:got*channels] {
				if sample != want.step0.pcm24[i] {
					t.Fatalf("%s sample[%d]=%d want selected C %d", label, i, sample, want.step0.pcm24[i])
				}
			}
			assertInt24ParitySelectedCExact(t, pcmInt24[:got*channels], want.step0.pcm24, tc.decoderRate, channels, label)
		})
	}
}

func TestDecodeDREDCarrierHistoryCodecStateMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cfg := libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeSILK, Bandwidth: BandwidthWideband}
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, cfg)
	maxDRED, oracleRate := libopusDREDRequestForDecoder(packetInfo, 48000)
	before, err := probeLibopusDecoderDREDSequence(seedPacket, packetInfo.packet, nil,
		maxDRED, oracleRate, n,
		libopusDecoderDREDSequenceSourceNone, 0,
		libopusDecoderDREDSequenceSourceNone, 0, false)
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED carrier pre-state", err)
	}
	requireLibopusDREDSequenceParsed(t, before, "libopus carrier pre-state")
	want, err := probeLibopusDecoderDREDSequenceWithSampleFormats(seedPacket, packetInfo.packet, nil,
		maxDRED, oracleRate, n,
		libopusDecoderDREDSequenceSourceCarrierDRED, n,
		libopusDecoderDREDSequenceSourceNone, 0, false,
		libopusDecoderDREDSequenceSampleFormatInt24,
		libopusDecoderDREDSequenceSampleFormatFloat32,
		libopusDecoderDREDSequenceSampleFormatFloat32,
		true)
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED carrier history state", err)
	}
	pcm := make([]float32, dec.maxPacketSamples*dec.Channels())
	if _, err := dec.Decode(packetInfo.packet, pcm); err != nil {
		t.Fatalf("Decode(carrier packet): %v", err)
	}
	if got := dec.FinalRange(); got != before.step0.finalRange {
		t.Fatalf("carrier pre-DRED final range=%08x want selected C %08x", got, before.step0.finalRange)
	}
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, before.step0.celt48k, "carrier pre-DRED CELT")
	assertDecoderDREDSILKStateBitsMatch(t, dec, before.step0.silk, silkpkg.BandwidthWideband, "carrier pre-DRED SILK")
	if !dec.dredNeuralConcealmentReady() {
		t.Fatal("DRED neural runtime did not initialize")
	}
	dec.primeExplicitSILKDREDEntryHistory()
	primed := requireDecoderDREDState(t, dec)
	assertDecoderDREDPLCStateBitsMatch(t, primed.dredPLC.Snapshot(), before.step0.state, "carrier primed pre-queue PLC")
	assertDecoderDREDFARGANStateBitsMatch(t, primed.dredFARGAN.Snapshot(), before.step0.fargan, "carrier primed pre-queue FARGAN")
	queued, err := probeLibopusDecoderDREDSequenceWithSampleFormats(seedPacket, packetInfo.packet, nil,
		maxDRED, oracleRate, n,
		libopusDecoderDREDSequenceSourceQueueCarrierDRED, n,
		libopusDecoderDREDSequenceSourceNone, 0, false,
		libopusDecoderDREDSequenceSampleFormatInt24,
		libopusDecoderDREDSequenceSampleFormatFloat32,
		libopusDecoderDREDSequenceSampleFormatFloat32,
		true)
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED queued carrier state", err)
	}
	dec.queueExplicitDREDRecovery(dred, n, n)
	queuedState := requireDecoderDREDState(t, dec)
	gotQueued := queuedState.dredPLC.Snapshot()
	assertDecoderDREDPLCStateBitsMatch(t, gotQueued, queued.step0.state, "carrier queued PLC")
	assertDecoderDREDFARGANStateBitsMatch(t, queuedState.dredFARGAN.Snapshot(), queued.step0.fargan, "carrier queued FARGAN")
	if gotQueued.FECFillPos != queued.step0.state.FECFillPos || gotQueued.FECSkip != queued.step0.state.FECSkip {
		t.Fatalf("carrier queued feature counts Go(fill=%d skip=%d) C(fill=%d skip=%d)", gotQueued.FECFillPos, gotQueued.FECSkip, queued.step0.state.FECFillPos, queued.step0.state.FECSkip)
	}
	for i := 0; i < gotQueued.FECFillPos; i++ {
		assertFloat32BitsEqual(t, gotQueued.FEC[i][:], queued.step0.state.FEC[i][:], fmt.Sprintf("carrier queued feature[%d]", i))
	}
	pcm24 := make([]int32, n*dec.Channels())
	if _, err := dec.DecodeDREDInt24(dred, n, pcm24, n); err != nil {
		t.Fatalf("DecodeDREDInt24 after carrier: %v", err)
	}
	state := requireDecoderDREDState(t, dec)
	assertDecoderDREDPLCStateBitsMatch(t, state.dredPLC.Snapshot(), want.step0.state, "carrier-history DRED PLC")
	assertDecoderDREDFARGANStateBitsMatch(t, state.dredFARGAN.Snapshot(), want.step0.fargan, "carrier-history DRED FARGAN")
	assertDecoderDREDCELT48kBridgeBitsMatch(t, dec, want.step0.celt48k, "carrier history CELT")
	assertDecoderDREDSILKStateBitsMatch(t, dec, want.step0.silk, silkpkg.BandwidthWideband, "carrier history SILK")
	if got := dec.FinalRange(); got != want.step0.finalRange {
		t.Fatalf("carrier DRED final range=%08x want selected C %08x", got, want.step0.finalRange)
	}
	if len(want.step0.pcm24) != len(pcm24) {
		t.Fatalf("carrier DRED int24 sample count=%d want %d", len(pcm24), len(want.step0.pcm24))
	}
	for i := range pcm24 {
		if pcm24[i] != want.step0.pcm24[i] {
			t.Fatalf("carrier DRED int24 sample[%d]=%d want selected C %d", i, pcm24[i], want.step0.pcm24[i])
		}
	}
}

// TestDecodeDREDInt24MatrixTracksDecodeDREDFloat verifies that across the full
// DRED mode/rate/channel matrix DecodeDREDInt24 equals RES2INT24 applied to the
// gopus float DRED decode (DecodeDRED) sample-for-sample. Both run the identical
// decodeExplicitDREDFloat float compute on the same host, so this is a strict
// byte/sample-exact equality on every architecture — it is independent of the
// libopus oracle and of the documented arm64 1-ULP float drift. It locks the
// int24 quantisation wiring (float32ToInt24Slice == per-sample RES2INT24) for
// the whole matrix rather than only the CELT-FB fixture.
func TestDecodeDREDInt24MatrixTracksDecodeDREDFloat(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name        string
		decoderRate int
		cfg         libopusDREDPacketConfig
	}{
		{name: "celt_fb_mono_48k_20ms", decoderRate: 48000, cfg: libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeCELT, Bandwidth: BandwidthFullband}},
		{name: "celt_fb_stereo_48k_20ms", decoderRate: 48000, cfg: libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeCELT, Bandwidth: BandwidthFullband, Channels: 2, ForceChannels: 2}},
		{name: "celt_fb_mono_16k_20ms", decoderRate: 16000, cfg: libopusDREDPacketConfig{FrameSize: 480, ForceMode: ModeCELT, Bandwidth: BandwidthFullband}},
		{name: "silk_wb_mono_48k_20ms", decoderRate: 48000, cfg: libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeSILK, Bandwidth: BandwidthWideband}},
		{name: "silk_wb_mono_16k_20ms", decoderRate: 16000, cfg: libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeSILK, Bandwidth: BandwidthWideband}},
		{name: "silk_wb_stereo_48k_20ms", decoderRate: 48000, cfg: libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeSILK, Bandwidth: BandwidthWideband, Channels: 2, ForceChannels: 2}},
		{name: "hybrid_swb_mono_48k_20ms", decoderRate: 48000, cfg: libopusDREDPacketConfig{FrameSize: 960, ForceMode: ModeHybrid, Bandwidth: BandwidthSuperwideband}},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Two decoders seeded identically advance through the same DRED parse
			// and process steps; DecodeDRED produces the float reference,
			// DecodeDREDInt24 must equal float32ToInt24(floatPCM) sample-for-sample.
			decFloat, dredFloat, _, _, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, tc.decoderRate, tc.cfg)
			decInt24, dredInt24, _, _, n2 := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, tc.decoderRate, tc.cfg)
			if n != n2 {
				t.Fatalf("seed frame count mismatch: float=%d int24=%d", n, n2)
			}

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

			for i := 0; i < gotFloat*channels; i++ {
				want := float32ToInt24(pcmFloat[i])
				if pcmInt24[i] != want {
					t.Fatalf("%s: pcmInt24[%d]=%d want float32ToInt24(%g)=%d",
						tc.name, i, pcmInt24[i], pcmFloat[i], want)
				}
			}
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
