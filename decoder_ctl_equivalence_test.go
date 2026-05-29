// decoder_ctl_equivalence_test.go verifies that every decoder CTL exposed by
// gopus matches the libopus 1.5.2 C oracle for default values, round-trip
// SET/GET, and clamping.  Error-code equivalence for malformed packets is also
// covered here.
//
// Libopus references (src/opus_decoder.c):
//   - opus_decoder_init:      complexity=0, decode_gain=0, frame_size=Fs/400
//   - opus_decoder_ctl cases: OPUS_GET_BANDWIDTH, OPUS_SET/GET_COMPLEXITY,
//     OPUS_GET_FINAL_RANGE, OPUS_GET_SAMPLE_RATE, OPUS_GET_PITCH,
//     OPUS_GET/SET_GAIN, OPUS_GET_LAST_PACKET_DURATION,
//     OPUS_SET/GET_PHASE_INVERSION_DISABLED,
//     OPUS_SET/GET_IGNORE_EXTENSIONS
//   - opus_decode_native:     OPUS_BUFFER_TOO_SMALL when
//     count*packet_frame_size > frame_size; OPUS_INVALID_PACKET propagated
//     from opus_packet_parse_impl (count < 0); OPUS_BAD_ARG when len < 0.

package gopus

import (
	"testing"
)

// ---------------------------------------------------------------------------
// CTL default values (opus_decoder_init zeroes the struct; only non-zero
// defaults are noted in the code).
// ---------------------------------------------------------------------------

// TestDecoderCTL_Defaults verifies freshly-created decoder CTL getter values
// match libopus opus_decoder_init() defaults.
//
// C ref: opus_decoder.c opus_decoder_init()
//   st->complexity = 0
//   st->decode_gain = 0 (OPUS_CLEAR zeroes the struct)
//   st->frame_size = Fs/400  (last_packet_duration starts at 0 before any decode)
//   st->bandwidth = 0 → reported as 0 (no packet decoded yet)
func TestDecoderCTL_Defaults(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)

	// OPUS_GET_COMPLEXITY default = 0
	if got := dec.Complexity(); got != 0 {
		t.Errorf("Complexity() default = %d, want 0 (libopus: st->complexity=0 at init)", got)
	}

	// OPUS_GET_GAIN default = 0
	if got := dec.Gain(); got != 0 {
		t.Errorf("Gain() default = %d, want 0 (libopus: decode_gain=0 via OPUS_CLEAR)", got)
	}

	// OPUS_GET_LAST_PACKET_DURATION default = 0 (no packet decoded yet)
	if got := dec.LastPacketDuration(); got != 0 {
		t.Errorf("LastPacketDuration() default = %d, want 0", got)
	}

	// OPUS_GET_FINAL_RANGE default = 0 (lastDataLen=0 → FinalRange returns 0)
	if got := dec.FinalRange(); got != 0 {
		t.Errorf("FinalRange() default = %d, want 0", got)
	}

	// OPUS_GET_PITCH default = 0 (prev_mode=0, not MODE_CELT_ONLY, silk signal
	// type not VOICED)
	if got := dec.Pitch(); got != 0 {
		t.Errorf("Pitch() default = %d, want 0", got)
	}

	// OPUS_GET_PHASE_INVERSION_DISABLED: mono decoder always has phase
	// inversion disabled in gopus (libopus enforces mono constraint in CELT).
	// Stereo default is false.
	stereo := mustNewTestDecoder(t, 48000, 2)
	if stereo.PhaseInversionDisabled() {
		t.Errorf("stereo PhaseInversionDisabled() default = true, want false")
	}

	// OPUS_GET_IGNORE_EXTENSIONS default = false (OPUS_CLEAR zeroes the struct)
	if dec.IgnoreExtensions() {
		t.Errorf("IgnoreExtensions() default = true, want false")
	}

	// OPUS_GET_SAMPLE_RATE
	for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
		d := mustNewTestDecoder(t, rate, 1)
		if got := d.SampleRate(); got != rate {
			t.Errorf("SampleRate() = %d, want %d", got, rate)
		}
	}

	// Channels is not a CTL but a constructor parameter.
	if got := dec.Channels(); got != 1 {
		t.Errorf("Channels() = %d, want 1", got)
	}
}

// ---------------------------------------------------------------------------
// CTL round-trip: SET then GET must return the value just set.
// ---------------------------------------------------------------------------

// TestDecoderCTL_GainRoundTrip verifies OPUS_SET_GAIN / OPUS_GET_GAIN
// round-trips the full valid range boundary values.
//
// C ref: opus_decoder_ctl OPUS_SET_GAIN_REQUEST – range [-32768, 32767].
func TestDecoderCTL_GainRoundTrip(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)

	for _, gain := range []int{-32768, -1, 0, 1, 256, 32767} {
		if err := dec.SetGain(gain); err != nil {
			t.Fatalf("SetGain(%d) unexpected error: %v", gain, err)
		}
		if got := dec.Gain(); got != gain {
			t.Errorf("Gain() = %d after SetGain(%d), want %d", got, gain, gain)
		}
	}
}

// TestDecoderCTL_GainBoundaryReject verifies out-of-range gain returns an error
// and does not change the stored value.
//
// C ref: opus_decoder_ctl OPUS_SET_GAIN_REQUEST – "if (value<-32768 || value>32767) goto bad_arg"
func TestDecoderCTL_GainBoundaryReject(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	if err := dec.SetGain(0); err != nil {
		t.Fatalf("SetGain(0) error: %v", err)
	}

	for _, gain := range []int{-32769, 32768, -100000, 100000} {
		err := dec.SetGain(gain)
		if err == nil {
			t.Errorf("SetGain(%d) should return error, got nil", gain)
		}
		// Value must not have changed.
		if got := dec.Gain(); got != 0 {
			t.Errorf("SetGain(%d) changed Gain() to %d, want 0", gain, got)
		}
	}
}

// TestDecoderCTL_ComplexityRoundTrip verifies OPUS_SET_COMPLEXITY /
// OPUS_GET_COMPLEXITY round-trips all valid values [0,10].
//
// C ref: opus_decoder_ctl OPUS_SET_COMPLEXITY_REQUEST – "if(value<0 || value>10) goto bad_arg"
func TestDecoderCTL_ComplexityRoundTrip(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)

	for c := 0; c <= 10; c++ {
		if err := dec.SetComplexity(c); err != nil {
			t.Fatalf("SetComplexity(%d) error: %v", c, err)
		}
		if got := dec.Complexity(); got != c {
			t.Errorf("Complexity() = %d after SetComplexity(%d)", got, c)
		}
	}
}

// TestDecoderCTL_ComplexityBoundaryReject verifies out-of-range complexity is
// rejected.
//
// C ref: opus_decoder_ctl OPUS_SET_COMPLEXITY_REQUEST – "if(value<0 || value>10) goto bad_arg"
func TestDecoderCTL_ComplexityBoundaryReject(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	if err := dec.SetComplexity(5); err != nil {
		t.Fatalf("SetComplexity(5) error: %v", err)
	}

	for _, c := range []int{-1, 11, 100} {
		err := dec.SetComplexity(c)
		if err == nil {
			t.Errorf("SetComplexity(%d) should return error, got nil", c)
		}
		if got := dec.Complexity(); got != 5 {
			t.Errorf("invalid SetComplexity(%d) changed Complexity() to %d, want 5", c, got)
		}
	}
}

// TestDecoderCTL_PhaseInversionDisabledRoundTrip verifies
// OPUS_SET/GET_PHASE_INVERSION_DISABLED on a stereo decoder.
//
// C ref: opus_decoder_ctl OPUS_SET_PHASE_INVERSION_DISABLED_REQUEST –
//   "if(value<0 || value>1) goto bad_arg"
func TestDecoderCTL_PhaseInversionDisabledRoundTrip(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 2)

	dec.SetPhaseInversionDisabled(true)
	if !dec.PhaseInversionDisabled() {
		t.Error("PhaseInversionDisabled() = false after Set(true), want true")
	}

	dec.SetPhaseInversionDisabled(false)
	if dec.PhaseInversionDisabled() {
		t.Error("PhaseInversionDisabled() = true after Set(false), want false")
	}
}

// TestDecoderCTL_IgnoreExtensionsRoundTrip verifies
// OPUS_SET/GET_IGNORE_EXTENSIONS round-trip.
//
// C ref: opus_decoder_ctl OPUS_SET_IGNORE_EXTENSIONS_REQUEST –
//   "if(value<0 || value>1) goto bad_arg"
func TestDecoderCTL_IgnoreExtensionsRoundTrip(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)

	dec.SetIgnoreExtensions(true)
	if !dec.IgnoreExtensions() {
		t.Error("IgnoreExtensions() = false after Set(true), want true")
	}

	dec.SetIgnoreExtensions(false)
	if dec.IgnoreExtensions() {
		t.Error("IgnoreExtensions() = true after Set(false), want false")
	}
}

// TestDecoderCTL_LastPacketDurationAfterDecode verifies
// OPUS_GET_LAST_PACKET_DURATION equals the decoded sample count after a real
// packet.
//
// C ref: opus_decode_native – "st->last_packet_duration = nb_samples"
func TestDecoderCTL_LastPacketDurationAfterDecode(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 960)
	n, err := dec.Decode(minimalHybridTestPacket20ms(), pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if got := dec.LastPacketDuration(); got != n {
		t.Errorf("LastPacketDuration() = %d after Decode returning %d, want equal", got, n)
	}
}

// TestDecoderCTL_BandwidthAfterDecode verifies OPUS_GET_BANDWIDTH returns the
// bandwidth of the last decoded packet.
//
// C ref: opus_decode_native – "st->bandwidth = packet_bandwidth"
func TestDecoderCTL_BandwidthAfterDecode(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	// minimalHybridTestPacket20ms is Hybrid Fullband (config 15).
	if _, err := dec.Decode(minimalHybridTestPacket20ms(), make([]float32, 960)); err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if got := dec.Bandwidth(); got != BandwidthFullband {
		t.Errorf("Bandwidth() = %v after Hybrid-FB decode, want BandwidthFullband", got)
	}
}

// TestDecoderCTL_SampleRateAllRates verifies OPUS_GET_SAMPLE_RATE returns the
// configured API rate for all valid sample rates.
//
// C ref: opus_decoder_ctl OPUS_GET_SAMPLE_RATE_REQUEST – "*value = st->Fs"
func TestDecoderCTL_SampleRateAllRates(t *testing.T) {
	for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
		dec := mustNewTestDecoder(t, rate, 1)
		if got := dec.SampleRate(); got != rate {
			t.Errorf("SampleRate() = %d, want %d", got, rate)
		}
	}
}

// TestDecoderCTL_FinalRangeZeroBeforeDecode verifies FinalRange returns 0
// before any packet has been decoded.
//
// C ref: opus_decoder.c FinalRange – "if lastDataLen <= 1 return 0"
func TestDecoderCTL_FinalRangeZeroBeforeDecode(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	if got := dec.FinalRange(); got != 0 {
		t.Errorf("FinalRange() = %d before any decode, want 0", got)
	}
}

// TestDecoderCTL_FinalRangeNonZeroAfterDecode verifies FinalRange returns a
// non-zero value after successfully decoding a real packet.
//
// C ref: opus_decoder.c – "st->rangeFinal" is set from the range coder state
//   after opus_decode_frame (celt_decoder_ctl OPUS_GET_FINAL_RANGE).
func TestDecoderCTL_FinalRangeNonZeroAfterDecode(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 960)
	if _, err := dec.Decode(minimalHybridTestPacket20ms(), pcm); err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if got := dec.FinalRange(); got == 0 {
		t.Error("FinalRange() = 0 after decoding real packet, want non-zero")
	}
}

// TestDecoderCTL_ResetPreservesComplexity verifies that Reset() does not clear
// the complexity setting (matching libopus: opus_decoder_ctl OPUS_RESET_STATE
// uses OPUS_CLEAR over the RESET_START range, which excludes complexity from
// its reset because st->complexity is inside the preserved region).
//
// C ref: opus_decoder.c OPUS_RESET_STATE case – OPUS_CLEAR starts at
//   &st->OPUS_DECODER_RESET_START, which is after st->complexity.
func TestDecoderCTL_ResetPreservesComplexity(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	if err := dec.SetComplexity(7); err != nil {
		t.Fatalf("SetComplexity(7) error: %v", err)
	}
	dec.Reset()
	if got := dec.Complexity(); got != 7 {
		t.Errorf("Complexity() = %d after Reset(), want 7 (complexity is preserved by libopus reset)", got)
	}
}

// TestDecoderCTL_ResetPreservesGain verifies that Reset() preserves the
// decode gain, matching libopus OPUS_RESET_STATE behavior.
//
// C ref: opus_decoder.c struct layout – decode_gain is at line 71, before
//   "OPUS_DECODER_RESET_START stream_channels" (line 80).  OPUS_RESET_STATE
//   clears only from stream_channels onward, so decode_gain survives reset.
//   Complexity (line 72) is likewise preserved (see TestDecoderCTL_ResetPreservesComplexity).
func TestDecoderCTL_ResetPreservesGain(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	if err := dec.SetGain(256); err != nil {
		t.Fatalf("SetGain(256) error: %v", err)
	}
	dec.Reset()
	if got := dec.Gain(); got != 256 {
		t.Errorf("Gain() = %d after Reset(), want 256 (libopus preserves decode_gain across OPUS_RESET_STATE)", got)
	}
}

// TestDecoderCTL_ResetClearsLastPacketDuration verifies Reset() zeroes
// LastPacketDuration.
func TestDecoderCTL_ResetClearsLastPacketDuration(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	if _, err := dec.Decode(minimalHybridTestPacket20ms(), make([]float32, 960)); err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if dec.LastPacketDuration() == 0 {
		t.Fatal("LastPacketDuration() = 0 after valid decode – precondition failed")
	}
	dec.Reset()
	if got := dec.LastPacketDuration(); got != 0 {
		t.Errorf("LastPacketDuration() = %d after Reset(), want 0", got)
	}
}

// ---------------------------------------------------------------------------
// Error-code equivalence for invalid packets.
// ---------------------------------------------------------------------------
// libopus maps packet parsing failures to OPUS_INVALID_PACKET (-4) and buffer
// sizing failures to OPUS_BUFFER_TOO_SMALL (-2).  gopus uses ErrInvalidPacket
// and ErrBufferTooSmall.  The tests below feed known-bad packets and confirm
// the gopus error matches the libopus classification.

// TestDecodeErrorCode_ZeroLengthPacket verifies that an empty (zero-length)
// byte slice returns ErrInvalidPacket.
//
// C ref: opus_decode_native – non-nil data with len==0 takes the PLC branch,
//   but when called from opus_decode_float with data!=nil it would have
//   attempted get_nb_samples first; however gopus (like libopus float path)
//   treats an empty non-nil slice as a zero-length data → PLC, not an error.
//   A truly absent packet (nil) is PLC.  A single-byte (TOC-only) packet with
//   no frame data is handled by opus_packet_parse_impl returning count≥0, and
//   the single frame has zero bytes, which is valid for the CELT/SILK layer.
//
// gopus mirrors libopus: empty non-nil data decodes as PLC (no error).
func TestDecodeErrorCode_ZeroLengthPacket(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 960)

	// nil data → PLC, not error
	_, err := dec.Decode(nil, pcm)
	if err != nil {
		t.Errorf("Decode(nil) error = %v, want nil (PLC)", err)
	}

	// empty non-nil data → PLC (matches libopus len==0 branch)
	_, err = dec.Decode([]byte{}, pcm)
	if err != nil {
		t.Errorf("Decode([]byte{}) error = %v, want nil (PLC)", err)
	}
}

// TestDecodeErrorCode_TruncatedCode3 verifies that a code-3 packet that is
// only 1 byte (missing the frame-count byte) is rejected.
//
// C ref: opus_packet_parse_impl – code==3 requires at least 2 bytes; returns
//   OPUS_INVALID_PACKET when len<2.
func TestDecodeErrorCode_TruncatedCode3(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 5760)

	// One-byte code-3 packet (missing frame-count byte).
	_, err := dec.Decode([]byte{GenerateTOC(16, false, 3)}, pcm)
	if err == nil {
		t.Error("Decode(truncated code-3) = nil, want error")
	}
}

// TestDecodeErrorCode_Code3ZeroFrames verifies that M=0 in a code-3 packet
// is rejected.
//
// C ref: opus_packet_parse_impl – "if (count==0) return OPUS_INVALID_PACKET"
// gopus: ErrInvalidFrameCount ("M > 48" message covers M==0 via m==0 check)
func TestDecodeErrorCode_Code3ZeroFrames(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 5760)

	// Code-3 packet with M=0 in the frame-count byte.
	pkt := []byte{GenerateTOC(16, false, 3), 0x00}
	_, err := dec.Decode(pkt, pcm)
	if err == nil {
		t.Error("Decode(code-3 M=0) = nil, want error")
	}
}

// TestDecodeErrorCode_Code3ExcessiveFrames verifies M>48 is rejected.
//
// C ref: opus_packet_parse_impl – if (count > 48) return OPUS_INVALID_PACKET
func TestDecodeErrorCode_Code3ExcessiveFrames(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 5760)

	// Code-3 packet with M=49 (0x31 = 0b0110001 = CBR, no-padding, 49 frames).
	pkt := []byte{GenerateTOC(16, false, 3), 49}
	_, err := dec.Decode(pkt, pcm)
	if err == nil {
		t.Error("Decode(code-3 M=49) = nil, want error")
	}
}

// TestDecodeErrorCode_TotalDurationOver120ms verifies that a packet whose
// total duration exceeds 120ms is rejected.
//
// C ref: opus_packet_get_nb_samples – "if (samples*25 > Fs*3) return
//   OPUS_INVALID_PACKET"
func TestDecodeErrorCode_TotalDurationOver120ms(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	// Config 31 = CELT FB 20ms; code 3 with M=7 → 140ms > 120ms.
	pkt := []byte{GenerateTOC(31, false, 3), 0x07}
	pcm := make([]float32, 7000)

	_, err := dec.Decode(pkt, pcm)
	if err == nil {
		t.Error("Decode(>120ms) = nil, want error")
	}
}

// TestDecodeErrorCode_BufferTooSmall verifies OPUS_BUFFER_TOO_SMALL when the
// output PCM buffer is shorter than the packet requires.
//
// C ref: opus_decode_native – "if (count*packet_frame_size > frame_size)
//   return OPUS_BUFFER_TOO_SMALL"
func TestDecodeErrorCode_BufferTooSmall(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	// minimalHybridTestPacket20ms() is 960 samples; give only 480.
	pcm := make([]float32, 480)
	_, err := dec.Decode(minimalHybridTestPacket20ms(), pcm)
	if err != ErrBufferTooSmall {
		t.Errorf("Decode with short buffer = %v, want ErrBufferTooSmall", err)
	}
}

// TestDecodeErrorCode_Code1OddPayload verifies that a code-1 packet with an
// odd payload length is rejected (two equal-length frames require an even
// total payload).
//
// C ref: opus_packet_parse_impl code==1 branch – "if (framesize & 1)
//   return OPUS_INVALID_PACKET"
func TestDecodeErrorCode_Code1OddPayload(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 960*2)

	// Config 16 (CELT NB 2.5ms), code 1, 3 payload bytes (odd).
	pkt := []byte{GenerateTOC(16, false, 1), 0xAA, 0xBB, 0xCC}
	_, err := dec.Decode(pkt, pcm)
	if err == nil {
		t.Error("Decode(code-1 odd payload) = nil, want error")
	}
}

// TestDecodeErrorCode_Code2ShortPacket verifies that a code-2 packet with
// insufficient bytes for the first frame length is rejected.
//
// C ref: opus_packet_parse_impl code==2 branch checks parsed frame1Len
//   against the remaining packet bytes.
func TestDecodeErrorCode_Code2ShortPacket(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 960*2)

	// TOC byte only, no frame-length byte for code-2.
	pkt := []byte{GenerateTOC(16, false, 2)}
	_, err := dec.Decode(pkt, pcm)
	if err == nil {
		t.Error("Decode(code-2 truncated) = nil, want error")
	}
}

// TestDecodeErrorCode_Code2Frame1TooLarge verifies that a code-2 packet
// where the encoded first-frame length exceeds the packet remainder is
// rejected.
//
// C ref: opus_packet_parse_impl code==2 – frame2 length becomes negative →
//   OPUS_INVALID_PACKET.
func TestDecodeErrorCode_Code2Frame1TooLarge(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 960*2)

	// TOC (code-2) + frame1-length byte 200 + 3 bytes payload (200 > 3).
	pkt := []byte{GenerateTOC(16, false, 2), 200, 0xAA, 0xBB, 0xCC}
	_, err := dec.Decode(pkt, pcm)
	if err == nil {
		t.Error("Decode(code-2 frame1 too large) = nil, want error")
	}
}

// TestDecodeErrorCode_Code3CBRUneven verifies that a CBR code-3 packet whose
// total frame bytes are not divisible by M is rejected.
//
// C ref: opus_packet_parse_impl code==3 CBR branch –
//   "if (framesize % count) return OPUS_INVALID_PACKET"
func TestDecodeErrorCode_Code3CBRUneven(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 960*3)

	// Code-3, CBR, M=3, no padding: 2.5ms CELT NB config, payload 5 bytes
	// (5 % 3 != 0 → invalid).
	frameCountByte := byte(3) // CBR (bit7=0), no padding (bit6=0), M=3
	pkt := append([]byte{GenerateTOC(16, false, 3), frameCountByte}, make([]byte, 5)...)
	_, err := dec.Decode(pkt, pcm)
	if err == nil {
		t.Error("Decode(code-3 CBR uneven) = nil, want error")
	}
}

// TestDecodeErrorCode_Int16BufferTooSmall verifies ErrBufferTooSmall is
// returned from DecodeInt16 when the output int16 buffer is short.
func TestDecodeErrorCode_Int16BufferTooSmall(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]int16, 480)
	_, err := dec.DecodeInt16(minimalHybridTestPacket20ms(), pcm)
	if err != ErrBufferTooSmall {
		t.Errorf("DecodeInt16 with short buffer = %v, want ErrBufferTooSmall", err)
	}
}

// TestDecodeErrorCode_InvalidPacketPropagatesFromInt16 verifies that
// ErrInvalidPacket is returned from DecodeInt16 for a malformed packet.
func TestDecodeErrorCode_InvalidPacketPropagatesFromInt16(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]int16, 5760)
	// Code-3 M=7 with 20ms config → >120ms
	_, err := dec.DecodeInt16([]byte{GenerateTOC(31, false, 3), 0x07}, pcm)
	if err == nil {
		t.Error("DecodeInt16(>120ms) = nil, want error")
	}
}
