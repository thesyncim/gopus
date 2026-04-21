package testvectors

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

const libopusDecoderMatrixFixturePath = "testdata/libopus_decoder_matrix_fixture.json"
const libopusDecoderMatrixFixturePathAMD64 = "testdata/libopus_decoder_matrix_fixture_amd64.json"

type libopusDecoderMatrixFixtureFile struct {
	Version    int                            `json:"version"`
	SampleRate int                            `json:"sample_rate"`
	Generator  string                         `json:"generator"`
	Signal     string                         `json:"signal"`
	Cases      []libopusDecoderMatrixCaseFile `json:"cases"`
}

type libopusDecoderMatrixCaseFile struct {
	Name          string                       `json:"name"`
	Application   string                       `json:"application"`
	Bandwidth     string                       `json:"bandwidth"`
	FrameSize     int                          `json:"frame_size"`
	Channels      int                          `json:"channels"`
	Bitrate       int                          `json:"bitrate"`
	Frames        int                          `json:"frames"`
	ModeHistogram map[string]int               `json:"mode_histogram"`
	Packets       []libopusDecoderMatrixPacket `json:"packets"`
	DecodedLen    int                          `json:"decoded_len"`
	DecodedF32B64 string                       `json:"decoded_f32_le_b64"`

	decodedPackets [][]byte
	decodedSamples []float32
}

type libopusDecoderMatrixPacket struct {
	DataB64    string `json:"data_b64"`
	FinalRange uint32 `json:"final_range"`
}

var (
	libopusDecoderMatrixFixtureOnce sync.Once
	libopusDecoderMatrixFixtureData libopusDecoderMatrixFixtureFile
	libopusDecoderMatrixFixtureErr  error
)

func libopusDecoderMatrixFixturePathForArch() string {
	if runtime.GOARCH == "amd64" {
		return libopusDecoderMatrixFixturePathAMD64
	}
	return libopusDecoderMatrixFixturePath
}

func loadLibopusDecoderMatrixFixture() (libopusDecoderMatrixFixtureFile, error) {
	libopusDecoderMatrixFixtureOnce.Do(func() {
		data, err := os.ReadFile(filepath.Join(libopusDecoderMatrixFixturePathForArch()))
		if err != nil {
			libopusDecoderMatrixFixtureErr = err
			return
		}
		var fixture libopusDecoderMatrixFixtureFile
		if err := json.Unmarshal(data, &fixture); err != nil {
			libopusDecoderMatrixFixtureErr = err
			return
		}
		for i := range fixture.Cases {
			c := &fixture.Cases[i]
			if c.Frames != len(c.Packets) {
				libopusDecoderMatrixFixtureErr = errors.New("decoder matrix fixture frame count mismatch")
				return
			}
			c.decodedPackets = make([][]byte, len(c.Packets))
			for j := range c.Packets {
				payload, err := base64.StdEncoding.DecodeString(c.Packets[j].DataB64)
				if err != nil {
					libopusDecoderMatrixFixtureErr = err
					return
				}
				c.decodedPackets[j] = payload
			}
			raw, err := base64.StdEncoding.DecodeString(c.DecodedF32B64)
			if err != nil {
				libopusDecoderMatrixFixtureErr = err
				return
			}
			if len(raw)%4 != 0 {
				libopusDecoderMatrixFixtureErr = errors.New("decoded f32 payload length must be multiple of 4")
				return
			}
			if c.DecodedLen != len(raw)/4 {
				libopusDecoderMatrixFixtureErr = errors.New("decoded f32 payload length metadata mismatch")
				return
			}
			c.decodedSamples = make([]float32, len(raw)/4)
			for j := range c.decodedSamples {
				bits := binary.LittleEndian.Uint32(raw[j*4 : j*4+4])
				c.decodedSamples[j] = math.Float32frombits(bits)
			}
		}
		libopusDecoderMatrixFixtureData = fixture
	})
	return libopusDecoderMatrixFixtureData, libopusDecoderMatrixFixtureErr
}

func decodeLibopusDecoderMatrixPackets(c libopusDecoderMatrixCaseFile) ([][]byte, error) {
	return append([][]byte(nil), c.decodedPackets...), nil
}

func decodeLibopusDecoderMatrixSamples(c libopusDecoderMatrixCaseFile) ([]float32, error) {
	return c.decodedSamples, nil
}
