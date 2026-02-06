package gopus

import (
	"fmt"
	"math"
	"testing"
)

// TestStereoEnergyTrace encodes a stereo sine wave at 64kbps and 128kbps
// and traces the max decoded amplitude per frame to see where the inflation occurs.
func TestStereoEnergyTrace(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960
		numFrames  = 50
		amplitude  = 0.5
	)

	for _, bitrate := range []int{64000, 128000} {
		t.Run(fmt.Sprintf("bitrate_%d", bitrate), func(t *testing.T) {
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

			var prevMaxL, prevMaxR float64
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
				if f < 5 || f%10 == 0 || f == numFrames-1 ||
					maxL > amplitude*1.3 || maxR > amplitude*1.3 ||
					(prevMaxL > 0 && maxL/prevMaxL > 1.1) ||
					(prevMaxR > 0 && maxR/prevMaxR > 1.1) {
					t.Logf("Frame %3d: L_max=%.4f (%.1f dBFS, gain=%.2fx) R_max=%.4f (%.1f dBFS, gain=%.2fx) pkt=%d",
						f, maxL, 20*math.Log10(maxL+1e-12), maxL/amplitude,
						maxR, 20*math.Log10(maxR+1e-12), maxR/amplitude, n)
				}
				prevMaxL = maxL
				prevMaxR = maxR
			}
		})
	}
}

// TestMonoEnergyTrace encodes a mono sine wave at matching per-channel bitrate for comparison.
func TestMonoEnergyTrace(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		numFrames  = 50
		amplitude  = 0.5
	)

	for _, bitrate := range []int{32000, 64000} {
		t.Run(fmt.Sprintf("bitrate_%d", bitrate), func(t *testing.T) {
			enc, err := NewEncoder(sampleRate, channels, ApplicationAudio)
			if err != nil {
				t.Fatal(err)
			}
			enc.SetBitrate(bitrate)

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatal(err)
			}

			pcmIn := make([]float32, frameSize)
			packet := make([]byte, 4000)
			pcmOut := make([]float32, frameSize)

			for f := 0; f < numFrames; f++ {
				for i := 0; i < frameSize; i++ {
					tt := float64(f*frameSize+i) / float64(sampleRate)
					pcmIn[i] = float32(amplitude * math.Sin(2*math.Pi*440*tt))
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
				var maxV float64
				for i := 0; i < frameSize; i++ {
					v := math.Abs(float64(pcmOut[i]))
					if v > maxV {
						maxV = v
					}
				}
				if f < 5 || f%10 == 0 || f == numFrames-1 || maxV > amplitude*1.3 {
					t.Logf("Frame %3d: max=%.4f (%.1f dBFS, gain=%.2fx) pkt=%d",
						f, maxV, 20*math.Log10(maxV+1e-12), maxV/amplitude, n)
				}
			}
		})
	}
}
