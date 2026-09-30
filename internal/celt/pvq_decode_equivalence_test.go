package celt

import (
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// pvqDecodeCovered reports whether the decoder's static-table path handles
// (n, k): the pairs libopus decodes with its CELT_PVQ_U table.
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
