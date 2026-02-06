package gopus_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

func TestStereoRoundTripDiag(t *testing.T) {
	const (
		sampleRate = 48000
		frameSize  = 960
		channels   = 2
		numFrames  = 20
	)

	// Create encoder with ApplicationAudio (CELT mode)
	enc, err := gopus.NewEncoder(sampleRate, channels, gopus.ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	enc.SetBitrate(128000)

	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	// Generate stereo signal: left=440Hz, right=554Hz (distinct per channel)
	pcmIn := make([]float32, frameSize*channels*numFrames)
	for i := 0; i < frameSize*numFrames; i++ {
		t := float64(i) / float64(sampleRate)
		left := float32(0.5 * math.Sin(2*math.Pi*440*t))
		right := float32(0.5 * math.Sin(2*math.Pi*554.37*t))
		pcmIn[i*2] = left
		pcmIn[i*2+1] = right
	}

	packet := make([]byte, 4000)
	pcmOut := make([]float32, frameSize*channels)

	// Encode and decode all frames, measure quality of last few
	var totalLeftEnergy, totalRightEnergy float64
	var totalLeftErrEnergy, totalRightErrEnergy float64
	measureStart := 10 // skip first 10 frames for warmup

	for f := 0; f < numFrames; f++ {
		start := f * frameSize * channels
		end := start + frameSize*channels
		frame := pcmIn[start:end]

		n, err := enc.Encode(frame, packet)
		if err != nil {
			t.Fatalf("Encode frame %d: %v", f, err)
		}
		if n == 0 {
			t.Logf("Frame %d: DTX suppressed", f)
			continue
		}

		toc := gopus.ParseTOC(packet[0])
		if f == 0 {
			t.Logf("TOC: config=%d mode=%d bw=%d stereo=%v frameCode=%d", toc.Config, toc.Mode, toc.Bandwidth, toc.Stereo, toc.FrameCode)
		}

		samples, err := dec.Decode(packet[:n], pcmOut)
		if err != nil {
			t.Fatalf("Decode frame %d: %v", f, err)
		}
		if samples != frameSize {
			t.Fatalf("Expected %d samples, got %d", frameSize, samples)
		}

		if f >= measureStart {
			for i := 0; i < frameSize; i++ {
				inL := float64(frame[i*2])
				inR := float64(frame[i*2+1])
				outL := float64(pcmOut[i*2])
				outR := float64(pcmOut[i*2+1])

				totalLeftEnergy += inL * inL
				totalRightEnergy += inR * inR
				totalLeftErrEnergy += (inL - outL) * (inL - outL)
				totalRightErrEnergy += (inR - outR) * (inR - outR)
			}
		}
	}

	leftSNR := 10 * math.Log10(totalLeftEnergy/totalLeftErrEnergy)
	rightSNR := 10 * math.Log10(totalRightEnergy/totalRightErrEnergy)

	t.Logf("Left channel SNR:  %.1f dB", leftSNR)
	t.Logf("Right channel SNR: %.1f dB", rightSNR)

	// Also check that channels are distinct (not both the same)
	// Compare decoded L and R channels: correlation should be low
	var corrSum, leftSum2, rightSum2 float64
	for i := frameSize * measureStart * channels; i < frameSize*numFrames*channels; i += 2 {
		l := float64(pcmOut[0]) // reuse last decoded frame
		r := float64(pcmOut[1])
		corrSum += l * r
		leftSum2 += l * l
		rightSum2 += r * r
	}

	// Check first sample of each decoded frame to see if L != R
	enc2, _ := gopus.NewEncoder(sampleRate, channels, gopus.ApplicationAudio)
	enc2.SetBitrate(128000)
	dec2, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, channels))

	// Encode 15 frames, check last few
	for f := 0; f < 15; f++ {
		start := f * frameSize * channels
		end := start + frameSize*channels
		frame := pcmIn[start:end]
		n, _ := enc2.Encode(frame, packet)
		if n > 0 {
			dec2.Decode(packet[:n], pcmOut)
		}
	}

	// Analyze last decoded frame
	var maxL, maxR float32
	var diffCount int
	for i := 0; i < frameSize; i++ {
		l := pcmOut[i*2]
		r := pcmOut[i*2+1]
		if math.Abs(float64(l)) > float64(maxL) { maxL = float32(math.Abs(float64(l))) }
		if math.Abs(float64(r)) > float64(maxR) { maxR = float32(math.Abs(float64(r))) }
		if math.Abs(float64(l-r)) > 0.01 {
			diffCount++
		}
	}

	t.Logf("Last frame: maxL=%.4f maxR=%.4f diffSamples=%d/%d",
		maxL, maxR, diffCount, frameSize)

	if maxL < 0.01 && maxR < 0.01 {
		t.Error("Both channels are near-silent - stereo encoding is broken")
	}
	if diffCount < frameSize/10 {
		t.Errorf("Left and right channels are too similar (%d/%d differ) - stereo separation lost",
			diffCount, frameSize)
	}

	// Print first few samples for inspection
	t.Logf("First 5 output samples:")
	for i := 0; i < 5 && i < frameSize; i++ {
		inL := pcmIn[(numFrames-1)*frameSize*2 + i*2]
		inR := pcmIn[(numFrames-1)*frameSize*2 + i*2+1]
		t.Logf("  [%d] in=(%.4f, %.4f) out=(%.4f, %.4f)",
			i, inL, inR, pcmOut[i*2], pcmOut[i*2+1])
	}

	fmt.Printf("SUMMARY: Left SNR=%.1f dB, Right SNR=%.1f dB, Channel diff=%d/%d\n",
		leftSNR, rightSNR, diffCount, frameSize)
}
