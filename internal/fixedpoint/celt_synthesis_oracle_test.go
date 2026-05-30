//go:build gopus_fixedpoint

package fixedpoint

import (
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// The static 48000 CELT mode used by celt_decode_with_ec: shortMdctSize=120,
// nbShortMdcts=8 (maxLM=3), overlap=120, nbEBands=effEBands=21, full MDCT size
// 1920.
const (
	synthShortMdctSize = 120
	synthMaxLM         = 3
	synthMDCTSize      = 2 * synthShortMdctSize << synthMaxLM // 1920
	synthNbEBands      = 21
	synthEffEnd        = 21
	synthOverlap       = 120
)

// synthLogE builds a deterministic per-band log energy buffer (celt_glog, Q24)
// in the moderate range CELT band energies occupy, driving every shift branch
// of the denormalise gain derivation without saturating.
func synthLogE(n int, seed int64) []int32 {
	rng := rand.New(rand.NewSource(seed))
	out := make([]int32, n)
	for i := range out {
		out[i] = int32(rng.Intn(12<<24) - (6 << 24))
	}
	return out
}

// synthCoeffs builds a deterministic normalized-coefficient buffer (celt_norm,
// int32) spanning the range CELT carries before denormalisation, with a few
// extremes mixed in.
func synthCoeffs(n int, seed int64) []int32 {
	rng := rand.New(rand.NewSource(seed))
	out := make([]int32, n)
	extremes := []int32{0, 1, -1, 1 << 24, -(1 << 24), 1 << 28, -(1 << 28)}
	for i := range out {
		if i < len(extremes) {
			out[i] = extremes[i]
		} else {
			out[i] = int32(rng.Intn(1<<26) - (1 << 25))
		}
	}
	return out
}

// synthInitMem builds a deterministic decode_mem history buffer (celt_sig) for
// CC channels, each chanLen long, within the SIG_SAT range.
func synthInitMem(cc, chanLen int, seed int64) []int32 {
	rng := rand.New(rand.NewSource(seed))
	out := make([]int32, cc*chanLen)
	for i := range out {
		out[i] = int32(rng.Intn(1<<27) - (1 << 26))
	}
	return out
}

type synthCase struct {
	name        string
	c, cc       int
	lm          int
	isTransient bool
	silence     bool
	downsample  int
}

// TestCELTSynthesisOracle drives the Go integer celt_synthesis against the real
// libopus FIXED_POINT celt_synthesis bit-for-bit, across mono/stereo, LM 0..3,
// transient on/off and silence on/off, threading the decode_mem overlap history
// across two consecutive frames.
func TestCELTSynthesisOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	lookup := NewMDCTLookup(synthMDCTSize, synthMaxLM)
	if lookup == nil {
		t.Fatalf("NewMDCTLookup(%d, %d) returned nil", synthMDCTSize, synthMaxLM)
	}

	const chanLen = libopustest.CELTSynthesisDecodeBufferSize + synthOverlap

	cases := []synthCase{
		{"mono_lm0", 1, 1, 0, false, false, 1},
		{"mono_lm1", 1, 1, 1, false, false, 1},
		{"mono_lm2", 1, 1, 2, false, false, 1},
		{"mono_lm3", 1, 1, 3, false, false, 1},
		{"mono_lm3_transient", 1, 1, 3, true, false, 1},
		{"mono_lm2_transient", 1, 1, 2, true, false, 1},
		{"mono_lm3_silence", 1, 1, 3, false, true, 1},
		{"stereo_lm3", 2, 2, 3, false, false, 1},
		{"stereo_lm0", 2, 2, 0, false, false, 1},
		{"stereo_lm3_transient", 2, 2, 3, true, false, 1},
		{"stereo_lm2_transient", 2, 2, 2, true, false, 1},
		{"stereo_lm3_silence", 2, 2, 3, false, true, 1},
		{"mono2stereo_lm3", 1, 2, 3, false, false, 1},
		{"mono2stereo_lm2_transient", 1, 2, 2, true, false, 1},
		{"stereo2mono_lm3", 2, 1, 3, false, false, 1},
		{"stereo2mono_lm1_transient", 2, 1, 1, true, false, 1},
		{"mono_lm3_downsample2", 1, 1, 3, false, false, 2},
	}

	const frames = 2

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := synthShortMdctSize << tc.lm

			var window, eBands []int16
			goMem := synthInitMem(tc.cc, chanLen, int64(0x5117033)+int64(tc.lm))
			refMem := append([]int32(nil), goMem...)

			for frame := 0; frame < frames; frame++ {
				seed := int64(0x5117000 + frame*0x1000 + tc.lm*7 + tc.c*3 + tc.cc)
				x := synthCoeffs(tc.c*n, seed+0x11)
				logE := synthLogE(tc.c*synthNbEBands, seed+0x22)

				params := libopustest.CELTSynthesisParams{
					FrameSize:   n,
					C:           tc.c,
					CC:          tc.cc,
					LM:          tc.lm,
					Downsample:  tc.downsample,
					Start:       0,
					EffEnd:      synthEffEnd,
					IsTransient: tc.isTransient,
					Silence:     tc.silence,
				}

				res, err := libopustest.ProbeCELTFixedSynthesis(params, x, logE, refMem)
				if err != nil {
					libopustest.HelperUnavailable(t, "celt synthesis", err)
					return
				}
				if res.ChanLen != chanLen {
					t.Fatalf("oracle ChanLen=%d want %d", res.ChanLen, chanLen)
				}
				refMem = res.DecodeMem
				window = res.Window
				eBands = res.EBands

				outSyn := make([][]int32, tc.cc)
				for c := 0; c < tc.cc; c++ {
					// out_syn[c] = decode_mem[c] + DECODE_BUFFER_SIZE - N
					off := c*chanLen + libopustest.CELTSynthesisDecodeBufferSize - n
					outSyn[c] = goMem[off:]
				}
				CeltSynthesis(lookup, window, eBands,
					synthNbEBands, synthShortMdctSize, synthMaxLM, synthOverlap,
					x, outSyn, logE,
					params.Start, params.EffEnd, tc.c, tc.cc, tc.lm, tc.downsample,
					tc.isTransient, tc.silence)

				for i := range goMem {
					if goMem[i] != refMem[i] {
						t.Fatalf("frame %d: decode_mem[%d] = %d, libopus = %d", frame, i, goMem[i], refMem[i])
					}
				}

				if frame+1 < frames {
					shiftDecodeMem(goMem, tc.cc, chanLen, n, synthOverlap)
					shiftDecodeMem(refMem, tc.cc, chanLen, n, synthOverlap)
				}
			}
		})
	}
}

// shiftDecodeMem reproduces the per-frame decode_mem advance from
// celt_decode_with_ec: OPUS_MOVE(decode_mem[c], decode_mem[c]+N,
// DECODE_BUFFER_SIZE-N+overlap), over the CC output channels (the channels the
// decode_mem buffer is sized for).
func shiftDecodeMem(mem []int32, cc, chanLen, n, overlap int) {
	moveLen := libopustest.CELTSynthesisDecodeBufferSize - n + overlap
	for ch := 0; ch < cc; ch++ {
		base := ch * chanLen
		copy(mem[base:base+moveLen], mem[base+n:base+n+moveLen])
	}
}
