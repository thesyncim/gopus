package multistream

import (
	"errors"
	"testing"
)

// TestNewProjectionDecoder_InvalidLayout verifies that NewProjectionDecoder
// surfaces the underlying NewDecoder validation error when the stream layout is
// invalid (here coupledStreams exceeds streams), rather than returning a
// half-initialized decoder.
func TestNewProjectionDecoder_InvalidLayout(t *testing.T) {
	// coupledStreams (3) > streams (2) is rejected by NewDecoder.
	_, err := NewProjectionDecoder(48000, 4, 2, 3, nil)
	if !errors.Is(err, ErrInvalidCoupledStreams) {
		t.Fatalf("NewProjectionDecoder bad layout: got %v, want ErrInvalidCoupledStreams", err)
	}
}

// TestNewProjectionDecoder_BadMatrix verifies that NewProjectionDecoder returns
// ErrInvalidProjectionMatrix when the supplied demixing matrix does not match the
// expected 2*rows*cols byte size for the channel/stream layout.
func TestNewProjectionDecoder_BadMatrix(t *testing.T) {
	// Valid FOA family-3 layout: 4 channels, streams=2, coupled=2.
	// Expected matrix size = 2 * rows(4) * cols(streams+coupled=4) = 32 bytes.
	// Supply a too-short matrix to trip the size check.
	badMatrix := make([]byte, 8)
	_, err := NewProjectionDecoder(48000, 4, 2, 2, badMatrix)
	if !errors.Is(err, ErrInvalidProjectionMatrix) {
		t.Fatalf("NewProjectionDecoder bad matrix: got %v, want ErrInvalidProjectionMatrix", err)
	}
}

// TestAmbisonicsMapping_InvalidChannels verifies that AmbisonicsMapping
// propagates the ValidateAmbisonics error for an invalid channel count instead of
// returning a mapping.
func TestAmbisonicsMapping_InvalidChannels(t *testing.T) {
	// 5 channels is not a valid ambisonics count ((order+1)^2 + {0,2}).
	mapping, err := AmbisonicsMapping(5)
	if err == nil {
		t.Fatalf("AmbisonicsMapping(5) returned mapping %v, want error", mapping)
	}
	if !errors.Is(err, ErrInvalidAmbisonicsChannels) {
		t.Fatalf("AmbisonicsMapping(5) error = %v, want ErrInvalidAmbisonicsChannels", err)
	}
}
