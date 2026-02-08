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
