//go:build gopus_fixedpoint

package fixedpoint

import (
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/internal/libopustest"
)

// celtTestSignal builds a deterministic float32 [-1,1) signal of n samples that
// mixes a couple of tones with noise, so the CELT encoder exercises real band
// shapes (and transients when bursts are present).
func celtTestSignal(n int, seed int64, transient bool) []float32 {
	rng := rand.New(rand.NewSource(seed))
	out := make([]float32, n)
	for i := range out {
		t := float64(i)
		v := 0.30*math.Sin(2*math.Pi*440.0*t/48000.0) +
			0.15*math.Sin(2*math.Pi*1200.0*t/48000.0) +
			0.05*(rng.Float64()*2-1)
		if transient && i >= n/2 && i < n/2+80 {
			v += 0.6
		}
		out[i] = float32(v)
	}
	return out
}

// TestCELTDecoderFullFrameOracle drives the integer CELTDecoder.DecodeWithEC
// against the real libopus FIXED_POINT celt_decode_with_ec (MODE_DECODE) on the
// static 48000/960 mode, comparing the int16 PCM bit-for-bit across several
// real encoded CELT packets threaded as consecutive frames.
func TestCELTDecoderFullFrameOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		channels  int
		frameSize int
		bitrate   int
		transient bool
	}
	cases := []tc{
		{"mono_normal_960_64k", 1, 960, 64000, false},
		{"mono_normal_960_96k", 1, 960, 96000, false},
		{"mono_normal_960_32k", 1, 960, 32000, false},
		{"mono_transient_960_96k", 1, 960, 96000, true},
		{"mono_normal_480_64k", 1, 480, 64000, false},
		{"mono_transient_480_96k", 1, 480, 96000, true},
		{"mono_normal_240_48k", 1, 240, 48000, false},
		{"stereo_normal_960_128k", 2, 960, 128000, false},
		{"stereo_transient_960_128k", 2, 960, 128000, true},
		{"stereo_normal_480_96k", 2, 480, 96000, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			channels := c.channels
			enc := celt.NewEncoder(channels)
			enc.SetBitrate(c.bitrate)

			// The MODE_DECODE oracle builds a fresh decoder per call (no
			// cross-frame state), so each packet is decoded by a fresh Go
			// decoder too. Encoding successive frames through one encoder still
			// produces a realistic variety of inter-coded packets.
			const frames = 4
			compared := 0
			for frame := 0; frame < frames; frame++ {
				transient := c.transient && frame%2 == 1
				mono := celtTestSignal(c.frameSize, int64(0x5e1d+frame*7)+int64(c.frameSize), transient)
				pcm := make([]float32, c.frameSize*channels)
				for i := 0; i < c.frameSize; i++ {
					pcm[i*channels] = mono[i]
					if channels == 2 {
						pcm[i*channels+1] = mono[i] * 0.8
					}
				}
				packet, err := enc.EncodeFrame(pcm, c.frameSize)
				if err != nil {
					t.Fatalf("frame %d: encode: %v", frame, err)
				}
				if len(packet) <= 1 {
					continue // degenerate/silence packet handled by the PLC path (out of scope)
				}

				ref, err := libopustest.ProbeCELTFixedDecode(packet, channels, c.frameSize, 0, celtNbEBands)
				if err != nil {
					libopustest.HelperUnavailable(t, "celt fixed decode", err)
					return
				}

				dec := NewCELTDecoder(channels)
				out := make([]int16, channels*c.frameSize)
				dec.DecodeWithEC(packet, c.frameSize, out)

				for i := range out {
					if out[i] != ref[i] {
						t.Fatalf("frame %d: pcm[%d] = %d, libopus = %d (packet %d bytes)",
							frame, i, out[i], ref[i], len(packet))
					}
				}
				compared++
			}
			if compared == 0 {
				t.Fatalf("no packets compared (all degenerate)")
			}
		})
	}
}
