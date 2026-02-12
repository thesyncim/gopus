//go:build !gopus_tmp_env

package encoder

// Production/default build: compile out temporary analysis debug logging.
func maybeLogAnalysisDebug(_ int, _ AnalysisInfo) {}
