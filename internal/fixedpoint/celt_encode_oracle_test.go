//go:build gopus_fixedpoint

package fixedpoint

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/rangecoding"
)

// TestCELTEncodeWithECOracle checks the FIXED_POINT celt_encode_with_ec CBR
// driver byte-for-byte against the libopus MODE_ENCODE reference for the static
// 48000/960 mode: mono and stereo, normal and transient-prone content, across
// LM 0..3 and a range of CBR targets/complexities. It reports the first
// divergence precisely.
func TestCELTEncodeWithECOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		shortMdctSize = 120
		start         = 0
		end           = 21
	)

	next := func(state *uint32) uint32 {
		s := *state
		s ^= s << 13
		s ^= s >> 17
		s ^= s << 5
		*state = s
		return s
	}

	type kase struct {
		lm         int
		C          int
		bitrate    int
		complexity int
		transient  bool
	}
	var cases []kase
	for _, C := range []int{1, 2} {
		for _, lm := range []int{0, 1, 2, 3} {
			for _, br := range []int{32000, 64000, 96000, 256000} {
				for _, cx := range []int{0, 5, 10} {
					for _, tr := range []bool{false, true} {
						cases = append(cases, kase{lm: lm, C: C, bitrate: br, complexity: cx, transient: tr})
					}
				}
			}
		}
	}

	for _, c := range cases {
		frameSize := shortMdctSize << c.lm
		nbCompressedBytes := c.bitrate * frameSize / (8 * 48000)
		if nbCompressedBytes < 2 {
			nbCompressedBytes = 2
		}

		state := uint32(0xBEEF01 + c.lm*131 + c.bitrate + c.complexity*7)
		if c.transient {
			state ^= 0x33333333
		}
		pcm := make([]int16, c.C*frameSize)
		for i := range pcm {
			v := int32(next(&state))
			s := int16(v >> 19)
			if c.transient && i > len(pcm)/2 && i < len(pcm)/2+40 {
				// inject an energy spike in the second half for a real transient
				s = int16(v >> 12)
			}
			pcm[i] = s
		}

		want, err := libopustest.ProbeCELTFixedEncode(pcm, c.C, frameSize, start, end,
			c.bitrate, c.complexity, nbCompressedBytes)
		if err != nil {
			libopustest.HelperUnavailable(t, "celt fixed encode", err)
			return
		}

		enc := NewCELTEncoder(c.C)
		enc.SetBandRange(start, end)
		enc.SetComplexity(c.complexity)
		enc.SetBitrate(c.bitrate)

		buf := make([]byte, nbCompressedBytes)
		re := &rangecoding.Encoder{}
		re.Init(buf)
		got := enc.EncodeWithEC(pcm, frameSize, re, nbCompressedBytes)
		packet := re.Buffer()

		label := func() string {
			return fmt.Sprintf("LM=%d C=%d br=%d cx=%d transient=%v",
				c.lm, c.C, c.bitrate, c.complexity, c.transient)
		}
		if got != len(want) {
			t.Errorf("%s: gopus byte count %d, libopus %d", label(), got, len(want))
			continue
		}
		mismatch := -1
		for i := 0; i < len(want); i++ {
			if packet[i] != want[i] {
				mismatch = i
				break
			}
		}
		if mismatch >= 0 {
			t.Errorf("%s: first byte divergence at %d: gopus=0x%02x libopus=0x%02x (len=%d)",
				label(), mismatch, packet[mismatch], want[mismatch], len(want))
		}
	}
}
