//go:build gopus_fixedpoint

package fixedpoint

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/rangecoding"
)

// TestQuantAllBandsEncodeOracle checks the FIXED_POINT band-shape encode
// (QuantAllBandsEncode) against the real libopus quant_all_bands(1, ...) encode
// path bit-for-bit. Both encoders start a fresh range encoder over an
// identically sized buffer and receive identical normalized X/Y, band
// energies, allocation, tf and balance inputs, so the coded byte stream, the
// post-encode (resynthesized) X[]/Y[] and the collapse masks must match
// exactly.
//
// The normalized X/Y are deterministic per-band unit-norm vectors (mirroring
// what normalise_bands produces). The band energies are deterministic Q14-ish
// values driving intensity_stereo and the min-stereo-energy guard.
func TestQuantAllBandsEncodeOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	const nbEBands = 21 // static 48000/960 mode

	// normVector fills X[band] with a deterministic unit-norm celt_norm vector.
	normVector := func(seed uint32, x []int32, n int) {
		state := seed | 1
		for i := 0; i < n; i++ {
			state ^= state << 13
			state ^= state >> 17
			state ^= state << 5
			// Spread into the celt_norm range, signed.
			x[i] = int32(state>>8) - (1 << 23)
		}
		RenormaliseVector(x, n, q31One)
	}

	pulsePattern := func(kind, end int) []int32 {
		p := make([]int32, nbEBands)
		for i := 0; i < end; i++ {
			switch kind {
			case 0:
				p[i] = 40
			case 1:
				p[i] = int32(8 + i*12)
			case 2:
				if i%3 == 0 {
					p[i] = 0
				} else {
					p[i] = 24
				}
			case 3:
				p[i] = 160
			}
		}
		return p
	}

	tfPattern := func(kind, end, lm, shortBlocks int) []int32 {
		tf := make([]int32, nbEBands)
		for i := 0; i < end; i++ {
			switch kind {
			case 1:
				if shortBlocks > 0 && lm > 0 {
					tf[i] = 1
				}
			case 2:
				tf[i] = -1
			}
		}
		return tf
	}

	// bandEnergies builds deterministic celt_ener values; some bands are forced
	// near/below MIN_STEREO_ENERGY to exercise the stereo silence guard.
	bandEnergies := func(seed uint32, channels, end int) []int32 {
		e := make([]int32, channels*nbEBands)
		state := seed | 1
		for c := 0; c < channels; c++ {
			for i := 0; i < end; i++ {
				state ^= state << 13
				state ^= state >> 17
				state ^= state << 5
				v := int32(state>>10) & 0x3fffff
				if i%7 == 0 {
					v = int32(state & 1) // 0 or 1, below MIN_STEREO_ENERGY (==2)
				}
				e[c*nbEBands+i] = v + 1
			}
		}
		return e
	}

	type cfg struct {
		channels    int
		lm          int
		shortBlocks int
		spread      int
		dualStereo  int
		intensity   int
		complexity  int
		pulseKind   int
		tfKind      int
		nbytes      int
		seed        uint32
	}

	var cases []cfg
	seed := uint32(0x1234abcd)
	for _, ch := range []int{1, 2} {
		for lm := 0; lm <= 3; lm++ {
			for _, spread := range []int{spreadNone, 1, 2, spreadAggressive} {
				for _, pk := range []int{0, 1, 2, 3} {
					seed = seed*1664525 + 1013904223
					tfKind := int(seed>>8) % 3
					ds := 0
					intensity := nbEBands
					complexity := 5
					if ch == 2 {
						if seed&1 == 1 {
							ds = 1
						}
						intensity = 3 + int(seed>>16)%(nbEBands-3)
						if (seed>>2)&1 == 1 {
							complexity = 10 // exercise theta_rdo
						}
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
						complexity:  complexity,
						pulseKind:   pk,
						tfKind:      tfKind,
						nbytes:      40 + int(seed>>20)%80,
						seed:        seed,
					})
				}
			}
		}
	}

	eBands := celt.EBands

	for idx, c := range cases {
		name := fmt.Sprintf("c%d_ch%d_lm%d_sb%d_spread%d_ds%d_cx%d_pk%d_tf%d",
			idx, c.channels, c.lm, c.shortBlocks, c.spread, c.dualStereo, c.complexity, c.pulseKind, c.tfKind)
		t.Run(name, func(t *testing.T) {
			end := nbEBands
			start := 0
			codedBands := end
			lm := c.lm
			M := 1 << lm
			frameSize := 120 << lm

			pulses := pulsePattern(c.pulseKind, end)
			tfRes := tfPattern(c.tfKind, end, lm, c.shortBlocks)
			bandE := bandEnergies(c.seed^0xa5a5a5a5, c.channels, end)
			totalBits := int32(c.nbytes * (8 << bitRes))
			balance := int32(0)
			startSeed := uint32(0xc0ffee11) ^ c.seed

			// Build the per-band unit-norm X (and Y) input, shared by both sides.
			x := make([]int32, c.channels*frameSize)
			for ch := 0; ch < c.channels; ch++ {
				for i := start; i < end; i++ {
					bandStart := eBands[i] * M
					bandEnd := eBands[i+1] * M
					n := bandEnd - bandStart
					vseed := c.seed ^ (uint32(ch) << 28) ^ (uint32(i) * 2654435761)
					normVector(vseed, x[ch*frameSize+bandStart:ch*frameSize+bandEnd], n)
				}
			}

			// Reference.
			refX := append([]int32(nil), x...)
			ref, err := libopustest.ProbeCELTFixedQuantAllBandsEncode(libopustest.CELTQuantAllBandsEncodeParams{
				Channels:    c.channels,
				LM:          lm,
				Start:       start,
				End:         end,
				ShortBlocks: c.shortBlocks,
				Spread:      c.spread,
				DualStereo:  c.dualStereo,
				Intensity:   c.intensity,
				TotalBits:   totalBits,
				Balance:     balance,
				CodedBands:  codedBands,
				Complexity:  c.complexity,
				DisableInv:  false,
				Seed:        startSeed,
				NbEBands:    nbEBands,
				Nbytes:      c.nbytes,
				Pulses:      pulses,
				TfRes:       tfRes,
				BandE:       bandE,
				X:           refX,
			})
			if err != nil {
				t.Fatalf("oracle: %v", err)
			}

			// Go port over the same inputs.
			goX := append([]int32(nil), x...)
			var goLeft, goRight []int32
			goLeft = goX[:frameSize]
			if c.channels == 2 {
				goRight = goX[frameSize:]
			}
			buf := make([]byte, c.nbytes)
			enc := &rangecoding.Encoder{}
			enc.Init(buf)
			goSeed := startSeed
			pulsesI := toIntSlice(pulses)
			tfResI := toIntSlice(tfRes)

			collapse := QuantAllBandsEncode(enc, c.channels, frameSize, lm, start, end,
				goLeft, goRight, bandE, pulsesI, tfResI, c.shortBlocks, c.spread,
				c.dualStereo, c.intensity, int(totalBits), int(balance), codedBands,
				c.complexity, false, &goSeed)
			// Match the libopus oracle buffer layout: ec_enc_done leaves the
			// range-coded bytes at the front, the raw/end bytes at the very end,
			// and a zeroed gap between. Shrink(nbytes) keeps that in-place layout
			// instead of the convenience packing Done() applies otherwise.
			enc.Shrink(uint32(c.nbytes))
			enc.Done()

			if ref.N != frameSize {
				t.Fatalf("N mismatch: ref=%d go=%d", ref.N, frameSize)
			}
			if goSeed != ref.Seed {
				t.Errorf("seed mismatch: ref=%d go=%d", ref.Seed, goSeed)
			}

			// Coded byte stream (full storage buffer, raw bits included).
			if len(buf) != len(ref.Coded) {
				t.Fatalf("coded len mismatch: ref=%d go=%d", len(ref.Coded), len(buf))
			}
			for i := range buf {
				if buf[i] != ref.Coded[i] {
					t.Fatalf("coded[%d] mismatch: ref=0x%02x go=0x%02x (bytesUsed ref=%d)",
						i, ref.Coded[i], buf[i], ref.BytesUsed)
				}
			}

			// Post-encode (resynth) X comparison; only meaningful when resynth ran.
			resynth := c.channels == 2 && c.dualStereo == 0 && c.complexity >= 8
			if resynth {
				for i := 0; i < c.channels*frameSize; i++ {
					if goX[i] != ref.X[i] {
						t.Fatalf("postX[%d] mismatch: ref=%d go=%d", i, ref.X[i], goX[i])
					}
				}
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
