//go:build ignore
// +build ignore

package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

const (
	encoderFixtureSampleRate = 48000
	refQFixturePath          = "testvectors/testdata/encoder_compliance_libopus_ref_q.json"
	outputFixturePath        = "testvectors/testdata/encoder_compliance_libopus_packets_fixture.json"
)

type refQFixtureFile struct {
	Version int           `json:"version"`
	Cases   []refQCaseRow `json:"cases"`
}

type refQCaseRow struct {
	Mode      string  `json:"mode"`
	Bandwidth string  `json:"bandwidth"`
	FrameSize int     `json:"frame_size"`
	Channels  int     `json:"channels"`
	Bitrate   int     `json:"bitrate"`
	LibQ      float64 `json:"lib_q"`
}

type encoderPacketFixtureFile struct {
	Version    int                      `json:"version"`
	SampleRate int                      `json:"sample_rate"`
	Generator  string                   `json:"generator"`
	Signal     string                   `json:"signal"`
	Cases      []encoderPacketFixtureTC `json:"cases"`
}

type encoderPacketFixtureTC struct {
	Mode          string                       `json:"mode"`
	Bandwidth     string                       `json:"bandwidth"`
	FrameSize     int                          `json:"frame_size"`
	Channels      int                          `json:"channels"`
	Bitrate       int                          `json:"bitrate"`
	LibQ          float64                      `json:"lib_q"`
	SignalFrames  int                          `json:"signal_frames"`
	Frames        int                          `json:"frames"`
	ModeHistogram map[string]int               `json:"mode_histogram"`
	Packets       []encoderPacketFixturePacket `json:"packets"`
}

type encoderPacketFixturePacket struct {
	DataB64    string `json:"data_b64"`
	FinalRange uint32 `json:"final_range"`
}

func getOpusDemoPath() string {
	if p, ok := libopustooling.FindOrEnsureOpusDemo(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots()); ok {
		return p
	}
	return ""
}

func modeFromTOC(toc byte) string {
	cfg := int(toc >> 3)
	switch {
	case cfg <= 11:
		return "silk"
	case cfg <= 15:
		return "hybrid"
	default:
		return "celt"
	}
}

func parseOpusDemoBitstream(path string) ([]encoderPacketFixturePacket, map[string]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	out := make([]encoderPacketFixturePacket, 0, 64)
	modeHist := map[string]int{"silk": 0, "hybrid": 0, "celt": 0}
	off := 0
	for off+8 <= len(data) {
		pktLen := int(binary.BigEndian.Uint32(data[off : off+4]))
		off += 4
		finalRange := binary.BigEndian.Uint32(data[off : off+4])
		off += 4
		if pktLen < 0 || off+pktLen > len(data) {
			return nil, nil, fmt.Errorf("invalid packet length %d at offset %d", pktLen, off)
		}
		pkt := data[off : off+pktLen]
		off += pktLen
		if len(pkt) > 0 {
			modeHist[modeFromTOC(pkt[0])]++
		}
		out = append(out, encoderPacketFixturePacket{
			DataB64:    base64.StdEncoding.EncodeToString(pkt),
			FinalRange: finalRange,
		})
	}
	if len(out) == 0 {
		return nil, nil, fmt.Errorf("no packets parsed from %s", path)
	}
	return out, modeHist, nil
}

func frameSizeArgFromSamples(frameSize int) (string, error) {
	switch frameSize {
	case 120:
		return "2.5", nil
	case 240:
		return "5", nil
	case 480:
		return "10", nil
	case 960:
		return "20", nil
	case 1920:
		return "40", nil
	case 2880:
		return "60", nil
	case 3840:
		return "80", nil
	case 4800:
		return "100", nil
	case 5760:
		return "120", nil
	default:
		return "", fmt.Errorf("unsupported frame size: %d", frameSize)
	}
}

func generateEncoderTestSignal(samples int, channels int) []float32 {
	signal := make([]float32, samples)
	freqs := []float64{440, 1000, 2000}
	amp := 0.3
	modFreqs := []float64{1.3, 2.7, 0.9}
	for i := 0; i < samples; i++ {
		ch := i % channels
		sampleIdx := i / channels
		t := float64(sampleIdx) / encoderFixtureSampleRate
		var val float64
		for fi, freq := range freqs {
			f := freq
			if channels == 2 && ch == 1 {
				f *= 1.01
			}
			modDepth := 0.5 + 0.5*math.Sin(2*math.Pi*modFreqs[fi]*t)
			val += amp * modDepth * math.Sin(2*math.Pi*f*t)
		}
		onsetSamples := int(0.010 * encoderFixtureSampleRate)
		if sampleIdx < onsetSamples {
			frac := float64(sampleIdx) / float64(onsetSamples)
			val *= frac * frac * frac
		}
		signal[i] = float32(val)
	}
	return signal
}

func writeRawFloat32(path string, samples []float32) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, 4)
	for _, s := range samples {
		binary.LittleEndian.PutUint32(buf, math.Float32bits(s))
		if _, err := f.Write(buf); err != nil {
			return err
		}
	}
	return nil
}

func appFromMode(mode string) (string, error) {
	switch strings.ToLower(mode) {
	case "celt":
		return "restricted-celt", nil
	case "silk":
		return "restricted-silk", nil
	case "hybrid":
		return "audio", nil
	default:
		return "", fmt.Errorf("unsupported mode %q", mode)
	}
}

func bwArgFromFixture(bw string) (string, error) {
	switch strings.ToLower(bw) {
	case "nb":
		return "NB", nil
	case "mb":
		return "MB", nil
	case "wb":
		return "WB", nil
	case "swb":
		return "SWB", nil
	case "fb":
		return "FB", nil
	default:
		return "", fmt.Errorf("unsupported bandwidth %q", bw)
	}
}

func loadRefQFixture() (refQFixtureFile, error) {
	data, err := os.ReadFile(refQFixturePath)
	if err != nil {
		return refQFixtureFile{}, err
	}
	var fixture refQFixtureFile
	if err := json.Unmarshal(data, &fixture); err != nil {
		return refQFixtureFile{}, err
	}
	return fixture, nil
}

func runCase(opusDemoPath string, tmpDir string, row refQCaseRow) (encoderPacketFixtureTC, error) {
	app, err := appFromMode(row.Mode)
	if err != nil {
		return encoderPacketFixtureTC{}, err
	}
	bwArg, err := bwArgFromFixture(row.Bandwidth)
	if err != nil {
		return encoderPacketFixtureTC{}, err
	}
	frameArg, err := frameSizeArgFromSamples(row.FrameSize)
	if err != nil {
		return encoderPacketFixtureTC{}, err
	}
	signalFrames := encoderFixtureSampleRate / row.FrameSize
	totalSamples := signalFrames * row.FrameSize * row.Channels
	signal := generateEncoderTestSignal(totalSamples, row.Channels)

	key := fmt.Sprintf("%s_%s_%d_%d_%d", strings.ToLower(row.Mode), strings.ToLower(row.Bandwidth), row.FrameSize, row.Channels, row.Bitrate)
	inputPath := filepath.Join(tmpDir, key+".f32")
	bitPath := filepath.Join(tmpDir, key+".bit")
	if err := writeRawFloat32(inputPath, signal); err != nil {
		return encoderPacketFixtureTC{}, fmt.Errorf("write input: %w", err)
	}

	encArgs := []string{
		"-e", app, fmt.Sprintf("%d", encoderFixtureSampleRate), fmt.Sprintf("%d", row.Channels), fmt.Sprintf("%d", row.Bitrate),
		"-f32", "-cbr", "-complexity", "10", "-bandwidth", bwArg, "-framesize", frameArg,
		inputPath, bitPath,
	}
	if out, err := exec.Command(opusDemoPath, encArgs...).CombinedOutput(); err != nil {
		return encoderPacketFixtureTC{}, fmt.Errorf("opus_demo encode failed: %v (%s)", err, out)
	}

	packets, modeHist, err := parseOpusDemoBitstream(bitPath)
	if err != nil {
		return encoderPacketFixtureTC{}, err
	}

	return encoderPacketFixtureTC{
		Mode:          strings.ToLower(row.Mode),
		Bandwidth:     strings.ToLower(row.Bandwidth),
		FrameSize:     row.FrameSize,
		Channels:      row.Channels,
		Bitrate:       row.Bitrate,
		LibQ:          row.LibQ,
		SignalFrames:  signalFrames,
		Frames:        len(packets),
		ModeHistogram: modeHist,
		Packets:       packets,
	}, nil
}

func main() {
	opusDemoPath := getOpusDemoPath()
	if opusDemoPath == "" {
		fmt.Fprintln(os.Stderr, "opus_demo not found. expected tmp_check/opus-1.6.1/opus_demo (run: make ensure-libopus)")
		os.Exit(1)
	}
	refFixture, err := loadRefQFixture()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load ref-q fixture: %v\n", err)
		os.Exit(1)
	}
	tmpDir, err := os.MkdirTemp("", "gopus-enc-packet-fixture-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	out := encoderPacketFixtureFile{
		Version:    1,
		SampleRate: encoderFixtureSampleRate,
		Generator:  opusDemoPath,
		Signal:     "generateEncoderTestSignal:v1",
		Cases:      make([]encoderPacketFixtureTC, 0, len(refFixture.Cases)),
	}
	for _, row := range refFixture.Cases {
		fmt.Fprintf(os.Stderr, "generating %s/%s fs=%d ch=%d br=%d...\n", row.Mode, row.Bandwidth, row.FrameSize, row.Channels, row.Bitrate)
		tc, err := runCase(opusDemoPath, tmpDir, row)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed case %s/%s fs=%d ch=%d br=%d: %v\n", row.Mode, row.Bandwidth, row.FrameSize, row.Channels, row.Bitrate, err)
			os.Exit(1)
		}
		out.Cases = append(out.Cases, tc)
	}
	sort.Slice(out.Cases, func(i, j int) bool {
		a, b := out.Cases[i], out.Cases[j]
		if a.Mode != b.Mode {
			return a.Mode < b.Mode
		}
		if a.Bandwidth != b.Bandwidth {
			return a.Bandwidth < b.Bandwidth
		}
		if a.FrameSize != b.FrameSize {
			return a.FrameSize < b.FrameSize
		}
		if a.Channels != b.Channels {
			return a.Channels < b.Channels
		}
		return a.Bitrate < b.Bitrate
	})

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal fixture: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outputFixturePath, append(data, '\n'), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write fixture: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d cases)\n", outputFixturePath, len(out.Cases))
}
