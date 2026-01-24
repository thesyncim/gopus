package celt

import (
	"testing"
)

func TestDebugAllocation(t *testing.T) {
	// Simulate what happens in EncodeFrame for 20ms at 64kbps
	_ = 960 // frameSize
	lm := 3 // 20ms
	nbBands := 21
	channels := 1
	targetBits := 1280 // 64kbps at 20ms

	// Simulated bitsUsed after encoding flags (rough estimate)
	// In reality, TellFrac() returns Q3 bits used
	bitsUsedQ3 := 200 * 8 // ~200 bits in Q3 format

	totalBitsQ3 := (targetBits << bitRes) - bitsUsedQ3 - 1
	totalBits := totalBitsQ3 >> bitRes

	t.Logf("Target bits: %d", targetBits)
	t.Logf("Bits used for flags/energy: ~%d", bitsUsedQ3/8)
	t.Logf("Available for allocation: %d bits", totalBits)

	caps := initCaps(nbBands, lm, channels)
	offsets := make([]int, nbBands)

	result := ComputeAllocation(totalBits, nbBands, channels, caps, offsets, 5, -1, false, lm)

	t.Logf("Coded bands: %d", result.CodedBands)
	t.Logf("Balance: %d", result.Balance)

	totalPVQ := 0
	totalFine := 0
	for i := 0; i < nbBands; i++ {
		bits := result.BandBits[i]
		fine := result.FineBits[i]
		totalPVQ += bits
		totalFine += fine * channels
		if bits > 0 || fine > 0 {
			t.Logf("Band %2d: PVQ=%4d (%3d bits), Fine=%d", i, bits, bits/8, fine)
		}
	}

	t.Logf("")
	t.Logf("Total PVQ bits (Q3): %d = %d bits", totalPVQ, totalPVQ/8)
	t.Logf("Total Fine bits: %d", totalFine)
	t.Logf("Sum: %d bits", totalPVQ/8+totalFine)
}
