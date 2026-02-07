package testvectors

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

const silkWBFloatPacketFixturePath = "testdata/silk_wb_libopus_float_packets_fixture.json"

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
