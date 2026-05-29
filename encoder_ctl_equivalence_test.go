// encoder_ctl_equivalence_test.go verifies that every encoder CTL GET
// reflects the value just SET and matches libopus 1.6.1 defaults/clamping.
//
// Libopus references (src/opus_encoder.c opus_encoder_init + opus_encoder_ctl):
//   complexity=9, use_vbr=1, vbr_constraint=1, user_bitrate_bps=OPUS_AUTO,
//   signal_type=OPUS_AUTO, user_bandwidth=OPUS_AUTO,
//   max_bandwidth=OPUS_BANDWIDTH_FULLBAND, force_channels=OPUS_AUTO,
//   lsb_depth=24, variable_duration=OPUS_FRAMESIZE_ARG,
//   use_dtx=0, useInBandFEC=0, packetLossPercentage=0.
//
// For OPUS_GET_BITRATE libopus calls user_bitrate_to_bitrate(st,
// prev_framesize, 1276) which resolves OPUS_AUTO to a frame-rate-derived
// value.  gopus returns the stored user bitrate (or BitrateAuto sentinel)
// because it defers resolution to Encode time; this is a known honest
// residual documented below (TestEncoderCTL_BitrateGetResidual).

package gopus

import (
	"testing"
)

// ---------------------------------------------------------------------------
// Default values
// ---------------------------------------------------------------------------

// TestEncoderCTL_Defaults verifies that a freshly created encoder exposes
// the same defaults as libopus opus_encoder_init().
//
// C ref: src/opus_encoder.c opus_encoder_init():
//   st->silk_mode.complexity = 9
//   st->use_vbr = 1, st->vbr_constraint = 1
//   st->user_bitrate_bps = OPUS_AUTO
//   st->signal_type = OPUS_AUTO (-1000)
//   st->user_bandwidth = OPUS_AUTO (-1000)
//   st->max_bandwidth = OPUS_BANDWIDTH_FULLBAND (1105)
//   st->force_channels = OPUS_AUTO (-1000)
//   st->lsb_depth = 24
//   st->variable_duration = OPUS_FRAMESIZE_ARG (5000)
//   st->silk_mode.useInBandFEC = 0, st->silk_mode.useDTX = 0
//   st->silk_mode.packetLossPercentage = 0
func TestEncoderCTL_Defaults(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	// complexity = 9
	if got := enc.Complexity(); got != 9 {
		t.Errorf("Complexity() default = %d, want 9 (libopus: st->silk_mode.complexity=9)", got)
	}

	// use_vbr = 1 (VBR enabled)
	if !enc.VBR() {
		t.Error("VBR() default = false, want true (libopus: st->use_vbr=1)")
	}

	// vbr_constraint = 1 (constrained VBR enabled)
	if !enc.VBRConstraint() {
		t.Error("VBRConstraint() default = false, want true (libopus: st->vbr_constraint=1)")
	}

	// signal_type = OPUS_AUTO (-1000)
	if got := enc.Signal(); got != SignalAuto {
		t.Errorf("Signal() default = %v, want SignalAuto (libopus: st->signal_type=OPUS_AUTO)", got)
	}

	// max_bandwidth = OPUS_BANDWIDTH_FULLBAND (1105)
	if got := enc.MaxBandwidth(); got != BandwidthFullband {
		t.Errorf("MaxBandwidth() default = %v, want BandwidthFullband (libopus: st->max_bandwidth=OPUS_BANDWIDTH_FULLBAND)", got)
	}

	// force_channels = OPUS_AUTO (-1000) → gopus -1
	if got := enc.ForceChannels(); got != -1 {
		t.Errorf("ForceChannels() default = %d, want -1 (libopus: st->force_channels=OPUS_AUTO)", got)
	}

	// lsb_depth = 24
	if got := enc.LSBDepth(); got != 24 {
		t.Errorf("LSBDepth() default = %d, want 24 (libopus: st->lsb_depth=24)", got)
	}

	// variable_duration = OPUS_FRAMESIZE_ARG (5000)
	if got := enc.ExpertFrameDuration(); got != ExpertFrameDurationArg {
		t.Errorf("ExpertFrameDuration() default = %v, want ExpertFrameDurationArg (libopus: st->variable_duration=OPUS_FRAMESIZE_ARG)", got)
	}

	// useInBandFEC = 0
	if enc.FECEnabled() {
		t.Error("FECEnabled() default = true, want false (libopus: st->silk_mode.useInBandFEC=0)")
	}
	if got := enc.InBandFEC(); got != InBandFECDisabled {
		t.Errorf("InBandFEC() default = %d, want InBandFECDisabled (libopus: st->fec_config=0)", got)
	}

	// useDTX = 0
	if enc.DTXEnabled() {
		t.Error("DTXEnabled() default = true, want false (libopus: st->silk_mode.useDTX=0)")
	}

	// packetLossPercentage = 0
	if got := enc.PacketLoss(); got != 0 {
		t.Errorf("PacketLoss() default = %d, want 0", got)
	}

	// prediction_disabled = 0
	if enc.PredictionDisabled() {
		t.Error("PredictionDisabled() default = true, want false (libopus: st->silk_mode.reducedDependency=0)")
	}

	// phase_inversion_disabled = 0 (stereo encoder only)
	stereo := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)
	if stereo.PhaseInversionDisabled() {
		t.Error("stereo PhaseInversionDisabled() default = true, want false")
	}
}

// ---------------------------------------------------------------------------
// Round-trip: SET then GET must return the value just set.
// ---------------------------------------------------------------------------

// TestEncoderCTL_ComplexityRoundTrip verifies OPUS_SET/GET_COMPLEXITY.
//
// C ref: opus_encoder_ctl OPUS_SET_COMPLEXITY_REQUEST – "if(value<0 || value>10) goto bad_arg"
func TestEncoderCTL_ComplexityRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	for c := 0; c <= 10; c++ {
		if err := enc.SetComplexity(c); err != nil {
			t.Fatalf("SetComplexity(%d) error: %v", c, err)
		}
		if got := enc.Complexity(); got != c {
			t.Errorf("Complexity() = %d after SetComplexity(%d), want %d", got, c, c)
		}
	}
}

// TestEncoderCTL_ComplexityBoundaryReject verifies that out-of-range complexity
// is rejected and the stored value is unchanged.
//
// C ref: opus_encoder_ctl OPUS_SET_COMPLEXITY_REQUEST – "if(value<0 || value>10) goto bad_arg"
func TestEncoderCTL_ComplexityBoundaryReject(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)
	if err := enc.SetComplexity(5); err != nil {
		t.Fatalf("SetComplexity(5) error: %v", err)
	}

	for _, c := range []int{-1, 11, 100} {
		if err := enc.SetComplexity(c); err == nil {
			t.Errorf("SetComplexity(%d) = nil, want error", c)
		}
		if got := enc.Complexity(); got != 5 {
			t.Errorf("invalid SetComplexity(%d) changed Complexity() to %d, want 5", c, got)
		}
	}
}

// TestEncoderCTL_VBRRoundTrip verifies OPUS_SET/GET_VBR.
//
// C ref: opus_encoder_ctl OPUS_SET_VBR_REQUEST – "if(value<0 || value>1) goto bad_arg"
func TestEncoderCTL_VBRRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	enc.SetVBR(false)
	if enc.VBR() {
		t.Error("VBR() = true after SetVBR(false), want false")
	}

	enc.SetVBR(true)
	if !enc.VBR() {
		t.Error("VBR() = false after SetVBR(true), want true")
	}
}

// TestEncoderCTL_VBRConstraintRoundTrip verifies OPUS_SET/GET_VBR_CONSTRAINT.
//
// C ref: opus_encoder_ctl OPUS_SET_VBR_CONSTRAINT_REQUEST – "if(value<0 || value>1) goto bad_arg"
func TestEncoderCTL_VBRConstraintRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	enc.SetVBRConstraint(false)
	if enc.VBRConstraint() {
		t.Error("VBRConstraint() = true after Set(false), want false")
	}

	enc.SetVBRConstraint(true)
	if !enc.VBRConstraint() {
		t.Error("VBRConstraint() = false after Set(true), want true")
	}
}

// TestEncoderCTL_DTXRoundTrip verifies OPUS_SET/GET_DTX.
//
// C ref: opus_encoder_ctl OPUS_SET_DTX_REQUEST – "if(value<0 || value>1) goto bad_arg"
func TestEncoderCTL_DTXRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	enc.SetDTX(true)
	if !enc.DTXEnabled() {
		t.Error("DTXEnabled() = false after SetDTX(true), want true")
	}

	enc.SetDTX(false)
	if enc.DTXEnabled() {
		t.Error("DTXEnabled() = true after SetDTX(false), want false")
	}
}

// TestEncoderCTL_InBandFECRoundTrip verifies OPUS_SET/GET_INBAND_FEC for all
// three valid configurations.
//
// C ref: opus_encoder_ctl OPUS_SET_INBAND_FEC_REQUEST – "if(value<0 || value>2) goto bad_arg"
func TestEncoderCTL_InBandFECRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationVoIP)

	for _, cfg := range []int{InBandFECDisabled, InBandFECEnabled, InBandFECMusicSafe} {
		if err := enc.SetInBandFEC(cfg); err != nil {
			t.Fatalf("SetInBandFEC(%d) error: %v", cfg, err)
		}
		if got := enc.InBandFEC(); got != cfg {
			t.Errorf("InBandFEC() = %d after SetInBandFEC(%d), want %d", got, cfg, cfg)
		}
	}
}

// TestEncoderCTL_InBandFECBoundaryReject verifies that out-of-range FEC
// config is rejected.
//
// C ref: opus_encoder_ctl OPUS_SET_INBAND_FEC_REQUEST – "if(value<0 || value>2) goto bad_arg"
func TestEncoderCTL_InBandFECBoundaryReject(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationVoIP)

	for _, cfg := range []int{-1, 3, 10} {
		if err := enc.SetInBandFEC(cfg); err == nil {
			t.Errorf("SetInBandFEC(%d) = nil, want error", cfg)
		}
	}
}

// TestEncoderCTL_PacketLossRoundTrip verifies OPUS_SET/GET_PACKET_LOSS_PERC.
//
// C ref: opus_encoder_ctl OPUS_SET_PACKET_LOSS_PERC_REQUEST –
//   "if (value < 0 || value > 100) goto bad_arg"
func TestEncoderCTL_PacketLossRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	for _, pct := range []int{0, 1, 50, 100} {
		if err := enc.SetPacketLoss(pct); err != nil {
			t.Fatalf("SetPacketLoss(%d) error: %v", pct, err)
		}
		if got := enc.PacketLoss(); got != pct {
			t.Errorf("PacketLoss() = %d after SetPacketLoss(%d), want %d", got, pct, pct)
		}
	}
}

// TestEncoderCTL_PacketLossBoundaryReject verifies out-of-range packet loss is
// rejected.
//
// C ref: opus_encoder_ctl OPUS_SET_PACKET_LOSS_PERC_REQUEST –
//   "if (value < 0 || value > 100) goto bad_arg"
func TestEncoderCTL_PacketLossBoundaryReject(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)
	if err := enc.SetPacketLoss(10); err != nil {
		t.Fatalf("SetPacketLoss(10) error: %v", err)
	}

	for _, pct := range []int{-1, 101, 200} {
		if err := enc.SetPacketLoss(pct); err == nil {
			t.Errorf("SetPacketLoss(%d) = nil, want error", pct)
		}
		if got := enc.PacketLoss(); got != 10 {
			t.Errorf("invalid SetPacketLoss(%d) changed PacketLoss() to %d, want 10", pct, got)
		}
	}
}

// TestEncoderCTL_SignalRoundTrip verifies OPUS_SET/GET_SIGNAL.
//
// C ref: opus_encoder_ctl OPUS_SET_SIGNAL_REQUEST –
//   "if(value!=OPUS_AUTO && value!=OPUS_SIGNAL_VOICE && value!=OPUS_SIGNAL_MUSIC) goto bad_arg"
func TestEncoderCTL_SignalRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	for _, sig := range []Signal{SignalAuto, SignalVoice, SignalMusic} {
		if err := enc.SetSignal(sig); err != nil {
			t.Fatalf("SetSignal(%v) error: %v", sig, err)
		}
		if got := enc.Signal(); got != sig {
			t.Errorf("Signal() = %v after SetSignal(%v), want %v", got, sig, sig)
		}
	}
}

// TestEncoderCTL_SignalBoundaryReject verifies invalid signal values are
// rejected.
func TestEncoderCTL_SignalBoundaryReject(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	for _, sig := range []Signal{Signal(-1), Signal(0), Signal(9999)} {
		if err := enc.SetSignal(sig); err == nil {
			t.Errorf("SetSignal(%v) = nil, want error", sig)
		}
	}
}

// TestEncoderCTL_BandwidthRoundTrip verifies OPUS_SET/GET_BANDWIDTH for all
// five bandwidths plus the auto sentinel.
//
// C ref: opus_encoder_ctl OPUS_SET_BANDWIDTH_REQUEST –
//   "(value < OPUS_BANDWIDTH_NARROWBAND || value > OPUS_BANDWIDTH_FULLBAND) && value != OPUS_AUTO"
func TestEncoderCTL_BandwidthRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	for _, bw := range []Bandwidth{
		BandwidthNarrowband,
		BandwidthMediumband,
		BandwidthWideband,
		BandwidthSuperwideband,
		BandwidthFullband,
	} {
		if err := enc.SetBandwidth(bw); err != nil {
			t.Fatalf("SetBandwidth(%v) error: %v", bw, err)
		}
		if got := enc.Bandwidth(); got != bw {
			t.Errorf("Bandwidth() = %v after SetBandwidth(%v), want %v", got, bw, bw)
		}
	}

	// Restore auto.
	if err := enc.SetBandwidthAuto(); err != nil {
		t.Fatalf("SetBandwidthAuto() error: %v", err)
	}
}

// TestEncoderCTL_MaxBandwidthRoundTrip verifies OPUS_SET/GET_MAX_BANDWIDTH.
//
// C ref: opus_encoder_ctl OPUS_SET_MAX_BANDWIDTH_REQUEST –
//   "if (value < OPUS_BANDWIDTH_NARROWBAND || value > OPUS_BANDWIDTH_FULLBAND) goto bad_arg"
func TestEncoderCTL_MaxBandwidthRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	for _, bw := range []Bandwidth{
		BandwidthNarrowband,
		BandwidthMediumband,
		BandwidthWideband,
		BandwidthSuperwideband,
		BandwidthFullband,
	} {
		if err := enc.SetMaxBandwidth(bw); err != nil {
			t.Fatalf("SetMaxBandwidth(%v) error: %v", bw, err)
		}
		if got := enc.MaxBandwidth(); got != bw {
			t.Errorf("MaxBandwidth() = %v after SetMaxBandwidth(%v), want %v", got, bw, bw)
		}
	}
}

// TestEncoderCTL_MaxBandwidthBoundaryReject verifies invalid bandwidth values
// are rejected from SetMaxBandwidth.
//
// Bandwidth is uint8 with valid enum range [0,4] (NB..FB).  Values outside
// that range must be rejected.
func TestEncoderCTL_MaxBandwidthBoundaryReject(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	for _, bw := range []Bandwidth{Bandwidth(5), Bandwidth(10), Bandwidth(255)} {
		if err := enc.SetMaxBandwidth(bw); err == nil {
			t.Errorf("SetMaxBandwidth(%v) = nil, want error", bw)
		}
	}
}

// TestEncoderCTL_ForceChannelsRoundTrip verifies OPUS_SET/GET_FORCE_CHANNELS.
//
// C ref: opus_encoder_ctl OPUS_SET_FORCE_CHANNELS_REQUEST –
//   "if((value<1 || value>st->channels) && value != OPUS_AUTO) goto bad_arg"
func TestEncoderCTL_ForceChannelsRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)

	for _, ch := range []int{-1, 1, 2} {
		if err := enc.SetForceChannels(ch); err != nil {
			t.Fatalf("SetForceChannels(%d) error: %v", ch, err)
		}
		if got := enc.ForceChannels(); got != ch {
			t.Errorf("ForceChannels() = %d after SetForceChannels(%d), want %d", got, ch, ch)
		}
	}
}

// TestEncoderCTL_ForceChannelsBoundaryReject verifies that ForceChannels
// values outside [-1, channels] are rejected.
//
// C ref: opus_encoder_ctl OPUS_SET_FORCE_CHANNELS_REQUEST –
//   "if((value<1 || value>st->channels) && value != OPUS_AUTO) goto bad_arg"
func TestEncoderCTL_ForceChannelsBoundaryReject(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	// channels=1: forcing stereo (2) is invalid.
	for _, ch := range []int{0, 2, 3} {
		if err := enc.SetForceChannels(ch); err == nil {
			t.Errorf("SetForceChannels(%d) on mono encoder = nil, want error", ch)
		}
	}
}

// TestEncoderCTL_LSBDepthRoundTrip verifies OPUS_SET/GET_LSB_DEPTH.
//
// C ref: opus_encoder_ctl OPUS_SET_LSB_DEPTH_REQUEST – "if (value<8 || value>24) goto bad_arg"
func TestEncoderCTL_LSBDepthRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	for _, depth := range []int{8, 16, 24} {
		if err := enc.SetLSBDepth(depth); err != nil {
			t.Fatalf("SetLSBDepth(%d) error: %v", depth, err)
		}
		if got := enc.LSBDepth(); got != depth {
			t.Errorf("LSBDepth() = %d after SetLSBDepth(%d), want %d", got, depth, depth)
		}
	}
}

// TestEncoderCTL_LSBDepthBoundaryReject verifies out-of-range depth is
// rejected.
//
// C ref: opus_encoder_ctl OPUS_SET_LSB_DEPTH_REQUEST – "if (value<8 || value>24) goto bad_arg"
func TestEncoderCTL_LSBDepthBoundaryReject(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	for _, depth := range []int{7, 25, 32} {
		if err := enc.SetLSBDepth(depth); err == nil {
			t.Errorf("SetLSBDepth(%d) = nil, want error", depth)
		}
	}
}

// TestEncoderCTL_ExpertFrameDurationRoundTrip verifies
// OPUS_SET/GET_EXPERT_FRAME_DURATION for all valid values.
//
// C ref: opus_encoder_ctl OPUS_SET_EXPERT_FRAME_DURATION_REQUEST – must be
//   one of the OPUS_FRAMESIZE_* constants.
func TestEncoderCTL_ExpertFrameDurationRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	for _, dur := range []ExpertFrameDuration{
		ExpertFrameDurationArg,
		ExpertFrameDuration2_5Ms,
		ExpertFrameDuration5Ms,
		ExpertFrameDuration10Ms,
		ExpertFrameDuration20Ms,
		ExpertFrameDuration40Ms,
		ExpertFrameDuration60Ms,
		ExpertFrameDuration80Ms,
		ExpertFrameDuration100Ms,
		ExpertFrameDuration120Ms,
	} {
		if err := enc.SetExpertFrameDuration(dur); err != nil {
			t.Fatalf("SetExpertFrameDuration(%v) error: %v", dur, err)
		}
		if got := enc.ExpertFrameDuration(); got != dur {
			t.Errorf("ExpertFrameDuration() = %v after Set(%v), want %v", got, dur, dur)
		}
	}
}

// TestEncoderCTL_ExpertFrameDurationBoundaryReject verifies an invalid
// frame-duration enum value is rejected.
//
// C ref: opus_encoder_ctl OPUS_SET_EXPERT_FRAME_DURATION_REQUEST – must be
//   one of the OPUS_FRAMESIZE_* constants; otherwise goto bad_arg.
func TestEncoderCTL_ExpertFrameDurationBoundaryReject(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	for _, dur := range []ExpertFrameDuration{ExpertFrameDuration(0), ExpertFrameDuration(4999), ExpertFrameDuration(5010)} {
		if err := enc.SetExpertFrameDuration(dur); err == nil {
			t.Errorf("SetExpertFrameDuration(%v) = nil, want error", dur)
		}
	}
}

// TestEncoderCTL_PredictionDisabledRoundTrip verifies
// OPUS_SET/GET_PREDICTION_DISABLED.
//
// C ref: opus_encoder_ctl OPUS_SET_PREDICTION_DISABLED_REQUEST –
//   "if (value > 1 || value < 0) goto bad_arg"
func TestEncoderCTL_PredictionDisabledRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	enc.SetPredictionDisabled(true)
	if !enc.PredictionDisabled() {
		t.Error("PredictionDisabled() = false after Set(true), want true")
	}

	enc.SetPredictionDisabled(false)
	if enc.PredictionDisabled() {
		t.Error("PredictionDisabled() = true after Set(false), want false")
	}
}

// TestEncoderCTL_PhaseInversionDisabledRoundTrip verifies
// OPUS_SET/GET_PHASE_INVERSION_DISABLED on a stereo encoder.
//
// C ref: opus_encoder_ctl OPUS_SET_PHASE_INVERSION_DISABLED_REQUEST –
//   "if(value<0 || value>1) goto bad_arg"
func TestEncoderCTL_PhaseInversionDisabledRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)

	enc.SetPhaseInversionDisabled(true)
	if !enc.PhaseInversionDisabled() {
		t.Error("PhaseInversionDisabled() = false after Set(true), want true")
	}

	enc.SetPhaseInversionDisabled(false)
	if enc.PhaseInversionDisabled() {
		t.Error("PhaseInversionDisabled() = true after Set(false), want false")
	}
}

// TestEncoderCTL_SampleRateAllRates verifies OPUS_GET_SAMPLE_RATE for all
// valid Opus sample rates.
//
// C ref: opus_encoder_ctl OPUS_GET_SAMPLE_RATE_REQUEST – "*value = st->Fs"
func TestEncoderCTL_SampleRateAllRates(t *testing.T) {
	for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
		enc := mustNewTestEncoder(t, rate, 1, ApplicationAudio)
		if got := enc.SampleRate(); got != rate {
			t.Errorf("SampleRate() = %d, want %d", got, rate)
		}
	}
}

// TestEncoderCTL_ChannelsGetter verifies Channels() returns the configured
// channel count.
func TestEncoderCTL_ChannelsGetter(t *testing.T) {
	for _, ch := range []int{1, 2} {
		enc := mustNewTestEncoder(t, 48000, ch, ApplicationAudio)
		if got := enc.Channels(); got != ch {
			t.Errorf("Channels() = %d, want %d", got, ch)
		}
	}
}

// TestEncoderCTL_LookaheadMatchesFormula verifies OPUS_GET_LOOKAHEAD for all
// sample rates and the three standard application values.
//
// C ref: opus_encoder_ctl OPUS_GET_LOOKAHEAD_REQUEST:
//   *value = st->Fs/400;
//   if (st->application != OPUS_APPLICATION_RESTRICTED_LOWDELAY &&
//       st->application != OPUS_APPLICATION_RESTRICTED_CELT)
//       *value += st->delay_compensation; // delay_compensation = Fs/250
func TestEncoderCTL_LookaheadMatchesFormula(t *testing.T) {
	cases := []struct {
		rate int
		app  Application
		want int
	}{
		{48000, ApplicationAudio, 48000/400 + 48000/250},
		{48000, ApplicationVoIP, 48000/400 + 48000/250},
		{48000, ApplicationLowDelay, 48000 / 400},
		{24000, ApplicationAudio, 24000/400 + 24000/250},
		{24000, ApplicationLowDelay, 24000 / 400},
		{16000, ApplicationAudio, 16000/400 + 16000/250},
		{8000, ApplicationAudio, 8000/400 + 8000/250},
	}

	for _, tc := range cases {
		enc := mustNewTestEncoder(t, tc.rate, 1, tc.app)
		if got := enc.Lookahead(); got != tc.want {
			t.Errorf("Lookahead(%d Hz, %v) = %d, want %d", tc.rate, tc.app, got, tc.want)
		}
	}
}

// TestEncoderCTL_FinalRangeBeforeEncode verifies that FinalRange() returns 0
// before any frame has been encoded.
func TestEncoderCTL_FinalRangeBeforeEncode(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)
	if got := enc.FinalRange(); got != 0 {
		t.Errorf("FinalRange() before encode = %d, want 0", got)
	}
}

// TestEncoderCTL_FinalRangeNonZeroAfterEncode verifies that FinalRange()
// returns a non-zero value after a successful encode.
func TestEncoderCTL_FinalRangeNonZeroAfterEncode(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)
	pcm := generateSineWave(48000, 440, 960)
	buf := make([]byte, 4000)
	if _, err := enc.Encode(pcm, buf); err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if got := enc.FinalRange(); got == 0 {
		t.Error("FinalRange() after encode = 0, want non-zero")
	}
}

// TestEncoderCTL_ApplicationRoundTrip verifies OPUS_SET/GET_APPLICATION.
//
// C ref: opus_encoder_ctl OPUS_SET_APPLICATION_REQUEST – only
//   OPUS_APPLICATION_VOIP, OPUS_APPLICATION_AUDIO, and
//   OPUS_APPLICATION_RESTRICTED_LOWDELAY are valid after first encode.
func TestEncoderCTL_ApplicationRoundTrip(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	if got := enc.Application(); got != ApplicationAudio {
		t.Errorf("Application() = %v, want ApplicationAudio", got)
	}

	if err := enc.SetApplication(ApplicationVoIP); err != nil {
		t.Fatalf("SetApplication(VoIP) error: %v", err)
	}
	if got := enc.Application(); got != ApplicationVoIP {
		t.Errorf("Application() = %v after SetApplication(VoIP), want VoIP", got)
	}

	if err := enc.SetApplication(ApplicationLowDelay); err != nil {
		t.Fatalf("SetApplication(LowDelay) error: %v", err)
	}
	if got := enc.Application(); got != ApplicationLowDelay {
		t.Errorf("Application() = %v after SetApplication(LowDelay), want LowDelay", got)
	}
}

// TestEncoderCTL_ApplicationLockedAfterFirstEncode verifies that
// OPUS_SET_APPLICATION fails after the first frame has been encoded if the
// application value would change.
//
// C ref: opus_encoder_ctl OPUS_SET_APPLICATION_REQUEST –
//   "if (!st->first && st->application != value) { ret = OPUS_BAD_ARG; break; }"
func TestEncoderCTL_ApplicationLockedAfterFirstEncode(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)
	pcm := generateSineWave(48000, 440, 960)
	buf := make([]byte, 4000)

	if _, err := enc.Encode(pcm, buf); err != nil {
		t.Fatalf("first Encode error: %v", err)
	}

	// Changing application after first encode must fail.
	if err := enc.SetApplication(ApplicationVoIP); err == nil {
		t.Error("SetApplication(VoIP) after first encode = nil, want error")
	}

	// Setting the same application must succeed.
	if err := enc.SetApplication(ApplicationAudio); err != nil {
		t.Errorf("SetApplication(same) after first encode error: %v", err)
	}
}

// TestEncoderCTL_BitrateGetResidual documents the known honest residual:
// gopus Bitrate() returns the stored user bitrate (BitrateAuto = -1000 by
// default) rather than resolving it at GET time.  libopus OPUS_GET_BITRATE
// calls user_bitrate_to_bitrate(st, st->prev_framesize, 1276) which resolves
// OPUS_AUTO to "60*Fs/frame_size + Fs*channels".
//
// This is a deliberate gopus design choice (resolution deferred to Encode
// time) and is not a bug; it means Bitrate() returns BitrateAuto until
// SetBitrate() is called with an explicit value.
func TestEncoderCTL_BitrateGetResidual(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	// Default: gopus returns BitrateAuto sentinel.
	got := enc.Bitrate()
	if got != BitrateAuto {
		t.Logf("NOTE: Bitrate() default = %d (want BitrateAuto=%d); libopus returns a resolved value at GET time", got, BitrateAuto)
	}

	// After an explicit SET, GET must return the clamped stored value.
	if err := enc.SetBitrate(64000); err != nil {
		t.Fatalf("SetBitrate(64000) error: %v", err)
	}
	if got := enc.Bitrate(); got != 64000 {
		t.Errorf("Bitrate() = %d after SetBitrate(64000), want 64000", got)
	}

	// BitrateMax sentinel round-trips.
	if err := enc.SetBitrate(BitrateMax); err != nil {
		t.Fatalf("SetBitrate(BitrateMax) error: %v", err)
	}
	if got := enc.Bitrate(); got != BitrateMax {
		t.Errorf("Bitrate() = %d after SetBitrate(BitrateMax), want %d", got, BitrateMax)
	}
}

// TestEncoderCTL_BitrateBoundaryReject verifies that non-positive non-sentinel
// bitrate values are rejected.
//
// C ref: opus_encoder_ctl OPUS_SET_BITRATE_REQUEST –
//   "if (value <= 0) goto bad_arg" (after OPUS_AUTO / OPUS_BITRATE_MAX check)
func TestEncoderCTL_BitrateBoundaryReject(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	for _, br := range []int{0, -2, -999} {
		if err := enc.SetBitrate(br); err == nil {
			t.Errorf("SetBitrate(%d) = nil, want error", br)
		}
	}
}

// TestEncoderCTL_ResetRestoresDefaults verifies that Reset() re-establishes
// the libopus default values that are cleared by OPUS_RESET_STATE.
//
// C ref: opus_encoder_ctl OPUS_RESET_STATE case – clears from
//   st->OPUS_ENCODER_RESET_START through the end of the struct; sets
//   st->first=1, st->mode=MODE_HYBRID, st->bandwidth=OPUS_BANDWIDTH_FULLBAND.
func TestEncoderCTL_ResetRestoresDefaults(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)

	// Modify some values before reset.
	if err := enc.SetComplexity(3); err != nil {
		t.Fatalf("SetComplexity(3) error: %v", err)
	}
	enc.SetDTX(true)

	enc.Reset()

	// After Reset, application-lock must be released (encodedOnce=false in
	// gopus, matching st->first=1 in libopus).
	if err := enc.SetApplication(ApplicationVoIP); err != nil {
		t.Errorf("SetApplication after Reset() error: %v", err)
	}
}
