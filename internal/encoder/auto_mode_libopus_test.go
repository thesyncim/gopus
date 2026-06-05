package encoder

import (
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/types"
)

const (
	libopusAutoModeInputMagic  = "GAMI"
	libopusAutoModeOutputMagic = "GAMO"
)

type libopusAutoModeCase struct {
	sampleRate   int
	channels     int
	frameSize    int
	bitrate      int
	maxDataBytes int
	pcm          []float32
}

type libopusAutoModeResult struct {
	n   int
	toc byte
}

var (
	libopusAutoModeHelperOnce sync.Once
	libopusAutoModeHelperPath string
	libopusAutoModeHelperErr  error
)

func getLibopusAutoModeHelperPath() (string, error) {
	libopusAutoModeHelperOnce.Do(func() {
		libopusAutoModeHelperPath, libopusAutoModeHelperErr = libopustest.BuildCHelper(libopustest.CHelperConfig{
			Label:      "auto mode",
			OutputBase: "gopus_libopus_auto_mode",
			SourceFile: "libopus_auto_mode_info.c",
			CFlags:     []string{"-DHAVE_CONFIG_H"},
			Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		})
	})
	if libopusAutoModeHelperErr != nil {
		return "", libopusAutoModeHelperErr
	}
	return libopusAutoModeHelperPath, nil
}

func probeLibopusAutoMode(cases []libopusAutoModeCase) ([]libopusAutoModeResult, error) {
	binPath, err := getLibopusAutoModeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusAutoModeInputMagic, uint32(len(cases)))
	for _, c := range cases {
		if len(c.pcm) != c.frameSize*c.channels {
			return nil, fmt.Errorf("case pcm len=%d want %d", len(c.pcm), c.frameSize*c.channels)
		}
		payload.U32(uint32(c.sampleRate))
		payload.U32(uint32(c.channels))
		payload.U32(uint32(c.frameSize))
		payload.U32(uint32(c.bitrate))
		payload.U32(uint32(c.maxDataBytes))
		for _, sample := range c.pcm {
			payload.Float32(sample)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "auto mode", libopusAutoModeOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	reader.ExpectRemaining(8 * count)
	out := make([]libopusAutoModeResult, count)
	for i := range out {
		out[i].n = int(reader.I32())
		out[i].toc = byte(reader.U32())
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func autoModeOraclePCM(frameSize, channels int) []float32 {
	pcm := make([]float32, frameSize*channels)
	for i := range frameSize {
		t := float64(i) / 48000.0
		sample := float32(0.09*math.Sin(2*math.Pi*220*t) + 0.03*math.Sin(2*math.Pi*660*t))
		for ch := range channels {
			pcm[i*channels+ch] = sample
		}
	}
	return pcm
}

func TestAutoModePacketBudgetMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
	)
	pcm32 := autoModeOraclePCM(frameSize, channels)
	cases := []libopusAutoModeCase{
		{sampleRate: sampleRate, channels: channels, frameSize: frameSize, bitrate: 4000, maxDataBytes: 120, pcm: pcm32},
		{sampleRate: sampleRate, channels: channels, frameSize: frameSize, bitrate: 64000, maxDataBytes: 14, pcm: pcm32},
	}
	want, err := probeLibopusAutoMode(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "auto mode", err)
	}

	for i, c := range cases {
		name := fmt.Sprintf("bitrate_%d_max_%d", c.bitrate, c.maxDataBytes)
		t.Run(name, func(t *testing.T) {
			if want[i].n <= 0 {
				t.Fatalf("libopus encode returned %d", want[i].n)
			}

			pcm64 := make([]float64, len(c.pcm))
			for j, sample := range c.pcm {
				pcm64[j] = float64(sample)
			}
			enc := NewEncoder(c.sampleRate, c.channels)
			enc.SetMode(ModeAuto)
			enc.SetSignalType(types.SignalAuto)
			enc.SetBandwidth(types.BandwidthFullband)
			enc.SetBitrate(c.bitrate)
			enc.SetBitrateMode(ModeVBR)
			enc.SetVBR(true)
			enc.SetVBRConstraint(false)
			enc.SetComplexity(10)
			enc.SetLSBDepth(24)
			enc.SetPacketLoss(0)
			enc.SetFEC(false)
			enc.SetDTX(false)

			gotPacket, err := encodeWithAnalysisMaxBytesTest(enc, pcm64, c.frameSize, pcm64, c.maxDataBytes)
			if err != nil {
				t.Fatalf("EncodeWithAnalysisMaxBytes: %v", err)
			}
			if len(gotPacket) == 0 {
				t.Fatal("EncodeWithAnalysisMaxBytes returned empty packet")
			}
			gotMode := modeFixtureLabelFromConfig(int(gotPacket[0] >> 3))
			wantMode := modeFixtureLabelFromConfig(int(want[i].toc >> 3))
			if gotMode != wantMode {
				t.Fatalf("packet mode=%s want %s (go_toc=0x%02x libopus_toc=0x%02x)",
					gotMode, wantMode, gotPacket[0], want[i].toc)
			}
		})
	}
}
