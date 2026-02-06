package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
)

// TestStereoMergeDebug enables stereo merge debug and traces the values.
func TestStereoMergeDebug(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960
		amplitude  = 0.5
	)

	// Enable debug
	celt.DebugStereoMerge = true
	defer func() { celt.DebugStereoMerge = false }()

	enc, err := NewEncoder(sampleRate, channels, ApplicationAudio)
	if err != nil {
		t.Fatal(err)
	}
	enc.SetBitrate(64000)

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatal(err)
	}

	pcmIn := make([]float32, frameSize*channels)
	packet := make([]byte, 4000)
	pcmOut := make([]float32, frameSize*channels)

	// Warm up for 34 frames, then decode frame 35 with debug
	for f := 0; f < 36; f++ {
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
			if f == 35 {
				t.Logf("=== Decoding frame 35 (should show 2x issue) ===")
			}
			_, derr := dec.Decode(packet[:n], pcmOut)
			if derr != nil {
				t.Fatalf("decode error frame %d: %v", f, derr)
			}
			if f == 35 {
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
				t.Logf("Frame 35: L_max=%.4f (%.2fx) R_max=%.4f (%.2fx)",
					maxL, maxL/amplitude, maxR, maxR/amplitude)
			}
		}
	}
}
