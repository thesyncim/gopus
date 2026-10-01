package celt

import (
	"math"
	"math/rand"
	"testing"
)

func equalFloat32Bits(a, b []float32) int {
	for i := range a {
		if math.Float32bits(a[i]) != math.Float32bits(b[i]) {
			return i
		}
	}
	return -1
}

func randSpectrum(rng *rand.Rand, n int) []float32 {
	x := make([]float32, n)
	for i := range x {
		x[i] = float32(rng.NormFloat64() * math.Pow(10, float64(rng.Intn(6))))
	}
	return x
}

// TestIMDCTPreRotateFFTStridedMatchesGather checks the strided pre-rotation
// and FFT of an interleaved short block against gathering the block into a
// contiguous spectrum first.
func TestIMDCTPreRotateFFTStridedMatchesGather(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5b10))
	for _, n := range []int{240, 480} {
		n2, n4 := n/2, n/4
		trig := getMDCTTrigF32(n)
		for _, st := range []*kissFFTState{getKissFFTState(n4), nil} {
			for _, stride := range []int{1, 2, 4, 8} {
				coeffs := randSpectrum(rng, n2*stride)
				for first := range stride {
					got := imdctPreRotateFFTStrided(make([]complex64, n4), make([]kissCpx, n4), coeffs, first, stride, trig, n2, n4, st, make([]float32, n2))
					block := make([]float32, n2)
					for i := range block {
						block[i] = coeffs[first+i*stride]
					}
					want := imdctPreRotateFFT(make([]complex64, n4), make([]kissCpx, n4), block, trig, n2, n4, getKissFFTState(n4))
					for i := range want {
						if math.Float32bits(got[i].r) != math.Float32bits(want[i].r) || math.Float32bits(got[i].i) != math.Float32bits(want[i].i) {
							t.Fatalf("n=%d stride=%d first=%d st=%v: out[%d]=%v want %v", n, stride, first, st != nil, i, got[i], want[i])
						}
					}
				}
			}
		}
	}
}

// synthesizeTransientReference is the short-block synthesis written the way
// celt_synthesis reads it block by block: the previous overlap, a cleared
// buffer, each block gathered and passed through imdctInPlaceScratchF32Spectrum.
func synthesizeTransientReference(coeffs []float32, prevOverlap []celtSig, overlap, shortBlocks int) []float32 {
	frameSize := len(coeffs)
	out := make([]float32, frameSize+overlap)
	for i := range overlap {
		out[i] = float32(prevOverlap[i])
	}
	shortSize := frameSize / shortBlocks
	block := make([]float32, shortSize)
	var scratch imdctScratchF32
	for b := range shortBlocks {
		for i := range block {
			block[i] = coeffs[b+i*shortBlocks]
		}
		imdctInPlaceScratchF32Spectrum(block, out, b*shortSize, overlap, &scratch)
	}
	return out
}

// TestSynthesizeChannelDirectMatchesReference checks the transient short-block
// synthesis that writes each IMDCT straight into out, and the long-block
// synthesis into out, against the gather-and-copy reference paths.
func TestSynthesizeChannelDirectMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0x7e2d))
	const overlap = 120
	for _, frameSize := range []int{120, 240, 480, 960} {
		for iter := range 8 {
			coeffs := randSpectrum(rng, frameSize)
			prev := make([]celtSig, overlap)
			for i := range prev {
				prev[i] = celtSig(rng.NormFloat64() * 1e3)
			}
			shortCoeffs := make([]float32, frameSize)
			var scratch imdctScratchF32
			// Garbage in out must not reach the result.
			out := make([]float32, frameSize+overlap)
			for i := range out {
				out[i] = float32(rng.NormFloat64())
			}
			for _, shortBlocks := range []int{2, 4, 8} {
				if frameSize%(shortBlocks*120) != 0 {
					continue
				}
				got := synthesizeChannelWithOverlapScratchF32(coeffs, prev, overlap, true, shortBlocks, out, &scratch, shortCoeffs)
				want := synthesizeTransientReference(coeffs, prev, overlap, shortBlocks)
				if i := equalFloat32Bits(got, want); i >= 0 {
					t.Fatalf("frame=%d B=%d iter %d: out[%d]=%v want %v", frameSize, shortBlocks, iter, i, got[i], want[i])
				}
			}
			got := synthesizeChannelWithOverlapScratchF32(coeffs, prev, overlap, false, 1, out, &scratch, shortCoeffs)
			want := imdctOverlapWithPrevScratchF32Output32(coeffs, prev, overlap, nil)
			if i := equalFloat32Bits(got, want); i >= 0 {
				t.Fatalf("frame=%d long iter %d: out[%d]=%v want %v", frameSize, iter, i, got[i], want[i])
			}
		}
	}
}

// TestDecodeBandTablesMatchInitCaps checks the precomputed per-LM caps,
// dynalloc quanta, allocation thresholds and scaled allocation vectors
// against init_caps and the celt_decode_with_ec and clt_compute_allocation
// formulas.
func TestDecodeBandTablesMatchInitCaps(t *testing.T) {
	for lm := range 4 {
		for c := range 2 {
			channels := c + 1
			var caps [MaxBands]int32
			initCapsInto(caps[:], MaxBands, lm, channels)
			tab := &decodeBandTables[lm][c]
			for i := range MaxBands {
				width := channels * (EBands[i+1] - EBands[i]) << uint(lm)
				quanta := int32(min(width<<bitRes, max(6<<bitRes, width)))
				if tab.caps[i] != caps[i] || tab.quanta[i] != quanta {
					t.Fatalf("lm=%d C=%d band %d: caps %d quanta %d, want %d %d", lm, channels, i, tab.caps[i], tab.quanta[i], caps[i], quanta)
				}
				n := EBands[i+1] - EBands[i]
				if thresh := int32(max(channels<<bitRes, (3*n<<uint(lm)<<bitRes)>>4)); tab.thresh[i] != thresh {
					t.Fatalf("lm=%d C=%d band %d: thresh %d, want %d", lm, channels, i, tab.thresh[i], thresh)
				}
				for q := range BandAlloc {
					if alloc := int32(channels * n * BandAlloc[q][i] << uint(lm) >> 2); tab.alloc[q][i] != alloc {
						t.Fatalf("lm=%d C=%d vector %d band %d: alloc %d, want %d", lm, channels, q, i, tab.alloc[q][i], alloc)
					}
				}
			}
		}
	}
}
