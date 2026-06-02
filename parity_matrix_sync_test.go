package gopus

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestParityMatrixDocsStayInSyncWithFixtureCoverage guards PARITY_MATRIX.md
// against silent drift when parity fixtures or oracles move forward.
func TestParityMatrixDocsStayInSyncWithFixtureCoverage(t *testing.T) {
	doc := mustReadDocForTest(t, "PARITY_MATRIX.md")

	for _, needle := range []string{
		"bit-exact",
		"Hybrid QEXT",
	} {
		if !containsDocText(doc, needle) {
			t.Fatalf("PARITY_MATRIX.md missing %q (update matrix when closing parity gaps)", needle)
		}
	}

	if strings.Contains(doc, "Fixture stale") {
		t.Fatal("PARITY_MATRIX.md still documents opusdec crossval as stale; refresh the matrix after regenerating the fixture")
	}

	data, err := os.ReadFile("testvectors/testdata/libopus_decoder_matrix_fixture.json")
	if err != nil {
		t.Fatalf("read decoder matrix fixture: %v", err)
	}
	var fixture struct {
		Cases []struct {
			Name string `json:"name"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode decoder matrix fixture: %v", err)
	}
	requiredCases := []string{
		"celt-fb-60ms-mono-64k",
		"silk-wb-80ms-mono-32k",
		"celt-fb-80ms-mono-64k",
		"silk-wb-120ms-mono-32k",
	}
	names := make(map[string]struct{}, len(fixture.Cases))
	for _, c := range fixture.Cases {
		names[c.Name] = struct{}{}
	}
	for _, want := range requiredCases {
		if _, ok := names[want]; !ok {
			t.Fatalf("decoder matrix fixture missing %q; update PARITY_MATRIX.md after regenerating the fixture", want)
		}
	}
	if len(fixture.Cases) < 26 {
		t.Fatalf("decoder matrix fixture has %d cases, want at least 26", len(fixture.Cases))
	}
}
