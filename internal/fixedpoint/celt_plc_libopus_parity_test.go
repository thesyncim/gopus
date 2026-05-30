//go:build gopus_fixedpoint

package fixedpoint

import (
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestCELTDecoderPLCOracle drives the integer CELTDecoder PLC path
// (DecodeLost / celt_decode_lost) against the real libopus FIXED_POINT
// celt_decode_with_ec(NULL,...) concealment on the static 48000/960 mode. Both
// decoders are primed with the same prior good packet, then run 1..N
// consecutive lost frames; the concealed int16 PCM must match bit-for-bit at
// every loss count, for mono and stereo, exercising the pitch-based concealment
// fade-out and the transition into noise-based concealment (plc_duration>=40).
func TestCELTDecoderPLCOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		channels  int
		frameSize int
		bitrate   int
		transient bool
		numLost   int
	}
	cases := []tc{
		{"mono_960_64k", 1, 960, 64000, false, 8},
		{"mono_960_96k", 1, 960, 96000, false, 8},
		{"mono_transient_960_96k", 1, 960, 96000, true, 8},
		{"mono_480_64k", 1, 480, 64000, false, 12},
		{"mono_240_48k", 1, 240, 48000, false, 16},
		{"stereo_960_128k", 2, 960, 128000, false, 8},
		{"stereo_transient_960_128k", 2, 960, 128000, true, 8},
		{"stereo_480_96k", 2, 480, 96000, false, 12},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			channels := c.channels
			enc := celt.NewEncoder(channels)
			enc.SetBitrate(c.bitrate)

			// Encode a couple of frames so the prime packet is a realistic
			// inter-coded frame; use the last as the prior-good packet.
			var packet []byte
			for frame := 0; frame < 3; frame++ {
				transient := c.transient && frame == 2
				mono := celtTestSignal(c.frameSize, int64(0x9b1c+frame*5)+int64(c.frameSize), transient)
				pcm := make([]float32, c.frameSize*channels)
				for i := 0; i < c.frameSize; i++ {
					pcm[i*channels] = mono[i]
					if channels == 2 {
						pcm[i*channels+1] = mono[i] * 0.8
					}
				}
				p, err := enc.EncodeFrame(pcm, c.frameSize)
				if err != nil {
					t.Fatalf("frame %d: encode: %v", frame, err)
				}
				if len(p) > 1 {
					packet = p
				}
			}
			if len(packet) <= 1 {
				t.Skip("no non-degenerate prime packet produced")
			}

			ref, err := libopustest.ProbeCELTFixedPLC(packet, channels, c.frameSize, 0, celtNbEBands, c.numLost)
			if err != nil {
				libopustest.HelperUnavailable(t, "celt fixed plc", err)
				return
			}

			dec := NewCELTDecoder(channels)
			// Prime with the same prior good packet.
			prime := make([]int16, channels*c.frameSize)
			dec.DecodeWithEC(packet, c.frameSize, prime)

			n := channels * c.frameSize
			for k := 0; k < c.numLost; k++ {
				out := make([]int16, n)
				dec.DecodeLost(c.frameSize, out)
				for i := range out {
					want := ref[k*n+i]
					if out[i] != want {
						t.Fatalf("loss %d: pcm[%d] = %d, libopus = %d (channels=%d frameSize=%d)",
							k+1, i, out[i], want, channels, c.frameSize)
					}
				}
			}
		})
	}
}
