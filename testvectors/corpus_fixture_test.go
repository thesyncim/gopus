package testvectors

// corpus_fixture_test.go — loader for the broader signal-class corpus fixture.
//
// The fixture at testdata/corpus_decoder_parity_fixture.json is produced by
// tools/gen_corpus_decoder_parity_fixture.go using the pinned libopus opus_demo
// encoder/decoder. It covers signal classes:
//   corpus_clean_speech_v1, corpus_music_v1, corpus_mixed_v1,
//   corpus_white_noise_v1, corpus_castanet_transient_v1,
//   corpus_pure_tone_v1, corpus_near_silence_v1
// across SILK / CELT / Hybrid, low-bitrate (6–12 kbps) and high-bitrate
// (128+ kbps), mono and stereo.

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync"
)

const corpusFixturePath = "testdata/corpus_decoder_parity_fixture.json"

type corpusFixtureFileData struct {
	Version    int                          `json:"version"`
	SampleRate int                          `json:"sample_rate"`
	Generator  string                       `json:"generator"`
	Provenance libopusFixtureProvenance     `json:"provenance"`
	Cases      []corpusFixtureCaseData      `json:"cases"`
}

type corpusFixtureCaseData struct {
	Name          string                    `json:"name"`
	SignalClass   string                    `json:"signal_class"`
	Application   string                    `json:"application"`
	Bandwidth     string                    `json:"bandwidth"`
	FrameSize     int                       `json:"frame_size"`
	Channels      int                       `json:"channels"`
	Bitrate       int                       `json:"bitrate"`
	Frames        int                       `json:"frames"`
	ModeHistogram map[string]int            `json:"mode_histogram"`
	SignalSHA256  string                    `json:"signal_sha256"`
	Packets       []corpusFixturePacketData `json:"packets"`
	DecodedLen    int                       `json:"decoded_len"`
	DecodedF32B64 string                    `json:"decoded_f32_le_b64"`

	decodedPackets [][]byte
	decodedSamples []float32
}

type corpusFixturePacketData struct {
	DataB64    string `json:"data_b64"`
	FinalRange uint32 `json:"final_range"`
}

var (
	corpusFixtureOnce sync.Once
	corpusFixtureData corpusFixtureFileData
	corpusFixtureErr  error
)

func corpusFixtureReadPath() string {
	return platformFixtureReadPath(corpusFixturePath)
}

func loadCorpusFixture() (corpusFixtureFileData, error) {
	corpusFixtureOnce.Do(func() {
		data, err := os.ReadFile(filepath.Join(corpusFixtureReadPath()))
		if err != nil {
			corpusFixtureErr = err
			return
		}
		var fixture corpusFixtureFileData
		if err := json.Unmarshal(data, &fixture); err != nil {
			corpusFixtureErr = err
			return
		}
		if err := validateLibopusFixtureProvenance(fixture.Provenance); err != nil {
			corpusFixtureErr = err
			return
		}
		for i := range fixture.Cases {
			c := &fixture.Cases[i]
			if c.Frames != len(c.Packets) {
				corpusFixtureErr = errors.New("corpus fixture frame count mismatch in case " + c.Name)
				return
			}
			c.decodedPackets = make([][]byte, len(c.Packets))
			for j := range c.Packets {
				payload, err := base64.StdEncoding.DecodeString(c.Packets[j].DataB64)
				if err != nil {
					corpusFixtureErr = err
					return
				}
				c.decodedPackets[j] = payload
			}
			raw, err := base64.StdEncoding.DecodeString(c.DecodedF32B64)
			if err != nil {
				corpusFixtureErr = err
				return
			}
			if len(raw)%4 != 0 {
				corpusFixtureErr = errors.New("corpus fixture decoded f32 payload length must be multiple of 4 in case " + c.Name)
				return
			}
			if c.DecodedLen != len(raw)/4 {
				corpusFixtureErr = errors.New("corpus fixture decoded f32 length metadata mismatch in case " + c.Name)
				return
			}
			c.decodedSamples = make([]float32, len(raw)/4)
			for j := range c.decodedSamples {
				bits := binary.LittleEndian.Uint32(raw[j*4 : j*4+4])
				c.decodedSamples[j] = math.Float32frombits(bits)
			}
		}
		corpusFixtureData = fixture
	})
	return corpusFixtureData, corpusFixtureErr
}
