//go:build gopus_fixedpoint

package fixedpoint

import (
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestCELTDecoderPLCRecoveryOracle drives the integer CELTDecoder loss-recovery
// path against the real libopus FIXED_POINT celt_decode_with_ec. Both decoders
// are primed with the same prior good packet, then run M=1..N consecutive lost
// frames (celt_decode_with_ec(NULL,...)), then decode the same good recovery
// packet (entered with loss_duration!=0, exercising the energy-prediction safety
// block before unquant_coarse_energy). The recovered good-frame int16 PCM must
// match bit-for-bit at every loss count, for mono and stereo, including LM==0/1
// where the safety term is non-zero.
func TestCELTDecoderPLCRecoveryOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		channels  int
		frameSize int
		bitrate   int
		transient bool
		maxLost   int
	}
	cases := []tc{
		{"mono_960_64k", 1, 960, 64000, false, 8},
		{"mono_960_96k", 1, 960, 96000, false, 8},
		{"mono_transient_960_96k", 1, 960, 96000, true, 8},
		{"mono_480_64k", 1, 480, 64000, false, 12},
		{"mono_240_48k", 1, 240, 48000, false, 16},
		{"mono_120_48k", 1, 120, 48000, false, 20},
		{"stereo_960_128k", 2, 960, 128000, false, 8},
		{"stereo_transient_960_128k", 2, 960, 128000, true, 8},
		{"stereo_480_96k", 2, 480, 96000, false, 12},
		{"stereo_120_96k", 2, 120, 96000, false, 20},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			channels := c.channels

			encodeFrame := func(enc *celt.Encoder, frame int, transient bool) ([]byte, error) {
				mono := celtTestSignal(c.frameSize, int64(0x9b1c+frame*5)+int64(c.frameSize), transient)
				pcm := make([]float32, c.frameSize*channels)
				for i := 0; i < c.frameSize; i++ {
					pcm[i*channels] = mono[i]
					if channels == 2 {
						pcm[i*channels+1] = mono[i] * 0.8
					}
				}
				return enc.EncodeFrame(pcm, c.frameSize)
			}

			// Build the prime packet (a realistic inter-coded frame) and a good
			// recovery packet from a fresh encoder so its range state is plausible.
			encP := celt.NewEncoder(channels)
			encP.SetBitrate(c.bitrate)
			var prime []byte
			for frame := 0; frame < 3; frame++ {
				transient := c.transient && frame == 2
				p, err := encodeFrame(encP, frame, transient)
				if err != nil {
					t.Fatalf("prime frame %d: encode: %v", frame, err)
				}
				if len(p) > 1 {
					prime = p
				}
			}
			if len(prime) <= 1 {
				t.Skip("no non-degenerate prime packet produced")
			}

			encG := celt.NewEncoder(channels)
			encG.SetBitrate(c.bitrate)
			var good []byte
			for frame := 0; frame < 3; frame++ {
				p, err := encodeFrame(encG, 100+frame, false)
				if err != nil {
					t.Fatalf("good frame %d: encode: %v", frame, err)
				}
				if len(p) > 1 {
					good = p
				}
			}
			if len(good) <= 1 {
				t.Skip("no non-degenerate good packet produced")
			}

			n := channels * c.frameSize
			for m := 1; m <= c.maxLost; m++ {
				ref, err := libopustest.ProbeCELTFixedPLCRecovery(prime, good, channels, c.frameSize, 0, celtNbEBands, m)
				if err != nil {
					libopustest.HelperUnavailable(t, "celt fixed plc recovery", err)
					return
				}

				dec := NewCELTDecoder(channels)
				scratch := make([]int16, n)
				dec.DecodeWithEC(prime, c.frameSize, scratch)
				for k := 0; k < m; k++ {
					dec.DecodeLost(c.frameSize, scratch)
				}
				out := make([]int16, n)
				dec.DecodeWithEC(good, c.frameSize, out)

				for i := range out {
					if out[i] != ref[i] {
						t.Fatalf("lost=%d: pcm[%d] = %d, libopus = %d (channels=%d frameSize=%d)",
							m, i, out[i], ref[i], channels, c.frameSize)
					}
				}
			}
		})
	}
}
