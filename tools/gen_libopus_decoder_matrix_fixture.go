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
	"strconv"
)

const (
	generatorSampleRate = 48000
	frameRuns           = 50
)

type decoderMatrixFixtureFile struct {
	Version    int                        `json:"version"`
	SampleRate int                        `json:"sample_rate"`
	Generator  string                     `json:"generator"`
	Signal     string                     `json:"signal"`
	Cases      []decoderMatrixFixtureCase `json:"cases"`
}

type decoderMatrixFixtureCase struct {
	Name          string                       `json:"name"`
	Application   string                       `json:"application"`
	Bandwidth     string                       `json:"bandwidth"`
	FrameSize     int                          `json:"frame_size"`
	Channels      int                          `json:"channels"`
	Bitrate       int                          `json:"bitrate"`
	Frames        int                          `json:"frames"`
	ModeHistogram map[string]int               `json:"mode_histogram"`
	Packets       []decoderMatrixFixturePacket `json:"packets"`
	DecodedLen    int                          `json:"decoded_len"`
	DecodedF32B64 string                       `json:"decoded_f32_le_b64"`
}

type decoderMatrixFixturePacket struct {
	DataB64    string `json:"data_b64"`
	FinalRange uint32 `json:"final_range"`
}

type decoderCaseConfig struct {
	Name         string
	Application  string
	Bandwidth    string
	FrameSize    int
	Channels     int
	Bitrate      int
	ExpectedMode string
}

func fixtureModeFromTOC(toc byte) string {
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

func parseOpusDemoBitstream(path string) ([]decoderMatrixFixturePacket, map[string]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	out := make([]decoderMatrixFixturePacket, 0, 64)
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
			modeHist[fixtureModeFromTOC(pkt[0])]++
		}
		out = append(out, decoderMatrixFixturePacket{
			DataB64:    base64.StdEncoding.EncodeToString(pkt),
			FinalRange: finalRange,
		})
	}
	if len(out) == 0 {
		return nil, nil, fmt.Errorf("no packets parsed from %s", path)
	}
	return out, modeHist, nil
}

func decodeOpusDemoF32(path string) ([]float32, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	if len(raw)%4 != 0 {
		return nil, nil, fmt.Errorf("decoded float payload length must be multiple of 4, got %d", len(raw))
	}
	samples := make([]float32, len(raw)/4)
	for i := range samples {
		bits := binary.LittleEndian.Uint32(raw[i*4 : i*4+4])
		samples[i] = math.Float32frombits(bits)
	}
	return samples, raw, nil
}

func generateEncoderTestSignal(samples int, channels int) []float32 {
	signal := make([]float32, samples)
	freqs := []float64{440, 1000, 2000}
	amp := 0.3
	modFreqs := []float64{1.3, 2.7, 0.9}
	for i := 0; i < samples; i++ {
		ch := i % channels
		sampleIdx := i / channels
		t := float64(sampleIdx) / generatorSampleRate
		var val float64
		for fi, freq := range freqs {
			f := freq
			if channels == 2 && ch == 1 {
				f *= 1.01
			}
			modDepth := 0.5 + 0.5*math.Sin(2*math.Pi*modFreqs[fi]*t)
			val += amp * modDepth * math.Sin(2*math.Pi*f*t)
		}
		onsetSamples := int(0.010 * generatorSampleRate)
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

func getOpusDemoPath() string {
	candidate := filepath.Join("tmp_check", "opus-1.6.1", "opus_demo")
	if st, err := os.Stat(candidate); err == nil && (st.Mode()&0111) != 0 {
		return candidate
	}
	if p, err := exec.LookPath("opus_demo"); err == nil {
		return p
	}
	return ""
}

func runCase(opusDemoPath string, tmpDir string, c decoderCaseConfig) (decoderMatrixFixtureCase, error) {
	frameSizeArg, err := frameSizeArgFromSamples(c.FrameSize)
	if err != nil {
		return decoderMatrixFixtureCase{}, err
	}
	totalSamples := frameRuns * c.FrameSize * c.Channels
	signal := generateEncoderTestSignal(totalSamples, c.Channels)

	inputPath := filepath.Join(tmpDir, c.Name+".f32")
	bitPath := filepath.Join(tmpDir, c.Name+".bit")
	decodedPath := filepath.Join(tmpDir, c.Name+".decoded.f32")
	if err := writeRawFloat32(inputPath, signal); err != nil {
		return decoderMatrixFixtureCase{}, fmt.Errorf("write input: %w", err)
	}

	encArgs := []string{
		"-e", c.Application, strconv.Itoa(generatorSampleRate), strconv.Itoa(c.Channels), strconv.Itoa(c.Bitrate),
		"-f32", "-cbr", "-complexity", "10", "-bandwidth", c.Bandwidth, "-framesize", frameSizeArg,
		inputPath, bitPath,
	}
	if out, err := exec.Command(opusDemoPath, encArgs...).CombinedOutput(); err != nil {
		return decoderMatrixFixtureCase{}, fmt.Errorf("opus_demo encode failed: %v (%s)", err, out)
	}

	packets, modeHist, err := parseOpusDemoBitstream(bitPath)
	if err != nil {
		return decoderMatrixFixtureCase{}, fmt.Errorf("parse bitstream: %w", err)
	}
	if c.ExpectedMode != "" && modeHist[c.ExpectedMode] == 0 {
		return decoderMatrixFixtureCase{}, fmt.Errorf("expected mode %q not present (hist=%v)", c.ExpectedMode, modeHist)
	}

	decArgs := []string{"-d", strconv.Itoa(generatorSampleRate), strconv.Itoa(c.Channels), "-f32", bitPath, decodedPath}
	if out, err := exec.Command(opusDemoPath, decArgs...).CombinedOutput(); err != nil {
		return decoderMatrixFixtureCase{}, fmt.Errorf("opus_demo decode failed: %v (%s)", err, out)
	}
	decoded, decodedRaw, err := decodeOpusDemoF32(decodedPath)
	if err != nil {
		return decoderMatrixFixtureCase{}, fmt.Errorf("parse decoded f32: %w", err)
	}

	return decoderMatrixFixtureCase{
		Name:          c.Name,
		Application:   c.Application,
		Bandwidth:     c.Bandwidth,
		FrameSize:     c.FrameSize,
		Channels:      c.Channels,
		Bitrate:       c.Bitrate,
		Frames:        len(packets),
		ModeHistogram: modeHist,
		Packets:       packets,
		DecodedLen:    len(decoded),
		DecodedF32B64: base64.StdEncoding.EncodeToString(decodedRaw),
	}, nil
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
		return "", fmt.Errorf("unsupported frame size for fixture generation: %d", frameSize)
	}
}

func main() {
	opusDemoPath := getOpusDemoPath()
	if opusDemoPath == "" {
		fmt.Fprintln(os.Stderr, "opus_demo not found. expected tmp_check/opus-1.6.1/opus_demo")
		os.Exit(1)
	}

	cases := []decoderCaseConfig{
		{Name: "silk-nb-10ms-mono-16k", Application: "restricted-silk", Bandwidth: "NB", FrameSize: 480, Channels: 1, Bitrate: 16000, ExpectedMode: "silk"},
		{Name: "silk-nb-20ms-mono-16k", Application: "restricted-silk", Bandwidth: "NB", FrameSize: 960, Channels: 1, Bitrate: 16000, ExpectedMode: "silk"},
		{Name: "silk-nb-40ms-mono-16k", Application: "restricted-silk", Bandwidth: "NB", FrameSize: 1920, Channels: 1, Bitrate: 16000, ExpectedMode: "silk"},
		{Name: "silk-nb-60ms-mono-16k", Application: "restricted-silk", Bandwidth: "NB", FrameSize: 2880, Channels: 1, Bitrate: 16000, ExpectedMode: "silk"},
		{Name: "silk-mb-20ms-mono-24k", Application: "restricted-silk", Bandwidth: "MB", FrameSize: 960, Channels: 1, Bitrate: 24000, ExpectedMode: "silk"},
		{Name: "silk-wb-10ms-mono-32k", Application: "restricted-silk", Bandwidth: "WB", FrameSize: 480, Channels: 1, Bitrate: 32000, ExpectedMode: "silk"},
		{Name: "silk-wb-20ms-mono-32k", Application: "restricted-silk", Bandwidth: "WB", FrameSize: 960, Channels: 1, Bitrate: 32000, ExpectedMode: "silk"},
		{Name: "silk-wb-40ms-mono-32k", Application: "restricted-silk", Bandwidth: "WB", FrameSize: 1920, Channels: 1, Bitrate: 32000, ExpectedMode: "silk"},
		{Name: "silk-wb-60ms-mono-32k", Application: "restricted-silk", Bandwidth: "WB", FrameSize: 2880, Channels: 1, Bitrate: 32000, ExpectedMode: "silk"},
		{Name: "silk-wb-20ms-stereo-48k", Application: "restricted-silk", Bandwidth: "WB", FrameSize: 960, Channels: 2, Bitrate: 48000, ExpectedMode: "silk"},
		{Name: "celt-fb-2p5ms-mono-64k", Application: "restricted-celt", Bandwidth: "FB", FrameSize: 120, Channels: 1, Bitrate: 64000, ExpectedMode: "celt"},
		{Name: "celt-fb-5ms-mono-64k", Application: "restricted-celt", Bandwidth: "FB", FrameSize: 240, Channels: 1, Bitrate: 64000, ExpectedMode: "celt"},
		{Name: "celt-fb-10ms-mono-64k", Application: "restricted-celt", Bandwidth: "FB", FrameSize: 480, Channels: 1, Bitrate: 64000, ExpectedMode: "celt"},
		{Name: "celt-fb-20ms-mono-64k", Application: "restricted-celt", Bandwidth: "FB", FrameSize: 960, Channels: 1, Bitrate: 64000, ExpectedMode: "celt"},
		{Name: "celt-fb-20ms-stereo-128k", Application: "restricted-celt", Bandwidth: "FB", FrameSize: 960, Channels: 2, Bitrate: 128000, ExpectedMode: "celt"},
		{Name: "celt-swb-20ms-mono-48k", Application: "restricted-celt", Bandwidth: "SWB", FrameSize: 960, Channels: 1, Bitrate: 48000, ExpectedMode: "celt"},
		{Name: "hybrid-swb-10ms-mono-24k", Application: "audio", Bandwidth: "SWB", FrameSize: 480, Channels: 1, Bitrate: 24000, ExpectedMode: "hybrid"},
		{Name: "hybrid-swb-20ms-mono-24k", Application: "audio", Bandwidth: "SWB", FrameSize: 960, Channels: 1, Bitrate: 24000, ExpectedMode: "hybrid"},
		{Name: "hybrid-fb-10ms-mono-24k", Application: "audio", Bandwidth: "FB", FrameSize: 480, Channels: 1, Bitrate: 24000, ExpectedMode: "hybrid"},
		{Name: "hybrid-fb-10ms-stereo-24k", Application: "audio", Bandwidth: "FB", FrameSize: 480, Channels: 2, Bitrate: 24000, ExpectedMode: "hybrid"},
		{Name: "hybrid-fb-20ms-mono-24k", Application: "audio", Bandwidth: "FB", FrameSize: 960, Channels: 1, Bitrate: 24000, ExpectedMode: "hybrid"},
		{Name: "hybrid-fb-20ms-stereo-24k", Application: "audio", Bandwidth: "FB", FrameSize: 960, Channels: 2, Bitrate: 24000, ExpectedMode: "hybrid"},
	}

	tmpDir, err := os.MkdirTemp("", "gopus-decoder-fixture-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create temp dir:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	fixture := decoderMatrixFixtureFile{
		Version:    1,
		SampleRate: generatorSampleRate,
		Generator:  opusDemoPath,
		Signal:     "generateEncoderTestSignal:v1",
		Cases:      make([]decoderMatrixFixtureCase, 0, len(cases)),
	}

	for _, c := range cases {
		fmt.Fprintf(os.Stderr, "generating %s...\n", c.Name)
		fc, err := runCase(opusDemoPath, tmpDir, c)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed %s: %v\n", c.Name, err)
			os.Exit(1)
		}
		fixture.Cases = append(fixture.Cases, fc)
	}

	sort.Slice(fixture.Cases, func(i, j int) bool { return fixture.Cases[i].Name < fixture.Cases[j].Name })

	outPath := filepath.Join("testvectors", "testdata", "libopus_decoder_matrix_fixture.json")
	encoded, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal json:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outPath, append(encoded, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write fixture:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d cases)\n", outPath, len(fixture.Cases))
}
