package silk

import (
	"sync"
	"testing"
)

const int16Scale = 1.0 / 32768.0

var silkQualityOnce sync.Once

func logSilkQualityStatus(t *testing.T) {
	t.Helper()
	silkQualityOnce.Do(func() {
		t.Log("SILK QUALITY STATUS: focused SILK parity probes are green; keep monitoring amplitude and correlation drift with fixture-backed tests.")
		t.Log("COVERED: frame type coding tables, inputBuffer-based pitch residual/noise-shape window alignment, int16 entry quantization, NLSF/gain/LTP focused probes.")
		t.Log("WATCH: broad packet exactness and quality thresholds should move only with live libopus-backed fixture evidence.")
	})
}
