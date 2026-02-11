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
	"github.com/thesyncim/gopus/internal/testsignal"
)

const (
	encoderVariantsFixtureSampleRate = 48000
	encoderVariantsRefQFixturePath   = "testvectors/testdata/encoder_compliance_libopus_ref_q.json"
	encoderVariantsOutputFixturePath = "testvectors/testdata/encoder_compliance_libopus_variants_fixture.json"
)

type encoderVariantsRefQFixtureFile struct {
	Version int                      `json:"version"`
	Cases   []encoderVariantsRefQRow `json:"cases"`
}

type encoderVariantsRefQRow struct {
	Mode      string  `json:"mode"`
	Bandwidth string  `json:"bandwidth"`
	FrameSize int     `json:"frame_size"`
	Channels  int     `json:"channels"`
	Bitrate   int     `json:"bitrate"`
	LibQ      float64 `json:"lib_q"`
}

type encoderVariantsFixtureFile struct {
	Version    int                        `json:"version"`
	SampleRate int                        `json:"sample_rate"`
	Generator  string                     `json:"generator"`
	Variants   []string                   `json:"variants"`
	Cases      []encoderVariantsFixtureTC `json:"cases"`
}

type encoderVariantsFixtureTC struct {
	Name          string                         `json:"name"`
	Variant       string                         `json:"variant"`
	Mode          string                         `json:"mode"`
	Bandwidth     string                         `json:"bandwidth"`
	FrameSize     int                            `json:"frame_size"`
	Channels      int                            `json:"channels"`
	Bitrate       int                            `json:"bitrate"`
	LibQ          float64                        `json:"lib_q"`
	SignalFrames  int                            `json:"signal_frames"`
	SignalSHA256  string                         `json:"signal_sha256"`
	Frames        int                            `json:"frames"`
	ModeHistogram map[string]int                 `json:"mode_histogram"`
	Packets       []encoderVariantsFixturePacket `json:"packets"`
}

type encoderVariantsFixturePacket struct {
	DataB64    string `json:"data_b64"`
	FinalRange uint32 `json:"final_range"`
}

func getVariantsOpusDemoPath() string {
	if p, ok := libopustooling.FindOrEnsureOpusDemo(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots()); ok {
		return p
	}
	return ""
}

func variantsModeFromTOC(toc byte) string {
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

func parseVariantsBitstream(path string) ([]encoderVariantsFixturePacket, map[string]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	out := make([]encoderVariantsFixturePacket, 0, 64)
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
			modeHist[variantsModeFromTOC(pkt[0])]++
		}
		out = append(out, encoderVariantsFixturePacket{
			DataB64:    base64.StdEncoding.EncodeToString(pkt),
			FinalRange: finalRange,
		})
	}
	if len(out) == 0 {
		return nil, nil, fmt.Errorf("no packets parsed from %s", path)
	}
	return out, modeHist, nil
}

func variantsFrameSizeArg(frameSize int) (string, error) {
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

func variantsAppFromMode(mode string) (string, error) {
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

func variantsBWArgFromFixture(bw string) (string, error) {
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

func loadVariantsRefQFixture() (encoderVariantsRefQFixtureFile, error) {
	data, err := os.ReadFile(encoderVariantsRefQFixturePath)
	if err != nil {
		return encoderVariantsRefQFixtureFile{}, err
	}
	var fixture encoderVariantsRefQFixtureFile
	if err := json.Unmarshal(data, &fixture); err != nil {
		return encoderVariantsRefQFixtureFile{}, err
	}
	return fixture, nil
}

func writeRawFloat32(path string, samples []float32) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var b [4]byte
	for _, s := range samples {
		binary.LittleEndian.PutUint32(b[:], math.Float32bits(s))
		if _, err := f.Write(b[:]); err != nil {
			return err
		}
	}
	return nil
}

func buildCaseName(row encoderVariantsRefQRow) string {
	frameMs := float64(row.FrameSize) * 1000.0 / float64(encoderVariantsFixtureSampleRate)
	if frameMs == float64(int(frameMs)) {
		return fmt.Sprintf("%s-%s-%.0fms-%s-%dk",
			strings.ToUpper(row.Mode),
			strings.ToUpper(row.Bandwidth),
			frameMs,
			channelLabel(row.Channels),
			row.Bitrate/1000,
		)
	}
	return fmt.Sprintf("%s-%s-%.1fms-%s-%dk",
		strings.ToUpper(row.Mode),
		strings.ToUpper(row.Bandwidth),
		frameMs,
		channelLabel(row.Channels),
		row.Bitrate/1000,
	)
}

func channelLabel(ch int) string {
	if ch == 1 {
		return "mono"
	}
	return "stereo"
}

func runVariantsCase(opusDemoPath, tmpDir string, row encoderVariantsRefQRow, variant string) (encoderVariantsFixtureTC, error) {
	app, err := variantsAppFromMode(row.Mode)
	if err != nil {
		return encoderVariantsFixtureTC{}, err
	}
	bwArg, err := variantsBWArgFromFixture(row.Bandwidth)
	if err != nil {
		return encoderVariantsFixtureTC{}, err
	}
	frameArg, err := variantsFrameSizeArg(row.FrameSize)
	if err != nil {
		return encoderVariantsFixtureTC{}, err
	}

	signalFrames := encoderVariantsFixtureSampleRate / row.FrameSize
	totalSamples := signalFrames * row.FrameSize * row.Channels
	signal, err := testsignal.GenerateEncoderSignalVariant(variant, encoderVariantsFixtureSampleRate, totalSamples, row.Channels)
	if err != nil {
		return encoderVariantsFixtureTC{}, err
	}
	signalHash := testsignal.HashFloat32LE(signal)

	key := fmt.Sprintf("%s_%s_%d_%d_%d_%s",
		strings.ToLower(row.Mode),
		strings.ToLower(row.Bandwidth),
		row.FrameSize,
		row.Channels,
		row.Bitrate,
		strings.ToLower(variant),
	)
	inputPath := filepath.Join(tmpDir, key+".f32")
	bitPath := filepath.Join(tmpDir, key+".bit")
	if err := writeRawFloat32(inputPath, signal); err != nil {
		return encoderVariantsFixtureTC{}, fmt.Errorf("write input: %w", err)
	}

	args := []string{
		"-e", app, fmt.Sprintf("%d", encoderVariantsFixtureSampleRate), fmt.Sprintf("%d", row.Channels), fmt.Sprintf("%d", row.Bitrate),
		"-f32", "-cbr", "-complexity", "10", "-bandwidth", bwArg, "-framesize", frameArg,
		inputPath, bitPath,
	}
	if out, err := exec.Command(opusDemoPath, args...).CombinedOutput(); err != nil {
		return encoderVariantsFixtureTC{}, fmt.Errorf("opus_demo encode failed: %v (%s)", err, out)
	}

	packets, modeHist, err := parseVariantsBitstream(bitPath)
	if err != nil {
		return encoderVariantsFixtureTC{}, err
	}

	return encoderVariantsFixtureTC{
		Name:          buildCaseName(row),
		Variant:       variant,
		Mode:          strings.ToLower(row.Mode),
		Bandwidth:     strings.ToLower(row.Bandwidth),
		FrameSize:     row.FrameSize,
		Channels:      row.Channels,
		Bitrate:       row.Bitrate,
		LibQ:          row.LibQ,
		SignalFrames:  signalFrames,
		SignalSHA256:  signalHash,
		Frames:        len(packets),
		ModeHistogram: modeHist,
		Packets:       packets,
	}, nil
}

func main() {
	opusDemoPath := getVariantsOpusDemoPath()
	if opusDemoPath == "" {
		fmt.Fprintln(os.Stderr, "opus_demo not found. expected tmp_check/opus-1.6.1/opus_demo (run: make ensure-libopus)")
		os.Exit(1)
	}

	refFixture, err := loadVariantsRefQFixture()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load ref fixture: %v\n", err)
		os.Exit(1)
	}

	tmpDir, err := os.MkdirTemp("", "gopus-enc-variants-fixture-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	variants := testsignal.EncoderSignalVariants()
	out := encoderVariantsFixtureFile{
		Version:    1,
		SampleRate: encoderVariantsFixtureSampleRate,
		Generator:  opusDemoPath,
		Variants:   variants,
		Cases:      make([]encoderVariantsFixtureTC, 0, len(refFixture.Cases)*len(variants)),
	}

	for _, variant := range variants {
		for _, row := range refFixture.Cases {
			fmt.Fprintf(os.Stderr, "generating %s variant=%s fs=%d ch=%d br=%d...\n",
				row.Mode, variant, row.FrameSize, row.Channels, row.Bitrate)
			tc, err := runVariantsCase(opusDemoPath, tmpDir, row, variant)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed %s/%s fs=%d ch=%d br=%d: %v\n",
					row.Mode, variant, row.FrameSize, row.Channels, row.Bitrate, err)
				os.Exit(1)
			}
			out.Cases = append(out.Cases, tc)
		}
	}

	sort.Slice(out.Cases, func(i, j int) bool {
		a, b := out.Cases[i], out.Cases[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Variant < b.Variant
	})

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal fixture: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(encoderVariantsOutputFixturePath, append(data, '\n'), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write fixture: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "wrote %s (%d cases, %d variants)\n",
		encoderVariantsOutputFixturePath,
		len(out.Cases),
		len(variants),
	)
}
