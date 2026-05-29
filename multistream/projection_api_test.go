package multistream

import (
	"errors"
	"testing"
)

// TestNewProjectionEncoderRoundtrip validates the NewProjectionEncoder /
// NewProjectionDecoder convenience API end-to-end for all supported ambisonics
// orders 1-5 (with and without non-diegetic channels).
func TestNewProjectionEncoderRoundtrip(t *testing.T) {
	cases := []struct {
		name     string
		channels int
	}{
		{name: "foa-4ch", channels: 4},
		{name: "foa-nd-6ch", channels: 6},
		{name: "soa-9ch", channels: 9},
		{name: "soa-nd-11ch", channels: 11},
		{name: "toa-16ch", channels: 16},
		{name: "toa-nd-18ch", channels: 18},
		{name: "fourthoa-25ch", channels: 25},
		{name: "fourthoa-nd-27ch", channels: 27},
		{name: "fifthoa-36ch", channels: 36},
		{name: "fifthoa-nd-38ch", channels: 38},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewProjectionEncoder(48000, tc.channels)
			if err != nil {
				t.Fatalf("NewProjectionEncoder(%d): %v", tc.channels, err)
			}
			if enc.MappingFamily() != 3 {
				t.Errorf("MappingFamily() = %d, want 3", enc.MappingFamily())
			}

			demix := enc.GetDemixingMatrix()
			if demix == nil {
				t.Fatal("GetDemixingMatrix() returned nil")
			}
			wantSize := enc.DemixingMatrixSize()
			if len(demix) != wantSize {
				t.Fatalf("GetDemixingMatrix() len = %d, want %d", len(demix), wantSize)
			}

			dec, err := NewProjectionDecoder(48000, tc.channels, enc.Streams(), enc.CoupledStreams(), demix)
			if err != nil {
				t.Fatalf("NewProjectionDecoder(%d): %v", tc.channels, err)
			}

			frameSize := 960
			pcm := generateMultichannelSine(tc.channels, frameSize)
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
			if len(out) != frameSize*tc.channels {
				t.Fatalf("output len = %d, want %d", len(out), frameSize*tc.channels)
			}
			if energy := computeEnergyF32(out); energy < 1e-6 {
				t.Fatalf("output energy too low: %e", energy)
			}
		})
	}
}

// TestNewProjectionEncoderUnsupportedOrders verifies that projection encoding
// correctly rejects ambisonics orders outside the range supported by libopus 1.6.1.
//
// libopus only provides pre-computed matrices for orders 1-5; orders 0 and 6-14
// return OPUS_BAD_ARG from opus_projection_ambisonics_encoder_create.
//
// Reference: libopus src/opus_projection_encoder.c:opus_projection_ambisonics_encoder_get_size,
// src/mapping_matrix.c (only foa..fifthoa tables exist)
func TestNewProjectionEncoderUnsupportedOrders(t *testing.T) {
	unsupported := []struct {
		name     string
		channels int
	}{
		// Order 0 (no mixing/demixing matrix in libopus)
		{name: "order0-1ch", channels: 1},
		{name: "order0-nd-3ch", channels: 3},
		// Orders 6-14 (no matrices in libopus)
		{name: "order6-49ch", channels: 49},
		{name: "order6-nd-51ch", channels: 51},
		{name: "order7-64ch", channels: 64},
		{name: "order7-nd-66ch", channels: 66},
		{name: "order8-81ch", channels: 81},
		{name: "order14-225ch", channels: 225},
		{name: "order14-nd-227ch", channels: 227},
	}

	for _, tc := range unsupported {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewProjectionEncoder(48000, tc.channels)
			if err == nil {
				t.Fatalf("NewProjectionEncoder(%d) expected error, got nil", tc.channels)
			}
			if !errors.Is(err, ErrProjectionOrderUnsupported) {
				t.Errorf("NewProjectionEncoder(%d) error = %v, want ErrProjectionOrderUnsupported", tc.channels, err)
			}
		})
	}
}

// TestProjectionDecoderNilDemixMatrix verifies that NewProjectionDecoder with a nil
// demixing matrix creates a plain multistream decoder (no projection applied).
func TestProjectionDecoderNilDemixMatrix(t *testing.T) {
	dec, err := NewProjectionDecoder(48000, 4, 2, 2, nil)
	if err != nil {
		t.Fatalf("NewProjectionDecoder with nil matrix: %v", err)
	}
	if dec.Channels() != 4 {
		t.Errorf("Channels() = %d, want 4", dec.Channels())
	}
}
