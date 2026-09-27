//go:build !gopus_fixed_point

package gopus

import "github.com/thesyncim/gopus/internal/libopustest"

func apiRateReferenceHelperPath() (string, error) {
	return libopusAPIRateRefdecodeHelper.CHelperPath(libopustest.CHelperConfig{
		Label:      "api-rate reference decode",
		OutputBase: "gopus_libopus_refdecode_api_rate",
		SourceFile: "libopus_refdecode_single.c",
		CFlags:     []string{"-O3", "-DNDEBUG"},
		Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
	})
}
