package celt

import (
	"fmt"
	"math"
	"slices"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestPatchTransientHistoryStrideMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	type testCase struct {
		name                   string
		bands, physical, coded int
		current, history       []celtGLog
	}
	var cases []testCase
	for _, bands := range []int{13, 17, 19, 21} {
		for _, layout := range [][2]int{{1, 1}, {2, 1}, {2, 2}} {
			for _, signal := range []string{"below", "at", "above", "right-history", "right-rise", "shaped"} {
				tc := testCase{name: fmt.Sprintf("bands%d-physical%d-coded%d-%s", bands, layout[0], layout[1], signal), bands: bands, physical: layout[0], coded: layout[1], current: make([]celtGLog, bands*layout[1]), history: make([]celtGLog, MaxBands*layout[0])}
				for i := range tc.history {
					tc.history[i] = -90
				}
				for c := range tc.coded {
					for b := range bands {
						old, cur := float32(0), float32(1)
						switch signal {
						case "below":
							cur = math.Nextafter32(1, 0)
						case "above":
							cur = math.Nextafter32(1, 2)
						case "right-history":
							cur = 4
							if c == 1 {
								old = 4
							}
						case "right-rise":
							if c == 1 {
								cur = 2.125
							} else {
								cur = 0
							}
						case "shaped":
							old = float32((b*7+c*11)%17) * .125
							cur = float32((b*13+c*3)%23) * .25
						}
						tc.current[c*bands+b] = cur
						tc.history[c*MaxBands+b] = old
					}
				}
				cases = append(cases, tc)
			}
		}
	}
	payload := libopustest.NewOraclePayload("GPTI", uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(MaxBands)
		payload.U32(0)
		payload.U32(uint32(tc.bands))
		payload.U32(uint32(tc.coded))
		for c := range tc.coded {
			payload.Float32s(tc.current[c*tc.bands : (c+1)*tc.bands]...)
			for i := tc.bands; i < MaxBands; i++ {
				payload.Float32s(-90)
			}
		}
		payload.Float32s(tc.history[:MaxBands*tc.coded]...)
	}
	bin, err := libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label: "CELT transient history stride", OutputBase: "gopus_libopus_patch_transient", SourceFile: "libopus_patch_transient_info.c",
		CFlags: []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"}, RefIncludes: []string{"celt", "silk", "src"},
		Libs: []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"}, DeadStrip: true,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "transient history stride", err)
		return
	}
	reader, err := libopustest.RunOracle(bin, payload.Bytes(), "transient history stride", "GPTO")
	if err != nil {
		t.Fatal(err)
	}
	if n := reader.Count(len(cases)); n != len(cases) {
		t.Fatalf("C count=%d want%d", n, len(cases))
	}
	for _, tc := range cases {
		expected := reader.U32() != 0
		t.Run(tc.name, func(t *testing.T) {
			initialCurrent, initialHistory := slices.Clone(tc.current), slices.Clone(tc.history)
			var scratch [MaxBands]celtGLog
			call := func() bool {
				return PatchTransientDecisionWithScratch(tc.current, tc.history, tc.bands, MaxBands, 0, tc.bands, tc.coded, scratch[:])
			}
			if got := call(); got != expected {
				t.Fatalf("transient Go=%t C=%t", got, expected)
			}
			if !slices.Equal(tc.current, initialCurrent) || !slices.Equal(tc.history, initialHistory) {
				t.Fatal("transient patch mutates energy input/history")
			}
			if allocs := testing.AllocsPerRun(20, func() {
				if call() != expected {
					panic("transient result changes")
				}
			}); allocs != 0 {
				t.Fatalf("warm allocations=%g", allocs)
			}
		})
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
}
