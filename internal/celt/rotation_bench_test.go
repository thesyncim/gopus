package celt

import (
	"math"
	"math/rand"
	"strconv"
	"testing"
)

func benchmarkExpRotation1(b *testing.B, length, stride int) {
	b.Helper()
	if length <= 0 || stride <= 0 {
		b.Fatal("invalid benchmark params")
	}
	x := make([]celtNorm, length+stride+8)
	for i := range x {
		x[i] = celtNorm(float32(math.Sin(float64(i) * 0.137)))
	}
	c := opusVal16(0.9238795325) // cos(pi/8)
	s := opusVal16(0.3826834324) // sin(pi/8)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		expRotation1(x, length, stride, c, s)
	}
}

func BenchmarkExpRotation1Stride1Len32(b *testing.B) { benchmarkExpRotation1(b, 32, 1) }
func BenchmarkExpRotation1Stride1Len64(b *testing.B) { benchmarkExpRotation1(b, 64, 1) }
func BenchmarkExpRotation1Stride2Len32(b *testing.B) { benchmarkExpRotation1(b, 32, 2) }
func BenchmarkExpRotation1Stride4Len32(b *testing.B) { benchmarkExpRotation1(b, 32, 4) }

func testRotationUnit(t *testing.T, n, k int, rnd *rand.Rand) {
	t.Helper()

	x0 := make([]celtNorm, n)
	x1 := make([]celtNorm, n)
	for i := range n {
		v := celtNorm(float32(rnd.Intn(16777215) - 8388608))
		x0[i] = v
		x1[i] = v
	}

	expRotation(x1, n, 1, 1, k, spreadNormal)
	var err, ener float64
	for i := range n {
		d := float64(x0[i] - x1[i])
		err += d * d
		ener += float64(x0[i]) * float64(x0[i])
	}
	snr0 := 20 * math.Log10(ener/err)

	err, ener = 0, 0
	expRotation(x1, n, -1, 1, k, spreadNormal)
	for i := range n {
		d := float64(x0[i] - x1[i])
		err += d * d
		ener += float64(x0[i]) * float64(x0[i])
	}
	snr := 20 * math.Log10(ener/err)

	t.Logf("SNR for size %d (%d pulses) is %f (was %f without inverse)", n, k, snr, snr0)
	if snr < 60 || snr0 > 20 {
		t.Fatalf("rotation SNR out of range: snr=%f snr0=%f", snr, snr0)
	}
}

func TestRotationUnitLibopus(t *testing.T) {
	rnd := rand.New(rand.NewSource(0))
	cases := []struct {
		n int
		k int
	}{
		{n: 15, k: 3},
		{n: 23, k: 5},
		{n: 50, k: 3},
		{n: 80, k: 1},
	}

	for _, tc := range cases {
		t.Run("N"+strconv.Itoa(tc.n)+"K"+strconv.Itoa(tc.k), func(t *testing.T) {
			testRotationUnit(t, tc.n, tc.k, rnd)
		})
	}
}
