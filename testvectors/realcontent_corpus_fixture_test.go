package testvectors

// realcontent_corpus_fixture_test.go — loader for the REAL-CONTENT corpus
// fixture (testdata/realcontent_corpus_fixture.json).
//
// The fixture, produced by tools/gen_realcontent_corpus_fixture.go, holds small
// source PCM clips extracted from the official RFC 6716/8251 Opus test-vector
// decoded reference outputs — i.e. real speech and music recordings, NOT the
// synthetic signal classes in internal/testsignal. It stores only the input PCM
// plus provenance; the parity reference is produced live by libopus at gate time
// (never gopus's own output), so no codec output is frozen here.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync"
)

const realcontentFixturePath = "testdata/realcontent_corpus_fixture.json"

type realcontentFixtureFileData struct {
	Version    int                          `json:"version"`
	SampleRate int                          `json:"sample_rate"`
	Generator  string                       `json:"generator"`
	Note       string                       `json:"note"`
	Source     realcontentSourceData        `json:"source"`
	Provenance libopusFixtureProvenance     `json:"provenance"`
	Cases      []realcontentFixtureCaseData `json:"cases"`
}

type realcontentSourceData struct {
	Origin      string `json:"origin"`
	Archive     string `json:"archive"`
	Description string `json:"description"`
}

type realcontentFixtureCaseData struct {
	Name           string                         `json:"name"`
	ContentKind    string                         `json:"content_kind"`
	SourceFile     string                         `json:"source_file"`
	FrameOffset    int                            `json:"frame_offset"`
	Frames         int                            `json:"frames"`
	Channels       int                            `json:"channels"`
	RMS            float64                        `json:"rms"`
	Crest          float64                        `json:"crest"`
	StereoCorr     float64                        `json:"stereo_corr"`
	ZCR            float64                        `json:"zcr"`
	PCMSHA256      string                         `json:"pcm_sha256"`
	PCMS16LEB64    string                         `json:"pcm_s16le_b64"`
	LibopusPackets []realcontentLibopusEncodeData `json:"libopus_packets"`

	// stereo holds the decoded interleaved-stereo float32 clip (the source PCM).
	stereo []float32
}

type realcontentLibopusEncodeData struct {
	Name          string                         `json:"name"`
	Application   string                         `json:"application"`
	Bandwidth     string                         `json:"bandwidth"`
	FrameSize     int                            `json:"frame_size"`
	Channels      int                            `json:"channels"`
	Bitrate       int                            `json:"bitrate"`
	ModeHistogram map[string]int                 `json:"mode_histogram"`
	Packets       []realcontentLibopusPacketData `json:"packets"`

	decodedPackets [][]byte
}

type realcontentLibopusPacketData struct {
	DataB64    string `json:"data_b64"`
	FinalRange uint32 `json:"final_range"`
}

var (
	realcontentFixtureOnce sync.Once
	realcontentFixtureData realcontentFixtureFileData
	realcontentFixtureErr  error
)

func realcontentFixtureReadPath() string {
	return platformFixtureReadPath(realcontentFixturePath)
}

func loadRealcontentFixture() (realcontentFixtureFileData, error) {
	realcontentFixtureOnce.Do(func() {
		data, err := os.ReadFile(filepath.Join(realcontentFixtureReadPath()))
		if err != nil {
			realcontentFixtureErr = err
			return
		}
		var fixture realcontentFixtureFileData
		if err := json.Unmarshal(data, &fixture); err != nil {
			realcontentFixtureErr = err
			return
		}
		if fixture.SampleRate != 48000 {
			realcontentFixtureErr = errors.New("realcontent fixture: unsupported sample rate")
			return
		}
		if err := validateLibopusFixtureProvenance(fixture.Provenance); err != nil {
			realcontentFixtureErr = err
			return
		}
		for i := range fixture.Cases {
			c := &fixture.Cases[i]
			if c.Channels != 2 {
				realcontentFixtureErr = errors.New("realcontent fixture: case " + c.Name + " is not stereo")
				return
			}
			raw, err := base64.StdEncoding.DecodeString(c.PCMS16LEB64)
			if err != nil {
				realcontentFixtureErr = err
				return
			}
			if len(raw)%4 != 0 {
				realcontentFixtureErr = errors.New("realcontent fixture: case " + c.Name + " PCM length not a multiple of 4")
				return
			}
			if c.Frames*2*2 != len(raw) {
				realcontentFixtureErr = errors.New("realcontent fixture: case " + c.Name + " frame count mismatch")
				return
			}
			n := len(raw) / 2
			c.stereo = make([]float32, n)
			for j := 0; j < n; j++ {
				s := int16(binary.LittleEndian.Uint16(raw[j*2 : j*2+2]))
				c.stereo[j] = float32(s) / 32768.0
			}
			for k := range c.LibopusPackets {
				enc := &c.LibopusPackets[k]
				enc.decodedPackets = make([][]byte, len(enc.Packets))
				for p := range enc.Packets {
					payload, err := base64.StdEncoding.DecodeString(enc.Packets[p].DataB64)
					if err != nil {
						realcontentFixtureErr = err
						return
					}
					enc.decodedPackets[p] = payload
				}
			}
		}
		realcontentFixtureData = fixture
	})
	return realcontentFixtureData, realcontentFixtureErr
}

// realcontentMono returns the equal-power mono downmix of a stereo clip.
func realcontentMono(stereo []float32) []float32 {
	frames := len(stereo) / 2
	out := make([]float32, frames)
	for f := 0; f < frames; f++ {
		out[f] = 0.5 * (stereo[f*2] + stereo[f*2+1])
	}
	return out
}

// realcontentClipSHA256 recomputes the sha256 the generator recorded: the hash of
// the int16 LE bytes the clip was stored from. int16 values survive the
// float32 round-trip exactly (|s| <= 32768, a power-of-two scale), so the inverse
// is lossless and the digest matches the fixture's pcm_sha256.
func realcontentClipSHA256(stereo []float32) string {
	buf := make([]byte, len(stereo)*2)
	for i, f := range stereo {
		s := int16(math.Round(float64(f) * 32768.0))
		binary.LittleEndian.PutUint16(buf[i*2:i*2+2], uint16(s))
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}
