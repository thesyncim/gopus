//go:build gopus_custom_modes

package custom_test

import (
	"bytes"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt/custom"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestOracleCustomDynamicFFTExact(t *testing.T) {
	libopustest.RequireOracle(t)
	// This mode uses dynamically generated FFT tables at lengths 40..320.
	const sampleRate, frameSize, maxBytes = 48000, 640, 200
	pcm := generateSine(440, sampleRate, frameSize)
	ref := runCustomOracle(t, []oracleCase{{sampleRate, frameSize, 1, maxBytes, pcm}})[0]
	if ref.status < 0 || len(ref.decoded) != frameSize {
		t.Fatalf("C status=%d decoded samples=%d want=%d", ref.status, len(ref.decoded), frameSize)
	}
	mode, err := custom.NewMode(sampleRate, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := custom.NewEncoder(mode, 1)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := enc.EncodeFloat(pcm, maxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(packet, ref.packet) || enc.FinalRange() != ref.encRange {
		t.Fatalf("encoded packet/range: got=%x/%08x want=%x/%08x", packet, enc.FinalRange(), ref.packet, ref.encRange)
	}
	dec, err := custom.NewDecoder(mode, 1)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := dec.DecodeFloat(ref.packet, frameSize)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != frameSize || dec.FinalRange() != ref.decRange {
		t.Fatalf("decoded samples/range=%d/%08x want=%d/%08x", len(decoded), dec.FinalRange(), frameSize, ref.decRange)
	}
	for i, sample := range decoded {
		if math.Float32bits(sample) != math.Float32bits(ref.decoded[i]) {
			t.Fatalf("decoded sample %d bits=%08x want=%08x", i, math.Float32bits(sample), math.Float32bits(ref.decoded[i]))
		}
	}
}
