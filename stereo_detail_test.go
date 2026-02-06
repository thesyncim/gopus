package gopus_test

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

func TestStereoDetail(t *testing.T) {
	const (
		sampleRate = 48000
		frameSize  = 960
		channels   = 2
		numFrames  = 20
	)

	enc, _ := gopus.NewEncoder(sampleRate, channels, gopus.ApplicationAudio)
	enc.SetBitrate(128000)
	dec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, channels))

	// Use chirp signal that varies in frequency to avoid alignment ambiguity
	pcmIn := make([]float32, frameSize*channels*numFrames)
	for i := 0; i < frameSize*numFrames; i++ {
		t := float64(i) / float64(sampleRate)
		freq := 200.0 + 2000.0*t // chirp from 200Hz to 200+2000*dur
		pcmIn[i*2] = float32(0.4 * math.Sin(2*math.Pi*freq*t))
		// Right channel: same signal but 0.5x amplitude
		pcmIn[i*2+1] = float32(0.2 * math.Sin(2*math.Pi*freq*t))
	}

	packet := make([]byte, 4000)
	pcmOut := make([]float32, frameSize*channels)

	// Encode and decode
	for f := 0; f < numFrames; f++ {
		start := f * frameSize * channels
		end := start + frameSize*channels
		n, _ := enc.Encode(pcmIn[start:end], packet)
		if n > 0 {
			dec.Decode(packet[:n], pcmOut)
		}
	}

	// Analyze last decoded frame
	lastFrame := numFrames - 1
	start := lastFrame * frameSize * channels

	// Check amplitude ratio between channels
	var maxInL, maxInR, maxOutL, maxOutR float64
	var rmsInL, rmsInR, rmsOutL, rmsOutR float64
	for i := 0; i < frameSize; i++ {
		inL := math.Abs(float64(pcmIn[start+i*2]))
		inR := math.Abs(float64(pcmIn[start+i*2+1]))
		outL := math.Abs(float64(pcmOut[i*2]))
		outR := math.Abs(float64(pcmOut[i*2+1]))
		if inL > maxInL { maxInL = inL }
		if inR > maxInR { maxInR = inR }
		if outL > maxOutL { maxOutL = outL }
		if outR > maxOutR { maxOutR = outR }
		rmsInL += inL * inL
		rmsInR += inR * inR
		rmsOutL += outL * outL
		rmsOutR += outR * outR
	}
	rmsInL = math.Sqrt(rmsInL / float64(frameSize))
	rmsInR = math.Sqrt(rmsInR / float64(frameSize))
	rmsOutL = math.Sqrt(rmsOutL / float64(frameSize))
	rmsOutR = math.Sqrt(rmsOutR / float64(frameSize))

	t.Logf("Input:  L max=%.4f rms=%.4f, R max=%.4f rms=%.4f", maxInL, rmsInL, maxInR, rmsInR)
	t.Logf("Output: L max=%.4f rms=%.4f, R max=%.4f rms=%.4f", maxOutL, rmsOutL, maxOutR, rmsOutR)
	t.Logf("Input ratio L/R:  max=%.2f rms=%.2f", maxInL/maxInR, rmsInL/rmsInR)
	t.Logf("Output ratio L/R: max=%.2f rms=%.2f", maxOutL/maxOutR, rmsOutL/rmsOutR)

	// Check that L is approximately 2x louder than R (as encoded)
	inputRatio := rmsInL / rmsInR
	outputRatio := rmsOutL / rmsOutR
	ratioDiff := math.Abs(outputRatio - inputRatio) / inputRatio
	t.Logf("Ratio preservation: input=%.2f output=%.2f (%.1f%% error)", inputRatio, outputRatio, ratioDiff*100)

	if ratioDiff > 0.5 {
		t.Errorf("Stereo amplitude ratio poorly preserved: expected %.2f got %.2f (>50%% error)", inputRatio, outputRatio)
	}

	// Check correlation: L and R should be highly correlated (same shape, different amplitude)
	var corr, sumL2, sumR2 float64
	for i := 0; i < frameSize; i++ {
		l := float64(pcmOut[i*2])
		r := float64(pcmOut[i*2+1])
		corr += l * r
		sumL2 += l * l
		sumR2 += r * r
	}
	if sumL2 > 0 && sumR2 > 0 {
		corrCoef := corr / math.Sqrt(sumL2*sumR2)
		t.Logf("Output L/R correlation: %.4f", corrCoef)
		if corrCoef < 0.8 {
			t.Errorf("Stereo channels should be correlated (same signal, different amplitude): got %.4f", corrCoef)
		}
	}

	// Test with truly distinct signals (different frequencies)
	t.Log("\n--- Test with distinct L/R frequencies ---")
	enc2, _ := gopus.NewEncoder(sampleRate, channels, gopus.ApplicationAudio)
	enc2.SetBitrate(128000)
	dec2, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, channels))

	pcmIn2 := make([]float32, frameSize*channels*numFrames)
	for i := 0; i < frameSize*numFrames; i++ {
		t := float64(i) / float64(sampleRate)
		pcmIn2[i*2] = float32(0.4 * math.Sin(2*math.Pi*300*t))      // L: 300Hz
		pcmIn2[i*2+1] = float32(0.4 * math.Sin(2*math.Pi*1000*t))   // R: 1000Hz
	}

	for f := 0; f < numFrames; f++ {
		start := f * frameSize * channels
		end := start + frameSize*channels
		n, _ := enc2.Encode(pcmIn2[start:end], packet)
		if n > 0 {
			dec2.Decode(packet[:n], pcmOut)
		}
	}

	// Measure frequency content of decoded L and R
	// Simple: count zero crossings to estimate frequency
	var zerosL, zerosR int
	for i := 1; i < frameSize; i++ {
		if pcmOut[(i-1)*2] * pcmOut[i*2] < 0 { zerosL++ }
		if pcmOut[(i-1)*2+1] * pcmOut[i*2+1] < 0 { zerosR++ }
	}
	estFreqL := float64(zerosL) / 2.0 * float64(sampleRate) / float64(frameSize)
	estFreqR := float64(zerosR) / 2.0 * float64(sampleRate) / float64(frameSize)
	t.Logf("Distinct signals: L est freq=%.0fHz (expected 300), R est freq=%.0fHz (expected 1000)", estFreqL, estFreqR)

	if math.Abs(estFreqL-300) > 100 {
		t.Errorf("Left channel frequency wrong: expected ~300Hz, got ~%.0fHz", estFreqL)
	}
	if math.Abs(estFreqR-1000) > 200 {
		t.Errorf("Right channel frequency wrong: expected ~1000Hz, got ~%.0fHz", estFreqR)
	}
}
