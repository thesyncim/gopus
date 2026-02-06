package encoder

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

// TestDiagnose10msResampleTrace traces signal through the encoder pipeline
// to find where the 2x amplitude corruption originates.
func TestDiagnose10msResampleTrace(t *testing.T) {
	channels := 1
	freqs := []float64{440, 1000, 2000}
	amp := 0.3
	modFreqs := []float64{1.3, 2.7, 0.9}

	frameSize := 480 // 10ms at 48kHz
	numFrames := 50
	totalSamples := numFrames * frameSize * channels
	original := make([]float32, totalSamples)
	for i := 0; i < totalSamples; i++ {
		t := float64(i) / 48000.0
		var val float64
		for fi, freq := range freqs {
			modDepth := 0.5 + 0.5*math.Sin(2*math.Pi*modFreqs[fi]*t)
			val += amp * modDepth * math.Sin(2*math.Pi*freq*t)
		}
		onsetSamples := int(0.010 * 48000)
		if i < onsetSamples {
			frac := float64(i) / float64(onsetSamples)
			val *= frac * frac * frac
		}
		original[i] = float32(val)
	}

	enc := NewEncoder(48000, channels)
	enc.SetMode(ModeSILK)
	enc.SetBandwidth(types.BandwidthNarrowband)
	enc.SetBitrate(32000)

	// Track the resampled signal by manually running the pipeline
	resampler := silk.NewDownsamplingResampler(48000, 8000)

	fmt.Println("Frame  origRMS48k  dcRMS48k   resampRMS8k  alignedRMS8k")
	for i := 0; i < numFrames; i++ {
		start := i * frameSize
		end := start + frameSize
		pcm64 := make([]float64, end-start)
		for j, v := range original[start:end] {
			pcm64[j] = float64(v)
		}

		// Step 1: Original RMS
		origRMS := rms32(original[start:end])

		// Step 2: DC reject (emulate encoder)
		dcFiltered := enc.dcReject(pcm64, frameSize)
		dcF32 := make([]float32, len(dcFiltered))
		for j, v := range dcFiltered {
			dcF32[j] = float32(v)
		}
		dcRMS := rms32(dcF32)

		// Step 3: Resample 48kHz -> 8kHz
		targetSamples := frameSize * 8000 / 48000 // 80 samples
		resampledBuf := make([]float32, targetSamples)
		n := resampler.ProcessInto(dcF32, resampledBuf)
		resampledBuf = resampledBuf[:n]
		resampRMS := rms32(resampledBuf)

		// Step 4: alignSilkMonoInput
		aligned := enc.alignSilkMonoInput(resampledBuf)
		alignedRMS := rms32(aligned)

		marker := ""
		if i >= 25 && i <= 50 {
			marker = " *"
		}

		fmt.Printf("  %2d    %.4f     %.4f      %.4f       %.4f%s\n",
			i, origRMS, dcRMS, resampRMS, alignedRMS, marker)

		// Actually encode too (to advance encoder state)
		enc.Encode(pcm64, frameSize)
	}
}

func rms32(buf []float32) float64 {
	var sum float64
	for _, v := range buf {
		sum += float64(v) * float64(v)
	}
	if len(buf) == 0 {
		return 0
	}
	return math.Sqrt(sum / float64(len(buf)))
}
