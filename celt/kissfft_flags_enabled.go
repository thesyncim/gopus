//go:build gopus_tmp_env

package celt

import "runtime"

var kissFFTM1FastPathEnabled = tmpGetenv("GOPUS_TMP_KISSFFT_DISABLE_M1_FASTPATHS") != "1"

var kissFFTNoFMAMulEnabled = tmpGetenv("GOPUS_TMP_KISSFFT_NOFMA_MUL") == "1"

var kissFFTFMALikeEnabled = func() bool {
	if v, ok := tmpLookupEnv("GOPUS_TMP_KISSFFT_FMALIKE"); ok {
		return v == "1"
	}
	return runtime.GOARCH == "arm64"
}()

var kissFFTDFTFallbackEnabled = tmpGetenv("GOPUS_TMP_KISSFFT_DFT") == "1"
