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

const libopusDecoderLossFixturePath = "testdata/libopus_decoder_loss_fixture.json"
const libopusDecoderLossFixturePathAMD64 = "testdata/libopus_decoder_loss_fixture_amd64.json"

type libopusDecoderLossFixtureFile struct {
	Version    int                          `json:"version"`
	SampleRate int                          `json:"sample_rate"`
	Generator  string                       `json:"generator"`
	Signal     string                       `json:"signal"`
	Cases      []libopusDecoderLossCaseFile `json:"cases"`
	Patterns   []string                     `json:"patterns"`
}

type libopusDecoderLossCaseFile struct {
	Name        string                         `json:"name"`
	Application string                         `json:"application"`
	Bandwidth   string                         `json:"bandwidth"`
	FrameSize   int                            `json:"frame_size"`
	Channels    int                            `json:"channels"`
	Bitrate     int                            `json:"bitrate"`
	Frames      int                            `json:"frames"`
	Packets     []libopusDecoderLossPacketFile `json:"packets"`
	Results     []libopusDecoderLossResultFile `json:"results"`

	decodedPackets [][]byte
}

type libopusDecoderLossPacketFile struct {
	DataB64    string `json:"data_b64"`
	FinalRange uint32 `json:"final_range"`
}

type libopusDecoderLossResultFile struct {
	Pattern       string `json:"pattern"`
	LossBits      string `json:"loss_bits"`
	DecodedLen    int    `json:"decoded_len"`
	DecodedF32B64 string `json:"decoded_f32_le_b64"`

	parsedLossBits []bool
	decodedSamples []float32
}

var (
	libopusDecoderLossFixtureOnce sync.Once
	libopusDecoderLossFixtureData libopusDecoderLossFixtureFile
	libopusDecoderLossFixtureErr  error
)

func libopusDecoderLossFixturePathForArch() string {
	if runtime.GOARCH == "amd64" {
		return libopusDecoderLossFixturePathAMD64
	}
	return libopusDecoderLossFixturePath
}

func loadLibopusDecoderLossFixture() (libopusDecoderLossFixtureFile, error) {
	libopusDecoderLossFixtureOnce.Do(func() {
		data, err := os.ReadFile(filepath.Join(libopusDecoderLossFixturePathForArch()))
		if err != nil {
			libopusDecoderLossFixtureErr = err
			return
		}
		var fixture libopusDecoderLossFixtureFile
		if err := json.Unmarshal(data, &fixture); err != nil {
			libopusDecoderLossFixtureErr = err
			return
		}
		for i := range fixture.Cases {
			c := &fixture.Cases[i]
			if c.Frames != len(c.Packets) {
				libopusDecoderLossFixtureErr = errors.New("decoder loss fixture frame count mismatch")
				return
			}
			c.decodedPackets = make([][]byte, len(c.Packets))
			for j := range c.Packets {
				payload, err := base64.StdEncoding.DecodeString(c.Packets[j].DataB64)
				if err != nil {
					libopusDecoderLossFixtureErr = err
					return
				}
				c.decodedPackets[j] = payload
			}
			for j := range c.Results {
				r := &c.Results[j]
				if len(r.LossBits) != c.Frames {
					libopusDecoderLossFixtureErr = errors.New("decoder loss fixture loss pattern length mismatch")
					return
				}
				for k := 0; k < len(r.LossBits); k++ {
					if r.LossBits[k] != '0' && r.LossBits[k] != '1' {
						libopusDecoderLossFixtureErr = errors.New("decoder loss fixture has non-binary loss pattern")
						return
					}
				}
				r.parsedLossBits = parseLossBits(r.LossBits)
				raw, err := base64.StdEncoding.DecodeString(r.DecodedF32B64)
				if err != nil {
					libopusDecoderLossFixtureErr = err
					return
				}
				if len(raw)%4 != 0 {
					libopusDecoderLossFixtureErr = errors.New("decoder loss decoded f32 payload length must be multiple of 4")
					return
				}
				if r.DecodedLen != len(raw)/4 {
					libopusDecoderLossFixtureErr = errors.New("decoder loss decoded f32 payload length metadata mismatch")
					return
				}
				r.decodedSamples = make([]float32, len(raw)/4)
				for k := range r.decodedSamples {
					bits := binary.LittleEndian.Uint32(raw[k*4 : k*4+4])
					r.decodedSamples[k] = math.Float32frombits(bits)
				}
			}
		}
		libopusDecoderLossFixtureData = fixture
	})
	return libopusDecoderLossFixtureData, libopusDecoderLossFixtureErr
}

func decodeLibopusDecoderLossPackets(c libopusDecoderLossCaseFile) ([][]byte, error) {
	return append([][]byte(nil), c.decodedPackets...), nil
}

func decodeLibopusDecoderLossSamples(r libopusDecoderLossResultFile) ([]float32, error) {
	return r.decodedSamples, nil
}

func parseLossBits(bits string) []bool {
	out := make([]bool, len(bits))
	for i := 0; i < len(bits); i++ {
		out[i] = bits[i] == '1'
	}
	return out
}
