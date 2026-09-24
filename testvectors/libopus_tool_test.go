package testvectors

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

func requireFixtureOpusDemo(t testing.TB) string {
	t.Helper()
	path, err := libopustooling.FindOrEnsureOpusDemo(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots())
	if err != nil {
		libopustest.HelperUnavailable(t, "opus_demo", err)
		return ""
	}
	return path
}
