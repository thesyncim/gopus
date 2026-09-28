//go:build gopus_custom_modes

package celt

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
)

// These oracles exercise the float CELT primitives in this package. The
// gopus_fixed_point tag selects a separate integer codec; it does not change
// these functions' float32 arithmetic. Their C kernels therefore use float
// custom modes, with matching QEXT and CPU settings. Public custom codec
// sequences use BuildPublicAPIHelper and the selected integer or float C codec.
func buildCustomFloatKernelHelper(cfg libopustest.CHelperConfig) (string, error) {
	var archive string
	if extsupport.QEXT {
		cfg.CustomQEXTRef = true
		archive = libopustest.CustomQEXTRefPath(".libs", "libopus.a")
	} else {
		cfg.CustomRef = true
		archive = libopustest.CustomRefPath(".libs", "libopus.a")
	}
	cfg.Libs = append([]string{archive}, cfg.Libs...)
	return libopustest.BuildCHelper(cfg)
}
