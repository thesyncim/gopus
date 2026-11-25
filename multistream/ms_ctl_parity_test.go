package multistream

import (
	"errors"
	"fmt"
	internalenc "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/qualitycompare"
	"github.com/thesyncim/gopus/types"
	"math"
	"runtime"
	"testing"
)

func assertMSControlPCMExact(t *testing.T, got, want []float32, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s PCM samples Go=%d C=%d", label, len(got), len(want))
	}
	for i, sample := range got {
		if math.Float32bits(sample) != math.Float32bits(want[i]) {
			t.Fatalf("%s PCM sample %d Go=%08x C=%08x", label, i,
				math.Float32bits(sample), math.Float32bits(want[i]))
		}
	}
}

// TestMSDecoderCTL_GainBroadcast asserts that SetGain broadcasts to all per-
// stream decoders and that the decoded audio matches the libopus oracle with
// the same gain applied.
// C ref: opus_multistream_decoder_ctl, OPUS_SET_GAIN, opus_multistream.c libopus 1.6.1
func TestMSDecoderCTL_GainBroadcast(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		channels   = 3
		sampleRate = 48000
		gainQ8     = 8192 // 32 dB Q8 ≈ +4 dB
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
	enc.SetMode(internalenc.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(bitrate)

	packet, err := encodePacket(enc, generateTestSignal(channels, frameSize, sampleRate, 997), frameSize)
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
	assertMSControlPCMExact(t, got, want, "MS decoder gain broadcast")
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

	packet, err := encodePacket(enc, generateTestSignal(channels, sampleRate/50, sampleRate, 440), sampleRate/50)
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

	packet, err := encodePacket(enc, generateTestSignal(channels, frameSize, sampleRate, 220), frameSize)
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

	packet, err := encodePacket(enc, generateTestSignal(channels, frameSize, sampleRate, 220), frameSize)
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

// TestMSEncoderCTL_BitrateBroadcast checks that SetBitrate retains the
// configured aggregate bitrate and that Go's per-stream allocator produces
// positive child targets. The Go facade stores the configured total and
// assigns initial child rates and recalculates them for each encode.
// In libopus 1.6.1, SET_BITRATE
// stores an aggregate target; its getter sums the current child targets.
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
	// SetBandwidth broadcasts the user request to every per-stream encoder, but
	// Bandwidth() mirrors libopus OPUS_GET_BANDWIDTH (st->bandwidth) which holds
	// the FULLBAND init default until an encode decides it. The request itself is
	// applied at encode time via each stream's bandwidth clamp.
	for _, bw := range []types.Bandwidth{
		types.BandwidthNarrowband,
		types.BandwidthWideband,
		types.BandwidthFullband,
	} {
		enc.SetBandwidth(bw)
		if got := enc.Bandwidth(); got != types.BandwidthFullband {
			t.Fatalf("Bandwidth()=%v want %v (FULLBAND init default before encode)", got, types.BandwidthFullband)
		}
		for i, e := range enc.encoders {
			if got := e.Bandwidth(); got != types.BandwidthFullband {
				t.Fatalf("stream %d Bandwidth()=%v want %v (FULLBAND init default before encode)", i, got, types.BandwidthFullband)
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
	if _, err := encodePacket(enc, pcm, frameSize); err != nil {
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
	packet, err := encodePacket(enc, pcm, frameSize)
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
	assertMSControlPCMExact(t, got, want, "6ch gain CTL audio")
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
	packet, err := encodePacket(enc, pcm, frameSize)
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
	assertMSControlPCMExact(t, got, want, "3ch SILK gain CTL audio")
}

func streamModeClass(cfg int) string {
	switch {
	case cfg < 0:
		return "none"
	case cfg < 12:
		return "SILK"
	case cfg < 16:
		return "Hybrid"
	default:
		return "CELT"
	}
}

// isCELTConfig reports whether a TOC config is pure-CELT (>=16).
func isCELTConfig(cfg int) bool { return cfg >= 16 }

// transitionFrameMask returns, for each packet index, whether decoding that
// packet is a per-stream mode-transition frame: at least one stream crosses the
// CELT_ONLY boundary versus that stream's previous frame. libopus applies its
// pcm_transition crossfade on those frames. The classification labels failures
// and reports mode counts; it does not alter strict PCM comparisons.
func transitionFrameMask(packets [][]byte, streams int) (mask []bool, modeCounts map[string]int) {
	mask = make([]bool, len(packets))
	modeCounts = map[string]int{}
	prevCELT := make([]int, streams) // -1 unknown, 0 non-CELT, 1 CELT
	for i := range prevCELT {
		prevCELT[i] = -1
	}
	for pi, pkt := range packets {
		sp, err := parseMultistreamPacket(pkt, streams)
		if err != nil {
			continue
		}
		for s := 0; s < streams && s < len(sp); s++ {
			cfg := -1
			if len(sp[s]) > 0 {
				cfg = int(sp[s][0] >> 3)
			}
			modeCounts[streamModeClass(cfg)]++
			if cfg < 0 {
				continue // empty per-stream frame (DTX/PLC): no mode change recorded
			}
			cur := 0
			if isCELTConfig(cfg) {
				cur = 1
			}
			if prevCELT[s] != -1 && prevCELT[s] != cur {
				mask[pi] = true
			}
			prevCELT[s] = cur
		}
	}
	return mask, modeCounts
}

// assertMSDecodeFloatFrameAware compares every interleaved float32 sample by
// raw bits. Transition classification only labels failures and summary stats.
func assertMSDecodeFloatFrameAware(t *testing.T, got, want []float32, channels, frameSize int, transition []bool, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: sample count mismatch gopus=%d libopus=%d", label, len(got), len(want))
	}
	perFrame := frameSize * channels
	for f := 0; f*perFrame < len(got); f++ {
		isTransition := f < len(transition) && transition[f]
		base := f * perFrame
		frameMax := 0.0
		firstIdx := -1
		mism := 0
		for i := base; i < base+perFrame && i < len(got); i++ {
			if math.Float32bits(got[i]) == math.Float32bits(want[i]) {
				continue
			}
			d := math.Abs(float64(got[i] - want[i]))
			if d > frameMax {
				frameMax = d
			}
			if firstIdx < 0 {
				firstIdx = i
			}
			mism++
		}
		if mism == 0 {
			continue
		}
		region := "steady"
		if isTransition {
			region = "transition"
		}
		t.Fatalf("%s frame %d (%s): float bits differ: %d/%d samples, maxAbs=%g (firstIdx=%d got=%08x %.10g want=%08x %.10g)",
			label, f, region, mism, perFrame, frameMax, firstIdx, math.Float32bits(got[firstIdx]), got[firstIdx], math.Float32bits(want[firstIdx]), want[firstIdx])
	}
}

// assertMSDecodeInt16FrameAware is the int16 analog of
// assertMSDecodeFloatFrameAware.
func assertMSDecodeInt16FrameAware(t *testing.T, got, want []int16, channels, frameSize int, transition []bool, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: sample count mismatch gopus=%d libopus=%d", label, len(got), len(want))
	}
	perFrame := frameSize * channels
	for f := 0; f*perFrame < len(got); f++ {
		isTransition := f < len(transition) && transition[f]
		base := f * perFrame
		frameMax := 0
		firstIdx := -1
		mism := 0
		for i := base; i < base+perFrame && i < len(got); i++ {
			if got[i] == want[i] {
				continue
			}
			d := int(got[i]) - int(want[i])
			if d < 0 {
				d = -d
			}
			if d > frameMax {
				frameMax = d
			}
			if firstIdx < 0 {
				firstIdx = i
			}
			mism++
		}
		if mism == 0 {
			continue
		}
		region := "steady"
		if isTransition {
			region = "transition"
		}
		t.Fatalf("%s frame %d (%s): int16 differs: %d/%d samples, maxAbs=%d (firstIdx=%d got=%d want=%d)",
			label, f, region, mism, perFrame, frameMax, firstIdx, got[firstIdx], want[firstIdx])
	}
}

// decodeSurroundGopusFloat32 decodes every packet through a fresh gopus
// multistream decoder (optionally with a decode gain) and concatenates the
// interleaved float32 output.
func decodeSurroundGopusFloat32(sampleRate, channels, streams, coupled, frameSize, gainQ8 int, mapping []byte, packets [][]byte) ([]float32, error) {
	dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		return nil, fmt.Errorf("NewDecoder: %w", err)
	}
	if gainQ8 != 0 {
		if err := dec.SetGain(gainQ8); err != nil {
			return nil, fmt.Errorf("SetGain(%d): %w", gainQ8, err)
		}
	}
	var out []float32
	for i, pkt := range packets {
		frame, err := dec.DecodeToFloat32(pkt, frameSize)
		if err != nil {
			return nil, fmt.Errorf("frame %d: %w", i, err)
		}
		out = append(out, frame...)
	}
	return out, nil
}

// decodeSurroundGopusInt16 decodes every packet through a fresh gopus
// multistream decoder (optionally with a decode gain) and concatenates the
// interleaved int16 output.
func decodeSurroundGopusInt16(sampleRate, channels, streams, coupled, frameSize, gainQ8 int, mapping []byte, packets [][]byte) ([]int16, error) {
	dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		return nil, fmt.Errorf("NewDecoder: %w", err)
	}
	if gainQ8 != 0 {
		if err := dec.SetGain(gainQ8); err != nil {
			return nil, fmt.Errorf("SetGain(%d): %w", gainQ8, err)
		}
	}
	var out []int16
	for i, pkt := range packets {
		frame, err := dec.DecodeToInt16(pkt, frameSize)
		if err != nil {
			return nil, fmt.Errorf("frame %d: %w", i, err)
		}
		out = append(out, frame...)
	}
	return out, nil
}

// msDecodeFuzzSpec is one point in the multistream (family 1/255) decode config
// space.
type msDecodeFuzzSpec struct {
	name          string
	mappingFamily int
	channels      int
	frameSize     int
	frameCount    int
	bitrate       int
	vbr           bool
	vbrConstraint bool
	gainQ8        int
	int16Path     bool
	seed          int64
}

// buildSurroundDecodeFuzzSweep enumerates the surround (mapping family 1)
// CLEAN-packet decode matrix. The libopus surround encoder picks the
// stream/coupled layout + mapping; both decoders then consume identical bytes.
//
//   - layouts: mono, stereo, quad, 5.0, 5.1, 6.1, 7.1 (coupling + surround gain)
//   - frame sizes: 2.5/5/10/20/40/60 ms (120…2880 samples at 48 kHz)
//   - bitrates: low → high so per-stream coding spans SILK / Hybrid / CELT
//   - rate control: CBR, constrained VBR, unconstrained VBR
//   - decode gain: 0 and a non-zero Q8 gain (exercises OPUS_SET_GAIN parity)
//   - sample format: float32 + int16
func buildSurroundDecodeFuzzSweep() []msDecodeFuzzSpec {
	layouts := []struct {
		name     string
		channels int
	}{
		{"mono", 1}, {"stereo", 2}, {"quad", 4},
		{"surround_5_0", 5}, {"surround_5_1", 6}, {"surround_6_1", 7}, {"surround_7_1", 8},
	}
	frameSizes := []int{120, 240, 480, 960, 1920, 2880}
	bitrates := []int{16000, 32000, 64000, 128000, 256000, 384000}
	type rc struct {
		vbr        bool
		constraint bool
	}
	rcModes := []rc{{false, false}, {true, true}, {true, false}}
	gains := []int{0, 768} // 0 dB and +3 dB (Q8)
	formats := []bool{false, true}

	var specs []msDecodeFuzzSpec
	var seed int64 = 0xD3C0
	for _, layout := range layouts {
		for _, fs := range frameSizes {
			for _, br := range bitrates {
				for _, m := range rcModes {
					for _, g := range gains {
						for _, i16 := range formats {
							seed++
							fmtTag := "f32"
							if i16 {
								fmtTag = "i16"
							}
							specs = append(specs, msDecodeFuzzSpec{
								name:          fmt.Sprintf("%s/fs%d/br%d/vbr%t/c%t/g%d/%s", layout.name, fs, br, m.vbr, m.constraint, g, fmtTag),
								mappingFamily: 1,
								channels:      layout.channels,
								frameSize:     fs,
								frameCount:    5,
								bitrate:       br,
								vbr:           m.vbr,
								vbrConstraint: m.constraint,
								gainQ8:        g,
								int16Path:     i16,
								seed:          seed,
							})
						}
					}
				}
			}
		}
	}
	return specs
}

// buildDiscreteDecodeFuzzSweep enumerates the discrete (mapping family 255)
// CLEAN-packet decode matrix: N independent mono streams, identity mapping, no
// stereo coupling and no surround masking gain — a distinct decode routing path
// from the coupled surround layouts.
func buildDiscreteDecodeFuzzSweep() []msDecodeFuzzSpec {
	channelCounts := []int{2, 3, 4, 6, 8}
	frameSizes := []int{120, 480, 960, 1920, 2880}
	bitrates := []int{24000, 64000, 128000, 256000}
	type rc struct {
		vbr        bool
		constraint bool
	}
	rcModes := []rc{{false, false}, {true, false}}
	formats := []bool{false, true}

	var specs []msDecodeFuzzSpec
	var seed int64 = 0xFF00
	for _, ch := range channelCounts {
		for _, fs := range frameSizes {
			for _, br := range bitrates {
				for _, m := range rcModes {
					for _, i16 := range formats {
						seed++
						fmtTag := "f32"
						if i16 {
							fmtTag = "i16"
						}
						specs = append(specs, msDecodeFuzzSpec{
							name:          fmt.Sprintf("discrete_%dch/fs%d/br%d/vbr%t/c%t/%s", ch, fs, br, m.vbr, m.constraint, fmtTag),
							mappingFamily: 255,
							channels:      ch,
							frameSize:     fs,
							frameCount:    5,
							bitrate:       br,
							vbr:           m.vbr,
							vbrConstraint: m.constraint,
							gainQ8:        0,
							int16Path:     i16,
							seed:          seed,
						})
					}
				}
			}
		}
	}
	return specs
}

type msDecodeFuzzResult struct {
	executed         bool
	failed           bool
	transitionFrames int
	modeTotals       map[string]int
}

// runMSDecodeFuzzSpec encodes one seeded PCM input through libopus, then
// decodes those packets through gopus and the libopus decode oracle.
func runMSDecodeFuzzSpec(t *testing.T, spec msDecodeFuzzSpec, result *msDecodeFuzzResult) {
	const (
		application    = 2049  // OPUS_APPLICATION_AUDIO
		bandwidthAuto  = -1000 // OPUS_AUTO
		maxPacketBytes = 4000
		sampleRate     = 48000
	)

	pcm := seededMultichannelPCM(spec.seed, spec.channels, spec.frameSize, spec.frameCount)
	ref, err := encodeLibopusSurround(sampleRate, spec.channels, spec.mappingFamily, application,
		spec.bitrate, spec.vbr, spec.vbrConstraint, 10, bandwidthAuto,
		spec.frameSize, spec.frameCount, maxPacketBytes, pcm, false)
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream surround reference encode", err)
		return
	}

	transition, modes := transitionFrameMask(ref.packets, ref.streams)
	result.modeTotals = modes
	for _, tf := range transition {
		if tf {
			result.transitionFrames++
		}
	}

	if spec.int16Path {
		got, err := decodeSurroundGopusInt16(sampleRate, spec.channels, ref.streams, ref.coupledStreams, spec.frameSize, spec.gainQ8, ref.mapping, ref.packets)
		if err != nil {
			t.Fatalf("gopus int16 decode: %v", err)
		}
		want, err := decodeWithLibopusReferencePacketsInt16Gain(1, sampleRate, spec.channels, ref.streams, ref.coupledStreams, spec.frameSize, spec.gainQ8, ref.mapping, nil, ref.packets)
		if err != nil {
			libopustest.HelperUnavailable(t, "multistream reference decode", err)
			return
		}
		assertMSDecodeInt16FrameAware(t, got, want, spec.channels, spec.frameSize, transition, "int16/"+spec.name)
		return
	}

	got, err := decodeSurroundGopusFloat32(sampleRate, spec.channels, ref.streams, ref.coupledStreams, spec.frameSize, spec.gainQ8, ref.mapping, ref.packets)
	if err != nil {
		t.Fatalf("gopus float decode: %v", err)
	}
	want, err := decodeWithLibopusReferencePacketsGain(1, sampleRate, spec.channels, ref.streams, ref.coupledStreams, spec.frameSize, spec.gainQ8, ref.mapping, nil, ref.packets)
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream reference decode", err)
		return
	}
	assertMSDecodeFloatFrameAware(t, got, want, spec.channels, spec.frameSize, transition, "float32/"+spec.name)
}

// runMSDecodeFuzz drives a multistream (family 1/255) decode sweep: it encodes
// each seeded multichannel PCM buffer through the libopus surround oracle, then
// decodes the SAME packets through gopus and the libopus decode oracle, asserting
// sample-exact PCM, including mode-transition frames. Independent surround
// cases may run concurrently; the discrete matrix stays serial.
func runMSDecodeFuzz(t *testing.T, specs []msDecodeFuzzSpec, label string, parallel bool) {
	libopustest.RequireOracle(t)

	budget := fuzzBudget(len(specs))
	stride := 1
	if budget < len(specs) {
		stride = len(specs) / budget
	}

	if parallel {
		if _, err := surroundRefencodeHelper.Path(func() (string, error) {
			return buildMultistreamReferenceHelper(libopustest.CHelperConfig{
				Label:       "multistream surround reference encode",
				OutputBase:  "gopus_libopus_refencode_public_multistream",
				SourceFile:  "libopus_refencode_multistream.c",
				CFlags:      []string{"-O3", "-DNDEBUG"},
				RefIncludes: []string{"celt", "src"},
				Libs:        []string{"-lm"},
			})
		}); err != nil {
			libopustest.HelperUnavailable(t, "multistream surround reference encode", err)
			return
		}
		if _, err := getLibopusRefdecodePath(); err != nil {
			libopustest.HelperUnavailable(t, "multistream reference decode", err)
			return
		}

		selected := make([]msDecodeFuzzSpec, 0, budget)
		for idx := 0; idx < len(specs) && len(selected) < budget; idx += stride {
			selected = append(selected, specs[idx])
		}
		results := make([]msDecodeFuzzResult, len(selected))
		limit := min(4, runtime.GOMAXPROCS(0), len(selected))
		semaphore := make(chan struct{}, limit)
		for i, spec := range selected {
			t.Run(spec.name, func(t *testing.T) {
				t.Parallel()
				semaphore <- struct{}{}
				defer func() {
					<-semaphore
					results[i].failed = t.Failed()
				}()
				results[i].executed = true
				runMSDecodeFuzzSpec(t, spec, &results[i])
			})
		}
		t.Cleanup(func() {
			tested, failedSpecs, transFrames := 0, 0, 0
			modeTotals := map[string]int{}
			for _, result := range results {
				if !result.executed {
					continue
				}
				tested++
				if result.failed {
					failedSpecs++
				}
				transFrames += result.transitionFrames
				for mode, count := range result.modeTotals {
					modeTotals[mode] += count
				}
			}
			t.Logf("%s decode differential sweep: tested=%d/%d specs; failed=%d; arch=%s; modes=%v; transition-frames=%d (all frames strict)",
				label, tested, len(specs), failedSpecs, runtime.GOARCH, modeTotals, transFrames)
		})
		return
	}

	var (
		tested      int
		failedSpecs int
		transFrames int
		modeTotals  = map[string]int{}
	)

	for idx := 0; idx < len(specs) && tested < budget; idx += stride {
		spec := specs[idx]
		if !t.Run(spec.name, func(t *testing.T) {
			tested++
			result := msDecodeFuzzResult{}
			defer func() {
				transFrames += result.transitionFrames
				for k, v := range result.modeTotals {
					modeTotals[k] += v
				}
			}()
			runMSDecodeFuzzSpec(t, spec, &result)
		}) {
			failedSpecs++
		}
	}
	t.Logf("%s decode differential sweep: tested=%d/%d specs; failed=%d; arch=%s; modes=%v; transition-frames=%d (all frames strict)",
		label, tested, len(specs), failedSpecs, runtime.GOARCH, modeTotals, transFrames)
}

// TestMultistreamSurroundDecodeDifferentialFuzz locks gopus multistream surround
// (mapping family 1) DECODE to sample-exact parity against the libopus
// opus_multistream_decode_float / opus_multistream_decode oracle across a broad
// layout × frame-size × bitrate × rate-control × decode-gain × sample-format
// matrix on CLEAN packets. Every decoded sample, including per-stream mode
// transitions, must match the paired libopus output exactly.
func TestMultistreamSurroundDecodeDifferentialFuzz(t *testing.T) {
	runMSDecodeFuzz(t, buildSurroundDecodeFuzzSweep(), "surround", true)
}

// TestMultistreamDiscreteDecodeDifferentialFuzz locks gopus multistream discrete
// (mapping family 255: N independent mono streams, identity mapping, no coupling)
// DECODE to sample-exact parity against the libopus oracle across the
// channel-count × frame-size × bitrate × rate-control × sample-format matrix on
// CLEAN packets.
func TestMultistreamDiscreteDecodeDifferentialFuzz(t *testing.T) {
	runMSDecodeFuzz(t, buildDiscreteDecodeFuzzSweep(), "discrete", false)
}

// projectionDecodeFuzzSpec is one point in the projection (mapping family 3)
// decode config space.
type projectionDecodeFuzzSpec struct {
	name       string
	channels   int // 4=FOA(order1), 9=SOA(order2), 16=TOA(order3)
	frameSize  int
	frameCount int
	bitrate    int
	int16Path  bool
	seed       int64
}

// buildProjectionDecodeFuzzSweep enumerates the projection (mapping family 3)
// CLEAN-packet decode matrix. The libopus projection encoder picks the
// stream/coupled layout + demixing matrix; both decoders then consume identical
// bytes and apply the (locked bit-exact) demix. Spans FOA/SOA/TOA × frame sizes
// × bitrates × float32/int16, so per-stream coding ranges over CELT (high rate /
// low order) through Hybrid/SILK (low rate / high order).
func buildProjectionDecodeFuzzSweep() []projectionDecodeFuzzSpec {
	orders := []struct {
		name     string
		channels int
	}{
		{"foa-4ch", 4}, {"soa-9ch", 9}, {"toa-16ch", 16},
	}
	frameSizes := []int{120, 240, 480, 960, 1920, 2880}
	bitrates := []int{64000, 128000, 256000, 384000}
	formats := []bool{false, true}

	var specs []projectionDecodeFuzzSpec
	var seed int64 = 0x9A03
	for _, order := range orders {
		for _, fs := range frameSizes {
			for _, br := range bitrates {
				for _, i16 := range formats {
					seed++
					fmtTag := "f32"
					if i16 {
						fmtTag = "i16"
					}
					specs = append(specs, projectionDecodeFuzzSpec{
						name:       fmt.Sprintf("%s/fs%d/br%d/%s", order.name, fs, br, fmtTag),
						channels:   order.channels,
						frameSize:  fs,
						frameCount: 5,
						bitrate:    br,
						int16Path:  i16,
						seed:       seed,
					})
				}
			}
		}
	}
	return specs
}

// TestProjectionDecodeDifferentialFuzz locks gopus projection (mapping family 3)
// DECODE — per-stream decode plus the demixing-matrix application — to
// sample-exact parity against the libopus opus_projection_decode_float /
// opus_projection_decode oracle across the order × frame-size × bitrate ×
// sample-format matrix on CLEAN packets. The demixing application is itself
// locked bit-exact in projection_matrix_libopus_test.go. Every decoded sample,
// including per-stream mode transitions, must match the paired libopus output.
func TestProjectionDecodeDifferentialFuzz(t *testing.T) {
	libopustest.RequireOracle(t)

	specs := buildProjectionDecodeFuzzSweep()
	budget := fuzzBudget(len(specs))
	stride := 1
	if budget < len(specs) {
		stride = len(specs) / budget
	}

	const (
		application    = 2049
		bandwidthAuto  = -1000
		maxPacketBytes = 4000
		sampleRate     = 48000
	)

	var (
		tested      int
		failedSpecs int
		transFrames int
		modeTotals  = map[string]int{}
	)

	for idx := 0; idx < len(specs) && tested < budget; idx += stride {
		spec := specs[idx]
		if !t.Run(spec.name, func(t *testing.T) {
			tested++
			pcm := generateAmbisonicsSweep(spec.channels, spec.frameSize, spec.frameCount)
			// Encode through the libopus projection oracle (float input path) so
			// both decoders consume byte-identical family-3 packets.
			ref, err := encodeLibopusProjection(sampleRate, spec.channels, application,
				spec.bitrate, false, false, 10, bandwidthAuto, spec.frameSize, spec.frameCount,
				maxPacketBytes, 0, pcm, nil)
			if err != nil {
				libopustest.HelperUnavailable(t, "projection reference encode", err)
				return
			}

			transition, modes := transitionFrameMask(ref.packets, ref.streams)
			for k, v := range modes {
				modeTotals[k] += v
			}
			for _, tf := range transition {
				if tf {
					transFrames++
				}
			}
			mapping := trivialMapping(spec.channels)

			if spec.int16Path {
				got, err := decodeProjectionGopusInt16(sampleRate, spec.channels, ref.streams, ref.coupledStreams, spec.frameSize, ref.demixing, ref.packets)
				if err != nil {
					t.Fatalf("gopus projection int16 decode: %v", err)
				}
				want, err := decodeWithLibopusReferencePacketsInt16Gain(3, sampleRate, spec.channels, ref.streams, ref.coupledStreams, spec.frameSize, 0, mapping, ref.demixing, ref.packets)
				if err != nil {
					libopustest.HelperUnavailable(t, "projection reference decode", err)
					return
				}
				assertMSDecodeInt16FrameAware(t, got, want, spec.channels, spec.frameSize, transition, "int16/"+spec.name)
				return
			}

			got, err := decodeProjectionGopusFloat32(sampleRate, spec.channels, ref.streams, ref.coupledStreams, spec.frameSize, ref.demixing, ref.packets)
			if err != nil {
				t.Fatalf("gopus projection float decode: %v", err)
			}
			want, err := decodeWithLibopusReferencePackets(3, sampleRate, spec.channels, ref.streams, ref.coupledStreams, spec.frameSize, mapping, ref.demixing, ref.packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "projection reference decode", err)
				return
			}
			assertMSDecodeFloatFrameAware(t, got, want, spec.channels, spec.frameSize, transition, "float32/"+spec.name)
		}) {
			failedSpecs++
		}
	}
	t.Logf("projection decode differential sweep: tested=%d/%d specs; failed=%d; arch=%s; modes=%v; transition-frames=%d (all frames strict)",
		tested, len(specs), failedSpecs, runtime.GOARCH, modeTotals, transFrames)
}

// gopusEncodedDecodeSpec is one point in the gopus-emitted decode config space.
type gopusEncodedDecodeSpec struct {
	name       string
	projection bool
	channels   int
	frameSize  int
	frameCount int
	bitrate    int
	seed       int64
}

// buildGopusEncodedDecodeSweep enumerates a matrix where the gopus encoder
// produces the packets, which are then decoded through BOTH gopus and the
// libopus decode oracle. This closes the loop: it proves the packets gopus emits
// decode to the same PCM under libopus as under gopus, across surround (family 1)
// and projection (family 3) layouts.
func buildGopusEncodedDecodeSweep() []gopusEncodedDecodeSpec {
	type layout struct {
		name       string
		projection bool
		channels   int
	}
	layouts := []layout{
		{"stereo", false, 2}, {"quad", false, 4}, {"surround_5_1", false, 6}, {"surround_7_1", false, 8},
		{"foa-4ch", true, 4}, {"soa-9ch", true, 9},
	}
	frameSizes := []int{240, 480, 960, 1920}
	bitrates := []int{64000, 128000, 256000}

	var specs []gopusEncodedDecodeSpec
	var seed int64 = 0x6E00
	for _, l := range layouts {
		for _, fs := range frameSizes {
			for _, br := range bitrates {
				seed++
				specs = append(specs, gopusEncodedDecodeSpec{
					name:       fmt.Sprintf("%s/fs%d/br%d", l.name, fs, br),
					projection: l.projection,
					channels:   l.channels,
					frameSize:  fs,
					frameCount: 5,
					bitrate:    br,
					seed:       seed,
				})
			}
		}
	}
	return specs
}

// TestMultistreamGopusEncodedDecodeDifferentialFuzz drives the gopus
// multistream/projection ENCODER to produce CLEAN packets, then decodes the SAME
// packets through gopus and the libopus decode oracle, asserting sample-exact
// PCM. This guards the decode path against gopus-emitted bitstreams (not just
// libopus-emitted ones), across surround (family 1) and projection (family 3).
func TestMultistreamGopusEncodedDecodeDifferentialFuzz(t *testing.T) {
	libopustest.RequireOracle(t)

	specs := buildGopusEncodedDecodeSweep()
	budget := fuzzBudget(len(specs))
	stride := 1
	if budget < len(specs) {
		stride = len(specs) / budget
	}

	const (
		maxPacketBytes = 4000
		sampleRate     = 48000
	)

	var (
		tested      int
		failedSpecs int
		transFrames int
	)

	for idx := 0; idx < len(specs) && tested < budget; idx += stride {
		spec := specs[idx]
		if !t.Run(spec.name, func(t *testing.T) {
			tested++
			pcm := seededMultichannelPCM(spec.seed, spec.channels, spec.frameSize, spec.frameCount)

			var (
				enc      *Encoder
				err      error
				mapping  []byte
				demixing []byte
				family   = 1
			)
			if spec.projection {
				enc, err = NewProjectionEncoder(sampleRate, spec.channels)
				family = 3
			} else {
				enc, err = NewEncoderDefault(sampleRate, spec.channels)
			}
			if err != nil {
				t.Fatalf("encoder create (proj=%t ch=%d): %v", spec.projection, spec.channels, err)
			}
			enc.SetBitrate(spec.bitrate)
			enc.SetVBR(true)
			enc.SetVBRConstraint(false)
			enc.SetComplexity(10)
			enc.SetBandwidthAuto()

			streams, coupled := enc.Streams(), enc.CoupledStreams()
			if spec.projection {
				demixing = enc.GetDemixingMatrix()
				mapping = trivialMapping(spec.channels)
			} else {
				var derr error
				_, _, mapping, derr = DefaultMapping(spec.channels)
				if derr != nil {
					t.Fatalf("DefaultMapping(%d): %v", spec.channels, derr)
				}
			}

			packets := make([][]byte, 0, spec.frameCount)
			for i := 0; i < spec.frameCount; i++ {
				start := i * spec.frameSize * spec.channels
				frame := pcm[start : start+spec.frameSize*spec.channels]
				p, err := encodePacketMax(enc, frame, spec.frameSize, frame, maxPacketBytes)
				if err != nil {
					t.Fatalf("frame %d: gopus encode: %v", i, err)
				}
				packets = append(packets, append([]byte(nil), p...))
			}

			transition, _ := transitionFrameMask(packets, streams)
			for _, tf := range transition {
				if tf {
					transFrames++
				}
			}

			var got []float32
			if spec.projection {
				got, err = decodeProjectionGopusFloat32(sampleRate, spec.channels, streams, coupled, spec.frameSize, demixing, packets)
			} else {
				got, err = decodeSurroundGopusFloat32(sampleRate, spec.channels, streams, coupled, spec.frameSize, 0, mapping, packets)
			}
			if err != nil {
				t.Fatalf("gopus decode: %v", err)
			}
			want, err := decodeWithLibopusReferencePackets(family, sampleRate, spec.channels, streams, coupled, spec.frameSize, mapping, demixing, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "multistream reference decode", err)
				return
			}
			assertMSDecodeFloatFrameAware(t, got, want, spec.channels, spec.frameSize, transition, "gopus-enc/"+spec.name)
		}) {
			failedSpecs++
		}
	}
	t.Logf("gopus-encoded decode differential sweep: tested=%d/%d specs; failed=%d; arch=%s; transition-frames=%d (all frames strict)",
		tested, len(specs), failedSpecs, runtime.GOARCH, transFrames)
}

func runMSRecoveryOracle(t *testing.T, label string, channels, sampleRate, bitrate int, mode internalenc.Mode, bandwidth types.Bandwidth, forceMode bool, lossAt map[int]bool) {
	t.Helper()
	libopustest.RequireOracle(t)

	streams, coupled, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("%s DefaultMapping: %v", label, err)
	}

	enc, err := NewEncoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("%s NewEncoder: %v", label, err)
	}
	if forceMode {
		enc.SetMode(mode)
		if bandwidth != 0 {
			enc.SetBandwidth(bandwidth)
		}
	}
	enc.SetBitrate(bitrate)

	const numFrames = 12
	frameSize := sampleRate / 50 // 20 ms

	packets := make([][]byte, numFrames)
	for i := range numFrames {
		pcm := generateMultichannelSine(channels, frameSize)
		p, encErr := encodePacket(enc, pcm, frameSize)
		if encErr != nil {
			t.Fatalf("%s frame %d Encode: %v", label, i, encErr)
		}
		if p == nil {
			// DTX silence - substitute comfort noise TOC so oracle has same input
			p = []byte{0xF8, 0xFF, 0xFE}
		}
		packets[i] = p
	}

	// Build oracle sequence: nil becomes zero-length packet_len=0 in oracle wire
	// format (the C oracle interprets packet_len==0 as PLC like libopus does).
	oracleSeq := make([][]byte, numFrames)
	gopusSeq := make([][]byte, numFrames)
	for i, p := range packets {
		if lossAt[i] {
			oracleSeq[i] = nil // oracle interprets nil as packet_len=0 → PLC
			gopusSeq[i] = nil  // gopus Decode(nil, ...) → PLC
		} else {
			oracleSeq[i] = p
			gopusSeq[i] = p
		}
	}

	// libopus oracle decode
	want, err := decodeWithLibopusReferencePackets(
		1, sampleRate, channels, streams, coupled, frameSize, mapping, nil, oracleSeq,
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "reference decode", err)
	}

	// gopus decode
	dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("%s NewDecoder: %v", label, err)
	}
	got := make([]float32, 0, len(want))
	for i, p := range gopusSeq {
		frame, decErr := dec.Decode(p, frameSize)
		if decErr != nil {
			t.Fatalf("%s frame %d Decode: %v", label, i, decErr)
		}
		if len(frame) != frameSize*channels {
			t.Fatalf("%s frame %d Decode samples=%d want %d", label, i, len(frame)/channels, frameSize)
		}
		got = append(got, frame...)
	}

	if len(got) != len(want) {
		t.Fatalf("%s decoded sample count=%d want %d", label, len(got), len(want))
	}

	assertRecoveryPCMExact(t, got, want)
	cmp := compareWaveformF32(got, want)
	qualitycompare.AssertQuality(t, cmp, qualityBarWaveformNearExact, label)
}

// TestLibopus_MSRecovery_CELTSingleGap validates PLC recovery for a single
// dropped packet in a 3-channel CELT multistream.
// C ref: opus_multistream_decode_float, opus_multistream.c, libopus 1.6.1
func TestLibopus_MSRecovery_CELTSingleGap(t *testing.T) {
	runMSRecoveryOracle(t, "CELT-3ch-gap@5",
		3, 48000, 192000,
		internalenc.ModeCELT, types.BandwidthFullband, true,
		map[int]bool{5: true},
	)
}

// TestLibopus_MSRecovery_CELTLeadingGap validates PLC when the very first
// packet is lost (decoder not yet warmed).
func TestLibopus_MSRecovery_CELTLeadingGap(t *testing.T) {
	runMSRecoveryOracle(t, "CELT-3ch-gap@0",
		3, 48000, 192000,
		internalenc.ModeCELT, types.BandwidthFullband, true,
		map[int]bool{0: true},
	)
}

// TestLibopus_MSRecovery_CELTConsecutiveGaps validates PLC over two
// consecutive dropped frames.
func TestLibopus_MSRecovery_CELTConsecutiveGaps(t *testing.T) {
	runMSRecoveryOracle(t, "CELT-3ch-gap@3,4",
		3, 48000, 192000,
		internalenc.ModeCELT, types.BandwidthFullband, true,
		map[int]bool{3: true, 4: true},
	)
}

// TestLibopus_MSRecovery_CELTTwoSeparateGaps validates PLC recovery with two
// independent gaps in a 6-channel (5.1) multistream.
func TestLibopus_MSRecovery_CELTTwoSeparateGaps(t *testing.T) {
	runMSRecoveryOracle(t, "CELT-6ch-gap@2,8",
		6, 48000, 256000,
		internalenc.ModeCELT, types.BandwidthFullband, true,
		map[int]bool{2: true, 8: true},
	)
}

// TestLibopus_MSRecovery_SILKSingleGap validates PLC recovery for a single
// dropped packet in a 3-channel SILK multistream.
func TestLibopus_MSRecovery_SILKSingleGap(t *testing.T) {
	runMSRecoveryOracle(t, "SILK-3ch-gap@5",
		3, 48000, 192000,
		internalenc.ModeSILK, types.BandwidthWideband, true,
		map[int]bool{5: true},
	)
}

// TestLibopus_MSRecovery_SILKConsecutiveGaps validates SILK multi-gap PLC.
func TestLibopus_MSRecovery_SILKConsecutiveGaps(t *testing.T) {
	runMSRecoveryOracle(t, "SILK-3ch-gap@4,5",
		3, 48000, 192000,
		internalenc.ModeSILK, types.BandwidthWideband, true,
		map[int]bool{4: true, 5: true},
	)
}

// TestLibopus_MSRecovery_HybridSingleGap validates PLC recovery for a single
// dropped packet in a 3-channel Hybrid multistream.
func TestLibopus_MSRecovery_HybridSingleGap(t *testing.T) {
	runMSRecoveryOracle(t, "Hybrid-3ch-gap@5",
		3, 48000, 192000,
		internalenc.ModeHybrid, types.BandwidthFullband, true,
		map[int]bool{5: true},
	)
}

// TestLibopus_MSRecovery_71SurroundSingleGap validates PLC recovery for a
// single dropped frame across all 5 streams of a 7.1 (8-channel) config.
func TestLibopus_MSRecovery_71SurroundSingleGap(t *testing.T) {
	runMSRecoveryOracle(t, "CELT-8ch-gap@6",
		8, 48000, 384000,
		internalenc.ModeCELT, types.BandwidthFullband, true,
		map[int]bool{6: true},
	)
}

// TestLibopus_MSRecovery_71SurroundMultiGap validates multi-gap PLC for 7.1.
func TestLibopus_MSRecovery_71SurroundMultiGap(t *testing.T) {
	runMSRecoveryOracle(t, "CELT-8ch-gap@3,7,10",
		8, 48000, 384000,
		internalenc.ModeCELT, types.BandwidthFullband, true,
		map[int]bool{3: true, 7: true, 10: true},
	)
}

// TestLibopus_MSRecovery_GapAtEnd validates PLC when the last packet is lost.
func TestLibopus_MSRecovery_GapAtEnd(t *testing.T) {
	runMSRecoveryOracle(t, "CELT-3ch-gap@11",
		3, 48000, 192000,
		internalenc.ModeCELT, types.BandwidthFullband, true,
		map[int]bool{11: true},
	)
}

// TestLibopus_MSRecovery_AutoModeSingleGap validates PLC for Auto (mode-selected)
// multistream across a single gap.
func TestLibopus_MSRecovery_AutoModeSingleGap(t *testing.T) {
	runMSRecoveryOracle(t, "Auto-6ch-gap@5",
		6, 48000, 256000,
		internalenc.ModeAuto, 0, false,
		map[int]bool{5: true},
	)
}

// ---------------------------------------------------------------------------
// FEC-enabled PLC parity
// ---------------------------------------------------------------------------

// runMSFECEnabledPLCOracle encodes with in-band FEC enabled, replaces one
// packet with nil, and decodes every sequence entry through the ordinary
// multistream decode API. The nil entry exercises PLC, and the later packet is
// decoded with decode_fec=0. The sequence checks PLC parity for FEC-enabled
// encoded packets; it does not request explicit LBRR recovery.
func runMSFECEnabledPLCOracle(t *testing.T, label string, channels, sampleRate, bitrate, lossIdx int) {
	t.Helper()
	libopustest.RequireOracle(t)

	streams, coupled, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("%s DefaultMapping: %v", label, err)
	}

	enc, err := NewEncoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("%s NewEncoder: %v", label, err)
	}
	enc.SetBitrate(bitrate)
	enc.SetFEC(true)
	enc.SetPacketLoss(10)
	enc.SetMode(internalenc.ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)

	const numFrames = 10
	frameSize := sampleRate / 50

	packets := make([][]byte, numFrames)
	for i := range numFrames {
		pcm := generateMultichannelSine(channels, frameSize)
		p, encErr := encodePacket(enc, pcm, frameSize)
		if encErr != nil {
			t.Fatalf("%s frame %d Encode: %v", label, i, encErr)
		}
		if p == nil {
			p = []byte{0xF8, 0xFF, 0xFE}
		}
		packets[i] = p
	}

	// Build sequence with PLC hole at lossIdx — both oracle and gopus use nil.
	seq := make([][]byte, numFrames)
	copy(seq, packets)
	seq[lossIdx] = nil

	want, err := decodeWithLibopusReferencePackets(
		1, sampleRate, channels, streams, coupled, frameSize, mapping, nil, seq,
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "reference FEC decode", err)
	}

	dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("%s NewDecoder: %v", label, err)
	}
	got := make([]float32, 0, len(want))
	for i, p := range seq {
		frame, decErr := dec.Decode(p, frameSize)
		if decErr != nil {
			t.Fatalf("%s frame %d Decode: %v", label, i, decErr)
		}
		got = append(got, frame...)
	}

	if len(got) != len(want) {
		t.Fatalf("%s decoded sample count=%d want %d", label, len(got), len(want))
	}
	assertRecoveryPCMExact(t, got, want)
	cmp := compareWaveformF32(got, want)
	qualitycompare.AssertQuality(t, cmp, qualityBarWaveformNearExact, label)
}

// TestLibopus_MSRecovery_FECEnabledPLCSILKGap validates SILK PLC parity for a
// 3-channel multistream encoded with in-band FEC enabled. The C helper passes
// decode_fec=0 for every packet; the missing packet is nil and selects PLC.
// C ref: opus_multistream_decoder.c and opus_decoder.c, libopus 1.6.1.
func TestLibopus_MSRecovery_FECEnabledPLCSILKGap(t *testing.T) {
	runMSFECEnabledPLCOracle(t, "FEC-enabled-SILK-PLC-3ch-gap@3", 3, 48000, 192000, 3)
}

// TestLibopus_MSRecovery_FECEnabledPLCSILKGap51 validates SILK PLC parity for
// a 5.1 multistream encoded with in-band FEC enabled. The C helper passes
// decode_fec=0 for every packet; the missing packet is nil and selects PLC.
func TestLibopus_MSRecovery_FECEnabledPLCSILKGap51(t *testing.T) {
	runMSFECEnabledPLCOracle(t, "FEC-enabled-SILK-PLC-6ch-gap@5", 6, 48000, 256000, 5)
}

// ---------------------------------------------------------------------------
// Cross-mode handover parity
// ---------------------------------------------------------------------------

// TestLibopus_MSRecovery_ModeHandoverGap checks PLC after a mode transition:
// first half SILK, second half CELT, with a gap across the transition boundary.
// This exercises the cross-mode decoder state management per stream.
// C ref: opus_decode_frame mode-switch PLC path, opus_decoder.c libopus 1.6.1
func TestLibopus_MSRecovery_ModeHandoverGap(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		channels   = 3
		sampleRate = 48000
		bitrate    = 192000
		numFrames  = 12
	)
	frameSize := sampleRate / 50

	streams, coupled, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("DefaultMapping: %v", err)
	}

	var packets [numFrames][]byte
	for phase := range 2 {
		enc, err := NewEncoder(sampleRate, channels, streams, coupled, mapping)
		if err != nil {
			t.Fatalf("phase%d NewEncoder: %v", phase, err)
		}
		enc.SetBitrate(bitrate)
		if phase == 0 {
			enc.SetMode(internalenc.ModeSILK)
			enc.SetBandwidth(types.BandwidthWideband)
		} else {
			enc.SetMode(internalenc.ModeCELT)
			enc.SetBandwidth(types.BandwidthFullband)
		}
		start, end := phase*(numFrames/2), (phase+1)*(numFrames/2)
		for i := start; i < end; i++ {
			pcm := generateMultichannelSine(channels, frameSize)
			p, encErr := encodePacket(enc, pcm, frameSize)
			if encErr != nil {
				t.Fatalf("phase%d frame %d Encode: %v", phase, i, encErr)
			}
			if p == nil {
				p = []byte{0xF8, 0xFF, 0xFE}
			}
			packets[i] = p
		}
	}

	// Drop the first frame after the mode switch (frame 6)
	lossAt := map[int]bool{6: true}
	seq := make([][]byte, numFrames)
	copy(seq, packets[:])
	for i := range seq {
		if lossAt[i] {
			seq[i] = nil
		}
	}

	want, err := decodeWithLibopusReferencePackets(
		1, sampleRate, channels, streams, coupled, frameSize, mapping, nil, seq,
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "reference mode-handover decode", err)
	}

	dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	got := make([]float32, 0, len(want))
	for i, p := range seq {
		frame, decErr := dec.Decode(p, frameSize)
		if decErr != nil {
			t.Fatalf("frame %d Decode: %v", i, decErr)
		}
		got = append(got, frame...)
	}
	if len(got) != len(want) {
		t.Fatalf("decoded sample count=%d want %d", len(got), len(want))
	}
	assertRecoveryPCMExact(t, got, want)
	cmp := compareWaveformF32(got, want)
	qualitycompare.AssertQuality(t, cmp, qualityBarWaveformNearExact, "mode-handover-gap")
}

// ---------------------------------------------------------------------------
// Sub-48 kHz PLC parity
// ---------------------------------------------------------------------------

// TestLibopus_MSRecovery_16kSILKGap validates PLC at 16 kHz API rate for a
// 1-channel (mono) multistream — tests the API-rate PLC path for MS decoder.
func TestLibopus_MSRecovery_16kSILKGap(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		encRate    = 48000
		apiRate    = 16000
		channels   = 1
		sampleRate = apiRate
		numFrames  = 8
	)
	encFrameSize := encRate / 50
	frameSize := apiRate / 50

	streams, coupled, mapping, err := DefaultMapping(channels)
	if err != nil {
		t.Fatalf("DefaultMapping: %v", err)
	}

	enc, err := NewEncoder(encRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	enc.SetMode(internalenc.ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)
	enc.SetBitrate(32000)

	var packets [numFrames][]byte
	for i := range packets {
		pcm := generateMultichannelSine(channels, encFrameSize)
		p, encErr := encodePacket(enc, pcm, encFrameSize)
		if encErr != nil {
			t.Fatalf("frame %d Encode: %v", i, encErr)
		}
		if p == nil {
			p = []byte{0xF8, 0xFF, 0xFE}
		}
		packets[i] = p
	}

	seq := make([][]byte, numFrames)
	copy(seq, packets[:])
	seq[4] = nil // single gap

	want, err := decodeWithLibopusReferencePackets(
		1, sampleRate, channels, streams, coupled, frameSize, mapping, nil, seq,
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "reference 16k SILK PLC", err)
	}

	dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	got := make([]float32, 0, len(want))
	for i, p := range seq {
		frame, decErr := dec.Decode(p, frameSize)
		if decErr != nil {
			t.Fatalf("frame %d Decode: %v", i, decErr)
		}
		got = append(got, frame...)
	}
	if len(got) != len(want) {
		t.Fatalf("decoded sample count=%d want %d", len(got), len(want))
	}
	assertRecoveryPCMExact(t, got, want)
	cmp := compareWaveformF32(got, want)
	qualitycompare.AssertQuality(t, cmp, qualityBarWaveformNearExact, "16k-SILK-1ch-gap@4")
}

// ---------------------------------------------------------------------------
// Per-stream isolation: only the gapped stream should produce PLC audio while
// the others decode normally.
// ---------------------------------------------------------------------------

// TestLibopus_MSRecovery_PerStreamIsolation feeds a 6-channel (5.1) multistream
// sequence to gopus and libopus.  In the reference the entire multistream
// packet is nil (simulating loss of the network packet), so ALL streams PLC
// simultaneously — same as libopus opus_multistream_decode_float(NULL, 0, ...).
// This confirms per-stream state isolation is correct (the other streams resume
// normally after recovery).
func TestLibopus_MSRecovery_PerStreamIsolation(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		channels   = 6
		sampleRate = 48000
		bitrate    = 256000
		numFrames  = 10
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
	enc.SetBitrate(bitrate)
	enc.SetMode(internalenc.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)

	packets := make([][]byte, numFrames)
	for i := range packets {
		pcm := generateMultichannelSine(channels, frameSize)
		p, encErr := encodePacket(enc, pcm, frameSize)
		if encErr != nil {
			t.Fatalf("frame %d Encode: %v", i, encErr)
		}
		if p == nil {
			p = []byte{0xF8, 0xFF, 0xFE}
		}
		packets[i] = p
	}

	// Drop frames 3 and 4 (two consecutive losses, then resume)
	seq := make([][]byte, numFrames)
	copy(seq, packets)
	seq[3] = nil
	seq[4] = nil

	want, err := decodeWithLibopusReferencePackets(
		1, sampleRate, channels, streams, coupled, frameSize, mapping, nil, seq,
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "reference per-stream isolation", err)
	}

	dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	got := make([]float32, 0, len(want))
	for i, p := range seq {
		frame, decErr := dec.Decode(p, frameSize)
		if decErr != nil {
			t.Fatalf("frame %d Decode: %v", i, decErr)
		}
		got = append(got, frame...)
	}
	if len(got) != len(want) {
		t.Fatalf("decoded sample count=%d want %d", len(got), len(want))
	}

	// Assert full-sequence quality: both the PLC frames AND the resumed
	// good frames must match libopus.
	assertRecoveryPCMExact(t, got, want)
	cmp := compareWaveformF32(got, want)
	qualitycompare.AssertQuality(t, cmp, qualityBarWaveformNearExact, "5.1-CELT-per-stream-isolation-gap@3,4")

	// Additionally assert the resumed frames (5..9) agree closely: extract
	// the post-recovery tail and score it independently.
	skipSamples := 5 * frameSize * channels
	if len(got) > skipSamples && len(want) > skipSamples {
		cmpTail := compareWaveformF32(got[skipSamples:], want[skipSamples:])
		qualitycompare.AssertQuality(t, cmpTail, qualityBarWaveformNearExact, "5.1-CELT-per-stream-isolation-resumed-tail")
	}
}

// assertRecoveryPCMExact preserves the complete selected-C recovery output.
func assertRecoveryPCMExact(t *testing.T, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("PCM length=%d want=%d", len(got), len(want))
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("PCM sample %d bits=%08x want=%08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}
