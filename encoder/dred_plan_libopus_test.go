//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package encoder

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestDREDBitsTableMatchesLibopusReference(t *testing.T) {
	want := readLibopusDREDBitsTable(t)
	if len(want) != len(dredBitsTable) {
		t.Fatalf("dredBitsTable len=%d want %d", len(dredBitsTable), len(want))
	}
	for i, got := range dredBitsTable {
		if got != want[i] {
			t.Fatalf("dredBitsTable[%d]=%g want %g", i, got, want[i])
		}
	}
}

func readLibopusDREDBitsTable(t *testing.T) []float64 {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
	path := filepath.Join(repoRoot, "tmp_check", "opus-1.6.1", "src", "opus_encoder.c")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read libopus opus_encoder.c: %v", err)
	}

	re := regexp.MustCompile(`(?s)static\s+const\s+float\s+dred_bits_table\[16\]\s*=\s*\{([^}]*)\}`)
	m := re.FindSubmatch(data)
	if m == nil {
		t.Fatal("libopus dred_bits_table not found")
	}
	fields := strings.Split(string(m[1]), ",")
	values := make([]float64, 0, len(fields))
	for _, field := range fields {
		value := strings.TrimSpace(field)
		value = strings.TrimSuffix(value, "f")
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			t.Fatalf("parse libopus dred_bits_table value %q: %v", field, err)
		}
		values = append(values, parsed)
	}
	return values
}
