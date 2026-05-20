package rdovae

import (
	"os"
	"strings"
	"testing"
)

func readLibopusRefFileOrSkip(t *testing.T, path, label string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err == nil {
		return data
	}
	if os.IsNotExist(err) && !strictLibopusRefRequired() {
		t.Skipf("libopus %s reference unavailable: %v", label, err)
	}
	t.Fatalf("read libopus %s reference: %v", label, err)
	return nil
}

func strictLibopusRefRequired() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("GOPUS_STRICT_LIBOPUS_REF")))
	return v == "1" || v == "true" || v == "yes"
}
