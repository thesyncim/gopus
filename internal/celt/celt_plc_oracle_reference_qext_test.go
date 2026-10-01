//go:build gopus_qext && !gopus_dred

package celt

import "github.com/thesyncim/gopus/internal/libopustest"

func configureCELTOracleReference(cfg *libopustest.CHelperConfig) {
	cfg.QEXTRef = true
	cfg.Libs = []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"}
}
