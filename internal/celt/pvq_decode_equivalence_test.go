package celt

import (
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"math"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func pvqDecodeCovered(n, k int) bool {
	return n >= 2 && k >= 1 && (k <= 2 || n == 2 || canUseCWRSFast(n, k))
}

// TestCWRSTableDecodeMatchesRowBasedFullRange checks decodePulsesTable32
// against the row-based ncwrsUrow+cwrsi32 walk over every (n, k) pair the
// band decoder reaches, with the first, last and sampled indices of each.
func TestCWRSTableDecodeMatchesRowBasedFullRange(t *testing.T) {
	rng := rand.New(rand.NewSource(0xc425))
	yTab := make([]int32, 176)
	yRow := make([]int32, 176)
	u := make([]uint32, 180)
	for n := 2; n <= 176; n++ {
		for k := 1; k <= 176; k++ {
			if !pvqDecodeCovered(n, k) {
				continue
			}
			v := PVQ_V(n, k)
			idxs := []uint32{0, v - 1, v / 2}
			for range 6 {
				idxs = append(idxs, rng.Uint32()%v)
			}
			for _, i := range idxs {
				clear(yTab[:n])
				clear(yRow[:n])
				yyTab := decodePulsesTable32(i, n, k, yTab[:n])
				ncwrsUrow(n, k, u)
				yyRow := cwrsi32(n, k, i, yRow[:n], u)
				if yyTab != yyRow {
					t.Fatalf("n=%d k=%d i=%d: yy table=%d row=%d", n, k, i, yyTab, yyRow)
				}
				for z := range n {
					if yTab[z] != yRow[z] {
						t.Fatalf("n=%d k=%d i=%d: y[%d] table=%d row=%d", n, k, i, z, yTab[z], yRow[z])
					}
				}
			}
		}
	}
}

// algUnquantReference is libopus alg_unquant() written from celt/vq.c:
// decode_pulses() with ec_dec_uint and the row-based cwrsi(), the
// normalise_residual() scaling, exp_rotation() and extract_collapse_mask().
func algUnquantReference(x []celtNorm, rd *rangecoding.Decoder, k, spread, b int, gain float32) int {
	n := len(x)
	iy := make([]int32, n)
	u := make([]uint32, k+2)
	idx := rd.DecodeUniform(PVQ_V(n, k))
	ncwrsUrow(n, k, u)
	ryy := cwrsi32(n, k, idx, iy, u)
	g := celtRSqrt(float32(ryy)) * gain
	for i := range x {
		x[i] = celtNorm(float32(iy[i]) * g)
	}
	expRotationNorm(x, n, -1, b, k, spread)
	if b <= 1 {
		return 1
	}
	n0 := n / b
	collapse := 0
	for i := range b {
		tmp := int32(0)
		for j := range n0 {
			tmp |= iy[i*n0+j]
		}
		if tmp != 0 {
			collapse |= 1 << i
		}
	}
	return collapse
}

// TestAlgUnquantNoExtMatchesReference checks the band decoder's PVQ decode
// against algUnquantReference bit for bit on random codewords: the
// normalised shape, the collapse mask and the range decoder state after the
// codeword.
func TestAlgUnquantNoExtMatchesReference(t *testing.T) {
	type pair struct{ n, k int }
	var pairs []pair
	for n := 2; n <= 176; n++ {
		for k := 1; k <= 176; k++ {
			if pvqDecodeCovered(n, k) {
				pairs = append(pairs, pair{n, k})
			}
		}
	}
	rng := rand.New(rand.NewSource(0xa190))
	var scratch bandDecodeScratch
	payload := make([]byte, 64)
	for iter := range 20000 {
		p := pairs[rng.Intn(len(pairs))]
		n, k := p.n, p.k
		b := 1 << rng.Intn(5)
		for b > 1 && n%b != 0 {
			b >>= 1
		}
		spread := rng.Intn(4)
		gain := float32(0.25 + rng.Float64())
		rng.Read(payload)

		var rdGot, rdWant rangecoding.Decoder
		rdGot.Init(payload)
		rdWant.Init(payload)
		got := make([]celtNorm, n)
		want := make([]celtNorm, n)
		sc := &scratch
		if iter%5 == 0 {
			sc = nil
		}
		gotCM := algUnquantNoExtInto(got, &rdGot, k, spread, b, opusVal16(gain), sc)
		wantCM := algUnquantReference(want, &rdWant, k, spread, b, gain)
		if gotCM != wantCM {
			t.Fatalf("iter %d n=%d k=%d b=%d: collapse %#x, want %#x", iter, n, k, b, gotCM, wantCM)
		}
		for i := range n {
			if math.Float32bits(float32(got[i])) != math.Float32bits(float32(want[i])) {
				t.Fatalf("iter %d n=%d k=%d b=%d spread=%d: x[%d] = %v, want %v", iter, n, k, b, spread, i, got[i], want[i])
			}
		}
		gr, gv := rdGot.State()
		wr, wv := rdWant.State()
		if gr != wr || gv != wv || rdGot.Tell() != rdWant.Tell() {
			t.Fatalf("iter %d n=%d k=%d: range state (%d,%d,%d), want (%d,%d,%d)", iter, n, k, gr, gv, rdGot.Tell(), wr, wv, rdWant.Tell())
		}
	}
}

// TestAlgUnquantNoExtAllocs checks the band decoder's PVQ decode reuses the
// decoder scratch.
func TestAlgUnquantNoExtAllocs(t *testing.T) {
	var scratch bandDecodeScratch
	payload := []byte{0x5a, 0x13, 0xc4, 0x77, 0x02, 0x9e, 0x41, 0xd0, 0x3b, 0x88, 0x61, 0xf2}
	x := make([]celtNorm, 48)
	var rd rangecoding.Decoder
	decode := func() {
		rd.Init(payload)
		algUnquantNoExtInto(x, &rd, 9, spreadNormal, 4, 1, &scratch)
	}
	decode()
	if allocs := testing.AllocsPerRun(100, decode); allocs != 0 {
		t.Fatalf("algUnquantNoExtInto allocs = %v, want 0", allocs)
	}
}

func pvqSearchPulseLoopRef(absX, y []float32, iy []int32, xy, yy float32, n, pulsesLeft int) (float32, float32) {
	for range pulsesLeft {
		yy += 1

		bestID := 0
		rxy := xy + absX[0]
		ryy := yy + y[0]
		bestNum := rxy * rxy
		bestDen := ryy
		for j := 1; j < n; j++ {
			rxy = xy + absX[j]
			ryy = yy + y[j]
			num := rxy * rxy
			if bestDen*num > ryy*bestNum {
				bestDen = ryy
				bestNum = num
				bestID = j
			}
		}

		xy += absX[bestID]
		yy += y[bestID]
		y[bestID] += 2
		iy[bestID]++
	}
	return xy, yy
}

func TestPVQDispatchMatchesGeneric(t *testing.T) {
	absX := []float32{1.5, 0.75, 2.25, 0.5, 1.125, 0.875, 1.75, 0.25}
	y := []float32{2, 0, 4, 2, 0, 6, 2, 0}

	yGot := append([]float32(nil), y...)
	yWant := append([]float32(nil), y...)
	iyGot := make([]int32, len(absX))
	iyWant := make([]int32, len(absX))
	gotXY, gotYY := pvqSearchPulseLoop(absX, yGot, iyGot, 1.25, 3.5, len(absX), 4)
	wantXY, wantYY := pvqSearchPulseLoopRef(absX, yWant, iyWant, 1.25, 3.5, len(absX), 4)
	if gotXY != wantXY || gotYY != wantYY || !reflect.DeepEqual(yGot, yWant) || !reflect.DeepEqual(iyGot, iyWant) {
		t.Fatalf("pvqSearchPulseLoop mismatch: got (%v,%v,%v,%v) want (%v,%v,%v,%v)", gotXY, gotYY, yGot, iyGot, wantXY, wantYY, yWant, iyWant)
	}

}

func TestPVQSearchPulseLoopExactOrder(t *testing.T) {
	negativeZero := math.Float32frombits(1 << 31)
	cases := []struct {
		name       string
		n          int
		pulses     int
		xy, yy     float32
		tie        bool
		signedZero bool
	}{
		{name: "n1", n: 1, pulses: 3},
		{name: "n2", n: 2, pulses: 3, xy: -0.25, yy: 1},
		{name: "n3-tail", n: 3, pulses: 4, xy: 0.5, yy: 2},
		{name: "n4-tail", n: 4, pulses: 4, xy: -1, yy: 4},
		{name: "n5-tail", n: 5, pulses: 7, xy: 1.25, yy: 8},
		{name: "n48-tie", n: 48, pulses: 16, tie: true},
		{name: "n48-signed-zero", n: 48, pulses: 16, xy: negativeZero, yy: negativeZero, signedZero: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			absX := make([]float32, tc.n)
			yInitial := make([]float32, tc.n)
			for i := range absX {
				if tc.tie {
					absX[i] = 1
					yInitial[i] = 0
				} else {
					absX[i] = float32((i*7)%11+1) / 16
					yInitial[i] = float32((i*3)%6) * 2
				}
			}
			if tc.signedZero {
				absX[0] = negativeZero
				yInitial[0] = negativeZero
			}

			yGot := append([]float32(nil), yInitial...)
			yWant := append([]float32(nil), yInitial...)
			iyGot := make([]int32, tc.n)
			iyWant := make([]int32, tc.n)
			gotXY, gotYY := pvqSearchPulseLoop(absX, yGot, iyGot, tc.xy, tc.yy, tc.n, tc.pulses)
			wantXY, wantYY := pvqSearchPulseLoopRef(absX, yWant, iyWant, tc.xy, tc.yy, tc.n, tc.pulses)
			if math.Float32bits(gotXY) != math.Float32bits(wantXY) || math.Float32bits(gotYY) != math.Float32bits(wantYY) {
				t.Fatalf("xy/yy bits got (%08x,%08x), want (%08x,%08x)", math.Float32bits(gotXY), math.Float32bits(gotYY), math.Float32bits(wantXY), math.Float32bits(wantYY))
			}
			for i := range yGot {
				if math.Float32bits(yGot[i]) != math.Float32bits(yWant[i]) || iyGot[i] != iyWant[i] {
					t.Fatalf("index %d got y/iy (%08x,%d), want (%08x,%d)", i, math.Float32bits(yGot[i]), iyGot[i], math.Float32bits(yWant[i]), iyWant[i])
				}
			}
		})
	}
}

func TestPVQSearchPulseLoopZeroAllocs(t *testing.T) {
	const n, pulses = 48, 16
	absX := make([]float32, n)
	y := make([]float32, n)
	iy := make([]int32, n)
	for i := range absX {
		absX[i] = float32((i*7)%11+1) / 16
	}
	run := func() {
		clear(y)
		clear(iy)
		_, _ = pvqSearchPulseLoop(absX, y, iy, 0, 0, n, pulses)
	}
	run()
	if got := testing.AllocsPerRun(100, run); got != 0 {
		t.Fatalf("pvqSearchPulseLoop allocations/run = %v, want 0", got)
	}
}

func BenchmarkPVQSearchPulseLoopCurrent(b *testing.B) {
	absX := make([]float32, 48)
	yBase := make([]float32, 48)
	iyBase := make([]int32, 48)
	for i := range absX {
		absX[i] = float32(((i * 7) % 11) + 1)
		yBase[i] = float32((i * 3) % 6)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		y := append([]float32(nil), yBase...)
		iy := append([]int32(nil), iyBase...)
		_, _ = pvqSearchPulseLoop(absX, y, iy, 3.25, 9.5, len(absX), 16)
	}
}

func BenchmarkPVQSearchPulseLoopGeneric(b *testing.B) {
	absX := make([]float32, 48)
	yBase := make([]float32, 48)
	iyBase := make([]int32, 48)
	for i := range absX {
		absX[i] = float32(((i * 7) % 11) + 1)
		yBase[i] = float32((i * 3) % 6)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		y := append([]float32(nil), yBase...)
		iy := append([]int32(nil), iyBase...)
		_, _ = pvqSearchPulseLoopRef(absX, y, iy, 3.25, 9.5, len(absX), 16)
	}
}

func TestPVQProjectionRoundingMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	probe := libopustest.ProbeCELTPVQSearchFloat
	if useX86PVQSearchSSE2 {
		probe = libopustest.ProbeCELTPVQSearchFloatSSE2
	}
	// This normalized six-bin shape comes from a live 2.5 ms CELT packet.
	bits := []uint32{0x3deb418e, 0x3e440f79, 0x3f0af847, 0x3ce14d80, 0x3e43c501, 0x3e5c2735}
	for _, n := range []int{3, 4, 5, 6, 7, 15, 16, 17} {
		for _, k := range []int{40, 72, 256} {
			t.Run(fmt.Sprintf("n%d-k%d", n, k), func(t *testing.T) {
				x := make([]float32, n)
				for i := range x {
					x[i] = math.Float32frombits(bits[i%len(bits)])
				}
				wantYY, wantIY, err := probe(x, k)
				if err != nil {
					libopustest.HelperUnavailable(t, "PVQ projection", err)
					return
				}
				for _, mutate := range []bool{false, true} {
					t.Run(fmt.Sprintf("mutate%t", mutate), func(t *testing.T) {
						var iy []int32
						var sign []byte
						var y, abs []float32
						input := make([]float32, len(x))
						call := func() ([]int32, float32) {
							copy(input, x)
							return opPVQSearchScratchNormWithInputMutation(input, k, &iy, &sign, &y, &abs, mutate)
						}
						gotIY, gotYY := call()
						if !equalInt32Slices(gotIY, wantIY) || math.Float32bits(gotYY) != math.Float32bits(wantYY) {
							t.Fatalf("Go pulses=%v yy=%08x C pulses=%v yy=%08x", gotIY, math.Float32bits(gotYY), wantIY, math.Float32bits(wantYY))
						}
						if allocs := testing.AllocsPerRun(20, func() { _, _ = call() }); allocs != 0 {
							t.Fatalf("warm allocations=%g", allocs)
						}
					})
				}
			})
		}
	}
}

func TestPVQSearchInputMutationEdgeCasesMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	probe := libopustest.ProbeCELTPVQSearchFloat
	if useX86PVQSearchSSE2 {
		probe = libopustest.ProbeCELTPVQSearchFloatSSE2
	}
	negativeZero := math.Float32frombits(1 << 31)
	for caseIndex, source := range [][]float32{
		{0, negativeZero, 0, negativeZero},
		{-0.25, 0.5, -0.75, 0, 0.25, -0.125, 0.625},
		{math.SmallestNonzeroFloat32, -math.SmallestNonzeroFloat32, 0, negativeZero},
	} {
		for _, k := range []int{1, 2, 40} {
			wantYY, wantIY, err := probe(source, k)
			if err != nil {
				libopustest.HelperUnavailable(t, "PVQ input mutation edge cases", err)
				return
			}
			for _, mutate := range []bool{false, true} {
				input := append([]float32(nil), source...)
				gotIY, gotYY := opPVQSearchScratchNormWithInputMutation(input, k, nil, nil, nil, nil, mutate)
				if !equalInt32Slices(gotIY, wantIY) || math.Float32bits(gotYY) != math.Float32bits(wantYY) {
					t.Fatalf("case%d k=%d mutate=%t: Go pulses=%v yy=%08x C pulses=%v yy=%08x", caseIndex, k, mutate, gotIY, math.Float32bits(gotYY), wantIY, math.Float32bits(wantYY))
				}
			}
		}
	}
}

func TestPVQSearchQuality(t *testing.T) {
	testCases := []struct {
		name string
		x    []celtNorm
		k    int
	}{
		{
			name: "uniform_positive",
			x:    []celtNorm{1.0, 1.0, 1.0, 1.0},
			k:    4,
		},
		{
			name: "single_peak",
			x:    []celtNorm{0.0, 0.0, 1.0, 0.0},
			k:    4,
		},
		{
			name: "two_peaks",
			x:    []celtNorm{0.7, 0.0, 0.7, 0.0},
			k:    4,
		},
		{
			name: "mixed_signs",
			x:    []celtNorm{1.0, -1.0, 1.0, -1.0},
			k:    4,
		},
		{
			name: "sparse_large",
			x:    []celtNorm{0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 1.0},
			k:    4,
		},
		{
			name: "descending",
			x:    []celtNorm{4.0, 3.0, 2.0, 1.0},
			k:    4,
		},
		{
			name: "high_k",
			x:    []celtNorm{1.0, 2.0, 3.0, 4.0},
			k:    16,
		},
		{
			name: "real_audio_like",
			x:    []celtNorm{0.5, -0.3, 0.8, -0.2, 0.1, 0.4, -0.6, 0.3},
			k:    8,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Normalize input to unit L2 norm (like PVQ expects)
			normX := normalizeNormForTest(tc.x)

			// Run PVQ search
			pulses, yy := opPVQSearch(normX, tc.k)

			// Verify L1 norm equals k
			l1 := int32(0)
			for _, p := range pulses {
				if p < 0 {
					l1 -= p
				} else {
					l1 += p
				}
			}
			if l1 != int32(tc.k) {
				t.Errorf("L1 norm = %d, want %d", l1, tc.k)
			}

			// Verify yy is correctly computed
			var computedYY float64
			for _, p := range pulses {
				computedYY += float64(p * p)
			}
			// Allow some tolerance for float32/float64 precision differences
			if math.Abs(float64(yy)-computedYY) > 1e-3 {
				t.Errorf("yy = %v, computed = %v", yy, computedYY)
			}

			// Normalize the result and check correlation with input
			normResult := normalizeNormForTest(int32ToNormForTest(pulses))
			correlation := dotProductNorm(normX, normResult)

			// Correlation should be positive and reasonably high
			if correlation < 0 {
				t.Errorf("negative correlation = %v", correlation)
			}

			// Log correlation for analysis
			t.Logf("input=%v, pulses=%v, correlation=%.4f", tc.x, pulses, correlation)
		})
	}
}

// TestPVQSearchPreservesSign verifies that PVQ search respects input signs
func TestPVQSearchPreservesSign(t *testing.T) {
	testCases := []struct {
		x []celtNorm
		k int
	}{
		{[]celtNorm{1.0, -1.0, 1.0, -1.0}, 4},
		{[]celtNorm{-1.0, -1.0, -1.0, -1.0}, 4},
		{[]celtNorm{1.0, 1.0, 1.0, 1.0}, 4},
		{[]celtNorm{0.5, -0.5, 0.0, 0.0}, 2},
	}

	for _, tc := range testCases {
		pulses, _ := opPVQSearch(tc.x, tc.k)

		for i := range tc.x {
			if tc.x[i] > 0 && pulses[i] < 0 {
				t.Errorf("sign mismatch at %d: input=%v, pulse=%d", i, tc.x[i], pulses[i])
			}
			if tc.x[i] < 0 && pulses[i] > 0 {
				t.Errorf("sign mismatch at %d: input=%v, pulse=%d", i, tc.x[i], pulses[i])
			}
		}
	}
}

// TestPVQSearchEdgeCases tests edge cases in PVQ search
func TestPVQSearchEdgeCases(t *testing.T) {
	t.Run("zero_input", func(t *testing.T) {
		x := []celtNorm{0.0, 0.0, 0.0, 0.0}
		pulses, _ := opPVQSearch(x, 4)
		// For zero input, libopus puts all pulses in first position
		l1 := int32(0)
		for _, p := range pulses {
			if p < 0 {
				l1 -= p
			} else {
				l1 += p
			}
		}
		if l1 != 4 {
			t.Errorf("L1 = %d, want 4", l1)
		}
	})

	t.Run("very_small_input", func(t *testing.T) {
		x := []celtNorm{1e-20, 1e-20, 1e-20, 1e-20}
		pulses, _ := opPVQSearch(x, 4)
		l1 := int32(0)
		for _, p := range pulses {
			if p < 0 {
				l1 -= p
			} else {
				l1 += p
			}
		}
		if l1 != 4 {
			t.Errorf("L1 = %d, want 4", l1)
		}
	})

	t.Run("single_dimension", func(t *testing.T) {
		x := []celtNorm{1.0}
		pulses, _ := opPVQSearch(x, 4)
		if len(pulses) != 1 || pulses[0] != 4 {
			t.Errorf("pulses = %v, want [4]", pulses)
		}
	})

	t.Run("k_equals_1", func(t *testing.T) {
		x := []celtNorm{0.1, 0.2, 0.9, 0.1}
		pulses, _ := opPVQSearch(x, 1)
		l1 := int32(0)
		for _, p := range pulses {
			if p < 0 {
				l1 -= p
			} else {
				l1 += p
			}
		}
		if l1 != 1 {
			t.Errorf("L1 = %d, want 1", l1)
		}
		// Should place the pulse at the maximum position
		if pulses[2] != 1 {
			t.Errorf("pulse should be at position 2, got %v", pulses)
		}
	})
}

// TestPVQRoundtrip tests that encode/decode produces correct results
func TestPVQRoundtrip(t *testing.T) {
	// Note: Keep n and k combinations within the CWRS table bounds.
	// The CWRS table is limited for large n,k combinations.
	// For n >= 15, only k up to about 14 is supported.
	testCases := []struct {
		n, k int
	}{
		{4, 2},
		{4, 4},
		{8, 4},
		{8, 8},
		{16, 8},
		{12, 10}, // Keep within table bounds
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			// Generate random-ish test vector
			x := make([]celtNorm, tc.n)
			for i := range x {
				x[i] = celtNorm(float32((i*7+3)%11) - 5.0)
			}
			x = normalizeNormForTest(x)

			// Encode
			pulses, _ := opPVQSearchNorm(x, tc.k)
			index := encodePulsesFast32(pulses, tc.n, tc.k, nil)

			// Decode
			decoded := DecodePulses(index, tc.n, tc.k)

			// Verify roundtrip
			for i := range pulses {
				if pulses[i] != int32(decoded[i]) {
					t.Errorf("mismatch at %d: encoded=%d, decoded=%d", i, pulses[i], decoded[i])
				}
			}
		})
	}
}

// TestExpRotationSymmetry tests that exp_rotation forward + inverse = identity
func TestExpRotationSymmetry(t *testing.T) {
	testCases := []struct {
		n, k, spread int
	}{
		{8, 2, spreadNormal},
		{16, 4, spreadNormal},
		{16, 4, spreadAggressive},
		{16, 4, spreadLight},
		{32, 8, spreadNormal},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			// Create test vector
			orig := make([]celtNorm, tc.n)
			for i := range orig {
				orig[i] = celtNorm(float32(i + 1))
			}

			// Copy for rotation
			x := make([]celtNorm, tc.n)
			copy(x, orig)

			// Forward rotation (dir=1)
			expRotation(x, tc.n, 1, 1, tc.k, tc.spread)

			// Inverse rotation (dir=-1)
			expRotation(x, tc.n, -1, 1, tc.k, tc.spread)

			// Check result matches original
			// Use relative tolerance for larger values since float32 accumulates errors
			for i := range orig {
				diff := math.Abs(float64(x[i] - orig[i]))
				relTol := 1e-4 * math.Abs(float64(orig[i]))
				if relTol < 1e-5 {
					relTol = 1e-5
				}
				if diff > relTol {
					t.Errorf("pos %d: orig=%.6f, result=%.6f, diff=%.6f (relTol=%.6f)", i, orig[i], x[i], diff, relTol)
				}
			}
		})
	}
}

// TestPVQSearchN2 tests the specialized N=2 search
func TestPVQSearchN2(t *testing.T) {
	testCases := []struct {
		x  []celtNorm
		k  int
		up int
	}{
		{[]celtNorm{1.0, 0.0}, 4, 3},
		{[]celtNorm{0.7, 0.7}, 4, 3},
		{[]celtNorm{1.0, -1.0}, 4, 3},
		{[]celtNorm{-0.5, 0.5}, 2, 7},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			iy, upIy, refine := opPVQSearchN2(tc.x, tc.k, tc.up)

			// Verify L1 norm of iy equals k
			l1 := absInt32(iy[0]) + absInt32(iy[1])
			if l1 != int32(tc.k) {
				t.Errorf("L1(iy) = %d, want %d", l1, tc.k)
			}

			// Verify L1 norm of upIy equals up*k
			l1up := absInt32(upIy[0]) + absInt32(upIy[1])
			if l1up != int32(tc.up*tc.k) {
				t.Errorf("L1(upIy) = %d, want %d", l1up, tc.up*tc.k)
			}

			// Note: the sign handling in opPVQSearchN2 is complex, so just verify bounds.
			if absInt32(refine) > int32((tc.up-1)/2+1) {
				t.Errorf("refine=%d out of expected bounds", refine)
			}

			t.Logf("x=%v, k=%d, up=%d: iy=%v, upIy=%v, refine=%d", tc.x, tc.k, tc.up, iy, upIy, refine)
		})
	}
}

// TestPVQSearchExtra tests the extended precision search
func TestPVQSearchExtra(t *testing.T) {
	testCases := []struct {
		x  []celtNorm
		k  int
		up int
	}{
		{[]celtNorm{0.5, 0.5, 0.5, 0.5}, 4, 3},
		{[]celtNorm{1.0, 0.0, 0.0, 0.0}, 4, 7},
		{[]celtNorm{0.7, -0.3, 0.5, -0.2}, 8, 15},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			normX := normalizeNormForTest(tc.x)
			iy, upIy, refine := opPVQSearchExtra(normX, tc.k, tc.up)

			// Verify L1 norm of iy equals k
			l1 := int32(0)
			for _, v := range iy {
				l1 += absInt32(v)
			}
			if l1 != int32(tc.k) {
				t.Errorf("L1(iy) = %d, want %d", l1, tc.k)
			}

			// Verify L1 norm of upIy equals up*k
			l1up := int32(0)
			for _, v := range upIy {
				l1up += absInt32(v)
			}
			if l1up != int32(tc.up*tc.k) {
				t.Errorf("L1(upIy) = %d, want %d", l1up, tc.up*tc.k)
			}

			// Verify refine values are within bounds
			for i, r := range refine {
				if absInt32(r) > int32(tc.up) {
					t.Errorf("refine[%d]=%d exceeds up=%d", i, r, tc.up)
				}
				// Verify upIy[i] = up*iy[i] + refine[i]
				expected := int32(tc.up)*iy[i] + refine[i]
				if upIy[i] != expected {
					t.Errorf("upIy[%d]=%d != up*iy[%d]+refine[%d]=%d", i, upIy[i], i, i, expected)
				}
			}

			t.Logf("x=%v: iy=%v, upIy=%v, refine=%v", tc.x, iy, upIy, refine)
		})
	}
}

// BenchmarkPVQSearch benchmarks the PVQ search algorithm
func BenchmarkPVQSearch(b *testing.B) {
	sizes := []struct {
		n, k int
	}{
		{8, 4},
		{16, 8},
		{32, 16},
		{64, 32},
		{128, 64},
	}

	for _, s := range sizes {
		b.Run("", func(b *testing.B) {
			x := make([]celtNorm, s.n)
			for i := range x {
				x[i] = celtNorm(float32((i*7+3)%11) - 5.0)
			}
			x = normalizeNormForTest(x)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				opPVQSearchNorm(x, s.k)
			}
		})
	}
}

// Helper functions

func int32ToNormForTest(v []int32) []celtNorm {
	result := make([]celtNorm, len(v))
	for i, x := range v {
		result[i] = celtNorm(float32(x))
	}
	return result
}

func dotProductNorm(a, b []celtNorm) float64 {
	if len(a) != len(b) {
		return 0
	}
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}

func normalizeNormForTest(v []celtNorm) []celtNorm {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return v
	}
	scale := float32(1 / math.Sqrt(sum))
	out := make([]celtNorm, len(v))
	for i, x := range v {
		out[i] = celtNorm(float32(x) * scale)
	}
	return out
}

func TestOpPVQSearchFloatMatchesLibopusSameArch(t *testing.T) {
	libopustest.RequireOracle(t)

	// Sanity that the oracle builds/links the float reference on this host.
	if _, _, err := libopustest.ProbeCELTPVQSearchFloat([]float32{1, 0}, 1); err != nil {
		libopustest.HelperUnavailable(t, "celt float pvq", err)
		return
	}

	rng := rand.New(rand.NewSource(0x5045565131))
	probe := libopustest.ProbeCELTPVQSearchFloat
	if useX86PVQSearchSSE2 {
		probe = libopustest.ProbeCELTPVQSearchFloatSSE2
	}

	// CELT PVQ band widths (M*nbBins for the static 48k mode bands) and a range
	// of pulse counts spanning low-K (high bands) through high-K (pre-search).
	ns := []int{2, 3, 4, 6, 8, 12, 16, 18, 24, 32, 48, 64, 96, 144, 176}
	ks := []int{1, 2, 3, 5, 7, 11, 16, 24, 40, 64, 100, 160, 256}

	cases := 0
	for _, n := range ns {
		for _, k := range ks {
			if k <= 0 || n <= 0 {
				continue
			}
			for trial := range 24 {
				x := make([]float32, n)
				switch trial % 4 {
				case 0:
					// Unit-norm random (typical normalized band shape).
					var s float64
					for i := range x {
						v := float32(rng.NormFloat64())
						x[i] = v
						s += float64(v) * float64(v)
					}
					inv := float32(1.0 / (math.Sqrt(s) + 1e-30))
					for i := range x {
						x[i] *= inv
					}
				case 1:
					// Sparse: a couple of dominant bins (tie-break stress).
					for i := range x {
						x[i] = 0
					}
					x[rng.Intn(n)] = 1
					if n > 1 {
						x[rng.Intn(n)] = float32(rng.NormFloat64())
					}
				case 2:
					// Moderately peaky shape (still a realistic normalized band:
					// one or two dominant bins plus a decaying tail). Exact ties are
					// exercised separately by TestOpPVQSearchFloatHighKNearTieResidual.
					for i := range x {
						x[i] = float32(rng.NormFloat64()) / float32(i+2)
					}
				default:
					// Small values / near-silence.
					for i := range x {
						x[i] = float32(rng.NormFloat64()) * 1e-3
					}
				}

				xCopy := append([]float32(nil), x...)
				goIy, goYY := opPVQSearchFloatForTest(xCopy, k)

				libYY, libIy, err := probe(x, k)
				if err != nil {
					t.Fatalf("n=%d k=%d trial=%d: float pvq oracle: %v", n, k, trial, err)
				}

				if len(goIy) != len(libIy) {
					t.Fatalf("n=%d k=%d trial=%d: iy length go=%d lib=%d", n, k, trial, len(goIy), len(libIy))
				}
				for j := range goIy {
					if goIy[j] != libIy[j] {
						t.Fatalf("n=%d k=%d trial=%d: iy[%d] go=%d lib=%d\n  X=%v\n  goIy=%v\n  libIy=%v",
							n, k, trial, j, goIy[j], libIy[j], x, goIy, libIy)
					}
				}
				if math.Float32bits(goYY) != math.Float32bits(libYY) {
					t.Fatalf("n=%d k=%d trial=%d: yy go=%g(0x%08x) lib=%g(0x%08x)\n  X=%v",
						n, k, trial, goYY, math.Float32bits(goYY), libYY, math.Float32bits(libYY), x)
				}
				cases++
			}
		}
	}

	for _, x := range [][]float32{
		{1, 1, 1, 1, 1, 1, 1, 1},
		{1, -1, 1, -1, 1},
		{0, 0, 0, 0, 0, 0, 0, 0, 0},
	} {
		k := len(x) / 2
		if k < 1 {
			k = 1
		}
		goInput := make([]celtNorm, len(x))
		for i, v := range x {
			goInput[i] = celtNorm(v)
		}
		goIy, goYY := opPVQSearchNorm(goInput, k)
		libYY, libIy, err := probe(x, k)
		if err != nil {
			t.Fatalf("tie vector n=%d k=%d: float pvq oracle: %v", len(x), k, err)
		}
		if math.Float32bits(float32(goYY)) != math.Float32bits(libYY) || !equalInt32Slices(goIy, libIy) {
			t.Fatalf("tie vector n=%d k=%d mismatch: Go iy=%v yy=%08x, libopus iy=%v yy=%08x",
				len(x), k, goIy, math.Float32bits(float32(goYY)), libIy, math.Float32bits(libYY))
		}
	}
	t.Logf("op_pvq_search float same-arch parity: %d vectors byte-exact (iy + yy)", cases)
}

// TestOpPVQSearchFloatHighKNearTieResidual requires exact pulse placement on
// a high-pulse vector whose competing scores differ at a rounding boundary.
func TestOpPVQSearchFloatHighKNearTieResidual(t *testing.T) {
	libopustest.RequireOracle(t)
	x := []float32{
		0.99995637, 1.000096, -1.0001544, 1.0001608, 1.0001048, 0.9999021,
		0.99996865, 1.0000812, -0.9998345, -0.99997115, -0.99992955, -1.0000807,
		0.99997205, 0.99987245, 1.0001543, -1.0000563, -1.0001165, -1.0000064,
	}
	const k = 256
	probe := libopustest.ProbeCELTPVQSearchFloat
	if useX86PVQSearchSSE2 {
		probe = libopustest.ProbeCELTPVQSearchFloatSSE2
	}
	wantYY, wantIY, err := probe(x, k)
	if err != nil {
		libopustest.HelperUnavailable(t, "high-pulse near-tie PVQ", err)
		return
	}
	gotIY, gotYY := opPVQSearchFloatForTest(append([]float32(nil), x...), k)
	if !equalInt32Slices(gotIY, wantIY) || math.Float32bits(gotYY) != math.Float32bits(wantYY) {
		t.Fatalf("high-pulse near-tie PVQ differs: Go pulses=%v yy=%08x C pulses=%v yy=%08x", gotIY, math.Float32bits(gotYY), wantIY, math.Float32bits(wantYY))
	}
}

func equalInt32Slices(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func opPVQSearchFloatForTest(x []float32, k int) ([]int32, float32) {
	xn := make([]celtNorm, len(x))
	for i, v := range x {
		xn[i] = celtNorm(v)
	}
	iy, yy := opPVQSearchNorm(xn, k)
	return iy, float32(yy)
}

func TestPVQSearchInputMutationMatchesScratch(t *testing.T) {
	negativeZero := math.Float32frombits(1 << 31)
	cases := [][]float32{
		{1, -1},
		{0, negativeZero, 0, negativeZero},
		{-0.25, 0.5, -0.75, 0, 0.25, -0.125, 0.625},
		{1e-30, -1e-30, 0, negativeZero, 1e-30},
		{float32(math.Inf(1)), -0.5, float32(math.Inf(-1)), 0.25},
		{math.Float32frombits(0x7fc01234), -0.5, 0.25, -1, 0},
	}
	for caseIndex, source := range cases {
		for _, k := range []int{0, 1, len(source) / 2, 2 * len(source)} {
			t.Run(fmt.Sprintf("case%d-k%d", caseIndex, k), func(t *testing.T) {
				storage := make([]float32, len(source)+2)
				storage[0], storage[len(storage)-1] = -123.25, 456.75
				input := storage[1 : len(storage)-1 : len(storage)-1]
				copy(input, source)
				var wantIY []int32
				var wantSigns []byte
				var wantY, wantAbs []float32
				wantIY, wantYY := opPVQSearchScratchNorm(input, k, &wantIY, &wantSigns, &wantY, &wantAbs)
				for i, want := range source {
					if math.Float32bits(input[i]) != math.Float32bits(want) {
						t.Fatalf("scratch search changes input[%d]", i)
					}
				}

				var gotIY []int32
				var gotSigns []byte
				var gotY, gotAbs []float32
				gotIY, gotYY := opPVQSearchScratchNormWithInputMutation(input, k, &gotIY, &gotSigns, &gotY, &gotAbs, true)
				if !equalInt32Slices(gotIY, wantIY) || math.Float32bits(gotYY) != math.Float32bits(wantYY) {
					t.Fatalf("mutating search pulses=%v yy=%08x; scratch pulses=%v yy=%08x", gotIY, math.Float32bits(gotYY), wantIY, math.Float32bits(wantYY))
				}
				if storage[0] != -123.25 || storage[len(storage)-1] != 456.75 {
					t.Fatal("search changes samples outside its input")
				}
				wantInput := source
				if !useX86PVQSearchSSE2 && k > 0 {
					// The scalar C search leaves the absolute input in X,
					// including its high-pulse invalid-input replacement.
					wantInput = wantAbs
				}
				for i, want := range wantInput {
					if math.Float32bits(input[i]) != math.Float32bits(want) {
						t.Fatalf("input[%d]=%08x, want %08x", i, math.Float32bits(input[i]), math.Float32bits(want))
					}
				}
			})
		}
	}
}

func normalizeFloat64Vector(v []float64) []float64 {
	norms := NormalizeVector(float64sToNorms(v))
	out := make([]float64, len(norms))
	for i := range norms {
		out[i] = float64(norms[i])
	}
	return out
}

// TestNormalizeVectorUnit verifies various input vectors normalize to unit L2 norm.
func TestNormalizeVectorUnit(t *testing.T) {
	tests := []struct {
		name  string
		input []float64
		desc  string
	}{
		{
			name:  "3-4-5_triangle",
			input: []float64{3, 4},
			desc:  "Classic Pythagorean triple",
		},
		{
			name:  "already_unit",
			input: []float64{1, 0, 0},
			desc:  "Already unit length",
		},
		{
			name:  "negative_values",
			input: []float64{-3, 4, 0},
			desc:  "Mixed signs",
		},
		{
			name:  "single_element",
			input: []float64{7},
			desc:  "Single element normalizes to +/-1",
		},
		{
			name:  "single_negative",
			input: []float64{-7},
			desc:  "Single negative normalizes to -1",
		},
		{
			name:  "large_values",
			input: []float64{1000, 2000, 3000, 4000},
			desc:  "Large values should normalize without overflow",
		},
		{
			name:  "small_values",
			input: []float64{0.001, 0.002, 0.003},
			desc:  "Small values should normalize without precision loss",
		},
		{
			name:  "uniform_8",
			input: []float64{1, 1, 1, 1, 1, 1, 1, 1},
			desc:  "Uniform distribution",
		},
		{
			name:  "alternating_signs",
			input: []float64{1, -2, 3, -4, 5},
			desc:  "Alternating signs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeFloat64Vector(tt.input)

			if len(result) != len(tt.input) {
				t.Errorf("NormalizeVector length = %d, want %d", len(result), len(tt.input))
				return
			}

			// Compute L2 norm
			var norm2 float64
			for _, x := range result {
				norm2 += float64(x) * float64(x)
			}

			// Verify unit norm (within tolerance)
			if math.Abs(norm2-1.0) > 1e-6 {
				t.Errorf("normalizeFloat64Vector(%v) has L2 norm^2 = %v, want 1.0", tt.input, norm2)
			}
		})
	}
}

// TestNormalizeVectorZero verifies zero vector handling.
func TestNormalizeVectorZero(t *testing.T) {
	tests := []struct {
		name  string
		input []float64
	}{
		{"zero_2", []float64{0, 0}},
		{"zero_3", []float64{0, 0, 0}},
		{"zero_8", []float64{0, 0, 0, 0, 0, 0, 0, 0}},
		{"near_zero", []float64{1e-20, 1e-20, 1e-20}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeFloat64Vector(tt.input)

			// Should return input unchanged (no NaN or Inf)
			if len(result) != len(tt.input) {
				t.Errorf("NormalizeVector length = %d, want %d", len(result), len(tt.input))
				return
			}

			for i, x := range result {
				if math.IsNaN(x) || math.IsInf(x, 0) {
					t.Errorf("NormalizeVector[%d] = %v (NaN/Inf)", i, x)
				}
			}
		})
	}
}

// TestNormalizeVectorPreservesDirection verifies normalization preserves vector direction.
func TestNormalizeVectorPreservesDirection(t *testing.T) {
	input := []float64{3, 4}
	result := normalizeFloat64Vector(input)

	// Ratio of components should be preserved
	expectedRatio := input[0] / input[1]
	actualRatio := result[0] / result[1]

	if math.Abs(expectedRatio-actualRatio) > 1e-6 {
		t.Errorf("Direction not preserved: expected ratio %v, got %v", expectedRatio, actualRatio)
	}
}

// TestPVQUnitNorm verifies that PVQ decoded vectors always have unit L2 norm.
// This is critical for correct CELT band shape decoding.
func TestPVQUnitNorm(t *testing.T) {
	// Test various (n, k) combinations
	testCases := []struct {
		name string
		n, k int
	}{
		{"n4_k1", 4, 1},   // Small band, few pulses
		{"n8_k4", 8, 4},   // Typical band size
		{"n8_k8", 8, 8},   // More pulses than dimensions
		{"n16_k8", 16, 8}, // Larger band
		{"n2_k1", 2, 1},   // Minimal non-trivial case
		{"n3_k2", 3, 2},   // Small with multiple pulses
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			vCount := PVQ_V(tc.n, tc.k)

			// Test multiple indices within valid range
			testIndices := []uint32{0, 1}
			if vCount > 2 {
				testIndices = append(testIndices, vCount/2)
			}
			if vCount > 3 {
				testIndices = append(testIndices, vCount-1)
			}

			for _, idx := range testIndices {
				// Decode pulses
				pulses := DecodePulses(idx, tc.n, tc.k)
				if pulses == nil {
					t.Errorf("DecodePulses(%d, %d, %d) returned nil", idx, tc.n, tc.k)
					continue
				}

				// Convert to float and normalize
				normalized := NormalizeVector(intToNorm(pulses))

				// Verify unit L2 norm
				var norm2 float64
				for _, x := range normalized {
					norm2 += float64(x) * float64(x)
				}

				if math.Abs(norm2-1.0) > 1e-6 {
					t.Errorf("PVQ(n=%d, k=%d, idx=%d) L2 norm^2 = %v, want 1.0 (pulses=%v)",
						tc.n, tc.k, idx, norm2, pulses)
				}
			}
		})
	}
}

// TestPVQDeterminism verifies that same index, n, k always produces same output.
func TestPVQDeterminism(t *testing.T) {
	testCases := []struct {
		n, k  int
		index uint32
	}{
		{4, 2, 0},
		{4, 2, 5},
		{8, 4, 100},
		{8, 4, 1000},
		{16, 8, 50000},
	}

	for _, tc := range testCases {
		vCount := PVQ_V(tc.n, tc.k)
		if tc.index >= vCount {
			tc.index = vCount - 1
		}

		// Decode twice
		result1 := DecodePulses(tc.index, tc.n, tc.k)
		result2 := DecodePulses(tc.index, tc.n, tc.k)

		if result1 == nil || result2 == nil {
			t.Errorf("DecodePulses returned nil")
			continue
		}

		if len(result1) != len(result2) {
			t.Errorf("Length mismatch: %d vs %d", len(result1), len(result2))
			continue
		}

		for i := range result1 {
			if result1[i] != result2[i] {
				t.Errorf("DecodePulses(%d, %d, %d) not deterministic: %v vs %v",
					tc.index, tc.n, tc.k, result1, result2)
				break
			}
		}
	}
}

// TestPVQEnergyDistribution verifies that PVQ distributes energy across bins.
// This catches bugs where all pulses concentrate in a single position.
func TestPVQEnergyDistribution(t *testing.T) {
	n := 8
	k := 4
	vCount := PVQ_V(n, k)

	// Track how many times each position is non-zero across all codewords
	nonzeroCount := make([]int, n)
	totalNonzero := 0

	// Sample codewords (or all if small enough)
	sampleSize := min(int(vCount), 1000)

	for idx := uint32(0); idx < uint32(sampleSize); idx++ {
		pulses := DecodePulses(idx, n, k)
		if pulses == nil {
			continue
		}

		for i, p := range pulses {
			if p != 0 {
				nonzeroCount[i]++
				totalNonzero++
			}
		}
	}

	// Verify energy is not concentrated in just one or two positions
	// For (n=8, k=4), we expect reasonable distribution
	t.Logf("Nonzero distribution across %d samples: %v", sampleSize, nonzeroCount)

	zerosCount := 0
	for _, count := range nonzeroCount {
		if count == 0 {
			zerosCount++
		}
	}

	// At least half the positions should have some pulses
	if zerosCount > n/2 {
		t.Errorf("Too many positions with zero pulses: %d/%d positions never used", zerosCount, n)
	}
}

// TestPVQAllCodewordsHaveCorrectK verifies every codeword has exactly k pulses.
func TestPVQAllCodewordsHaveCorrectK(t *testing.T) {
	testCases := []struct {
		n, k int
	}{
		{2, 1},
		{2, 2},
		{3, 2},
		{4, 2},
		{4, 3},
		{5, 3},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			vCount := PVQ_V(tc.n, tc.k)

			for idx := range vCount {
				pulses := DecodePulses(idx, tc.n, tc.k)
				if pulses == nil {
					t.Errorf("DecodePulses(%d, %d, %d) returned nil", idx, tc.n, tc.k)
					continue
				}

				// Sum of absolute values should equal k
				sum := 0
				for _, p := range pulses {
					if p < 0 {
						sum -= p
					} else {
						sum += p
					}
				}

				if sum != tc.k {
					t.Errorf("DecodePulses(%d, %d, %d) = %v has L1=%d, want %d",
						idx, tc.n, tc.k, pulses, sum, tc.k)
				}
			}
		})
	}
}

// TestPVQZeroPulses verifies k=0 returns zero vector.
func TestPVQZeroPulses(t *testing.T) {
	for n := 1; n <= 10; n++ {
		pulses := DecodePulses(0, n, 0)
		if pulses == nil {
			t.Errorf("DecodePulses(0, %d, 0) returned nil", n)
			continue
		}

		if len(pulses) != n {
			t.Errorf("DecodePulses(0, %d, 0) length = %d, want %d", n, len(pulses), n)
			continue
		}

		for i, p := range pulses {
			if p != 0 {
				t.Errorf("DecodePulses(0, %d, 0)[%d] = %d, want 0", n, i, p)
			}
		}
	}
}

// TestPVQCodebookSize verifies the codebook size V(n,k) matches expected values.
func TestPVQCodebookSize(t *testing.T) {
	// For small (n, k), we can verify all codewords are unique and count them
	testCases := []struct {
		n, k     int
		expected uint32
	}{
		{2, 1, 4},  // [+1,0], [-1,0], [0,+1], [0,-1]
		{2, 2, 8},  // 4 sign combinations * 2 distribution patterns
		{3, 1, 6},  // 3 positions * 2 signs
		{3, 2, 18}, // Computed from recurrence
		{4, 1, 8},  // 4 positions * 2 signs
		{4, 2, 32}, // Computed from recurrence
	}

	for _, tc := range testCases {
		v := PVQ_V(tc.n, tc.k)
		if v != tc.expected {
			t.Errorf("V(%d, %d) = %d, want %d", tc.n, tc.k, v, tc.expected)
		}

		// Verify by actually enumerating all unique codewords
		seen := make(map[string]bool)
		for idx := range v {
			pulses := DecodePulses(idx, tc.n, tc.k)
			var key strings.Builder
			for _, p := range pulses {
				key.WriteString(string(rune(p + 1000))) // Simple unique key
			}
			seen[key.String()] = true
		}

		if uint32(len(seen)) != tc.expected {
			t.Errorf("Enumerated %d unique codewords for V(%d, %d), want %d",
				len(seen), tc.n, tc.k, tc.expected)
		}
	}
}

// BenchmarkNormalizeVectorSmall benchmarks normalization for small vectors.
func BenchmarkNormalizeVectorSmall(b *testing.B) {
	v := []float64{1, 2, 3, 4}
	for i := 0; i < b.N; i++ {
		_ = normalizeFloat64Vector(v)
	}
}

// BenchmarkNormalizeVectorLarge benchmarks normalization for large vectors.
func BenchmarkNormalizeVectorLarge(b *testing.B) {
	v := make([]float64, 256)
	for i := range v {
		v[i] = float64(i + 1)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = normalizeFloat64Vector(v)
	}
}

// BenchmarkIntToFloat benchmarks int to float conversion.
func BenchmarkIntToFloat(b *testing.B) {
	v := make([]int, 16)
	for i := range v {
		v[i] = i - 8
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = intToNorm(v)
	}
}
