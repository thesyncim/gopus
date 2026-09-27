package celt

import (
	"math"
	"math/rand"
	"testing"
)

// encodeKernelValue draws encoder-shaped samples with occasional exact zeros
// of both signs, tiny values and large magnitudes.
func encodeKernelValue(rng *rand.Rand) float32 {
	switch rng.Intn(12) {
	case 0:
		return 0
	case 1:
		return float32(math.Copysign(0, -1))
	case 2:
		return float32(rng.NormFloat64()) * 1e-20
	case 3:
		return float32(rng.NormFloat64()) * 3e4
	default:
		return float32(rng.NormFloat64())
	}
}

func encodeKernelSlice(rng *rand.Rand, n int) []float32 {
	s := make([]float32, n)
	for i := range s {
		s[i] = encodeKernelValue(rng)
	}
	return s
}

func sameFloatBits(a, b float32) bool {
	return math.Float32bits(a) == math.Float32bits(b)
}

// TestInnerProdSSEOrderLagsMatchesPerLag requires the multi-lag pass to
// reproduce innerProdFloat32SSEOrder for every lag bit for bit.
func TestInnerProdSSEOrderLagsMatchesPerLag(t *testing.T) {
	rng := rand.New(rand.NewSource(0x1a95))
	for trial := range 300 {
		length := 1 + rng.Intn(260)
		lags := 1 + rng.Intn(13)
		x := encodeKernelSlice(rng, length)
		y := encodeKernelSlice(rng, length+lags-1)
		if trial%17 == 0 {
			y[rng.Intn(len(y))] = float32(math.NaN())
		}
		got := make([]float32, lags)
		innerProdFloat32SSEOrderLags(x, y, got, length)
		for l := range lags {
			want := innerProdFloat32SSEOrder(x, y[l:], length)
			if !sameFloatBits(got[l], want) && !(want != want && got[l] != got[l]) {
				t.Fatalf("trial %d length %d lag %d: got %08x want %08x", trial, length, l, math.Float32bits(got[l]), math.Float32bits(want))
			}
		}
	}
}

// TestInnerProdPairMatchesSingle requires the paired inner products to equal
// two celtInnerProdLibopusOrder calls.
func TestInnerProdPairMatchesSingle(t *testing.T) {
	rng := rand.New(rand.NewSource(0x9a17))
	for trial := range 400 {
		n := rng.Intn(200)
		x1, y1 := encodeKernelSlice(rng, n), encodeKernelSlice(rng, n)
		x2, y2 := encodeKernelSlice(rng, n), encodeKernelSlice(rng, n)
		g1, g2 := celtInnerProdPairLibopusOrder(x1, y1, x2, y2)
		w1, w2 := celtInnerProdLibopusOrder(x1, y1), celtInnerProdLibopusOrder(x2, y2)
		if !sameFloatBits(g1, w1) || !sameFloatBits(g2, w2) {
			t.Fatalf("trial %d n %d: got (%08x, %08x) want (%08x, %08x)", trial, n,
				math.Float32bits(g1), math.Float32bits(g2), math.Float32bits(w1), math.Float32bits(w2))
		}
	}
	x := encodeKernelSlice(rng, 96)
	if a := testing.AllocsPerRun(100, func() { celtInnerProdPairLibopusOrder(x, x, x, x) }); a != 0 {
		t.Fatalf("celtInnerProdPairLibopusOrder allocates %v per run", a)
	}
}

// TestRawMaxMinScanMatchesSequential requires the vector extrema scan to give
// celt_maxabs16's sequential extrema. Only the sign of an equal zero may
// differ, which rawMaxAbsResult and its callers never observe.
func TestRawMaxMinScanMatchesSequential(t *testing.T) {
	rng := rand.New(rand.NewSource(0x3a3a))
	for trial := range 500 {
		x := encodeKernelSlice(rng, rng.Intn(1000))
		if trial%11 == 0 && len(x) > 0 {
			x[rng.Intn(len(x))] = float32(math.NaN())
		}
		if trial%13 == 0 {
			for i := range x {
				x[i] = 0
				if rng.Intn(2) == 0 {
					x[i] = float32(math.Copysign(0, -1))
				}
			}
		}
		maxIn, minIn := encodeKernelValue(rng), encodeKernelValue(rng)
		gotMax, gotMin := rawMaxMinScan(x, maxIn, minIn)
		wantMax, wantMin := rawMaxMinScanScalar(x, maxIn, minIn)
		same := func(a, b float32) bool {
			return sameFloatBits(a, b) || (a == 0 && b == 0)
		}
		if !same(gotMax, wantMax) || !same(gotMin, wantMin) {
			t.Fatalf("trial %d: got (%v, %v) want (%v, %v)", trial, gotMax, gotMin, wantMax, wantMin)
		}
		if g, w := rawMaxAbsResult(gotMax, gotMin), rawMaxAbsResult(wantMax, wantMin); !same(g, w) {
			t.Fatalf("trial %d: maxabs got %v want %v", trial, g, w)
		}
	}
}

// TestPreemphInterleavedMatchesScalar requires the selected pre-emphasis
// kernel to match the scalar celt_preemphasis loop in every output bit and
// in the carried state.
func TestPreemphInterleavedMatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(0x9e39))
	coef := float32(PreemphCoef)
	for trial := range 300 {
		channels := 1 + trial%2
		total := channels * rng.Intn(1000)
		pcm := make([]float32, total)
		for i := range pcm {
			pcm[i] = encodeKernelValue(rng) * 0.5
		}
		state := [2]float32{encodeKernelValue(rng), encodeKernelValue(rng)}
		if channels == 1 {
			state[1] = 0
		}
		got := make([]float32, total)
		want := make([]float32, total)
		gotState := preemphInterleaved(pcm, got, total, channels, coef, state)
		wantState := preemphInterleavedScalar(pcm, want, total, channels, coef, state)
		for i := range want {
			if !sameFloatBits(got[i], want[i]) {
				t.Fatalf("trial %d channels %d total %d sample %d: got %v want %v", trial, channels, total, i, got[i], want[i])
			}
		}
		if gotState != wantState {
			t.Fatalf("trial %d: state got %v want %v", trial, gotState, wantState)
		}
	}
}

// TestAbsSumLevelsMatchesPerLevel requires the interleaved per-level sums to
// equal one l1_metric sum per level.
func TestAbsSumLevelsMatchesPerLevel(t *testing.T) {
	rng := rand.New(rand.NewSource(0xab5))
	for trial := range 300 {
		n := 1 + rng.Intn(176)
		count := 1 + rng.Intn(tfAnalysisMaxLevels)
		levels := encodeKernelSlice(rng, n*count)
		got := make([]float32, count)
		absSumLevels(levels, n, count, got)
		for l := range count {
			level := levels[l*n : (l+1)*n]
			want := absSumSerial(level)
			if celtAbsSumUsesNeon {
				want = l1AbsSumNeon(level, n)
			}
			if !sameFloatBits(got[l], want) {
				t.Fatalf("trial %d n %d level %d: got %v want %v", trial, n, l, got[l], want)
			}
		}
	}
}

// TestPitchXCorrBlocksMatchKernel requires the shared-tail eight-lag blocks to
// equal pitchXcorrKernelAVX8 per block, including when y's capacity past the
// slice holds non-finite garbage that a full-width tail load reads and masks.
func TestPitchXCorrBlocksMatchKernel(t *testing.T) {
	rng := rand.New(rand.NewSource(0x8c0))
	for trial := range 200 {
		length := 8 + rng.Intn(300)
		maxPitch := 8 + rng.Intn(200)
		x := encodeKernelSlice(rng, length)
		backing := make([]float32, length+maxPitch+32)
		for i := range backing {
			backing[i] = float32(math.NaN())
		}
		y := backing[:length+maxPitch-1]
		for i := range y {
			y[i] = encodeKernelValue(rng)
		}
		got := make([]float32, maxPitch)
		done := pitchXCorrAVX2Blocks(x, y, got, length, maxPitch)
		if done%8 != 0 || done > maxPitch {
			t.Fatalf("trial %d: blocks end at %d of %d", trial, done, maxPitch)
		}
		for i := 0; i < done; i += 8 {
			var want [8]float32
			pitchXcorrKernelAVX8(x[:length], y[i:i+length+7], &want, length)
			for l := range 8 {
				if !sameFloatBits(got[i+l], want[l]) {
					t.Fatalf("trial %d length %d lag %d: got %v want %v", trial, length, i+l, got[i+l], want[l])
				}
			}
		}
	}
}

// TestMDCTForwardRotationsMatchScalar requires the forward MDCT's vector
// folds, pre-rotation and post-rotation to reproduce the scalar per-element
// loops.
func TestMDCTForwardRotationsMatchScalar(t *testing.T) {
	if !mdctUseSSEForward {
		t.Skip("scalar forward MDCT rotations on this build")
	}
	rng := rand.New(rand.NewSource(0x3dc7))
	for trial := range 200 {
		n4 := 8 * (1 + rng.Intn(60))
		blocks := 1 + rng.Intn(n4/4)
		i0 := rng.Intn(n4 - 4*blocks + 1)
		in := encodeKernelSlice(rng, 16*blocks+32)
		trig := encodeKernelSlice(rng, 2*n4)
		bitrev := rng.Perm(n4)
		xp1 := rng.Intn(8)
		xp2 := len(in) - 1 - rng.Intn(8)
		scale := encodeKernelValue(rng)
		got := make([]kissCpx, n4)
		want := make([]kissCpx, n4)
		mdctMidRotateSSE(got, bitrev, in, trig, i0, n4, xp1, xp2, blocks, scale)
		for k := 0; k < 4*blocks; k++ {
			i := i0 + k
			mdctStoreDirectStage(want, bitrev[i], scale, in[xp2-2*k], in[xp1+2*k], trig[i], trig[n4+i])
		}
		for i := range want {
			if !sameFloatBits(got[i].r, want[i].r) || !sameFloatBits(got[i].i, want[i].i) {
				t.Fatalf("pre-rotation trial %d index %d: got %v want %v", trial, i, got[i], want[i])
			}
		}

		// Windowed folds: the lead fold reads samples around xp1+n2 and
		// xp2-n2, the tail fold around xp1-n2 and xp2+n2.
		n2 := 2 * n4
		fs := encodeKernelSlice(rng, 3*n2+64)
		win := encodeKernelSlice(rng, 16*blocks+32)
		fxp1 := n2 + rng.Intn(8)
		fxp2 := n2 + 8*blocks + 8 + rng.Intn(8)
		fwp1 := rng.Intn(8)
		fwp2 := 8*blocks + rng.Intn(8)
		for _, tail := range []bool{false, true} {
			gotF := make([]kissCpx, n4)
			wantF := make([]kissCpx, n4)
			if tail {
				mdctTailFoldSSE(gotF, bitrev, fs, win, trig, i0, n4, n2, fxp1, fxp2, fwp1, fwp2, blocks, scale)
			} else {
				mdctLeadFoldSSE(gotF, bitrev, fs, win, trig, i0, n4, n2, fxp1, fxp2, fwp1, fwp2, blocks, scale)
			}
			for k := 0; k < 4*blocks; k++ {
				i := i0 + k
				x1, x2, w1, w2 := fxp1+2*k, fxp2-2*k, fwp1+2*k, fwp2-2*k
				var re, im float32
				if tail {
					re = mdctNegMulAddMixEncode(fs[x1-n2], fs[x2], win[w1], win[w2])
					im = mdctMulAddMixEncode(fs[x1], fs[x2+n2], win[w2], win[w1])
				} else {
					re = mdctMulAddMixEncode(fs[x1+n2], fs[x2], win[w2], win[w1])
					im = mdctMulSubMixEncode(fs[x1], fs[x2-n2], win[w1], win[w2])
				}
				mdctStoreDirectStage(wantF, bitrev[i], scale, re, im, trig[i], trig[n4+i])
			}
			for i := range wantF {
				if !sameFloatBits(gotF[i].r, wantF[i].r) || !sameFloatBits(gotF[i].i, wantF[i].i) {
					t.Fatalf("fold (tail %v) trial %d index %d: got %v want %v", tail, trial, i, gotF[i], wantF[i])
				}
			}
		}

		stage := make([]kissCpx, n4)
		for i := range stage {
			stage[i] = kissCpx{r: encodeKernelValue(rng), i: encodeKernelValue(rng)}
		}
		pairBlocks := n4 >> 3
		gotC := make([]float32, n2)
		wantC := make([]float32, n2)
		mdctPostTwiddleSSE(gotC, stage, trig, n2, n4, pairBlocks)
		covered := 4 * pairBlocks
		for i := range n4 {
			if i >= covered && i < n4-covered {
				continue
			}
			re, im := stage[i].r, stage[i].i
			t0, t1 := trig[i], trig[n4+i]
			wantC[2*i] = mdctMulSubMixEncode(im, re, t1, t0)
			wantC[n2-1-2*i] = mdctMulAddMixEncode(re, im, t1, t0)
		}
		for i := range wantC {
			if !sameFloatBits(gotC[i], wantC[i]) {
				t.Fatalf("post-rotation trial %d n4 %d coeff %d: got %v want %v", trial, n4, i, gotC[i], wantC[i])
			}
		}
	}
}
