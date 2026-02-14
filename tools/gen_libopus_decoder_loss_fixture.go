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
	"strconv"
	"strings"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

const (
	lossFixtureSampleRate = 48000
	lossFixtureFrames     = 50
	lossFixtureVersion    = 1

	lossFixtureDefaultOut = "testvectors/testdata/libopus_decoder_loss_fixture.json"
	lossFixtureOutEnv     = "GOPUS_DECODER_LOSS_FIXTURE_OUT"
)

type decoderLossFixtureFile struct {
	Version    int                    `json:"version"`
	SampleRate int                    `json:"sample_rate"`
	Generator  string                 `json:"generator"`
	Signal     string                 `json:"signal"`
	Cases      []decoderLossCase      `json:"cases"`
	Patterns   []string               `json:"patterns"`
	Notes      map[string]interface{} `json:"notes,omitempty"`
}

type decoderLossCase struct {
	Name        string                     `json:"name"`
	Application string                     `json:"application"`
	Bandwidth   string                     `json:"bandwidth"`
	FrameSize   int                        `json:"frame_size"`
	Channels    int                        `json:"channels"`
	Bitrate     int                        `json:"bitrate"`
	Frames      int                        `json:"frames"`
	Packets     []decoderLossFixturePacket `json:"packets"`
	Results     []decoderLossResult        `json:"results"`
}

type decoderLossFixturePacket struct {
	DataB64    string `json:"data_b64"`
	FinalRange uint32 `json:"final_range"`
}

type decoderLossResult struct {
	Pattern       string `json:"pattern"`
	LossBits      string `json:"loss_bits"`
	DecodedLen    int    `json:"decoded_len"`
	DecodedF32B64 string `json:"decoded_f32_le_b64"`
}

type decoderLossCaseSpec struct {
	Name        string
	Application string
	Bandwidth   string
	FrameSize   int
	Channels    int
	Bitrate     int
}

type lossPatternSpec struct {
	Name string
	Mask []int
}

func parseOpusDemoBitstream(path string) ([]decoderLossFixturePacket, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := make([]decoderLossFixturePacket, 0, 64)
	off := 0
	for off+8 <= len(data) {
		pktLen := int(binary.BigEndian.Uint32(data[off : off+4]))
		off += 4
		finalRange := binary.BigEndian.Uint32(data[off : off+4])
		off += 4
		if pktLen < 0 || off+pktLen > len(data) {
			return nil, fmt.Errorf("invalid packet length %d at offset %d", pktLen, off)
		}
		pkt := data[off : off+pktLen]
		off += pktLen
		out = append(out, decoderLossFixturePacket{
			DataB64:    base64.StdEncoding.EncodeToString(pkt),
			FinalRange: finalRange,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no packets parsed from %s", path)
	}
	return out, nil
}

func decodeOpusDemoF32(path string) ([]byte, int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if len(raw)%4 != 0 {
		return nil, 0, fmt.Errorf("decoded float payload length must be multiple of 4, got %d", len(raw))
	}
	return raw, len(raw) / 4, nil
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

func writeLossFile(path string, mask []int) error {
	var b strings.Builder
	for i := range mask {
		b.WriteString(strconv.Itoa(mask[i]))
		b.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
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
	default:
		return "", fmt.Errorf("unsupported frame size for fixture generation: %d", frameSize)
	}
}

func outputPath() string {
	if v := os.Getenv(lossFixtureOutEnv); v != "" {
		return v
	}
	return lossFixtureDefaultOut
}

func getOpusDemoPath() string {
	if p, ok := libopustooling.FindOrEnsureOpusDemo(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots()); ok {
		return p
	}
	return ""
}

func generateLossFixtureSignal(totalSamples, channels int) []float32 {
	out := make([]float32, totalSamples)
	for i := 0; i < totalSamples; i++ {
		ch := i % channels
		n := i / channels
		t := float64(n) / lossFixtureSampleRate

		base := 0.28*math.Sin(2*math.Pi*180.0*t) +
			0.19*math.Sin(2*math.Pi*360.0*t) +
			0.12*math.Sin(2*math.Pi*720.0*t)
		mod := 0.65 + 0.35*math.Sin(2*math.Pi*1.7*t)
		v := base * mod

		// Add sparse transients so PLC/FEC paths are exercised on non-stationary input.
		if n%960 == 240 || n%960 == 480 {
			v += 0.20 * math.Sin(2*math.Pi*2400.0*t)
		}
		if ch == 1 {
			v *= 0.93
		}
		out[i] = float32(v)
	}
	return out
}

func buildLossPatterns(frames int) []lossPatternSpec {
	newMask := func() []int {
		return make([]int, frames)
	}

	single := newMask()
	if frames > 14 {
		single[12] = 1
	}

	burst2 := newMask()
	if frames > 24 {
		burst2[20] = 1
		burst2[21] = 1
	}

	periodic := newMask()
	for i := 9; i < frames-1; i += 9 {
		periodic[i] = 1
	}

	return []lossPatternSpec{
		{Name: "single_mid", Mask: single},
		{Name: "burst2_mid", Mask: burst2},
		{Name: "periodic9", Mask: periodic},
	}
}

func maskToBits(mask []int) string {
	var b strings.Builder
	b.Grow(len(mask))
	for i := range mask {
		if mask[i] != 0 {
			b.WriteByte('1')
		} else {
			b.WriteByte('0')
		}
	}
	return b.String()
}

func runCase(
	opusDemoPath string,
	tmpDir string,
	spec decoderLossCaseSpec,
) (decoderLossCase, error) {
	frameSizeArg, err := frameSizeArgFromSamples(spec.FrameSize)
	if err != nil {
		return decoderLossCase{}, err
	}

	totalSamples := lossFixtureFrames * spec.FrameSize * spec.Channels
	signal := generateLossFixtureSignal(totalSamples, spec.Channels)

	inputPath := filepath.Join(tmpDir, spec.Name+".f32")
	bitPath := filepath.Join(tmpDir, spec.Name+".bit")
	if err := writeRawFloat32(inputPath, signal); err != nil {
		return decoderLossCase{}, fmt.Errorf("write input: %w", err)
	}

	encArgs := []string{
		"-e", spec.Application, strconv.Itoa(lossFixtureSampleRate), strconv.Itoa(spec.Channels), strconv.Itoa(spec.Bitrate),
		"-f32", "-cbr", "-complexity", "10",
		"-bandwidth", spec.Bandwidth,
		"-framesize", frameSizeArg,
		"-inbandfec",
		"-loss", "15",
		inputPath, bitPath,
	}
	if out, err := exec.Command(opusDemoPath, encArgs...).CombinedOutput(); err != nil {
		return decoderLossCase{}, fmt.Errorf("opus_demo encode failed: %v (%s)", err, out)
	}

	packets, err := parseOpusDemoBitstream(bitPath)
	if err != nil {
		return decoderLossCase{}, fmt.Errorf("parse bitstream: %w", err)
	}
	patterns := buildLossPatterns(len(packets))

	results := make([]decoderLossResult, 0, len(patterns))
	for _, p := range patterns {
		lossPath := filepath.Join(tmpDir, spec.Name+"."+p.Name+".loss.txt")
		decodedPath := filepath.Join(tmpDir, spec.Name+"."+p.Name+".decoded.f32")
		if err := writeLossFile(lossPath, p.Mask); err != nil {
			return decoderLossCase{}, fmt.Errorf("write lossfile (%s): %w", p.Name, err)
		}

		decArgs := []string{
			"-d", strconv.Itoa(lossFixtureSampleRate), strconv.Itoa(spec.Channels),
			"-f32",
			"-lossfile", lossPath,
			bitPath, decodedPath,
		}
		if out, err := exec.Command(opusDemoPath, decArgs...).CombinedOutput(); err != nil {
			return decoderLossCase{}, fmt.Errorf("opus_demo decode failed (%s): %v (%s)", p.Name, err, out)
		}

		raw, decodedLen, err := decodeOpusDemoF32(decodedPath)
		if err != nil {
			return decoderLossCase{}, fmt.Errorf("parse decoded f32 (%s): %w", p.Name, err)
		}

		results = append(results, decoderLossResult{
			Pattern:       p.Name,
			LossBits:      maskToBits(p.Mask),
			DecodedLen:    decodedLen,
			DecodedF32B64: base64.StdEncoding.EncodeToString(raw),
		})
	}

	return decoderLossCase{
		Name:        spec.Name,
		Application: spec.Application,
		Bandwidth:   spec.Bandwidth,
		FrameSize:   spec.FrameSize,
		Channels:    spec.Channels,
		Bitrate:     spec.Bitrate,
		Frames:      len(packets),
		Packets:     packets,
		Results:     results,
	}, nil
}

func main() {
	opusDemoPath := getOpusDemoPath()
	if opusDemoPath == "" {
		fmt.Fprintf(os.Stderr, "opus_demo not found. expected tmp_check/opus-%s/opus_demo (run: make ensure-libopus)\n", libopustooling.DefaultVersion)
		os.Exit(1)
	}

	caseSpecs := []decoderLossCaseSpec{
		{
			Name:        "silk-wb-20ms-mono-24k-fec",
			Application: "restricted-silk",
			Bandwidth:   "WB",
			FrameSize:   960,
			Channels:    1,
			Bitrate:     24000,
		},
		{
			Name:        "hybrid-fb-20ms-mono-32k-fec",
			Application: "audio",
			Bandwidth:   "FB",
			FrameSize:   960,
			Channels:    1,
			Bitrate:     32000,
		},
		{
			Name:        "celt-fb-20ms-mono-64k-plc",
			Application: "restricted-celt",
			Bandwidth:   "FB",
			FrameSize:   960,
			Channels:    1,
			Bitrate:     64000,
		},
	}

	tmpDir, err := os.MkdirTemp("", "gopus-decoder-loss-fixture-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create temp dir:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	fixture := decoderLossFixtureFile{
		Version:    lossFixtureVersion,
		SampleRate: lossFixtureSampleRate,
		Generator:  opusDemoPath,
		Signal:     "generateLossFixtureSignal:v1",
		Cases:      make([]decoderLossCase, 0, len(caseSpecs)),
		Patterns:   []string{"single_mid", "burst2_mid", "periodic9"},
		Notes: map[string]interface{}{
			"flow": "opus_demo decode-only with lossfile; parity tests mirror opus_demo loss->fec/plc decode cadence",
		},
	}

	for _, spec := range caseSpecs {
		fmt.Fprintf(os.Stderr, "generating %s...\n", spec.Name)
		c, err := runCase(opusDemoPath, tmpDir, spec)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed %s: %v\n", spec.Name, err)
			os.Exit(1)
		}
		fixture.Cases = append(fixture.Cases, c)
	}

	sort.Slice(fixture.Cases, func(i, j int) bool { return fixture.Cases[i].Name < fixture.Cases[j].Name })
	for i := range fixture.Cases {
		sort.Slice(fixture.Cases[i].Results, func(a, b int) bool {
			return fixture.Cases[i].Results[a].Pattern < fixture.Cases[i].Results[b].Pattern
		})
	}

	encoded, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal json:", err)
		os.Exit(1)
	}

	outPath := outputPath()
	if err := os.WriteFile(outPath, append(encoded, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write fixture:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d cases)\n", outPath, len(fixture.Cases))
}
