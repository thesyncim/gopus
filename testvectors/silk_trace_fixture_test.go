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

const silkWBFloatPacketFixturePath = "testdata/silk_wb_libopus_float_packets_fixture.json"
const silkWBFloatDecodedFixturePath = "testdata/silk_wb_libopus_float_decoded_fixture.json"

type silkWBFloatPacketFixtureFile struct {
	Version    int                                `json:"version"`
	SampleRate int                                `json:"sample_rate"`
	Channels   int                                `json:"channels"`
	FrameSize  int                                `json:"frame_size"`
	Bitrate    int                                `json:"bitrate"`
	Frames     int                                `json:"frames"`
	Packets    []silkWBFloatPacketFixturePacket   `json:"packets"`
}

type silkWBFloatPacketFixturePacket struct {
	DataB64    string `json:"data_b64"`
	FinalRange uint32 `json:"final_range"`
}

var (
	silkWBFloatPacketFixtureOnce sync.Once
	silkWBFloatPacketFixtureData silkWBFloatPacketFixtureFile
	silkWBFloatPacketFixtureErr  error

	silkWBFloatDecodedFixtureOnce sync.Once
	silkWBFloatDecodedFixtureData silkWBFloatDecodedFixtureFile
	silkWBFloatDecodedFixtureErr  error
)

func loadSILKWBFloatPacketFixture() (silkWBFloatPacketFixtureFile, error) {
	silkWBFloatPacketFixtureOnce.Do(func() {
		data, err := os.ReadFile(filepath.Join(silkWBFloatPacketFixturePath))
		if err != nil {
			silkWBFloatPacketFixtureErr = err
			return
		}

		var fixture silkWBFloatPacketFixtureFile
		if err := json.Unmarshal(data, &fixture); err != nil {
			silkWBFloatPacketFixtureErr = err
			return
		}

		for i := range fixture.Packets {
			if _, err := base64.StdEncoding.DecodeString(fixture.Packets[i].DataB64); err != nil {
				silkWBFloatPacketFixtureErr = err
				return
			}
		}
		silkWBFloatPacketFixtureData = fixture
	})
	return silkWBFloatPacketFixtureData, silkWBFloatPacketFixtureErr
}

func loadSILKWBFloatPacketFixturePackets() ([]libopusPacket, silkWBFloatPacketFixtureFile, error) {
	fixture, err := loadSILKWBFloatPacketFixture()
	if err != nil {
		return nil, silkWBFloatPacketFixtureFile{}, err
	}
	packets := make([]libopusPacket, len(fixture.Packets))
	for i := range fixture.Packets {
		payload, err := base64.StdEncoding.DecodeString(fixture.Packets[i].DataB64)
		if err != nil {
			return nil, silkWBFloatPacketFixtureFile{}, err
		}
		packets[i] = libopusPacket{
			data:       payload,
			finalRange: fixture.Packets[i].FinalRange,
		}
	}
	return packets, fixture, nil
}

type silkWBFloatDecodedFixtureFile struct {
	Version       int    `json:"version"`
	SampleRate    int    `json:"sample_rate"`
	Channels      int    `json:"channels"`
	FrameSize     int    `json:"frame_size"`
	Bitrate       int    `json:"bitrate"`
	Frames        int    `json:"frames"`
	DecodedLen    int    `json:"decoded_len"`
	DecodedF32B64 string `json:"decoded_f32_le_b64"`
}

func loadSILKWBFloatDecodedFixture() (silkWBFloatDecodedFixtureFile, error) {
	silkWBFloatDecodedFixtureOnce.Do(func() {
		data, err := os.ReadFile(filepath.Join(silkWBFloatDecodedFixturePath))
		if err != nil {
			silkWBFloatDecodedFixtureErr = err
			return
		}

		var fixture silkWBFloatDecodedFixtureFile
		if err := json.Unmarshal(data, &fixture); err != nil {
			silkWBFloatDecodedFixtureErr = err
			return
		}

		raw, err := base64.StdEncoding.DecodeString(fixture.DecodedF32B64)
		if err != nil {
			silkWBFloatDecodedFixtureErr = err
			return
		}
		if len(raw)%4 != 0 {
			silkWBFloatDecodedFixtureErr = errors.New("decoded fixture payload length must be multiple of 4")
			return
		}
		if fixture.DecodedLen != len(raw)/4 {
			silkWBFloatDecodedFixtureErr = errors.New("decoded fixture length mismatch")
			return
		}
		silkWBFloatDecodedFixtureData = fixture
	})
	return silkWBFloatDecodedFixtureData, silkWBFloatDecodedFixtureErr
}

func loadSILKWBFloatDecodedFixtureSamples() ([]float32, silkWBFloatDecodedFixtureFile, error) {
	fixture, err := loadSILKWBFloatDecodedFixture()
	if err != nil {
		return nil, silkWBFloatDecodedFixtureFile{}, err
	}
	raw, err := base64.StdEncoding.DecodeString(fixture.DecodedF32B64)
	if err != nil {
		return nil, silkWBFloatDecodedFixtureFile{}, err
	}
	out := make([]float32, len(raw)/4)
	for i := range out {
		bits := binary.LittleEndian.Uint32(raw[i*4:])
		out[i] = math.Float32frombits(bits)
	}
	return out, fixture, nil
}
