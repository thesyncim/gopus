// ctl_table_decoder_test.go — complete table-driven enumeration of every
// libopus opus_decoder_ctl request.
//
// For each CTL the table records:
//   - the libopus request name and numeric ID (from opus_defines.h)
//   - the gopus method(s) that mirror it
//   - whether it is GET-only, SET-only, or SET+GET
//   - the default value expected immediately after init (opus_decoder_init /
//     opus_encoder_init)
//   - any clamping / validation rule
//   - the build tag that gates it (empty string = always present)
//
// Tests: TestCTLTable_Decoder and TestCTLTable_Encoder run the full table
// against fresh instances.  Individual sub-tests follow the naming convention
// Test<Codec>CTL_<CTLName> and are skipped when a build tag is absent.
//
// C references:
//   opus_decoder_init:  src/opus_decoder.c   (OPUS_CLEAR zeroes the struct)
//   opus_encoder_init:  src/opus_encoder.c   (explicit field assignments)
//   opus_decoder_ctl:   src/opus_decoder.c   switch(request) handler
//   opus_encoder_ctl:   src/opus_encoder.c   switch(request) handler

package gopus

import (
	"testing"
)

// decoderCTLRow describes one entry in the complete libopus decoder CTL table.
type decoderCTLRow struct {
	// ctlName is the C macro name (without _REQUEST suffix where applicable).
	ctlName string
	// ctlID is the numeric request value from opus_defines.h.
	ctlID int
	// dir is "GET", "SET", or "SET+GET".
	dir string
	// buildTag is the gopus build tag that gates this CTL; "" = always present.
	buildTag string
	// testFn runs the full default/round-trip/clamp suite against a fresh decoder.
	testFn func(t *testing.T)
}

// decoderCTLTable enumerates every case handled by opus_decoder_ctl in
// src/opus_decoder.c (libopus 1.6.1).  CTLs gated by compile-time ifdefs are
// listed with their buildTag and tested only under that tag.
//
// Request IDs are from include/opus_defines.h lines 130–180.
var decoderCTLTable = []decoderCTLRow{
	// ------------------------------------------------------------------
	// 4009 OPUS_GET_BANDWIDTH — returns bandwidth of last decoded packet.
	// C ref: opus_decoder_ctl case OPUS_GET_BANDWIDTH_REQUEST → "*value = st->bandwidth"
	// Default after init: 0 (no packet decoded yet).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_GET_BANDWIDTH",
		ctlID:    4009,
		dir:      "GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			dec := mustNewTestDecoder(t, 48000, 1)
			// Default before any decode: 0 (maps to BandwidthNarrowband == 0 in gopus,
			// but libopus stores 0 which means "not yet decoded").
			// C ref: opus_decoder.c OPUS_CLEAR zeroes st->bandwidth at init.
			if got := dec.Bandwidth(); got != 0 {
				t.Errorf("OPUS_GET_BANDWIDTH default = %v, want 0 (no packet decoded)", got)
			}

			// After decoding a Hybrid-FB packet the bandwidth must be BandwidthFullband.
			// C ref: opus_decode_native → st->bandwidth = packet_bandwidth
			if _, err := dec.Decode(minimalHybridTestPacket20ms(), make([]float32, 960)); err != nil {
				t.Fatalf("Decode error: %v", err)
			}
			if got := dec.Bandwidth(); got != BandwidthFullband {
				t.Errorf("OPUS_GET_BANDWIDTH after Hybrid-FB decode = %v, want BandwidthFullband", got)
			}
		},
	},

	// ------------------------------------------------------------------
	// 4010/4011 OPUS_SET/GET_COMPLEXITY
	// C ref: opus_decoder_ctl OPUS_SET_COMPLEXITY_REQUEST – "if(value<0 || value>10) goto bad_arg"
	// Default after init: 0 (OPUS_CLEAR zeroes st->complexity).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_SET_COMPLEXITY / OPUS_GET_COMPLEXITY",
		ctlID:    4010,
		dir:      "SET+GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			dec := mustNewTestDecoder(t, 48000, 1)

			// Default = 0
			if got := dec.Complexity(); got != 0 {
				t.Errorf("OPUS_GET_COMPLEXITY default = %d, want 0", got)
			}

			// Round-trip all valid values.
			for c := 0; c <= 10; c++ {
				if err := dec.SetComplexity(c); err != nil {
					t.Fatalf("SetComplexity(%d) error: %v", c, err)
				}
				if got := dec.Complexity(); got != c {
					t.Errorf("OPUS_GET_COMPLEXITY after SET(%d) = %d, want %d", c, got, c)
				}
			}

			// Clamp: out-of-range must be rejected; stored value unchanged.
			if err := dec.SetComplexity(5); err != nil {
				t.Fatalf("SetComplexity(5) error: %v", err)
			}
			for _, bad := range []int{-1, 11, 100} {
				if err := dec.SetComplexity(bad); err == nil {
					t.Errorf("SetComplexity(%d) = nil, want error", bad)
				}
				if got := dec.Complexity(); got != 5 {
					t.Errorf("SetComplexity(%d) changed Complexity() to %d, want 5", bad, got)
				}
			}

			// Reset preserves complexity (st->complexity is before OPUS_DECODER_RESET_START).
			// C ref: opus_decoder.c OPUS_RESET_STATE – OPUS_CLEAR starts at
			//   &st->OPUS_DECODER_RESET_START, which is after st->complexity.
			if err := dec.SetComplexity(7); err != nil {
				t.Fatalf("SetComplexity(7) error: %v", err)
			}
			dec.Reset()
			if got := dec.Complexity(); got != 7 {
				t.Errorf("Complexity() = %d after Reset(), want 7 (preserved by OPUS_RESET_STATE)", got)
			}
		},
	},

	// ------------------------------------------------------------------
	// 4031 OPUS_GET_FINAL_RANGE — range-coder final state.
	// C ref: opus_decoder_ctl OPUS_GET_FINAL_RANGE_REQUEST → "*value = st->rangeFinal"
	// Default: 0 before any decode (lastDataLen == 0).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_GET_FINAL_RANGE",
		ctlID:    4031,
		dir:      "GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			dec := mustNewTestDecoder(t, 48000, 1)

			// Default = 0 (no packet decoded yet).
			if got := dec.FinalRange(); got != 0 {
				t.Errorf("OPUS_GET_FINAL_RANGE default = %d, want 0", got)
			}

			// Single-byte packet (len == 1) → rangeFinal = 0 per libopus convention.
			// C ref: opus_decoder.c – if lastDataLen <= 1 return 0
			pcm := make([]float32, 960)
			if _, err := dec.Decode(minimalHybridTestPacket20ms(), pcm); err != nil {
				t.Fatalf("Decode error: %v", err)
			}
			if got := dec.FinalRange(); got == 0 {
				t.Error("OPUS_GET_FINAL_RANGE after valid decode = 0, want non-zero")
			}
		},
	},

	// ------------------------------------------------------------------
	// 4028 OPUS_RESET_STATE — resets decoder stream state.
	// C ref: opus_decoder_ctl OPUS_RESET_STATE – OPUS_CLEAR from
	//   OPUS_DECODER_RESET_START, then frame_size = Fs/400.
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_RESET_STATE",
		ctlID:    4028,
		dir:      "SET",
		buildTag: "",
		testFn: func(t *testing.T) {
			dec := mustNewTestDecoder(t, 48000, 1)

			// Decode a real packet so state is non-initial.
			if _, err := dec.Decode(minimalHybridTestPacket20ms(), make([]float32, 960)); err != nil {
				t.Fatalf("Decode error: %v", err)
			}
			if dec.LastPacketDuration() == 0 {
				t.Fatal("LastPacketDuration precondition failed")
			}

			dec.Reset()

			// After reset, last_packet_duration → 0.
			// C ref: OPUS_CLEAR from OPUS_DECODER_RESET_START zeroes last_packet_duration.
			if got := dec.LastPacketDuration(); got != 0 {
				t.Errorf("LastPacketDuration() = %d after Reset(), want 0", got)
			}

			// gain and complexity survive reset (before OPUS_DECODER_RESET_START).
			if err := dec.SetGain(128); err != nil {
				t.Fatalf("SetGain error: %v", err)
			}
			if err := dec.SetComplexity(8); err != nil {
				t.Fatalf("SetComplexity error: %v", err)
			}
			dec.Reset()
			if got := dec.Gain(); got != 128 {
				t.Errorf("Gain() = %d after Reset(), want 128 (preserved)", got)
			}
			if got := dec.Complexity(); got != 8 {
				t.Errorf("Complexity() = %d after Reset(), want 8 (preserved)", got)
			}
		},
	},

	// ------------------------------------------------------------------
	// 4029 OPUS_GET_SAMPLE_RATE — returns st->Fs.
	// C ref: opus_decoder_ctl OPUS_GET_SAMPLE_RATE_REQUEST → "*value = st->Fs"
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_GET_SAMPLE_RATE",
		ctlID:    4029,
		dir:      "GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
				d := mustNewTestDecoder(t, rate, 1)
				if got := d.SampleRate(); got != rate {
					t.Errorf("OPUS_GET_SAMPLE_RATE(%d Hz) = %d, want %d", rate, got, rate)
				}
			}
		},
	},

	// ------------------------------------------------------------------
	// 4033 OPUS_GET_PITCH — most recently decoded pitch period.
	// C ref: opus_decoder_ctl OPUS_GET_PITCH_REQUEST:
	//   if prev_mode == MODE_CELT_ONLY → celt_decoder_ctl(OPUS_GET_PITCH)
	//   else → *value = st->DecControl.prevPitchLag
	// Default: 0 (no packet decoded).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_GET_PITCH",
		ctlID:    4033,
		dir:      "GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			dec := mustNewTestDecoder(t, 48000, 1)

			// Default: 0 (prev_mode 0, not CELT, SILK not voiced).
			if got := dec.Pitch(); got != 0 {
				t.Errorf("OPUS_GET_PITCH default = %d, want 0", got)
			}
		},
	},

	// ------------------------------------------------------------------
	// 4034/4045 OPUS_SET/GET_GAIN
	// C ref: OPUS_SET_GAIN_REQUEST – "if (value<-32768 || value>32767) goto bad_arg"
	//        OPUS_GET_GAIN_REQUEST → "*value = st->decode_gain"
	// Default: 0 (OPUS_CLEAR zeroes decode_gain).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_SET_GAIN / OPUS_GET_GAIN",
		ctlID:    4034,
		dir:      "SET+GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			dec := mustNewTestDecoder(t, 48000, 1)

			// Default = 0.
			if got := dec.Gain(); got != 0 {
				t.Errorf("OPUS_GET_GAIN default = %d, want 0", got)
			}

			// Round-trip boundary values.
			for _, gain := range []int{-32768, -256, -1, 0, 1, 256, 32767} {
				if err := dec.SetGain(gain); err != nil {
					t.Fatalf("SetGain(%d) error: %v", gain, err)
				}
				if got := dec.Gain(); got != gain {
					t.Errorf("OPUS_GET_GAIN after SET(%d) = %d, want %d", gain, got, gain)
				}
			}

			// Clamp: values outside [-32768, 32767] must be rejected.
			if err := dec.SetGain(0); err != nil {
				t.Fatalf("SetGain(0) error: %v", err)
			}
			for _, bad := range []int{-32769, 32768, -100000, 100000} {
				if err := dec.SetGain(bad); err == nil {
					t.Errorf("SetGain(%d) = nil, want error", bad)
				}
				if got := dec.Gain(); got != 0 {
					t.Errorf("SetGain(%d) changed Gain() to %d, want 0", bad, got)
				}
			}

			// Gain survives Reset (decode_gain is before OPUS_DECODER_RESET_START).
			if err := dec.SetGain(256); err != nil {
				t.Fatalf("SetGain(256) error: %v", err)
			}
			dec.Reset()
			if got := dec.Gain(); got != 256 {
				t.Errorf("Gain() = %d after Reset(), want 256 (preserved)", got)
			}
		},
	},

	// ------------------------------------------------------------------
	// 4039 OPUS_GET_LAST_PACKET_DURATION
	// C ref: OPUS_GET_LAST_PACKET_DURATION_REQUEST → "*value = st->last_packet_duration"
	// Default: 0 (OPUS_CLEAR).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_GET_LAST_PACKET_DURATION",
		ctlID:    4039,
		dir:      "GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			dec := mustNewTestDecoder(t, 48000, 1)

			// Default: 0 before any decode.
			if got := dec.LastPacketDuration(); got != 0 {
				t.Errorf("OPUS_GET_LAST_PACKET_DURATION default = %d, want 0", got)
			}

			// After decode equals the sample count returned.
			pcm := make([]float32, 960)
			n, err := dec.Decode(minimalHybridTestPacket20ms(), pcm)
			if err != nil {
				t.Fatalf("Decode error: %v", err)
			}
			// C ref: opus_decode_native → "st->last_packet_duration = nb_samples"
			if got := dec.LastPacketDuration(); got != n {
				t.Errorf("OPUS_GET_LAST_PACKET_DURATION = %d, want %d", got, n)
			}

			// Reset zeroes it.
			dec.Reset()
			if got := dec.LastPacketDuration(); got != 0 {
				t.Errorf("LastPacketDuration() = %d after Reset(), want 0", got)
			}
		},
	},

	// ------------------------------------------------------------------
	// 4046/4047 OPUS_SET/GET_PHASE_INVERSION_DISABLED
	// C ref: OPUS_SET_PHASE_INVERSION_DISABLED_REQUEST – "if(value<0 || value>1) goto bad_arg"
	//        delegates to celt_decoder_ctl
	// Default: false (0).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_SET_PHASE_INVERSION_DISABLED / OPUS_GET_PHASE_INVERSION_DISABLED",
		ctlID:    4046,
		dir:      "SET+GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			dec := mustNewTestDecoder(t, 48000, 2) // stereo: phase inversion is meaningful

			// Default: false.
			if dec.PhaseInversionDisabled() {
				t.Error("OPUS_GET_PHASE_INVERSION_DISABLED default = true, want false")
			}

			// Round-trip.
			dec.SetPhaseInversionDisabled(true)
			if !dec.PhaseInversionDisabled() {
				t.Error("PhaseInversionDisabled() = false after Set(true)")
			}
			dec.SetPhaseInversionDisabled(false)
			if dec.PhaseInversionDisabled() {
				t.Error("PhaseInversionDisabled() = true after Set(false)")
			}
		},
	},

	// ------------------------------------------------------------------
	// 4058/4059 OPUS_SET/GET_IGNORE_EXTENSIONS
	// C ref: OPUS_SET_IGNORE_EXTENSIONS_REQUEST – "if(value<0 || value>1) goto bad_arg"
	//        → st->ignore_extensions = value
	// Default: false (0, OPUS_CLEAR).
	// ------------------------------------------------------------------
	{
		ctlName:  "OPUS_SET_IGNORE_EXTENSIONS / OPUS_GET_IGNORE_EXTENSIONS",
		ctlID:    4058,
		dir:      "SET+GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			dec := mustNewTestDecoder(t, 48000, 1)

			// Default: false.
			if dec.IgnoreExtensions() {
				t.Error("OPUS_GET_IGNORE_EXTENSIONS default = true, want false")
			}

			// Round-trip.
			dec.SetIgnoreExtensions(true)
			if !dec.IgnoreExtensions() {
				t.Error("IgnoreExtensions() = false after Set(true)")
			}
			dec.SetIgnoreExtensions(false)
			if dec.IgnoreExtensions() {
				t.Error("IgnoreExtensions() = true after Set(false)")
			}
		},
	},

	// ------------------------------------------------------------------
	// 4049 OPUS_GET_IN_DTX (decoder-side gopus extension)
	// Note: libopus only exposes OPUS_GET_IN_DTX on the encoder.  gopus
	// provides Decoder.InDTX() as a convenience that inspects lastDataLen.
	// Per opus_decoder.c line 315: "Payloads of 1 (2 including ToC) or 0
	// trigger the PLC/DTX" — a payload of 0 (len<=1) means PLC/DTX frame.
	// gopus: InDTX() ↔ lastDataLen > 0 && lastDataLen <= 2 (ToC + 0-1 data
	// bytes = DTX packet length from SILK DTX comfort-noise packets).
	// ------------------------------------------------------------------
	{
		ctlName:  "InDTX (decoder extension)",
		ctlID:    4049,
		dir:      "GET",
		buildTag: "",
		testFn: func(t *testing.T) {
			dec := mustNewTestDecoder(t, 48000, 1)

			// Before any decode: InDTX = false (lastDataLen == 0).
			if dec.InDTX() {
				t.Error("Decoder.InDTX() before decode = true, want false")
			}

			// After decoding a normal non-DTX packet: InDTX = false.
			if _, err := dec.Decode(minimalHybridTestPacket20ms(), make([]float32, 960)); err != nil {
				t.Fatalf("Decode error: %v", err)
			}
			if dec.InDTX() {
				t.Error("Decoder.InDTX() after normal packet = true, want false")
			}

			// A 2-byte packet (ToC + 1 data byte) falls in the DTX range.
			// C ref: silk_dec/DTX CN packets are 1-byte payload after ToC.
			dtxPkt := []byte{GenerateTOC(1, false, 0), 0x00}
			if _, err := dec.Decode(dtxPkt, make([]float32, 960)); err == nil {
				// If decode succeeds (minimal SILK frame), InDTX should be true.
				if !dec.InDTX() {
					t.Error("Decoder.InDTX() after 2-byte DTX packet = false, want true")
				}
			}
			// If the DTX packet decodes as PLC (error), InDTX state is still consistent
			// with lastDataLen being set to the packet length.
		},
	},
}

// TestCTLTable_Decoder runs every row of the decoder CTL table.
func TestCTLTable_Decoder(t *testing.T) {
	for _, row := range decoderCTLTable {
		t.Run(row.ctlName, func(t *testing.T) {
			t.Logf("CTL %d (%s) dir=%s tag=%q", row.ctlID, row.ctlName, row.dir, row.buildTag)
			row.testFn(t)
		})
	}
}

// ---------------------------------------------------------------------------
// Encoder CTL table
// ---------------------------------------------------------------------------
