//go:build gopus_fixed_point && !gopus_qext

package multistream

import "github.com/thesyncim/gopus/internal/libopustest"

func multistreamReferenceArchive() string {
	return libopustest.FixedRefPath(".libs", "libopus.a")
}

func multistreamReferenceFlags(cfg *libopustest.CHelperConfig) { cfg.FixedRef = true }
func multistreamReferenceUsesQEXT() bool                       { return false }
func multistreamReferenceUsesDREDQEXT() bool                   { return false }
func multistreamReferenceUsesFixed() bool                      { return true }
func multistreamReferenceUsesFixedQEXT() bool                  { return false }
