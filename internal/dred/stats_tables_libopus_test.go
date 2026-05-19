package dred

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

func TestDREDStatsTablesMatchLibopusReference(t *testing.T) {
	source := readLibopusDREDStatsSource(t)
	for _, tc := range []struct {
		name string
		got  []uint8
	}{
		{name: "dred_latent_quant_scales_q8", got: dredLatentQuantScalesQ8[:]},
		{name: "dred_latent_dead_zone_q8", got: dredLatentDeadZoneQ8[:]},
		{name: "dred_latent_r_q8", got: dredLatentRQ8[:]},
		{name: "dred_latent_p0_q8", got: dredLatentP0Q8[:]},
		{name: "dred_state_quant_scales_q8", got: dredStateQuantScalesQ8[:]},
		{name: "dred_state_dead_zone_q8", got: dredStateDeadZoneQ8[:]},
		{name: "dred_state_r_q8", got: dredStateRQ8[:]},
		{name: "dred_state_p0_q8", got: dredStateP0Q8[:]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := parseLibopusUint8Array(t, source, tc.name)
			if len(tc.got) != len(want) {
				t.Fatalf("len=%d want %d", len(tc.got), len(want))
			}
			for i := range tc.got {
				if tc.got[i] != want[i] {
					t.Fatalf("[%d]=%d want %d", i, tc.got[i], want[i])
				}
			}
		})
	}
}

func readLibopusDREDStatsSource(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	path := filepath.Join(repoRoot, "tmp_check", "opus-1.6.1", "dnn", "dred_rdovae_stats_data.c")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read libopus DRED stats source: %v", err)
	}
	return string(data)
}

func parseLibopusUint8Array(t *testing.T, source, name string) []uint8 {
	t.Helper()
	pattern := `(?s)const\s+opus_uint8\s+` + regexp.QuoteMeta(name) + `\s*\[[^\]]+\]\s*=\s*\{(.*?)\};`
	match := regexp.MustCompile(pattern).FindStringSubmatch(source)
	if match == nil {
		t.Fatalf("array %s not found", name)
	}
	fields := strings.FieldsFunc(match[1], func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
	values := make([]uint8, 0, len(fields))
	for _, field := range fields {
		if field == "" {
			continue
		}
		value, err := strconv.Atoi(field)
		if err != nil || value < 0 || value > 255 {
			t.Fatalf("array %s has invalid uint8 value %q", name, field)
		}
		values = append(values, uint8(value))
	}
	return values
}
