//go:build !gopus_fixed_point && gopus_qext && !gopus_dred

package multistream

import "github.com/thesyncim/gopus/internal/libopustest"

func multistreamReferenceArchive() string {
	return libopustest.QEXTRefPath(".libs", "libopus.a")
}

func multistreamReferenceFlags(cfg *libopustest.CHelperConfig) { cfg.QEXTRef = true }
func multistreamReferenceUsesQEXT() bool                       { return true }
func multistreamReferenceUsesDREDQEXT() bool                   { return false }
func multistreamReferenceUsesFixed() bool                      { return false }
func multistreamReferenceUsesFixedQEXT() bool                  { return false }
