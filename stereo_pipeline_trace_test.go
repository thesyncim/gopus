package gopus

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
)

// TestStereoPipelineTrace traces the stereo CELT pipeline step by step
// at both 64kbps and 128kbps to find where the 2x amplitude diverges.
func TestStereoPipelineTrace(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960
		numFrames  = 40
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
					decoded, derr := dec.DecodeFrame(packet, frameSize)
					if derr != nil {
						t.Fatalf("decode error frame %d: %v", f, derr)
					}

					if f == numFrames-1 {
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
						t.Logf("Bitrate %d, Frame %d: L_max=%.4f R_max=%.4f gainL=%.2f gainR=%.2f",
							bitrate, f, maxL/celt.CELTSigScale, maxR/celt.CELTSigScale,
							maxL/celt.CELTSigScale/amplitude, maxR/celt.CELTSigScale/amplitude)

						// Check encoder and decoder energies
						encE := enc.PrevEnergy()
						decE := dec.PrevEnergy()
						nBands := 21
						if encE != nil && len(encE) >= nBands*2 && decE != nil && len(decE) >= nBands*2 {
							for b := 0; b < 6; b++ {
								t.Logf("  Band %d: encL=%.3f encR=%.3f decL=%.3f decR=%.3f",
									b, encE[b], encE[nBands+b], decE[b], decE[nBands+b])
							}
						}
					}
				}
			}
		})
	}
}

// TestStereoCELTDirectMinimal is a minimal test that encodes/decodes a single
// stereo frame to trace exactly where the 2x factor appears.
func TestStereoCELTDirectMinimal(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960
		amplitude  = 0.5
		numWarmup  = 35
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

			// Warm up
			for f := 0; f < numWarmup+1; f++ {
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
					decoded, derr := dec.DecodeFrame(packet, frameSize)
					if derr != nil {
						t.Fatalf("decode error frame %d: %v", f, derr)
					}

					if f == numWarmup {
						// Input max per channel
						var inMaxL, inMaxR float64
						for i := 0; i < frameSize; i++ {
							l := math.Abs(pcm[i*2])
							r := math.Abs(pcm[i*2+1])
							if l > inMaxL {
								inMaxL = l
							}
							if r > inMaxR {
								inMaxR = r
							}
						}

						// Output max per channel (in internal scale)
						var outMaxL, outMaxR float64
						for i := 0; i < frameSize && i*2+1 < len(decoded); i++ {
							l := math.Abs(decoded[i*2])
							r := math.Abs(decoded[i*2+1])
							if l > outMaxL {
								outMaxL = l
							}
							if r > outMaxR {
								outMaxR = r
							}
						}

						t.Logf("Input:  L_max=%.6f R_max=%.6f", inMaxL, inMaxR)
						t.Logf("Output: L_max=%.6f R_max=%.6f (raw internal scale)", outMaxL, outMaxR)
						t.Logf("Output: L_max=%.6f R_max=%.6f (÷32768)", outMaxL/celt.CELTSigScale, outMaxR/celt.CELTSigScale)
						t.Logf("Gain:   L=%.4f R=%.4f", outMaxL/celt.CELTSigScale/inMaxL, outMaxR/celt.CELTSigScale/inMaxR)
						t.Logf("Packet size: %d bytes", len(packet))
					}
				}
			}
		})
	}
}
