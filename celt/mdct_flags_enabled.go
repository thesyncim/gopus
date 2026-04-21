//go:build gopus_tmp_env

package celt

var mdctUseNativeMulEnabled = tmpGetenv("GOPUS_TMP_MDCT_NATIVE_MUL") == "1"

var mdctUseF64MixEnabled = tmpGetenv("GOPUS_TMP_MDCT_MIX_F64") == "1"

var mdctUseFMALikeMixEnabled = func() bool {
	if v, ok := tmpLookupEnv("GOPUS_TMP_MDCT_FMALIKE"); ok {
		return v == "1"
	}
	return false
}()

var mdctUseNativeMulShort240Enabled = tmpGetenv("GOPUS_TMP_MDCT_NATIVE_MUL_SHORT240") == "1"

var mdctUseFMALikeMixShort240Enabled = func() bool {
	if v, ok := tmpLookupEnv("GOPUS_TMP_MDCT_FMALIKE_SHORT240"); ok {
		return v == "1"
	}
	return false
}()
