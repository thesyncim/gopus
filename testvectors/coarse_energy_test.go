package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/rangecoding"
)

func TestCoarseEnergyRoundTrip(t *testing.T) {
	// Create simple test energies
	nbBands := 5
	energies := []float64{0.0, 12.0, 18.0, -3.0, -6.0}
	lm := 3 // 20ms
	intra := true

	// Encode
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	enc := celt.NewEncoder(1) // 1 channel (mono)
	enc.SetRangeEncoder(re)

	t.Log("=== ENCODING ===")
	t.Logf("Input energies: %v", energies)

	quantizedEnergies := enc.EncodeCoarseEnergy(energies, nbBands, intra, lm)
	t.Logf("Quantized energies: %v", quantizedEnergies)

	// Check how many bits used
	bitsUsed := re.TellFrac() >> 3
	t.Logf("Bits used for encoding: %d", bitsUsed)

	data := re.Done()
	t.Logf("Encoded data: %d bytes", len(data))
	t.Logf("Hex: % x", data[:min(20, len(data))])

	// Decode
	rd := &rangecoding.Decoder{}
	rd.Init(data)

	dec := celt.NewDecoder(1) // 1 channel (mono)
	dec.SetRangeDecoder(rd)

	t.Log("\n=== DECODING ===")
	decodedEnergies := dec.DecodeCoarseEnergy(nbBands, intra, lm)
	t.Logf("Decoded energies: %v", decodedEnergies)

	// Compare
	t.Log("\n=== COMPARISON ===")
	allMatch := true
	for i := 0; i < nbBands; i++ {
		diff := math.Abs(quantizedEnergies[i] - decodedEnergies[i])
		status := "OK"
		if diff > 0.01 {
			status = "MISMATCH"
			allMatch = false
		}
		t.Logf("Band %d: quantized=%.4f, decoded=%.4f, diff=%.4f %s",
			i, quantizedEnergies[i], decodedEnergies[i], diff, status)
	}

	if !allMatch {
		t.Error("Coarse energy round-trip failed - some bands don't match")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
