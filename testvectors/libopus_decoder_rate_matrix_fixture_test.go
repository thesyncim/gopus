package testvectors

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

const libopusDecoderRateMatrixFixturePath = "testdata/libopus_decoder_rate_matrix_fixture.json"

// libopusDecoderRateMatrixFixtureFile is the top-level fixture produced by
// tools/gen_libopus_decoder_rate_matrix_fixture.go. It holds one case per
// (encode-config, api_rate) combination: the same encoded packets re-decoded
// at each of the five Opus API sample rates (8000, 12000, 16000, 24000, 48000
// Hz) using libopus 1.6.1 opus_demo -d <rate>.
type libopusDecoderRateMatrixFixtureFile struct {
	Version    int                                `json:"version"`
	Generator  string                             `json:"generator"`
	Provenance libopusFixtureProvenance           `json:"provenance"`
	Signal     string                             `json:"signal"`
	Cases      []libopusDecoderRateMatrixCaseFile `json:"cases"`
}

// libopusDecoderRateMatrixCaseFile is one (encode-config, api_rate) row.
type libopusDecoderRateMatrixCaseFile struct {
	Name          string                           `json:"name"`
	Application   string                           `json:"application"`
	Bandwidth     string                           `json:"bandwidth"`
	FrameSize     int                              `json:"frame_size"`
	Channels      int                              `json:"channels"`
	Bitrate       int                              `json:"bitrate"`
	Frames        int                              `json:"frames"`
	APIRate       int                              `json:"api_rate"`
	ModeHistogram map[string]int                   `json:"mode_histogram"`
	Packets       []libopusDecoderRateMatrixPacket `json:"packets"`
	DecodedLen    int                              `json:"decoded_len"`
	DecodedF32B64 string                           `json:"decoded_f32_le_b64"`

	decodedPackets [][]byte
	decodedSamples []float32
}

type libopusDecoderRateMatrixPacket struct {
	DataB64    string `json:"data_b64"`
	FinalRange uint32 `json:"final_range"`
}

var (
	libopusDecoderRateMatrixFixtureOnce sync.Once
	libopusDecoderRateMatrixFixtureData libopusDecoderRateMatrixFixtureFile
	libopusDecoderRateMatrixFixtureErr  error
)

func libopusDecoderRateMatrixFixturePathForArch() string {
	return platformFixtureReadPath(libopusDecoderRateMatrixFixturePath)
}

func loadLibopusDecoderRateMatrixFixture() (libopusDecoderRateMatrixFixtureFile, error) {
	libopusDecoderRateMatrixFixtureOnce.Do(func() {
		data, err := os.ReadFile(filepath.Join(libopusDecoderRateMatrixFixturePathForArch()))
		if err != nil {
			libopusDecoderRateMatrixFixtureErr = err
			return
		}
		var fixture libopusDecoderRateMatrixFixtureFile
		if err := json.Unmarshal(data, &fixture); err != nil {
			libopusDecoderRateMatrixFixtureErr = err
			return
		}
		if err := validateLibopusFixtureProvenance(fixture.Provenance); err != nil {
			libopusDecoderRateMatrixFixtureErr = err
			return
		}
		for i := range fixture.Cases {
			c := &fixture.Cases[i]
			if c.Frames != len(c.Packets) {
				libopusDecoderRateMatrixFixtureErr = errors.New("decoder rate matrix fixture frame count mismatch")
				return
			}
			c.decodedPackets = make([][]byte, len(c.Packets))
			for j := range c.Packets {
				payload, err := base64.StdEncoding.DecodeString(c.Packets[j].DataB64)
				if err != nil {
					libopusDecoderRateMatrixFixtureErr = err
					return
				}
				c.decodedPackets[j] = payload
			}
			raw, err := base64.StdEncoding.DecodeString(c.DecodedF32B64)
			if err != nil {
				libopusDecoderRateMatrixFixtureErr = err
				return
			}
			if len(raw)%4 != 0 {
				libopusDecoderRateMatrixFixtureErr = errors.New("decoded f32 payload length must be multiple of 4")
				return
			}
			if c.DecodedLen != len(raw)/4 {
				libopusDecoderRateMatrixFixtureErr = errors.New("decoded f32 payload length metadata mismatch")
				return
			}
			c.decodedSamples = make([]float32, len(raw)/4)
			for j := range c.decodedSamples {
				bits := binary.LittleEndian.Uint32(raw[j*4 : j*4+4])
				c.decodedSamples[j] = math.Float32frombits(bits)
			}
		}
		libopusDecoderRateMatrixFixtureData = fixture
	})
	return libopusDecoderRateMatrixFixtureData, libopusDecoderRateMatrixFixtureErr
}

func decodeLibopusDecoderRateMatrixPackets(c libopusDecoderRateMatrixCaseFile) ([][]byte, error) {
	return append([][]byte(nil), c.decodedPackets...), nil
}

func decodeLibopusDecoderRateMatrixSamples(c libopusDecoderRateMatrixCaseFile) ([]float32, error) {
	return c.decodedSamples, nil
}
