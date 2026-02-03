//go:build cgo_libopus

package silk

import (
	"testing"

	cgowrap "github.com/thesyncim/gopus/celt/cgo_test"
)

func TestSilkEncodePulsesLibopusRoundtrip(t *testing.T) {
	const frameLength = 320
	pulses := make([]int32, frameLength)

	// Deterministic non-zero pulse pattern in [-31, 31] to exercise LSB encoding.
	var seed uint32 = 1
	for i := range pulses {
		seed = seed*1664525 + 1013904223
		v := int32((seed>>26)&0x3F) - 31
		if v == 0 {
			v = 1
		}
		pulses[i] = v
	}

	enc := NewEncoder(BandwidthWideband)
	output := ensureByteSlice(&enc.scratchOutput, 2048)
	enc.scratchRangeEncoder.Init(output)
	enc.rangeEncoder = &enc.scratchRangeEncoder
	enc.encodePulses(pulses, typeVoiced, 0)
	data := enc.rangeEncoder.Done()
	enc.rangeEncoder = nil

	decoded, err := cgowrap.SilkDecodePulsesOnly(data, typeVoiced, 0, frameLength)
	if err != nil {
		t.Fatalf("libopus decode pulses: %v", err)
	}
	if decoded == nil {
		t.Fatalf("libopus decode returned nil")
	}

	for i := 0; i < frameLength; i++ {
		if decoded[i] != int16(pulses[i]) {
			t.Fatalf("pulse[%d] mismatch: go=%d lib=%d", i, pulses[i], decoded[i])
		}
	}
}
