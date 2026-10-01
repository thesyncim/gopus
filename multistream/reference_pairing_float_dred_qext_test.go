//go:build !gopus_fixed_point && gopus_qext && gopus_dred

package multistream

import "github.com/thesyncim/gopus/internal/libopustest"

func multistreamReferenceArchive() string {
	return libopustest.DREDQEXTRefPath(".libs", "libopus.a")
}

func multistreamReferenceFlags(cfg *libopustest.CHelperConfig) {
	cfg.DREDQEXTRef = true
	cfg.RefIncludes = append(cfg.RefIncludes, "dnn")
}
func multistreamReferenceUsesQEXT() bool      { return false }
func multistreamReferenceUsesDREDQEXT() bool  { return true }
func multistreamReferenceUsesFixed() bool     { return false }
func multistreamReferenceUsesFixedQEXT() bool { return false }
