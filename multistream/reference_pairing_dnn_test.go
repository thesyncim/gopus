//go:build gopus_dred || gopus_osce

package multistream

import "github.com/thesyncim/gopus/internal/libopustest"

func buildMultistreamReferenceHelper(cfg libopustest.CHelperConfig) (string, error) {
	return libopustest.BuildDNNCHelper("", cfg)
}
