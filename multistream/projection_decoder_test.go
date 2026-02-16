package multistream

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"

	oggcontainer "github.com/thesyncim/gopus/container/ogg"
)

func matrixBytes(vals ...int16) []byte {
	out := make([]byte, 2*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint16(out[2*i:2*i+2], uint16(v))
	}
	return out
}

func TestSetProjectionDemixingMatrixInvalidSize(t *testing.T) {
	dec, err := NewDecoder(48000, 2, 1, 1, []byte{0, 1})
	if err != nil {
		t.Fatalf("NewDecoder failed: %v", err)
	}

	err = dec.SetProjectionDemixingMatrix([]byte{1, 2, 3})
	if !errors.Is(err, ErrInvalidProjectionMatrix) {
		t.Fatalf("SetProjectionDemixingMatrix error = %v, want %v", err, ErrInvalidProjectionMatrix)
	}
}

func TestApplyProjectionDemixingSwapsChannels(t *testing.T) {
	dec, err := NewDecoder(48000, 2, 1, 1, []byte{0, 1})
	if err != nil {
		t.Fatalf("NewDecoder failed: %v", err)
	}

	// Column-major 2x2 swap matrix:
	// [0 1]
	// [1 0]
	if err := dec.SetProjectionDemixingMatrix(matrixBytes(0, 32767, 32767, 0)); err != nil {
		t.Fatalf("SetProjectionDemixingMatrix failed: %v", err)
	}

	pcm := []float64{1, 2, 3, 4}
	dec.applyProjectionDemixing(pcm, 2)

	scale := 32767.0 / 32768.0
	want := []float64{2 * scale, 1 * scale, 4 * scale, 3 * scale}
	for i := range want {
		if math.Abs(pcm[i]-want[i]) > 1e-3 {
			t.Fatalf("sample[%d] = %f, want %f", i, pcm[i], want[i])
		}
	}
}

func TestSetProjectionDemixingMatrixFromFamily3Header(t *testing.T) {
	head := oggcontainer.DefaultOpusHeadMultistreamWithFamily(48000, 4, oggcontainer.MappingFamilyProjection, 2, 2, nil)
	dec, err := NewDecoder(48000, 4, 2, 2, []byte{0, 1, 2, 3})
	if err != nil {
		t.Fatalf("NewDecoder failed: %v", err)
	}

	if err := dec.SetProjectionDemixingMatrix(head.DemixingMatrix); err != nil {
		t.Fatalf("SetProjectionDemixingMatrix failed: %v", err)
	}
	if got, want := len(dec.projectionDemixing), 16; got != want {
		t.Fatalf("projectionDemixing len = %d, want %d", got, want)
	}
}
