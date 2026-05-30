//go:build gopus_fixedpoint

package fixedpoint

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/rangecoding"
)

// TestQuantAllBandsDecodeOracle checks the FIXED_POINT band-shape decode
// (QuantAllBandsDecode) against the real libopus quant_all_bands(0, ...) decode
// path bit-for-bit. Both decoders are initialized on the same range-coded byte
// buffer and given identical allocation/tf/balance inputs, so the decoded
// normalized celt_norm X[] and the collapse masks must match exactly.
//
// quant_all_bands is a pure function of its (range-coded input + allocation)
// inputs, so feeding identical bytes and inputs to both sides makes the
// comparison decoupled from the surrounding decoder state. The coded bytes do
// not have to originate from a real encoder: the range decoder reads whatever
// bits the PVQ/split logic requests and the budget logic clamps internally.
func TestQuantAllBandsDecodeOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	const nbEBands = 21 // static 48000/960 mode

	codedBuf := func(seed uint32, n int) []byte {
		state := seed
		buf := make([]byte, n)
		for i := range buf {
			state ^= state << 13
			state ^= state >> 17
			state ^= state << 5
			buf[i] = byte(state >> 11)
		}
		return buf
	}

	// Per-band pulse allocations exercising the no-split, split and zero-pulse
	// (fold/noise) paths. Values are in Q3 bits (the units quant_all_bands
	// receives as pulses[]).
	pulsePattern := func(kind, end int) []int32 {
		p := make([]int32, nbEBands)
		for i := 0; i < end; i++ {
			switch kind {
			case 0: // moderate, uniform
				p[i] = 40
			case 1: // rising
				p[i] = int32(8 + i*12)
			case 2: // sparse: several zero-pulse bands to drive folding/noise
				if i%3 == 0 {
					p[i] = 0
				} else {
					p[i] = 24
				}
			case 3: // high, forces splits
				p[i] = 160
			}
		}
		return p
	}

	// tfPattern builds a per-band tf_res respecting the encoder invariants the
	// reference quant_band relies on: a positive tf_change (recombine) is only
	// produced for short-block (transient) frames where B = M > 1, otherwise
	// B>>recombine would underflow to 0 (an impossible state that sends libopus
	// into an unbounded loop). Negative tf_change (time divide) is valid for
	// long blocks.
	tfPattern := func(kind, end, lm, shortBlocks int) []int32 {
		tf := make([]int32, nbEBands)
		for i := 0; i < end; i++ {
			switch kind {
			case 1:
				if shortBlocks > 0 && lm > 0 {
					tf[i] = 1 // recombine (short blocks only)
				}
			case 2:
				tf[i] = -1 // time divide
			}
		}
		return tf
	}

	type cfg struct {
		channels    int
		lm          int
		shortBlocks int
		spread      int
		dualStereo  int
		intensity   int
		pulseKind   int
		tfKind      int
		nbytes      int
		seed        uint32
	}

	var cases []cfg
	seed := uint32(0x9e3779b9)
	for _, ch := range []int{1, 2} {
		for lm := 0; lm <= 3; lm++ {
			for _, spread := range []int{spreadNone, 1, 2, spreadAggressive} {
				for _, pk := range []int{0, 1, 2, 3} {
					seed = seed*1664525 + 1013904223
					tfKind := int(seed>>8) % 3
					ds := 0
					intensity := nbEBands
					if ch == 2 {
						if seed&1 == 1 {
							ds = 1
						}
						intensity = 3 + int(seed>>16)%(nbEBands-3)
					}
					sb := 0
					if lm > 0 && (seed>>4)&1 == 1 {
						sb = 1 << lm
					}
					cases = append(cases, cfg{
						channels:    ch,
						lm:          lm,
						shortBlocks: sb,
						spread:      spread,
						dualStereo:  ds,
						intensity:   intensity,
						pulseKind:   pk,
						tfKind:      tfKind,
						nbytes:      40 + int(seed>>20)%80,
						seed:        seed,
					})
				}
			}
		}
	}

	for idx, c := range cases {
		name := fmt.Sprintf("c%d_ch%d_lm%d_sb%d_spread%d_ds%d_pk%d_tf%d",
			idx, c.channels, c.lm, c.shortBlocks, c.spread, c.dualStereo, c.pulseKind, c.tfKind)
		t.Run(name, func(t *testing.T) {
			end := nbEBands
			start := 0
			codedBands := end
			pulses := pulsePattern(c.pulseKind, end)
			tfRes := tfPattern(c.tfKind, end, c.lm, c.shortBlocks)
			coded := codedBuf(c.seed, c.nbytes)
			totalBits := int32(len(coded) * (8 << bitRes))
			balance := int32(0)
			startSeed := uint32(0xfeed1234) ^ c.seed

			// Reference.
			ref, err := libopustest.ProbeCELTFixedQuantAllBands(libopustest.CELTQuantAllBandsParams{
				Channels:    c.channels,
				LM:          c.lm,
				Start:       start,
				End:         end,
				ShortBlocks: c.shortBlocks,
				Spread:      c.spread,
				DualStereo:  c.dualStereo,
				Intensity:   c.intensity,
				TotalBits:   totalBits,
				Balance:     balance,
				CodedBands:  codedBands,
				DisableInv:  false,
				Seed:        startSeed,
				NbEBands:    nbEBands,
				Pulses:      pulses,
				TfRes:       tfRes,
				Coded:       coded,
			})
			if err != nil {
				t.Fatalf("oracle: %v", err)
			}

			// Go port over the same bytes / inputs.
			dec := &rangecoding.Decoder{}
			dec.Init(append([]byte(nil), coded...))
			goSeed := startSeed
			pulsesI := toIntSlice(pulses)
			tfResI := toIntSlice(tfRes)
			frameSize := 120 << c.lm
			left, right, collapse := QuantAllBandsDecode(dec, c.channels, frameSize, c.lm,
				start, end, pulsesI, tfResI, c.shortBlocks, c.spread, c.dualStereo,
				c.intensity, int(totalBits), int(balance), codedBands, false, &goSeed)

			if ref.N != frameSize {
				t.Fatalf("N mismatch: ref=%d go=%d", ref.N, frameSize)
			}
			if goSeed != ref.Seed {
				t.Errorf("seed mismatch: ref=%d go=%d", ref.Seed, goSeed)
			}

			// X comparison (channel-major: X[0:N] then Y[0:N]).
			cmpX := func(label string, got []int32, base int) {
				for i := 0; i < frameSize; i++ {
					want := ref.X[base+i]
					if got[i] != want {
						t.Fatalf("%s X[%d] mismatch: ref=%d go=%d", label, i, want, got[i])
					}
				}
			}
			cmpX("L", left, 0)
			if c.channels == 2 {
				cmpX("R", right, frameSize)
			}

			if len(collapse) != len(ref.Collapse) {
				t.Fatalf("collapse len mismatch: ref=%d go=%d", len(ref.Collapse), len(collapse))
			}
			for i := range collapse {
				if collapse[i] != ref.Collapse[i] {
					t.Fatalf("collapse[%d] mismatch: ref=%d go=%d", i, ref.Collapse[i], collapse[i])
				}
			}
		})
	}
}

func toIntSlice(v []int32) []int {
	out := make([]int, len(v))
	for i, x := range v {
		out[i] = int(x)
	}
	return out
}
