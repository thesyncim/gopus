//go:build gopus_fixed_point

package fixedpoint

import (
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/libopustest"
)

// encodeCELTSequence drives one celt.Encoder over a run of synthetic frames,
// alternating transient/normal content when requested, and returns the encoded
// packets (skipping degenerate <=1 byte packets that route to the PLC path).
func encodeCELTSequence(t *testing.T, channels, frameSize, bitrate, frames int, transient bool) [][]byte {
	t.Helper()
	enc := celt.NewEncoder(channels)
	enc.SetBitrate(bitrate)

	packets := make([][]byte, 0, frames)
	for frame := 0; frame < frames; frame++ {
		tr := transient && frame%2 == 1
		mono := celtTestSignal(frameSize, int64(0x5e1d+frame*7)+int64(frameSize), tr)
		pcm := make([]float32, frameSize*channels)
		for i := 0; i < frameSize; i++ {
			pcm[i*channels] = mono[i]
			if channels == 2 {
				pcm[i*channels+1] = mono[i] * 0.8
			}
		}
		packet, err := enc.EncodeFrame(pcm, frameSize)
		if err != nil {
			t.Fatalf("frame %d: encode: %v", frame, err)
		}
		if len(packet) <= 1 {
			continue
		}
		// Copy: EncodeFrame may reuse its internal buffer across frames.
		cp := make([]byte, len(packet))
		copy(cp, packet)
		packets = append(packets, cp)
	}
	if len(packets) == 0 {
		t.Fatalf("no non-degenerate packets produced")
	}
	return packets
}

// TestCELTDecoderSequenceOracle validates the cross-frame state carry the
// single-shot MODE_DECODE oracle cannot: it threads a SEQUENCE of consecutive
// real CELT packets through ONE gopus CELTDecoder and ONE libopus FIXED_POINT
// decoder (MODE_DECODE_SEQ), asserting every frame's int16 PCM matches. This
// exercises the decode_mem overlap, post-filter _old taps and the energy
// prediction histories (oldBandE/oldLogE/oldLogE2/backgroundLogE) accumulated
// from prior frames.
func TestCELTDecoderSequenceOracle(t *testing.T) {
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
		{"mono_mixed_960_96k", 1, 960, 96000, true},
		{"mono_mixed_480_64k", 1, 480, 64000, true},
		{"mono_normal_240_48k", 1, 240, 48000, false},
		{"stereo_normal_960_128k", 2, 960, 128000, false},
		{"stereo_mixed_960_128k", 2, 960, 128000, true},
		{"stereo_mixed_480_96k", 2, 480, 96000, true},
	}

	const frames = 8
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			packets := encodeCELTSequence(t, c.channels, c.frameSize, c.bitrate, frames, c.transient)

			ref, err := libopustest.ProbeCELTFixedDecodeSeq(packets, c.channels, c.frameSize, 0, celtNbEBands, 48000)
			if err != nil {
				libopustest.HelperUnavailable(t, "celt fixed decode seq", err)
				return
			}

			dec := NewCELTDecoder(c.channels)
			for p, packet := range packets {
				out := make([]int16, c.channels*c.frameSize)
				dec.DecodeWithEC(packet, c.frameSize, out)
				for i := range out {
					if out[i] != ref[p][i] {
						t.Fatalf("packet %d (%d bytes): pcm[%d] = %d, libopus = %d",
							p, len(packet), i, out[i], ref[p][i])
					}
				}
			}
		})
	}
}

// TestCELTDecoderDownsampleOracle validates the st->downsample > 1 path: a 48k
// CELT core decoded to 24k/16k/12k/8k output via the decimating denormalise +
// deemphasis. It threads a sequence of real packets through one gopus decoder at
// the target rate and one libopus FIXED_POINT decoder created at the same rate,
// asserting bit-exact int16 PCM at the decimated output length.
func TestCELTDecoderDownsampleOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	rates := []struct {
		rate       int
		downsample int
	}{
		{24000, 2},
		{16000, 3},
		{12000, 4},
		{8000, 6},
	}

	type tc struct {
		name      string
		channels  int
		frameSize int
		bitrate   int
		transient bool
	}
	cases := []tc{
		{"mono_960_64k", 1, 960, 64000, false},
		{"mono_mixed_960_96k", 1, 960, 96000, true},
		{"mono_480_64k", 1, 480, 64000, false},
		{"stereo_960_128k", 2, 960, 128000, false},
		{"stereo_mixed_480_96k", 2, 480, 96000, true},
	}

	const frames = 6
	for _, r := range rates {
		for _, c := range cases {
			name := c.name + "_" + rateLabel(r.rate)
			t.Run(name, func(t *testing.T) {
				packets := encodeCELTSequence(t, c.channels, c.frameSize, c.bitrate, frames, c.transient)

				ref, err := libopustest.ProbeCELTFixedDecodeSeq(packets, c.channels, c.frameSize, 0, celtNbEBands, r.rate)
				if err != nil {
					libopustest.HelperUnavailable(t, "celt fixed decode seq", err)
					return
				}

				dec := NewCELTDecoderRate(c.channels, r.rate)
				outLen := c.channels * (c.frameSize / r.downsample)
				for p, packet := range packets {
					out := make([]int16, outLen)
					got := dec.DecodeWithEC(packet, c.frameSize, out)
					if got != c.frameSize/r.downsample {
						t.Fatalf("packet %d: returned %d output samples, want %d",
							p, got, c.frameSize/r.downsample)
					}
					if len(ref[p]) != outLen {
						t.Fatalf("packet %d: ref len %d, want %d", p, len(ref[p]), outLen)
					}
					for i := 0; i < outLen; i++ {
						if out[i] != ref[p][i] {
							t.Fatalf("rate %d packet %d: pcm[%d] = %d, libopus = %d",
								r.rate, p, i, out[i], ref[p][i])
						}
					}
				}
			})
		}
	}
}

func rateLabel(rate int) string {
	switch rate {
	case 24000:
		return "24k"
	case 16000:
		return "16k"
	case 12000:
		return "12k"
	case 8000:
		return "8k"
	default:
		return "48k"
	}
}
