//go:build gopus_libopus_oracle

package celt

import (
	"reflect"
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
)

func TestDecoderBandAllocationProbeDeterministic(t *testing.T) {
	packet := []byte{
		0xFC, // config 31 CELT mono 20ms
		0x00, 0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x70,
		0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88,
	}
	frameSize := 960

	decA := NewDecoder(1)
	decB := NewDecoder(1)

	var rdA, rdB rangecoding.Decoder
	rdA.Init(packet[1:])
	rdB.Init(packet[1:])

	gotA, err := decA.ProbeBandAllocationWithDecoder(&rdA, frameSize)
	if err != nil {
		t.Fatalf("probe A: %v", err)
	}
	gotB, err := decB.ProbeBandAllocationWithDecoder(&rdB, frameSize)
	if err != nil {
		t.Fatalf("probe B: %v", err)
	}
	if !reflect.DeepEqual(gotA, gotB) {
		t.Fatalf("probe not deterministic: A=%+v B=%+v", gotA, gotB)
	}
}
