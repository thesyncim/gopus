package gopus

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
)

// TestStereoCELTEnergyDump directly tests the CELT encoder to dump
// per-band energies across frames at low bitrate stereo.
func TestStereoCELTEnergyDump(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960
		numFrames  = 10
		amplitude  = 0.5
	)

	for _, bitrate := range []int{64000, 128000} {
		t.Run(fmt.Sprintf("bitrate_%d", bitrate), func(t *testing.T) {
			enc := celt.NewEncoder(channels)
			enc.SetBitrate(bitrate)
			enc.SetVBR(true)
			enc.SetConstrainedVBR(true)
			enc.SetComplexity(10)

			dec := celt.NewDecoder(channels)

			pcm := make([]float64, frameSize*channels)

			for f := 0; f < numFrames; f++ {
				// Generate interleaved stereo: L=440Hz, R=554Hz
				for i := 0; i < frameSize; i++ {
					tt := float64(f*frameSize+i) / float64(sampleRate)
					pcm[i*2] = amplitude * math.Sin(2*math.Pi*440*tt)
					pcm[i*2+1] = amplitude * math.Sin(2*math.Pi*554.37*tt+0.1)
				}
				packet, err := enc.EncodeFrame(pcm, frameSize)
				if err != nil {
					t.Fatalf("encode error frame %d: %v", f, err)
				}
				n := len(packet)
				if n > 0 {
					// Decode
					decoded, derr := dec.DecodeFrame(packet, frameSize)
					if derr != nil {
						t.Fatalf("decode error frame %d: %v", f, derr)
					}

					// Check output amplitude
					var maxL, maxR float64
					for i := 0; i < frameSize && i*2+1 < len(decoded); i++ {
						l := math.Abs(decoded[i*2])
						r := math.Abs(decoded[i*2+1])
						if l > maxL {
							maxL = l
						}
						if r > maxR {
							maxR = r
						}
					}

					// Get encoder's prev energy (the quantized energies from this frame)
					prevE := enc.PrevEnergy()
					nBands := 21
					t.Logf("Frame %d (pkt=%d bytes): L_max=%.4f R_max=%.4f gainL=%.2f gainR=%.2f",
						f, n, maxL/celt.CELTSigScale, maxR/celt.CELTSigScale,
						maxL/celt.CELTSigScale/amplitude, maxR/celt.CELTSigScale/amplitude)
					if prevE != nil && len(prevE) >= nBands*2 {
						leftE := prevE[:nBands]
						rightE := prevE[nBands:]
						t.Logf("  enc prevEnergy L[0..5]: %v", fmtEnergies(leftE[:6]))
						t.Logf("  enc prevEnergy R[0..5]: %v", fmtEnergies(rightE[:6]))
					}

					// Also check decoder's prev energy
					decPrevE := dec.PrevEnergy()
					if decPrevE != nil && len(decPrevE) >= nBands*2 {
						decLeftE := decPrevE[:nBands]
						decRightE := decPrevE[nBands:]
						t.Logf("  dec prevEnergy L[0..5]: %v", fmtEnergies(decLeftE[:6]))
						t.Logf("  dec prevEnergy R[0..5]: %v", fmtEnergies(decRightE[:6]))
					}
				}
			}
		})
	}
}

func fmtEnergies(e []float64) string {
	s := "["
	for i, v := range e {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("%.2f", v)
	}
	s += "]"
	return s
}
