//go:build amd64

package celt

import (
	"math"
	"math/rand"
	"testing"
)

//go:noinline
func kissMulAddSourceBeforeInline(a, b, c, d float32) float32 {
	if kissFFTTargetV3FMA {
		return fma32(a, b, noFMA32Mul(c, d))
	}
	return a*b + c*d
}

//go:noinline
func kissMulSubSourceBeforeInline(a, b, c, d float32) float32 {
	if kissFFTTargetV3FMA {
		return fma32(a, b, -noFMA32Mul(c, d))
	}
	return a*b - c*d
}

//go:noinline
func kissMulSubSourceNoInlineForTest(a, b, c, d float32) (float32, bool) {
	return kissMulSubSource(a, b, c, d)
}

func TestKissMulSourceInliningPreservesScalarOrder(t *testing.T) {
	patterns := []uint32{
		0x00000000, 0x80000000, 0x00000001, 0x80000001,
		0x007fffff, 0x807fffff, 0x00800000, 0x80800000,
		0x3f800000, 0xbf800000, 0x40490fdb, 0xc0490fdb,
		0x7f7fffff, 0xff7fffff, 0x7f800000, 0xff800000,
		0x7fc00001, 0x7fc12345, 0xffc54321,
	}
	values := make([]float32, len(patterns))
	for i, bits := range patterns {
		values[i] = math.Float32frombits(bits)
	}
	for _, a := range values {
		for _, b := range values {
			for _, c := range values {
				for _, d := range values {
					assertKissMulSourceBits(t, a, b, c, d)
				}
			}
		}
	}

	rng := rand.New(rand.NewSource(0x4b495353))
	for i := 0; i < 100_000; i++ {
		a, b, c, d := math.Float32frombits(rng.Uint32()), math.Float32frombits(rng.Uint32()), math.Float32frombits(rng.Uint32()), math.Float32frombits(rng.Uint32())
		assertKissMulSourceBits(t, a, b, c, d)
	}
}

func assertKissMulSourceBits(t *testing.T, a, b, c, d float32) {
	t.Helper()
	assertKissMulAddSourceBits(t, a, b, c, d)
	assertKissMulSubSourceBits(t, a, b, c, d)
}

func assertKissMulAddSourceBits(t *testing.T, a, b, c, d float32) {
	t.Helper()
	got, needsFallback := kissMulAddSource(a, b, c, d)
	want := kissMulAddSourceBeforeInline(a, b, c, d)
	assertKissMulSourceResult(t, "mul-add", got, needsFallback, want, func() float32 {
		return kissMulAddSourceNonFinite(a, b, c, d)
	})
}

func assertKissMulSubSourceBits(t *testing.T, a, b, c, d float32) {
	t.Helper()
	got, needsFallback := kissMulSubSource(a, b, c, d)
	want := kissMulSubSourceBeforeInline(a, b, c, d)
	assertKissMulSourceResult(t, "mul-sub", got, needsFallback, want, func() float32 {
		return kissMulSubSourceNonFinite(a, b, c, d)
	})
}

func assertKissMulSourceResult(t *testing.T, op string, got float32, needsFallback bool, want float32, fallback func() float32) {
	t.Helper()
	gotBits, wantBits := math.Float32bits(got), math.Float32bits(want)
	if kissFFTTargetV3FMA {
		if needsFallback {
			t.Fatalf("%s requested a non-finite fallback on the fused target", op)
		}
		if gotBits != wantBits {
			t.Fatalf("%s fused result=%08x want %08x", op, gotBits, wantBits)
		}
		return
	}
	wantIsNaN := wantBits&0x7fffffff > 0x7f800000
	if needsFallback != wantIsNaN {
		t.Fatalf("%s exceptional flag=%t for result %08x; want %t for %08x", op, needsFallback, gotBits, wantIsNaN, wantBits)
	}
	if !wantIsNaN {
		if gotBits != wantBits {
			t.Fatalf("%s fast result=%08x want %08x", op, gotBits, wantBits)
		}
		return
	}
	if gotBits&0x7fffffff <= 0x7f800000 {
		t.Fatalf("%s fallback requested for non-NaN fast result %08x", op, gotBits)
	}
	if fallbackBits := math.Float32bits(fallback()); fallbackBits != wantBits {
		t.Fatalf("%s exceptional fallback=%08x want exact legacy bits %08x", op, fallbackBits, wantBits)
	}
}

func TestKissMulSourceContractionWitness(t *testing.T) {
	a := math.Float32frombits(0xbf661f26)
	b := math.Float32frombits(0x3f7ffa63)
	c := math.Float32frombits(0xbe859cfc)
	d := math.Float32frombits(0xbc5675bf)
	got, exceptional := kissMulSubSourceNoInlineForTest(a, b, c, d)
	if exceptional {
		t.Fatal("finite C_MUL witness requested a non-finite fallback")
	}
	wantBits := uint32(0xbf66f9f8)
	if !kissFFTTargetV3FMA {
		wantBits = 0xbf66f9f7
	}
	if gotBits := math.Float32bits(got); gotBits != wantBits {
		t.Fatalf("kissMulSubSource witness=%08x, want %08x", gotBits, wantBits)
	}
}
