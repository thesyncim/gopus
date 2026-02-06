package encoder_test

import (
	"fmt"
	"math"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

// TestDiagnose10msPipeline traces quality through the full Opus pipeline
// to identify where the 2x amplitude corruption starts.
func TestDiagnose10msPipeline(t *testing.T) {
	channels := 1
	freqs := []float64{440, 1000, 2000}
	amp := 0.3
	modFreqs := []float64{1.3, 2.7, 0.9}

	frameSize := 480 // 10ms at 48kHz
	numFrames := 60
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

	enc := encoder.NewEncoder(48000, channels)
	enc.SetMode(encoder.ModeSILK)
	enc.SetBandwidth(types.BandwidthNarrowband)
	enc.SetBitrate(32000)

	dec, _ := gopus.NewDecoder(gopus.DecoderConfig{SampleRate: 48000, Channels: channels})

	allDecoded := make([]float32, 0, totalSamples+5000)

	fmt.Println("Frame  pktLen  decLen  origRMS     decRMS      ratio    marker")
	for i := 0; i < numFrames; i++ {
		start := i * frameSize
		end := start + frameSize
		pcm64 := make([]float64, end-start)
		for j, v := range original[start:end] {
			pcm64[j] = float64(v)
		}

		// Check resampled signal
		packet, _ := enc.Encode(pcm64, frameSize)
		if len(packet) == 0 {
			continue
		}
		pkt := make([]byte, len(packet))
		copy(pkt, packet)

		decoded := make([]float32, frameSize*channels)
		n, _ := dec.Decode(pkt, decoded)
		frameDecoded := decoded[:n]
		allDecoded = append(allDecoded, frameDecoded...)

		// Compute RMS
		var origE, decE float64
		for j := start; j < end; j++ {
			origE += float64(original[j]) * float64(original[j])
		}
		origRMS := math.Sqrt(origE / float64(frameSize))
		for _, v := range frameDecoded {
			decE += float64(v) * float64(v)
		}
		decRMS := math.Sqrt(decE / float64(n))

		ratio := decRMS / (origRMS + 1e-10)
		marker := ""
		if ratio > 1.5 || ratio < 0.3 {
			marker = " <-- BAD"
		}

		if i >= 20 || marker != "" {
			fmt.Printf("  %2d    %3d    %4d    %.4f    %.4f    %.2f%s\n",
				i, len(pkt), n, origRMS, decRMS, ratio, marker)
		}
	}

	// Compute overall SNR
	delay := 315
	var sigPow, noisePow float64
	count := 0
	for i := 480; i < len(original)-480; i++ {
		di := i + delay
		if di >= 480 && di < len(allDecoded)-480 {
			ref := float64(original[i])
			dec := float64(allDecoded[di])
			sigPow += ref * ref
			noise := dec - ref
			noisePow += noise * noise
			count++
		}
	}
	snr := math.Inf(-1)
	if count > 1000 && sigPow > 0 && noisePow > 0 {
		snr = 10.0 * math.Log10(sigPow/noisePow)
	}
	fmt.Printf("\nOverall SNR=%.2f dB at delay=%d (count=%d)\n", snr, delay, count)
}

// TestDiagnose20msPipeline traces quality through the full Opus pipeline for 20ms
func TestDiagnose20msPipeline(t *testing.T) {
	channels := 1
	freqs := []float64{440, 1000, 2000}
	amp := 0.3
	modFreqs := []float64{1.3, 2.7, 0.9}

	frameSize := 960 // 20ms at 48kHz
	numFrames := 30
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

	enc := encoder.NewEncoder(48000, channels)
	enc.SetMode(encoder.ModeSILK)
	enc.SetBandwidth(types.BandwidthNarrowband)
	enc.SetBitrate(32000)

	dec, _ := gopus.NewDecoder(gopus.DecoderConfig{SampleRate: 48000, Channels: channels})

	allDecoded := make([]float32, 0, totalSamples+5000)

	fmt.Println("Frame  pktLen  decLen  origRMS     decRMS      ratio")
	for i := 0; i < numFrames; i++ {
		start := i * frameSize
		end := start + frameSize
		pcm64 := make([]float64, end-start)
		for j, v := range original[start:end] {
			pcm64[j] = float64(v)
		}
		packet, _ := enc.Encode(pcm64, frameSize)
		if len(packet) == 0 {
			continue
		}
		pkt := make([]byte, len(packet))
		copy(pkt, packet)

		decoded := make([]float32, frameSize*channels)
		n, _ := dec.Decode(pkt, decoded)
		frameDecoded := decoded[:n]
		allDecoded = append(allDecoded, frameDecoded...)

		var origE, decE float64
		for j := start; j < end; j++ {
			origE += float64(original[j]) * float64(original[j])
		}
		origRMS := math.Sqrt(origE / float64(frameSize))
		for _, v := range frameDecoded {
			decE += float64(v) * float64(v)
		}
		decRMS := math.Sqrt(decE / float64(n))
		ratio := decRMS / (origRMS + 1e-10)
		fmt.Printf("  %2d    %3d    %4d    %.4f    %.4f    %.2f\n",
			i, len(pkt), n, origRMS, decRMS, ratio)
	}

	delay := 315
	var sigPow, noisePow float64
	count := 0
	for i := 960; i < len(original)-960; i++ {
		di := i + delay
		if di >= 960 && di < len(allDecoded)-960 {
			ref := float64(original[i])
			dec := float64(allDecoded[di])
			sigPow += ref * ref
			noise := dec - ref
			noisePow += noise * noise
			count++
		}
	}
	snr := math.Inf(-1)
	if count > 1000 && sigPow > 0 && noisePow > 0 {
		snr = 10.0 * math.Log10(sigPow/noisePow)
	}
	fmt.Printf("\nOverall SNR=%.2f dB at delay=%d (count=%d)\n", snr, delay, count)
}
