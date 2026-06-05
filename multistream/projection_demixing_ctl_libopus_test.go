package multistream

import (
	"encoding/binary"
	"fmt"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

var (
	projDemixingCTLOnce sync.Once
	projDemixingCTLPath string
	projDemixingCTLErr  error
)

func getProjectionDemixingCTLPath() (string, error) {
	projDemixingCTLOnce.Do(func() {
		if _, ok := libopustooling.FindOrEnsureOpusDemo(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots()); !ok {
			projDemixingCTLErr = fmt.Errorf("libopus reference tree not found")
			return
		}
		projDemixingCTLPath, projDemixingCTLErr = libopustest.BuildCHelper(libopustest.CHelperConfig{
			Label:      "projection demixing CTL",
			OutputBase: "gopus_libopus_projection_demixing_ctl",
			SourceFile: "libopus_projection_demixing_ctl.c",
			CFlags:     []string{"-O2", "-DNDEBUG"},
			Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		})
	})
	return projDemixingCTLPath, projDemixingCTLErr
}

type projDemixingCTLResult struct {
	streams  int
	coupled  int
	demixing []byte
	gain     int
}

// probeProjectionDemixingCTL calls the libopus oracle to get the demixing matrix and gain
// for a family-3 projection encoder created with opus_projection_ambisonics_encoder_create.
//
// This exercises:
//   - OPUS_PROJECTION_GET_DEMIXING_MATRIX_SIZE_REQUEST
//   - OPUS_PROJECTION_GET_DEMIXING_MATRIX_GAIN_REQUEST
//   - OPUS_PROJECTION_GET_DEMIXING_MATRIX_REQUEST
//
// Reference: libopus src/opus_projection_encoder.c:opus_projection_encoder_ctl
func probeProjectionDemixingCTL(t *testing.T, sampleRate, channels, application int) projDemixingCTLResult {
	t.Helper()
	binPath, err := getProjectionDemixingCTLPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "projection demixing CTL", err)
	}

	// NewOraclePayload writes magic + version(1) + header fields.
	// C oracle expects: GPDI version(1) sample_rate channels application
	payload := libopustest.NewOraclePayload(
		"GPDI",
		uint32(sampleRate),
		uint32(channels),
		uint32(application),
	)

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "projection demixing CTL", "GPDO")
	if err != nil {
		libopustest.HelperUnavailable(t, "projection demixing CTL", err)
	}

	// RunOracle already consumed the magic+version header; next fields are streams, coupled.
	streams := int(reader.U32())
	coupled := int(reader.U32())
	demixSize := int(reader.U32())
	reader.ExpectRemaining(demixSize + 4)
	demixBytes := reader.Bytes(demixSize)
	gain := int(int32(reader.U32()))
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}

	return projDemixingCTLResult{
		streams:  streams,
		coupled:  coupled,
		demixing: demixBytes,
		gain:     gain,
	}
}

// TestProjectionDemixingMatrixCTLParityOrders validates GetDemixingMatrix() against
// opus_projection_ambisonics_encoder_create + OPUS_PROJECTION_GET_DEMIXING_MATRIX
// for all supported ambisonics orders (1-5) with and without non-diegetic channels.
//
// Reference: libopus src/opus_projection_encoder.c:opus_projection_encoder_ctl
// OPUS_PROJECTION_GET_DEMIXING_MATRIX_REQUEST
func TestProjectionDemixingMatrixCTLParityOrders(t *testing.T) {
	libopustest.RequireOracle(t)

	cases := []struct {
		name     string
		channels int
	}{
		// Order 1 (FOA)
		{name: "foa-4ch", channels: 4},
		{name: "foa-nd-6ch", channels: 6},
		// Order 2 (SOA)
		{name: "soa-9ch", channels: 9},
		{name: "soa-nd-11ch", channels: 11},
		// Order 3 (TOA)
		{name: "toa-16ch", channels: 16},
		{name: "toa-nd-18ch", channels: 18},
		// Order 4 (4thOA)
		{name: "fourthoa-25ch", channels: 25},
		{name: "fourthoa-nd-27ch", channels: 27},
		// Order 5 (5thOA)
		{name: "fifthoa-36ch", channels: 36},
		{name: "fifthoa-nd-38ch", channels: 38},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// OPUS_APPLICATION_AUDIO = 2049
			ref := probeProjectionDemixingCTL(t, 48000, tc.channels, 2049)

			enc, err := NewEncoderAmbisonics(48000, tc.channels, 3)
			if err != nil {
				t.Fatalf("NewEncoderAmbisonics(%d, 3): %v", tc.channels, err)
			}

			if enc.Streams() != ref.streams {
				t.Errorf("Streams() = %d, want %d", enc.Streams(), ref.streams)
			}
			if enc.CoupledStreams() != ref.coupled {
				t.Errorf("CoupledStreams() = %d, want %d", enc.CoupledStreams(), ref.coupled)
			}

			gotSize := enc.DemixingMatrixSize()
			if gotSize != len(ref.demixing) {
				t.Errorf("DemixingMatrixSize() = %d, want %d", gotSize, len(ref.demixing))
			}

			gotGain := enc.DemixingMatrixGain()
			if gotGain != ref.gain {
				t.Errorf("DemixingMatrixGain() = %d, want %d", gotGain, ref.gain)
			}

			gotMatrix := enc.GetDemixingMatrix()
			if len(gotMatrix) != len(ref.demixing) {
				t.Fatalf("GetDemixingMatrix() len = %d, want %d", len(gotMatrix), len(ref.demixing))
			}

			n := len(ref.demixing) / 2
			for i := range n {
				got := int16(binary.LittleEndian.Uint16(gotMatrix[2*i : 2*i+2]))
				want := int16(binary.LittleEndian.Uint16(ref.demixing[2*i : 2*i+2]))
				if got != want {
					t.Errorf("demixing[%d] = %d, want %d", i, got, want)
				}
			}
		})
	}
}

// TestProjectionDemixingMatrixSize validates DemixingMatrixSize formula matches libopus.
//
// libopus formula: channels * (streams + coupled_streams) * sizeof(opus_int16)
// Reference: libopus src/opus_projection_encoder.c:opus_projection_encoder_ctl
// OPUS_PROJECTION_GET_DEMIXING_MATRIX_SIZE_REQUEST
func TestProjectionDemixingMatrixSize(t *testing.T) {
	cases := []struct {
		channels int
		streams  int
		coupled  int
		want     int
	}{
		{4, 2, 2, 4 * 4 * 2},    // 4ch FOA: 4 inputs × 4 outputs × 2 bytes
		{6, 3, 3, 6 * 6 * 2},    // 6ch FOA+nd: 6 inputs × 6 outputs × 2 bytes
		{9, 5, 4, 9 * 9 * 2},    // 9ch SOA
		{16, 8, 8, 16 * 16 * 2}, // 16ch TOA
	}
	for _, tc := range cases {
		got := ProjectionDemixingMatrixSize(tc.channels, tc.streams, tc.coupled)
		if got != tc.want {
			t.Errorf("ProjectionDemixingMatrixSize(%d,%d,%d) = %d, want %d",
				tc.channels, tc.streams, tc.coupled, got, tc.want)
		}
	}
}

// TestGetDemixingMatrixNonProjection validates that GetDemixingMatrix returns nil
// for non-projection encoders (family != 3).
func TestGetDemixingMatrixNonProjection(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 6) // family 1
	if err != nil {
		t.Fatalf("NewEncoderDefault: %v", err)
	}
	if got := enc.GetDemixingMatrix(); got != nil {
		t.Errorf("GetDemixingMatrix() on non-projection encoder = %v, want nil", got)
	}
	if got := enc.DemixingMatrixGain(); got != 0 {
		t.Errorf("DemixingMatrixGain() on non-projection encoder = %d, want 0", got)
	}
	if got := enc.DemixingMatrixSize(); got != 0 {
		t.Errorf("DemixingMatrixSize() on non-projection encoder = %d, want 0", got)
	}
}

// TestGetDemixingMatrixMatchesDecoder validates that GetDemixingMatrix from the encoder
// can be used to initialize a projection decoder that produces correct output.
func TestGetDemixingMatrixMatchesDecoder(t *testing.T) {
	channels := 4
	enc, err := NewEncoderAmbisonics(48000, channels, 3)
	if err != nil {
		t.Fatalf("NewEncoderAmbisonics: %v", err)
	}

	demix := enc.GetDemixingMatrix()
	if demix == nil {
		t.Fatal("GetDemixingMatrix() returned nil")
	}

	mapping := make([]byte, channels)
	for i := range mapping {
		mapping[i] = byte(i)
	}
	dec, err := NewDecoder(48000, channels, enc.Streams(), enc.CoupledStreams(), mapping)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if err := dec.SetProjectionDemixingMatrix(demix); err != nil {
		t.Fatalf("SetProjectionDemixingMatrix: %v", err)
	}

	// Encode and decode a frame to verify the pipeline works end-to-end.
	frameSize := 960
	pcm := generateMultichannelSine(channels, frameSize)
	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if packet == nil {
		t.Fatal("Encode returned nil (DTX)")
	}

	out, err := dec.DecodeToFloat32(packet, frameSize)
	if err != nil {
		t.Fatalf("DecodeToFloat32: %v", err)
	}
	if len(out) != frameSize*channels {
		t.Fatalf("output len = %d, want %d", len(out), frameSize*channels)
	}

	energy := computeEnergyF32(out)
	if energy < 1e-6 {
		t.Fatalf("output energy too low: %e (silent output)", energy)
	}
}
