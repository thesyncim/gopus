package silk

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKGainInputMagic  = "GSGI"
	libopusSILKGainOutputMagic = "GSGO"

	libopusSILKGainModeQuant   = uint32(0)
	libopusSILKGainModeDequant = uint32(1)
	libopusSILKGainModeID      = uint32(2)
)

var libopusSILKGainHelper libopustest.HelperCache

type libopusSILKGainRecord struct {
	first int32
	ind   [maxNbSubfr]int8
	gains [maxNbSubfr]int32
}

func buildLibopusSILKGainHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:        "silk gain",
		OutputBase:   "gopus_libopus_silk_gain",
		SourceFile:   "libopus_silk_gain_info.c",
		ProbeRelPath: "silk/main.h",
		CFlags:       []string{"-DHAVE_CONFIG_H"},
		RefIncludes:  []string{"celt", "silk"},
		RefSources:   []string{"silk/gain_quant.c", "silk/lin2log.c", "silk/log2lin.c"},
	})
}

func getLibopusSILKGainHelperPath() (string, error) {
	return libopusSILKGainHelper.Path(buildLibopusSILKGainHelper)
}

func probeLibopusSILKGain(mode uint32, records [][]int32) ([]libopusSILKGainRecord, error) {
	binPath, err := getLibopusSILKGainHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKGainInputMagic, mode, uint32(len(records)))
	for _, record := range records {
		for _, word := range record {
			payload.I32(word)
		}
	}

	data, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return nil, fmt.Errorf("run silk gain helper: %w", err)
	}
	reader, err := libopustest.NewOracleReader("silk gain", libopusSILKGainOutputMagic, data)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(records))
	reader.ExpectRemaining(count * 36)
	out := make([]libopusSILKGainRecord, count)
	for i := range out {
		out[i].first = reader.I32()
		for j := 0; j < maxNbSubfr; j++ {
			out[i].ind[j] = int8(reader.I32())
		}
		for j := 0; j < maxNbSubfr; j++ {
			out[i].gains[j] = reader.I32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
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
		record := []int32{int32(tc.nbSubfr), int32(tc.prev), boolWord(tc.conditional)}
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
		record := []int32{int32(tc.nbSubfr), int32(tc.prev), boolWord(tc.conditional)}
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

func boolWord(v bool) int32 {
	if v {
		return 1
	}
	return 0
}
