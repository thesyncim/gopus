//go:build !gopus_qext

package celt

import "github.com/thesyncim/gopus/internal/libopustest"

func configureCELTOracleReference(cfg *libopustest.CHelperConfig) {
	cfg.Libs = []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"}
}
