package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
)

// TestStereoEnergyCompare directly compares encoder band energies at 64kbps vs 128kbps
// to see if the energies themselves are inflated.
func TestStereoEnergyCompare(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960
		numFrames  = 40
		amplitude  = 0.5
	)

	for _, bitrate := range []int{64000, 128000} {
		t.Run("celt_direct", func(t *testing.T) {
			enc := celt.NewEncoder(channels)
			enc.SetBitrate(bitrate)
			enc.SetVBR(true)
			enc.SetConstrainedVBR(true)
			enc.SetComplexity(10)

			dec := celt.NewDecoder(channels)

			pcm := make([]float64, frameSize*channels)

			for f := 0; f < numFrames; f++ {
				for i := 0; i < frameSize; i++ {
					tt := float64(f*frameSize+i) / float64(sampleRate)
					pcm[i*2] = amplitude * math.Sin(2*math.Pi*440*tt)
					pcm[i*2+1] = amplitude * math.Sin(2*math.Pi*554.37*tt+0.1)
				}
				packet, err := enc.EncodeFrame(pcm, frameSize)
				if err != nil {
					t.Fatalf("encode error frame %d: %v", f, err)
				}
				if len(packet) > 0 {
					dec.DecodeFrame(packet, frameSize)
				}

				if f == numFrames-1 {
					encE := enc.PrevEnergy()
					decE := dec.PrevEnergy()
					nBands := 21
					t.Logf("Bitrate %d, Frame %d:", bitrate, f)
					if encE != nil && len(encE) >= nBands*2 {
						for b := 0; b < nBands; b++ {
							eL := encE[b]
							eR := encE[nBands+b]
							dL := decE[b]
							dR := decE[nBands+b]
							// Denormalize: gain = exp2(e + eMeans[b])
							// The actual scaling factor applied to the coefficients
							t.Logf("  Band %2d: encL=%.3f encR=%.3f decL=%.3f decR=%.3f diffL=%.3f diffR=%.3f",
								b, eL, eR, dL, dR, eL-dL, eR-dR)
						}
					}
				}
			}
		})
	}
}
