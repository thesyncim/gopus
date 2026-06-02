//go:build ignore

// gen_realcontent_corpus_fixture extracts small REAL-CONTENT source PCM clips
// from the official RFC 6716 / RFC 8251 Opus test vectors and writes them to a
// committed fixture used by the real-content corpus parity gate
// (testvectors/realcontent_corpus_quality_parity_test.go).
//
// Unlike the synthetic corpus (internal/testsignal.CorpusSignalClasses, which is
// algorithmically generated), these clips are decoded reference PCM from the
// canonical opus-codec.org conformance vectors — i.e. real speech and music
// recordings as decoded by the reference Opus decoder. They are the closest
// public-domain "real-world content" available inside the repo without pulling
// in new third-party media.
//
// The fixture stores ONLY the real-content INPUT PCM (int16 LE, base64) plus
// provenance (source vector, sample offset, sha256). It deliberately stores no
// libopus packets or decoded output: the corpus gate encodes each clip live with
// gopus and decodes it with BOTH gopus and the tier-matched libopus reference, so
// the libopus oracle (never gopus's own output) is the parity reference. Freezing
// only the input keeps the fixture small and the reference live.
//
// Provenance: the .dec reference outputs are produced by the reference decoder
// from the opus-codec.org rfc8251 test-vector archive (the same archive the
// existing RFC conformance gate downloads). They are gitignored runtime cache;
// this generator reads whatever copy is already present under
// testvectors/testdata/opus_testvectors and records each clip's source so the
// derivation is reproducible.
//
// Usage:
//
//	go run tools/gen_realcontent_corpus_fixture.go \
//	    [GOPUS_REALCONTENT_FIXTURE_OUT=testvectors/testdata/realcontent_corpus_fixture.json]
package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

const (
	realcontentSampleRate     = 48000
	realcontentDefaultOutPath = "testvectors/testdata/realcontent_corpus_fixture.json"
	realcontentOutPathEnv     = "GOPUS_REALCONTENT_FIXTURE_OUT"
	realcontentVectorDir      = "testvectors/testdata/opus_testvectors"
	// Clip length per case (stereo frames @ 48 kHz). 0.7 s comfortably exceeds
	// opus_compare's per-band-model minimum while keeping the fixture small.
	realcontentClipFrames = 33600 // 0.7 s @ 48 kHz
	// Active-window search granularity.
	realcontentSearchHop = 4800 // 0.1 s
)

type realcontentFixtureFile struct {
	Version    int                                   `json:"version"`
	SampleRate int                                   `json:"sample_rate"`
	Generator  string                                `json:"generator"`
	Note       string                                `json:"note"`
	Source     realcontentSourceInfo                 `json:"source"`
	Provenance libopustooling.LibopusBuildProvenance `json:"provenance"`
	Cases      []realcontentFixtureCase              `json:"cases"`
}

// realcontentSourceInfo records where the real-content material originates so the
// derivation is auditable: the canonical opus-codec.org conformance vectors.
type realcontentSourceInfo struct {
	Origin      string `json:"origin"`
	Archive     string `json:"archive"`
	Description string `json:"description"`
}

type realcontentFixtureCase struct {
	Name        string `json:"name"`
	ContentKind string `json:"content_kind"`
	SourceFile  string `json:"source_file"`
	// FrameOffset/Frames describe the extracted window in the source .dec file,
	// counted in stereo frames (one frame = one L+R int16 pair).
	FrameOffset int `json:"frame_offset"`
	Frames      int `json:"frames"`
	Channels    int `json:"channels"`
	// RMS/Crest/StereoCorr/ZCR characterize the clip so the content kind is
	// auditable from the fixture alone.
	RMS        float64 `json:"rms"`
	Crest      float64 `json:"crest"`
	StereoCorr float64 `json:"stereo_corr"`
	ZCR        float64 `json:"zcr"`
	PCMSHA256  string  `json:"pcm_sha256"`
	// PCMS16LEB64 is the extracted interleaved-stereo int16 LE PCM (the source
	// clip). Mono cases are downmixed at load time by the test.
	PCMS16LEB64 string `json:"pcm_s16le_b64"`
	// LibopusPackets holds libopus opus_demo encodes of this real clip for the
	// decode-of-libopus-packets lane. Only the packet bytes (+ final_range) are
	// frozen; the decode reference is produced live and tier-matched at gate time,
	// so it is always a libopus reference (never gopus output). Empty if opus_demo
	// was unavailable when the fixture was generated.
	LibopusPackets []realcontentLibopusEncode `json:"libopus_packets,omitempty"`
}

// realcontentLibopusEncode is one libopus opus_demo encode of a real clip.
type realcontentLibopusEncode struct {
	Name          string                     `json:"name"`
	Application   string                     `json:"application"`
	Bandwidth     string                     `json:"bandwidth"`
	FrameSize     int                        `json:"frame_size"`
	Channels      int                        `json:"channels"`
	Bitrate       int                        `json:"bitrate"`
	ModeHistogram map[string]int             `json:"mode_histogram"`
	Packets       []realcontentLibopusPacket `json:"packets"`
}

type realcontentLibopusPacket struct {
	DataB64    string `json:"data_b64"`
	FinalRange uint32 `json:"final_range"`
}

// libopusEncodeConfig is one opus_demo encode pass over a clip.
type libopusEncodeConfig struct {
	name        string
	application string
	bandwidth   string
	frameSize   int
	channels    int
	bitrate     int
}

// libopusEncodeConfigs covers SILK / Hybrid / CELT across mono and stereo so the
// decode-of-libopus-packets lane exercises all three modes on real content. Kept
// small to bound fixture size and generation time; the live encode→decode gate
// covers the wider matrix.
var libopusEncodeConfigs = []libopusEncodeConfig{
	{name: "silk-wb-mono-24k", application: "restricted-silk", bandwidth: "WB", frameSize: 960, channels: 1, bitrate: 24000},
	// voip + SWB reliably selects Hybrid (SILK+CELT) across these clips; the
	// coverage gate enforces that all three modes actually appear.
	{name: "hybrid-swb-stereo-32k", application: "voip", bandwidth: "SWB", frameSize: 960, channels: 2, bitrate: 32000},
	{name: "celt-fb-stereo-128k", application: "restricted-celt", bandwidth: "FB", frameSize: 960, channels: 2, bitrate: 128000},
}

// clipSpec selects a content clip from a source vector.
type clipSpec struct {
	name        string
	contentKind string
	sourceFile  string
}

// clipSpecs picks representative real-content clips: real music (sustained and
// dense), wide-stereo mixed content, and several speech talkers. Content kinds
// are derived from the per-vector signal statistics measured on the .dec files.
var clipSpecs = []clipSpec{
	{name: "music_sustained", contentKind: "music", sourceFile: "testvector10"},
	{name: "music_dense_wide", contentKind: "music", sourceFile: "testvector11"},
	{name: "mixed_wide_stereo", contentKind: "mixed", sourceFile: "testvector01"},
	{name: "speech_talker_a", contentKind: "speech", sourceFile: "testvector02"},
	{name: "speech_talker_b", contentKind: "speech", sourceFile: "testvector05"},
	{name: "speech_talker_c", contentKind: "speech", sourceFile: "testvector08"},
}

func readDecStereo(path string) ([]int16, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data)%4 != 0 {
		return nil, fmt.Errorf("%s: length %d not a multiple of 4 (stereo int16)", path, len(data))
	}
	n := len(data) / 2
	out := make([]int16, n)
	for i := 0; i < n; i++ {
		out[i] = int16(binary.LittleEndian.Uint16(data[i*2 : i*2+2]))
	}
	return out, nil
}

// windowEnergy returns the summed |L|+|R| amplitude over [frame, frame+frames).
func windowEnergy(pcm []int16, frame, frames int) float64 {
	var e float64
	start := frame * 2
	end := (frame + frames) * 2
	if end > len(pcm) {
		end = len(pcm)
	}
	for i := start; i < end; i++ {
		v := float64(pcm[i])
		if v < 0 {
			v = -v
		}
		e += v
	}
	return e
}

// pickActiveWindow scans the source for the most energetic clip-length window so
// the extracted clip is content-rich rather than mostly silence.
func pickActiveWindow(pcm []int16, frames int) int {
	total := len(pcm) / 2
	if total <= frames {
		return 0
	}
	best, bestE := 0, -1.0
	for f := 0; f+frames <= total; f += realcontentSearchHop {
		e := windowEnergy(pcm, f, frames)
		if e > bestE {
			bestE, best = e, f
		}
	}
	return best
}

func clipStats(clip []int16) (rms, crest, stereoCorr, zcr float64) {
	frames := len(clip) / 2
	if frames == 0 {
		return
	}
	var sumsq, peak float64
	var sl, sr, sll, srr, slr float64
	for f := 0; f < frames; f++ {
		l := float64(clip[f*2]) / 32768.0
		r := float64(clip[f*2+1]) / 32768.0
		sumsq += l*l + r*r
		if math.Abs(l) > peak {
			peak = math.Abs(l)
		}
		if math.Abs(r) > peak {
			peak = math.Abs(r)
		}
		sl += l
		sr += r
		sll += l * l
		srr += r * r
		slr += l * r
	}
	rms = math.Sqrt(sumsq / float64(frames*2))
	if rms > 0 {
		crest = peak / rms
	}
	ml, mr := sl/float64(frames), sr/float64(frames)
	covlr := slr/float64(frames) - ml*mr
	varl := sll/float64(frames) - ml*ml
	varr := srr/float64(frames) - mr*mr
	if varl > 0 && varr > 0 {
		stereoCorr = covlr / math.Sqrt(varl*varr)
	}
	zc := 0
	for f := 1; f < frames; f++ {
		if (clip[f*2] >= 0) != (clip[(f-1)*2] >= 0) {
			zc++
		}
	}
	zcr = float64(zc) / float64(frames)
	return
}

func encodeS16LE(clip []int16) string {
	buf := make([]byte, len(clip)*2)
	for i, s := range clip {
		binary.LittleEndian.PutUint16(buf[i*2:i*2+2], uint16(s))
	}
	return base64.StdEncoding.EncodeToString(buf)
}

func clip16ToBytes(clip []int16) []byte {
	buf := make([]byte, len(clip)*2)
	for i, s := range clip {
		binary.LittleEndian.PutUint16(buf[i*2:i*2+2], uint16(s))
	}
	return buf
}

// monoDownmixS16 averages a stereo int16 clip to mono int16.
func monoDownmixS16(stereo []int16) []int16 {
	frames := len(stereo) / 2
	out := make([]int16, frames)
	for f := 0; f < frames; f++ {
		out[f] = int16((int32(stereo[f*2]) + int32(stereo[f*2+1])) / 2)
	}
	return out
}

func realcontentFrameSizeArg(frameSize int) (string, error) {
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
		return "", fmt.Errorf("unsupported frame size %d", frameSize)
	}
}

func realcontentModeFromTOC(toc byte) string {
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

// parseOpusDemoBitstream parses opus_demo's length+final_range framed output.
func parseOpusDemoBitstream(path string) ([]realcontentLibopusPacket, map[string]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var out []realcontentLibopusPacket
	hist := map[string]int{"silk": 0, "hybrid": 0, "celt": 0}
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
			hist[realcontentModeFromTOC(pkt[0])]++
		}
		out = append(out, realcontentLibopusPacket{
			DataB64:    base64.StdEncoding.EncodeToString(pkt),
			FinalRange: finalRange,
		})
	}
	if len(out) == 0 {
		return nil, nil, fmt.Errorf("no packets in %s", path)
	}
	return out, hist, nil
}

// libopusEncodeClip encodes a clip with opus_demo and returns the framed packets.
func libopusEncodeClip(opusDemoPath, tmpDir, caseName string, stereoClip []int16, cfg libopusEncodeConfig) (realcontentLibopusEncode, error) {
	fsArg, err := realcontentFrameSizeArg(cfg.frameSize)
	if err != nil {
		return realcontentLibopusEncode{}, err
	}
	clip := stereoClip
	if cfg.channels == 1 {
		clip = monoDownmixS16(stereoClip)
	}
	inputPath := filepath.Join(tmpDir, caseName+"-"+cfg.name+".sw")
	bitPath := filepath.Join(tmpDir, caseName+"-"+cfg.name+".bit")
	if err := os.WriteFile(inputPath, clip16ToBytes(clip), 0o644); err != nil {
		return realcontentLibopusEncode{}, fmt.Errorf("write input: %w", err)
	}
	encArgs := []string{
		"-e", cfg.application,
		strconv.Itoa(realcontentSampleRate), strconv.Itoa(cfg.channels), strconv.Itoa(cfg.bitrate),
		"-cbr", "-complexity", "10", "-bandwidth", cfg.bandwidth, "-framesize", fsArg,
		inputPath, bitPath,
	}
	if out, err := exec.Command(opusDemoPath, encArgs...).CombinedOutput(); err != nil {
		return realcontentLibopusEncode{}, fmt.Errorf("opus_demo encode: %v (%s)", err, out)
	}
	packets, hist, err := parseOpusDemoBitstream(bitPath)
	if err != nil {
		return realcontentLibopusEncode{}, fmt.Errorf("parse bitstream: %w", err)
	}
	return realcontentLibopusEncode{
		Name:          cfg.name,
		Application:   cfg.application,
		Bandwidth:     cfg.bandwidth,
		FrameSize:     cfg.frameSize,
		Channels:      cfg.channels,
		Bitrate:       cfg.bitrate,
		ModeHistogram: hist,
		Packets:       packets,
	}, nil
}

func main() {
	outPath := os.Getenv(realcontentOutPathEnv)
	if outPath == "" {
		outPath = realcontentDefaultOutPath
	}

	// opus_demo is required: the decode-of-libopus-packets lane freezes libopus
	// encodes of each real clip, so the input bitstreams are reproducible.
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

	tmpDir, err := os.MkdirTemp("", "realcontent-corpus-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "mkdir temp: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	fixture := realcontentFixtureFile{
		Version:    1,
		SampleRate: realcontentSampleRate,
		Generator:  "tools/gen_realcontent_corpus_fixture.go (opus-" + libopustooling.DefaultVersion + " opus_demo)",
		Note: "Real-content (not synthetic) source PCM clips derived from the " +
			"official RFC 6716/8251 Opus test-vector decoded reference outputs. " +
			"The corpus gate encodes these with gopus and gates decode parity " +
			"against the tier-matched libopus reference (never gopus's own output). " +
			"libopus_packets are libopus opus_demo encodes of the same clips for the " +
			"decode-of-libopus-packets lane (decode reference produced live at gate time).",
		Source: realcontentSourceInfo{
			Origin:  "opus-codec.org RFC 6716 / RFC 8251 conformance test vectors",
			Archive: "https://opus-codec.org/docs/opus_testvectors-rfc8251.tar.gz",
			Description: "testvectorNN.dec are the reference-decoder outputs of the " +
				"conformance bitstreams (real speech and music recordings); these " +
				"clips are contiguous active windows extracted from those outputs.",
		},
		Provenance: provenance,
	}

	for _, spec := range clipSpecs {
		decPath := filepath.Join(realcontentVectorDir, spec.sourceFile+".dec")
		pcm, err := readDecStereo(decPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", spec.name, err)
			os.Exit(1)
		}
		frame := pickActiveWindow(pcm, realcontentClipFrames)
		clip := append([]int16(nil), pcm[frame*2:(frame+realcontentClipFrames)*2]...)

		rms, crest, stereoCorr, zcr := clipStats(clip)
		sum := sha256.Sum256(int16sToBytes(clip))

		var libopusEncodes []realcontentLibopusEncode
		for _, cfg := range libopusEncodeConfigs {
			enc, err := libopusEncodeClip(opusDemoPath, tmpDir, spec.name, clip, cfg)
			if err != nil {
				fmt.Fprintf(os.Stderr, "encode %s/%s: %v\n", spec.name, cfg.name, err)
				os.Exit(1)
			}
			libopusEncodes = append(libopusEncodes, enc)
		}

		fixture.Cases = append(fixture.Cases, realcontentFixtureCase{
			Name:           spec.name,
			ContentKind:    spec.contentKind,
			SourceFile:     spec.sourceFile + ".dec",
			FrameOffset:    frame,
			Frames:         realcontentClipFrames,
			Channels:       2,
			RMS:            round4(rms),
			Crest:          round4(crest),
			StereoCorr:     round4(stereoCorr),
			ZCR:            round4(zcr),
			PCMSHA256:      hex.EncodeToString(sum[:]),
			PCMS16LEB64:    encodeS16LE(clip),
			LibopusPackets: libopusEncodes,
		})
		fmt.Printf("clip %-18s kind=%-6s src=%s offset=%d frames=%d rms=%.4f crest=%.1f stereoCorr=%.3f zcr=%.4f libopus_encodes=%d\n",
			spec.name, spec.contentKind, spec.sourceFile, frame, realcontentClipFrames, rms, crest, stereoCorr, zcr, len(libopusEncodes))
	}

	data, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", outPath, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d cases, %d bytes)\n", outPath, len(fixture.Cases), len(data))
}

func int16sToBytes(s []int16) []byte {
	b := make([]byte, len(s)*2)
	for i, v := range s {
		binary.LittleEndian.PutUint16(b[i*2:i*2+2], uint16(v))
	}
	return b
}

func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}
