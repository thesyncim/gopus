package gopus

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

type controlParityStep struct {
	frameSize              int
	channels               int
	application            int
	ret                    int
	lookahead              int
	finalRange             uint32
	bitrate                int
	complexity             int
	vbr                    int
	vbrConstraint          int
	fec                    int
	packetLoss             int
	dtx                    int
	inDTX                  int
	forceChannels          int
	signal                 int
	bandwidth              int
	maxBandwidth           int
	expertFrameDuration    int
	lsbDepth               int
	predictionDisabled     int
	phaseInversionDisabled int
	packet                 []byte
}

type controlParityOptions struct {
	exactPacketBytes bool
	onlyControlState bool
}

var (
	libopusControlHelperOnce sync.Once
	libopusControlHelperPath string
	libopusControlHelperErr  error
)

func TestLibopusControlTransitionParity(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T) []controlParityStep
		opts controlParityOptions
	}{
		{name: "default_applications", run: runGopusDefaultApplicationsDriftProbe},
		{name: "applications", run: runGopusApplicationsParity},
		{name: "audio_controls", run: runGopusAudioControlParity},
		{name: "bitrate_mode_transitions", run: runGopusBitrateModeTransitionsParity},
		{name: "lowdelay_controls", run: runGopusLowDelayControlParity},
		{name: "expert_durations", run: runGopusExpertDurationsParity},
		{name: "bandwidth_signal_controls", run: runGopusBandwidthSignalControlsParity, opts: controlParityOptions{onlyControlState: true}},
		{name: "fec_dtx_lsb_controls", run: runGopusFECDTXLSBControlsParity},
		{name: "force_channels", run: runGopusForceChannelsParity},
		{name: "prediction_phase_controls", run: runGopusPredictionPhaseControlsParity},
		{name: "reset_preserves_controls", run: runGopusResetPreservesControlsParity},
		{name: "dtx_silence_exact", run: runGopusDTXSilenceExactParity, opts: controlParityOptions{exactPacketBytes: true}},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			want, err := probeLibopusControlScenario(tc.name)
			if err != nil {
				t.Skipf("libopus control helper unavailable: %v", err)
			}
			got := tc.run(t)
			compareControlParitySteps(t, got, want, tc.opts)
		})
	}
}

// Tracks known libopus-backed encoder drifts that are too meaningful to hide
// behind relaxed transition checks.
func TestLibopusMeaningfulControlDrifts(t *testing.T) {
	t.Run("bandwidth_signal_packet_shape", func(t *testing.T) {
		want, err := probeLibopusControlScenario("bandwidth_signal_controls")
		if err != nil {
			t.Skipf("libopus control helper unavailable: %v", err)
		}
		got := runGopusBandwidthSignalControlsParity(t)
		assertControlDriftSet(t, collectPacketMetadataDrifts(t, got, want), []string{
			"step_1/packet_bandwidth",
			"step_2/packet_bandwidth",
			"step_3/packet_bandwidth",
			"step_3/packet_mode",
		})
	})
}

func runGopusDefaultApplicationsDriftProbe(t *testing.T) []controlParityStep {
	t.Helper()

	applications := []Application{
		ApplicationVoIP,
		ApplicationAudio,
		ApplicationLowDelay,
		ApplicationRestrictedSilk,
		ApplicationRestrictedCelt,
	}
	var out []controlParityStep
	for i, application := range applications {
		enc := mustNewTestEncoder(t, 48000, 1, application)
		packet := make([]byte, maxPacketBytesPerStream)
		out = append(out, encodeControlParityStep(t, enc, packet, i))
	}
	return out
}

func runGopusApplicationsParity(t *testing.T) []controlParityStep {
	t.Helper()

	applications := []Application{
		ApplicationVoIP,
		ApplicationAudio,
		ApplicationLowDelay,
		ApplicationRestrictedSilk,
		ApplicationRestrictedCelt,
	}
	var out []controlParityStep
	for i, application := range applications {
		enc := mustNewTestEncoder(t, 48000, 1, application)
		mustSetControl(t, enc.SetBitrate(64000), "SetBitrate")
		switch application {
		case ApplicationVoIP, ApplicationRestrictedSilk:
			mustSetControl(t, enc.SetBandwidth(BandwidthWideband), "SetBandwidth")
			mustSetControl(t, enc.SetMaxBandwidth(BandwidthWideband), "SetMaxBandwidth")
		default:
			mustSetControl(t, enc.SetBandwidth(BandwidthFullband), "SetBandwidth")
			mustSetControl(t, enc.SetMaxBandwidth(BandwidthFullband), "SetMaxBandwidth")
		}
		packet := make([]byte, maxPacketBytesPerStream)
		out = append(out, encodeControlParityStep(t, enc, packet, i))
	}
	return out
}

func runGopusAudioControlParity(t *testing.T) []controlParityStep {
	t.Helper()

	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)
	packet := make([]byte, maxPacketBytesPerStream)
	var out []controlParityStep

	mustSetControl(t, enc.SetBitrate(64000), "SetBitrate")
	mustSetControl(t, enc.SetComplexity(3), "SetComplexity")
	enc.SetVBR(true)
	enc.SetVBRConstraint(false)
	mustSetControl(t, enc.SetBandwidth(BandwidthFullband), "SetBandwidth")
	mustSetControl(t, enc.SetMaxBandwidth(BandwidthFullband), "SetMaxBandwidth")
	mustSetControl(t, enc.SetSignal(SignalMusic), "SetSignal")
	mustSetControl(t, enc.SetExpertFrameDuration(ExpertFrameDuration20Ms), "SetExpertFrameDuration")
	mustSetControl(t, enc.SetLSBDepth(24), "SetLSBDepth")
	enc.SetFEC(false)
	mustSetControl(t, enc.SetPacketLoss(0), "SetPacketLoss")
	enc.SetDTX(false)
	enc.SetPredictionDisabled(false)
	enc.SetPhaseInversionDisabled(false)
	out = append(out, encodeControlParityStep(t, enc, packet, 0))

	mustSetControl(t, enc.SetBitrate(24000), "SetBitrate")
	mustSetControl(t, enc.SetComplexity(8), "SetComplexity")
	mustSetControl(t, enc.SetBandwidth(BandwidthWideband), "SetBandwidth")
	mustSetControl(t, enc.SetMaxBandwidth(BandwidthWideband), "SetMaxBandwidth")
	mustSetControl(t, enc.SetSignal(SignalVoice), "SetSignal")
	enc.SetFEC(true)
	mustSetControl(t, enc.SetPacketLoss(20), "SetPacketLoss")
	enc.SetDTX(true)
	mustSetControl(t, enc.SetLSBDepth(16), "SetLSBDepth")
	out = append(out, encodeControlParityStep(t, enc, packet, 1))

	return out
}

func runGopusBitrateModeTransitionsParity(t *testing.T) []controlParityStep {
	t.Helper()

	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)
	packet := make([]byte, maxPacketBytesPerStream)
	var out []controlParityStep

	mustSetControl(t, enc.SetBitrate(64000), "SetBitrate")
	mustSetControl(t, enc.SetComplexity(9), "SetComplexity")
	mustSetControl(t, enc.SetBandwidth(BandwidthFullband), "SetBandwidth")
	mustSetControl(t, enc.SetMaxBandwidth(BandwidthFullband), "SetMaxBandwidth")
	out = append(out, encodeControlParityStep(t, enc, packet, 0))

	enc.SetVBRConstraint(false)
	out = append(out, encodeControlParityStep(t, enc, packet, 1))

	enc.SetVBR(false)
	out = append(out, encodeControlParityStep(t, enc, packet, 2))

	enc.SetVBRConstraint(true)
	out = append(out, encodeControlParityStep(t, enc, packet, 3))

	enc.SetVBR(true)
	out = append(out, encodeControlParityStep(t, enc, packet, 4))

	mustSetControl(t, enc.SetBitrateMode(BitrateModeVBR), "SetBitrateMode(VBR)")
	out = append(out, encodeControlParityStep(t, enc, packet, 5))

	mustSetControl(t, enc.SetBitrateMode(BitrateModeCBR), "SetBitrateMode(CBR)")
	out = append(out, encodeControlParityStep(t, enc, packet, 6))

	mustSetControl(t, enc.SetBitrateMode(BitrateModeCVBR), "SetBitrateMode(CVBR)")
	out = append(out, encodeControlParityStep(t, enc, packet, 7))

	return out
}

func runGopusLowDelayControlParity(t *testing.T) []controlParityStep {
	t.Helper()

	enc := mustNewTestEncoder(t, 48000, 1, ApplicationLowDelay)
	packet := make([]byte, maxPacketBytesPerStream)
	var out []controlParityStep

	mustSetControl(t, enc.SetBitrate(64000), "SetBitrate")
	mustSetControl(t, enc.SetComplexity(5), "SetComplexity")
	mustSetControl(t, enc.SetExpertFrameDuration(ExpertFrameDuration2_5Ms), "SetExpertFrameDuration")
	mustSetControl(t, enc.SetMaxBandwidth(BandwidthFullband), "SetMaxBandwidth")
	mustSetControl(t, enc.SetSignal(SignalMusic), "SetSignal")
	out = append(out, encodeControlParityStep(t, enc, packet, 0))

	mustSetControl(t, enc.SetBitrate(96000), "SetBitrate")
	mustSetControl(t, enc.SetExpertFrameDuration(ExpertFrameDuration5Ms), "SetExpertFrameDuration")
	enc.SetPredictionDisabled(true)
	out = append(out, encodeControlParityStep(t, enc, packet, 1))

	mustSetControl(t, enc.SetExpertFrameDuration(ExpertFrameDuration20Ms), "SetExpertFrameDuration")
	enc.SetPredictionDisabled(false)
	out = append(out, encodeControlParityStep(t, enc, packet, 2))

	return out
}

func runGopusExpertDurationsParity(t *testing.T) []controlParityStep {
	t.Helper()

	enc := mustNewTestEncoder(t, 48000, 1, ApplicationLowDelay)
	packet := make([]byte, maxPacketBytesPerStream)
	var out []controlParityStep

	mustSetControl(t, enc.SetBitrate(96000), "SetBitrate")
	mustSetControl(t, enc.SetComplexity(7), "SetComplexity")
	mustSetControl(t, enc.SetSignal(SignalMusic), "SetSignal")
	mustSetControl(t, enc.SetMaxBandwidth(BandwidthFullband), "SetMaxBandwidth")

	durations := []ExpertFrameDuration{
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
	}
	frameSizes := []int{960, 120, 240, 480, 960, 1920, 2880, 3840, 4800, 5760}
	for i, duration := range durations {
		mustSetControl(t, enc.SetExpertFrameDuration(duration), "SetExpertFrameDuration")
		if duration == ExpertFrameDurationArg {
			mustSetControl(t, enc.SetFrameSize(frameSizes[i]), "SetFrameSize(ARG)")
		}
		out = append(out, encodeControlParityStep(t, enc, packet, i))
	}

	return out
}

func runGopusBandwidthSignalControlsParity(t *testing.T) []controlParityStep {
	t.Helper()

	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)
	packet := make([]byte, maxPacketBytesPerStream)
	var out []controlParityStep

	mustSetControl(t, enc.SetBitrate(48000), "SetBitrate")
	mustSetControl(t, enc.SetExpertFrameDuration(ExpertFrameDuration20Ms), "SetExpertFrameDuration")
	steps := []struct {
		bandwidth Bandwidth
		max       Bandwidth
		signal    Signal
		bitrate   int
	}{
		{bandwidth: BandwidthNarrowband, max: BandwidthNarrowband, signal: SignalAuto, bitrate: 16000},
		{bandwidth: BandwidthMediumband, max: BandwidthMediumband, signal: SignalVoice, bitrate: 20000},
		{bandwidth: BandwidthWideband, max: BandwidthWideband, signal: SignalVoice, bitrate: 28000},
		{bandwidth: BandwidthSuperwideband, max: BandwidthSuperwideband, signal: SignalMusic, bitrate: 64000},
		{bandwidth: BandwidthFullband, max: BandwidthFullband, signal: SignalMusic, bitrate: 96000},
	}
	for i, step := range steps {
		mustSetControl(t, enc.SetBitrate(step.bitrate), "SetBitrate")
		mustSetControl(t, enc.SetBandwidth(step.bandwidth), "SetBandwidth")
		mustSetControl(t, enc.SetMaxBandwidth(step.max), "SetMaxBandwidth")
		mustSetControl(t, enc.SetSignal(step.signal), "SetSignal")
		out = append(out, encodeControlParityStep(t, enc, packet, i))
	}

	return out
}

func runGopusFECDTXLSBControlsParity(t *testing.T) []controlParityStep {
	t.Helper()

	enc := mustNewTestEncoder(t, 48000, 1, ApplicationVoIP)
	packet := make([]byte, maxPacketBytesPerStream)
	var out []controlParityStep

	mustSetControl(t, enc.SetBitrate(24000), "SetBitrate")
	mustSetControl(t, enc.SetBandwidth(BandwidthWideband), "SetBandwidth")
	mustSetControl(t, enc.SetSignal(SignalVoice), "SetSignal")
	steps := []struct {
		fec        bool
		packetLoss int
		dtx        bool
		lsbDepth   int
	}{
		{fec: false, packetLoss: 0, dtx: false, lsbDepth: 24},
		{fec: true, packetLoss: 5, dtx: false, lsbDepth: 24},
		{fec: true, packetLoss: 20, dtx: true, lsbDepth: 16},
		{fec: false, packetLoss: 100, dtx: true, lsbDepth: 8},
	}
	for i, step := range steps {
		enc.SetFEC(step.fec)
		mustSetControl(t, enc.SetPacketLoss(step.packetLoss), "SetPacketLoss")
		enc.SetDTX(step.dtx)
		mustSetControl(t, enc.SetLSBDepth(step.lsbDepth), "SetLSBDepth")
		out = append(out, encodeControlParityStep(t, enc, packet, i))
	}

	return out
}

func runGopusForceChannelsParity(t *testing.T) []controlParityStep {
	t.Helper()

	enc := mustNewTestEncoder(t, 48000, 2, ApplicationLowDelay)
	packet := make([]byte, maxPacketBytesPerStream)
	var out []controlParityStep

	mustSetControl(t, enc.SetBitrate(128000), "SetBitrate")
	mustSetControl(t, enc.SetExpertFrameDuration(ExpertFrameDuration20Ms), "SetExpertFrameDuration")
	mustSetControl(t, enc.SetForceChannels(2), "SetForceChannels")
	out = append(out, encodeControlParityStep(t, enc, packet, 0))

	mustSetControl(t, enc.SetForceChannels(1), "SetForceChannels")
	out = append(out, encodeControlParityStep(t, enc, packet, 1))

	mustSetControl(t, enc.SetForceChannels(2), "SetForceChannels")
	out = append(out, encodeControlParityStep(t, enc, packet, 2))

	return out
}

func runGopusPredictionPhaseControlsParity(t *testing.T) []controlParityStep {
	t.Helper()

	enc := mustNewTestEncoder(t, 48000, 2, ApplicationLowDelay)
	packet := make([]byte, maxPacketBytesPerStream)
	var out []controlParityStep

	mustSetControl(t, enc.SetBitrate(128000), "SetBitrate")
	mustSetControl(t, enc.SetExpertFrameDuration(ExpertFrameDuration20Ms), "SetExpertFrameDuration")
	mustSetControl(t, enc.SetSignal(SignalMusic), "SetSignal")

	steps := []struct {
		predictionDisabled     bool
		phaseInversionDisabled bool
	}{
		{predictionDisabled: false, phaseInversionDisabled: false},
		{predictionDisabled: true, phaseInversionDisabled: false},
		{predictionDisabled: true, phaseInversionDisabled: true},
		{predictionDisabled: false, phaseInversionDisabled: true},
		{predictionDisabled: false, phaseInversionDisabled: false},
	}
	for i, step := range steps {
		enc.SetPredictionDisabled(step.predictionDisabled)
		enc.SetPhaseInversionDisabled(step.phaseInversionDisabled)
		out = append(out, encodeControlParityStep(t, enc, packet, i))
	}

	return out
}

func runGopusResetPreservesControlsParity(t *testing.T) []controlParityStep {
	t.Helper()

	enc := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)
	packet := make([]byte, maxPacketBytesPerStream)
	var out []controlParityStep

	mustSetControl(t, enc.SetBitrate(48000), "SetBitrate")
	mustSetControl(t, enc.SetComplexity(4), "SetComplexity")
	mustSetControl(t, enc.SetBitrateMode(BitrateModeVBR), "SetBitrateMode")
	mustSetControl(t, enc.SetBandwidth(BandwidthWideband), "SetBandwidth")
	mustSetControl(t, enc.SetMaxBandwidth(BandwidthWideband), "SetMaxBandwidth")
	mustSetControl(t, enc.SetSignal(SignalVoice), "SetSignal")
	mustSetControl(t, enc.SetLSBDepth(16), "SetLSBDepth")
	enc.SetFEC(true)
	mustSetControl(t, enc.SetPacketLoss(12), "SetPacketLoss")
	enc.SetDTX(true)
	out = append(out, encodeControlParityStep(t, enc, packet, 0))

	enc.Reset()
	out = append(out, encodeControlParityStep(t, enc, packet, 1))

	return out
}

func runGopusDTXSilenceExactParity(t *testing.T) []controlParityStep {
	t.Helper()

	enc := mustNewTestEncoder(t, 48000, 1, ApplicationVoIP)
	packet := make([]byte, maxPacketBytesPerStream)
	mustSetControl(t, enc.SetBitrate(16000), "SetBitrate")
	mustSetControl(t, enc.SetBandwidth(BandwidthWideband), "SetBandwidth")
	mustSetControl(t, enc.SetMaxBandwidth(BandwidthWideband), "SetMaxBandwidth")
	mustSetControl(t, enc.SetSignal(SignalVoice), "SetSignal")
	enc.SetDTX(true)

	for i := 0; i < 10; i++ {
		encodeControlParityStepWithSilence(t, enc, packet, i, true)
	}
	return []controlParityStep{encodeControlParityStepWithSilence(t, enc, packet, 10, true)}
}

func encodeControlParityStep(t *testing.T, enc *Encoder, packet []byte, frameIndex int) controlParityStep {
	t.Helper()

	return encodeControlParityStepWithSilence(t, enc, packet, frameIndex, false)
}

func encodeControlParityStepWithSilence(t *testing.T, enc *Encoder, packet []byte, frameIndex int, silence bool) controlParityStep {
	t.Helper()

	pcm := publicCodecPCM(48000, enc.FrameSize(), enc.Channels(), frameIndex, silence)
	n, err := enc.Encode(pcm, packet)
	if err != nil {
		t.Fatalf("Encode frame %d: %v", frameIndex, err)
	}
	if n <= 0 {
		t.Fatalf("Encode frame %d returned %d bytes", frameIndex, n)
	}

	return controlParityStep{
		frameSize:              enc.FrameSize(),
		channels:               enc.Channels(),
		application:            int(enc.Application()),
		ret:                    n,
		lookahead:              enc.Lookahead(),
		finalRange:             enc.FinalRange(),
		bitrate:                enc.Bitrate(),
		complexity:             enc.Complexity(),
		vbr:                    boolInt(enc.VBR()),
		vbrConstraint:          boolInt(enc.VBRConstraint()),
		fec:                    boolInt(enc.FECEnabled()),
		packetLoss:             enc.PacketLoss(),
		dtx:                    boolInt(enc.DTXEnabled()),
		inDTX:                  boolInt(enc.InDTX()),
		forceChannels:          enc.ForceChannels(),
		signal:                 int(enc.Signal()),
		bandwidth:              int(enc.Bandwidth()),
		maxBandwidth:           int(enc.MaxBandwidth()),
		expertFrameDuration:    int(enc.ExpertFrameDuration()),
		lsbDepth:               enc.LSBDepth(),
		predictionDisabled:     boolInt(enc.PredictionDisabled()),
		phaseInversionDisabled: boolInt(enc.PhaseInversionDisabled()),
		packet:                 append([]byte(nil), packet[:n]...),
	}
}

func compareControlParitySteps(t *testing.T, got, want []controlParityStep, opts controlParityOptions) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("step count=%d want %d", len(got), len(want))
	}
	for i := range got {
		t.Run(fmt.Sprintf("step_%d", i), func(t *testing.T) {
			requireControlStepPacketContract(t, "gopus", got[i])
			requireControlStepPacketContract(t, "libopus", want[i])
			if opts.exactPacketBytes {
				compareControlParityScalar(t, "ret", got[i].ret, want[i].ret)
				compareControlParityScalar(t, "finalRange", int(got[i].finalRange), int(want[i].finalRange))
			}
			compareControlParityScalar(t, "frameSize", got[i].frameSize, want[i].frameSize)
			compareControlParityScalar(t, "channels", got[i].channels, want[i].channels)
			compareControlParityScalar(t, "application", got[i].application, want[i].application)
			compareControlParityScalar(t, "lookahead", got[i].lookahead, want[i].lookahead)
			compareControlParityScalar(t, "bitrate", got[i].bitrate, want[i].bitrate)
			compareControlParityScalar(t, "complexity", got[i].complexity, want[i].complexity)
			compareControlParityScalar(t, "vbr", got[i].vbr, want[i].vbr)
			compareControlParityScalar(t, "vbrConstraint", got[i].vbrConstraint, want[i].vbrConstraint)
			compareControlParityScalar(t, "fec", got[i].fec, want[i].fec)
			compareControlParityScalar(t, "packetLoss", got[i].packetLoss, want[i].packetLoss)
			compareControlParityScalar(t, "dtx", got[i].dtx, want[i].dtx)
			compareControlParityScalar(t, "inDTX", got[i].inDTX, want[i].inDTX)
			compareControlParityScalar(t, "forceChannels", got[i].forceChannels, want[i].forceChannels)
			compareControlParityScalar(t, "signal", got[i].signal, want[i].signal)
			compareControlParityScalar(t, "bandwidth", got[i].bandwidth, want[i].bandwidth)
			compareControlParityScalar(t, "maxBandwidth", got[i].maxBandwidth, want[i].maxBandwidth)
			compareControlParityScalar(t, "expertFrameDuration", got[i].expertFrameDuration, want[i].expertFrameDuration)
			compareControlParityScalar(t, "lsbDepth", got[i].lsbDepth, want[i].lsbDepth)
			compareControlParityScalar(t, "predictionDisabled", got[i].predictionDisabled, want[i].predictionDisabled)
			compareControlParityScalar(t, "phaseInversionDisabled", got[i].phaseInversionDisabled, want[i].phaseInversionDisabled)
			if opts.exactPacketBytes {
				compareControlPacketBytes(t, got[i].packet, want[i].packet)
			} else if !opts.onlyControlState {
				compareControlPacketMetadata(t, got[i].packet, want[i].packet)
			}
		})
	}
}

func requireControlStepPacketContract(t *testing.T, name string, step controlParityStep) {
	t.Helper()
	if step.ret < 0 {
		t.Fatalf("%s ret=%d want non-negative", name, step.ret)
	}
	if len(step.packet) != step.ret {
		t.Fatalf("%s packet length=%d want ret %d", name, len(step.packet), step.ret)
	}
}

func compareControlParityScalar(t *testing.T, name string, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("%s=%d want %d", name, got, want)
	}
}

func compareControlPacketBytes(t *testing.T, gotPacket, wantPacket []byte) {
	t.Helper()
	if !bytes.Equal(gotPacket, wantPacket) {
		t.Fatalf("packet bytes differ:\n got % X\nwant % X", gotPacket, wantPacket)
	}
	compareControlPacketMetadata(t, gotPacket, wantPacket)
}

func compareControlPacketMetadata(t *testing.T, gotPacket, wantPacket []byte) {
	t.Helper()

	got, err := ParsePacket(gotPacket)
	if err != nil {
		t.Fatalf("ParsePacket(got): %v", err)
	}
	want, err := ParsePacket(wantPacket)
	if err != nil {
		t.Fatalf("ParsePacket(libopus): %v", err)
	}

	if got.TOC.Mode != want.TOC.Mode {
		t.Fatalf("packet mode=%v want libopus %v", got.TOC.Mode, want.TOC.Mode)
	}
	if got.TOC.Bandwidth != want.TOC.Bandwidth {
		t.Fatalf("packet bandwidth=%v want libopus %v", got.TOC.Bandwidth, want.TOC.Bandwidth)
	}
	if got.TOC.FrameSize != want.TOC.FrameSize {
		t.Fatalf("packet frame size=%d want libopus %d", got.TOC.FrameSize, want.TOC.FrameSize)
	}
	if got.TOC.Stereo != want.TOC.Stereo {
		t.Fatalf("packet stereo=%v want libopus %v", got.TOC.Stereo, want.TOC.Stereo)
	}
	if got.FrameCount != want.FrameCount {
		t.Fatalf("packet frame count=%d want libopus %d", got.FrameCount, want.FrameCount)
	}
}

func collectPacketMetadataDrifts(t *testing.T, got, want []controlParityStep) []string {
	t.Helper()
	requireControlStepCount(t, got, want)

	var drifts []string
	for i := range got {
		gotPacket, err := ParsePacket(got[i].packet)
		if err != nil {
			t.Fatalf("ParsePacket(got step %d): %v", i, err)
		}
		wantPacket, err := ParsePacket(want[i].packet)
		if err != nil {
			t.Fatalf("ParsePacket(libopus step %d): %v", i, err)
		}
		prefix := fmt.Sprintf("step_%d/", i)
		if gotPacket.TOC.Mode != wantPacket.TOC.Mode {
			drifts = append(drifts, prefix+"packet_mode")
		}
		if gotPacket.TOC.Bandwidth != wantPacket.TOC.Bandwidth {
			drifts = append(drifts, prefix+"packet_bandwidth")
		}
		if gotPacket.TOC.FrameSize != wantPacket.TOC.FrameSize {
			drifts = append(drifts, prefix+"packet_frame_size")
		}
		if gotPacket.TOC.Stereo != wantPacket.TOC.Stereo {
			drifts = append(drifts, prefix+"packet_stereo")
		}
		if gotPacket.FrameCount != wantPacket.FrameCount {
			drifts = append(drifts, prefix+"packet_frame_count")
		}
	}
	return drifts
}

func requireControlStepCount(t *testing.T, got, want []controlParityStep) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("step count=%d want %d", len(got), len(want))
	}
}

func assertControlDriftSet(t *testing.T, got, want []string) {
	t.Helper()

	sort.Strings(got)
	sort.Strings(want)
	if !equalStringSlices(got, want) {
		t.Fatalf("meaningful drift set changed:\n got %v\nwant %v", got, want)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func probeLibopusControlScenario(name string) ([]controlParityStep, error) {
	binPath, err := getLibopusControlHelperPath()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(binPath, name)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("run libopus control helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return parseLibopusControlOutput(out)
}

func parseLibopusControlOutput(data []byte) ([]controlParityStep, error) {
	r := bytes.NewReader(data)
	magic := make([]byte, 4)
	if _, err := io.ReadFull(r, magic); err != nil {
		return nil, err
	}
	if string(magic) != "GOCP" {
		return nil, fmt.Errorf("unexpected helper magic %q", string(magic))
	}
	version, err := readControlU32(r)
	if err != nil {
		return nil, err
	}
	if version != 1 {
		return nil, fmt.Errorf("unsupported helper version %d", version)
	}
	count, err := readControlU32(r)
	if err != nil {
		return nil, err
	}
	steps := make([]controlParityStep, 0, count)
	for i := uint32(0); i < count; i++ {
		step := controlParityStep{}
		if step.frameSize, err = readControlI32(r); err != nil {
			return nil, err
		}
		if step.channels, err = readControlI32(r); err != nil {
			return nil, err
		}
		if step.application, err = readControlI32(r); err != nil {
			return nil, err
		}
		if step.ret, err = readControlI32(r); err != nil {
			return nil, err
		}
		if step.lookahead, err = readControlI32(r); err != nil {
			return nil, err
		}
		if step.finalRange, err = readControlU32(r); err != nil {
			return nil, err
		}
		fields := []*int{
			&step.bitrate,
			&step.complexity,
			&step.vbr,
			&step.vbrConstraint,
			&step.fec,
			&step.packetLoss,
			&step.dtx,
			&step.inDTX,
			&step.forceChannels,
			&step.signal,
			&step.bandwidth,
			&step.maxBandwidth,
			&step.expertFrameDuration,
			&step.lsbDepth,
			&step.predictionDisabled,
			&step.phaseInversionDisabled,
		}
		for _, field := range fields {
			if *field, err = readControlI32(r); err != nil {
				return nil, err
			}
		}
		packetLen, err := readControlI32(r)
		if err != nil {
			return nil, err
		}
		if packetLen < 0 || packetLen > maxPacketBytesPerStream {
			return nil, fmt.Errorf("invalid helper packet length %d", packetLen)
		}
		step.packet = make([]byte, packetLen)
		if _, err := io.ReadFull(r, step.packet); err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	if r.Len() != 0 {
		return nil, fmt.Errorf("trailing helper bytes: %d", r.Len())
	}
	return steps, nil
}

func readControlI32(r io.Reader) (int, error) {
	var v int32
	if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
		return 0, err
	}
	return int(v), nil
}

func readControlU32(r io.Reader) (uint32, error) {
	var v uint32
	if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
		return 0, err
	}
	return v, nil
}

func getLibopusControlHelperPath() (string, error) {
	libopusControlHelperOnce.Do(func() {
		libopusControlHelperPath, libopusControlHelperErr = buildLibopusControlHelper()
	})
	return libopusControlHelperPath, libopusControlHelperErr
}

func buildLibopusControlHelper() (string, error) {
	ccPath, err := libopustooling.FindCCompiler()
	if err != nil {
		return "", err
	}
	repoRoot, err := os.Getwd()
	if err != nil {
		return "", err
	}

	sourceDir := filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion)
	libopusStatic := filepath.Join(sourceDir, ".libs", "libopus.a")
	if _, err := os.Stat(libopusStatic); err != nil {
		libopustooling.EnsureLibopus(libopustooling.DefaultVersion, []string{repoRoot})
		if _, err := os.Stat(libopusStatic); err != nil {
			return "", fmt.Errorf("libopus static library unavailable: %w", err)
		}
	}

	srcPath := filepath.Join(repoRoot, "tools", "csrc", "libopus_control_sequence.c")
	outPath := filepath.Join(repoRoot, "tmp_check", fmt.Sprintf("gopus_libopus_control_sequence_%s_%s", runtime.GOOS, runtime.GOARCH))
	if runtime.GOOS == "windows" {
		outPath += ".exe"
	}

	args := []string{
		"-std=c99",
		"-O2",
		"-DHAVE_CONFIG_H",
		"-I", sourceDir,
		"-I", filepath.Join(sourceDir, "include"),
		srcPath,
		libopusStatic,
		"-lm",
		"-o", outPath,
	}
	cmd := exec.Command(ccPath, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build libopus control helper: %w (%s)", err, bytes.TrimSpace(output))
	}
	return outPath, nil
}

func mustSetControl(t *testing.T, err error, name string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
