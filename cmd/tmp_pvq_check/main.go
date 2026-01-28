package main

import (
	"fmt"
	"github.com/thesyncim/gopus/internal/celt"
)

func main() {
	// Find max k that fits in 32-bit for n=16
	for n := 4; n <= 64; n *= 2 {
		fmt.Printf("n=%d:\n", n)
		for k := 1; k <= 128; k++ {
			v := celt.PVQ_V(n, k)
			if v == 0xFFFFFFFF {
				fmt.Printf("  max k for 32-bit: %d (V(n,%d)=overflow)\n", k-1, k)
				break
			}
			if k == 128 {
				fmt.Printf("  k=128 still fits: V=%d\n", v)
			}
		}
	}

	fmt.Println("\nThis shows the limits from libopus test_unit_cwrs32.c:")
	// From cwrs_libopus_test.go
	cwrsPN := []int{
		2, 3, 4, 6, 8, 9, 11, 12, 16,
		18, 22, 24, 32, 36, 44, 48, 64, 72,
		88, 96, 144, 176,
	}
	cwrsPKMax := []int{
		128, 128, 128, 88, 36, 26, 18, 16, 12,
		11, 9, 9, 7, 7, 6, 6, 5, 5,
		5, 5, 4, 4,
	}
	for i, n := range cwrsPN {
		kmax := cwrsPKMax[i]
		fmt.Printf("n=%d: max_k=%d\n", n, kmax)
	}
}
