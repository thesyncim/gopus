//go:build gopus_qext && gopus_dred

package celt

import "github.com/thesyncim/gopus/internal/libopustest"

func configureCELTOracleReference(cfg *libopustest.CHelperConfig) {
	cfg.DREDQEXTRef = true
	cfg.RefIncludes = append(cfg.RefIncludes, "dnn")
	cfg.Libs = []string{libopustest.DREDQEXTRefPath(".libs", "libopus.a"), "-lm"}
}
