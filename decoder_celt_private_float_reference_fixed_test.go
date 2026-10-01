//go:build gopus_fixed_point

package gopus

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
)

var privateFloatCELTAPIRateRefdecodeHelper libopustest.HelperCache

// Fixed public builds still compile this package-private CELT decoder with
// float32 state. Select a float archive with matching optional CELT features
// rather than comparing it with the public FIXED_POINT decoder.
func privateFloatCELTAPIRateReferenceHelperPath() (string, error) {
	return privateFloatCELTAPIRateRefdecodeHelper.Path(func() (string, error) {
		cfg := libopustest.CHelperConfig{
			Label:      "private float CELT API-rate reference decode",
			OutputBase: "gopus_libopus_refdecode_private_float_api_rate",
			SourceFile: "libopus_refdecode_single.c",
			CFlags:     []string{"-O3", "-DNDEBUG"},
		}
		switch {
		case privateFloatCELTCustomModesEnabled && extsupport.QEXT:
			cfg.CustomQEXTRef = true
			cfg.Libs = []string{libopustest.CustomQEXTRefPath(".libs", "libopus.a"), "-lm"}
		case privateFloatCELTCustomModesEnabled:
			cfg.CustomRef = true
			cfg.Libs = []string{libopustest.CustomRefPath(".libs", "libopus.a"), "-lm"}
		case extsupport.QEXT:
			cfg.QEXTRef = true
			cfg.Libs = []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"}
		default:
			cfg.Libs = []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"}
		}
		return libopustest.BuildCHelper(cfg)
	})
}
