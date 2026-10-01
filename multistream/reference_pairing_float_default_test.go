//go:build !gopus_fixed_point && !gopus_qext

package multistream

import "github.com/thesyncim/gopus/internal/libopustest"

func multistreamReferenceArchive() string {
	return libopustest.RefPath(".libs", "libopus.a")
}

func multistreamReferenceFlags(*libopustest.CHelperConfig) {}
func multistreamReferenceUsesQEXT() bool                   { return false }
func multistreamReferenceUsesDREDQEXT() bool               { return false }
func multistreamReferenceUsesFixed() bool                  { return false }
func multistreamReferenceUsesFixedQEXT() bool              { return false }
