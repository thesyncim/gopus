//go:build gopus_tmp_env

package celt

import "runtime"

var mdctUseNativeMulEnabled = tmpGetenv("GOPUS_TMP_MDCT_NATIVE_MUL") == "1"

var mdctUseF64MixEnabled = tmpGetenv("GOPUS_TMP_MDCT_MIX_F64") == "1"

var mdctUseFMALikeMixEnabled = func() bool {
	if v, ok := tmpLookupEnv("GOPUS_TMP_MDCT_FMALIKE"); ok {
		return v == "1"
	}
	return runtime.GOARCH == "arm64"
}()
