//go:build gopus_tmp_env

package encoder

import (
	"os"
	"strconv"
)

var (
	tmpHybridHBDebugEnabled     = os.Getenv("GOPUS_TMP_HYB_HB_DBG") == "1"
	tmpHybridHBDebugFrame       = parseTmpHybridDebugInt("GOPUS_TMP_HYB_HB_FRAME", -1)
	tmpHybridMDCTInDebugEnabled = os.Getenv("GOPUS_TMP_HYB_MDCT_IN_DBG") == "1"
	tmpHybridMDCTDebugEnabled   = os.Getenv("GOPUS_TMP_HYB_MDCT_DBG") == "1"
	tmpHybridMDCTCall           = parseTmpHybridDebugInt("GOPUS_TMP_HYB_MDCT_CALL", -1)
	tmpHybridAMPDebugEnabled    = os.Getenv("GOPUS_TMP_HYB_AMP_DBG") == "1"
	tmpPrefillRNGDebugEnabled   = os.Getenv("GOPUS_TMP_PREFILL_RNG_DBG") == "1"
)

func parseTmpHybridDebugInt(name string, fallback int) int {
	if s := os.Getenv(name); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			return v
		}
	}
	return fallback
}
