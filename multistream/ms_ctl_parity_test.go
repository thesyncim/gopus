// Multistream encoder/decoder CTL surface parity.
//
// Asserts that the gopus multistream Encoder and Decoder CTL surface matches
// the libopus opus_multistream_encoder_ctl / opus_multistream_decoder_ctl
// broadcast semantics documented in opus_multistream.h (libopus 1.6.1).
//
// Key CTL broadcast rules (from opus_multistream.c libopus 1.6.1):
//   - SET CTLs are applied to every per-stream encoder/decoder.
//   - GET CTLs read back from the first stream (stream 0).
//   - OPUS_GET_FINAL_RANGE XORs all per-stream final range values.
//   - Per-stream state access via OPUS_MULTISTREAM_GET_ENCODER_STATE /
//     OPUS_MULTISTREAM_GET_DECODER_STATE allows stream-individual CTLs.
//
// These tests probe the internal per-stream state directly to verify that
// broadcast semantics are respected, and gate decoded audio against the
// libopus oracle to confirm the effect is identical.

package multistream

import (
	"errors"
	"testing"

	internalenc "github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/qualitycompare"
	"github.com/thesyncim/gopus/types"
)

// ---------------------------------------------------------------------------
// Decoder CTL broadcast parity
// ---------------------------------------------------------------------------

// TestMSDecoderCTL_GainBroadcast asserts that SetGain broadcasts to all per-
// stream decoders and that the decoded audio matches the libopus oracle with
// the same gain applied.
// C ref: opus_multistream_decoder_ctl, OPUS_SET_GAIN, opus_multistream.c libopus 1.6.1
func TestMSDecoderCTL_GainBroadcast(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		channels  = 3
		sampleRate = 48000
		gainQ8    = 8192 // 32 dB Q8 ≈ +4 dB
		bitrate   = 192000
	)
	frameSize := sampleRate / 50

	streams, coupled, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("DefaultMapping: %v", err)
	}

	enc, err := NewEncoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	enc.SetMode(internalenc.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(bitrate)

	packet, err := enc.Encode(generateTestSignal(channels, frameSize, sampleRate, 997), frameSize)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// libopus oracle decode with gain
	want, err := decodeWithLibopusReferencePacketsGain(
		1, sampleRate, channels, streams, coupled, frameSize, gainQ8,
		mapping, nil, [][]byte{packet},
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "reference gain decode", err)
	}

	// gopus decode with SetGain
	dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if err := dec.SetGain(gainQ8); err != nil {
		t.Fatalf("SetGain(%d): %v", gainQ8, err)
	}

	// Assert broadcast: every per-stream decoder must have the gain set.
	for i, sd := range dec.decoders {
		st := sd.(*streamState)
		if got := st.Gain(); got != gainQ8 {
			t.Fatalf("stream %d Gain()=%d want %d (broadcast not applied)", i, got, gainQ8)
		}
	}

	got, err := dec.Decode(packet, frameSize)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("decoded sample count=%d want %d", len(got), len(want))
	}
	cmp := compareWaveformF32(got, want)
	qualitycompare.AssertQuality(t, cmp, qualityBarWaveformNearExact, "MS decoder gain broadcast vs libopus oracle")
}

// TestMSDecoderCTL_GainZeroRoundtrip asserts that SetGain(0) restores the
// identity gain applied to all streams, matching the libopus default behavior.
func TestMSDecoderCTL_GainZeroRoundtrip(t *testing.T) {
	const channels = 6
	dec, err := NewDecoderDefault(48000, channels)
	if err != nil {
		t.Fatalf("NewDecoderDefault: %v", err)
	}

	// Set non-zero, then reset to zero
	if err := dec.SetGain(512); err != nil {
		t.Fatalf("SetGain(512): %v", err)
	}
	if err := dec.SetGain(0); err != nil {
		t.Fatalf("SetGain(0): %v", err)
	}
	if got := dec.Gain(); got != 0 {
		t.Fatalf("Gain()=%d after SetGain(0), want 0", got)
	}
	for i, sd := range dec.decoders {
		st := sd.(*streamState)
		if got := st.Gain(); got != 0 {
			t.Fatalf("stream %d Gain()=%d after SetGain(0), want 0 (broadcast not applied)", i, got)
		}
	}
}

// TestMSDecoderCTL_GainBoundary asserts that SetGain validates boundaries
// per opus_multistream_decoder_ctl OPUS_SET_GAIN: valid range [-32768, 32767].
// C ref: OPUS_SET_GAIN validation in opus.h + opus_multistream.c libopus 1.6.1
func TestMSDecoderCTL_GainBoundary(t *testing.T) {
	dec, err := NewDecoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewDecoderDefault: %v", err)
	}

	// Valid boundaries
	for _, gain := range []int{-32768, -1, 0, 1, 32767} {
		if err := dec.SetGain(gain); err != nil {
			t.Fatalf("SetGain(%d) unexpected error: %v", gain, err)
		}
		if got := dec.Gain(); got != gain {
			t.Fatalf("Gain()=%d after SetGain(%d)", got, gain)
		}
	}

	// Out-of-range must fail without changing the gain
	prevGain := dec.Gain()
	for _, bad := range []int{-32769, 32768} {
		if err := dec.SetGain(bad); !errors.Is(err, ErrInvalidGain) {
			t.Fatalf("SetGain(%d) error=%v want ErrInvalidGain", bad, err)
		}
		if got := dec.Gain(); got != prevGain {
			t.Fatalf("SetGain(%d) mutated Gain() to %d", bad, got)
		}
	}
}

// TestMSDecoderCTL_ComplexityBroadcast asserts that SetComplexity broadcasts to
// all per-stream decoders.
// C ref: opus_multistream_decoder_ctl OPUS_SET_COMPLEXITY, opus_multistream.c libopus 1.6.1
func TestMSDecoderCTL_ComplexityBroadcast(t *testing.T) {
	dec, err := NewDecoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewDecoderDefault: %v", err)
	}

	for _, complexity := range []int{0, 5, 10} {
		if err := dec.SetComplexity(complexity); err != nil {
			t.Fatalf("SetComplexity(%d): %v", complexity, err)
		}
		if got := dec.Complexity(); got != complexity {
			t.Fatalf("Complexity()=%d want %d (get from first stream)", got, complexity)
		}
		for i, sd := range dec.decoders {
			st := sd.(*streamState)
			if got := st.Complexity(); got != complexity {
				t.Fatalf("stream %d Complexity()=%d want %d (broadcast)", i, got, complexity)
			}
		}
	}

	// Boundary validation
	for _, bad := range []int{-1, 11} {
		if err := dec.SetComplexity(bad); !errors.Is(err, ErrInvalidComplexity) {
			t.Fatalf("SetComplexity(%d) error=%v want ErrInvalidComplexity", bad, err)
		}
	}
}

// TestMSDecoderCTL_PhaseInversionBroadcast asserts that SetPhaseInversionDisabled
// broadcasts to all per-stream CELT/Hybrid decoders.
// C ref: opus_multistream_decoder_ctl OPUS_SET_PHASE_INVERSION_DISABLED, opus_multistream.c libopus 1.6.1
func TestMSDecoderCTL_PhaseInversionBroadcast(t *testing.T) {
	dec, err := NewDecoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewDecoderDefault: %v", err)
	}

	for _, disabled := range []bool{true, false} {
		dec.SetPhaseInversionDisabled(disabled)
		if got := dec.PhaseInversionDisabled(); got != disabled {
			t.Fatalf("PhaseInversionDisabled()=%v want %v", got, disabled)
		}
		for i, sd := range dec.decoders {
			st := sd.(*streamState)
			if got := st.PhaseInversionDisabled(); got != disabled {
				t.Fatalf("stream %d PhaseInversionDisabled()=%v want %v (broadcast)", i, got, disabled)
			}
		}
	}
}

// TestMSDecoderCTL_FinalRangeXOR validates that GetFinalRange / FinalRange XOR
// all per-stream final range values, matching the libopus OPUS_GET_FINAL_RANGE
// semantics for multistream decoders.
// C ref: opus_multistream_decoder_ctl OPUS_GET_FINAL_RANGE, opus_multistream.c libopus 1.6.1
func TestMSDecoderCTL_FinalRangeXOR(t *testing.T) {
	const (
		channels   = 3
		sampleRate = 48000
	)
	streams, coupled, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("DefaultMapping: %v", err)
	}

	enc, err := NewEncoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	enc.SetMode(internalenc.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(192000)

	packet, err := enc.Encode(generateTestSignal(channels, sampleRate/50, sampleRate, 440), sampleRate/50)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	// Before any decode, final range should be zero
	if fr := dec.FinalRange(); fr != 0 {
		t.Fatalf("FinalRange() before decode = 0x%08x, want 0", fr)
	}

	if _, err := dec.Decode(packet, sampleRate/50); err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// GetFinalRange must equal FinalRange
	if a, b := dec.GetFinalRange(), dec.FinalRange(); a != b {
		t.Fatalf("GetFinalRange()=0x%08x FinalRange()=0x%08x mismatch", a, b)
	}

	// XOR of per-stream ranges must equal the aggregate
	var xored uint32
	for _, sd := range dec.decoders {
		st := sd.(*streamState)
		xored ^= st.FinalRange()
	}
	if got := dec.FinalRange(); got != xored {
		t.Fatalf("FinalRange()=0x%08x want per-stream XOR=0x%08x", got, xored)
	}
}

// TestMSDecoderCTL_LastPacketDurationReflectsFirstStream asserts that
// LastPacketDuration reads from the first stream, matching libopus
// OPUS_GET_LAST_PACKET_DURATION (reads st->decoder[0]->last_packet_duration).
// C ref: opus_multistream_decoder_ctl OPUS_GET_LAST_PACKET_DURATION, opus_multistream.c libopus 1.6.1
func TestMSDecoderCTL_LastPacketDurationReflectsFirstStream(t *testing.T) {
	const (
		channels   = 6
		sampleRate = 48000
		frameSize  = sampleRate / 50
	)
	dec, err := NewDecoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewDecoderDefault: %v", err)
	}
	if got := dec.LastPacketDuration(); got != 0 {
		t.Fatalf("LastPacketDuration() before decode=%d want 0", got)
	}

	enc, err := NewEncoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewEncoderDefault: %v", err)
	}
	enc.SetMode(internalenc.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(256000)

	packet, err := enc.Encode(generateTestSignal(channels, frameSize, sampleRate, 220), frameSize)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if _, err := dec.Decode(packet, frameSize); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got := dec.LastPacketDuration(); got != frameSize {
		t.Fatalf("LastPacketDuration()=%d want %d", got, frameSize)
	}

	// Matches stream 0 directly
	st0 := dec.decoders[0].(*streamState)
	if got := st0.LastPacketDuration(); got != frameSize {
		t.Fatalf("stream0 LastPacketDuration()=%d want %d", got, frameSize)
	}
}

// TestMSDecoderCTL_BandwidthReflectsFirstStream asserts Bandwidth() reads from
// stream 0 after decode, matching libopus OPUS_GET_BANDWIDTH multistream path.
// C ref: opus_multistream_decoder_ctl OPUS_GET_BANDWIDTH, opus_multistream.c libopus 1.6.1
func TestMSDecoderCTL_BandwidthReflectsFirstStream(t *testing.T) {
	const (
		channels   = 6
		sampleRate = 48000
		frameSize  = sampleRate / 50
	)
	dec, err := NewDecoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewDecoderDefault: %v", err)
	}

	enc, err := NewEncoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewEncoderDefault: %v", err)
	}
	enc.SetMode(internalenc.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(256000)

	packet, err := enc.Encode(generateTestSignal(channels, frameSize, sampleRate, 220), frameSize)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if _, err := dec.Decode(packet, frameSize); err != nil {
		t.Fatalf("Decode: %v", err)
	}

	got := dec.Bandwidth()
	if got == types.Bandwidth(0) {
		t.Fatalf("Bandwidth() returned zero after decode")
	}

	// Must equal stream 0
	st0 := dec.decoders[0].(*streamState)
	if got != st0.Bandwidth() {
		t.Fatalf("dec.Bandwidth()=%v stream0.Bandwidth()=%v mismatch", got, st0.Bandwidth())
	}
}

// TestMSDecoderCTL_ResetPreservesGainAndComplexity asserts that Reset() does
// not clobber gain or complexity, mirroring libopus opus_decoder_init behavior
// (it zeroes state but not user-configured CTLs).
// C ref: opus_decoder_init preserves output_gain, opus_decoder.c libopus 1.6.1
func TestMSDecoderCTL_ResetPreservesGainAndComplexity(t *testing.T) {
	dec, err := NewDecoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewDecoderDefault: %v", err)
	}
	if err := dec.SetGain(512); err != nil {
		t.Fatalf("SetGain: %v", err)
	}
	if err := dec.SetComplexity(7); err != nil {
		t.Fatalf("SetComplexity: %v", err)
	}
	dec.Reset()
	if got := dec.Gain(); got != 512 {
		t.Fatalf("Reset() changed Gain() to %d, want 512", got)
	}
	if got := dec.Complexity(); got != 7 {
		t.Fatalf("Reset() changed Complexity() to %d, want 7", got)
	}
	// Per stream
	for i, sd := range dec.decoders {
		st := sd.(*streamState)
		if got := st.Gain(); got != 512 {
			t.Fatalf("Reset() cleared stream %d Gain()=%d want 512", i, got)
		}
	}
}

// TestMSDecoderCTL_IgnoreExtensionsBroadcast asserts SetIgnoreExtensions
// broadcasts to all per-stream decoders.
func TestMSDecoderCTL_IgnoreExtensionsBroadcast(t *testing.T) {
	dec, err := NewDecoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewDecoderDefault: %v", err)
	}

	for _, ignore := range []bool{true, false} {
		dec.SetIgnoreExtensions(ignore)
		if got := dec.IgnoreExtensions(); got != ignore {
			t.Fatalf("IgnoreExtensions()=%v want %v", got, ignore)
		}
		for i, sd := range dec.decoders {
			st := sd.(*streamState)
			if got := st.ignoreExtensions; got != ignore {
				t.Fatalf("stream %d ignoreExtensions=%v want %v (broadcast)", i, got, ignore)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Encoder CTL broadcast parity
// ---------------------------------------------------------------------------

// TestMSEncoderCTL_BitrateBroadcast asserts that SetBitrate distributes
// rate to all stream encoders and that the per-stream rates are positive.
// C ref: opus_multistream_encoder_ctl OPUS_SET_BITRATE, opus_multistream.c libopus 1.6.1
func TestMSEncoderCTL_BitrateBroadcast(t *testing.T) {
	const (
		channels = 6
		bitrate  = 256000
	)
	enc, err := NewEncoderDefault(48000, channels)
	if err != nil {
		t.Fatalf("NewEncoderDefault: %v", err)
	}
	enc.SetBitrate(bitrate)

	if got := enc.Bitrate(); got != bitrate {
		t.Fatalf("Bitrate()=%d want %d", got, bitrate)
	}
	// Distribute must produce positive per-stream rates
	rates := enc.allocateRates(960)
	for i, r := range rates {
		if r <= 0 {
			t.Fatalf("stream %d allocateRates=%d <= 0", i, r)
		}
	}
}

// TestMSEncoderCTL_ComplexityBroadcast asserts SetComplexity broadcasts to
// every stream encoder.
// C ref: opus_multistream_encoder_ctl OPUS_SET_COMPLEXITY, opus_multistream.c libopus 1.6.1
func TestMSEncoderCTL_ComplexityBroadcast(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault: %v", err)
	}
	for _, complexity := range []int{0, 5, 10} {
		enc.SetComplexity(complexity)
		if got := enc.Complexity(); got != complexity {
			t.Fatalf("Complexity()=%d want %d", got, complexity)
		}
		for i, e := range enc.encoders {
			if got := e.Complexity(); got != complexity {
				t.Fatalf("stream %d Complexity()=%d want %d (broadcast)", i, got, complexity)
			}
		}
	}
}

// TestMSEncoderCTL_VBRBroadcast asserts SetVBR / SetVBRConstraint broadcast.
// C ref: opus_multistream_encoder_ctl OPUS_SET_VBR / OPUS_SET_VBR_CONSTRAINT, opus_multistream.c libopus 1.6.1
func TestMSEncoderCTL_VBRBroadcast(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault: %v", err)
	}
	for _, vbr := range []bool{false, true} {
		enc.SetVBR(vbr)
		if got := enc.VBR(); got != vbr {
			t.Fatalf("VBR()=%v want %v", got, vbr)
		}
		for i, e := range enc.encoders {
			if got := e.VBR(); got != vbr {
				t.Fatalf("stream %d VBR()=%v want %v (broadcast)", i, got, vbr)
			}
		}
	}
	for _, cvbr := range []bool{false, true} {
		enc.SetVBRConstraint(cvbr)
		if got := enc.VBRConstraint(); got != cvbr {
			t.Fatalf("VBRConstraint()=%v want %v", got, cvbr)
		}
		for i, e := range enc.encoders {
			if got := e.VBRConstraint(); got != cvbr {
				t.Fatalf("stream %d VBRConstraint()=%v want %v (broadcast)", i, got, cvbr)
			}
		}
	}
}

// TestMSEncoderCTL_FECBroadcast asserts SetFEC / SetPacketLoss broadcast.
// C ref: opus_multistream_encoder_ctl OPUS_SET_INBAND_FEC / OPUS_SET_PACKET_LOSS_PERC, opus_multistream.c libopus 1.6.1
func TestMSEncoderCTL_FECBroadcast(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault: %v", err)
	}
	for _, fec := range []bool{false, true} {
		enc.SetFEC(fec)
		if got := enc.FECEnabled(); got != fec {
			t.Fatalf("FECEnabled()=%v want %v", got, fec)
		}
		for i, e := range enc.encoders {
			if got := e.FECEnabled(); got != fec {
				t.Fatalf("stream %d FECEnabled()=%v want %v (broadcast)", i, got, fec)
			}
		}
	}
	enc.SetPacketLoss(15)
	if got := enc.PacketLoss(); got != 15 {
		t.Fatalf("PacketLoss()=%d want 15", got)
	}
	for i, e := range enc.encoders {
		if got := e.PacketLoss(); got != 15 {
			t.Fatalf("stream %d PacketLoss()=%d want 15 (broadcast)", i, got)
		}
	}
}

// TestMSEncoderCTL_DTXBroadcast asserts SetDTX broadcasts to all streams.
// C ref: opus_multistream_encoder_ctl OPUS_SET_DTX, opus_multistream.c libopus 1.6.1
func TestMSEncoderCTL_DTXBroadcast(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault: %v", err)
	}
	for _, dtx := range []bool{true, false} {
		enc.SetDTX(dtx)
		if got := enc.DTXEnabled(); got != dtx {
			t.Fatalf("DTXEnabled()=%v want %v", got, dtx)
		}
		for i, e := range enc.encoders {
			if got := e.DTXEnabled(); got != dtx {
				t.Fatalf("stream %d DTXEnabled()=%v want %v (broadcast)", i, got, dtx)
			}
		}
	}
}

// TestMSEncoderCTL_BandwidthBroadcast asserts SetBandwidth broadcasts.
// C ref: opus_multistream_encoder_ctl OPUS_SET_BANDWIDTH, opus_multistream.c libopus 1.6.1
func TestMSEncoderCTL_BandwidthBroadcast(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault: %v", err)
	}
	for _, bw := range []types.Bandwidth{
		types.BandwidthNarrowband,
		types.BandwidthWideband,
		types.BandwidthFullband,
	} {
		enc.SetBandwidth(bw)
		if got := enc.Bandwidth(); got != bw {
			t.Fatalf("Bandwidth()=%v want %v", got, bw)
		}
		for i, e := range enc.encoders {
			if got := e.Bandwidth(); got != bw {
				t.Fatalf("stream %d Bandwidth()=%v want %v (broadcast)", i, got, bw)
			}
		}
	}
	// Restore auto
	enc.SetBandwidthAuto()
}

// TestMSEncoderCTL_SignalBroadcast asserts SetSignal broadcasts.
// C ref: opus_multistream_encoder_ctl OPUS_SET_SIGNAL, opus_multistream.c libopus 1.6.1
func TestMSEncoderCTL_SignalBroadcast(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault: %v", err)
	}
	for _, sig := range []types.Signal{types.SignalVoice, types.SignalMusic, types.SignalAuto} {
		enc.SetSignal(sig)
		if got := enc.Signal(); got != sig {
			t.Fatalf("Signal()=%v want %v", got, sig)
		}
		for i, e := range enc.encoders {
			if got := e.SignalType(); got != sig {
				t.Fatalf("stream %d SignalType()=%v want %v (broadcast)", i, got, sig)
			}
		}
	}
}

// TestMSEncoderCTL_PhaseInversionBroadcast asserts SetPhaseInversionDisabled
// broadcasts to all stream encoders.
// C ref: opus_multistream_encoder_ctl OPUS_SET_PHASE_INVERSION_DISABLED, opus_multistream.c libopus 1.6.1
func TestMSEncoderCTL_PhaseInversionBroadcast(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault: %v", err)
	}
	for _, disabled := range []bool{true, false} {
		enc.SetPhaseInversionDisabled(disabled)
		if got := enc.PhaseInversionDisabled(); got != disabled {
			t.Fatalf("PhaseInversionDisabled()=%v want %v", got, disabled)
		}
		for i, e := range enc.encoders {
			if got := e.PhaseInversionDisabled(); got != disabled {
				t.Fatalf("stream %d PhaseInversionDisabled()=%v want %v (broadcast)", i, got, disabled)
			}
		}
	}
}

// TestMSEncoderCTL_FinalRangeXOR asserts that GetFinalRange XORs per-stream
// final range values after an encode, matching libopus OPUS_GET_FINAL_RANGE.
// C ref: opus_multistream_encoder_ctl OPUS_GET_FINAL_RANGE, opus_multistream.c libopus 1.6.1
func TestMSEncoderCTL_FinalRangeXOR(t *testing.T) {
	const (
		channels   = 6
		sampleRate = 48000
		frameSize  = sampleRate / 50
	)
	enc, err := NewEncoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewEncoderDefault: %v", err)
	}
	enc.SetMode(internalenc.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(256000)

	pcm := generateTestSignal(channels, frameSize, sampleRate, 330)
	if _, err := enc.Encode(pcm, frameSize); err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// XOR of per-stream final ranges must equal GetFinalRange
	var xored uint32
	for _, e := range enc.encoders {
		xored ^= e.FinalRange()
	}
	if got := enc.GetFinalRange(); got != xored {
		t.Fatalf("GetFinalRange()=0x%08x want per-stream XOR=0x%08x", got, xored)
	}
}

// TestMSEncoderCTL_LookaheadMatchesAllStreams asserts that Lookahead() returns
// the same value as each stream encoder's Lookahead, since all stream encoders
// share the same algorithmic delay.
// C ref: opus_multistream_encoder_ctl OPUS_GET_LOOKAHEAD, opus_multistream.c libopus 1.6.1
func TestMSEncoderCTL_LookaheadMatchesAllStreams(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault: %v", err)
	}
	want := enc.Lookahead()
	if want <= 0 {
		t.Fatalf("Lookahead()=%d, want > 0", want)
	}
	for i, e := range enc.encoders {
		if got := e.Lookahead(); got != want {
			t.Fatalf("stream %d Lookahead()=%d want %d", i, got, want)
		}
	}
}

// TestMSEncoderCTL_PredictionDisabledBroadcast asserts SetPredictionDisabled
// broadcasts to all stream encoders.
// C ref: opus_multistream_encoder_ctl OPUS_SET_PREDICTION_DISABLED, opus_multistream.c libopus 1.6.1
func TestMSEncoderCTL_PredictionDisabledBroadcast(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault: %v", err)
	}
	for _, disabled := range []bool{true, false} {
		enc.SetPredictionDisabled(disabled)
		if got := enc.PredictionDisabled(); got != disabled {
			t.Fatalf("PredictionDisabled()=%v want %v", got, disabled)
		}
		for i, e := range enc.encoders {
			if got := e.PredictionDisabled(); got != disabled {
				t.Fatalf("stream %d PredictionDisabled()=%v want %v (broadcast)", i, got, disabled)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Gain CTL end-to-end: decoded audio with gain matches libopus oracle
// ---------------------------------------------------------------------------

// TestMSDecoderCTL_GainAudioMatchesLibopus verifies that a non-trivial gain
// value applied through SetGain produces audio that matches the libopus oracle
// which applies the same gain via OPUS_SET_GAIN on a multistream decoder.
// This is the authoritative end-to-end gate for the CTL parity surface.
func TestMSDecoderCTL_GainAudioMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		channels   = 6
		sampleRate = 48000
		gainQ8     = -4096 // approx −16 dB in Q8
		bitrate    = 256000
	)
	frameSize := sampleRate / 50

	streams, coupled, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("DefaultMapping: %v", err)
	}

	enc, err := NewEncoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	enc.SetMode(internalenc.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(bitrate)

	pcm := generateMultichannelSine(channels, frameSize)
	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	want, err := decodeWithLibopusReferencePacketsGain(
		1, sampleRate, channels, streams, coupled, frameSize, gainQ8,
		mapping, nil, [][]byte{packet},
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "reference gain audio", err)
	}

	dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if err := dec.SetGain(gainQ8); err != nil {
		t.Fatalf("SetGain(%d): %v", gainQ8, err)
	}
	got, err := dec.Decode(packet, frameSize)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("decoded sample count=%d want %d", len(got), len(want))
	}
	cmp := compareWaveformF32(got, want)
	qualitycompare.AssertQuality(t, cmp, qualityBarWaveformNearExact, "6ch gain CTL audio vs libopus oracle")
}

// TestMSDecoderCTL_GainAudioMatchesLibopusSILK validates gain CTL on a SILK
// multistream to confirm it applies equally across all modes.
func TestMSDecoderCTL_GainAudioMatchesLibopusSILK(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		channels   = 3
		sampleRate = 48000
		gainQ8     = 2048
		bitrate    = 192000
	)
	frameSize := sampleRate / 50

	streams, coupled, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("DefaultMapping: %v", err)
	}

	enc, err := NewEncoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	enc.SetMode(internalenc.ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)
	enc.SetBitrate(bitrate)

	pcm := generateMultichannelSine(channels, frameSize)
	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	want, err := decodeWithLibopusReferencePacketsGain(
		1, sampleRate, channels, streams, coupled, frameSize, gainQ8,
		mapping, nil, [][]byte{packet},
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "reference SILK gain audio", err)
	}

	dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if err := dec.SetGain(gainQ8); err != nil {
		t.Fatalf("SetGain(%d): %v", gainQ8, err)
	}
	got, err := dec.Decode(packet, frameSize)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("decoded sample count=%d want %d", len(got), len(want))
	}
	cmp := compareWaveformF32(got, want)
	qualitycompare.AssertQuality(t, cmp, qualityBarWaveformNearExact, "3ch SILK gain CTL audio vs libopus oracle")
}
