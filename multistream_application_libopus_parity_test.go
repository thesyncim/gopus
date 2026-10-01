package gopus

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var multistreamApplicationStateHelper libopustest.HelperCache

const (
	libopusApplicationVoIP     int32 = 2048
	libopusApplicationAudio    int32 = 2049
	libopusApplicationLowDelay int32 = 2051
)

type multistreamApplicationStateCase struct {
	name           string
	channels       int
	streams        int
	coupledStreams int
	mapping        []byte
	lowCapacity    int
	pcm            []float32
}

type multistreamApplicationPacket struct {
	status      int32
	rangeStatus int32
	finalRange  uint32
	bytes       []byte
}

type multistreamApplicationOracleResult struct {
	createStatus          int32
	bitrateStatus         int32
	lowPacket             multistreamApplicationPacket
	setApplicationStatus  int32
	getApplicationStatus  int32
	application           int32
	childApplicationState []int32
	fullPacket            multistreamApplicationPacket
}

func buildMultistreamApplicationStateHelper() (string, error) {
	return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
		Label:      "multistream application state after low-budget encode",
		OutputBase: "gopus_libopus_multistream_application_state",
		SourceFile: "libopus_multistream_application_state.c",
		CFlags:     []string{"-O3", "-DNDEBUG"},
	})
}

func probeMultistreamApplicationState(tc multistreamApplicationStateCase) (multistreamApplicationOracleResult, error) {
	helper, err := multistreamApplicationStateHelper.Path(buildMultistreamApplicationStateHelper)
	if err != nil {
		return multistreamApplicationOracleResult{}, err
	}
	if len(tc.pcm) != tc.channels*960 {
		return multistreamApplicationOracleResult{}, fmt.Errorf("invalid PCM length %d for %d channels", len(tc.pcm), tc.channels)
	}

	const fullCapacity = 4000
	payload := libopustest.NewOraclePayloadVersion("GMCI", 1,
		uint32(tc.channels), uint32(tc.streams), uint32(tc.coupledStreams),
		960, uint32(tc.lowCapacity), fullCapacity, 64000, uint32(len(tc.mapping)))
	payload.Raw(tc.mapping)
	for _, sample := range tc.pcm {
		payload.Float32(sample)
	}
	reader, err := libopustest.RunOracle(helper, payload.Bytes(), "multistream application state", "GMCO")
	if err != nil {
		return multistreamApplicationOracleResult{}, err
	}
	result := multistreamApplicationOracleResult{
		createStatus:         reader.I32(),
		bitrateStatus:        reader.I32(),
		lowPacket:            readMultistreamApplicationPacket(reader),
		setApplicationStatus: reader.I32(),
		getApplicationStatus: reader.I32(),
		application:          reader.I32(),
	}
	streamCount := reader.Count(tc.streams)
	result.childApplicationState = make([]int32, streamCount)
	for i := range result.childApplicationState {
		if status := reader.I32(); status != 0 {
			return multistreamApplicationOracleResult{}, fmt.Errorf("libopus child %d application getter status=%d", i, status)
		}
		result.childApplicationState[i] = reader.I32()
	}
	result.fullPacket = readMultistreamApplicationPacket(reader)
	if err := reader.ExpectConsumed(); err != nil {
		return multistreamApplicationOracleResult{}, fmt.Errorf("parse libopus multistream application result: %w", err)
	}
	if err := reader.Err(); err != nil {
		return multistreamApplicationOracleResult{}, err
	}
	return result, nil
}

func readMultistreamApplicationPacket(reader *libopustest.OracleReader) multistreamApplicationPacket {
	status := reader.I32()
	rangeStatus := reader.I32()
	finalRange := reader.U32()
	packetLen := reader.Count(-1)
	return multistreamApplicationPacket{
		status:      status,
		rangeStatus: rangeStatus,
		finalRange:  finalRange,
		bytes:       reader.Bytes(packetLen),
	}
}

func TestMultistreamEncoderSetApplicationAfterLowSpaceMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	monoSilence := make([]float32, 960)
	mixedPCM := make([]float32, 3*960)
	mixedPCM[2] = 0.25
	mixedPCM[5] = -0.125
	cases := []multistreamApplicationStateCase{
		{
			name:        "mono_low_space_keeps_first_frame_open",
			channels:    1,
			streams:     1,
			mapping:     []byte{0},
			lowCapacity: 1,
			pcm:         monoSilence,
		},
		{
			name:           "coupled_and_mono_low_space_keeps_all_children_open",
			channels:       3,
			streams:        2,
			coupledStreams: 1,
			mapping:        []byte{0, 1, 2},
			lowCapacity:    3,
			pcm:            mixedPCM,
		},
		{
			name:           "coupled_and_mono_budget_commits_a_child",
			channels:       3,
			streams:        2,
			coupledStreams: 1,
			mapping:        []byte{0, 1, 2},
			lowCapacity:    5,
			pcm:            mixedPCM,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, err := probeMultistreamApplicationState(tc)
			if err != nil {
				libopustest.HelperUnavailable(t, "multistream application state", err)
			}
			if want.createStatus != 0 || want.bitrateStatus != 0 {
				t.Fatalf("libopus setup status create=%d bitrate=%d", want.createStatus, want.bitrateStatus)
			}
			if want.lowPacket.status < 0 || want.lowPacket.rangeStatus != 0 {
				t.Fatalf("libopus low-budget encode status=%d range status=%d", want.lowPacket.status, want.lowPacket.rangeStatus)
			}
			if want.setApplicationStatus != 0 && want.setApplicationStatus != -1 {
				t.Fatalf("libopus SetApplication status=%d, want OPUS_OK or OPUS_BAD_ARG", want.setApplicationStatus)
			}
			if want.getApplicationStatus != 0 {
				t.Fatalf("libopus GetApplication status=%d", want.getApplicationStatus)
			}
			if want.fullPacket.status < 0 || want.fullPacket.rangeStatus != 0 {
				t.Fatalf("libopus full-budget encode status=%d range status=%d", want.fullPacket.status, want.fullPacket.rangeStatus)
			}

			enc, err := NewMultistreamEncoder(48000, tc.channels, tc.streams, tc.coupledStreams, tc.mapping, ApplicationAudio)
			if err != nil {
				t.Fatalf("NewMultistreamEncoder: %v", err)
			}
			if err := enc.SetBitrate(64000); err != nil {
				t.Fatalf("SetBitrate(64000): %v", err)
			}

			lowPacket := make([]byte, tc.lowCapacity)
			n, err := enc.Encode(tc.pcm, lowPacket)
			if err != nil {
				t.Fatalf("low-budget Encode: %v", err)
			}
			assertMultistreamApplicationPacket(t, "low-budget encode", multistreamApplicationPacket{
				status: int32(n), rangeStatus: 0, finalRange: enc.GetFinalRange(), bytes: lowPacket[:n],
			}, want.lowPacket)

			setErr := enc.SetApplication(ApplicationVoIP)
			if want.setApplicationStatus == 0 && setErr != nil {
				t.Fatalf("SetApplication(VoIP) error=%v, libopus accepted it", setErr)
			}
			if want.setApplicationStatus == -1 && setErr != ErrInvalidApplication {
				t.Fatalf("SetApplication(VoIP) error=%v, want %v from libopus rejection", setErr, ErrInvalidApplication)
			}
			if got := libopusApplicationCode(enc.Application()); got != want.application {
				t.Fatalf("Application()=%d, libopus OPUS_GET_APPLICATION=%d", got, want.application)
			}
			if got := enc.enc.VoIPApplication(); got != (want.application == libopusApplicationVoIP) {
				t.Fatalf("first child VoIP policy=%t, libopus application=%d", got, want.application)
			}
			if got := enc.enc.LowDelay(); got != (want.application == libopusApplicationLowDelay) {
				t.Fatalf("first child low-delay policy=%t, libopus application=%d", got, want.application)
			}
			for i, app := range want.childApplicationState {
				if want.setApplicationStatus == 0 && app != libopusApplicationVoIP {
					t.Fatalf("libopus child %d application=%d after successful broadcast, want VoIP", i, app)
				}
				if want.setApplicationStatus == -1 && app != libopusApplicationAudio {
					t.Fatalf("libopus child %d application=%d after rejected broadcast, want Audio", i, app)
				}
			}

			fullPacket := make([]byte, 4000)
			fullN, err := enc.Encode(tc.pcm, fullPacket)
			if err != nil {
				t.Fatalf("full-budget Encode: %v", err)
			}
			assertMultistreamApplicationPacket(t, "full-budget encode", multistreamApplicationPacket{
				status: int32(fullN), rangeStatus: 0, finalRange: enc.GetFinalRange(), bytes: fullPacket[:fullN],
			}, want.fullPacket)
		})
	}
}

func libopusApplicationCode(application Application) int32 {
	switch application {
	case ApplicationVoIP:
		return libopusApplicationVoIP
	case ApplicationAudio:
		return libopusApplicationAudio
	case ApplicationLowDelay:
		return libopusApplicationLowDelay
	default:
		return -1
	}
}

func assertMultistreamApplicationPacket(t *testing.T, label string, got, want multistreamApplicationPacket) {
	t.Helper()
	if got.status != want.status || got.rangeStatus != want.rangeStatus || got.finalRange != want.finalRange || !bytes.Equal(got.bytes, want.bytes) {
		t.Fatalf("%s differs from libopus: Go status=%d len=%d range=%08x; C status=%d len=%d range=%08x", label, got.status, len(got.bytes), got.finalRange, want.status, len(want.bytes), want.finalRange)
	}
}
