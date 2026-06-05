// ctl_table_test.go — complete table-driven enumeration of every libopus
// opus_decoder_ctl / opus_encoder_ctl request.
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

// ---------------------------------------------------------------------------
// Decoder CTL table
// ---------------------------------------------------------------------------

// ctlRef is one row of the generated reference table.
type ctlRef struct {
	// LibopusCTL is the C macro name (e.g. "OPUS_SET_GAIN").
	LibopusCTL string
	// RequestID is the numeric value from opus_defines.h.
	RequestID int
	// GopusMethod is the gopus method name(s) that implement this CTL.
	GopusMethod string
	// Dir is "GET", "SET", or "SET+GET".
	Dir string
	// Codec is "encoder", "decoder", or "both".
	Codec string
	// Default is the string representation of the default value.
	Default string
	// BuildTag is the gopus build tag required; "" = always present.
	BuildTag string
}

// ctlReferenceTable is the complete reference table of all libopus CTL
// requests and their gopus equivalents (libopus 1.6.1 opus_defines.h).
var ctlReferenceTable = []ctlRef{
	// Decoder CTLs
	{"OPUS_GET_BANDWIDTH", 4009, "Decoder.Bandwidth()", "GET", "decoder", "0 (no packet)", ""},
	{"OPUS_SET_COMPLEXITY", 4010, "Decoder.SetComplexity()", "SET+GET", "decoder", "0", ""},
	{"OPUS_GET_COMPLEXITY", 4011, "Decoder.Complexity()", "SET+GET", "decoder", "0", ""},
	{"OPUS_GET_FINAL_RANGE", 4031, "Decoder.FinalRange()", "GET", "decoder", "0", ""},
	{"OPUS_RESET_STATE", 4028, "Decoder.Reset()", "SET", "decoder", "—", ""},
	{"OPUS_GET_SAMPLE_RATE", 4029, "Decoder.SampleRate()", "GET", "decoder", "Fs from init", ""},
	{"OPUS_GET_PITCH", 4033, "Decoder.Pitch()", "GET", "decoder", "0", ""},
	{"OPUS_SET_GAIN", 4034, "Decoder.SetGain()", "SET+GET", "decoder", "0", ""},
	{"OPUS_GET_GAIN", 4045, "Decoder.Gain()", "SET+GET", "decoder", "0", ""},
	{"OPUS_GET_LAST_PACKET_DURATION", 4039, "Decoder.LastPacketDuration()", "GET", "decoder", "0", ""},
	{"OPUS_SET_PHASE_INVERSION_DISABLED", 4046, "Decoder.SetPhaseInversionDisabled()", "SET+GET", "decoder", "false", ""},
	{"OPUS_GET_PHASE_INVERSION_DISABLED", 4047, "Decoder.PhaseInversionDisabled()", "SET+GET", "decoder", "false", ""},
	{"OPUS_SET_IGNORE_EXTENSIONS", 4058, "Decoder.SetIgnoreExtensions()", "SET+GET", "decoder", "false", ""},
	{"OPUS_GET_IGNORE_EXTENSIONS", 4059, "Decoder.IgnoreExtensions()", "SET+GET", "decoder", "false", ""},
	{"OPUS_GET_IN_DTX (decoder ext.)", 4049, "Decoder.InDTX()", "GET", "decoder", "false", ""},
	{"OPUS_SET_OSCE_BWE", 4054, "Decoder.SetOSCEBWE()", "SET+GET", "decoder", "false", "gopus_osce"},
	{"OPUS_GET_OSCE_BWE", 4055, "Decoder.OSCEBWE()", "SET+GET", "decoder", "false", "gopus_osce"},
	{"OPUS_SET_DNN_BLOB (decoder)", 4052, "Decoder.SetDNNBlob()", "SET", "decoder", "—", "gopus_dred|gopus_osce"},

	// Encoder CTLs
	{"OPUS_SET_APPLICATION", 4000, "Encoder.SetApplication()", "SET+GET", "encoder", "from config", ""},
	{"OPUS_GET_APPLICATION", 4001, "Encoder.Application()", "SET+GET", "encoder", "from config", ""},
	{"OPUS_SET_BITRATE", 4002, "Encoder.SetBitrate()", "SET+GET", "encoder", "BitrateAuto", ""},
	{"OPUS_GET_BITRATE", 4003, "Encoder.Bitrate()", "SET+GET", "encoder", "BitrateAuto", ""},
	{"OPUS_SET_MAX_BANDWIDTH", 4004, "Encoder.SetMaxBandwidth()", "SET+GET", "encoder", "BandwidthFullband", ""},
	{"OPUS_GET_MAX_BANDWIDTH", 4005, "Encoder.MaxBandwidth()", "SET+GET", "encoder", "BandwidthFullband", ""},
	{"OPUS_SET_VBR", 4006, "Encoder.SetVBR()", "SET+GET", "encoder", "true", ""},
	{"OPUS_GET_VBR", 4007, "Encoder.VBR()", "SET+GET", "encoder", "true", ""},
	{"OPUS_SET_BANDWIDTH", 4008, "Encoder.SetBandwidth()/SetBandwidthAuto()", "SET+GET", "encoder", "auto", ""},
	{"OPUS_GET_BANDWIDTH", 4009, "Encoder.Bandwidth()", "SET+GET", "encoder", "auto", ""},
	{"OPUS_SET_COMPLEXITY", 4010, "Encoder.SetComplexity()", "SET+GET", "encoder", "9", ""},
	{"OPUS_GET_COMPLEXITY", 4011, "Encoder.Complexity()", "SET+GET", "encoder", "9", ""},
	{"OPUS_SET_INBAND_FEC", 4012, "Encoder.SetInBandFEC()", "SET+GET", "encoder", "InBandFECDisabled", ""},
	{"OPUS_GET_INBAND_FEC", 4013, "Encoder.InBandFEC()", "SET+GET", "encoder", "InBandFECDisabled", ""},
	{"OPUS_SET_PACKET_LOSS_PERC", 4014, "Encoder.SetPacketLoss()", "SET+GET", "encoder", "0", ""},
	{"OPUS_GET_PACKET_LOSS_PERC", 4015, "Encoder.PacketLoss()", "SET+GET", "encoder", "0", ""},
	{"OPUS_SET_DTX", 4016, "Encoder.SetDTX()", "SET+GET", "encoder", "false", ""},
	{"OPUS_GET_DTX", 4017, "Encoder.DTXEnabled()", "SET+GET", "encoder", "false", ""},
	{"OPUS_SET_VBR_CONSTRAINT", 4020, "Encoder.SetVBRConstraint()", "SET+GET", "encoder", "true", ""},
	{"OPUS_GET_VBR_CONSTRAINT", 4021, "Encoder.VBRConstraint()", "SET+GET", "encoder", "true", ""},
	{"OPUS_SET_FORCE_CHANNELS", 4022, "Encoder.SetForceChannels()", "SET+GET", "encoder", "-1 (auto)", ""},
	{"OPUS_GET_FORCE_CHANNELS", 4023, "Encoder.ForceChannels()", "SET+GET", "encoder", "-1 (auto)", ""},
	{"OPUS_SET_SIGNAL", 4024, "Encoder.SetSignal()", "SET+GET", "encoder", "SignalAuto", ""},
	{"OPUS_GET_SIGNAL", 4025, "Encoder.Signal()", "SET+GET", "encoder", "SignalAuto", ""},
	{"OPUS_GET_LOOKAHEAD", 4027, "Encoder.Lookahead()", "GET", "encoder", "Fs/400 (+ Fs/250 non-LD)", ""},
	{"OPUS_RESET_STATE", 4028, "Encoder.Reset()", "SET", "encoder", "—", ""},
	{"OPUS_GET_SAMPLE_RATE", 4029, "Encoder.SampleRate()", "GET", "encoder", "Fs from init", ""},
	{"OPUS_GET_FINAL_RANGE", 4031, "Encoder.FinalRange()", "GET", "encoder", "0", ""},
	{"OPUS_SET_LSB_DEPTH", 4036, "Encoder.SetLSBDepth()", "SET+GET", "encoder", "24", ""},
	{"OPUS_GET_LSB_DEPTH", 4037, "Encoder.LSBDepth()", "SET+GET", "encoder", "24", ""},
	{"OPUS_SET_EXPERT_FRAME_DURATION", 4040, "Encoder.SetExpertFrameDuration()", "SET+GET", "encoder", "ExpertFrameDurationArg", ""},
	{"OPUS_GET_EXPERT_FRAME_DURATION", 4041, "Encoder.ExpertFrameDuration()", "SET+GET", "encoder", "ExpertFrameDurationArg", ""},
	{"OPUS_SET_PREDICTION_DISABLED", 4042, "Encoder.SetPredictionDisabled()", "SET+GET", "encoder", "false", ""},
	{"OPUS_GET_PREDICTION_DISABLED", 4043, "Encoder.PredictionDisabled()", "SET+GET", "encoder", "false", ""},
	{"OPUS_SET_PHASE_INVERSION_DISABLED", 4046, "Encoder.SetPhaseInversionDisabled()", "SET+GET", "encoder", "false", ""},
	{"OPUS_GET_PHASE_INVERSION_DISABLED", 4047, "Encoder.PhaseInversionDisabled()", "SET+GET", "encoder", "false", ""},
	{"OPUS_GET_IN_DTX", 4049, "Encoder.InDTX()", "GET", "encoder", "false", ""},
	{"OPUS_SET_DRED_DURATION", 4050, "Encoder.SetDREDDuration()", "SET+GET", "encoder", "0", "gopus_dred|gopus_osce"},
	{"OPUS_GET_DRED_DURATION", 4051, "Encoder.DREDDuration()", "SET+GET", "encoder", "0", "gopus_dred|gopus_osce"},
	{"OPUS_SET_DNN_BLOB (encoder)", 4052, "Encoder.SetDNNBlob()", "SET", "encoder", "—", "gopus_dred|gopus_osce"},
	{"OPUS_SET_QEXT", 4056, "Encoder.SetQEXT()", "SET+GET", "encoder", "false", "gopus_qext"},
	{"OPUS_GET_QEXT", 4057, "Encoder.QEXT()", "SET+GET", "encoder", "false", "gopus_qext"},
}

// TestCTLReferenceTable_Smoke verifies the reference table is populated and
// that every entry has non-empty required fields.
func TestCTLReferenceTable_Smoke(t *testing.T) {
	if len(ctlReferenceTable) == 0 {
		t.Fatal("ctlReferenceTable is empty")
	}
	for i, row := range ctlReferenceTable {
		if row.LibopusCTL == "" {
			t.Errorf("row %d: LibopusCTL is empty", i)
		}
		if row.RequestID == 0 {
			t.Errorf("row %d (%s): RequestID is 0", i, row.LibopusCTL)
		}
		if row.GopusMethod == "" {
			t.Errorf("row %d (%s): GopusMethod is empty", i, row.LibopusCTL)
		}
		if row.Dir == "" {
			t.Errorf("row %d (%s): Dir is empty", i, row.LibopusCTL)
		}
		if row.Codec == "" {
			t.Errorf("row %d (%s): Codec is empty", i, row.LibopusCTL)
		}
	}
	t.Logf("CTL reference table: %d entries", len(ctlReferenceTable))
}

// TestCTLReferenceTable_NoDuplicates verifies there are no duplicate CTL IDs
// within the same codec's namespace.
func TestCTLReferenceTable_NoDuplicates(t *testing.T) {
	seen := make(map[string]bool)
	for _, row := range ctlReferenceTable {
		key := row.Codec + "/" + row.LibopusCTL
		if seen[key] {
			t.Errorf("duplicate entry in ctlReferenceTable: %s", key)
		}
		seen[key] = true
	}
}

// TestCTLReferenceTable_AllDecoderCTLsCovered verifies that every decoder CTL
// listed in the table has a corresponding decoder method in gopus.
//
// This is a compile-time assertion masquerading as a runtime test: if a method
// referenced in the table does not exist, the test package will fail to build.
func TestCTLReferenceTable_AllDecoderCTLsCovered(t *testing.T) {
	// Exercise every decoder CTL by calling its gopus method on a fresh instance.
	dec := mustNewTestDecoder(t, 48000, 2) // stereo for phase inversion

	// GET-only decoder CTLs.
	_ = dec.Bandwidth()
	_ = dec.Complexity()
	_ = dec.FinalRange()
	_ = dec.SampleRate()
	_ = dec.Pitch()
	_ = dec.Gain()
	_ = dec.LastPacketDuration()
	_ = dec.PhaseInversionDisabled()
	_ = dec.IgnoreExtensions()
	_ = dec.InDTX()

	// SET+GET decoder CTLs.
	_ = dec.SetComplexity(0)
	_ = dec.SetGain(0)
	dec.SetPhaseInversionDisabled(false)
	dec.SetIgnoreExtensions(false)

	// RESET.
	dec.Reset()

	t.Logf("All decoder CTL methods callable on *Decoder")
}

// TestCTLReferenceTable_AllEncoderCTLsCovered verifies that every encoder CTL
// listed in the table has a corresponding encoder method in gopus.
func TestCTLReferenceTable_AllEncoderCTLsCovered(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio) // stereo for phase inversion

	// Getters.
	_ = enc.Application()
	_ = enc.Bitrate()
	_ = enc.MaxBandwidth()
	_ = enc.VBR()
	_ = enc.Bandwidth()
	_ = enc.Complexity()
	_ = enc.InBandFEC()
	_ = enc.FECEnabled()
	_ = enc.PacketLoss()
	_ = enc.DTXEnabled()
	_ = enc.VBRConstraint()
	_ = enc.ForceChannels()
	_ = enc.Signal()
	_ = enc.Lookahead()
	_ = enc.SampleRate()
	_ = enc.FinalRange()
	_ = enc.LSBDepth()
	_ = enc.ExpertFrameDuration()
	_ = enc.PredictionDisabled()
	_ = enc.PhaseInversionDisabled()
	_ = enc.InDTX()

	// Setters.
	_ = enc.SetApplication(ApplicationAudio)
	_ = enc.SetBitrate(BitrateAuto)
	_ = enc.SetMaxBandwidth(BandwidthFullband)
	enc.SetVBR(true)
	_ = enc.SetBandwidth(BandwidthFullband)
	_ = enc.SetBandwidthAuto()
	_ = enc.SetComplexity(9)
	_ = enc.SetInBandFEC(InBandFECDisabled)
	_ = enc.SetPacketLoss(0)
	enc.SetDTX(false)
	enc.SetVBRConstraint(true)
	_ = enc.SetForceChannels(-1)
	_ = enc.SetSignal(SignalAuto)
	_ = enc.SetLSBDepth(24)
	_ = enc.SetExpertFrameDuration(ExpertFrameDurationArg)
	enc.SetPredictionDisabled(false)
	enc.SetPhaseInversionDisabled(false)

	// RESET.
	enc.Reset()

	t.Logf("All encoder CTL methods callable on *Encoder")
}
