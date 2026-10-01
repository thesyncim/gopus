package testvectors

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

func requirePairedOpusDemo(t testing.TB) string {
	t.Helper()
	path, err := libopustooling.FindOrEnsureOpusDemo(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots())
	if err != nil {
		libopustest.HelperUnavailable(t, "opus_demo", err)
		return ""
	}
	return path
}

func requireDefaultFixtureProducerOpusDemo(t testing.TB) string {
	t.Helper()
	path, err := libopustooling.FindOrEnsureDefaultOpusDemo(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots())
	if err != nil {
		libopustest.HelperUnavailable(t, "default fixture producer opus_demo", err)
		return ""
	}
	return path
}
