package gopus

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

type libopusPacketDurationCase struct {
	name       string
	packet     []byte
	sampleRate int
}

type libopusPacketDurationResult struct {
	samplesPerFrame int
	frameCount      int
	samples         int
}

var libopusPacketDurationHelper libopustest.HelperCache

func getLibopusPacketDurationHelperPath() (string, error) {
	return libopusPacketDurationHelper.CHelperPath(libopustest.CHelperConfig{
		Label:      "packet duration",
		OutputBase: "gopus_libopus_packet_duration",
		SourceFile: "libopus_packet_duration_info.c",
		CFlags:     []string{"-DHAVE_CONFIG_H", "-O2"},
		Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:  true,
	})
}

func probeLibopusPacketDurations(cases []libopusPacketDurationCase) ([]libopusPacketDurationResult, error) {
	binPath, err := getLibopusPacketDurationHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GPDI", uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.sampleRate))
		payload.U32(uint32(len(tc.packet)))
		payload.Raw(tc.packet)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "packet duration", "GPDO")
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	reader.ExpectRemaining(12 * count)
	out := make([]libopusPacketDurationResult, count)
	for i := range out {
		out[i] = libopusPacketDurationResult{
			samplesPerFrame: int(reader.I32()),
			frameCount:      int(reader.I32()),
			samples:         int(reader.I32()),
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestPacketDurationAtRateMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := packetDurationOracleCases()
	want, err := probeLibopusPacketDurations(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet duration", err)
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotFrame, err := packetSamplesPerFrameAtRate(tc.packet, tc.sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesPerFrameAtRate error: %v", err)
			}
			if gotFrame != want[i].samplesPerFrame {
				t.Fatalf("samples/frame=%d want %d", gotFrame, want[i].samplesPerFrame)
			}
			gotCount, err := packetFrameCountLibopus(tc.packet)
			if err != nil {
				t.Fatalf("packetFrameCountLibopus error: %v", err)
			}
			if gotCount != want[i].frameCount {
				t.Fatalf("frame count=%d want %d", gotCount, want[i].frameCount)
			}
			gotSamples, err := packetSamplesAtRate(tc.packet, tc.sampleRate)
			if want[i].samples < 0 {
				if err == nil {
					t.Fatalf("packetSamplesAtRate error=nil want libopus error %d", want[i].samples)
				}
				return
			}
			if err != nil {
				t.Fatalf("packetSamplesAtRate error: %v", err)
			}
			if gotSamples != want[i].samples {
				t.Fatalf("samples=%d want %d", gotSamples, want[i].samples)
			}
		})
	}
}

func packetDurationOracleCases() []libopusPacketDurationCase {
	var cases []libopusPacketDurationCase
	for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
		for _, config := range []uint8{0, 1, 2, 3, 12, 13, 14, 15, 16, 17, 18, 19, 28, 29, 30, 31} {
			for _, code := range []uint8{0, 1, 2} {
				toc := GenerateTOC(config, false, code)
				cases = append(cases, libopusPacketDurationCase{
					name:       durationCaseName(rate, config, code, 1),
					packet:     []byte{toc, 0, 0, 0},
					sampleRate: rate,
				})
			}
			for _, count := range []byte{1, 2, 3, 6} {
				toc := GenerateTOC(config, false, 3)
				cases = append(cases, libopusPacketDurationCase{
					name:       durationCaseName(rate, config, 3, int(count)),
					packet:     []byte{toc, count, 0, 0, 0, 0, 0, 0},
					sampleRate: rate,
				})
			}
		}
	}
	return cases
}

func durationCaseName(rate int, config, code uint8, count int) string {
	return "fs_" + itoaSmall(rate) + "_cfg_" + itoaSmall(int(config)) + "_code_" + itoaSmall(int(code)) + "_m_" + itoaSmall(count)
}

func itoaSmall(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
