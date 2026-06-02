//go:build ignore

package main

// gen_libopus_decoder_rate_matrix_fixture.go generates
// testvectors/testdata/libopus_decoder_rate_matrix_fixture.json.
//
// For each of the 26 encode configs used by the 48 kHz decoder matrix, it
// encodes the test signal at 48 kHz and then decodes the resulting bitstream at
// each API sample rate (8000, 12000, 16000, 24000, 48000 Hz) via
// opus_demo -d <rate>. The resulting fixture captures both the shared packets
// (encoded at 48 kHz) and the per-rate libopus reference decode so gopus
// per-rate parity tests can compare directly without re-running opus_demo.
//
// Usage:
//
//	go run tools/gen_libopus_decoder_rate_matrix_fixture.go
//	GOPUS_DECODER_RATE_MATRIX_FIXTURE_OUT=path/to/out.json \
//	  go run tools/gen_libopus_decoder_rate_matrix_fixture.go

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

	"github.com/thesyncim/gopus/internal/libopustooling"
)

const (
	rateMatrixGeneratorSampleRate = 48000
	rateMatrixFrameRuns           = 50
	rateMatrixDefaultOutputPath   = "testvectors/testdata/libopus_decoder_rate_matrix_fixture.json"
	rateMatrixOutputPathEnv       = "GOPUS_DECODER_RATE_MATRIX_FIXTURE_OUT"
)

// apiRates lists all Opus API decode rates supported by opus_decoder_create.
var apiRates = []int{8000, 12000, 16000, 24000, 48000}

type rateMatrixFixtureFile struct {
	Version    int                                   `json:"version"`
	Generator  string                                `json:"generator"`
	Provenance libopustooling.LibopusBuildProvenance `json:"provenance"`
	Signal     string                                `json:"signal"`
	Cases      []rateMatrixFixtureCase               `json:"cases"`
}

type rateMatrixFixtureCase struct {
	Name          string                    `json:"name"`
	Application   string                    `json:"application"`
	Bandwidth     string                    `json:"bandwidth"`
	FrameSize     int                       `json:"frame_size"`
	Channels      int                       `json:"channels"`
	Bitrate       int                       `json:"bitrate"`
	Frames        int                       `json:"frames"`
	APIRate       int                       `json:"api_rate"`
	ModeHistogram map[string]int            `json:"mode_histogram"`
	Packets       []rateMatrixFixturePacket `json:"packets"`
	DecodedLen    int                       `json:"decoded_len"`
	DecodedF32B64 string                    `json:"decoded_f32_le_b64"`
}

type rateMatrixFixturePacket struct {
	DataB64    string `json:"data_b64"`
	FinalRange uint32 `json:"final_range"`
}

type rateMatrixEncodeConfig struct {
	Name         string
	Application  string
	Bandwidth    string
	FrameSize    int
	Channels     int
	Bitrate      int
	ExpectedMode string
}

func rateMatrixFixtureModeFromTOC(toc byte) string {
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

func rateMatrixParseOpusDemoBitstream(path string) ([]rateMatrixFixturePacket, map[string]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	out := make([]rateMatrixFixturePacket, 0, 64)
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
			modeHist[rateMatrixFixtureModeFromTOC(pkt[0])]++
		}
		out = append(out, rateMatrixFixturePacket{
			DataB64:    base64.StdEncoding.EncodeToString(pkt),
			FinalRange: finalRange,
		})
	}
	if len(out) == 0 {
		return nil, nil, fmt.Errorf("no packets parsed from %s", path)
	}
	return out, modeHist, nil
}

func rateMatrixDecodeF32(path string) ([]float32, []byte, error) {
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

func rateMatrixGenerateSignal(samples, channels int) []float32 {
	signal := make([]float32, samples)
	freqs := []float64{440, 1000, 2000}
	amp := 0.3
	modFreqs := []float64{1.3, 2.7, 0.9}
	for i := 0; i < samples; i++ {
		ch := i % channels
		sampleIdx := i / channels
		t := float64(sampleIdx) / rateMatrixGeneratorSampleRate
		var val float64
		for fi, freq := range freqs {
			f := freq
			if channels == 2 && ch == 1 {
				f *= 1.01
			}
			modDepth := 0.5 + 0.5*math.Sin(2*math.Pi*modFreqs[fi]*t)
			val += amp * modDepth * math.Sin(2*math.Pi*f*t)
		}
		onsetSamples := int(0.010 * rateMatrixGeneratorSampleRate)
		if sampleIdx < onsetSamples {
			frac := float64(sampleIdx) / float64(onsetSamples)
			val *= frac * frac * frac
		}
		signal[i] = float32(val)
	}
	return signal
}

func rateMatrixWriteRawFloat32(path string, samples []float32) error {
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

func rateMatrixFrameSizeArg(frameSize int) (string, error) {
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

// runRateCase encodes one config at 48 kHz and decodes it at each API rate,
// producing one rateMatrixFixtureCase per rate.
func runRateCase(opusDemoPath, tmpDir string, c rateMatrixEncodeConfig) ([]rateMatrixFixtureCase, error) {
	frameSizeArg, err := rateMatrixFrameSizeArg(c.FrameSize)
	if err != nil {
		return nil, err
	}
	totalSamples := rateMatrixFrameRuns * c.FrameSize * c.Channels
	signal := rateMatrixGenerateSignal(totalSamples, c.Channels)

	inputPath := filepath.Join(tmpDir, c.Name+".f32")
	bitPath := filepath.Join(tmpDir, c.Name+".bit")
	if err := rateMatrixWriteRawFloat32(inputPath, signal); err != nil {
		return nil, fmt.Errorf("write input: %w", err)
	}

	// Encode at 48 kHz.
	encArgs := []string{
		"-e", c.Application,
		strconv.Itoa(rateMatrixGeneratorSampleRate),
		strconv.Itoa(c.Channels),
		strconv.Itoa(c.Bitrate),
		"-f32", "-cbr", "-complexity", "10",
		"-bandwidth", c.Bandwidth,
		"-framesize", frameSizeArg,
		inputPath, bitPath,
	}
	if out, err := exec.Command(opusDemoPath, encArgs...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("opus_demo encode failed: %v (%s)", err, out)
	}

	packets, modeHist, err := rateMatrixParseOpusDemoBitstream(bitPath)
	if err != nil {
		return nil, fmt.Errorf("parse bitstream: %w", err)
	}
	if c.ExpectedMode != "" && modeHist[c.ExpectedMode] == 0 {
		return nil, fmt.Errorf("expected mode %q not present (hist=%v)", c.ExpectedMode, modeHist)
	}

	// Decode at each API rate.
	results := make([]rateMatrixFixtureCase, 0, len(apiRates))
	for _, rate := range apiRates {
		decodedPath := filepath.Join(tmpDir, fmt.Sprintf("%s.decoded%d.f32", c.Name, rate))
		decArgs := []string{"-d", strconv.Itoa(rate), strconv.Itoa(c.Channels), "-f32", bitPath, decodedPath}
		if out, err := exec.Command(opusDemoPath, decArgs...).CombinedOutput(); err != nil {
			return nil, fmt.Errorf("opus_demo decode at %d Hz failed: %v (%s)", rate, err, out)
		}
		_, decodedRaw, err := rateMatrixDecodeF32(decodedPath)
		if err != nil {
			return nil, fmt.Errorf("parse decoded f32 at %d Hz: %w", rate, err)
		}
		decodedLen := len(decodedRaw) / 4
		results = append(results, rateMatrixFixtureCase{
			Name:          c.Name,
			Application:   c.Application,
			Bandwidth:     c.Bandwidth,
			FrameSize:     c.FrameSize,
			Channels:      c.Channels,
			Bitrate:       c.Bitrate,
			Frames:        len(packets),
			APIRate:       rate,
			ModeHistogram: modeHist,
			Packets:       packets,
			DecodedLen:    decodedLen,
			DecodedF32B64: base64.StdEncoding.EncodeToString(decodedRaw),
		})
	}
	return results, nil
}

func rateMatrixOutputPath() string {
	if v := os.Getenv(rateMatrixOutputPathEnv); v != "" {
		return v
	}
	return rateMatrixDefaultOutputPath
}

func main() {
	opusDemoPath, ok := libopustooling.FindOrEnsureOpusDemo(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots())
	if !ok {
		fmt.Fprintf(os.Stderr, "opus_demo not found. expected tmp_check/opus-%s/opus_demo (run: make ensure-libopus)\n", libopustooling.DefaultVersion)
		os.Exit(1)
	}
	provenance, ok := libopustooling.LibopusBuildProvenanceForTool(opusDemoPath)
	if !ok {
		fmt.Fprintf(os.Stderr, "libopus build provenance not found for %s (run: make ensure-libopus)\n", opusDemoPath)
		os.Exit(1)
	}

	// Identical encode configs to gen_libopus_decoder_matrix_fixture.go so the
	// packets in both fixtures come from the same signal/encoder setup.
	configs := []rateMatrixEncodeConfig{
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
		// libopus audio@FB 60 ms selects CELT at 64 kb/s.
		{Name: "celt-fb-60ms-mono-64k", Application: "audio", Bandwidth: "FB", FrameSize: 2880, Channels: 1, Bitrate: 64000, ExpectedMode: "celt"},
		{Name: "silk-wb-80ms-mono-32k", Application: "restricted-silk", Bandwidth: "WB", FrameSize: 3840, Channels: 1, Bitrate: 32000, ExpectedMode: "silk"},
		{Name: "celt-fb-80ms-mono-64k", Application: "restricted-celt", Bandwidth: "FB", FrameSize: 3840, Channels: 1, Bitrate: 64000, ExpectedMode: "celt"},
		{Name: "silk-wb-120ms-mono-32k", Application: "restricted-silk", Bandwidth: "WB", FrameSize: 5760, Channels: 1, Bitrate: 32000, ExpectedMode: "silk"},
	}

	tmpDir, err := os.MkdirTemp("", "gopus-decoder-rate-fixture-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create temp dir:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	fixture := rateMatrixFixtureFile{
		Version:    1,
		Generator:  opusDemoPath,
		Provenance: provenance,
		Signal:     "generateEncoderTestSignal:v1",
		Cases:      make([]rateMatrixFixtureCase, 0, len(configs)*len(apiRates)),
	}

	for _, c := range configs {
		fmt.Fprintf(os.Stderr, "generating %s (all rates)...\n", c.Name)
		cases, err := runRateCase(opusDemoPath, tmpDir, c)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed %s: %v\n", c.Name, err)
			os.Exit(1)
		}
		fixture.Cases = append(fixture.Cases, cases...)
	}

	// Sort by (name, api_rate) for deterministic output.
	sort.Slice(fixture.Cases, func(i, j int) bool {
		a, b := fixture.Cases[i], fixture.Cases[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.APIRate < b.APIRate
	})

	outPath := rateMatrixOutputPath()
	encoded, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal json:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outPath, append(encoded, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write fixture:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d cases, %d rates)\n", outPath, len(configs), len(apiRates))
}
