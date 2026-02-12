//go:build gopus_tmp_env

package encoder

import (
	"fmt"
	"os"
)

// Debug/tuning build: opt in to env-driven temporary analysis logging.
// Use only for local investigation (build with -tags gopus_tmp_env).
func maybeLogAnalysisDebug(frame int, info AnalysisInfo) {
	if os.Getenv("GOPUS_TMP_ANALYSISDBG") != "1" {
		return
	}
	if frame < 45 || frame > 80 {
		return
	}
	fmt.Fprintf(os.Stderr, "ANDBG frame=%d tonality=%.6f slope=%.6f music=%.6f vad=%.6f bw=%d\n",
		frame,
		info.Tonality,
		info.TonalitySlope,
		info.MusicProb,
		info.VADProb,
		info.BandwidthIndex,
	)
}
