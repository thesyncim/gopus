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

const libopusDecoderMatrixFixturePath = "testdata/libopus_decoder_matrix_fixture.json"

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

func loadLibopusDecoderMatrixFixture() (libopusDecoderMatrixFixtureFile, error) {
	libopusDecoderMatrixFixtureOnce.Do(func() {
		data, err := os.ReadFile(filepath.Join(libopusDecoderMatrixFixturePath))
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
			if fixture.Cases[i].Frames != len(fixture.Cases[i].Packets) {
				libopusDecoderMatrixFixtureErr = errors.New("decoder matrix fixture frame count mismatch")
				return
			}
			for j := range fixture.Cases[i].Packets {
				if _, err := base64.StdEncoding.DecodeString(fixture.Cases[i].Packets[j].DataB64); err != nil {
					libopusDecoderMatrixFixtureErr = err
					return
				}
			}
			raw, err := base64.StdEncoding.DecodeString(fixture.Cases[i].DecodedF32B64)
			if err != nil {
				libopusDecoderMatrixFixtureErr = err
				return
			}
			if len(raw)%4 != 0 {
				libopusDecoderMatrixFixtureErr = errors.New("decoded f32 payload length must be multiple of 4")
				return
			}
			if fixture.Cases[i].DecodedLen != len(raw)/4 {
				libopusDecoderMatrixFixtureErr = errors.New("decoded f32 payload length metadata mismatch")
				return
			}
		}
		libopusDecoderMatrixFixtureData = fixture
	})
	return libopusDecoderMatrixFixtureData, libopusDecoderMatrixFixtureErr
}

func decodeLibopusDecoderMatrixPackets(c libopusDecoderMatrixCaseFile) ([][]byte, error) {
	packets := make([][]byte, len(c.Packets))
	for i := range c.Packets {
		payload, err := base64.StdEncoding.DecodeString(c.Packets[i].DataB64)
		if err != nil {
			return nil, err
		}
		packets[i] = payload
	}
	return packets, nil
}

func decodeLibopusDecoderMatrixSamples(c libopusDecoderMatrixCaseFile) ([]float32, error) {
	raw, err := base64.StdEncoding.DecodeString(c.DecodedF32B64)
	if err != nil {
		return nil, err
	}
	out := make([]float32, len(raw)/4)
	for i := range out {
		bits := binary.LittleEndian.Uint32(raw[i*4 : i*4+4])
		out[i] = math.Float32frombits(bits)
	}
	return out, nil
}
