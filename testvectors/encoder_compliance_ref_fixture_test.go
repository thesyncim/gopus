package testvectors

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

const encoderComplianceRefQFixturePath = "testdata/encoder_compliance_libopus_ref_q.json"

type encoderComplianceRefQFixtureFile struct {
	Version int                               `json:"version"`
	Cases   []encoderComplianceRefQFixtureRow `json:"cases"`
}

type encoderComplianceRefQFixtureRow struct {
	Mode      string  `json:"mode"`
	Bandwidth string  `json:"bandwidth"`
	FrameSize int     `json:"frame_size"`
	Channels  int     `json:"channels"`
	Bitrate   int     `json:"bitrate"`
	LibQ      float64 `json:"lib_q"`
}

var (
	encoderComplianceRefQFixtureOnce sync.Once
	encoderComplianceRefQFixtureData encoderComplianceRefQFixtureFile
	encoderComplianceRefQFixtureErr  error
)

func libopusComplianceReferenceAvailable() bool {
	_, err := loadEncoderComplianceReferenceQFixture()
	return err == nil
}

func lookupEncoderComplianceReferenceQ(mode encoder.Mode, bandwidth types.Bandwidth, frameSize, channels, bitrate int) (float64, bool) {
	fixture, err := loadEncoderComplianceReferenceQFixture()
	if err != nil {
		return 0, false
	}
	modeName := fixtureModeName(mode)
	bwName := fixtureBandwidthName(bandwidth)
	for _, row := range fixture.Cases {
		if row.Mode == modeName &&
			row.Bandwidth == bwName &&
			row.FrameSize == frameSize &&
			row.Channels == channels &&
			row.Bitrate == bitrate {
			return row.LibQ, true
		}
	}
	return 0, false
}

func loadEncoderComplianceReferenceQFixture() (encoderComplianceRefQFixtureFile, error) {
	encoderComplianceRefQFixtureOnce.Do(func() {
		path := filepath.Join(encoderComplianceRefQFixturePath)
		data, err := os.ReadFile(path)
		if err != nil {
			encoderComplianceRefQFixtureErr = err
			return
		}
		var fixture encoderComplianceRefQFixtureFile
		if err := json.Unmarshal(data, &fixture); err != nil {
			encoderComplianceRefQFixtureErr = err
			return
		}
		encoderComplianceRefQFixtureData = fixture
	})
	return encoderComplianceRefQFixtureData, encoderComplianceRefQFixtureErr
}
