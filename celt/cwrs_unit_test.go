package celt

import (
	"os"
	"strconv"
	"testing"
)

// TestCWRSUnitLibopus is a port of libopus celt/tests/test_unit_cwrs32.c.
func TestCWRSUnitLibopus(t *testing.T) {
	dims := []int{
		2, 3, 4, 6, 8, 9, 11, 12, 16,
		18, 22, 24, 32, 36, 44, 48, 64, 72,
		88, 96, 144, 176,
	}
	pkmax := []int{
		128, 128, 128, 88, 36, 26, 18, 16, 12,
		11, 9, 9, 7, 7, 6, 6, 5, 5,
		5, 5, 4, 4,
	}

	maxPseudo := 40
	sampleDiv := uint32(20000)
	if env := os.Getenv("CWRS_UNIT_SAMPLES"); env != "" {
		if v, err := strconv.Atoi(env); err == nil && v > 0 {
			sampleDiv = uint32(v)
		}
	}
	if testing.Short() {
		dims = dims[:8]
		pkmax = pkmax[:8]
		maxPseudo = 10
		sampleDiv = 2000
	}

	for ti, n := range dims {
		for pseudo := 1; pseudo <= maxPseudo; pseudo++ {
			k := getPulses(pseudo)
			if k > pkmax[ti] {
				break
			}

			uBase := make([]uint32, k+2)
			nc := ncwrsUrow(n, k, uBase)
			if nc == 0 {
				t.Fatalf("ncwrsUrow returned 0 for n=%d k=%d", n, k)
			}
			inc := nc / sampleDiv
			if inc < 1 {
				inc = 1
			}

			for i := uint32(0); i < nc; i += inc {
				u := make([]uint32, k+2)
				copy(u, uBase)
				y := make([]int, n)
				_ = cwrsi(n, k, i, y, u)

				sum := 0
				for _, v := range y {
					if v < 0 {
						sum -= v
					} else {
						sum += v
					}
				}
				if sum != k {
					t.Fatalf("pulse sum mismatch n=%d k=%d i=%d sum=%d", n, k, i, sum)
				}

				ii, vcount := icwrs(n, k, y, make([]uint32, k+2))
				if ii != i {
					t.Fatalf("index mismatch n=%d k=%d i=%d got=%d", n, k, i, ii)
				}
				if vcount != nc {
					t.Fatalf("count mismatch n=%d k=%d want=%d got=%d", n, k, nc, vcount)
				}
			}
		}
	}
}
