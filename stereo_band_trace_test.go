package gopus

import (
	"fmt"
	"math"
	"testing"
)

// TestStereoBandAmplitudeTrace encodes stereo at 64kbps and decodes,
// then examines per-band amplitude to find which bands have inflated energy.
func TestStereoBandAmplitudeTrace(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960
		numFrames  = 40
		amplitude  = 0.5
		bitrate    = 64000
	)

	enc, err := NewEncoder(sampleRate, channels, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	enc.SetBitrate(bitrate)

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatal(err)
	}

	pcmIn := make([]float32, frameSize*channels)
	packet := make([]byte, 4000)
	pcmOut := make([]float32, frameSize*channels)

	for f := 0; f < numFrames; f++ {
		// Generate stereo sine: L=440Hz, R=554Hz
		for i := 0; i < frameSize; i++ {
			tt := float64(f*frameSize+i) / float64(sampleRate)
			pcmIn[i*2] = float32(amplitude * math.Sin(2*math.Pi*440*tt))
			pcmIn[i*2+1] = float32(amplitude * math.Sin(2*math.Pi*554.37*tt+0.1))
		}
		n, err := enc.Encode(pcmIn, packet)
		if err != nil {
			t.Fatalf("encode error frame %d: %v", f, err)
		}
		if n > 0 {
			_, derr := dec.Decode(packet[:n], pcmOut)
			if derr != nil {
				t.Fatalf("decode error frame %d: %v", f, derr)
			}
		}

		// Only check frame 35 which is in the stable 2x regime
		if f < 34 || f > 36 {
			continue
		}

		var maxL, maxR float64
		var sumSqL, sumSqR float64
		for i := 0; i < frameSize; i++ {
			l := math.Abs(float64(pcmOut[i*2]))
			r := math.Abs(float64(pcmOut[i*2+1]))
			sumSqL += float64(pcmOut[i*2]) * float64(pcmOut[i*2])
			sumSqR += float64(pcmOut[i*2+1]) * float64(pcmOut[i*2+1])
			if l > maxL {
				maxL = l
			}
			if r > maxR {
				maxR = r
			}
		}
		rmsL := math.Sqrt(sumSqL / float64(frameSize))
		rmsR := math.Sqrt(sumSqR / float64(frameSize))
		t.Logf("Frame %d: L_max=%.4f (%.2fx) R_max=%.4f (%.2fx) L_rms=%.4f R_rms=%.4f pkt=%d",
			f, maxL, maxL/amplitude, maxR, maxR/amplitude, rmsL, rmsR, n)
	}

	// Now do the same with mono at 32kbps for comparison
	t.Run("mono_comparison", func(t *testing.T) {
		monoEnc, _ := NewEncoder(sampleRate, 1, ApplicationAudio)
		monoEnc.SetBitrate(32000) // Same per-channel bitrate

		monoDec, _ := NewDecoder(DefaultDecoderConfig(sampleRate, 1))

		pcmInMono := make([]float32, frameSize)
		packetMono := make([]byte, 4000)
		pcmOutMono := make([]float32, frameSize)

		for f := 0; f < numFrames; f++ {
			for i := 0; i < frameSize; i++ {
				tt := float64(f*frameSize+i) / float64(sampleRate)
				pcmInMono[i] = float32(amplitude * math.Sin(2*math.Pi*440*tt))
			}
			n, _ := monoEnc.Encode(pcmInMono, packetMono)
			if n > 0 {
				monoDec.Decode(packetMono[:n], pcmOutMono)
			}
			if f < 34 || f > 36 {
				continue
			}
			var maxV float64
			for i := 0; i < frameSize; i++ {
				v := math.Abs(float64(pcmOutMono[i]))
				if v > maxV {
					maxV = v
				}
			}
			t.Logf("Frame %d: mono max=%.4f (%.2fx) pkt=%d", f, maxV, maxV/amplitude, n)
		}
	})

	// Now try stereo with increasing bitrates to find the threshold
	for _, br := range []int{48000, 56000, 64000, 72000, 80000, 96000, 128000} {
		t.Run(fmt.Sprintf("bitrate_%d", br), func(t *testing.T) {
			encBR, _ := NewEncoder(sampleRate, channels, ApplicationAudio)
			encBR.SetBitrate(br)
			decBR, _ := NewDecoder(DefaultDecoderConfig(sampleRate, channels))

			for f := 0; f < numFrames; f++ {
				for i := 0; i < frameSize; i++ {
					tt := float64(f*frameSize+i) / float64(sampleRate)
					pcmIn[i*2] = float32(amplitude * math.Sin(2*math.Pi*440*tt))
					pcmIn[i*2+1] = float32(amplitude * math.Sin(2*math.Pi*554.37*tt+0.1))
				}
				n, _ := encBR.Encode(pcmIn, packet)
				if n > 0 {
					decBR.Decode(packet[:n], pcmOut)
				}
			}
			// Check last frame
			var maxL, maxR float64
			for i := 0; i < frameSize; i++ {
				l := math.Abs(float64(pcmOut[i*2]))
				r := math.Abs(float64(pcmOut[i*2+1]))
				if l > maxL {
					maxL = l
				}
				if r > maxR {
					maxR = r
				}
			}
			t.Logf("Bitrate %d: L_max=%.4f (%.2fx) R_max=%.4f (%.2fx)",
				br, maxL, maxL/amplitude, maxR, maxR/amplitude)
		})
	}
}
