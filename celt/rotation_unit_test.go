package celt

import (
	"math"
	"math/rand"
	"strconv"
	"testing"
)

func testRotationUnit(t *testing.T, n, k int, rnd *rand.Rand) {
	t.Helper()

	x0 := make([]float64, n)
	x1 := make([]float64, n)
	for i := 0; i < n; i++ {
		v := float64(rnd.Intn(16777215) - 8388608)
		x0[i] = v
		x1[i] = v
	}

	expRotation(x1, n, 1, 1, k, spreadNormal)
	var err, ener float64
	for i := 0; i < n; i++ {
		d := x0[i] - x1[i]
		err += d * d
		ener += x0[i] * x0[i]
	}
	snr0 := 20 * math.Log10(ener/err)

	err, ener = 0, 0
	expRotation(x1, n, -1, 1, k, spreadNormal)
	for i := 0; i < n; i++ {
		d := x0[i] - x1[i]
		err += d * d
		ener += x0[i] * x0[i]
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
