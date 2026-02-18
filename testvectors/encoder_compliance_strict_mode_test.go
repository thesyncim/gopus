package testvectors

import (
	"strings"
	"testing"
)

func TestDecodeCompliancePackets_StrictModeRequiresLibopusReferenceDecode(t *testing.T) {
	t.Setenv("GOPUS_STRICT_LIBOPUS_REF", "1")
	t.Setenv("GOPUS_DISABLE_OPUSDEC", "1")

	_, err := decodeCompliancePackets([][]byte{{0xff}}, 1, 960)
	if err == nil {
		t.Fatal("expected strict libopus reference decode error, got nil")
	}
	if !strings.Contains(err.Error(), "strict libopus reference decode required") {
		t.Fatalf("unexpected error: %v", err)
	}
}
