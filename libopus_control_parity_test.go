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

var (
	libopusControlHelperOnce sync.Once
	libopusControlHelperPath string
	libopusControlHelperErr  error
)

func TestLibopusControlTransitionParity(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T) []controlParityStep
	}{
		{name: "audio_controls", run: runGopusAudioControlParity},
		{name: "lowdelay_controls", run: runGopusLowDelayControlParity},
		{name: "force_channels", run: runGopusForceChannelsParity},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			want, err := probeLibopusControlScenario(tc.name)
			if err != nil {
				t.Skipf("libopus control helper unavailable: %v", err)
			}
			got := tc.run(t)
			compareControlParitySteps(t, got, want)
		})
	}
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

func encodeControlParityStep(t *testing.T, enc *Encoder, packet []byte, frameIndex int) controlParityStep {
	t.Helper()

	pcm := publicCodecPCM(48000, enc.FrameSize(), enc.Channels(), frameIndex, false)
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

func compareControlParitySteps(t *testing.T, got, want []controlParityStep) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("step count=%d want %d", len(got), len(want))
	}
	for i := range got {
		t.Run(fmt.Sprintf("step_%d", i), func(t *testing.T) {
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
			compareControlPacketMetadata(t, got[i].packet, want[i].packet)
		})
	}
}

func compareControlParityScalar(t *testing.T, name string, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("%s=%d want %d", name, got, want)
	}
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
