package gopus

import (
	"errors"
	"testing"

	encodercore "github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/extsupport"
)

// TestMultistreamEncoder_Creation tests encoder creation for various channel counts.
func TestMultistreamEncoder_Creation(t *testing.T) {
	// Test NewMultistreamEncoderDefault for channels 1-8
	for channels := 1; channels <= 8; channels++ {
		t.Run(string(rune('0'+channels))+"ch", func(t *testing.T) {
			enc := mustNewDefaultMultistreamEncoder(t, 48000, channels, ApplicationAudio)

			if enc.Channels() != channels {
				t.Errorf("Channels() = %d, want %d", enc.Channels(), channels)
			}
			if enc.SampleRate() != 48000 {
				t.Errorf("SampleRate() = %d, want 48000", enc.SampleRate())
			}

			// Verify stream counts based on channel configuration
			streams := enc.Streams()
			coupled := enc.CoupledStreams()
			t.Logf("%d channels: %d streams, %d coupled", channels, streams, coupled)

			// Sanity check: coupled <= streams
			if coupled > streams {
				t.Errorf("CoupledStreams(%d) > Streams(%d)", coupled, streams)
			}
		})
	}

	// Test invalid sample rates
	_, err := NewMultistreamEncoderDefault(44100, 6, ApplicationAudio)
	if err != ErrInvalidSampleRate {
		t.Errorf("Invalid sample rate: got error %v, want ErrInvalidSampleRate", err)
	}

	// Test invalid channels (0)
	_, err = NewMultistreamEncoderDefault(48000, 0, ApplicationAudio)
	if !errors.Is(err, ErrInvalidChannels) {
		t.Errorf("Zero channels: got error %v, want ErrInvalidChannels", err)
	}

	// Test invalid channels (>8 for default)
	_, err = NewMultistreamEncoderDefault(48000, 9, ApplicationAudio)
	if !errors.Is(err, ErrInvalidChannels) {
		t.Errorf("9 channels: got error %v, want ErrInvalidChannels", err)
	}

	_, err = NewMultistreamEncoderDefault(48000, 6, Application(99))
	if err != ErrInvalidApplication {
		t.Errorf("invalid application: got error %v, want ErrInvalidApplication", err)
	}
}

// TestMultistreamEncoder_Controls tests encoder control methods.
func TestMultistreamEncoder_Controls(t *testing.T) {
	channels := 6
	enc := mustNewDefaultMultistreamEncoder(t, 48000, channels, ApplicationAudio)
	var err error

	// Test application control
	if got := enc.Application(); got != ApplicationAudio {
		t.Fatalf("Application()=%v want=%v", got, ApplicationAudio)
	}
	if err := enc.SetApplication(ApplicationVoIP); err != nil {
		t.Fatalf("SetApplication(ApplicationVoIP) error: %v", err)
	}
	if got := enc.Application(); got != ApplicationVoIP {
		t.Fatalf("Application()=%v want=%v after SetApplication", got, ApplicationVoIP)
	}
	if err := enc.SetApplication(Application(-1)); err != ErrInvalidApplication {
		t.Fatalf("SetApplication(invalid) error=%v want=%v", err, ErrInvalidApplication)
	}
	if err := enc.SetApplication(ApplicationRestrictedSilk); err != ErrInvalidApplication {
		t.Fatalf("SetApplication(restricted silk) error=%v want=%v", err, ErrInvalidApplication)
	}
	if err := enc.SetApplication(ApplicationRestrictedCelt); err != ErrInvalidApplication {
		t.Fatalf("SetApplication(restricted celt) error=%v want=%v", err, ErrInvalidApplication)
	}

	// Test SetBitrate
	err = enc.SetBitrate(256000)
	if err != nil {
		t.Errorf("SetBitrate(256000) error: %v", err)
	}
	if enc.Bitrate() != 256000 {
		t.Errorf("Bitrate() = %d, want 256000", enc.Bitrate())
	}
	if err := enc.SetBitrate(750000 * channels); err != nil {
		t.Errorf("SetBitrate(max total) error: %v", err)
	}
	if got := enc.Bitrate(); got != 750000*channels {
		t.Errorf("Bitrate() = %d, want %d after max total bitrate", got, 750000*channels)
	}
	if err := enc.SetBitrate(750000*channels + 1); err != nil {
		t.Errorf("SetBitrate(above max total) error: %v", err)
	}
	if got := enc.Bitrate(); got != 750000*channels {
		t.Errorf("Bitrate() = %d, want %d after above-max clamp", got, 750000*channels)
	}
	if err := enc.SetBitrate(1); err != nil {
		t.Errorf("SetBitrate(1) error: %v", err)
	}
	if got := enc.Bitrate(); got != 500*channels {
		t.Errorf("Bitrate() = %d, want %d after minimum clamp", got, 500*channels)
	}
	if err := enc.SetBitrate(0); err != ErrInvalidBitrate {
		t.Errorf("SetBitrate(0) error = %v, want %v", err, ErrInvalidBitrate)
	}
	if err := enc.SetBitrate(BitrateAuto); err != nil {
		t.Errorf("SetBitrate(BitrateAuto) error: %v", err)
	}
	if got := enc.Bitrate(); got != BitrateAuto {
		t.Errorf("Bitrate() = %d, want BitrateAuto", got)
	}
	if err := enc.SetBitrate(BitrateMax); err != nil {
		t.Errorf("SetBitrate(BitrateMax) error: %v", err)
	}
	if got := enc.Bitrate(); got != BitrateMax {
		t.Errorf("Bitrate() = %d, want BitrateMax", got)
	}

	// Test SetComplexity
	if got := enc.Complexity(); got != 9 {
		t.Errorf("Complexity() default = %d, want 9", got)
	}
	err = enc.SetComplexity(8)
	if err != nil {
		t.Errorf("SetComplexity(8) error: %v", err)
	}
	if enc.Complexity() != 8 {
		t.Errorf("Complexity() = %d, want 8", enc.Complexity())
	}

	// Test invalid complexity
	err = enc.SetComplexity(11)
	if err != ErrInvalidComplexity {
		t.Errorf("SetComplexity(11) error = %v, want ErrInvalidComplexity", err)
	}

	// Test bitrate mode controls
	if got := enc.BitrateMode(); got != BitrateModeCVBR {
		t.Errorf("BitrateMode() = %v, want %v by default", got, BitrateModeCVBR)
	}
	if !enc.VBR() {
		t.Error("VBR() should be true by default")
	}
	if !enc.VBRConstraint() {
		t.Error("VBRConstraint() should be true by default")
	}
	if err := enc.SetBitrateMode(BitrateModeCBR); err != nil {
		t.Errorf("SetBitrateMode(BitrateModeCBR) error: %v", err)
	}
	if got := enc.BitrateMode(); got != BitrateModeCBR {
		t.Errorf("BitrateMode() = %v, want %v", got, BitrateModeCBR)
	}
	enc.SetVBR(true)
	if !enc.VBR() {
		t.Error("VBR() should be true after SetVBR(true)")
	}
	if got := enc.BitrateMode(); got != BitrateModeCVBR {
		t.Errorf("BitrateMode() = %v, want %v after SetVBR(true) with retained constraint", got, BitrateModeCVBR)
	}
	enc.SetVBRConstraint(true)
	if !enc.VBRConstraint() {
		t.Error("VBRConstraint() should be true after SetVBRConstraint(true)")
	}
	enc.SetVBRConstraint(false)
	if got := enc.BitrateMode(); got != BitrateModeVBR {
		t.Errorf("BitrateMode() = %v, want %v after SetVBRConstraint(false)", got, BitrateModeVBR)
	}
	enc.SetVBR(false)
	if got := enc.BitrateMode(); got != BitrateModeCBR {
		t.Errorf("BitrateMode() = %v, want %v after SetVBR(false)", got, BitrateModeCBR)
	}
	enc.SetVBR(true)
	if got := enc.BitrateMode(); got != BitrateModeVBR {
		t.Errorf("BitrateMode() = %v, want %v after re-enabling VBR with constraint=false", got, BitrateModeVBR)
	}
	if err := enc.SetBitrateMode(BitrateMode(99)); err != ErrInvalidBitrateMode {
		t.Errorf("SetBitrateMode(invalid) error = %v, want %v", err, ErrInvalidBitrateMode)
	}

	// Test SetFEC
	enc.SetFEC(true)
	if !enc.FECEnabled() {
		t.Error("FEC should be enabled")
	}
	enc.SetFEC(false)
	if enc.FECEnabled() {
		t.Error("FEC should be disabled")
	}
	if err := enc.SetInBandFEC(InBandFECMusicSafe); err != nil {
		t.Fatalf("SetInBandFEC(%d) error: %v", InBandFECMusicSafe, err)
	}
	if got := enc.InBandFEC(); got != InBandFECMusicSafe {
		t.Fatalf("InBandFEC()=%d want %d", got, InBandFECMusicSafe)
	}
	if !enc.FECEnabled() {
		t.Fatal("FECEnabled()=false want true")
	}
	if err := enc.SetInBandFEC(3); err != ErrInvalidFECConfig {
		t.Fatalf("SetInBandFEC(3) error=%v want %v", err, ErrInvalidFECConfig)
	}

	// Test SetDTX
	enc.SetDTX(true)
	if !enc.DTXEnabled() {
		t.Error("DTX should be enabled")
	}
	enc.SetDTX(false)
	if enc.DTXEnabled() {
		t.Error("DTX should be disabled")
	}

	// Test SetPacketLoss
	err = enc.SetPacketLoss(15)
	if err != nil {
		t.Errorf("SetPacketLoss(15) error: %v", err)
	}
	if enc.PacketLoss() != 15 {
		t.Errorf("PacketLoss() = %d, want 15", enc.PacketLoss())
	}
	err = enc.SetPacketLoss(101)
	if err != ErrInvalidPacketLoss {
		t.Errorf("SetPacketLoss(101) error = %v, want ErrInvalidPacketLoss", err)
	}

	// Test bandwidth control. Bandwidth() mirrors libopus OPUS_GET_BANDWIDTH
	// (st->bandwidth): it stays at the FULLBAND init default until an encode
	// decides the bandwidth and does not echo the SET request.
	if err := enc.SetBandwidth(BandwidthWideband); err != nil {
		t.Errorf("SetBandwidth(BandwidthWideband) error: %v", err)
	}
	if got := enc.Bandwidth(); got != BandwidthFullband {
		t.Errorf("Bandwidth() = %v, want %v (FULLBAND init default before encode)", got, BandwidthFullband)
	}
	if err := enc.SetBandwidth(Bandwidth(255)); err != ErrInvalidBandwidth {
		t.Errorf("SetBandwidth(invalid) error = %v, want %v", err, ErrInvalidBandwidth)
	}
	if err := enc.SetBandwidthAuto(); err != nil {
		t.Errorf("SetBandwidthAuto error: %v", err)
	}
	if got := enc.Bandwidth(); got != BandwidthFullband {
		t.Errorf("Bandwidth() after SetBandwidthAuto = %v, want %v", got, BandwidthFullband)
	}
	if err := enc.SetMaxBandwidth(BandwidthWideband); err != nil {
		t.Errorf("SetMaxBandwidth(BandwidthWideband) error: %v", err)
	}
	if got := enc.MaxBandwidth(); got != BandwidthWideband {
		t.Errorf("MaxBandwidth() = %v, want %v", got, BandwidthWideband)
	}
	if err := enc.SetMaxBandwidth(Bandwidth(255)); err != ErrInvalidBandwidth {
		t.Errorf("SetMaxBandwidth(invalid) error = %v, want %v", err, ErrInvalidBandwidth)
	}

	// Test frame size control
	if got := enc.FrameSize(); got != 960 {
		t.Errorf("FrameSize() = %d, want 960", got)
	}
	for _, size := range []int{120, 240, 480, 960, 1920, 2880, 3840, 4800, 5760} {
		if err := enc.SetFrameSize(size); err != nil {
			t.Errorf("SetFrameSize(%d) error: %v", size, err)
		}
		if got := enc.FrameSize(); got != size {
			t.Errorf("FrameSize() = %d, want %d", got, size)
		}
	}
	if err := enc.SetFrameSize(111); err != ErrInvalidFrameSize {
		t.Errorf("SetFrameSize(invalid) error = %v, want %v", err, ErrInvalidFrameSize)
	}
	if err := enc.SetFrameSize(960); err != nil {
		t.Errorf("SetFrameSize(960) error: %v", err)
	}

	if got := enc.ExpertFrameDuration(); got != ExpertFrameDurationArg {
		t.Errorf("ExpertFrameDuration() = %v, want %v", got, ExpertFrameDurationArg)
	}
	if err := enc.SetExpertFrameDuration(ExpertFrameDuration120Ms); err != nil {
		t.Errorf("SetExpertFrameDuration(120ms) error: %v", err)
	}
	if got := enc.FrameSize(); got != 960 {
		t.Errorf("FrameSize() after 120ms = %d, want 960", got)
	}
	if got := enc.ExpertFrameDuration(); got != ExpertFrameDuration120Ms {
		t.Errorf("ExpertFrameDuration() after 120ms = %v, want %v", got, ExpertFrameDuration120Ms)
	}
	if err := enc.SetExpertFrameDuration(ExpertFrameDurationArg); err != nil {
		t.Errorf("SetExpertFrameDuration(arg) error: %v", err)
	}
	if err := enc.SetExpertFrameDuration(ExpertFrameDuration(0)); err != nil {
		t.Errorf("SetExpertFrameDuration(invalid) error = %v, want nil", err)
	}
	if got := enc.ExpertFrameDuration(); got != ExpertFrameDuration(0) {
		t.Errorf("ExpertFrameDuration() after invalid value = %v, want 0", got)
	}
	if n, err := enc.Encode(generateSurroundTestSignal(48000, enc.FrameSize(), enc.Channels()), make([]byte, 4000)); n != 0 || err != ErrInvalidFrameSize {
		t.Errorf("Encode with invalid expert duration = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
	}
	if err := enc.SetExpertFrameDuration(ExpertFrameDurationArg); err != nil {
		t.Errorf("SetExpertFrameDuration(arg restore) error: %v", err)
	}

	// Test force channels control
	for _, ch := range []int{1, -1} {
		if err := enc.SetForceChannels(ch); err != nil {
			t.Errorf("SetForceChannels(%d) error: %v", ch, err)
		}
		if got := enc.ForceChannels(); got != ch {
			t.Errorf("ForceChannels() = %d, want %d", got, ch)
		}
	}
	if err := enc.SetForceChannels(2); err != ErrInvalidForceChannels {
		t.Errorf("SetForceChannels(2) on layout with mono streams error = %v, want %v", err, ErrInvalidForceChannels)
	}
	if got := enc.ForceChannels(); got != -1 {
		t.Errorf("ForceChannels() after rejected stereo force = %d, want -1", got)
	}
	if err := enc.SetForceChannels(0); err != ErrInvalidForceChannels {
		t.Errorf("SetForceChannels(0) error = %v, want %v", err, ErrInvalidForceChannels)
	}

	// Test prediction and phase inversion controls
	enc.SetPredictionDisabled(true)
	if !enc.PredictionDisabled() {
		t.Error("PredictionDisabled() should be true after SetPredictionDisabled(true)")
	}
	enc.SetPredictionDisabled(false)
	if enc.PredictionDisabled() {
		t.Error("PredictionDisabled() should be false after SetPredictionDisabled(false)")
	}

	enc.SetPhaseInversionDisabled(true)
	if !enc.PhaseInversionDisabled() {
		t.Error("PhaseInversionDisabled() should be true after SetPhaseInversionDisabled(true)")
	}
	enc.SetPhaseInversionDisabled(false)
	if enc.PhaseInversionDisabled() {
		t.Error("PhaseInversionDisabled() should be false after SetPhaseInversionDisabled(false)")
	}

	// Test signal hint control parity (libopus OPUS_SET_SIGNAL semantics).
	if err := enc.SetSignal(SignalVoice); err != nil {
		t.Errorf("SetSignal(SignalVoice) error: %v", err)
	}
	if got := enc.Signal(); got != SignalVoice {
		t.Errorf("Signal() = %v, want %v", got, SignalVoice)
	}
	if err := enc.SetSignal(SignalMusic); err != nil {
		t.Errorf("SetSignal(SignalMusic) error: %v", err)
	}
	if got := enc.Signal(); got != SignalMusic {
		t.Errorf("Signal() = %v, want %v", got, SignalMusic)
	}
	if err := enc.SetSignal(Signal(9999)); err != ErrInvalidSignal {
		t.Errorf("SetSignal(invalid) error = %v, want %v", err, ErrInvalidSignal)
	}

	// Encode a frame after setting controls to verify no errors
	frameSize := enc.FrameSize()
	pcm := generateSurroundTestSignal(48000, frameSize, channels)
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Errorf("Encode after controls error: %v", err)
	}
	if len(packet) == 0 {
		t.Error("Encode after controls produced empty packet")
	}
	if enc.FinalRange() != enc.GetFinalRange() {
		t.Errorf("FinalRange() = %d, want %d", enc.FinalRange(), enc.GetFinalRange())
	}

	t.Logf("Controls verified: app=%v bitrate=%d complexity=%d mode=%v FEC=%v DTX=%v",
		enc.Application(), enc.Bitrate(), enc.Complexity(), enc.BitrateMode(), enc.FECEnabled(), enc.DTXEnabled())
}

func TestMultistreamEncoder_SetMode(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 6, ApplicationAudio)

	if got := enc.Mode(); got != EncoderModeAuto {
		t.Fatalf("Mode() default=%v want=%v", got, EncoderModeAuto)
	}
	for _, mode := range []EncoderMode{
		EncoderModeAuto,
		EncoderModeSILK,
		EncoderModeHybrid,
		EncoderModeCELT,
	} {
		if err := enc.SetMode(mode); err != nil {
			t.Fatalf("SetMode(%v) error: %v", mode, err)
		}
		if got := enc.Mode(); got != mode {
			t.Fatalf("Mode()=%v want=%v", got, mode)
		}
		if got := enc.enc.Mode(); got != mode {
			t.Fatalf("core Mode()=%v want=%v", got, mode)
		}
	}

	if err := enc.SetMode(EncoderMode(99)); err != ErrInvalidArgument {
		t.Fatalf("SetMode(invalid) error=%v want=%v", err, ErrInvalidArgument)
	}
	if got := enc.Mode(); got != EncoderModeCELT {
		t.Fatalf("Mode() after invalid=%v want=%v", got, EncoderModeCELT)
	}
	enc.Reset()
	if got := enc.Mode(); got != EncoderModeCELT {
		t.Fatalf("Mode() after Reset=%v want=%v", got, EncoderModeCELT)
	}
}

func TestMultistreamEncoder_ExpertFrameDurationSelectsEncodeFrame(t *testing.T) {
	enc, err := NewMultistreamEncoder(48000, 2, 1, 1, []byte{0, 1}, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoder error: %v", err)
	}
	if err := enc.SetFrameSize(5760); err != nil {
		t.Fatalf("SetFrameSize(5760) error: %v", err)
	}
	if err := enc.SetExpertFrameDuration(ExpertFrameDuration20Ms); err != nil {
		t.Fatalf("SetExpertFrameDuration(20ms) error: %v", err)
	}

	data := make([]byte, 4000)
	n, err := enc.Encode(generateSurroundTestSignal(48000, 5760, 2), data)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if n == 0 {
		t.Fatalf("Encode returned empty packet")
	}
	if got := ParseTOC(data[0]).FrameSize; got != 960 {
		t.Fatalf("TOC frame size = %d, want 960 from fixed 20ms duration", got)
	}
}

func TestMultistreamEncoderForceChannelsStereoAllowedWhenAllStreamsCoupled(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 2, ApplicationAudio)

	if err := enc.SetForceChannels(2); err != nil {
		t.Fatalf("SetForceChannels(2) on all-coupled layout error: %v", err)
	}
	if got := enc.ForceChannels(); got != 2 {
		t.Fatalf("ForceChannels() = %d, want 2", got)
	}
}

func TestMultistreamEncoder_EncodeInt24(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 6, ApplicationAudio)

	pcm := generateSurroundTestSignalInt24(48000, enc.FrameSize(), enc.Channels())
	data := make([]byte, 4000*enc.Streams())

	n, err := enc.EncodeInt24(pcm, data)
	if err != nil {
		t.Fatalf("EncodeInt24 error: %v", err)
	}
	if n == 0 {
		t.Fatal("EncodeInt24 returned 0 bytes")
	}

	packet, err := enc.EncodeInt24Slice(pcm)
	if err != nil {
		t.Fatalf("EncodeInt24Slice error: %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("EncodeInt24Slice returned empty packet")
	}
}

func TestMultistreamEncoder_EncodeInt24InvalidFrameSize(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 6, ApplicationAudio)

	short := make([]int32, enc.FrameSize()*enc.Channels()-1)
	data := make([]byte, 4000*enc.Streams())
	if _, err := enc.EncodeInt24(short, data); err != ErrInvalidFrameSize {
		t.Fatalf("EncodeInt24(short) error=%v want=%v", err, ErrInvalidFrameSize)
	}
	if _, err := enc.EncodeInt24Slice(short); err != ErrInvalidFrameSize {
		t.Fatalf("EncodeInt24Slice(short) error=%v want=%v", err, ErrInvalidFrameSize)
	}
}

func TestMultistreamEncoder_CVBRPacketEnvelope(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 6, ApplicationAudio)

	if got := enc.BitrateMode(); got != BitrateModeCVBR {
		t.Fatalf("BitrateMode() = %v, want %v", got, BitrateModeCVBR)
	}

	frameSize := 960
	pcm := generateSurroundTestSignal(48000, frameSize, 6)
	data := make([]byte, 4000*enc.Streams())

	for _, bitrate := range []int{128000, 256000, 384000} {
		if err := enc.SetBitrateMode(BitrateModeCVBR); err != nil {
			t.Fatalf("SetBitrateMode(CVBR) error: %v", err)
		}
		if err := enc.SetBitrate(bitrate); err != nil {
			t.Fatalf("SetBitrate(%d) error: %v", bitrate, err)
		}
		enc.Reset()

		maxPacket := 0
		for i := 0; i < 10; i++ {
			n, err := enc.Encode(pcm, data)
			if err != nil {
				t.Fatalf("Encode bitrate=%d frame=%d error: %v", bitrate, i, err)
			}
			if n > maxPacket {
				maxPacket = n
			}
		}
		if maxPacket > 1275 {
			t.Fatalf("bitrate=%d max packet=%d exceeds 1275-byte envelope", bitrate, maxPacket)
		}
	}
}

// TestMultistreamEncoder_SetApplicationPreservesControls verifies application
// updates do not clobber other encoder CTLs.
func TestMultistreamEncoder_SetApplicationPreservesControls(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 6, ApplicationAudio)

	const wantBitrate = 210000
	const wantComplexity = 3

	if err := enc.SetBitrate(wantBitrate); err != nil {
		t.Fatalf("SetBitrate(%d) error: %v", wantBitrate, err)
	}
	if err := enc.SetComplexity(wantComplexity); err != nil {
		t.Fatalf("SetComplexity(%d) error: %v", wantComplexity, err)
	}
	if err := enc.SetSignal(SignalMusic); err != nil {
		t.Fatalf("SetSignal(SignalMusic) error: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthSuperwideband); err != nil {
		t.Fatalf("SetBandwidth(SWB) error: %v", err)
	}
	if err := enc.SetForceChannels(1); err != nil {
		t.Fatalf("SetForceChannels(1) error: %v", err)
	}
	if err := enc.SetMode(EncoderModeCELT); err != nil {
		t.Fatalf("SetMode(CELT) error: %v", err)
	}

	if err := enc.SetApplication(ApplicationVoIP); err != nil {
		t.Fatalf("SetApplication(ApplicationVoIP) error: %v", err)
	}

	if got := enc.Bitrate(); got != wantBitrate {
		t.Fatalf("Bitrate() after SetApplication = %d, want %d", got, wantBitrate)
	}
	if got := enc.Complexity(); got != wantComplexity {
		t.Fatalf("Complexity() after SetApplication = %d, want %d", got, wantComplexity)
	}
	if got := enc.Signal(); got != SignalMusic {
		t.Fatalf("Signal() after SetApplication = %v, want %v", got, SignalMusic)
	}
	// Bandwidth() reports st->bandwidth (FULLBAND init default before encode);
	// SetApplication preserves the user bandwidth request but does not change
	// the decided value reported here.
	if got := enc.Bandwidth(); got != BandwidthFullband {
		t.Fatalf("Bandwidth() after SetApplication = %v, want %v (FULLBAND init default before encode)", got, BandwidthFullband)
	}
	if got := enc.ForceChannels(); got != 1 {
		t.Fatalf("ForceChannels() after SetApplication = %d, want 1", got)
	}
	if got := enc.Mode(); got != EncoderModeCELT {
		t.Fatalf("Mode() after SetApplication = %v, want %v", got, EncoderModeCELT)
	}
}

func TestMultistreamEncoder_SetApplicationUpdatesApplicationPolicy(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 6, ApplicationAudio)

	if got := enc.enc.Mode(); got != encodercore.ModeAuto {
		t.Fatalf("initial Mode() = %v, want %v", got, encodercore.ModeAuto)
	}
	if enc.enc.LowDelay() {
		t.Fatalf("initial LowDelay() = true, want false")
	}
	if got := enc.Bandwidth(); got != BandwidthFullband {
		t.Fatalf("initial Bandwidth() = %v, want %v", got, BandwidthFullband)
	}

	if err := enc.SetApplication(ApplicationVoIP); err != nil {
		t.Fatalf("SetApplication(ApplicationVoIP) error: %v", err)
	}
	if got := enc.enc.Mode(); got != encodercore.ModeAuto {
		t.Fatalf("Mode() after VoIP = %v, want %v", got, encodercore.ModeAuto)
	}
	if enc.enc.LowDelay() {
		t.Fatalf("LowDelay() after VoIP = true, want false")
	}
	if got := enc.Bandwidth(); got != BandwidthFullband {
		t.Fatalf("Bandwidth() after VoIP = %v, want preserved %v", got, BandwidthFullband)
	}

	if err := enc.SetApplication(ApplicationLowDelay); err != nil {
		t.Fatalf("SetApplication(ApplicationLowDelay) error: %v", err)
	}
	if got := enc.enc.Mode(); got != encodercore.ModeAuto {
		t.Fatalf("Mode() after LowDelay = %v, want preserved %v", got, encodercore.ModeAuto)
	}
	if !enc.enc.LowDelay() {
		t.Fatalf("LowDelay() after LowDelay app = false, want true")
	}
	if got := enc.Bandwidth(); got != BandwidthFullband {
		t.Fatalf("Bandwidth() after LowDelay = %v, want %v", got, BandwidthFullband)
	}
}

func TestMultistreamEncoder_SetApplicationFromLowDelayRestoresDefaultModeUnlessForced(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 6, ApplicationLowDelay)
	if err := enc.SetApplication(ApplicationAudio); err != nil {
		t.Fatalf("SetApplication(ApplicationAudio) error: %v", err)
	}
	if got := enc.Mode(); got != EncoderModeAuto {
		t.Fatalf("Mode()=%v want %v", got, EncoderModeAuto)
	}
	if enc.enc.LowDelay() {
		t.Fatal("LowDelay()=true after switching to audio, want false")
	}

	forced := mustNewDefaultMultistreamEncoder(t, 48000, 6, ApplicationLowDelay)
	if err := forced.SetMode(EncoderModeCELT); err != nil {
		t.Fatalf("SetMode(CELT) error: %v", err)
	}
	if err := forced.SetApplication(ApplicationAudio); err != nil {
		t.Fatalf("SetApplication(ApplicationAudio) with forced mode error: %v", err)
	}
	if got := forced.Mode(); got != EncoderModeCELT {
		t.Fatalf("forced Mode()=%v want %v", got, EncoderModeCELT)
	}
}

func TestMultistreamEncoder_SetApplicationAfterEncodeRejected(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 6, ApplicationAudio)

	pcm := generateSurroundTestSignal(48000, 960, 6)
	packet := make([]byte, 4000*enc.Streams())
	assertApplicationLockAfterEncode(
		t,
		enc.Application,
		enc.SetApplication,
		enc.Reset,
		func() error {
			_, err := enc.Encode(pcm, packet)
			return err
		},
		ApplicationVoIP,
		ApplicationVoIP,
	)
}

func TestMultistreamEncoder_RestrictedApplications(t *testing.T) {
	for _, tt := range restrictedApplicationTestCases() {
		t.Run(tt.name, func(t *testing.T) {
			enc := mustNewDefaultMultistreamEncoder(t, 48000, 6, tt.application)

			if got := enc.Application(); got != tt.application {
				t.Fatalf("Application()=%v want=%v", got, tt.application)
			}
			if got := enc.enc.Mode(); got != tt.wantMode {
				t.Fatalf("Mode()=%v want=%v", got, tt.wantMode)
			}
			if got := enc.enc.LowDelay(); got != tt.wantLowDelay {
				t.Fatalf("LowDelay()=%v want=%v", got, tt.wantLowDelay)
			}
			if got := enc.Bandwidth(); got != tt.wantBandwidth {
				t.Fatalf("Bandwidth()=%v want=%v", got, tt.wantBandwidth)
			}
			if got := enc.Lookahead(); got != tt.wantLookahead {
				t.Fatalf("Lookahead()=%d want=%d", got, tt.wantLookahead)
			}

			if err := enc.SetApplication(tt.application); err != ErrInvalidApplication {
				t.Fatalf("SetApplication(same restricted) error=%v want=%v", err, ErrInvalidApplication)
			}
			if err := enc.SetApplication(ApplicationAudio); err != ErrInvalidApplication {
				t.Fatalf("SetApplication(change restricted) error=%v want=%v", err, ErrInvalidApplication)
			}
			if tt.application == ApplicationRestrictedSilk {
				if err := enc.SetFrameSize(240); err != ErrInvalidFrameSize {
					t.Fatalf("SetFrameSize(240) error=%v want=%v", err, ErrInvalidFrameSize)
				}
				if err := enc.SetFrameSize(480); err != nil {
					t.Fatalf("SetFrameSize(480) error: %v", err)
				}
			}
		})
	}
}

func TestMultistreamEncoder_OptionalExtensionControls(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 2, ApplicationAudio)

	assertOptionalEncoderControls(t, enc)
	dred, ok := any(enc).(extraDREDControl)
	if extsupport.DRED {
		if !ok {
			t.Fatal("gopus_dred build does not expose DRED control")
		}
		assertWorkingDREDControl(t, dred)
	} else if extsupport.DREDRuntime {
		if !ok {
			t.Fatal("DRED runtime build does not expose DRED control")
		}
		assertWorkingDREDControl(t, dred)
	} else if ok {
		t.Fatal("non-DRED-runtime build unexpectedly exposes DRED control")
	}
	qext, ok := any(enc).(qextEncoderControl)
	if extsupport.QEXT {
		if !ok {
			t.Fatal("QEXT build does not expose multistream encoder QEXT control")
		}
		assertSupportedQEXTControl(t, qext)
	} else if ok {
		t.Fatal("non-QEXT build unexpectedly exposes multistream encoder QEXT control")
	}
}

// TestMultistreamEncoder_Reset tests encoder reset functionality.
func TestMultistreamEncoder_Reset(t *testing.T) {
	channels := 6
	sampleRate := 48000
	frameSize := 960

	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)

	// Encode a few frames
	for i := 0; i < 3; i++ {
		pcm := generateSurroundTestSignal(sampleRate, frameSize, channels)
		_, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Pre-reset encode %d error: %v", i, err)
		}
	}

	// Reset
	enc.Reset()

	// Encode more frames after reset
	for i := 0; i < 3; i++ {
		pcm := generateSurroundTestSignal(sampleRate, frameSize, channels)
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Post-reset encode %d error: %v", i, err)
		}
		if len(packet) == 0 {
			t.Errorf("Post-reset encode %d produced empty packet", i)
		}
	}

	t.Log("Encoder reset verified: no crashes, encoding continues normally")
}

// TestMultistreamEncoder_ExplicitConstructor tests explicit encoder constructor with custom mapping.
func TestMultistreamEncoder_ExplicitConstructor(t *testing.T) {
	// Test creating encoder with explicit parameters (5.1 surround)
	sampleRate := 48000
	channels := 6
	streams := 4
	coupledStreams := 2
	mapping := []byte{0, 4, 1, 2, 3, 5} // Standard 5.1 mapping

	enc, err := NewMultistreamEncoder(sampleRate, channels, streams, coupledStreams, mapping, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoder error: %v", err)
	}

	if enc.Channels() != channels {
		t.Errorf("Channels() = %d, want %d", enc.Channels(), channels)
	}
	if enc.Streams() != streams {
		t.Errorf("Streams() = %d, want %d", enc.Streams(), streams)
	}
	if enc.CoupledStreams() != coupledStreams {
		t.Errorf("CoupledStreams() = %d, want %d", enc.CoupledStreams(), coupledStreams)
	}

	// Verify encoding works
	frameSize := 960
	pcm := generateSurroundTestSignal(sampleRate, frameSize, channels)
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if len(packet) == 0 {
		t.Error("Explicit constructor encoder produced empty packet")
	}

	t.Logf("Explicit constructor: %d channels, %d streams, %d coupled, packet=%d bytes",
		channels, streams, coupledStreams, len(packet))
}

func TestMultistreamEncoder_Lookahead(t *testing.T) {
	for _, tc := range lookaheadTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			enc := mustNewDefaultMultistreamEncoder(t, tc.sampleRate, 6, tc.application)
			if got := enc.Lookahead(); got != tc.want {
				t.Fatalf("Lookahead() = %d, want %d", got, tc.want)
			}
		})
	}

	t.Run("set_application_updates_lookahead_before_encode", func(t *testing.T) {
		enc := mustNewDefaultMultistreamEncoder(t, 48000, 6, ApplicationAudio)
		assertLookaheadUpdatesBeforeEncode(t, enc.Lookahead, enc.SetApplication)
	})
}
