package gopus

import (
	"bytes"
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

type decoderControlParityStep struct {
	ret                int
	sampleRate         int
	channels           int
	gain               int
	ignoreExtensions   int
	bandwidth          int
	lastPacketDuration int
	pitch              int
	finalRange         uint32
	packet             []byte
}

var (
	libopusDecoderControlHelperOnce sync.Once
	libopusDecoderControlHelperPath string
	libopusDecoderControlHelperErr  error
)

func TestLibopusDecoderControlParity(t *testing.T) {
	for _, name := range []string{
		"defaults",
		"control_lifecycle",
		"packet_modes_mono",
		"packet_modes_stereo",
	} {
		name := name
		t.Run(name, func(t *testing.T) {
			want, err := probeLibopusDecoderControlScenario(name)
			if err != nil {
				t.Skipf("libopus decoder control helper unavailable: %v", err)
			}
			got := runGopusDecoderControlScenario(t, name, want)
			compareDecoderControlParitySteps(t, got, want)
			if name == "packet_modes_mono" || name == "packet_modes_stereo" {
				assertDecoderControlPacketCoverage(t, want)
			}
		})
	}
}

func runGopusDecoderControlScenario(t *testing.T, name string, want []decoderControlParityStep) []decoderControlParityStep {
	t.Helper()

	switch name {
	case "defaults":
		out := make([]decoderControlParityStep, 0, len(want))
		for _, step := range want {
			dec := mustNewTestDecoder(t, step.sampleRate, step.channels)
			out = append(out, captureDecoderControlParityStep(dec, 0, nil))
		}
		return out
	case "control_lifecycle":
		dec := mustNewTestDecoder(t, 48000, 1)
		out := make([]decoderControlParityStep, 0, 4)
		out = append(out, captureDecoderControlParityStep(dec, 0, nil))

		mustSetControl(t, dec.SetGain(512), "SetGain")
		dec.SetIgnoreExtensions(true)
		out = append(out, captureDecoderControlParityStep(dec, 0, nil))

		dec.Reset()
		out = append(out, captureDecoderControlParityStep(dec, 0, nil))

		mustSetControl(t, dec.SetGain(-768), "SetGain")
		dec.SetIgnoreExtensions(false)
		out = append(out, captureDecoderControlParityStep(dec, 0, nil))
		return out
	case "packet_modes_mono", "packet_modes_stereo":
		if len(want) == 0 {
			t.Fatal("libopus packet scenario produced no steps")
		}
		dec := mustNewTestDecoder(t, want[0].sampleRate, want[0].channels)
		pcm := make([]float32, defaultMaxPacketSamples*want[0].channels)
		out := make([]decoderControlParityStep, 0, len(want))
		for i, step := range want {
			if len(step.packet) == 0 {
				t.Fatalf("step %d has no libopus packet", i)
			}
			n, err := dec.Decode(step.packet, pcm)
			if err != nil {
				t.Fatalf("Decode step %d: %v", i, err)
			}
			out = append(out, captureDecoderControlParityStep(dec, n, step.packet))
		}
		return out
	default:
		t.Fatalf("unknown decoder control scenario %q", name)
		return nil
	}
}

func captureDecoderControlParityStep(dec *Decoder, ret int, packet []byte) decoderControlParityStep {
	return decoderControlParityStep{
		ret:                ret,
		sampleRate:         dec.SampleRate(),
		channels:           dec.Channels(),
		gain:               dec.Gain(),
		ignoreExtensions:   boolInt(dec.IgnoreExtensions()),
		bandwidth:          mapDecoderControlBandwidth(dec.Bandwidth()),
		lastPacketDuration: dec.LastPacketDuration(),
		pitch:              dec.Pitch(),
		finalRange:         dec.FinalRange(),
		packet:             append([]byte(nil), packet...),
	}
}

func mapDecoderControlBandwidth(bw Bandwidth) int {
	switch bw {
	case BandwidthNarrowband:
		return 0
	case BandwidthMediumband:
		return 1
	case BandwidthWideband:
		return 2
	case BandwidthSuperwideband:
		return 3
	case BandwidthFullband:
		return 4
	case BandwidthUnknown:
		return -1
	default:
		return int(bw)
	}
}

func compareDecoderControlParitySteps(t *testing.T, got, want []decoderControlParityStep) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("step count=%d want %d", len(got), len(want))
	}
	for i := range want {
		prefix := fmt.Sprintf("step %d", i)
		compareDecoderControlScalar(t, prefix+" ret", got[i].ret, want[i].ret)
		compareDecoderControlScalar(t, prefix+" sample_rate", got[i].sampleRate, want[i].sampleRate)
		compareDecoderControlScalar(t, prefix+" channels", got[i].channels, want[i].channels)
		compareDecoderControlScalar(t, prefix+" gain", got[i].gain, want[i].gain)
		compareDecoderControlScalar(t, prefix+" ignore_extensions", got[i].ignoreExtensions, want[i].ignoreExtensions)
		compareDecoderControlScalar(t, prefix+" bandwidth", got[i].bandwidth, want[i].bandwidth)
		compareDecoderControlScalar(t, prefix+" last_packet_duration", got[i].lastPacketDuration, want[i].lastPacketDuration)
		compareDecoderControlScalar(t, prefix+" pitch", got[i].pitch, want[i].pitch)
		if got[i].finalRange != want[i].finalRange {
			t.Fatalf("%s final_range=0x%08X want 0x%08X", prefix, got[i].finalRange, want[i].finalRange)
		}
		if len(got[i].packet) != len(want[i].packet) {
			t.Fatalf("%s packet length=%d want %d", prefix, len(got[i].packet), len(want[i].packet))
		}
	}
}

func compareDecoderControlScalar(t *testing.T, name string, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("%s=%d want %d", name, got, want)
	}
}

func assertDecoderControlPacketCoverage(t *testing.T, steps []decoderControlParityStep) {
	t.Helper()
	seenMode := map[Mode]bool{}
	seenStereo := false
	for i, step := range steps {
		info, err := ParsePacket(step.packet)
		if err != nil {
			t.Fatalf("ParsePacket step %d: %v", i, err)
		}
		seenMode[info.TOC.Mode] = true
		seenStereo = seenStereo || info.TOC.Stereo
	}
	for _, mode := range []Mode{ModeSILK, ModeHybrid, ModeCELT} {
		if !seenMode[mode] {
			t.Fatalf("decoder packet scenario missing mode %v", mode)
		}
	}
	if steps[0].channels == 2 && !seenStereo {
		t.Fatal("decoder packet scenario missing stereo packets")
	}
}

func probeLibopusDecoderControlScenario(name string) ([]decoderControlParityStep, error) {
	binPath, err := getLibopusDecoderControlHelperPath()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(binPath, name)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("run libopus decoder control helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return parseLibopusDecoderControlOutput(out)
}

func parseLibopusDecoderControlOutput(data []byte) ([]decoderControlParityStep, error) {
	r := bytes.NewReader(data)
	magic := make([]byte, 4)
	if _, err := io.ReadFull(r, magic); err != nil {
		return nil, err
	}
	if string(magic) != "GODC" {
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
	steps := make([]decoderControlParityStep, 0, count)
	for i := uint32(0); i < count; i++ {
		step := decoderControlParityStep{}
		fields := []*int{
			&step.ret,
			&step.sampleRate,
			&step.channels,
			&step.gain,
			&step.ignoreExtensions,
			&step.bandwidth,
			&step.lastPacketDuration,
			&step.pitch,
		}
		for _, field := range fields {
			if *field, err = readControlI32(r); err != nil {
				return nil, err
			}
		}
		if step.finalRange, err = readControlU32(r); err != nil {
			return nil, err
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

func getLibopusDecoderControlHelperPath() (string, error) {
	libopusDecoderControlHelperOnce.Do(func() {
		libopusDecoderControlHelperPath, libopusDecoderControlHelperErr = buildLibopusDecoderControlHelper()
	})
	return libopusDecoderControlHelperPath, libopusDecoderControlHelperErr
}

func buildLibopusDecoderControlHelper() (string, error) {
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

	srcPath := filepath.Join(repoRoot, "tools", "csrc", "libopus_decoder_control_sequence.c")
	outPath := filepath.Join(repoRoot, "tmp_check", fmt.Sprintf("gopus_libopus_decoder_control_sequence_%s_%s", runtime.GOOS, runtime.GOARCH))
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
		return "", fmt.Errorf("build libopus decoder control helper: %w (%s)", err, bytes.TrimSpace(output))
	}
	return outPath, nil
}
