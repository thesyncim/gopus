package silk

import (
	"testing"
	"unsafe"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestSILKEncoderPreviousGainIndexMatchesLibopusShapeWidth(t *testing.T) {
	libopustest.RequireOracle(t)
	want, err := probeLibopusSILKGain(libopusSILKGainModeShapeStateSizes, [][]int32{{}})
	if err != nil {
		libopustest.HelperUnavailable(t, "silk gain", err)
	}
	var enc Encoder
	if got := unsafe.Sizeof(enc.previousGainIndex); got != uintptr(want[0].first) {
		t.Fatalf("previousGainIndex size=%d want libopus LastGainIndex size %d", got, want[0].first)
	}
}

func TestSILKGainsQuantMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name        string
		nbSubfr     int
		prev        int8
		conditional bool
		gains       [maxNbSubfr]int32
	}{
		{name: "four_unconditional_ramp", nbSubfr: 4, prev: 10, gains: [maxNbSubfr]int32{65536, 262144, 1048576, 4194304}},
		{name: "four_conditional_drop", nbSubfr: 4, prev: 38, conditional: true, gains: [maxNbSubfr]int32{8388608, 2097152, 524288, 131072}},
		{name: "two_unconditional_high_prev", nbSubfr: 2, prev: 52, gains: [maxNbSubfr]int32{16777216, 33554432, 123456, 789012}},
		{name: "two_conditional_double_step", nbSubfr: 2, prev: 6, conditional: true, gains: [maxNbSubfr]int32{32768, 536870912, 222222, 333333}},
	}

	records := make([][]int32, len(cases))
	for i, tc := range cases {
		record := []int32{int32(tc.nbSubfr), int32(tc.prev), LibopusGainBoolWord(tc.conditional)}
		record = append(record, tc.gains[:]...)
		records[i] = record
	}
	want, err := probeLibopusSILKGain(libopusSILKGainModeQuant, records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk gain", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ind := make([]int8, maxNbSubfr)
			gains := tc.gains
			gotPrev := silkGainsQuantInto(ind, gains[:], tc.prev, tc.conditional, tc.nbSubfr)
			if int32(gotPrev) != want[i].first {
				t.Fatalf("prev=%d want %d", gotPrev, want[i].first)
			}
			for j := 0; j < maxNbSubfr; j++ {
				if ind[j] != want[i].ind[j] {
					t.Fatalf("ind[%d]=%d want %d", j, ind[j], want[i].ind[j])
				}
				if gains[j] != want[i].gains[j] {
					t.Fatalf("gain[%d]=%d want %d", j, gains[j], want[i].gains[j])
				}
			}
		})
	}
}

func TestSILKGainsDequantMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name        string
		nbSubfr     int
		prev        int8
		conditional bool
		ind         [maxNbSubfr]int8
	}{
		{name: "four_unconditional", nbSubfr: 4, prev: 22, ind: [maxNbSubfr]int8{18, 6, 5, 4}},
		{name: "four_conditional", nbSubfr: 4, prev: 40, conditional: true, ind: [maxNbSubfr]int8{0, 1, 8, 20}},
		{name: "two_unconditional", nbSubfr: 2, prev: 50, ind: [maxNbSubfr]int8{10, 7, 0, 0}},
		{name: "two_conditional_double_step", nbSubfr: 2, prev: 3, conditional: true, ind: [maxNbSubfr]int8{36, 36, 0, 0}},
	}

	records := make([][]int32, len(cases))
	for i, tc := range cases {
		record := []int32{int32(tc.nbSubfr), int32(tc.prev), LibopusGainBoolWord(tc.conditional)}
		for _, ind := range tc.ind {
			record = append(record, int32(ind))
		}
		records[i] = record
	}
	want, err := probeLibopusSILKGain(libopusSILKGainModeDequant, records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk gain", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prev := tc.prev
			ind := tc.ind
			var gains [maxNbSubfr]int32
			silkGainsDequant(&gains, &ind, &prev, tc.conditional, tc.nbSubfr)
			if int32(prev) != want[i].first {
				t.Fatalf("prev=%d want %d", prev, want[i].first)
			}
			for j := 0; j < maxNbSubfr; j++ {
				if gains[j] != want[i].gains[j] {
					t.Fatalf("gain[%d]=%d want %d", j, gains[j], want[i].gains[j])
				}
			}
		})
	}
}

func TestSILKGainsIDMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name    string
		nbSubfr int
		ind     [maxNbSubfr]int8
	}{
		{name: "two", nbSubfr: 2, ind: [maxNbSubfr]int8{18, 6, 0, 0}},
		{name: "four", nbSubfr: 4, ind: [maxNbSubfr]int8{18, 6, 5, 4}},
		{name: "negative_delta_vector", nbSubfr: 4, ind: [maxNbSubfr]int8{-1, 0, 2, -3}},
	}

	records := make([][]int32, len(cases))
	for i, tc := range cases {
		record := []int32{int32(tc.nbSubfr)}
		for _, ind := range tc.ind {
			record = append(record, int32(ind))
		}
		records[i] = record
	}
	want, err := probeLibopusSILKGain(libopusSILKGainModeID, records)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk gain", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := silkGainsID(tc.ind[:], tc.nbSubfr)
			if got != want[i].first {
				t.Fatalf("silkGainsID=%d want %d", got, want[i].first)
			}
		})
	}
}
