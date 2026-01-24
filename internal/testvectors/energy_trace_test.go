package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func TestEnergyEncodeDecode(t *testing.T) {
	// Generate 1 frame of simple sine wave
	sampleRate := 48000
	frameSize := 960
	freq := 440.0

	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		ti := float64(i) / float64(sampleRate)
		pcm[i] = 0.5 * math.Sin(2*math.Pi*freq*ti)
	}

	// Create encoder
	enc := celt.NewEncoder(1) // 1 channel (mono)
	enc.SetBitrate(64000)

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	// Apply pre-emphasis
	preemph := enc.ApplyPreemphasis(pcm)

	// Compute MDCT
	mdct := celt.MDCT(append(make([]float64, frameSize), preemph...))

	// Compute band energies
	energies := enc.ComputeBandEnergies(mdct, nbBands, frameSize)

	t.Logf("Raw energies (first 10 bands):")
	for i := 0; i < 10 && i < len(energies); i++ {
		t.Logf("  Band %d: %.4f dB", i, energies[i])
	}

	// Encode energies
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)
	enc.SetRangeEncoder(re)

	quantizedEnergies := enc.EncodeCoarseEnergy(energies, nbBands, true, lm)

	t.Logf("\nQuantized energies (first 10 bands):")
	for i := 0; i < 10 && i < len(quantizedEnergies); i++ {
		diff := quantizedEnergies[i] - energies[i]
		t.Logf("  Band %d: %.4f dB (diff from raw: %.4f)", i, quantizedEnergies[i], diff)
	}

	// Finish encoding
	encodedData := re.Done()
	t.Logf("\nEncoded coarse energy data: %d bytes", len(encodedData))

	// Now decode and compare
	rd := &rangecoding.Decoder{}
	rd.Init(encodedData)
	dec := celt.NewDecoder(1) // 1 channel (mono)
	dec.SetRangeDecoder(rd)

	decodedEnergies := dec.DecodeCoarseEnergy(nbBands, true, lm)

	t.Logf("\nDecoded energies (first 10 bands):")
	for i := 0; i < 10 && i < len(decodedEnergies); i++ {
		matchStr := "OK"
		diff := math.Abs(decodedEnergies[i] - quantizedEnergies[i])
		if diff > 0.01 {
			matchStr = "MISMATCH"
		}
		t.Logf("  Band %d: %.4f dB (quantized was %.4f) %s", i, decodedEnergies[i], quantizedEnergies[i], matchStr)
	}

	// Check if encode/decode round-trip matches
	totalDiff := 0.0
	for i := 0; i < nbBands && i < len(decodedEnergies) && i < len(quantizedEnergies); i++ {
		totalDiff += math.Abs(decodedEnergies[i] - quantizedEnergies[i])
	}
	t.Logf("\nTotal energy difference: %.4f", totalDiff)
}
