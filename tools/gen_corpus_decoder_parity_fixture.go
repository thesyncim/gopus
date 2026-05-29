//go:build ignore

// gen_corpus_decoder_parity_fixture generates the broader corpus fixture for
// decoder/encoder quality parity across all signal classes defined in
// internal/testsignal.CorpusSignalClasses(). Each case:
//   - encodes the signal with the pinned libopus opus_demo encoder
//   - decodes it with opus_demo -f32 to produce frozen reference samples
//   - records packets + decoded samples + provenance
//
// Usage:
//
//	go run tools/gen_corpus_decoder_parity_fixture.go \
//	    [GOPUS_CORPUS_FIXTURE_OUT=testvectors/testdata/corpus_decoder_parity_fixture.json]
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

	"github.com/thesyncim/gopus/internal/libopustooling"
	"github.com/thesyncim/gopus/internal/testsignal"
)

const (
	corpusFixtureSampleRate = 48000
	corpusFrameRuns         = 50
	corpusDefaultOutputPath = "testvectors/testdata/corpus_decoder_parity_fixture.json"
	corpusOutputPathEnv     = "GOPUS_CORPUS_FIXTURE_OUT"
)

// corpusFixtureFile is the top-level JSON schema.
type corpusFixtureFile struct {
	Version    int                                   `json:"version"`
	SampleRate int                                   `json:"sample_rate"`
	Generator  string                                `json:"generator"`
	Provenance libopustooling.LibopusBuildProvenance `json:"provenance"`
	Cases      []corpusFixtureCase                   `json:"cases"`
}

type corpusFixtureCase struct {
	Name          string                `json:"name"`
	SignalClass   string                `json:"signal_class"`
	Application   string                `json:"application"`
	Bandwidth     string                `json:"bandwidth"`
	FrameSize     int                   `json:"frame_size"`
	Channels      int                   `json:"channels"`
	Bitrate       int                   `json:"bitrate"`
	Frames        int                   `json:"frames"`
	ModeHistogram map[string]int        `json:"mode_histogram"`
	SignalSHA256  string                `json:"signal_sha256"`
	Packets       []corpusFixturePacket `json:"packets"`
	DecodedLen    int                   `json:"decoded_len"`
	DecodedF32B64 string                `json:"decoded_f32_le_b64"`
}

type corpusFixturePacket struct {
	DataB64    string `json:"data_b64"`
	FinalRange uint32 `json:"final_range"`
}

// corpusCaseConfig describes one case to generate.
type corpusCaseConfig struct {
	Name        string
	SignalClass string
	Application string
	Bandwidth   string
	FrameSize   int
	Channels    int
	Bitrate     int
}

func corpusModeFromTOC(toc byte) string {
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

func corpusParseOpusDemoBitstream(path string) ([]corpusFixturePacket, map[string]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var out []corpusFixturePacket
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
			modeHist[corpusModeFromTOC(pkt[0])]++
		}
		out = append(out, corpusFixturePacket{
			DataB64:    base64.StdEncoding.EncodeToString(pkt),
			FinalRange: finalRange,
		})
	}
	if len(out) == 0 {
		return nil, nil, fmt.Errorf("no packets in %s", path)
	}
	return out, modeHist, nil
}

func corpusDecodeOpusDemoF32(path string) ([]float32, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	if len(raw)%4 != 0 {
		return nil, nil, fmt.Errorf("decoded f32 payload length must be multiple of 4, got %d", len(raw))
	}
	samples := make([]float32, len(raw)/4)
	for i := range samples {
		bits := binary.LittleEndian.Uint32(raw[i*4 : i*4+4])
		samples[i] = math.Float32frombits(bits)
	}
	return samples, raw, nil
}

func corpusWriteRawFloat32(path string, samples []float32) error {
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

func corpusFrameSizeArg(frameSize int) (string, error) {
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
		return "", fmt.Errorf("unsupported frame size for corpus fixture: %d", frameSize)
	}
}

func runCorpusCase(opusDemoPath, tmpDir string, c corpusCaseConfig) (corpusFixtureCase, error) {
	fsArg, err := corpusFrameSizeArg(c.FrameSize)
	if err != nil {
		return corpusFixtureCase{}, err
	}
	totalSamples := corpusFrameRuns * c.FrameSize * c.Channels
	signal, err := testsignal.GenerateCorpusSignal(c.SignalClass, corpusFixtureSampleRate, totalSamples, c.Channels)
	if err != nil {
		return corpusFixtureCase{}, fmt.Errorf("generate %s: %w", c.SignalClass, err)
	}

	inputPath := filepath.Join(tmpDir, c.Name+".f32")
	bitPath := filepath.Join(tmpDir, c.Name+".bit")
	decodedPath := filepath.Join(tmpDir, c.Name+".dec.f32")

	if err := corpusWriteRawFloat32(inputPath, signal); err != nil {
		return corpusFixtureCase{}, fmt.Errorf("write input: %w", err)
	}

	encArgs := []string{
		"-e", c.Application,
		strconv.Itoa(corpusFixtureSampleRate), strconv.Itoa(c.Channels), strconv.Itoa(c.Bitrate),
		"-f32", "-cbr", "-complexity", "10", "-bandwidth", c.Bandwidth, "-framesize", fsArg,
		inputPath, bitPath,
	}
	if out, err := exec.Command(opusDemoPath, encArgs...).CombinedOutput(); err != nil {
		return corpusFixtureCase{}, fmt.Errorf("opus_demo encode: %v (%s)", err, out)
	}

	packets, modeHist, err := corpusParseOpusDemoBitstream(bitPath)
	if err != nil {
		return corpusFixtureCase{}, fmt.Errorf("parse bitstream: %w", err)
	}

	decArgs := []string{"-d", strconv.Itoa(corpusFixtureSampleRate), strconv.Itoa(c.Channels), "-f32", bitPath, decodedPath}
	if out, err := exec.Command(opusDemoPath, decArgs...).CombinedOutput(); err != nil {
		return corpusFixtureCase{}, fmt.Errorf("opus_demo decode: %v (%s)", err, out)
	}

	_, decodedRaw, err := corpusDecodeOpusDemoF32(decodedPath)
	if err != nil {
		return corpusFixtureCase{}, fmt.Errorf("parse decoded f32: %w", err)
	}

	return corpusFixtureCase{
		Name:          c.Name,
		SignalClass:   c.SignalClass,
		Application:   c.Application,
		Bandwidth:     c.Bandwidth,
		FrameSize:     c.FrameSize,
		Channels:      c.Channels,
		Bitrate:       c.Bitrate,
		Frames:        len(packets),
		ModeHistogram: modeHist,
		SignalSHA256:  testsignal.HashFloat32LE(signal),
		Packets:       packets,
		DecodedLen:    len(decodedRaw) / 4,
		DecodedF32B64: base64.StdEncoding.EncodeToString(decodedRaw),
	}, nil
}

func corpusOutputPath() string {
	if v := os.Getenv(corpusOutputPathEnv); v != "" {
		return v
	}
	return corpusDefaultOutputPath
}

func main() {
	opusDemoPath, ok := libopustooling.FindOrEnsureOpusDemo(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots())
	if !ok {
		fmt.Fprintf(os.Stderr, "opus_demo not found; run: make ensure-libopus\n")
		os.Exit(1)
	}
	provenance, ok := libopustooling.LibopusBuildProvenanceForTool(opusDemoPath)
	if !ok {
		fmt.Fprintf(os.Stderr, "libopus build provenance not found for %s; run: make ensure-libopus\n", opusDemoPath)
		os.Exit(1)
	}

	// Corpus cases: one case per (signal_class × codec_config) combination.
	// Bitrates:
	//   low bitrate:  SILK 6 kbps, 8 kbps, 12 kbps  → exercises SILK DTX / sparse packets
	//   high bitrate: CELT 128 kbps, 192 kbps        → exercises CELT at max quality
	// Modes deliberately chosen to cover SILK, CELT, and Hybrid for each class.
	cases := []corpusCaseConfig{
		// --- clean speech ---
		// SILK low-bitrate mono (6 kbps): bottom of usable speech range
		{Name: "speech-silk-nb-20ms-mono-6k", SignalClass: testsignal.CorpusCleanSpeechV1, Application: "restricted-silk", Bandwidth: "NB", FrameSize: 960, Channels: 1, Bitrate: 6000},
		// SILK low-bitrate mono (8 kbps)
		{Name: "speech-silk-nb-20ms-mono-8k", SignalClass: testsignal.CorpusCleanSpeechV1, Application: "restricted-silk", Bandwidth: "NB", FrameSize: 960, Channels: 1, Bitrate: 8000},
		// SILK mid-bitrate mono (12 kbps)
		{Name: "speech-silk-nb-20ms-mono-12k", SignalClass: testsignal.CorpusCleanSpeechV1, Application: "restricted-silk", Bandwidth: "NB", FrameSize: 960, Channels: 1, Bitrate: 12000},
		// SILK wideband stereo
		{Name: "speech-silk-wb-20ms-stereo-32k", SignalClass: testsignal.CorpusCleanSpeechV1, Application: "restricted-silk", Bandwidth: "WB", FrameSize: 960, Channels: 2, Bitrate: 32000},
		// Hybrid (SILK+CELT) fullband mono
		{Name: "speech-hybrid-fb-20ms-mono-32k", SignalClass: testsignal.CorpusCleanSpeechV1, Application: "audio", Bandwidth: "FB", FrameSize: 960, Channels: 1, Bitrate: 32000},

		// --- music ---
		// CELT high-bitrate mono
		{Name: "music-celt-fb-20ms-mono-128k", SignalClass: testsignal.CorpusMusicV1, Application: "restricted-celt", Bandwidth: "FB", FrameSize: 960, Channels: 1, Bitrate: 128000},
		// CELT high-bitrate stereo
		{Name: "music-celt-fb-20ms-stereo-192k", SignalClass: testsignal.CorpusMusicV1, Application: "restricted-celt", Bandwidth: "FB", FrameSize: 960, Channels: 2, Bitrate: 192000},
		// CELT low-bitrate stereo (pushes CELT to use fewer bits per frame)
		{Name: "music-celt-fb-20ms-stereo-32k", SignalClass: testsignal.CorpusMusicV1, Application: "restricted-celt", Bandwidth: "FB", FrameSize: 960, Channels: 2, Bitrate: 32000},
		// Hybrid music at moderate bitrate
		{Name: "music-hybrid-fb-20ms-stereo-64k", SignalClass: testsignal.CorpusMusicV1, Application: "audio", Bandwidth: "FB", FrameSize: 960, Channels: 2, Bitrate: 64000},

		// --- mixed speech+music ---
		// Hybrid mono (auto mode will select Hybrid for mixed at this rate)
		{Name: "mixed-hybrid-fb-20ms-mono-48k", SignalClass: testsignal.CorpusMixedV1, Application: "audio", Bandwidth: "FB", FrameSize: 960, Channels: 1, Bitrate: 48000},
		// Hybrid stereo
		{Name: "mixed-hybrid-swb-20ms-stereo-64k", SignalClass: testsignal.CorpusMixedV1, Application: "audio", Bandwidth: "SWB", FrameSize: 960, Channels: 2, Bitrate: 64000},
		// CELT high-bitrate mixed
		{Name: "mixed-celt-fb-20ms-mono-128k", SignalClass: testsignal.CorpusMixedV1, Application: "restricted-celt", Bandwidth: "FB", FrameSize: 960, Channels: 1, Bitrate: 128000},

		// --- noise ---
		// CELT fullband noise (entropy coder stress)
		{Name: "noise-celt-fb-20ms-mono-64k", SignalClass: testsignal.CorpusWhiteNoiseV1, Application: "restricted-celt", Bandwidth: "FB", FrameSize: 960, Channels: 1, Bitrate: 64000},
		{Name: "noise-celt-fb-20ms-stereo-128k", SignalClass: testsignal.CorpusWhiteNoiseV1, Application: "restricted-celt", Bandwidth: "FB", FrameSize: 960, Channels: 2, Bitrate: 128000},
		// SILK noise (unusual path for SILK)
		{Name: "noise-silk-wb-20ms-mono-32k", SignalClass: testsignal.CorpusWhiteNoiseV1, Application: "restricted-silk", Bandwidth: "WB", FrameSize: 960, Channels: 1, Bitrate: 32000},

		// --- transient / castanet ---
		// CELT short frame (2.5 ms) for transients — CELT mode is best here
		{Name: "transient-celt-fb-2p5ms-mono-64k", SignalClass: testsignal.CorpusCastanetTransientV1, Application: "restricted-celt", Bandwidth: "FB", FrameSize: 120, Channels: 1, Bitrate: 64000},
		{Name: "transient-celt-fb-10ms-stereo-128k", SignalClass: testsignal.CorpusCastanetTransientV1, Application: "restricted-celt", Bandwidth: "FB", FrameSize: 480, Channels: 2, Bitrate: 128000},
		// High-bitrate transient
		{Name: "transient-celt-fb-20ms-mono-128k", SignalClass: testsignal.CorpusCastanetTransientV1, Application: "restricted-celt", Bandwidth: "FB", FrameSize: 960, Channels: 1, Bitrate: 128000},

		// --- pure tone ---
		// CELT mono (pure sinusoid)
		{Name: "puretone-celt-fb-20ms-mono-64k", SignalClass: testsignal.CorpusPureToneV1, Application: "restricted-celt", Bandwidth: "FB", FrameSize: 960, Channels: 1, Bitrate: 64000},
		{Name: "puretone-celt-fb-20ms-stereo-128k", SignalClass: testsignal.CorpusPureToneV1, Application: "restricted-celt", Bandwidth: "FB", FrameSize: 960, Channels: 2, Bitrate: 128000},
		// SILK with pure tone (out-of-domain for SILK, exercises limits)
		{Name: "puretone-silk-wb-20ms-mono-32k", SignalClass: testsignal.CorpusPureToneV1, Application: "restricted-silk", Bandwidth: "WB", FrameSize: 960, Channels: 1, Bitrate: 32000},

		// --- near-silence ---
		// CELT near-silence at very low bitrate
		{Name: "silence-celt-fb-20ms-mono-8k", SignalClass: testsignal.CorpusNearSilenceV1, Application: "restricted-celt", Bandwidth: "FB", FrameSize: 960, Channels: 1, Bitrate: 8000},
		{Name: "silence-silk-nb-20ms-mono-6k", SignalClass: testsignal.CorpusNearSilenceV1, Application: "restricted-silk", Bandwidth: "NB", FrameSize: 960, Channels: 1, Bitrate: 6000},
		{Name: "silence-celt-fb-20ms-stereo-128k", SignalClass: testsignal.CorpusNearSilenceV1, Application: "restricted-celt", Bandwidth: "FB", FrameSize: 960, Channels: 2, Bitrate: 128000},
	}

	tmpDir, err := os.MkdirTemp("", "gopus-corpus-fixture-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create temp dir:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	fixture := corpusFixtureFile{
		Version:    1,
		SampleRate: corpusFixtureSampleRate,
		Generator:  fmt.Sprintf("gen_corpus_decoder_parity_fixture via opus_demo opus-%s", libopustooling.DefaultVersion),
		Provenance: provenance,
		Cases:      make([]corpusFixtureCase, 0, len(cases)),
	}

	for _, c := range cases {
		fmt.Fprintf(os.Stderr, "generating %s...\n", c.Name)
		fc, err := runCorpusCase(opusDemoPath, tmpDir, c)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed %s: %v\n", c.Name, err)
			os.Exit(1)
		}
		fixture.Cases = append(fixture.Cases, fc)
	}

	sort.Slice(fixture.Cases, func(i, j int) bool {
		return fixture.Cases[i].Name < fixture.Cases[j].Name
	})

	outPath := corpusOutputPath()
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
