package celt

import (
	"math"
	"runtime"
	"testing"
)

// TestNoiseFloorMatchesLibopusFloat checks computeNoiseFloor32 against the
// dynalloc_analysis() noise_floor values of a gcc -O2 float build of
// celt_encoder.c for the 48 kHz logN table, where GCONST(.0062f)*(i+5)*(i+5)
// rounds after each multiply. On arm64 the paired clang -O3 build contracts
// the final "+ .0062f*(i+5)*(i+5)" into an fmadd, as Go does, and
// arm64Golden holds the clang arm64 bits where that fused add rounds
// differently.
func TestNoiseFloorMatchesLibopusFloat(t *testing.T) {
	golden := []struct {
		lsbDepth, band int
		bits           uint32
	}{
		{16, 0, 0xc14c851f},
		{16, 1, 0xc1486dc6},
		{16, 2, 0xc13f23a3},
		{16, 3, 0xc136a6b5},
		{16, 4, 0xc130f6fd},
		{16, 5, 0xc12b147b},
		{16, 6, 0xc123ff2e},
		{16, 7, 0xc11fb717},
		{16, 8, 0xc11d3c36},
		{16, 9, 0xc1178e8a},
		{16, 10, 0xc112ae14},
		{16, 11, 0xc10d9ad4},
		{16, 12, 0xc10954ca},
		{16, 13, 0xc101dbf4},
		{16, 14, 0xc0f260aa},
		{16, 15, 0xc0e6a3d7},
		{16, 16, 0xc0da816f},
		{16, 17, 0xc0d3f972},
		{16, 18, 0xc0c50be1},
		{16, 19, 0xc0a7b8bb},
		{16, 20, 0xc0840000},
		{24, 0, 0xc1a6428f},
		{24, 1, 0xc1a436e3},
		{24, 2, 0xc19f91d1},
		{24, 3, 0xc19b535b},
		{24, 4, 0xc1987b7f},
		{24, 5, 0xc1958a3d},
		{24, 6, 0xc191ff97},
		{24, 7, 0xc18fdb8c},
		{24, 8, 0xc18e9e1b},
		{24, 9, 0xc18bc745},
		{24, 10, 0xc189570a},
		{24, 11, 0xc186cd6a},
		{24, 12, 0xc184aa65},
		{24, 13, 0xc180edfa},
		{24, 14, 0xc1793055},
		{24, 15, 0xc17351ec},
		{24, 16, 0xc16d40b8},
		{24, 17, 0xc169fcb9},
		{24, 18, 0xc16285f0},
		{24, 19, 0xc153dc5e},
		{24, 20, 0xc1420000},
	}
	arm64Golden := map[[2]int]uint32{
		{16, 12}: 0xc10954c9,
		{24, 15}: 0xc17351eb,
		{24, 19}: 0xc153dc5d,
	}
	for _, g := range golden {
		if bits, ok := arm64Golden[[2]int{g.lsbDepth, g.band}]; ok && runtime.GOARCH == "arm64" {
			g.bits = bits
		}
		got := computeNoiseFloor32(g.band, g.lsbDepth, int16(LogN[g.band]))
		if math.Float32bits(got) != g.bits {
			t.Errorf("lsb=%d band=%d: noise floor %08x want %08x", g.lsbDepth, g.band, math.Float32bits(got), g.bits)
		}
	}
}
