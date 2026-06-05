//go:build gopus_fixed_point

package fixedpoint

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// xorshift mirrors the PRNG used by the base encode oracle test.
func xorshift(state *uint32) uint32 {
	s := *state
	s ^= s << 13
	s ^= s >> 17
	s ^= s << 5
	*state = s
	return s
}

func genPCM(C, frameSize int, seed uint32, transient bool) []int16 {
	state := seed
	pcm := make([]int16, C*frameSize)
	for i := range pcm {
		v := int32(xorshift(&state))
		s := int16(v >> 19)
		if transient && i > len(pcm)/2 && i < len(pcm)/2+40 {
			s = int16(v >> 12)
		}
		pcm[i] = s
	}
	return pcm
}

func firstDivergence(got []byte, gotLen int, want []byte) (int, bool) {
	if gotLen != len(want) {
		return -1, false
	}
	for i := 0; i < len(want); i++ {
		if got[i] != want[i] {
			return i, false
		}
	}
	return 0, true
}

// TestCELTEncodeStartBandOracle validates the hybrid-CELT band subset path
// (st->start>0, used when SILK occupies the low band) byte-for-byte against the
// FIXED_POINT reference. start=17 is the typical hybrid split. Only bands
// [start,end) are coded.
func TestCELTEncodeStartBandOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		shortMdctSize = 120
		start         = 17
		end           = 21
	)

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

		seed := uint32(0xA17B01 + c.lm*131 + c.bitrate + c.complexity*7)
		if c.transient {
			seed ^= 0x33333333
		}
		pcm := genPCM(c.C, frameSize, seed, c.transient)

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

		label := fmt.Sprintf("start=%d LM=%d C=%d br=%d cx=%d transient=%v",
			start, c.lm, c.C, c.bitrate, c.complexity, c.transient)
		idx, ok := firstDivergence(packet, got, want)
		if idx == -1 {
			t.Errorf("%s: gopus byte count %d, libopus %d", label, got, len(want))
			continue
		}
		if !ok {
			t.Errorf("%s: first byte divergence at %d: gopus=0x%02x libopus=0x%02x (len=%d)",
				label, idx, packet[idx], want[idx], len(want))
		}
	}
}

// TestCELTEncodeLFEOracle validates the low-frequency-effects encode path
// (st->lfe==1) byte-for-byte. LFE forces the band-energy clamp above band 0,
// disables transient/pitch/TF analysis, and pins dynalloc/trim/bandwidth.
func TestCELTEncodeLFEOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		shortMdctSize = 120
		start         = 0
		end           = 21
	)

	type kase struct {
		lm         int
		bitrate    int
		complexity int
		vbr        bool
		cvbr       bool
		transient  bool
	}
	var cases []kase
	// LFE is mono in practice; exercise CBR, VBR and constrained-VBR.
	for _, lm := range []int{0, 1, 2, 3} {
		for _, br := range []int{6000, 12000, 24000, 64000} {
			for _, cx := range []int{0, 5, 10} {
				for _, m := range []struct{ vbr, cvbr bool }{{false, false}, {true, false}, {true, true}} {
					for _, tr := range []bool{false, true} {
						cases = append(cases, kase{lm: lm, bitrate: br, complexity: cx, vbr: m.vbr, cvbr: m.cvbr, transient: tr})
					}
				}
			}
		}
	}

	const C = 1
	for _, c := range cases {
		frameSize := shortMdctSize << c.lm
		nbCompressedBytes := c.bitrate * frameSize / (8 * 48000)
		if nbCompressedBytes < 2 {
			nbCompressedBytes = 2
		}

		seed := uint32(0x1FE01 + c.lm*131 + c.bitrate + c.complexity*7)
		if c.transient {
			seed ^= 0x55555555
		}
		pcm := genPCM(C, frameSize, seed, c.transient)

		want, err := libopustest.ProbeCELTFixedEncodeExt(pcm, C, frameSize, start, end,
			c.bitrate, c.complexity, nbCompressedBytes, c.vbr, c.cvbr, true, nil)
		if err != nil {
			libopustest.HelperUnavailable(t, "celt fixed encode ext", err)
			return
		}

		enc := NewCELTEncoder(C)
		enc.SetBandRange(start, end)
		enc.SetComplexity(c.complexity)
		enc.SetBitrate(c.bitrate)
		enc.SetVBR(c.vbr)
		enc.SetConstrainedVBR(c.cvbr)
		enc.SetLFE(true)

		buf := make([]byte, nbCompressedBytes)
		re := &rangecoding.Encoder{}
		re.Init(buf)
		got := enc.EncodeWithEC(pcm, frameSize, re, nbCompressedBytes)
		packet := re.Buffer()

		label := fmt.Sprintf("LFE LM=%d br=%d cx=%d vbr=%v cvbr=%v transient=%v",
			c.lm, c.bitrate, c.complexity, c.vbr, c.cvbr, c.transient)
		idx, ok := firstDivergence(packet, got, want)
		if idx == -1 {
			t.Errorf("%s: gopus byte count %d, libopus %d", label, got, len(want))
			continue
		}
		if !ok {
			t.Errorf("%s: first byte divergence at %d: gopus=0x%02x libopus=0x%02x (len=%d)",
				label, idx, packet[idx], want[idx], len(want))
		}
	}
}

// TestCELTEncodeEnergyMaskOracle validates the surround energy-mask path
// (st->energy_mask != NULL): surround_masking/surround_dynalloc/surround_trim
// driving dynalloc + trim + VBR adjustments. The mask is a deterministic
// pseudo-random celt_glog (Q24) map.
func TestCELTEncodeEnergyMaskOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		shortMdctSize = 120
		start         = 0
		end           = 21
		nbEBands      = 21
	)

	// genMask builds a C*nbEBands celt_glog (Q24) mask in a plausible range
	// (roughly [-3 dB, +0.5 dB] in Q24), the units st->energy_mask carries.
	genMask := func(C int, seed uint32) []int32 {
		state := seed
		mask := make([]int32, C*nbEBands)
		for i := range mask {
			v := int32(xorshift(&state))
			// Map to [-3.0, 0.5] dB in Q24: gconst range.
			lo := -gconstF(3.0)
			hi := gconstF(0.5)
			span := hi - lo
			mask[i] = lo + int32((int64(uint32(v)) % int64(uint32(span)+1)))
		}
		return mask
	}

	type kase struct {
		lm         int
		C          int
		bitrate    int
		complexity int
		vbr        bool
		cvbr       bool
		transient  bool
	}
	var cases []kase
	for _, C := range []int{1, 2} {
		for _, lm := range []int{0, 1, 2, 3} {
			for _, br := range []int{32000, 64000, 96000, 256000} {
				for _, cx := range []int{0, 5, 10} {
					for _, m := range []struct{ vbr, cvbr bool }{{false, false}, {true, false}, {true, true}} {
						for _, tr := range []bool{false, true} {
							cases = append(cases, kase{lm: lm, C: C, bitrate: br, complexity: cx, vbr: m.vbr, cvbr: m.cvbr, transient: tr})
						}
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

		seed := uint32(0xE7A501 + c.lm*131 + c.bitrate + c.complexity*7)
		if c.transient {
			seed ^= 0x0F0F0F0F
		}
		pcm := genPCM(c.C, frameSize, seed, c.transient)
		mask := genMask(c.C, seed^0xDEAD)

		want, err := libopustest.ProbeCELTFixedEncodeExt(pcm, c.C, frameSize, start, end,
			c.bitrate, c.complexity, nbCompressedBytes, c.vbr, c.cvbr, false, mask)
		if err != nil {
			libopustest.HelperUnavailable(t, "celt fixed encode ext", err)
			return
		}

		enc := NewCELTEncoder(c.C)
		enc.SetBandRange(start, end)
		enc.SetComplexity(c.complexity)
		enc.SetBitrate(c.bitrate)
		enc.SetVBR(c.vbr)
		enc.SetConstrainedVBR(c.cvbr)
		enc.SetEnergyMask(mask)

		buf := make([]byte, nbCompressedBytes)
		re := &rangecoding.Encoder{}
		re.Init(buf)
		got := enc.EncodeWithEC(pcm, frameSize, re, nbCompressedBytes)
		packet := re.Buffer()

		label := fmt.Sprintf("mask LM=%d C=%d br=%d cx=%d vbr=%v cvbr=%v transient=%v",
			c.lm, c.C, c.bitrate, c.complexity, c.vbr, c.cvbr, c.transient)
		idx, ok := firstDivergence(packet, got, want)
		if idx == -1 {
			t.Errorf("%s: gopus byte count %d, libopus %d", label, got, len(want))
			continue
		}
		if !ok {
			t.Errorf("%s: first byte divergence at %d: gopus=0x%02x libopus=0x%02x (len=%d)",
				label, idx, packet[idx], want[idx], len(want))
		}
	}
}
