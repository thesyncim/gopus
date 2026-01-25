package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

func TestTraceFullEncodeDecode(t *testing.T) {
	// Generate 1 frame of simple sine wave
	sampleRate := 48000
	frameSize := 960
	freq := 440.0

	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		ti := float64(i) / float64(sampleRate)
		pcm[i] = 0.5 * math.Sin(2*math.Pi*freq*ti)
	}

	// Get mode config
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	t.Logf("Frame size: %d, nbBands: %d", frameSize, nbBands)

	// eMeans for reference
	eMeans := []float64{6.4375, 6.25, 5.75, 5.3125, 5.0625, 4.8125, 4.5, 4.375, 4.875, 4.6875}
	DB6 := 6.0

	// === ENCODER SIDE ===
	enc := celt.NewEncoder(1)
	enc.SetBitrate(64000)

	// Create a separate encoder for analysis to avoid corrupting state
	encAnalysis := celt.NewEncoder(1)
	// Apply pre-emphasis for analysis only (doesn't affect enc's state)
	preemph := encAnalysis.ApplyPreemphasis(pcm)
	t.Logf("Pre-emphasis applied, max=%.4f", maxAbsF(preemph))

	// Compute MDCT with overlap
	mdct := celt.MDCT(append(make([]float64, celt.Overlap), preemph...))
	t.Logf("MDCT computed, len=%d, max=%.4f", len(mdct), maxAbsF(mdct))

	// Compute band energies (raw)
	energies := enc.ComputeBandEnergies(mdct, nbBands, frameSize)

	t.Logf("\n=== ENCODER: Band analysis ===")
	for i := 0; i < 5 && i < nbBands; i++ {
		// Band width and MDCT magnitude
		start := celt.ScaledBandStart(i, frameSize)
		end := celt.ScaledBandEnd(i, frameSize)
		var sumSq float64
		for j := start; j < end && j < len(mdct); j++ {
			sumSq += mdct[j] * mdct[j]
		}
		mdctL2 := math.Sqrt(sumSq)

		// Expected gain (with eMeans)
		eWithMeans := energies[i]
		if i < len(eMeans) {
			eWithMeans += eMeans[i] * DB6
		}
		if eWithMeans > 32 {
			eWithMeans = 32
		}
		gainWithMeans := math.Exp2(eWithMeans / DB6)

		t.Logf("Band %d: mdctL2=%.4f, energy=%.2f dB, e+eMeans=%.2f dB, gainWithMeans=%.4f",
			i, mdctL2, energies[i], eWithMeans, gainWithMeans)
	}

	// Do full encode
	packet, err := enc.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	t.Logf("\nEncoded packet: %d bytes", len(packet))
	t.Logf("First 10 bytes: %x", packet[:10])

	// === DECODER SIDE ===
	dec := celt.NewDecoder(1)
	decoded, err := dec.DecodeFrame(packet, frameSize)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	t.Logf("\n=== RESULTS ===")
	t.Logf("Original max: %.4f", maxAbsF(pcm))
	t.Logf("Decoded max:  %.4f", maxAbsF(decoded))
	t.Logf("Ratio: %.2f", maxAbsF(decoded)/maxAbsF(pcm))

	// Print first 20 samples like the other test
	t.Log("\nFirst 20 samples:")
	t.Log("  i      original     decoded")
	for i := 0; i < 20 && i < len(pcm) && i < len(decoded); i++ {
		t.Logf("%3d  %10.5f  %10.5f", i, pcm[i], decoded[i])
	}
}

func maxAbsF(s []float64) float64 {
	max := 0.0
	for _, v := range s {
		if math.Abs(v) > max {
			max = math.Abs(v)
		}
	}
	return max
}
