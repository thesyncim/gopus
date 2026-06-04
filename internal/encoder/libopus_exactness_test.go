package encoder

import (
	"os"
	"strings"
	"testing"
)

func requireLibopusExactness(t *testing.T) {
	t.Helper()
	v := strings.TrimSpace(strings.ToLower(os.Getenv("GOPUS_LIBOPUS_EXACTNESS")))
	if v == "1" || v == "true" || v == "yes" {
		return
	}
	t.Skip("requires GOPUS_LIBOPUS_EXACTNESS=1")
}
