package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

func TestTraceEncoding(t *testing.T) {
	// Generate 1 frame of simple sine wave
	sampleRate := 48000
	frameSize := 960
	freq := 440.0

	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		ti := float64(i) / float64(sampleRate)
		pcm[i] = 0.5 * math.Sin(2*math.Pi*freq*ti)
	}

	// Create CELT encoder directly to trace internals
	enc := celt.NewEncoder(1) // 1 channel (mono)
	enc.SetBitrate(64000)

	// Manually trace the encoding steps
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	t.Logf("Frame size: %d, nbBands: %d", frameSize, nbBands)

	// Apply pre-emphasis
	preemph := enc.ApplyPreemphasis(pcm)
	t.Logf("Pre-emphasis applied, len=%d, max=%.4f", len(preemph), maxAbs(preemph))

	// Compute MDCT (simplified - no overlap)
	mdct := celt.MDCT(append(make([]float64, frameSize), preemph...))
	t.Logf("MDCT computed, len=%d, max=%.4f", len(mdct), maxAbs(mdct))

	// Compute band energies
	energies := enc.ComputeBandEnergies(mdct, nbBands, frameSize)
	t.Logf("Raw energies (first 5 bands):")
	for i := 0; i < 5 && i < len(energies); i++ {
		t.Logf("  Band %d: energy=%.4f dB", i, energies[i])
	}

	// What the decoder does: e = energy + eMeans*DB6, gain = 2^(e/DB6)
	// The encoder's NormalizeBands now also adds eMeans, so gains should match.
	t.Logf("\nGain calculations:")
	eMeans := []float64{6.4375, 6.25, 5.75, 5.3125, 5.0625} // from tables.go
	DB6 := 6.0
	for i := 0; i < 5 && i < len(energies); i++ {
		rawGain := math.Exp2(energies[i] / DB6)
		withMeans := energies[i] + eMeans[i]*DB6
		if withMeans > 32 {
			withMeans = 32
		}
		decoderGain := math.Exp2(withMeans / DB6)
		// Now encoder should also use decoderGain (with eMeans)
		t.Logf("  Band %d: energy=%.2f dB, oldGain=%.4f, newGain(+eMeans)=%.4f", i, energies[i], rawGain, decoderGain)
	}

	// Test normalize-denormalize round trip
	t.Logf("\nNormalize-Denormalize test:")
	shapes := enc.NormalizeBands(mdct, energies, nbBands, frameSize)
	offset := 0
	for band := 0; band < 5 && band < len(shapes); band++ {
		shape := shapes[band]
		if len(shape) == 0 {
			continue
		}

		// Compute L2 norm of shape
		var norm float64
		for _, v := range shape {
			norm += v * v
		}
		norm = math.Sqrt(norm)

		// Original magnitude in this band
		n := len(shape)
		var origMag float64
		for i := 0; i < n && offset+i < len(mdct); i++ {
			origMag += mdct[offset+i] * mdct[offset+i]
		}
		origMag = math.Sqrt(origMag)

		t.Logf("  Band %d: width=%d, shapeL2Norm=%.4f, origL2=%.4f", band, n, norm, origMag)
		offset += n
	}
}

func maxAbs(s []float64) float64 {
	max := 0.0
	for _, v := range s {
		if math.Abs(v) > max {
			max = math.Abs(v)
		}
	}
	return max
}
