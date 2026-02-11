package gopus_test

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

func TestStereoRoundTripRegression(t *testing.T) {
	const (
		sampleRate = 48000
		frameSize  = 960
		channels   = 2
		numFrames  = 20
		measureAt  = 10
	)

	enc, err := gopus.NewEncoder(sampleRate, channels, gopus.ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	enc.SetBitrate(128000)

	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	packet := make([]byte, 4000)
	pcmOut := make([]float32, frameSize*channels)

	var totalLeftEnergy, totalRightEnergy float64
	var totalLeftErrEnergy, totalRightErrEnergy float64

	for f := 0; f < numFrames; f++ {
		frame := make([]float32, frameSize*channels)
		base := f * frameSize
		for i := 0; i < frameSize; i++ {
			tm := float64(base+i) / float64(sampleRate)
			frame[i*2] = float32(0.5 * math.Sin(2*math.Pi*440*tm))
			frame[i*2+1] = float32(0.5 * math.Sin(2*math.Pi*554.37*tm))
		}

		n, err := enc.Encode(frame, packet)
		if err != nil {
			t.Fatalf("Encode frame %d: %v", f, err)
		}
		if n == 0 {
			continue
		}

		samples, err := dec.Decode(packet[:n], pcmOut)
		if err != nil {
			t.Fatalf("Decode frame %d: %v", f, err)
		}
		if samples != frameSize {
			t.Fatalf("samples=%d want %d", samples, frameSize)
		}

		if f < measureAt {
			continue
		}
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

	if totalLeftErrEnergy <= 0 || totalRightErrEnergy <= 0 {
		t.Fatal("unexpected zero error energy in stereo roundtrip")
	}

	leftSNR := 10 * math.Log10(totalLeftEnergy/totalLeftErrEnergy)
	rightSNR := 10 * math.Log10(totalRightEnergy/totalRightErrEnergy)
	if math.IsNaN(leftSNR) || math.IsNaN(rightSNR) {
		t.Fatalf("invalid SNR values: L=%.2f R=%.2f", leftSNR, rightSNR)
	}
	if leftSNR < -30 || rightSNR < -30 {
		t.Fatalf("stereo SNR collapsed: L=%.2f dB R=%.2f dB", leftSNR, rightSNR)
	}

	var maxL, maxR float64
	var diffCount int
	for i := 0; i < frameSize; i++ {
		l := math.Abs(float64(pcmOut[i*2]))
		r := math.Abs(float64(pcmOut[i*2+1]))
		if l > maxL {
			maxL = l
		}
		if r > maxR {
			maxR = r
		}
		if math.Abs(float64(pcmOut[i*2]-pcmOut[i*2+1])) > 0.01 {
			diffCount++
		}
	}

	if maxL < 0.01 && maxR < 0.01 {
		t.Fatal("stereo output is near-silent")
	}
	if diffCount < frameSize/10 {
		t.Fatalf("stereo separation too low: diff=%d/%d", diffCount, frameSize)
	}
}
