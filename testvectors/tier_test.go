package testvectors

import (
	"os"
	"strings"
	"testing"
)

const (
	testTierFast       = "fast"
	testTierParity     = "parity"
	testTierExhaustive = "exhaustive"
)

func currentTestTier(short bool) string {
	if short {
		return testTierFast
	}
	v := strings.TrimSpace(strings.ToLower(os.Getenv("GOPUS_TEST_TIER")))
	switch v {
	case "", testTierParity:
		return testTierParity
	case testTierFast:
		return testTierFast
	case testTierExhaustive:
		return testTierExhaustive
	default:
		return testTierParity
	}
}

func requireTestTier(t *testing.T, minTier string) {
	t.Helper()
	cur := currentTestTier(testing.Short())
	rank := map[string]int{
		testTierFast:       1,
		testTierParity:     2,
		testTierExhaustive: 3,
	}
	if rank[cur] >= rank[minTier] {
		return
	}
	t.Skipf("requires %s tier (current=%s)", minTier, cur)
}

func strictLibopusExactnessEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("GOPUS_LIBOPUS_EXACTNESS")))
	return v == "1" || v == "true" || v == "yes"
}

func requireLibopusExactness(t *testing.T) {
	t.Helper()
	if strictLibopusExactnessEnabled() {
		return
	}
	t.Skip("requires GOPUS_LIBOPUS_EXACTNESS=1")
}

func requireStrictLibopusReference(t *testing.T) {
	t.Helper()
	if strictLibopusReferenceRequired() {
		return
	}
	t.Skip("requires GOPUS_STRICT_LIBOPUS_REF=1")
}
