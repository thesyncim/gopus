//go:build gopus_qext && !gopus_dred

package celt

import "github.com/thesyncim/gopus/internal/libopustest"

var qextPLCPublicOracleHelper libopustest.HelperCache

func buildQEXTPLCPublicOracleHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:      "QEXT CELT public PLC",
		OutputBase: "gopus_qext_celt_public_plc",
		SourceFile: "libopus_qext_celt_plc_public_info.c",
		CFlags:     []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		QEXTRef:    true,
		Libs:       []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:  true,
	})
}
