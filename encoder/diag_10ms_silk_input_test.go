package encoder

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

// TestDiagnose10msSilkInput traces what happens by replicating the encoder pipeline.
// We use the encoder's internal state to compare with a direct SILK-only path.
func TestDiagnose10msSilkInput(t *testing.T) {
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

	// Path 1: Full Opus encoder pipeline
	enc := NewEncoder(48000, channels)
	enc.SetMode(ModeSILK)
	enc.SetBandwidth(types.BandwidthNarrowband)
	enc.SetBitrate(32000)

	// Path 2: Manual replication of encoder pipeline to feed SILK directly
	resampler := silk.NewDownsamplingResampler(48000, 8000)
	silkEnc := silk.NewEncoder(silk.BandwidthNarrowband)
	silkEnc.SetBitrate(32000)
	silkDec := silk.NewDecoder()

	var silkMonoInputHist [2]float32

	fmt.Println("Frame  opusPktLen  silkDirRMS   opusDecRMS48k  silkDecRMS8k  marker")
	for i := 0; i < numFrames; i++ {
		start := i * frameSize
		end := start + frameSize
		pcm64 := make([]float64, end-start)
		for j, v := range original[start:end] {
			pcm64[j] = float64(v)
		}

		// ===== Full Opus path =====
		packet, _ := enc.Encode(pcm64, frameSize)
		opusPktLen := 0
		if packet != nil {
			opusPktLen = len(packet)
		}

		// Decode with gopus decoder (48kHz output) - simplified, just check packet
		_ = opusPktLen

		// ===== Direct SILK path (manual pipeline replication) =====
		// Step 1: Convert to float32
		pcm32 := make([]float32, frameSize)
		for j, v := range pcm64 {
			pcm32[j] = float32(v)
		}

		// Step 2: DC reject (simplified version matching encoder)
		// Skip DC reject for now since we showed it barely changes anything

		// Step 3: Resample 48kHz -> 8kHz
		targetSamples := frameSize * 8000 / 48000 // 80
		resampled := make([]float32, targetSamples)
		n := resampler.ProcessInto(pcm32, resampled)
		resampled = resampled[:n]

		// Step 4: alignSilkMonoInput
		aligned := make([]float32, len(resampled))
		aligned[0] = silkMonoInputHist[1]
		copy(aligned[1:], resampled[:len(resampled)-1])
		if len(resampled) >= 2 {
			silkMonoInputHist[0] = resampled[len(resampled)-2]
			silkMonoInputHist[1] = resampled[len(resampled)-1]
		}

		// Step 5: Encode with silk directly
		silkPkt := silkEnc.EncodeFrame(aligned, nil, true)
		silkPktLen := 0
		if silkPkt != nil {
			silkPktLen = len(silkPkt)
		}

		// Step 6: Decode with silk decoder
		var silkDecRMS float64
		if silkPkt != nil {
			cp := make([]byte, len(silkPkt))
			copy(cp, silkPkt)
			fsAt48k := 80 * 48000 / 8000 // 480 at 48kHz
			out, err := silkDec.Decode(cp, silk.BandwidthNarrowband, fsAt48k, true)
			if err == nil {
				for _, v := range out {
					silkDecRMS += float64(v) * float64(v)
				}
				if len(out) > 0 {
					silkDecRMS = math.Sqrt(silkDecRMS / float64(len(out)))
				}
			}
		}

		origRMS := rms32v3(original[start:end])
		ratio := silkDecRMS / (origRMS + 1e-10)

		marker := ""
		if ratio > 1.5 || ratio < 0.3 {
			marker = " <-- BAD"
		}

		fmt.Printf("  %2d    %3d / %3d   %.4f    %.4f    %.2f%s\n",
			i, opusPktLen, silkPktLen, origRMS, silkDecRMS, ratio, marker)
	}
}

func rms32v3(buf []float32) float64 {
	var sum float64
	for _, v := range buf {
		sum += float64(v) * float64(v)
	}
	if len(buf) == 0 {
		return 0
	}
	return math.Sqrt(sum / float64(len(buf)))
}
