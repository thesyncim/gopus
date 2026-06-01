package testvectors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParityMatrixFreshness guards PARITY_MATRIX.md against claiming a gap that
// the code has already closed. Each entry pairs a shipped capability with a
// phrase that must NOT appear in the matrix because it asserts the gap is still
// open. When a parity-closing change lands, the matrix must be updated in the
// same change or this test fails. This is the doc-contract counterpart that keeps
// the public status doc honest as features ship.
func TestParityMatrixFreshness(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "PARITY_MATRIX.md"))
	if err != nil {
		t.Fatalf("read PARITY_MATRIX.md: %v", err)
	}
	matrix := string(data)

	// Phrases that contradict shipped reality and must be removed when the
	// corresponding capability lands.
	retired := []struct {
		shipped string
		phrase  string
	}{
		{"public container/red RED API", "No public `gopus` RED parse/recover API"},
		{"per-rate decoder matrix (8/12/16/24 kHz)", "Decoder matrix is 48 kHz only"},
		{"multi-frame/stereo DTX sequence parity", "Multi-frame DTX TOC sequences; stereo DTX"},
		{"classical PLC IIR edge oracles", "Periodic PLC IIR edge cases"},
		{"mono-first/stereo-warm LBRR byte parity", "Mono first packets and stereo warm LBRR packet byte-exact"},
		{"auto-mode cross-product parity", "Full cross-product of application × rate × frame × signal class"},
	}
	for _, r := range retired {
		if strings.Contains(matrix, r.phrase) {
			t.Errorf("PARITY_MATRIX.md is stale: %s ships, but the matrix still claims the gap: %q", r.shipped, r.phrase)
		}
	}

	// Capabilities that ship and must be reflected somewhere in the matrix.
	mustMention := []struct {
		capability string
		token      string
	}{
		{"public RED package", "container/red"},
		{"int24 decode API", "DecodeInt24"},
		{"build-config matrix gate", "build-config matrix"},
		{"libopus 1.6 feature-scope section", "feature scope"},
	}
	for _, m := range mustMention {
		if !strings.Contains(matrix, m.token) {
			t.Errorf("PARITY_MATRIX.md omits shipped %s (expected mention of %q)", m.capability, m.token)
		}
	}
}
